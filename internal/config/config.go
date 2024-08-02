package config

import "github.com/BurntSushi/toml"

func ReadFile(filepath string) (*Config, error) {
	cfg := defaultConfig

	if _, err := toml.DecodeFile(filepath, cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

type Config struct {
	DB      DB      `toml:"db"`
	Indexer Indexer `toml:"indexer"`
}

var defaultConfig = Config{
	DB:      defaultDB,
	Indexer: defaultIndexer,
}

type DB struct {
	Host       string `toml:"host"`
	Port       int    `toml:"port"`
	Username   string `toml:"username"`
	Password   string `toml:"password"`
	DBName     string `toml:"db_name"`
	LogQueries bool   `toml:"log_queries"`
}

var defaultDB = DB{
	Host: "localhost",
	Port: 3306,
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
