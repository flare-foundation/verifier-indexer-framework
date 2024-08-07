package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"gitlab.com/ryancollingham/flare-common/pkg/logger"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/config"
	gormmysql "gorm.io/driver/mysql"
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

func New(cfg *config.DB) (*DB, error) {
	db, err := connect(cfg)
	if err != nil {
		return nil, err
	}

	log.Debug("Connected to the DB")

	if err := db.AutoMigrate(entities...); err != nil {
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

	return gorm.Open(gormmysql.Open(dsn), &gormCfg)
}

func getGormLogLevel(cfg *config.DB) gormlogger.LogLevel {
	if cfg.LogQueries {
		return gormlogger.Info
	}

	return gormlogger.Silent
}

func formatDSN(cfg *config.DB) string {
	dbCfg := mysql.Config{
		User:                 cfg.Username,
		Passwd:               cfg.Password,
		Net:                  "tcp",
		Addr:                 fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		DBName:               cfg.DBName,
		AllowNativePasswords: true,
		ParseTime:            true,
	}

	return dbCfg.FormatDSN()
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

func (db *DB) SaveBlocksBatch(ctx context.Context, blocks []*Block) error {
	return db.saveBatch(ctx, blocks)
}

func (db *DB) SaveTransactionsBatch(ctx context.Context, transactions []*Transaction) error {
	return db.saveBatch(ctx, transactions)
}

func (db *DB) saveBatch(ctx context.Context, items interface{}) error {
	return db.g.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(items).
		Error
}
