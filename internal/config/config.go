package config

import "github.com/BurntSushi/toml"

func ReadFile(filepath string) (*Config, error) {
	cfg := defaultConfig()

	if _, err := toml.DecodeFile(filepath, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

type Config struct {
	DB *DB `toml:"db"`
}

func defaultConfig() *Config {
	return &Config{
		DB: defaultDB(),
	}
}

type DB struct {
	Host       string `toml:"host"`
	Port       int    `toml:"port"`
	Username   string `toml:"username"`
	Password   string `toml:"password"`
	DBName     string `toml:"db_name"`
	LogQueries bool   `toml:"log_queries"`
}

func defaultDB() *DB {
	return &DB{
		Host: "localhost",
		Port: 3306,
	}
}
