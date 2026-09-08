package database

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Only delete up to 1000 items in a single DB transaction to avoid lock
// timeouts.
const deleteBatchSize = 1000

// DropHistoryIteration deletes blocks and related entities older than the given
// interval and updates the state to reflect the new first indexed block. The
// first-indexed boundary is persisted before any rows are deleted — emptied when
// nothing survives — so stored state never advertises removed blocks. A zero
// interval deletes everything older than lastBlockTime; the indexer uses it to
// purge the rows below a resume start block.
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
	started := time.Now()

	var deleted int64

	var b B
	if err := db.raiseFirstIndexedBoundary(ctx, b, &newState, deleteStart); err != nil {
		return nil, err
	}

	// Blocks first, as New validated: a block row is the consumer's coverage token and must not outlive its transactions.
	for _, entity := range b.HistoryDropOrder() {
		n, err := deleteInBatches(ctx, db.g, deleteStart, entity)
		if err != nil {
			return nil, err
		}

		deleted += n
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

	if errors.Is(err, gorm.ErrRecordNotFound) {
		db.log.Infof("history drop deleted %d rows in %s: every block older than %d, none remain", deleted, time.Since(started).Round(time.Millisecond), deleteStart)
	} else {
		db.log.Infof("history drop deleted %d rows in %s, first indexed block now %d", deleted, time.Since(started).Round(time.Millisecond), newState.FirstIndexedBlockNumber)
	}

	return &newState, nil
}

// raiseFirstIndexedBoundary moves the boundary to the first block surviving a
// drop at deleteStart and persists it before any row is deleted. When nothing
// survives it is emptied: under-advertising is safe, over-advertising is not.
func (db *DB[B, T, E]) raiseFirstIndexedBoundary(ctx context.Context, b B, state *State, deleteStart uint64) error {
	timestampCol, err := blockTimestampField(db.g.NamingStrategy, b)
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

// blockTimestampField returns the block entity's timestamp column: from B's own
// method set, or from the HistoryDropOrder entry for B's table, where v1.1.1
// only needed it. A pointer B skips the fallback, since its method set already
// includes every receiver.
func blockTimestampField[B Block](namer schema.Namer, b B) (string, error) {
	if d, ok := any(b).(Deletable); ok {
		return validColumn(d.TimestampField())
	}

	if reflect.TypeOf(any(b)).Kind() != reflect.Pointer {
		table, err := tableName(namer, b)
		if err != nil {
			return "", err
		}

		for _, entity := range b.HistoryDropOrder() {
			entityTable, err := tableName(namer, entity)
			if err != nil {
				return "", err
			}

			if entityTable == table {
				return validColumn(entity.TimestampField())
			}
		}
	}

	return "", fmt.Errorf(
		"block entity %T does not implement database.Deletable and no HistoryDropOrder entry maps its table: "+
			"declare TimestampField on the type used to instantiate the framework", b,
	)
}

// validateHistoryDropOrder checks the order the block entity declares for the
// drop: non-empty, with the block table first. A block row is the consumer's
// coverage token, so it must not outlive the rows it vouches for.
func validateHistoryDropOrder[B Block](namer schema.Namer, b B) error {
	order := b.HistoryDropOrder()
	if len(order) == 0 {
		return errors.New("HistoryDropOrder is empty: nothing would be pruned while history_drop is set")
	}

	blockTable, err := tableName(namer, b)
	if err != nil {
		return err
	}

	for i, entity := range order {
		table, err := tableName(namer, entity)
		if err != nil {
			return err
		}

		if table != blockTable {
			continue
		}

		if i != 0 {
			return fmt.Errorf("HistoryDropOrder must list the block table %q first, found it at position %d: "+
				"a block row must not outlive its transactions", blockTable, i)
		}

		return nil
	}

	return fmt.Errorf("HistoryDropOrder does not include the block table %q", blockTable)
}

// tableName resolves the table a model maps to.
func tableName(namer schema.Namer, model any) (string, error) {
	s, err := schema.Parse(model, &sync.Map{}, namer)
	if err != nil {
		return "", fmt.Errorf("failed to parse entity schema: %w", err)
	}

	return s.Table, nil
}

// validColumn guards a column name that ends up in raw SQL.
func validColumn(col string) (string, error) {
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
			fmt.Sprintf("CASE WHEN %[1]s.first_indexed_block_number = ? THEN ? ELSE %[1]s.first_indexed_block_number END", db.stateTable()),
			*prior, state.FirstIndexedBlockNumber)
		firstTimestamp = gorm.Expr(
			fmt.Sprintf("CASE WHEN %[1]s.first_indexed_block_number = ? THEN ? ELSE %[1]s.first_indexed_block_timestamp END", db.stateTable()),
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
func deleteInBatches(ctx context.Context, db *gorm.DB, deleteStart uint64, entity Deletable) (int64, error) {
	col, err := validColumn(entity.TimestampField())
	if err != nil {
		return 0, err
	}

	var deleted int64

	for {
		select {
		case <-ctx.Done():
			return deleted, ctx.Err()
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
			return deleted, fmt.Errorf("failed to delete historic data in the DB: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return deleted, nil
		}

		deleted += result.RowsAffected
	}
}
