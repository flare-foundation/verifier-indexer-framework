//go:build integration

package database

import (
	"context"
	"os"
	"testing"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/require"
)

// conflictBlock is the parent entity for the conflict tests.
type conflictBlock struct {
	BlockNumber uint64 `gorm:"primaryKey"`
	Timestamp   uint64 `gorm:"index"`
}

func (b conflictBlock) GetBlockNumber() uint64 { return b.BlockNumber }
func (b conflictBlock) GetTimestamp() uint64   { return b.Timestamp }
func (b conflictBlock) TimestampField() string { return "timestamp" }
func (b conflictBlock) HistoryDropOrder() []Deletable {
	return []Deletable{conflictTx{}, conflictBlock{}}
}

// conflictTx carries a default:null column — the class UpdateAll drops.
type conflictTx struct {
	Hash             string `gorm:"primaryKey"`
	BlockNumber      uint64 `gorm:"index"`
	Response         string `gorm:"type:varchar"`
	PaymentReference string `gorm:"index;default:null"`
}

func (t conflictTx) TimestampField() string { return "timestamp" }

// noPKBlock has no primary key, so there is no conflict target to infer.
type noPKBlock struct {
	BlockNumber uint64
	Timestamp   uint64
}

func (b noPKBlock) GetBlockNumber() uint64        { return b.BlockNumber }
func (b noPKBlock) GetTimestamp() uint64          { return b.Timestamp }
func (b noPKBlock) HistoryDropOrder() []Deletable { return nil }

func conflictTestDB(t *testing.T) *config.DB {
	t.Helper()

	cfgPath := os.Getenv("CONFIG_FILE")
	if cfgPath == "" {
		cfgPath = testConfigFile
	}

	cfg := config.Base{}
	require.NoError(t, config.ReadFile(cfgPath, &cfg))
	require.NoError(t, cfg.ApplyEnvOverrides())

	// These tests own their tables; the shared state row is another package's.
	cfg.DB.DropTableAtStart = false

	return &cfg.DB
}

// surrogateTx is the v1.1.1 shape the overwrite cannot serve: a sequence id and
// a unique hash, so re-indexing conflicts on hash with a fresh id.
type surrogateTx struct {
	ID          uint   `gorm:"primaryKey;autoIncrement"`
	Hash        string `gorm:"uniqueIndex"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

// TestNewAcceptsEntityWithoutPrimaryKey pins the v1.1.1 fallback: such an
// entity starts, with the target-free skip PostgreSQL accepts.
func TestNewAcceptsEntityWithoutPrimaryKey(t *testing.T) {
	// noPKBlock carries no timestamp column, so history drop stays off here.
	cfg := conflictTestDB(t)
	cfg.HistoryDrop = 0

	db, err := New(cfg, ExternalEntities[noPKBlock, conflictTx, struct{}]{
		Block:       new(noPKBlock),
		Transaction: new(conflictTx),
		Event:       new(struct{}),
	}, logger.Nop{})
	require.NoError(t, err)
	defer db.Close()                                           //nolint:errcheck // best-effort cleanup in a test
	defer db.g.Migrator().DropTable(noPKBlock{}, conflictTx{}) //nolint:errcheck // best-effort cleanup in a test

	block := noPKBlock{BlockNumber: 1, Timestamp: 1000}
	require.NoError(t, db.SaveAllEntities(context.Background(), []*noPKBlock{&block}, nil, nil, nil))
}

// TestSaveAllEntitiesSkipsConflictBeyondPrimaryKey pins the v1.1.1 semantic for
// the shape that crashed with a unique violation when overwriting on id.
func TestSaveAllEntitiesSkipsConflictBeyondPrimaryKey(t *testing.T) {
	ctx := context.Background()

	db, err := New(conflictTestDB(t), ExternalEntities[conflictBlock, surrogateTx, struct{}]{
		Block:       new(conflictBlock),
		Transaction: new(surrogateTx),
		Event:       new(struct{}),
	}, logger.Nop{})
	require.NoError(t, err)
	defer db.Close()                                                //nolint:errcheck // best-effort cleanup in a test
	defer db.g.Migrator().DropTable(surrogateTx{}, conflictBlock{}) //nolint:errcheck // best-effort cleanup in a test

	require.NoError(t, db.g.Migrator().DropTable(surrogateTx{}, conflictBlock{}))
	require.NoError(t, db.g.AutoMigrate(&conflictBlock{}, &surrogateTx{}))

	block := conflictBlock{BlockNumber: 1, Timestamp: 1000}
	first := surrogateTx{Hash: "a", BlockNumber: 1, Timestamp: 1000}
	require.NoError(t, db.SaveAllEntities(ctx, []*conflictBlock{&block}, []*surrogateTx{&first}, nil, nil))

	// Same hash, fresh sequence id: v1.1.1 skipped it, the overwrite raised.
	again := surrogateTx{Hash: "a", BlockNumber: 2, Timestamp: 1000}
	require.NoError(t, db.SaveAllEntities(ctx, []*conflictBlock{&block}, []*surrogateTx{&again}, nil, nil))

	var stored []surrogateTx
	require.NoError(t, db.g.Find(&stored).Error)
	require.Len(t, stored, 1)
	require.Equal(t, uint64(1), stored[0].BlockNumber, "a skipped row keeps its original value")
}

// TestSaveAllEntitiesRepairsDefaultNullColumn pins the update set to the schema
// rather than the batch.
func TestSaveAllEntitiesRepairsDefaultNullColumn(t *testing.T) {
	ctx := context.Background()

	db, err := New(conflictTestDB(t), ExternalEntities[conflictBlock, conflictTx, struct{}]{
		Block:       new(conflictBlock),
		Transaction: new(conflictTx),
		Event:       new(struct{}),
	}, logger.Nop{})
	require.NoError(t, err)

	// Defers run before t.Cleanup, so dropping there would hit a closed pool.
	defer db.Close()                                               //nolint:errcheck // best-effort cleanup in a test
	defer db.g.Migrator().DropTable(conflictTx{}, conflictBlock{}) //nolint:errcheck // best-effort cleanup in a test

	require.NoError(t, db.g.Migrator().DropTable(conflictTx{}, conflictBlock{}))
	require.NoError(t, db.g.AutoMigrate(&conflictBlock{}, &conflictTx{}))

	block := conflictBlock{BlockNumber: 1, Timestamp: 1000}
	stale := conflictTx{Hash: "a", BlockNumber: 1, Response: "old", PaymentReference: "deadbeef"}
	require.NoError(t, db.SaveAllEntities(ctx, []*conflictBlock{&block}, []*conflictTx{&stale}, nil, nil))

	// Fresh struct per read: gorm leaves a field untouched when it scans NULL.
	readTx := func(hash string) conflictTx {
		var stored conflictTx
		require.NoError(t, db.g.First(&stored, "hash = ?", hash).Error)

		return stored
	}

	require.Equal(t, "deadbeef", readTx("a").PaymentReference, "precondition: the column starts populated")

	// Re-index the same row with a batch in which no row sets PaymentReference.
	repaired := conflictTx{Hash: "a", BlockNumber: 1, Response: "new"}
	other := conflictTx{Hash: "b", BlockNumber: 1, Response: "other"}
	require.NoError(t, db.SaveAllEntities(
		ctx, []*conflictBlock{&block}, []*conflictTx{&repaired, &other}, nil, nil,
	))

	stored := readTx("a")
	require.Equal(t, "new", stored.Response, "a plain column must be overwritten")
	require.Empty(t, stored.PaymentReference,
		"a default:null column must be repaired even when no row in the batch sets it")
}
