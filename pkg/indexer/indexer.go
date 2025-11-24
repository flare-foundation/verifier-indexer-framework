package indexer

import (
	"context"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

type BlockchainClient[B database.Block, T database.Transaction, E database.Event] interface {
	GetLatestBlockInfo(context.Context) (*BlockInfo, error)
	GetBlockResult(context.Context, uint64) (*BlockResult[B, T, E], error)
	GetBlockTimestamp(context.Context, uint64) (uint64, error)
	GetServerInfo(context.Context) (string, error)
}

type BlockInfo struct {
	BlockNumber uint64
	Timestamp   uint64
}

type iterationResult[B database.Block, T database.Transaction, E database.Event] struct {
	blockResults []BlockResult[B, T, E]
	state        *database.State
}

type BlockResult[B database.Block, T database.Transaction, E database.Event] struct {
	Block        B
	Transactions []T
	Events       []E
}

func New[B database.Block, T database.Transaction, E database.Event](
	cfg *config.BaseConfig, db *database.DB[B, T, E], blockchain BlockchainClient[B, T, E],
) Indexer[B, T, E] {
	backoffMaxElapsedTime := time.Duration(cfg.Timeout.BackoffMaxElapsedTimeSeconds) * time.Second
	historyDropFrequency := cfg.DB.HistoryDropFrequency
	if historyDropFrequency == 0 {
		historyDropFrequency = cfg.DB.HistoryDrop
	}

	return Indexer[B, T, E]{
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
		historyDropFrequency:  historyDropFrequency,
		backoffMaxElapsedTime: backoffMaxElapsedTime,
	}
}

type Indexer[B database.Block, T database.Transaction, E database.Event] struct {
	blockchain            BlockchainClient[B, T, E]
	confirmations         uint64
	db                    *database.DB[B, T, E]
	maxBlockRange         uint64
	maxConcurrency        int
	startBlockNumber      uint64
	endBlockNumber        uint64
	historyDropInterval   uint64
	historyDropFrequency  uint64
	backoffMaxElapsedTime time.Duration
}

func (ix *Indexer[B, T, E]) Run(ctx context.Context) error {
	upToDateBackoff := backoff.NewExponentialBackOff()
	historyDropResults := make(chan *database.State, 1)
	var historyDropLock sync.Mutex

	state, err := ix.db.GetState(ctx)
	if err != nil {
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

		if err := ix.pollHistoryDropResults(ctx, &historyDropLock, historyDropResults, state); err != nil {
			return errors.Wrap(err, "pollHistoryDropResults failed")
		}

		ix.maybeRunHistoryDrop(ctx, &historyDropLock, historyDropResults, state)

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

func (ix *Indexer[B, T, E]) maybeRunHistoryDrop(
	ctx context.Context,
	historyDropLock *sync.Mutex,
	historyDropResults chan *database.State,
	state *database.State,
) {
	if !historyDropLock.TryLock() {
		// Another history drop is in progress
		return
	}

	if !ix.shouldRunHistoryDrop(state) {
		// Nothing to do so release the lock
		historyDropLock.Unlock()
		return
	}

	// Start the history drop in a separate goroutine.
	//
	// We pass a copy of the current state by value to avoid data races.
	//
	// Updates to the state will be applied when the results
	// are returned via the results channel.
	go func(state database.State) {
		var newState *database.State
		defer func() {
			historyDropResults <- newState
		}()

		err := backoff.RetryNotify(
			func() (err error) {
				newState, err = ix.runHistoryDrop(ctx, &state)
				return err
			},
			ix.newBackoff(),
			func(err error, d time.Duration) {
				logger.Errorf("indexer history drop error: %v. Will retry after %v", err, d)
			},
		)
		if err != nil {
			logger.Errorf("fatal error in indexer history drop: %v", err)
			return
		}
	}(*state)

	// The lock will stay held until the history drop results are
	// returned via the results channel.
}

func (ix *Indexer[B, T, E]) pollHistoryDropResults(
	ctx context.Context,
	historyDropLock *sync.Mutex,
	historyDropResults chan *database.State,
	state *database.State,
) error {
	// Check if history drop results are available each iteration but do
	// not block.
	select {
	case newState := <-historyDropResults:
		// Unlock the history drop lock after processing the results.
		defer historyDropLock.Unlock()

		if newState == nil {
			return errors.New("history drop failed")
		}

		logger.Infof("history drop completed, new state: %+v", newState)
		state.LastHistoryDrop = newState.LastHistoryDrop

		if newState.FirstIndexedBlockNumber > state.FirstIndexedBlockNumber {
			state.FirstIndexedBlockNumber = newState.FirstIndexedBlockNumber
			state.FirstIndexedBlockTimestamp = newState.FirstIndexedBlockTimestamp
		}

		// in case the history drop dropped all the blocks
		if newState.LastIndexedBlockNumber == 0 {
			state.LastIndexedBlockNumber = 0
			state.LastIndexedBlockTimestamp = 0

			if err := ix.updateStartBlock(ctx); err != nil {
				return err
			}
		}

	// default case to avoid blocking if results not available
	default:
	}

	return nil
}

func (ix *Indexer[B, T, E]) updateStartBlock(ctx context.Context) error {
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

func (ix *Indexer[B, T, E]) runIteration(
	ctx context.Context, state *database.State,
) (*iterationResult[B, T, E], error) {
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

	return &iterationResult[B, T, E]{
		blockResults: blockResults,
		state:        newState,
	}, nil
}

type blockRange struct {
	start uint64
	end   uint64
}

func (br blockRange) len() uint64 {
	// this should never happen, safety check
	if br.start > br.end {
		return 0
	}

	return br.end - br.start
}

func (ix *Indexer[B, T, E]) getBlockRange(state *database.State) (*blockRange, error) {
	result := new(blockRange)
	result.start = ix.getStartBlock(state)
	result.end = ix.getEndBlock(state, result.start)

	return result, nil
}

func (ix *Indexer[B, T, E]) getStartBlock(state *database.State) uint64 {
	if state == nil {
		return ix.startBlockNumber
	}

	if state.LastIndexedBlockNumber < ix.startBlockNumber {
		return ix.startBlockNumber
	}

	return state.LastIndexedBlockNumber + 1
}

func (ix *Indexer[B, T, E]) getEndBlock(state *database.State, start uint64) uint64 {
	latestConfirmedNum := state.LastChainBlockNumber - ix.confirmations + 1
	if latestConfirmedNum < start {
		return start
	}

	numBlocks := latestConfirmedNum + 1 - start
	if numBlocks > ix.maxBlockRange {
		return start + ix.maxBlockRange
	}

	return latestConfirmedNum + 1
}

func (ix *Indexer[B, T, E]) getBlockResults(
	ctx context.Context, blkRange *blockRange,
) ([]BlockResult[B, T, E], error) {
	sem := make(chan struct{}, ix.maxConcurrency)
	eg, ctx := errgroup.WithContext(ctx)

	l := blkRange.len()

	results := make([]BlockResult[B, T, E], l)

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

func (ix *Indexer[B, T, E]) saveData(ctx context.Context, results *iterationResult[B, T, E]) error {
	blocks := make([]*B, len(results.blockResults))
	var transactions []*T
	var events []*E

	for i := range results.blockResults {
		blocks[i] = &results.blockResults[i].Block

		resTxs := results.blockResults[i].Transactions
		for j := range resTxs {
			transactions = append(transactions, &resTxs[j])
		}

		resEvents := results.blockResults[i].Events
		for j := range results.blockResults[i].Events {
			events = append(events, &resEvents[j])
		}
	}

	logger.Debugf("fetched %d blocks with %d transactions from the chain", len(results.blockResults), len(transactions))

	err := ix.db.SaveAllEntities(ctx, blocks, transactions, events, results.state)
	if err != nil {
		return err
	}

	logger.Debug("data saved to the DB")

	return nil
}

func (ix *Indexer[B, T, E]) updateChainState(ctx context.Context, state *database.State) (*database.State, error) {
	newState := *state
	newState.LastChainBlockUpdated = uint64(time.Now().Unix())

	blockInfo, err := ix.blockchain.GetLatestBlockInfo(ctx)
	if err != nil {
		return nil, err
	}

	newState.LastChainBlockNumber = blockInfo.BlockNumber
	newState.LastChainBlockTimestamp = blockInfo.Timestamp

	return &newState, nil
}

func (ix *Indexer[B, T, E]) newBackoff() backoff.BackOff {
	return backoff.NewExponentialBackOff(backoff.WithMaxElapsedTime(ix.backoffMaxElapsedTime))
}

func updateState[B database.Block, T database.Transaction, E database.Event](
	results []BlockResult[B, T, E], state *database.State,
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

	newState.LastIndexedBlockUpdated = uint64(time.Now().Unix())

	return &newState
}
