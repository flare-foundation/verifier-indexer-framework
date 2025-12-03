package indexer

import (
	"context"
	"testing"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/stretchr/testify/require"
)

type Block struct{}

func (e Block) GetBlockNumber() uint64 {
	return 0
}
func (e Block) GetTimestamp() uint64 {
	return 0
}

type Transaction struct{}

type DB struct{}

func TestGetInitialStartBlockNumber(t *testing.T) {
	ctx := context.Background()

	t.Run("returns zero when no previous state exists", func(t *testing.T) {
		ix := Indexer[Block, Transaction]{}
		var state database.State

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(0), startBlock)
	})

	t.Run("returns last processed block number plus one when previous state exists", func(t *testing.T) {
		ix := Indexer[Block, Transaction]{}
		state := database.State{LastIndexedBlockNumber: 42}

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(43), startBlock)
	})
}
