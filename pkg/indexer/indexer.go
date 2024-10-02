package indexer

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/config"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/database"
	"gitlab.com/flarenetwork/libs/go-flare-common/pkg/logger"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
)

var log = logger.GetLogger()

type BlockchainClient[B database.Block, T database.Transaction] interface {
	GetLatestBlockInfo(context.Context) (uint64, uint64, error) // returns block number and timestamp
	GetBlockResult(context.Context, uint64) (*BlockResult[B, T], error)
	GetBlockTimestamp(context.Context, uint64) (uint64, error)
}

type BlockResult[B database.Block, T database.Transaction] struct {
	Block        B
	Transactions []T
}

func New[B database.Block, T database.Transaction](
	cfg *config.BaseConfig, db *gorm.DB, blockchain BlockchainClient[B, T],
) Indexer[B, T] {
	return Indexer[B, T]{
		blockchain:            blockchain,
		confirmations:         cfg.Indexer.Confirmations,
		db:                    db,
		maxBlockRange:         cfg.Indexer.MaxBlockRange,
		maxConcurrency:        cfg.Indexer.MaxConcurrency,
		startBlockNumber:      cfg.Indexer.StartBlockNumber,
		BackoffMaxElapsedTime: time.Duration(cfg.Timeout.BackoffMaxElapsedTimeSeconds) * time.Second,
		Timeout:               time.Duration(cfg.Timeout.TimeoutMillis) * time.Millisecond,
	}
}

type Indexer[B database.Block, T database.Transaction] struct {
	blockchain       BlockchainClient[B, T]
	confirmations    uint64
	db               *gorm.DB
	maxBlockRange    uint64
	maxConcurrency   int
	startBlockNumber uint64

	BackoffMaxElapsedTime time.Duration // currently not used
	Timeout               time.Duration
}

func (ix *Indexer[B, T]) Run(ctx context.Context) error {
	expBackOff := backoff.NewExponentialBackOff()
	expBackOff.MaxElapsedTime = ix.BackoffMaxElapsedTime
	upToDateBackoff := backoff.NewExponentialBackOff()
	upToDateBackoff.MaxInterval = ix.BackoffMaxElapsedTime

	for {
		err := backoff.RetryNotify(
			func() error {
				results, newStates, err := ix.runIteration(ctx, database.GlobalStates)
				if err != nil {
					return err
				}

				if newStates == nil {
					time.Sleep(upToDateBackoff.NextBackOff())
					return nil
				}

				upToDateBackoff.Reset()

				err = ix.saveData(ctx, results, newStates)
				if err != nil {
					return err
				}
				database.GlobalStates.UpdateStates(newStates)
				log.Infof("successfully processed up to block %d", newStates[database.LastDatabaseIndexState].Index)

				return nil
			},
			expBackOff,
			func(err error, d time.Duration) {
				log.Errorf("indexer error: %v. Will retry after %v", err, d)
			},
		)
		if err != nil {
			return err
		}
	}
}

func (ix *Indexer[B, T]) runIteration(
	ctx context.Context, states *database.DBStates,
) ([]BlockResult[B, T], map[string]*database.State, error) {
	blkRange, err := ix.getBlockRange(ctx, states.States[database.LastDatabaseIndexState])
	if err != nil {
		return nil, nil, err
	}

	if blkRange.len() == 0 {
		return nil, nil, nil
	}

	log.Debugf("indexing from block %d to %d, latest block on chain %d", blkRange.start, blkRange.end-1, blkRange.latest)

	ctxResults, cancelFunc := context.WithTimeout(ctx, ix.Timeout)
	results, err := ix.getBlockResults(ctxResults, blkRange)
	cancelFunc()
	if err != nil {
		return nil, nil, err
	}

	newStates := newStatesForBlockRange(blkRange, results, states)

	return results, newStates, nil
}

type blockRange struct {
	start           uint64
	end             uint64
	latest          uint64
	latestTimestamp uint64
}

func (br blockRange) len() uint64 {
	return br.end - br.start
}

func (ix *Indexer[B, T]) getBlockRange(ctx context.Context, lastDBState *database.State) (*blockRange, error) {
	ctxWithTimeout, cancelFunc := context.WithTimeout(ctx, ix.Timeout)
	latestBlockNumber, latestBlockTimestamp, err := ix.blockchain.GetLatestBlockInfo(ctxWithTimeout)
	cancelFunc()
	if err != nil {
		return nil, err
	}

	result := new(blockRange)
	result.start = ix.getStartBlock(lastDBState)
	result.end = ix.getEndBlock(result.start, latestBlockNumber)
	result.latest = latestBlockNumber
	result.latestTimestamp = latestBlockTimestamp

	return result, nil
}

func (ix *Indexer[B, T]) getStartBlock(lastDBState *database.State) uint64 {
	if lastDBState == nil {
		return ix.startBlockNumber
	}

	if lastDBState.Index < ix.startBlockNumber {
		return ix.startBlockNumber
	}

	return lastDBState.Index + 1
}

func (ix *Indexer[B, T]) getEndBlock(start uint64, latest uint64) uint64 {
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
			bOff := backoff.NewExponentialBackOff()
			bOff.MaxElapsedTime = ix.BackoffMaxElapsedTime

			return backoff.RetryNotify(
				func() error {
					ctxWithTimeout, cancelFunc := context.WithTimeout(ctx, ix.Timeout)
					res, err := ix.blockchain.GetBlockResult(ctxWithTimeout, blockNum)
					cancelFunc()
					if err != nil {
						return err
					}

					results[blockNum-blkRange.start] = *res
					return nil
				},
				bOff,
				func(err error, d time.Duration) {
					log.Errorf("error indexing block %d: %v. Will retry after %v", blockNum, err, d)
				},
			)
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

func (ix *Indexer[B, T]) saveData(ctx context.Context, results []BlockResult[B, T], states map[string]*database.State) error {
	blocks := make([]*B, len(results))
	var transactions []*T

	for i := range results {
		blocks[i] = &results[i].Block

		resTxs := results[i].Transactions
		for j := range resTxs {
			transactions = append(transactions, &resTxs[j])
		}
	}
	log.Debugf("fetched %d blocks with %d transactions from the chain", len(results), len(transactions))

	err := database.SaveData(ix.db, ctx, blocks, transactions, states)
	if err != nil {
		return err
	}
	log.Debug("data saved to the DB")

	return nil
}

func newStatesForBlockRange[B database.Block, T database.Transaction](
	blkRange *blockRange, results []BlockResult[B, T], states *database.DBStates,
) map[string]*database.State {
	newStates := make(map[string]*database.State)
	if states.States[database.FirstDatabaseIndexState] == nil {
		newStates[database.FirstDatabaseIndexState] = &database.State{
			Name: database.FirstDatabaseIndexState, Index: blkRange.start, BlockTimestamp: results[0].Block.GetTimestamp(), Updated: time.Now(),
		}
	}
	if states.States[database.LastDatabaseIndexState] == nil {
		newStates[database.LastDatabaseIndexState] = &database.State{
			Name: database.LastDatabaseIndexState, Index: blkRange.end - 1, BlockTimestamp: results[len(results)-1].Block.GetTimestamp(), Updated: time.Now(),
		}
	} else {
		newStates[database.LastDatabaseIndexState] = states.States[database.LastDatabaseIndexState].NewState(blkRange.end-1, results[len(results)-1].Block.GetTimestamp())
	}
	if states.States[database.LastChainIndexState] == nil {
		newStates[database.LastChainIndexState] = &database.State{
			Name: database.LastChainIndexState, Index: blkRange.latest, BlockTimestamp: blkRange.latestTimestamp, Updated: time.Now(),
		}
	} else {
		newStates[database.LastChainIndexState] = states.States[database.LastChainIndexState].NewState(blkRange.latest, blkRange.latestTimestamp)
	}

	return newStates
}
