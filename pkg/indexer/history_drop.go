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
		// Only a block genuinely absent from the node may move the start block;
		// any other failure aborts the startup instead of silently raising it.
		if !errors.Is(err, ErrBlockNotFound) {
			return 0, fmt.Errorf("failed to get timestamp for configured start block %d: %w", ix.startBlockNumber, err)
		}

		ix.log.Warnf("configured start block %d not found on the node, looking for the oldest available block instead", ix.startBlockNumber)

		start, findErr := ix.findBlockOnTheNode(ctx, ix.startBlockNumber, latestBlock.BlockNumber)
		if findErr != nil {
			return 0, fmt.Errorf("failed to find a block within numbers %d, %d: %w", ix.startBlockNumber, latestBlock.BlockNumber, findErr)
		}

		ix.log.Infof("using %d instead of start_block_number from config", start)
		ix.startBlockNumber = start

		firstBlockTime, err = ix.blockchain.GetBlockTimestamp(ctx, ix.startBlockNumber)
		if err != nil {
			return 0, fmt.Errorf("failed to get timestamp for fallback start block %d: %w", ix.startBlockNumber, err)
		}
	}

	if latestBlock.Timestamp <= firstBlockTime ||
		latestBlock.Timestamp-firstBlockTime < ix.historyDropInterval {
		return ix.startBlockNumber, nil
	}

	if latestBlock.BlockNumber < ix.startBlockNumber {
		return ix.startBlockNumber, nil
	}

	return ix.findEarliestBlockInInterval(
		ctx,
		ix.startBlockNumber,
		latestBlock.BlockNumber,
		latestBlock.Timestamp,
		ix.historyDropInterval,
	)
}

// findEarliestBlockInInterval returns the lowest block number in [low, high]
// whose timestamp falls within interval seconds of latestTimestamp, using
// binary search. If no block in the range satisfies the condition, it returns
// low — the caller is responsible for ensuring the range is non-empty and that
// a qualifying block exists when that matters.
func (ix *Indexer[B, T, E]) findEarliestBlockInInterval(
	ctx context.Context,
	low, high, latestTimestamp, interval uint64,
) (uint64, error) {
	if low > high {
		return 0, errors.New("invalid boundaries")
	}

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

// findBlockOnTheNode returns the lowest block number in [low, high] for which
// the node can serve a timestamp, using binary search. It assumes availability
// is monotonic: if block k is served, every block above k is too. Only errors
// wrapping ErrBlockNotFound count as absent blocks; any other error aborts the
// search.
func (ix *Indexer[B, T, E]) findBlockOnTheNode(
	ctx context.Context,
	low, high uint64,
) (uint64, error) {
	if low > high {
		return 0, errors.New("invalid boundaries")
	}

	var result uint64
	found := false
	for low <= high {
		mid := low + (high-low)/2

		_, err := ix.blockchain.GetBlockTimestamp(ctx, mid)
		if err != nil {
			if !errors.Is(err, ErrBlockNotFound) {
				return 0, fmt.Errorf("failed to get timestamp for block %d during search: %w", mid, err)
			}

			low = mid + 1
			continue
		}

		result = mid
		found = true
		if mid == 0 {
			break
		}
		high = mid - 1
	}

	if !found {
		return 0, errors.New("did not find block on node")
	}

	return result, nil
}
