package database

import (
	"fmt"

	"github.com/go-sql-driver/mysql"
	"gitlab.com/ryancollingham/flare-indexer-framework/internal/config"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	tcp                  = "tcp"
	transactionBatchSize = 1000
)

func New(cfg *config.DB) (*DB, error) {
	db, err := connect(cfg)
	if err != nil {
		return nil, err
	}

	return &DB{db: db}, err
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
	db *gorm.DB
}
