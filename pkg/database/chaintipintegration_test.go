//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSaveChainTipTouchesOnlyChainColumns pins the column scope: the boundary
// and drop stamp other writers own must survive, and a fresh database gets a
// row the loop's guards still read as empty.
func TestSaveChainTipTouchesOnlyChainColumns(t *testing.T) {
	ctx := context.Background()
	db := dropTestDB(t)

	require.NoError(t, db.g.Where("id = ?", globalStateID).Delete(&State{}).Error)

	tip := &State{LastChainBlockNumber: 10, LastChainBlockTimestamp: 1000, LastChainBlockUpdated: 42}
	require.NoError(t, db.SaveChainTip(ctx, tip))

	fresh := storedState(t, db)
	assert.Equal(t, uint64(10), fresh.LastChainBlockNumber)
	assert.Zero(t, fresh.FirstIndexedBlockNumber)
	assert.Zero(t, fresh.LastIndexedBlockNumber)
	assert.Zero(t, fresh.LastIndexedBlockUpdated, "a fresh row must still read as nothing indexed")

	seedState(t, db, State{
		FirstIndexedBlockNumber: 3, FirstIndexedBlockTimestamp: 300,
		LastIndexedBlockNumber: 9, LastIndexedBlockTimestamp: 900, LastIndexedBlockUpdated: 7,
		LastHistoryDrop:      5,
		LastChainBlockNumber: 10, LastChainBlockTimestamp: 1000, LastChainBlockUpdated: 42,
	})

	tip = &State{LastChainBlockNumber: 12, LastChainBlockTimestamp: 1200, LastChainBlockUpdated: 43}
	require.NoError(t, db.SaveChainTip(ctx, tip))

	stored := storedState(t, db)
	assert.Equal(t, uint64(12), stored.LastChainBlockNumber)
	assert.Equal(t, uint64(1200), stored.LastChainBlockTimestamp)
	assert.Equal(t, uint64(43), stored.LastChainBlockUpdated)
	assert.Equal(t, uint64(3), stored.FirstIndexedBlockNumber)
	assert.Equal(t, uint64(300), stored.FirstIndexedBlockTimestamp)
	assert.Equal(t, uint64(9), stored.LastIndexedBlockNumber)
	assert.Equal(t, uint64(7), stored.LastIndexedBlockUpdated)
	assert.Equal(t, uint64(5), stored.LastHistoryDrop)
}
