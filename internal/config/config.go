package config

import "github.com/BurntSushi/toml"

func ReadFile(filepath string) (*Config, error) {
	cfg := new(Config)

	if _, err := toml.DecodeFile(filepath, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

type Config struct{}
