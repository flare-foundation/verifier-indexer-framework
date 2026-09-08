//go:build integration

package database

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const testConfigFile = "../../tests/test_config.toml"

// sqlCapture records DELETE statements executed through the gorm session.
type sqlCapture struct {
	mu      sync.Mutex
	deletes []string
}

func (c *sqlCapture) LogMode(gormlogger.LogLevel) gormlogger.Interface { return c }
func (c *sqlCapture) Info(context.Context, string, ...any)             {}
func (c *sqlCapture) Warn(context.Context, string, ...any)             {}
func (c *sqlCapture) Error(context.Context, string, ...any)            {}

func (c *sqlCapture) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	if strings.HasPrefix(sql, "DELETE") {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.deletes = append(c.deletes, sql)
	}
}

// prunable is a minimal Deletable entity for exercising deleteInBatches.
type prunable struct {
	ID        uint64 `gorm:"primaryKey"`
	Timestamp uint64 `gorm:"index"`
}

func (p prunable) TimestampField() string { return "timestamp" }

func TestDeleteInBatches(t *testing.T) {
	db, err := Connect(conflictTestDB(t))
	require.NoError(t, err)

	require.NoError(t, db.Migrator().DropTable(&prunable{}))
	require.NoError(t, db.AutoMigrate(&prunable{}))

	// 2490 rows below the deletion boundary, 10 above it.
	rows := make([]*prunable, 0, 2500)
	for i := uint64(1); i <= 2500; i++ {
		ts := uint64(100)
		if i > 2490 {
			ts = 1000
		}
		rows = append(rows, &prunable{ID: i, Timestamp: ts})
	}
	require.NoError(t, db.CreateInBatches(rows, 1000).Error)

	capture := &sqlCapture{}
	session := db.Session(&gorm.Session{Logger: capture})

	_, err = deleteInBatches(context.Background(), session, 500, prunable{})
	require.NoError(t, err)

	var remaining int64
	require.NoError(t, db.Model(&prunable{}).Count(&remaining).Error)
	assert.Equal(t, int64(10), remaining)

	// 2490 deletable rows in batches of 1000: 1000 + 1000 + 490 + the final
	// zero-row statement that terminates the loop.
	require.Len(t, capture.deletes, 4)
	for _, sql := range capture.deletes {
		assert.Contains(t, sql, "ctid IN", "batched delete must select the batch by ctid")
	}

	require.NoError(t, db.Migrator().DropTable(&prunable{}))
}
