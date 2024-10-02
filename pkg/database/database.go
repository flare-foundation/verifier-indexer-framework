package database

import (
	"context"
	"fmt"
	"net/url"

	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/config"
	"gitlab.com/flarenetwork/libs/go-flare-common/pkg/logger"

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

type ExternalEntities struct {
	Block       interface{}
	Transaction interface{}
}

func New(cfg *config.DB, entities ExternalEntities) (*gorm.DB, error) {
	db, err := connect(cfg)
	if err != nil {
		return nil, err
	}

	log.Debug("connected to the DB")

	if cfg.DropTableAtStart {
		log.Info("DB tables dropped at start")

		err = db.Migrator().DropTable(State{}, entities.Block, entities.Transaction)
		if err != nil {
			return nil, err
		}
	}

	if err := db.AutoMigrate(State{}, entities.Block, entities.Transaction); err != nil {
		return nil, err
	}

	log.Debug("migrated DB entities")

	return db, err
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

func SaveData[B, T any](db *gorm.DB, ctx context.Context, blocks []*B, transactions []*T, states map[string]*State) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if len(blocks) != 0 {
			err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
				Create(blocks).
				Error
			if err != nil {
				return err
			}
		}

		if len(transactions) != 0 {
			err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
				Create(transactions).
				Error
			if err != nil {
				return err
			}
		}

		if len(states) != 0 {
			for _, state := range states {
				err := tx.WithContext(ctx).Save(state).Error
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}
