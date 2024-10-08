package config

import "github.com/BurntSushi/toml"

func ReadFile(filepath string, cfg interface{}) error {
	_, err := toml.DecodeFile(filepath, cfg)
	return err
}

type BaseConfig struct {
	DB      DB            `toml:"db"`
	Indexer Indexer       `toml:"indexer"`
	Timeout TimeoutConfig `toml:"timeout"`
}

var DefaultBaseConfig = BaseConfig{
	DB:      defaultDB,
	Indexer: defaultIndexer,
	Timeout: defaultTimeout,
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
	Port: 3306,
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
}

var defaultIndexer = Indexer{
	Confirmations:  1,
	MaxBlockRange:  1000,
	MaxConcurrency: 8,
}
