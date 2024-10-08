package indexer

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/database"
)

func (ix *Indexer[B, T]) shouldRunHistoryDrop(state *database.State) bool {
	if ix.historyDropInterval == 0 {
		return false
	}

	lastChainBlockTimestamp := time.Unix(int64(state.LastChainBlockTimestamp), 0)
	if lastChainBlockTimestamp.Sub(ix.lastHistoryDropRun) < time.Duration(ix.historyDropInterval)*time.Second {
		return false
	}

	return true
}

func (ix *Indexer[B, T]) runHistoryDrop(
	ctx context.Context, state *database.State,
) (*database.State, error) {
	return ix.db.DropHistoryIteration(
		ctx, state, ix.historyDropInterval, state.LastChainBlockTimestamp,
	)
}

func (ix *Indexer[B, T]) getMinBlockWithinHistoryInterval(
	ctx context.Context,
) (uint64, error) {
	firstBlockTime, err := ix.blockchain.GetBlockTimestamp(ctx, ix.startBlockNumber)
	if err != nil {
		return 0, err
	}

	lastBlockNumber, lastBlockTime, err := ix.blockchain.GetLatestBlockInfo(ctx)
	if err != nil {
		return 0, err
	}

	if lastBlockTime-firstBlockTime < ix.historyDropInterval {
		return ix.startBlockNumber, nil
	}

	var newBlockTime uint64
	firstBlockNumber := ix.startBlockNumber
	for lastBlockNumber-firstBlockNumber > 1 {
		newBlockNumber := (firstBlockNumber + lastBlockNumber) / 2

		err = backoff.RetryNotify(
			func() error {
				newBlockTime, err = ix.blockchain.GetBlockTimestamp(ctx, newBlockNumber)
				if err != nil {
					return err
				}
				return nil
			},
			backoff.NewExponentialBackOff(),
			func(err error, d time.Duration) {
				log.Errorf("error getting block timestamp: %w. Will retry after %v", err, d)
			},
		)
		if err != nil {
			return 0, err
		}

		if lastBlockTime-newBlockTime <= ix.historyDropInterval {
			lastBlockNumber = newBlockNumber
		} else {
			firstBlockNumber = newBlockNumber
		}
	}

	return lastBlockNumber, nil
}
