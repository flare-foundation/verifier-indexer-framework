package indexer

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"
	"gitlab.com/ryancollingham/flare-common/pkg/logger"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/config"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/database"
	"golang.org/x/sync/errgroup"
)

var log = logger.GetLogger()

func New(cfg *config.Indexer, db *database.DB, blockchain BlockchainClient) Indexer {
	return Indexer{
		blockchain:       blockchain,
		confirmations:    cfg.Confirmations,
		db:               db,
		maxBlockRange:    cfg.MaxBlockRange,
		maxConcurrency:   cfg.MaxConcurrency,
		startBlockNumber: cfg.StartBlockNumber,
	}
}

type Indexer struct {
	blockchain       BlockchainClient
	confirmations    uint64
	db               *database.DB
	maxBlockRange    uint64
	maxConcurrency   int
	startBlockNumber uint64
}

type BlockchainClient interface {
	GetLatestBlockNumber(context.Context) (uint64, error)
	GetBlockResult(context.Context, uint64) (*BlockResult, error)
}

func (ix *Indexer) Run(ctx context.Context) error {
	state, err := ix.db.GetState(ctx)
	if err != nil {
		return err
	}

	upToDateBackoff := backoff.NewExponentialBackOff()

	for {
		err := backoff.RetryNotify(
			func() error {
				newState, err := ix.runIteration(ctx, state)
				if err != nil {
					return err
				}

				if newState == nil {
					time.Sleep(upToDateBackoff.NextBackOff())
					return nil
				}

				upToDateBackoff.Reset()

				if err := ix.db.StoreState(ctx, newState); err != nil {
					return err
				}

				state = newState
				return nil
			},
			backoff.NewExponentialBackOff(),
			func(err error, d time.Duration) {
				log.Errorf("indexer error: %v. Will retry after %v", err, d)
			},
		)
		if err != nil {
			return err
		}
	}
}

func (ix *Indexer) runIteration(ctx context.Context, state *database.State) (*database.State, error) {
	blkRange, err := ix.getBlockRange(ctx, state)
	if err != nil {
		return nil, err
	}

	if blkRange.len() == 0 {
		return nil, nil
	}

	log.Debug("indexing from block %d to %d", blkRange.start, blkRange.end)

	if err := ix.indexBlockRange(ctx, blkRange); err != nil {
		return nil, err
	}

	return updateState(state, blkRange), nil
}

type blockRange struct {
	start uint64
	end   uint64
}

func (br blockRange) len() uint64 {
	return br.end - br.start
}

func (ix *Indexer) getBlockRange(ctx context.Context, state *database.State) (*blockRange, error) {
	latestBlockNumber, err := ix.blockchain.GetLatestBlockNumber(ctx)
	if err != nil {
		return nil, err
	}

	result := new(blockRange)
	result.start = ix.getStartBlock(state)
	result.end = ix.getEndBlock(result.start, latestBlockNumber)

	return result, nil
}

func (ix *Indexer) getStartBlock(state *database.State) uint64 {
	if state == nil {
		return ix.startBlockNumber
	}

	if state.LastIndexedBlockNumber < ix.startBlockNumber {
		return ix.startBlockNumber
	}

	return state.LastIndexedBlockNumber + 1
}

func (ix *Indexer) getEndBlock(start uint64, latest uint64) uint64 {
	latestConfirmedNum := latest - ix.confirmations
	if latestConfirmedNum < start {
		return latestConfirmedNum + 1
	}

	numBlocks := latestConfirmedNum + 1 - start
	if numBlocks > ix.maxBlockRange {
		return start + ix.maxBlockRange
	}

	return latestConfirmedNum + 1
}

func (ix *Indexer) indexBlockRange(ctx context.Context, blkRange *blockRange) error {
	results, err := ix.getBlockResults(ctx, blkRange)
	if err != nil {
		return err
	}

	return ix.saveResults(ctx, results)
}

type BlockResult struct {
	Block        *database.Block
	Transactions []database.Transaction
}

func (ix *Indexer) getBlockResults(
	ctx context.Context, blkRange *blockRange,
) ([]BlockResult, error) {
	sem := make(chan struct{}, ix.maxConcurrency)
	eg, ctx := errgroup.WithContext(ctx)

	l := blkRange.len()
	if l < 1 {
		return nil, nil
	}

	results := make([]BlockResult, l)
	bOff := backoff.NewExponentialBackOff()

	for i := blkRange.start; i < blkRange.end; i++ {
		blockNum := i
		eg.Go(func() error {
			return backoff.RetryNotify(
				func() error {
					sem <- struct{}{}
					defer func() { <-sem }()

					res, err := ix.blockchain.GetBlockResult(ctx, blockNum)
					if err != nil {
						return err
					}

					results[blockNum-blkRange.start] = *res
					return nil
				},
				bOff,
				func(err error, d time.Duration) {
					log.Errorf("error indexing block %d: %v. Will retry after %v", blockNum, d)
				},
			)
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

func (ix *Indexer) saveResults(ctx context.Context, results []BlockResult) error {
	blocks := make([]*database.Block, len(results))
	var transactions []*database.Transaction

	for i := range results {
		blocks[i] = results[i].Block

		resTxs := results[i].Transactions
		for j := range resTxs {
			transactions = append(transactions, &resTxs[j])
		}
	}

	if err := ix.db.SaveBlocksBatch(ctx, blocks); err != nil {
		return err
	}

	if err := ix.db.SaveTransactionsBatch(ctx, transactions); err != nil {
		return err
	}

	return nil
}

func updateState(state *database.State, blkRange *blockRange) *database.State {
	var newState database.State
	if state != nil {
		newState = *state
	}

	newState.LastIndexedBlockNumber = blkRange.end
	newState.UpdatedAt = time.Now()

	return &newState
}
