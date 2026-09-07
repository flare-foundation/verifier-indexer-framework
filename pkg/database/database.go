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
	// a pointer keeps DB comparable, as on v1.1.1: the clauses hold slices
	conflicts *entityConflicts
}

// stateTable returns the state table's name as the naming strategy derives it.
// Resolved on use so no construction path can leave it unset.
func (db *DB[B, T, E]) stateTable() string {
	return db.g.NamingStrategy.TableName("State")
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

	conflicts, err := deriveConflicts[B, T, E](db, log)
	if err != nil {
		closeOnError()
		return nil, err
	}

	// Reject an unusable block entity now, not a retention window from now.
	if cfg.HistoryDrop > 0 {
		if _, err := blockTimestampField(db.NamingStrategy, *new(B)); err != nil {
			closeOnError()
			return nil, err
		}

		if err := validateHistoryDropOrder(db.NamingStrategy, *new(B)); err != nil {
			closeOnError()
			return nil, err
		}
	}

	return &DB[B, T, E]{g: db, log: log, conflicts: &conflicts}, nil
}

// deriveConflicts builds each entity's ON CONFLICT clause, keeping v1.1.1's
// skip where the schema allows no overwrite. Runs after AutoMigrate: same
// schema it validated.
func deriveConflicts[B Block, T Transaction, E Event](db *gorm.DB, log logger.Logger) (entityConflicts, error) {
	var conflicts entityConflicts

	type namedModel struct {
		name   string
		model  any
		target *clause.OnConflict
	}

	entities := []namedModel{
		{"block", new(B), &conflicts.block},
		{"transaction", new(T), &conflicts.transaction},
	}

	// An empty event type has no table; see SaveAllEntities.
	if !isEmptyStruct[E]() {
		entities = append(entities, namedModel{"event", new(E), &conflicts.event})
	}

	for _, entity := range entities {
		conflict, skipReason, err := conflictClause(db.NamingStrategy, entity.model)
		if err != nil {
			return entityConflicts{}, fmt.Errorf("%s entity: %w", entity.name, err)
		}

		if skipReason != "" {
			log.Warnf(
				"%s entity has %s: conflicting rows are skipped instead of overwritten, "+
					"so re-indexing cannot repair them; make the chain identity the primary key to overwrite instead",
				entity.name, skipReason,
			)
		}

		*entity.target = conflict
	}

	return conflicts, nil
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
// single database transaction. Entity rows are overwritten on primary-key
// conflict, so re-indexing repairs values older code derived; a column the
// current code leaves empty is reset to its default. An entity the schema does
// not let the framework overwrite keeps v1.1.1's skip (New warns at startup).
//
// The state row is upserted so the indexing loop only ever writes the columns
// it owns. On conflict it updates the chain- and last-indexed-progress columns
// and establishes the first-indexed boundary only from the empty sentinel
// (raise-only), never lowering an already-set boundary and never touching
// last_history_drop. This keeps a regular save from clobbering a boundary that
// a concurrent history drop raised (and persisted) before deleting rows below
// it; callers holding the authoritative boundary use SaveState instead.
func (db *DB[B, T, E]) SaveAllEntities(
	ctx context.Context, blocks []*B, transactions []*T, events []*E, state *State,
) error {
	return db.g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(blocks) != 0 {
			err := tx.Clauses(db.conflicts.block).
				Create(blocks).
				Error
			if err != nil {
				return err
			}
		}

		if len(transactions) != 0 {
			err := tx.Clauses(db.conflicts.transaction).
				Create(transactions).
				Error
			if err != nil {
				return err
			}
		}

		if !isEmptyStruct[E]() && len(events) != 0 {
			err := tx.Clauses(db.conflicts.event).
				Create(events).
				Error
			if err != nil {
				return err
			}
		}

		if state != nil {
			err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: indexingStateAssignments(db.stateTable()),
			}).Create(state).Error
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// indexingStateAssignments returns the ON CONFLICT update set used by the
// indexing loop for the singleton state row. It writes the chain- and
// last-indexed-progress columns the loop owns, raises the first-indexed
// boundary only from the empty sentinel (so a stale carried-forward value can
// never overwrite a higher boundary persisted by a concurrent history drop),
// and deliberately omits last_history_drop, which the history drop owns.
//
// The first-indexed CASE qualifies the existing-row reference with the "states"
// table name (the gorm-derived table for State): inside ON CONFLICT DO UPDATE an
// unqualified column is ambiguous between the target row and excluded.
func indexingStateAssignments(table string) clause.Set {
	// The boundary is establishable only while the caller's view of the drop is
	// current: a save carrying a pre-drop boundary would otherwise resurrect it
	// through the empty sentinel a drop had just written.
	establishable := fmt.Sprintf(
		"%[1]s.first_indexed_block_number = 0 AND %[1]s.last_history_drop = excluded.last_history_drop", table)

	return clause.Assignments(map[string]any{
		"last_chain_block_number":      gorm.Expr("excluded.last_chain_block_number"),
		"last_chain_block_timestamp":   gorm.Expr("excluded.last_chain_block_timestamp"),
		"last_chain_block_updated":     gorm.Expr("excluded.last_chain_block_updated"),
		"last_indexed_block_number":    gorm.Expr("excluded.last_indexed_block_number"),
		"last_indexed_block_timestamp": gorm.Expr("excluded.last_indexed_block_timestamp"),
		"last_indexed_block_updated":   gorm.Expr("excluded.last_indexed_block_updated"),
		"first_indexed_block_number": gorm.Expr(fmt.Sprintf(
			"CASE WHEN %s THEN excluded.first_indexed_block_number ELSE %s.first_indexed_block_number END",
			establishable, table)),
		"first_indexed_block_timestamp": gorm.Expr(fmt.Sprintf(
			"CASE WHEN %s THEN excluded.first_indexed_block_timestamp ELSE %s.first_indexed_block_timestamp END",
			establishable, table)),
	})
}

// SaveState overwrites every column of the state row. Only for callers holding
// the authoritative first-indexed boundary, where SaveAllEntities' raise-only
// guard must not apply.
func (db *DB[B, T, E]) SaveState(ctx context.Context, state *State) error {
	return db.g.WithContext(ctx).Save(state).Error
}

// SaveChainTip persists the chain-tip columns the poll owns and nothing else,
// inserting the state row on a fresh database. Written after every successful
// poll so the row keeps moving while no batch is saved.
func (db *DB[B, T, E]) SaveChainTip(ctx context.Context, state *State) error {
	return db.g.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"last_chain_block_number", "last_chain_block_timestamp", "last_chain_block_updated",
		}),
	}).Create(&State{
		ID:                      globalStateID,
		LastChainBlockNumber:    state.LastChainBlockNumber,
		LastChainBlockTimestamp: state.LastChainBlockTimestamp,
		LastChainBlockUpdated:   state.LastChainBlockUpdated,
	}).Error
}

// SaveVersion persists the given version record to the database.
func (db *DB[B, T, E]) SaveVersion(
	ctx context.Context, version *Version,
) error {
	return db.g.WithContext(ctx).Save(version).Error
}
