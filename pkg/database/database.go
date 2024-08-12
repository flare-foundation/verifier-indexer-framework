package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"gitlab.com/ryancollingham/flare-common/pkg/logger"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

const (
	tcp                  = "tcp"
	transactionBatchSize = 1000
	globalStateID        = 1
)

var log = logger.GetLogger()

func New(cfg *config.DB, entities ExternalEntities) (*DB, error) {
	db, err := connect(cfg)
	if err != nil {
		return nil, err
	}

	log.Debug("Connected to the DB")

	if err := db.AutoMigrate(State{}, entities.Block, entities.Transaction); err != nil {
		return nil, err
	}

	log.Debug("Migrated DB entities")

	return &DB{g: db}, err
}

func connect(cfg *config.DB) (*gorm.DB, error) {
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

type DB struct {
	g *gorm.DB
}

func (db *DB) GetState(ctx context.Context) (*State, error) {
	state := new(State)

	if err := db.g.WithContext(ctx).First(state, globalStateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}

		return nil, err
	}

	return state, nil
}

func InitState() State {
	return State{
		ID:        globalStateID,
		UpdatedAt: time.Now(),
	}
}

func (db *DB) StoreState(ctx context.Context, state *State) error {
	return db.g.Save(state).Error
}

func (db *DB) SaveBatch(ctx context.Context, items interface{}) error {
	return db.g.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(items).
		Error
}
