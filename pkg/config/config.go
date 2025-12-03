package config

import (
	"github.com/BurntSushi/toml"
	"github.com/caarlos0/env/v11"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/pkg/errors"
)

type envConfig struct {
	DBHost     string `env:"DB_HOST"`
	DBPort     int    `env:"DB_PORT"`
	DBUsername string `env:"DB_USERNAME"`
	DBPassword string `env:"DB_PASSWORD"`
	DBName     string `env:"DB_NAME"`
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
	Host                 string `toml:"host"`
	Port                 int    `toml:"port"`
	Username             string `toml:"username"`
	Password             string `toml:"password"`
	DBName               string `toml:"db_name"`
	LogQueries           bool   `toml:"log_queries"`
	DropTableAtStart     bool   `toml:"drop_table_at_start"`
	HistoryDrop          uint64 `toml:"history_drop"`
	HistoryDropFrequency uint64 `toml:"history_drop_frequency"`
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

func ReadFile(filepath string, cfg any) error {
	_, err := toml.DecodeFile(filepath, cfg)
	return err
}

type EnvOverrideable interface {
	ApplyEnvOverrides()
}

func (cfg *BaseConfig) ApplyEnvOverrides() {
	var envCfg envConfig
	if err := env.Parse(&envCfg); err != nil {
		logger.Error("failed to parse environment variables for config overrides: %v", err)
		return
	}

	if envCfg.DBHost != "" {
		cfg.DB.Host = envCfg.DBHost
	}

	if envCfg.DBPort != 0 {
		cfg.DB.Port = envCfg.DBPort
	}

	if envCfg.DBUsername != "" {
		cfg.DB.Username = envCfg.DBUsername
	}

	if envCfg.DBPassword != "" {
		cfg.DB.Password = envCfg.DBPassword
	}

	if envCfg.DBName != "" {
		cfg.DB.DBName = envCfg.DBName
	}
}

func CheckParameters(cfg *BaseConfig) error {
	if cfg.Indexer.Confirmations == 0 {
		return errors.New("number of confirmations should be set to a positive integer")
	}

	return nil
}
