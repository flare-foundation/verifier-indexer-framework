package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/caarlos0/env/v11"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
)

type envCfg struct {
	DBHost     string `env:"DB_HOST"`
	DBPort     int    `env:"DB_PORT"`
	DBUsername string `env:"DB_USERNAME"`
	DBPassword string `env:"DB_PASSWORD"`
	DBName     string `env:"DB_NAME"`
}

type Base struct {
	DB      DB            `toml:"db"`
	Indexer Indexer       `toml:"indexer"`
	Timeout TimeoutConfig `toml:"timeout"`
	Logger  logger.Config `toml:"logger"`
}

var DefaultBase = Base{
	DB:      defaultDB,
	Indexer: defaultIndexer,
	Timeout: defaultTimeout,
	Logger:  logger.DefaultConfig(),
}

type DB struct {
	Host                   string `toml:"host"`
	Port                   int    `toml:"port"`
	Username               string `toml:"username"`
	Password               string `toml:"password"`
	DBName                 string `toml:"db_name"`
	MaxOpenConns           int    `toml:"max_open_conns"`
	MaxIdleConns           int    `toml:"max_idle_conns"`
	ConnMaxLifetimeSeconds int    `toml:"conn_max_lifetime_seconds"`
	LogQueries             bool   `toml:"log_queries"`
	DropTableAtStart       bool   `toml:"drop_table_at_start"`
	HistoryDrop            uint64 `toml:"history_drop"`
	HistoryDropFrequency   uint64 `toml:"history_drop_frequency"`
}

var defaultDB = DB{
	Host:                   "localhost",
	Port:                   5432,
	MaxOpenConns:           25,
	MaxIdleConns:           5,
	ConnMaxLifetimeSeconds: 300,
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

// ReadFile decodes a TOML configuration file at the given filepath into cfg.
// Unknown keys are rejected: a mistyped key would otherwise be dropped
// silently, leaving the default in place.
func ReadFile[T any](filepath string, cfg T) error {
	meta, err := toml.DecodeFile(filepath, cfg)
	if err != nil {
		return err
	}

	undecoded := meta.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}

	keys := make([]string, 0, len(undecoded))
	for _, key := range undecoded {
		keys = append(keys, key.String())
	}

	return fmt.Errorf("unknown configuration keys: %s", strings.Join(keys, ", "))
}

// EnvOverrideable is implemented by configuration types that support overriding
// fields from environment variables.
type EnvOverrideable interface {
	ApplyEnvOverrides() error
}

// ApplyEnvOverrides overrides database connection fields in Base with values from environment variables.
func (cfg *Base) ApplyEnvOverrides() error {
	var envCfg envCfg
	if err := env.Parse(&envCfg); err != nil {
		return fmt.Errorf("failed to parse environment variables for config overrides: %w", err)
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

	return nil
}

// CheckParameters validates that required configuration fields in Base have acceptable values.
func CheckParameters(cfg *Base) error {
	if cfg.Indexer.Confirmations == 0 {
		return errors.New("number of confirmations should be set to a positive integer")
	}

	if cfg.Indexer.MaxConcurrency <= 0 {
		return errors.New("max_concurrency must be a positive integer")
	}

	if cfg.Indexer.MaxBlockRange == 0 {
		return errors.New("max_block_range must be a positive integer")
	}

	if cfg.Timeout.RequestTimeoutMillis <= 0 {
		return errors.New("request_timeout_millis must be a positive integer")
	}

	if cfg.Timeout.BackoffMaxElapsedTimeSeconds <= 0 {
		return errors.New("backoff_max_elapsed_time_seconds must be a positive integer")
	}

	if cfg.Indexer.EndBlockNumber != 0 && cfg.Indexer.EndBlockNumber < cfg.Indexer.StartBlockNumber {
		return errors.New("end_block_number must be greater than or equal to start_block_number")
	}

	return nil
}
