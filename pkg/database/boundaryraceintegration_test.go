//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// preDropState is what the indexing loop still holds while a drop runs: the
// boundary the drop has since emptied and the stamp the drop has not yet moved.
func preDropState(last, lastTS uint64) *State {
	return &State{
		ID:                         globalStateID,
		FirstIndexedBlockNumber:    1,
		FirstIndexedBlockTimestamp: 100,
		LastIndexedBlockNumber:     last,
		LastIndexedBlockTimestamp:  lastTS,
		LastHistoryDrop:            500,
	}
}

// requireAdvertisedRowsExist asserts every block from the stored boundary to the
// last indexed block is in the table.
func requireAdvertisedRowsExist(t *testing.T, db *DB[dropBlock, struct{}, struct{}], stored State) {
	t.Helper()

	require.NotZero(t, stored.FirstIndexedBlockNumber)

	var count int64
	require.NoError(t, db.g.Model(&dropBlock{}).
		Where("block_number BETWEEN ? AND ?", stored.FirstIndexedBlockNumber, stored.LastIndexedBlockNumber).
		Count(&count).Error)
	require.EqualValues(t, stored.LastIndexedBlockNumber-stored.FirstIndexedBlockNumber+1, count)
}

// TestSaveIntoEmptiedBoundaryEstablishesItsOwnLowestBlock drives the row a
// no-survivor drop leaves for the whole deletion window — boundary emptied, stamp
// unchanged — and saves with the loop's stale view of it.
func TestSaveIntoEmptiedBoundaryEstablishesItsOwnLowestBlock(t *testing.T) {
	ctx := context.Background()
	db := dropTestDB(t)

	seedState(t, db, *preDropState(10, 1000))
	// what raiseFirstIndexedBoundary persists when nothing survives
	require.NoError(t, db.persistHistoryDropState(ctx, &State{ID: globalStateID, LastHistoryDrop: 500}, nil))

	batch := []*dropBlock{{BlockNumber: 12, Timestamp: 1200}, {BlockNumber: 11, Timestamp: 1100}, {BlockNumber: 13, Timestamp: 1300}}
	require.NoError(t, db.SaveAllEntities(ctx, batch, nil, nil, preDropState(13, 1300)))

	stored := storedState(t, db)
	assert.Equal(t, uint64(11), stored.FirstIndexedBlockNumber, "the boundary must be the save's own lowest block, not the pre-drop value")
	assert.Equal(t, uint64(1100), stored.FirstIndexedBlockTimestamp)
	assert.Equal(t, uint64(13), stored.LastIndexedBlockNumber)
	assert.Equal(t, uint64(500), stored.LastHistoryDrop, "the stamp belongs to the drop")
	requireAdvertisedRowsExist(t, db, stored)

	t.Run("a raised boundary is not lowered by a lower batch", func(t *testing.T) {
		require.NoError(t, db.SaveAllEntities(ctx, []*dropBlock{{BlockNumber: 3, Timestamp: 300}}, nil, nil, preDropState(13, 1300)))
		assert.Equal(t, uint64(11), storedState(t, db).FirstIndexedBlockNumber)
	})
}

// TestSaveDuringNoSurvivorDropNeverAdvertisesThePreDropBoundary runs a save
// inside the first delete of a drop that wipes every existing row, the window
// the stamp check left open.
func TestSaveDuringNoSurvivorDropNeverAdvertisesThePreDropBoundary(t *testing.T) {
	ctx := context.Background()
	db := dropTestDB(t)

	rows := make([]*dropBlock, 0, 10)
	for n := uint64(1); n <= 10; n++ {
		rows = append(rows, &dropBlock{BlockNumber: n, Timestamp: n * 100})
	}
	require.NoError(t, db.g.Create(rows).Error)
	seedState(t, db, *preDropState(10, 1000))

	// deleteStart = 2000 - 500 = 1500: rows 1..10 go; the batch saved mid-drop survives.
	afterSave := stateAtFirstDelete(t, db, func() {
		batch := []*dropBlock{{BlockNumber: 11, Timestamp: 1600}, {BlockNumber: 12, Timestamp: 1700}}
		if err := db.SaveAllEntities(ctx, batch, nil, nil, preDropState(12, 1700)); err != nil {
			t.Errorf("save during drop: %v", err)
		}
	})

	newState, err := db.DropHistoryIteration(ctx, preDropState(10, 1000), 500, 2000)
	require.NoError(t, err)

	seen := afterSave()
	require.NotNil(t, seen, "the mid-drop save must have run")
	assert.Equal(t, uint64(11), seen.FirstIndexedBlockNumber, "mid-drop the boundary must be the save's own lowest block")

	stored := storedState(t, db)
	assert.Equal(t, uint64(11), stored.FirstIndexedBlockNumber)
	assert.Equal(t, uint64(11), newState.FirstIndexedBlockNumber)
	requireAdvertisedRowsExist(t, db, stored)
}
