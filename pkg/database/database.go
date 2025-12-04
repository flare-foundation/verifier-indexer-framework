package database

import (
	"context"
	"fmt"
	"net/url"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/pkg/errors"

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

type ExternalEntities[B Block, T Transaction, E Event] struct {
	Block       *B
	Transaction *T
	Event       *E
}

type DB[B Block, T Transaction, E Event] struct {
	g *gorm.DB
}

func initState() *State {
	return &State{
		ID: globalStateID,
	}
}

func InitVersion() *Version {
	return &Version{
		ID: globalVersionID,
	}
}

func New[B Block, T Transaction, E Event](cfg *config.DB, entities ExternalEntities[B, T, E]) (*DB[B, T, E], error) {
	db, err := Connect(cfg)
	if err != nil {
		return nil, err
	}

	logger.Debug("connected to the DB")

	if cfg.DropTableAtStart {
		logger.Info("DB tables dropped at start")

		if isEmptyStruct[E]() {
			err = db.Migrator().DropTable(State{}, entities.Block, entities.Transaction)
		} else {
			err = db.Migrator().DropTable(State{}, entities.Block, entities.Transaction, entities.Event)
		}
		if err != nil {
			return nil, err
		}
	}

	if isEmptyStruct[E]() {
		err = db.AutoMigrate(State{}, Version{}, entities.Block, entities.Transaction)
	} else {
		err = db.AutoMigrate(State{}, Version{}, entities.Block, entities.Transaction, entities.Event)
	}
	if err != nil {
		return nil, err
	}

	logger.Debug("migrated DB entities")

	return &DB[B, T, E]{g: db}, err
}

func isEmptyStruct[T any]() bool {
	_, ok := any(*new(T)).(struct{})
	return ok
}

func Connect(cfg *config.DB) (*gorm.DB, error) {
	dsn := formatDSN(cfg)

	gormLogLevel := getGormLogLevel(cfg)
	gormCfg := gorm.Config{
		Logger:          gormlogger.Default.LogMode(gormLogLevel),
		CreateBatchSize: transactionBatchSize,
	}

	return gorm.Open(postgres.Open(dsn), &gormCfg)
}

func getGormLogLevel(cfg *config.DB) gormlogger.LogLevel {
	if cfg.LogQueries {
		return gormlogger.Info
	}

	return gormlogger.Silent
}

func formatDSN(cfg *config.DB) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.Username, cfg.Password),
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:   cfg.DBName,
	}

	return u.String()
}

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

func (db *DB[B, T, E]) SaveAllEntities(
	ctx context.Context, blocks []*B, transactions []*T, events []*E, state *State,
) error {
	return db.g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(blocks) != 0 {
			err := tx.Clauses(clause.OnConflict{DoNothing: true}).
				Create(blocks).
				Error
			if err != nil {
				return err
			}
		}

		if len(transactions) != 0 {
			err := tx.Clauses(clause.OnConflict{DoNothing: true}).
				Create(transactions).
				Error
			if err != nil {
				return err
			}
		}

		if !isEmptyStruct[E]() && len(events) != 0 {
			err := tx.Clauses(clause.OnConflict{DoNothing: true}).
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

func (db *DB[B, T, E]) SaveVersion(
	ctx context.Context, version *Version,
) error {
	return db.g.WithContext(ctx).Save(version).Error
}
