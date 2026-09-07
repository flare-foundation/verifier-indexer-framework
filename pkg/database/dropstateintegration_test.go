//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// dropBlock is the block entity for the DropHistoryIteration tests.
type dropBlock struct {
	BlockNumber uint64 `gorm:"primaryKey"`
	Timestamp   uint64 `gorm:"index"`
}

func (b dropBlock) GetBlockNumber() uint64        { return b.BlockNumber }
func (b dropBlock) GetTimestamp() uint64          { return b.Timestamp }
func (b dropBlock) TimestampField() string        { return "timestamp" }
func (b dropBlock) HistoryDropOrder() []Deletable { return []Deletable{dropBlock{}} }

// noTimestampFieldBlock satisfies Block but not Deletable.
type noTimestampFieldBlock struct {
	BlockNumber uint64 `gorm:"primaryKey"`
	Timestamp   uint64 `gorm:"index"`
}

func (b noTimestampFieldBlock) GetBlockNumber() uint64        { return b.BlockNumber }
func (b noTimestampFieldBlock) GetTimestamp() uint64          { return b.Timestamp }
func (b noTimestampFieldBlock) HistoryDropOrder() []Deletable { return nil }

// dropTestDB builds a DB over dropBlock with its own tables.
func dropTestDB(t *testing.T) *DB[dropBlock, struct{}, struct{}] {
	t.Helper()

	g, err := Connect(conflictTestDB(t))
	require.NoError(t, err)

	require.NoError(t, g.Migrator().DropTable(&dropBlock{}))
	require.NoError(t, g.AutoMigrate(&dropBlock{}, &State{}))

	t.Cleanup(func() {
		require.NoError(t, g.Migrator().DropTable(&dropBlock{}))
	})

	return &DB[dropBlock, struct{}, struct{}]{g: g, log: logger.Nop{}}
}

// seedState writes the singleton state row the drop updates and returns it.
func seedState(t *testing.T, db *DB[dropBlock, struct{}, struct{}], state State) State {
	t.Helper()

	state.ID = globalStateID
	require.NoError(t, db.g.Save(&state).Error)

	return state
}

func storedState(t *testing.T, db *DB[dropBlock, struct{}, struct{}]) State {
	t.Helper()

	var stored State
	require.NoError(t, db.g.First(&stored, globalStateID).Error)

	return stored
}

// stateAtFirstDelete runs hook, if any, when the first DELETE of a drop is about
// to execute and returns a getter for the state row read right after it: the
// only way to observe what the drop persisted before deleting.
func stateAtFirstDelete(t *testing.T, db *DB[dropBlock, struct{}, struct{}], hook func()) func() *State {
	t.Helper()

	var seen *State
	require.NoError(t, db.g.Callback().Delete().Before("gorm:delete").Register("state_at_first_delete", func(*gorm.DB) {
		if seen != nil {
			return
		}

		if hook != nil {
			hook()
		}

		var state State
		if err := db.g.Session(&gorm.Session{NewDB: true}).First(&state, globalStateID).Error; err == nil {
			seen = &state
		}
	}))
	t.Cleanup(func() {
		db.g.Callback().Delete().Remove("state_at_first_delete") //nolint:errcheck // best-effort cleanup in a test
	})

	return func() *State { return seen }
}

// TestDropHistoryIterationPersistsBoundaryBeforeDeleting pins the invariant that
// stored state never advertises an already-deleted block.
func TestDropHistoryIterationPersistsBoundaryBeforeDeleting(t *testing.T) {
	ctx := context.Background()
	db := dropTestDB(t)

	rows := make([]*dropBlock, 0, 10)
	for n := uint64(1); n <= 10; n++ {
		rows = append(rows, &dropBlock{BlockNumber: n, Timestamp: n * 100})
	}
	require.NoError(t, db.g.Create(rows).Error)

	state := seedState(t, db, State{FirstIndexedBlockNumber: 1, FirstIndexedBlockTimestamp: 100, LastIndexedBlockNumber: 10})

	// deleteStart = 1000 - 500 = 500, so blocks 1..4 go and block 5 survives.
	atFirstDelete := stateAtFirstDelete(t, db, nil)

	newState, err := db.DropHistoryIteration(ctx, &state, 500, 1000)
	require.NoError(t, err)

	seen := atFirstDelete()
	require.NotNil(t, seen, "the delete callback must have fired")
	require.Equal(t, uint64(5), seen.FirstIndexedBlockNumber,
		"the surviving boundary must already be stored when the first row is deleted")

	require.Equal(t, uint64(5), newState.FirstIndexedBlockNumber)
	require.Equal(t, uint64(5), storedState(t, db).FirstIndexedBlockNumber)
	require.NotZero(t, newState.LastHistoryDrop)
}

// TestDropHistoryIterationEmptiesBoundaryBeforeWipingTheTable covers the path
// where nothing survives, so the boundary must be emptied before the deletion.
func TestDropHistoryIterationEmptiesBoundaryBeforeWipingTheTable(t *testing.T) {
	ctx := context.Background()
	db := dropTestDB(t)

	rows := make([]*dropBlock, 0, 5)
	for n := uint64(1); n <= 5; n++ {
		rows = append(rows, &dropBlock{BlockNumber: n, Timestamp: n * 10})
	}
	require.NoError(t, db.g.Create(rows).Error)

	state := seedState(t, db, State{FirstIndexedBlockNumber: 1, FirstIndexedBlockTimestamp: 10, LastIndexedBlockNumber: 5})

	atFirstDelete := stateAtFirstDelete(t, db, nil)

	// deleteStart = 10000 - 500 = 9500: every row is older, nothing survives.
	newState, err := db.DropHistoryIteration(ctx, &state, 500, 10000)
	require.NoError(t, err)

	seen := atFirstDelete()
	require.NotNil(t, seen, "the delete callback must have fired")
	require.Zero(t, seen.FirstIndexedBlockNumber,
		"an empty range must be stored before the rows it advertised are deleted")

	require.Zero(t, newState.FirstIndexedBlockNumber)
	require.Zero(t, storedState(t, db).FirstIndexedBlockNumber)
}

// TestDropHistoryIterationDoesNotLowerTheBoundary covers the common path, where
// the surviving block is already the boundary.
func TestDropHistoryIterationDoesNotLowerTheBoundary(t *testing.T) {
	ctx := context.Background()
	db := dropTestDB(t)

	// Rows below the boundary exist after resuming past unindexed blocks; they
	// are outside the advertised range.
	rows := []*dropBlock{
		{BlockNumber: 1, Timestamp: 900},
		{BlockNumber: 2, Timestamp: 950},
		{BlockNumber: 8, Timestamp: 1000},
	}
	require.NoError(t, db.g.Create(rows).Error)

	state := seedState(t, db, State{FirstIndexedBlockNumber: 8, FirstIndexedBlockTimestamp: 1000, LastIndexedBlockNumber: 8})

	// deleteStart = 1000 - 500 = 500: every row survives, block 1 is the lowest.
	newState, err := db.DropHistoryIteration(ctx, &state, 500, 1000)
	require.NoError(t, err)

	require.Equal(t, uint64(8), newState.FirstIndexedBlockNumber,
		"the boundary must not be lowered onto rows outside the advertised range")
	require.Equal(t, uint64(8), storedState(t, db).FirstIndexedBlockNumber)
}

// TestNewRejectsBlockWithoutTimestampField pins the startup guard: the column
// used to fall back to a hardcoded "timestamp".
func TestNewRejectsBlockWithoutTimestampField(t *testing.T) {
	cfg := conflictTestDB(t)
	cfg.HistoryDrop = 3600

	db, err := New(cfg, ExternalEntities[noTimestampFieldBlock, conflictTx, struct{}]{
		Block:       new(noTimestampFieldBlock),
		Transaction: new(conflictTx),
		Event:       new(struct{}),
	}, logger.Nop{})

	require.ErrorContains(t, err, "does not implement database.Deletable")
	require.Nil(t, db)
}

// TestNewRejectsTransactionFirstDropOrder pins the startup guard on the delete
// order: transactions first leaves block rows whose transactions are gone.
func TestNewRejectsTransactionFirstDropOrder(t *testing.T) {
	cfg := conflictTestDB(t)
	cfg.HistoryDrop = 3600

	db, err := New(cfg, ExternalEntities[txFirstBlock, conflictTx, struct{}]{
		Block:       new(txFirstBlock),
		Transaction: new(conflictTx),
		Event:       new(struct{}),
	}, logger.Nop{})

	require.ErrorContains(t, err, "must list the block table")
	require.Nil(t, db)
}
