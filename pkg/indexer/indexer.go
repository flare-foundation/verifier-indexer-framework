package indexer

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type BlockchainClient[B database.Block, T database.Transaction] interface {
	GetLatestBlockInfo(context.Context) (*BlockInfo, error)
	GetBlockResult(context.Context, uint64) (*BlockResult[B, T], error)
	GetBlockTimestamp(context.Context, uint64) (uint64, error)
}

type BlockInfo struct {
	BlockNumber uint64
	Timestamp   uint64
}

type iterationResult[B database.Block, T database.Transaction] struct {
	blockResults []BlockResult[B, T]
	state        *database.State
}

type BlockResult[B database.Block, T database.Transaction] struct {
	Block        B
	Transactions []T
}

func New[B database.Block, T database.Transaction](
	cfg *config.BaseConfig, db *database.DB[B, T], blockchain BlockchainClient[B, T],
) Indexer[B, T] {
	backoffMaxElapsedTime := time.Duration(cfg.Timeout.BackoffMaxElapsedTimeSeconds) * time.Second

	return Indexer[B, T]{
		blockchain: newBlockchainWithBackoff(
			blockchain, backoffMaxElapsedTime, time.Duration(cfg.Timeout.RequestTimeoutMillis)*time.Millisecond,
		),
		confirmations:         cfg.Indexer.Confirmations,
		db:                    db,
		maxBlockRange:         cfg.Indexer.MaxBlockRange,
		maxConcurrency:        cfg.Indexer.MaxConcurrency,
		startBlockNumber:      cfg.Indexer.StartBlockNumber,
		endBlockNumber:        cfg.Indexer.EndBlockNumber,
		historyDropInterval:   cfg.DB.HistoryDrop,
		backoffMaxElapsedTime: backoffMaxElapsedTime,
	}
}

type Indexer[B database.Block, T database.Transaction] struct {
	blockchain            BlockchainClient[B, T]
	confirmations         uint64
	db                    *database.DB[B, T]
	maxBlockRange         uint64
	maxConcurrency        int
	startBlockNumber      uint64
	endBlockNumber        uint64
	historyDropInterval   uint64
	lastHistoryDropRun    time.Time
	backoffMaxElapsedTime time.Duration
}

func (ix *Indexer[B, T]) Run(ctx context.Context) error {
	upToDateBackoff := backoff.NewExponentialBackOff()

	state, err := ix.db.GetState(ctx)
	if err != nil {
		return err
	}

	if err := ix.initialSetup(ctx); err != nil {
		return err
	}

	for {
		err := backoff.RetryNotify(
			func() error {
				newState, err := ix.updateChainState(ctx, state)
				if err != nil {
					return err
				}

				state = newState
				return nil
			},
			ix.newBackoff(),
			func(err error, d time.Duration) {
				logger.Errorf("indexer update chain state error: %v. Will retry after %v", err, d)
			},
		)
		if err != nil {
			return errors.Wrap(err, "fatal error in indexer")
		}

		if ix.shouldRunHistoryDrop(state) {
			err := backoff.RetryNotify(
				func() error {
					newState, err := ix.runHistoryDrop(ctx, state)
					if err != nil {
						return err
					}

					state = newState
					ix.lastHistoryDropRun = time.Now()
					return nil
				},
				ix.newBackoff(),
				func(err error, d time.Duration) {
					logger.Errorf("indexer history drop error: %v. Will retry after %v", err, d)
				},
			)
			if err != nil {
				return errors.Wrap(err, "fatal error in indexer")
			}
		}

		err = backoff.RetryNotify(
			func() error {
				results, err := ix.runIteration(ctx, state)
				if err != nil {
					return err
				}

				if results == nil {
					time.Sleep(upToDateBackoff.NextBackOff())
					return nil
				}

				upToDateBackoff.Reset()

				err = ix.saveData(ctx, results)
				if err != nil {
					return err
				}

				logger.Infof("successfully processed up to block %d", results.state.LastIndexedBlockNumber)
				state = results.state

				return nil
			},
			ix.newBackoff(),
			func(err error, d time.Duration) {
				logger.Errorf("indexer iteration error: %v. Will retry after %v", err, d)
			},
		)
		if err != nil {
			return errors.Wrap(err, "fatal error in indexer")
		}

		if ix.endBlockNumber != 0 && ix.endBlockNumber <= state.LastIndexedBlockNumber {
			return nil
		}
	}
}

func (ix *Indexer[B, T]) initialSetup(ctx context.Context) error {
	if ix.historyDropInterval > 0 {
		// if the starting block number is set below the interval that gets dropped by history, fix it
		newStartBlockNumber, err := ix.getMinBlockWithinHistoryInterval(ctx)
		if err != nil {
			return err
		}

		ix.startBlockNumber = newStartBlockNumber
		logger.Infof("new starting block number set to %d due to history drop", ix.startBlockNumber)
	}

	return nil
}

func (ix *Indexer[B, T]) runIteration(
	ctx context.Context, state *database.State,
) (*iterationResult[B, T], error) {
	blkRange, err := ix.getBlockRange(state)
	if err != nil {
		return nil, err
	}

	if blkRange.len() == 0 {
		return nil, nil
	}

	logger.Debugf(
		"indexing from block %d to %d, latest block on chain %d",
		blkRange.start, blkRange.end-1, state.LastChainBlockNumber,
	)

	blockResults, err := ix.getBlockResults(ctx, blkRange)
	if err != nil {
		return nil, err
	}

	newState := updateState(blockResults, state)

	return &iterationResult[B, T]{
		blockResults: blockResults,
		state:        newState,
	}, nil
}

type blockRange struct {
	start uint64
	end   uint64
}

func (br blockRange) len() uint64 {
	return br.end - br.start
}

func (ix *Indexer[B, T]) getBlockRange(state *database.State) (*blockRange, error) {
	result := new(blockRange)
	result.start = ix.getStartBlock(state)
	result.end = ix.getEndBlock(state, result.start)

	return result, nil
}

func (ix *Indexer[B, T]) getStartBlock(state *database.State) uint64 {
	if state == nil {
		return ix.startBlockNumber
	}

	if state.LastIndexedBlockNumber < ix.startBlockNumber {
		return ix.startBlockNumber
	}

	return state.LastIndexedBlockNumber + 1
}

func (ix *Indexer[B, T]) getEndBlock(state *database.State, start uint64) uint64 {
	latestConfirmedNum := state.LastChainBlockNumber - ix.confirmations
	if latestConfirmedNum < start {
		return latestConfirmedNum + 1
	}

	numBlocks := latestConfirmedNum + 1 - start
	if numBlocks > ix.maxBlockRange {
		return start + ix.maxBlockRange
	}

	return latestConfirmedNum + 1
}

func (ix *Indexer[B, T]) getBlockResults(
	ctx context.Context, blkRange *blockRange,
) ([]BlockResult[B, T], error) {
	sem := make(chan struct{}, ix.maxConcurrency)
	eg, ctx := errgroup.WithContext(ctx)

	l := blkRange.len()
	if l < 1 {
		return nil, nil
	}

	results := make([]BlockResult[B, T], l)

	for i := blkRange.start; i < blkRange.end; i++ {
		blockNum := i
		eg.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()

			res, err := ix.blockchain.GetBlockResult(ctx, blockNum)
			if err != nil {
				return err
			}

			results[blockNum-blkRange.start] = *res
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

func (ix *Indexer[B, T]) saveData(ctx context.Context, results *iterationResult[B, T]) error {
	blocks := make([]*B, len(results.blockResults))
	var transactions []*T

	for i := range results.blockResults {
		blocks[i] = &results.blockResults[i].Block

		resTxs := results.blockResults[i].Transactions
		for j := range resTxs {
			transactions = append(transactions, &resTxs[j])
		}
	}

	logger.Debugf("fetched %d blocks with %d transactions from the chain", len(results.blockResults), len(transactions))

	err := ix.db.SaveAllEntities(ctx, blocks, transactions, results.state)
	if err != nil {
		return err
	}

	logger.Debug("data saved to the DB")

	return nil
}

func (ix *Indexer[B, T]) updateChainState(ctx context.Context, state *database.State) (*database.State, error) {
	blockInfo, err := ix.blockchain.GetLatestBlockInfo(ctx)
	if err != nil {
		return nil, err
	}

	newState := *state
	newState.LastChainBlockNumber = blockInfo.BlockNumber
	newState.LastChainBlockTimestamp = blockInfo.Timestamp

	return &newState, nil
}

func (ix *Indexer[B, T]) newBackoff() backoff.BackOff {
	return backoff.NewExponentialBackOff(backoff.WithMaxElapsedTime(ix.backoffMaxElapsedTime))
}

func updateState[B database.Block, T database.Transaction](
	results []BlockResult[B, T], state *database.State,
) *database.State {
	if len(results) == 0 {
		return state
	}

	newState := *state

	lastIndexedBlock := results[len(results)-1].Block
	newState.LastIndexedBlockNumber = lastIndexedBlock.GetBlockNumber()
	newState.LastIndexedBlockTimestamp = lastIndexedBlock.GetTimestamp()

	// handle first iteration
	if state.LastIndexedBlockNumber == 0 {
		firstIndexedBlock := results[0].Block
		newState.FirstIndexedBlockNumber = firstIndexedBlock.GetBlockNumber()
		newState.FirstIndexedBlockTimestamp = firstIndexedBlock.GetTimestamp()
	}

	return &newState
}
