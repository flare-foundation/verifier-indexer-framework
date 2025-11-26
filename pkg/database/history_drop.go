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

func (db *DB[B, T]) DropHistoryIteration(
	ctx context.Context,
	state *State,
	intervalSeconds, lastBlockTime uint64,
) (*State, error) {
	deleteStart := lastBlockTime - intervalSeconds

	// Delete in specified order to not break foreign keys.
	newState := *state

	var b B
	deleteOrder := b.HistoryDropOrder()

	for _, entity := range deleteOrder {
		if err := deleteInBatches(db.g, deleteStart, entity); err != nil {
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
	TimestampQuery() string
}

func deleteInBatches(db *gorm.DB, deleteStart uint64, entity Deletable) error {
	for {
		result := db.Limit(deleteBatchSize).Where(entity.TimestampQuery(), deleteStart).Delete(entity)

		if result.Error != nil {
			return errors.Wrap(result.Error, "Failed to delete historic data in the DB")
		}

		if result.RowsAffected == 0 {
			return nil
		}
	}
}
