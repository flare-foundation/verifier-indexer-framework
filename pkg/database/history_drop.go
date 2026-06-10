package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"gorm.io/gorm"
)

// Only delete up to 1000 items in a single DB transaction to avoid lock
// timeouts.
const deleteBatchSize = 1000

// DropHistoryIteration deletes blocks and related entities older than the given
// interval and updates the state to reflect the new first indexed block. The
// first-indexed boundary is persisted before any rows are deleted so the stored
// state never advertises blocks that have already been removed.
func (db *DB[B, T, E]) DropHistoryIteration(
	ctx context.Context,
	state *State,
	intervalSeconds, lastBlockTime uint64,
) (*State, error) {
	if lastBlockTime < intervalSeconds {
		return state, nil
	}

	deleteStart := lastBlockTime - intervalSeconds
	newState := *state

	var b B
	if err := db.raiseFirstIndexedBoundary(ctx, b, &newState, deleteStart); err != nil {
		return nil, err
	}

	// Delete in the order specified by HistoryDropOrder to avoid foreign key constraint violations.
	for _, entity := range b.HistoryDropOrder() {
		if err := deleteInBatches(ctx, db.g, deleteStart, entity); err != nil {
			return nil, err
		}
	}

	var firstBlock B
	err := db.g.WithContext(ctx).Order("block_number").First(&firstBlock).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to get first block in the DB: %w", err)
	}

	newState.LastHistoryDrop = uint64(time.Now().Unix())
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		newState.FirstIndexedBlockNumber = 0
		newState.FirstIndexedBlockTimestamp = 0
	case firstBlock.GetBlockNumber() > newState.FirstIndexedBlockNumber:
		newState.FirstIndexedBlockNumber = firstBlock.GetBlockNumber()
		newState.FirstIndexedBlockTimestamp = firstBlock.GetTimestamp()
	default:
		// Rows older than the current boundary exist (e.g. after resuming past
		// unindexed blocks); they are outside the advertised contiguous range,
		// so the boundary is not lowered onto them.
	}

	if err := db.persistHistoryDropState(ctx, &newState); err != nil {
		return nil, fmt.Errorf("failed to persist state after history drop: %w", err)
	}

	db.log.Infof("deleted blocks up to index %d", newState.FirstIndexedBlockNumber)

	return &newState, nil
}

// raiseFirstIndexedBoundary moves the state's first-indexed boundary up to the
// first block that will survive a drop starting at deleteStart and persists it.
// When no block survives, the boundary is left unchanged; the reset to zero is
// persisted after the deletion completes.
func (db *DB[B, T, E]) raiseFirstIndexedBoundary(ctx context.Context, b B, state *State, deleteStart uint64) error {
	timestampCol := "timestamp"
	if d, ok := any(b).(Deletable); ok {
		timestampCol = d.TimestampField()
	}
	if !validColumnName.MatchString(timestampCol) {
		return fmt.Errorf("invalid column name: %q", timestampCol)
	}

	var survivor B
	err := db.g.WithContext(ctx).
		Where(fmt.Sprintf("%s >= ?", timestampCol), deleteStart).
		Order("block_number").
		First(&survivor).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}

		return fmt.Errorf("failed to get first block surviving the history drop: %w", err)
	}

	if survivor.GetBlockNumber() <= state.FirstIndexedBlockNumber {
		return nil
	}

	state.FirstIndexedBlockNumber = survivor.GetBlockNumber()
	state.FirstIndexedBlockTimestamp = survivor.GetTimestamp()

	if err := db.persistHistoryDropState(ctx, state); err != nil {
		return fmt.Errorf("failed to persist state before history drop: %w", err)
	}

	return nil
}

// persistHistoryDropState updates only the state columns owned by the history
// drop, leaving the columns the indexing loop writes concurrently untouched.
func (db *DB[B, T, E]) persistHistoryDropState(ctx context.Context, state *State) error {
	return db.g.WithContext(ctx).Model(&State{}).
		Where("id = ?", state.ID).
		Updates(map[string]any{
			"first_indexed_block_number":    state.FirstIndexedBlockNumber,
			"first_indexed_block_timestamp": state.FirstIndexedBlockTimestamp,
			"last_history_drop":             state.LastHistoryDrop,
		}).Error
}

// Deletable is implemented by entities that support timestamp-based history pruning.
type Deletable interface {
	// TimestampField returns the database column name used for timestamp-based deletion.
	TimestampField() string
}

var validColumnName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// deleteInBatches removes rows from the entity's table where the timestamp column
// is older than deleteStart, processing up to deleteBatchSize rows per statement.
// Postgres does not support LIMIT on DELETE (gorm silently drops it), so the
// batch is selected by ctid in a subquery.
func deleteInBatches(ctx context.Context, db *gorm.DB, deleteStart uint64, entity Deletable) error {
	col := entity.TimestampField()
	if !validColumnName.MatchString(col) {
		return fmt.Errorf("invalid column name: %q", col)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		batch := db.WithContext(ctx).
			Session(&gorm.Session{NewDB: true}).
			Model(entity).
			Select("ctid").
			Where(fmt.Sprintf("%s < ?", col), deleteStart).
			Limit(deleteBatchSize)

		result := db.WithContext(ctx).
			Where("ctid IN (?)", batch).
			Delete(entity)

		if result.Error != nil {
			return fmt.Errorf("failed to delete historic data in the DB: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return nil
		}
	}
}
