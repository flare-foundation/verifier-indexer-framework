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

	// Checked before probing the start block: the not-found fallback would
	// binary-search an already-inverted [start, tip] range.
	if latestBlock.BlockNumber < ix.startBlockNumber {
		return ix.startBlockNumber, nil
	}

	firstBlockTime, err := ix.blockchain.GetBlockTimestamp(ctx, ix.startBlockNumber)
	if err != nil {
		// a shutdown, or a node that only times out, is not a pruned block
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, fmt.Errorf("failed to get timestamp for configured start block %d: %w", ix.startBlockNumber, err)
		}

		probe := ix.sentinelProbe
		if errors.Is(err, ErrBlockNotFound) {
			ix.log.Warnf("configured start block %d not found on the node, looking for the oldest available block instead", ix.startBlockNumber)
		} else {
			// v1.1.1 clients report a pruned block with a plain error. The retry
			// window has absorbed transient failures, but a persistent one on
			// exactly this block moves the start block as it did then.
			ix.log.Warnf(
				"start block %d probe failed with an error that does not wrap ErrBlockNotFound: %v; "+
					"treating the block as pruned as v1.1.1 did and looking for the oldest available block "+
					"with unretried probes; wrap indexer.ErrBlockNotFound so this is not guessed",
				ix.startBlockNumber, err,
			)
			probe = ix.legacyProbe
		}

		start, findErr := ix.findBlockOnTheNode(ctx, ix.startBlockNumber, latestBlock.BlockNumber, probe)
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

// blockProbe reports whether the node serves a block during the oldest-block
// search; an error aborts the search.
type blockProbe func(ctx context.Context, blockNumber uint64) (bool, error)

// sentinelProbe is the retried probe for clients that wrap ErrBlockNotFound:
// only that sentinel counts as absent.
func (ix *Indexer[B, T, E]) sentinelProbe(ctx context.Context, blockNumber uint64) (bool, error) {
	_, err := ix.blockchain.GetBlockTimestamp(ctx, blockNumber)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrBlockNotFound):
		return false, nil
	default:
		return false, err
	}
}

// legacyProbe is v1.1.1's unretried probe for clients that report a pruned
// block with a plain error: any failure counts as absent, so a retried probe
// would spend the whole backoff window on every pruned block of the search.
func (ix *Indexer[B, T, E]) legacyProbe(ctx context.Context, blockNumber uint64) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, ix.requestTimeout)
	defer cancel()

	_, err := ix.rawBlockchain.GetBlockTimestamp(probeCtx, blockNumber)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	return err == nil, nil
}

// findBlockOnTheNode returns the lowest block number in [low, high] the probe
// reports as served, using binary search. It assumes availability is monotonic:
// if block k is served, every block above k is too.
func (ix *Indexer[B, T, E]) findBlockOnTheNode(
	ctx context.Context,
	low, high uint64,
	probe blockProbe,
) (uint64, error) {
	if low > high {
		return 0, errors.New("invalid boundaries")
	}

	var result uint64
	found := false
	for low <= high {
		mid := low + (high-low)/2

		present, err := probe(ctx, mid)
		if err != nil {
			return 0, fmt.Errorf("failed to probe block %d during search: %w", mid, err)
		}

		if !present {
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
