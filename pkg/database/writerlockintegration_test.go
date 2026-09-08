//go:build integration

package database

import (
	"testing"
	"time"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockCfg is the integration config with the writer lock on and the given wait.
func lockCfg(t *testing.T, waitSeconds int) *config.DB {
	t.Helper()

	cfg := *conflictTestDB(t)
	cfg.WriterLock = true
	cfg.WriterLockWaitSeconds = waitSeconds

	return &cfg
}

func lockTestDB(t *testing.T, cfg *config.DB) (*DB[dropBlock, conflictTx, struct{}], error) {
	t.Helper()

	return New(cfg, ExternalEntities[dropBlock, conflictTx, struct{}]{
		Block:       new(dropBlock),
		Transaction: new(conflictTx),
		Event:       new(struct{}),
	}, logger.Nop{})
}

func TestWriterLockRefusesASecondIndexer(t *testing.T) {
	first, err := lockTestDB(t, lockCfg(t, 0))
	require.NoError(t, err)
	defer first.Close() //nolint:errcheck // best-effort cleanup in a test

	_, err = lockTestDB(t, lockCfg(t, 1))
	require.Error(t, err)
	assert.ErrorContains(t, err, "another indexer holds the writer lock")
	assert.ErrorContains(t, err, "pid ", "the holder must be named")
	assert.ErrorContains(t, err, "writer_lock = false", "the error must carry the way out")
}

func TestWriterLockIsReleasedByClose(t *testing.T) {
	first, err := lockTestDB(t, lockCfg(t, 0))
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := lockTestDB(t, lockCfg(t, 0))
	require.NoError(t, err, "Close must release the lock, not leave it on a pooled session")
	require.NoError(t, second.Close())
}

func TestWriterLockIsReleasedWhenNewFails(t *testing.T) {
	cfg := lockCfg(t, 0)
	cfg.HistoryDrop = 200

	// rejected after the lock is taken: the block has no timestamp column to prune by
	_, err := New(cfg, ExternalEntities[noTimestampFieldBlock, conflictTx, struct{}]{
		Block:       new(noTimestampFieldBlock),
		Transaction: new(conflictTx),
		Event:       new(struct{}),
	}, logger.Nop{})
	require.Error(t, err)

	db, err := lockTestDB(t, lockCfg(t, 0))
	require.NoError(t, err, "a failed construction must release the lock")
	require.NoError(t, db.Close())
}

func TestWriterLockWaitsForTheHolderToExit(t *testing.T) {
	first, err := lockTestDB(t, lockCfg(t, 0))
	require.NoError(t, err)

	go func() {
		time.Sleep(time.Second)
		_ = first.Close()
	}()

	start := time.Now()
	second, err := lockTestDB(t, lockCfg(t, 5))
	require.NoError(t, err)
	require.NoError(t, second.Close())
	assert.GreaterOrEqual(t, time.Since(start), 900*time.Millisecond, "the second instance must have waited")
}

func TestWriterLockOffAllowsASecondIndexer(t *testing.T) {
	cfg := lockCfg(t, 0)
	cfg.WriterLock = false

	first, err := lockTestDB(t, cfg)
	require.NoError(t, err)
	defer first.Close() //nolint:errcheck // best-effort cleanup in a test

	second, err := lockTestDB(t, cfg)
	require.NoError(t, err)
	require.NoError(t, second.Close())
}
