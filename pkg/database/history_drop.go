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
// interval and updates the state to reflect the new first indexed block.
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
	deleteOrder := b.HistoryDropOrder()

	// Delete in the order specified by HistoryDropOrder to avoid foreign key constraint violations.
	for _, entity := range deleteOrder {
		if err := deleteInBatches(ctx, db.g, deleteStart, entity); err != nil {
			return nil, err
		}
	}

	var firstBlock B
	err := db.g.Order("block_number").First(&firstBlock).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to get first block in the DB: %w", err)
	}

	newState.LastHistoryDrop = uint64(time.Now().Unix())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		newState.FirstIndexedBlockNumber = 0
		newState.FirstIndexedBlockTimestamp = 0

		return &newState, nil
	}

	newState.FirstIndexedBlockNumber = firstBlock.GetBlockNumber()
	newState.FirstIndexedBlockTimestamp = firstBlock.GetTimestamp()

	db.log.Infof("deleted blocks up to index %d", newState.FirstIndexedBlockNumber)

	return &newState, nil
}

// Deletable is implemented by entities that support timestamp-based history pruning.
type Deletable interface {
	// TimestampField returns the database column name used for timestamp-based deletion.
	TimestampField() string
}

var validColumnName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// deleteInBatches removes rows from the entity's table where the timestamp column
// is older than deleteStart, processing up to deleteBatchSize rows per transaction.
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

		result := db.WithContext(ctx).Limit(deleteBatchSize).Where(
			fmt.Sprintf("%s < ?", col), deleteStart).
			Delete(entity)

		if result.Error != nil {
			return fmt.Errorf("failed to delete historic data in the DB: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return nil
		}
	}
}
