package database

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// Only delete up to 1000 items in a single DB transaction to avoid lock
// timeouts.
const deleteBatchSize = 1000

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
		return nil, errors.Wrap(err, "Failed to get first block in the DB")
	}

	newState.LastHistoryDrop = uint64(time.Now().Unix())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		newState.FirstIndexedBlockNumber = 0
		newState.FirstIndexedBlockTimestamp = 0

		return &newState, nil
	}

	newState.FirstIndexedBlockNumber = firstBlock.GetBlockNumber()
	newState.FirstIndexedBlockTimestamp = firstBlock.GetTimestamp()

	logger.Infof("deleted blocks up to index %d", newState.FirstIndexedBlockNumber)

	return &newState, err
}

type Deletable interface {
	TimestampField() string
}

var validColumnName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

func deleteInBatches(ctx context.Context, db *gorm.DB, deleteStart uint64, entity Deletable) error {
	col := entity.TimestampField()
	if !validColumnName.MatchString(col) {
		return fmt.Errorf("invalid column name: %q", col)
	}

	for {
		result := db.WithContext(ctx).Limit(deleteBatchSize).Where(
			fmt.Sprintf("%s < ?", col), deleteStart).
			Delete(entity)

		if result.Error != nil {
			return errors.Wrap(result.Error, "Failed to delete historic data in the DB")
		}

		if result.RowsAffected == 0 {
			return nil
		}
	}
}
