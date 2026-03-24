package indexer

import (
	"context"
	"fmt"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
)

// shouldRunHistoryDrop reports whether enough time has elapsed since the last
// history drop to warrant running another one.
func (ix *Indexer[B, T, E]) shouldRunHistoryDrop(state *database.State) bool {
	if ix.historyDropInterval == 0 || state.LastChainBlockTimestamp < state.LastHistoryDrop {
		return false
	}

	if state.LastChainBlockTimestamp-state.LastHistoryDrop >= ix.historyDropFrequency {
		logger.Debugf(
			"history drop should run: last drop %d, last block %d, frequency %d",
			state.LastHistoryDrop, state.LastChainBlockTimestamp, ix.historyDropFrequency,
		)

		return true
	}

	return false
}

// runHistoryDrop executes a single history drop iteration, deleting blocks older
// than the configured interval.
func (ix *Indexer[B, T, E]) runHistoryDrop(
	ctx context.Context, state *database.State,
) (*database.State, error) {
	logger.Debugf("running history drop")

	return ix.db.DropHistoryIteration(
		ctx, state, ix.historyDropInterval, state.LastChainBlockTimestamp,
	)
}

// getMinBlockWithinHistoryInterval returns the lowest block number whose timestamp
// falls within the history drop interval of the latest block.
func (ix *Indexer[B, T, E]) getMinBlockWithinHistoryInterval(
	ctx context.Context,
) (uint64, error) {
	firstBlockTime, err := ix.blockchain.GetBlockTimestamp(ctx, ix.startBlockNumber)
	if err != nil {
		return 0, fmt.Errorf("failed to get timestamp for start block %d: %w", ix.startBlockNumber, err)
	}

	latestBlock, err := ix.blockchain.GetLatestBlockInfo(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest block info: %w", err)
	}

	if latestBlock.Timestamp <= firstBlockTime ||
		latestBlock.Timestamp-firstBlockTime < ix.historyDropInterval {
		return ix.startBlockNumber, nil
	}

	if latestBlock.BlockNumber < ix.startBlockNumber {
		return ix.startBlockNumber, nil
	}

	return ix.binarySearchBlockByTime(
		ctx,
		ix.startBlockNumber,
		latestBlock.BlockNumber,
		latestBlock.Timestamp,
		ix.historyDropInterval,
	)
}

// binarySearchBlockByTime finds the first block whose timestamp is within
// the given interval of the latest block's timestamp.
func (ix *Indexer[B, T, E]) binarySearchBlockByTime(
	ctx context.Context,
	low, high, latestTimestamp, interval uint64,
) (uint64, error) {
	result := low
	for low <= high {
		mid := low + (high-low)/2

		blockTime, err := ix.blockchain.GetBlockTimestamp(ctx, mid)
		if err != nil {
			return 0, fmt.Errorf("failed to get block timestamp for block %d during binary search: %w", mid, err)
		}

		if latestTimestamp >= blockTime && latestTimestamp-blockTime <= interval {
			result = mid
			if mid == low {
				break
			}
			high = mid - 1
		} else {
			low = mid + 1
		}
	}
	return result, nil
}
