//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// raceBlock is a minimal Block implementation for exercising the state writers.
type raceBlock struct {
	BlockNumber uint64 `gorm:"primaryKey"`
	Timestamp   uint64
}

func (b raceBlock) GetBlockNumber() uint64        { return b.BlockNumber }
func (b raceBlock) GetTimestamp() uint64          { return b.Timestamp }
func (b raceBlock) HistoryDropOrder() []Deletable { return nil }

// TestStateWriterColumnOwnership verifies that the indexing loop's SaveAllEntities
// and the history drop's persistHistoryDropState do not clobber each other's
// columns on the singleton state row, and that SaveState writes authoritatively.
func TestStateWriterColumnOwnership(t *testing.T) {
	g, err := Connect(conflictTestDB(t))
	require.NoError(t, err)

	require.NoError(t, g.Migrator().DropTable(&State{}))
	require.NoError(t, g.AutoMigrate(&State{}))

	db := &DB[raceBlock, struct{}, struct{}]{g: g, log: logger.Nop{}}
	ctx := context.Background()

	// The first save creates the row and establishes the boundary at 100.
	require.NoError(t, db.SaveAllEntities(ctx, nil, nil, nil, &State{
		ID:                         globalStateID,
		FirstIndexedBlockNumber:    100,
		FirstIndexedBlockTimestamp: 1000,
		LastIndexedBlockNumber:     100,
		LastIndexedBlockTimestamp:  1000,
	}))

	got, err := db.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(100), got.FirstIndexedBlockNumber, "first save must establish the boundary")

	// A concurrent history drop raises the boundary and claims last_history_drop.
	require.NoError(t, db.persistHistoryDropState(ctx, &State{
		ID:                         globalStateID,
		FirstIndexedBlockNumber:    5000,
		FirstIndexedBlockTimestamp: 50000,
		LastHistoryDrop:            777,
	}))

	// A steady-state save still carrying the stale low boundary must advance the
	// progress columns without lowering the raised boundary or touching
	// last_history_drop. This is the race the fix closes.
	require.NoError(t, db.SaveAllEntities(ctx, nil, nil, nil, &State{
		ID:                         globalStateID,
		FirstIndexedBlockNumber:    100,
		FirstIndexedBlockTimestamp: 1000,
		LastIndexedBlockNumber:     200,
		LastIndexedBlockTimestamp:  2000,
	}))

	got, err = db.GetState(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(5000), got.FirstIndexedBlockNumber, "stale save must not lower the raised boundary")
	assert.Equal(t, uint64(50000), got.FirstIndexedBlockTimestamp)
	assert.Equal(t, uint64(200), got.LastIndexedBlockNumber, "progress columns must still advance")
	assert.Equal(t, uint64(2000), got.LastIndexedBlockTimestamp)
	assert.Equal(t, uint64(777), got.LastHistoryDrop, "last_history_drop is owned by the drop")

	// After a drop empties the database and resets the boundary to zero, the next
	// regular save re-establishes it from the empty sentinel.
	require.NoError(t, db.persistHistoryDropState(ctx, &State{ID: globalStateID, LastHistoryDrop: 888}))

	require.NoError(t, db.SaveAllEntities(ctx, nil, nil, nil, &State{
		ID:                         globalStateID,
		FirstIndexedBlockNumber:    6000,
		FirstIndexedBlockTimestamp: 60000,
		LastIndexedBlockNumber:     6000,
		LastIndexedBlockTimestamp:  60000,
	}))

	got, err = db.GetState(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(6000), got.FirstIndexedBlockNumber, "boundary must be re-established from zero")
	assert.Equal(t, uint64(60000), got.FirstIndexedBlockTimestamp)

	require.NoError(t, db.SaveState(ctx, &State{
		ID:                         globalStateID,
		FirstIndexedBlockNumber:    300,
		FirstIndexedBlockTimestamp: 3000,
		LastIndexedBlockNumber:     6000,
		LastIndexedBlockTimestamp:  60000,
	}))

	got, err = db.GetState(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(300), got.FirstIndexedBlockNumber, "SaveState is authoritative and may lower the boundary")

	require.NoError(t, g.Migrator().DropTable(&State{}))
}
