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
// first-indexed boundary is persisted before any rows are deleted — emptied when
// nothing survives — so stored state never advertises removed blocks.
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

	if err := db.persistHistoryDropState(ctx, &newState, nil); err != nil {
		return nil, fmt.Errorf("failed to persist state after history drop: %w", err)
	}

	db.log.Infof("deleted blocks up to index %d", newState.FirstIndexedBlockNumber)

	return &newState, nil
}

// raiseFirstIndexedBoundary moves the boundary to the first block surviving a
// drop at deleteStart and persists it before any row is deleted. When nothing
// survives it is emptied: under-advertising is safe, over-advertising is not.
func (db *DB[B, T, E]) raiseFirstIndexedBoundary(ctx context.Context, b B, state *State, deleteStart uint64) error {
	timestampCol, err := blockTimestampField(b)
	if err != nil {
		return err
	}

	prior := state.FirstIndexedBlockNumber

	var survivor B
	err = db.g.WithContext(ctx).
		Where(fmt.Sprintf("%s >= ?", timestampCol), deleteStart).
		Order("block_number").
		First(&survivor).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("failed to get first block surviving the history drop: %w", err)
		}

		state.FirstIndexedBlockNumber = 0
		state.FirstIndexedBlockTimestamp = 0
	} else {
		if survivor.GetBlockNumber() <= state.FirstIndexedBlockNumber {
			return nil
		}

		state.FirstIndexedBlockNumber = survivor.GetBlockNumber()
		state.FirstIndexedBlockTimestamp = survivor.GetTimestamp()
	}

	if err := db.persistHistoryDropState(ctx, state, &prior); err != nil {
		return fmt.Errorf("failed to persist state before history drop: %w", err)
	}

	return nil
}

// blockTimestampField returns the block entity's timestamp column. Only B's
// value method set counts, so a pointer-receiver TimestampField is reported
// rather than guessed at.
func blockTimestampField[B Block](b B) (string, error) {
	d, ok := any(b).(Deletable)
	if !ok {
		return "", fmt.Errorf(
			"block entity %T does not implement database.Deletable: "+
				"declare TimestampField on the type used to instantiate the framework", b,
		)
	}

	col := d.TimestampField()
	if !validColumnName.MatchString(col) {
		return "", fmt.Errorf("invalid column name: %q", col)
	}

	return col, nil
}

// persistHistoryDropState updates only the state columns owned by the history
// drop, leaving the columns the indexing loop writes concurrently untouched.
//
// When prior is non-nil the boundary moves only while the stored value still
// matches it, so a boundary the loop established meanwhile survives.
// last_history_drop is always written; skipping it would retrigger the drop.
func (db *DB[B, T, E]) persistHistoryDropState(ctx context.Context, state *State, prior *uint64) error {
	firstNumber := any(state.FirstIndexedBlockNumber)
	firstTimestamp := any(state.FirstIndexedBlockTimestamp)

	if prior != nil {
		firstNumber = gorm.Expr(
			"CASE WHEN states.first_indexed_block_number = ? THEN ? ELSE states.first_indexed_block_number END",
			*prior, state.FirstIndexedBlockNumber)
		firstTimestamp = gorm.Expr(
			"CASE WHEN states.first_indexed_block_number = ? THEN ? ELSE states.first_indexed_block_timestamp END",
			*prior, state.FirstIndexedBlockTimestamp)
	}

	result := db.g.WithContext(ctx).Model(&State{}).
		Where("id = ?", state.ID).
		Updates(map[string]any{
			"first_indexed_block_number":    firstNumber,
			"first_indexed_block_timestamp": firstTimestamp,
			"last_history_drop":             state.LastHistoryDrop,
		})
	if result.Error != nil {
		return result.Error
	}

	// A fresh database has no state row until the first save inserts it.
	if result.RowsAffected == 0 {
		db.log.Warnf("history drop found no state row %d to update; nothing is advertised as indexed yet", state.ID)
	}

	return nil
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
