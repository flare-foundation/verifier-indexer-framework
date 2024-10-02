package database

import (
	"context"

	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// Only delete up to 1000 items in a single DB transaction to avoid lock
// timeouts.
const deleteBatchSize = 1000

func DropHistoryIteration[B Block, T Transaction](
	ctx context.Context, db *gorm.DB, intervalSeconds, lastBlockTime uint64,
) (uint64, error) {
	deleteStart := lastBlockTime - intervalSeconds

	// Delete in specified order to not break foreign keys.
	var deleteOrder []interface{} = []interface{}{
		new(B),
		new(T),
	}
	var firstBlockNumber uint64

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = GlobalStates.Delete(tx, FirstDatabaseIndexState)
			if err != nil {
				return errors.Wrap(err, "Failed to update state in the DB")
			}
			err = GlobalStates.Delete(tx, LastDatabaseIndexState)
			if err != nil {
				return errors.Wrap(err, "Failed to update state in the DB")
			}

			return nil
		}

		firstBlockNumber = firstBlock.GetBlockNumber()
		firstBlockTimestamp := firstBlock.GetTimestamp()

		err = GlobalStates.Update(tx, FirstDatabaseIndexState, firstBlockNumber, firstBlockTimestamp)
		if err != nil {
			return errors.Wrap(err, "Failed to update state in the DB")
		}

		log.Infof("deleted blocks up to index %d", firstBlockNumber)

		return nil
	})

	return firstBlockNumber, err
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
