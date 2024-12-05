package config

import (
	"os"

	"github.com/BurntSushi/toml"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/pkg/errors"
)

var envOverrides = map[string]func(*BaseConfig, string){
	"DB_USERNAME": func(c *BaseConfig, v string) { c.DB.Username = v },
	"DB_PASSWORD": func(c *BaseConfig, v string) { c.DB.Password = v },
}

type BaseConfig struct {
	DB      DB            `toml:"db"`
	Indexer Indexer       `toml:"indexer"`
	Timeout TimeoutConfig `toml:"timeout"`
	Logger  logger.Config `toml:"logger"`
}

var DefaultBaseConfig = BaseConfig{
	DB:      defaultDB,
	Indexer: defaultIndexer,
	Timeout: defaultTimeout,
	Logger:  logger.DefaultConfig(),
}

type DB struct {
	Host             string `toml:"host"`
	Port             int    `toml:"port"`
	Username         string `toml:"username"`
	Password         string `toml:"password"`
	DBName           string `toml:"db_name"`
	LogQueries       bool   `toml:"log_queries"`
	DropTableAtStart bool   `toml:"drop_table_at_start"`
	HistoryDrop      uint64 `toml:"history_drop"`
}

var defaultDB = DB{
	Host: "localhost",
	Port: 5432,
}

type TimeoutConfig struct {
	BackoffMaxElapsedTimeSeconds int `toml:"backoff_max_elapsed_time_seconds"`
	RequestTimeoutMillis         int `toml:"request_timeout_millis"`
}

var defaultTimeout = TimeoutConfig{
	BackoffMaxElapsedTimeSeconds: 300,
	RequestTimeoutMillis:         3000,
}

type Indexer struct {
	Confirmations    uint64 `toml:"confirmations"`
	MaxBlockRange    uint64 `toml:"max_block_range"`
	MaxConcurrency   int    `toml:"max_concurrency"`
	StartBlockNumber uint64 `toml:"start_block_number"`
	EndBlockNumber   uint64 `toml:"end_block_number"`
}

var defaultIndexer = Indexer{
	MaxBlockRange:  1000,
	MaxConcurrency: 8,
}

func ReadFile(filepath string, cfg interface{}) error {
	_, err := toml.DecodeFile(filepath, cfg)
	return err
}

func ApplyEnvOverrides(cfg *BaseConfig) {
	for env, override := range envOverrides {
		if val, ok := os.LookupEnv(env); ok {
			override(cfg, val)
		}
	}
}

func CheckParameters(cfg *BaseConfig) error {
	if cfg.Indexer.Confirmations == 0 {
		return errors.New("number of confirmations should be set to a positive integer")
	}

	return nil
}
