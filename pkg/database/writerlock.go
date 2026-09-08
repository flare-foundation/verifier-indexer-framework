package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"gorm.io/gorm"
)

// Advisory locks are scoped to a database, so one constant key pair means one
// indexer per database.
const (
	writerLockClass = 0x494e4458 // "INDX"
	writerLockObj   = 1

	writerLockPoll    = time.Second
	writerLockRelease = 5 * time.Second
)

var (
	tryWriterLockSQL = fmt.Sprintf("SELECT pg_try_advisory_lock(%d, %d)", writerLockClass, writerLockObj)
	unlockWriterSQL  = fmt.Sprintf("SELECT pg_advisory_unlock(%d, %d)", writerLockClass, writerLockObj)
	writerHolderSQL  = fmt.Sprintf(`SELECT a.pid, a.application_name, host(a.client_addr), a.backend_start
		FROM pg_locks l JOIN pg_stat_activity a ON a.pid = l.pid
		WHERE l.locktype = 'advisory' AND l.classid = %d AND l.objid = %d AND l.granted
		LIMIT 1`, writerLockClass, writerLockObj)
)

// writerLock is the dedicated session holding the advisory lock for the life
// of the process. sql.DB.Close skips a checked-out connection and Conn.Close
// hands the session back to the pool with its locks, so release is explicit.
type writerLock struct {
	conn *sql.Conn
}

// acquireWriterLock takes the writer lock on a dedicated connection, waiting up
// to wait for another indexer to release it. The failure names the holder.
func acquireWriterLock(g *gorm.DB, wait time.Duration, log logger.Logger) (*writerLock, error) {
	sqlDB, err := g.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB for the writer lock: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), wait+writerLockRelease)
	defer cancel()

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open the writer lock connection: %w", err)
	}

	deadline := time.Now().Add(wait)
	warned := false

	for {
		var got bool
		if err = conn.QueryRowContext(ctx, tryWriterLockSQL).Scan(&got); err != nil {
			conn.Close() //nolint:errcheck // best-effort cleanup on failure
			return nil, fmt.Errorf("failed to take the writer lock: %w", err)
		}

		if got {
			return &writerLock{conn: conn}, nil
		}

		holder := writerLockHolder(ctx, conn)
		if !time.Now().Before(deadline) {
			conn.Close() //nolint:errcheck // best-effort cleanup on failure
			return nil, fmt.Errorf("another indexer holds the writer lock on this database (%s): "+
				"run one indexer per database, or set writer_lock = false for a deliberate second writer", holder)
		}

		if !warned {
			log.Warnf("another indexer holds the writer lock on this database (%s); waiting up to %s for it to exit", holder, wait)
			warned = true
		}

		time.Sleep(writerLockPoll)
	}
}

// writerLockHolder describes the session holding the lock. pg_stat_activity
// hides other roles' details, so every column may be null.
func writerLockHolder(ctx context.Context, conn *sql.Conn) string {
	var (
		pid   sql.NullInt64
		app   sql.NullString
		addr  sql.NullString
		since sql.NullTime
	)

	if err := conn.QueryRowContext(ctx, writerHolderSQL).Scan(&pid, &app, &addr, &since); err != nil {
		return "holder unknown"
	}

	desc := fmt.Sprintf("pid %d", pid.Int64)
	if app.Valid && app.String != "" {
		desc += " " + app.String
	}

	if addr.Valid {
		desc += " from " + addr.String
	}

	if since.Valid {
		desc += " since " + since.Time.UTC().Format(time.RFC3339)
	}

	return desc
}

// release unlocks and closes the dedicated session. Nil-safe, so a failed
// construction can call it unconditionally.
func (l *writerLock) release() error {
	if l == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), writerLockRelease)
	defer cancel()

	_, err := l.conn.ExecContext(ctx, unlockWriterSQL)

	return errors.Join(err, l.conn.Close())
}
