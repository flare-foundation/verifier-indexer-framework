package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/config"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	tcp                  = "tcp"
	transactionBatchSize = 1000
	globalStateID        = 1
)

func New(cfg *config.DB) (*DB, error) {
	db, err := connect(cfg)
	if err != nil {
		return nil, err
	}

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
		return nil, err
	}

	return state, nil
}

func (db *DB) StoreState(ctx context.Context, state *State) error {
	return db.g.Save(state).Error
}

func (db *DB) SaveBlocksBatch(ctx context.Context, blocks []*Block) error {
	return errors.New("not implemented")
}

func (db *DB) SaveTransactionsBatch(ctx context.Context, transactions []*Transaction) error {
	return errors.New("not implemented")
}
