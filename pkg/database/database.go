package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

const (
	transactionBatchSize = 1000
	globalStateID        = 1
	globalVersionID      = 1
)

// ExternalEntities holds pointers to the user-defined block, transaction, and event types
// used for database schema migration and operations.
type ExternalEntities[B Block, T Transaction, E Event] struct {
	Block       *B
	Transaction *T
	Event       *E
}

// DB wraps a gorm.DB connection with generic type parameters for block, transaction, and event entities.
type DB[B Block, T Transaction, E Event] struct {
	g   *gorm.DB
	log logger.Logger
}

// Close closes the underlying database connection.
func (db *DB[B, T, E]) Close() error {
	sqlDB, err := db.g.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB for close: %w", err)
	}

	return sqlDB.Close()
}

// initState returns a new State with the global state primary key.
func initState() *State {
	return &State{
		ID: globalStateID,
	}
}

// InitVersion returns a new Version with the global version primary key.
func InitVersion() *Version {
	return &Version{
		ID: globalVersionID,
	}
}

// New connects to the database, optionally drops existing tables, runs migrations,
// and returns a ready-to-use DB instance.
// The caller should defer Close on the returned DB.
func New[B Block, T Transaction, E Event](cfg *config.DB, entities ExternalEntities[B, T, E], log logger.Logger) (*DB[B, T, E], error) {
	db, err := Connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	closeOnError := func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close() //nolint:errcheck // best-effort cleanup on initialization failure
		}
	}

	log.Debug("connected to the DB")

	if cfg.DropTableAtStart {
		log.Info("DB tables dropped at start")

		if isEmptyStruct[E]() {
			err = db.Migrator().DropTable(State{}, entities.Block, entities.Transaction)
		} else {
			err = db.Migrator().DropTable(State{}, entities.Block, entities.Transaction, entities.Event)
		}
		if err != nil {
			closeOnError()
			return nil, fmt.Errorf("failed to drop tables: %w", err)
		}
	}

	if isEmptyStruct[E]() {
		err = db.AutoMigrate(State{}, Version{}, entities.Block, entities.Transaction)
	} else {
		err = db.AutoMigrate(State{}, Version{}, entities.Block, entities.Transaction, entities.Event)
	}
	if err != nil {
		closeOnError()
		return nil, fmt.Errorf("failed to auto-migrate tables: %w", err)
	}

	log.Debug("migrated DB entities")

	return &DB[B, T, E]{g: db, log: log}, nil
}

// isEmptyStruct reports whether the type parameter T is struct{}.
func isEmptyStruct[T any]() bool {
	_, ok := any(*new(T)).(struct{})
	return ok
}

// Connect opens a PostgreSQL connection using the provided configuration and
// applies connection pool settings.
func Connect(cfg *config.DB) (*gorm.DB, error) {
	dsn := formatDSN(cfg)

	gormLogLevel := getGormLogLevel(cfg)
	gormCfg := gorm.Config{
		Logger:          gormlogger.Default.LogMode(gormLogLevel),
		CreateBatchSize: transactionBatchSize,
	}

	db, err := gorm.Open(postgres.Open(dsn), &gormCfg)
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetimeSeconds > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetimeSeconds) * time.Second)
	}

	return db, nil
}

// getGormLogLevel returns the gorm log level based on the database configuration.
func getGormLogLevel(cfg *config.DB) gormlogger.LogLevel {
	if cfg.LogQueries {
		return gormlogger.Info
	}

	return gormlogger.Silent
}

// formatDSN builds a PostgreSQL connection string from the database configuration.
func formatDSN(cfg *config.DB) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.Username, cfg.Password),
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:   cfg.DBName,
	}

	return u.String()
}

// GetState retrieves the current indexer state from the database, returning a
// fresh initial state if no record exists.
func (db *DB[B, T, E]) GetState(ctx context.Context) (*State, error) {
	state := new(State)

	if err := db.g.WithContext(ctx).First(state, globalStateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return initState(), nil
		}

		return nil, err
	}

	return state, nil
}

// SaveAllEntities persists blocks, transactions, events, and indexer state in a
// single database transaction. Rows that already exist are overwritten: entity
// rows are deterministically derived from immutable chain data, so re-indexing
// a range repairs values previously derived by older code instead of freezing
// them forever.
func (db *DB[B, T, E]) SaveAllEntities(
	ctx context.Context, blocks []*B, transactions []*T, events []*E, state *State,
) error {
	return db.g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(blocks) != 0 {
			err := tx.Clauses(clause.OnConflict{UpdateAll: true}).
				Create(blocks).
				Error
			if err != nil {
				return err
			}
		}

		if len(transactions) != 0 {
			err := tx.Clauses(clause.OnConflict{UpdateAll: true}).
				Create(transactions).
				Error
			if err != nil {
				return err
			}
		}

		if !isEmptyStruct[E]() && len(events) != 0 {
			err := tx.Clauses(clause.OnConflict{UpdateAll: true}).
				Create(events).
				Error
			if err != nil {
				return err
			}
		}

		if state != nil {
			err := tx.Save(state).Error
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// SaveVersion persists the given version record to the database.
func (db *DB[B, T, E]) SaveVersion(
	ctx context.Context, version *Version,
) error {
	return db.g.WithContext(ctx).Save(version).Error
}
