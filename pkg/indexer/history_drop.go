package indexer

import (
	"context"
	"sort"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
)

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

func (ix *Indexer[B, T, E]) runHistoryDrop(
	ctx context.Context, state *database.State,
) (*database.State, error) {
	logger.Debugf("running history drop")

	return ix.db.DropHistoryIteration(
		ctx, state, ix.historyDropInterval, state.LastChainBlockTimestamp,
	)
}

func (ix *Indexer[B, T, E]) getMinBlockWithinHistoryInterval(
	ctx context.Context,
) (uint64, error) {
	firstBlockTime, err := ix.blockchain.GetBlockTimestamp(ctx, ix.startBlockNumber)
	if err != nil {
		return 0, err
	}

	latestBlock, err := ix.blockchain.GetLatestBlockInfo(ctx)
	if err != nil {
		return 0, err
	}

	if latestBlock.Timestamp-firstBlockTime < ix.historyDropInterval {
		return ix.startBlockNumber, nil
	}

	if latestBlock.BlockNumber < ix.startBlockNumber {
		return ix.startBlockNumber, nil
	}

	// find the first block within the history drop interval using binary search
	i := sort.Search(int(latestBlock.BlockNumber-ix.startBlockNumber), func(i int) bool {
		// The err variable comes from the enclosing function. If it has been
		// set to a non-nil value by a previous iteration of the binary search,
		// we should not overwrite it. Ideally we would exit the binary search
		// early, but the sort.Search function does not provide a way to do
		// that. So instead, we just return false for all future iterations.
		// The results of the search are meaningless in this case.
		if err != nil {
			return false
		}

		blockNumber := ix.startBlockNumber + uint64(i)

		var blockTime uint64
		blockTime, err = ix.blockchain.GetBlockTimestamp(ctx, blockNumber)
		if err != nil {
			return false
		}

		return latestBlock.Timestamp-blockTime <= ix.historyDropInterval
	})

	// If there was any error during the binary search, return it.
	if err != nil {
		return 0, err
	}

	return ix.startBlockNumber + uint64(i), nil
}
