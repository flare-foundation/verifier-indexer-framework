//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/require"
)

// nullTx has the shape that made every batch's SQL text unique: nullable
// columns gorm writes as the literal DEFAULT when zero.
type nullTx struct {
	Hash        string `gorm:"primaryKey;type:varchar(64)"`
	BlockNumber uint64
	Timestamp   uint64  `gorm:"index"`
	Reference   *string `gorm:"type:varchar(64);default:null"`
	Amount      *uint64 `gorm:"default:null"`
	Tag         *uint32 `gorm:"default:null"`
}

// TestBatchSavesLeaveNoServerSidePreparedStatements drives saves through every
// null pattern on one connection and counts what the session keeps prepared.
func TestBatchSavesLeaveNoServerSidePreparedStatements(t *testing.T) {
	ctx := context.Background()

	cfg := *conflictTestDB(t)
	cfg.WriterLock = true
	// one connection for the lock, one for everything else, so the count below
	// runs on the session that executed the saves
	cfg.MaxOpenConns = 2

	db, err := New(&cfg, ExternalEntities[dropBlock, nullTx, struct{}]{
		Block:       new(dropBlock),
		Transaction: new(nullTx),
		Event:       new(struct{}),
	}, logger.Nop{})
	require.NoError(t, err)
	defer db.Close()                                       //nolint:errcheck // best-effort cleanup in a test
	defer db.g.Migrator().DropTable(nullTx{}, dropBlock{}) //nolint:errcheck // best-effort cleanup in a test

	ref, amount, tag := "ref", uint64(7), uint32(3)
	for pattern := range 8 {
		tx := &nullTx{Hash: fmt.Sprintf("%064d", pattern), BlockNumber: uint64(pattern), Timestamp: 100}
		if pattern&1 != 0 {
			tx.Reference = &ref
		}

		if pattern&2 != 0 {
			tx.Amount = &amount
		}

		if pattern&4 != 0 {
			tx.Tag = &tag
		}

		block := &dropBlock{BlockNumber: uint64(pattern), Timestamp: 100}
		require.NoError(t, db.SaveAllEntities(ctx, []*dropBlock{block}, []*nullTx{tx}, nil, nil))
	}

	var prepared int64
	require.NoError(t, db.g.WithContext(ctx).Raw("SELECT count(*) FROM pg_prepared_statements").Scan(&prepared).Error)
	require.Zero(t, prepared, "cache_describe must leave nothing prepared server-side; cache_statement would leave one statement per null pattern")
}
