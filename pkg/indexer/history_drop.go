package indexer

import (
	"context"
	"errors"
	"fmt"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
)

// shouldRunHistoryDrop reports whether enough time has elapsed since the last
// history drop to warrant running another one.
func (ix *Indexer[B, T, E]) shouldRunHistoryDrop(state *database.State) bool {
	if ix.historyDropInterval == 0 || state.LastChainBlockTimestamp < state.LastHistoryDrop {
		return false
	}

	if state.LastChainBlockTimestamp-state.LastHistoryDrop >= ix.historyDropFrequency {
		ix.log.Debugf(
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
	ix.log.Debugf("running history drop")

	return ix.db.DropHistoryIteration(
		ctx, state, ix.historyDropInterval, state.LastChainBlockTimestamp,
	)
}

// getMinBlockWithinHistoryInterval returns the lowest block number whose timestamp
// falls within the history drop interval of the latest block.
func (ix *Indexer[B, T, E]) getMinBlockWithinHistoryInterval(
	ctx context.Context,
) (uint64, error) {
	latestBlock, err := ix.blockchain.GetLatestBlockInfo(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest block info: %w", err)
	}

	firstBlockTime, err := ix.blockchain.GetBlockTimestamp(ctx, ix.startBlockNumber)
	if err != nil {
		ix.log.Warnf("could not get configured startBlock with number %d: %w", ix.startBlockNumber, err)
		ix.log.Warn("looking for the oldest block on the node instead")

		start, err := ix.findBlockOnTheNode(ctx, ix.startBlockNumber, latestBlock.BlockNumber)
		if err != nil {
			return 0, fmt.Errorf("failed to find a block within numbers %d, %d", ix.startBlockNumber, latestBlock.BlockNumber)
		}

		ix.log.Infof("using %d instead of start_block_number from config", start)
		ix.startBlockNumber = start
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

// binarySearchBlockByTime finds the first block after low that is stored by Blockchain Node using binary search.
func (ix *Indexer[B, T, E]) findBlockOnTheNode(
	ctx context.Context,
	low, high uint64,
) (uint64, error) {
	result := low
	for low <= high {
		mid := low + (high-low)/2

		_, err := ix.blockchain.GetBlockTimestamp(ctx, mid)
		if err == nil {
			result = mid
			if mid == low {
				return mid, nil
			}
			high = mid - 1
		} else {
			if mid == low {
				return 0, errors.New("did not find block on node")
			}

			low = mid + 1
		}
	}

	return result, nil
}
