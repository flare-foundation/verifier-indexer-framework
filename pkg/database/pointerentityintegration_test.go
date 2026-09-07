//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPointerBlockEntityRoundTrip mirrors a consumer instantiating the
// framework with a pointer block type: New probes a nil *B, saves receive **B
// and the drop scans into a nil *B.
func TestPointerBlockEntityRoundTrip(t *testing.T) {
	ctx := context.Background()
	cfg := conflictTestDB(t)
	cfg.HistoryDrop = 200

	g, err := Connect(cfg)
	require.NoError(t, err)
	require.NoError(t, g.Migrator().DropTable(&ptrBlock{}, &ptrTx{}))
	t.Cleanup(func() {
		require.NoError(t, g.Migrator().DropTable(&ptrBlock{}, &ptrTx{}))
	})

	db, err := New(cfg, ExternalEntities[*ptrBlock, *ptrTx, struct{}]{
		Block:       new(*ptrBlock),
		Transaction: new(*ptrTx),
		Event:       new(struct{}),
	}, logger.Nop{})
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	blocks := make([]**ptrBlock, 0, 5)
	txs := make([]**ptrTx, 0, 5)
	for n := uint64(1); n <= 5; n++ {
		block := &ptrBlock{Hash: fmt.Sprintf("b%d", n), BlockNumber: n, Timestamp: n * 100}
		tx := &ptrTx{Hash: fmt.Sprintf("t%d", n), BlockNumber: n, Timestamp: n * 100}
		blocks = append(blocks, &block)
		txs = append(txs, &tx)
	}

	state := &State{
		ID:                         globalStateID,
		FirstIndexedBlockNumber:    1,
		FirstIndexedBlockTimestamp: 100,
		LastIndexedBlockNumber:     5,
		LastIndexedBlockTimestamp:  500,
		LastIndexedBlockUpdated:    1,
		LastChainBlockNumber:       6,
		LastChainBlockTimestamp:    600,
	}
	// The singleton row is shared with the other tests; reset it first.
	require.NoError(t, db.SaveState(ctx, state))
	require.NoError(t, db.SaveAllEntities(ctx, blocks, txs, nil, state))

	var count int64
	require.NoError(t, g.Model(&ptrBlock{}).Count(&count).Error)
	assert.Equal(t, int64(5), count)
	require.NoError(t, g.Model(&ptrTx{}).Count(&count).Error)
	assert.Equal(t, int64(5), count)

	// Overwrite on conflict must reach the row behind the double pointer.
	(*blocks[2]).Timestamp = 301
	require.NoError(t, db.SaveAllEntities(ctx, blocks[2:3], nil, nil, nil))

	var stored ptrBlock
	require.NoError(t, g.First(&stored, "hash = ?", "b3").Error)
	assert.Equal(t, uint64(301), stored.Timestamp)

	// deleteStart 300: blocks 1 and 2 go, block 3 survives at its new timestamp.
	dropped, err := db.DropHistoryIteration(ctx, state, 200, 500)
	require.NoError(t, err)
	assert.Equal(t, uint64(3), dropped.FirstIndexedBlockNumber)
	assert.Equal(t, uint64(301), dropped.FirstIndexedBlockTimestamp)

	require.NoError(t, g.Model(&ptrBlock{}).Count(&count).Error)
	assert.Equal(t, int64(3), count)
	require.NoError(t, g.Model(&ptrTx{}).Count(&count).Error)
	assert.Equal(t, int64(3), count)
}
