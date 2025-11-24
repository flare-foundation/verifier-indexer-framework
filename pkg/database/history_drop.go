package database

import (
	"context"
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
	deleteStart := lastBlockTime - intervalSeconds

	// Delete in specified order to not break foreign keys.
	var deleteOrder []any
	if isEmptyStruct[E]() {
		deleteOrder = []any{
			new(T),
			new(B),
		}
	} else {
		deleteOrder = []any{
			new(E),
			new(T),
			new(B),
		}
	}
	newState := *state

	err := db.g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, entity := range deleteOrder {
			if err := deleteInBatches(tx, deleteStart, entity); err != nil {
				return err
			}
		}

		var firstBlock B
		err := tx.Order("block_number").First(&firstBlock).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.Wrap(err, "Failed to get first block in the DB")
		}

		newState.LastHistoryDrop = uint64(time.Now().Unix())
		if errors.Is(err, gorm.ErrRecordNotFound) {
			newState.FirstIndexedBlockNumber = 0
			newState.FirstIndexedBlockTimestamp = 0
			newState.LastIndexedBlockNumber = 0
			newState.LastIndexedBlockTimestamp = 0

			return nil
		}

		newState.FirstIndexedBlockNumber = firstBlock.GetBlockNumber()
		newState.FirstIndexedBlockTimestamp = firstBlock.GetTimestamp()

		if err := tx.Save(newState).Error; err != nil {
			return errors.Wrap(err, "Failed to update state in the DB")
		}

		return nil
	})

	logger.Infof("deleted blocks up to index %d", newState.FirstIndexedBlockNumber)

	return &newState, err
}

func deleteInBatches(db *gorm.DB, deleteStart uint64, entity interface{}) error {
	for {
		result := db.Limit(deleteBatchSize).Where("timestamp < ?", deleteStart).Delete(&entity)

		if result.Error != nil {
			return errors.Wrap(result.Error, "Failed to delete historic data in the DB")
		}

		if result.RowsAffected == 0 {
			return nil
		}
	}
}
