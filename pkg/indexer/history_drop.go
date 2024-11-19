package indexer

import (
	"context"
	"sort"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
)

func (ix *Indexer[B, T]) shouldRunHistoryDrop(state *database.State) bool {
	if ix.historyDropInterval == 0 || state.LastChainBlockTimestamp < state.LastHistoryDrop {
		return false
	}

	return state.LastChainBlockTimestamp-state.LastHistoryDrop >= ix.historyDropInterval
}

func (ix *Indexer[B, T]) runHistoryDrop(
	ctx context.Context, state *database.State,
) (*database.State, error) {
	logger.Debugf("running history drop")

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
		blockNumber := ix.startBlockNumber + uint64(i)

		var blockTime uint64
		blockTime, err = ix.blockchain.GetBlockTimestamp(ctx, blockNumber)
		if err != nil {
			return false
		}

		return latestBlock.Timestamp-blockTime <= ix.historyDropInterval
	})
	if err != nil {
		return 0, err
	}

	return ix.startBlockNumber + uint64(i), nil
}
