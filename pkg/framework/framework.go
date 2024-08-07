package framework

import (
	"context"

	"github.com/alexflint/go-arg"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/config"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/database"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/indexer"
)

type CLIArgs struct {
	ConfigFile string `arg:"--config,env:CONFIG_FILE" default:"config.toml"`
}

func Run[T any](newBlockchain func(T) (indexer.BlockchainClient, error), defaultConfig T) error {
	var args CLIArgs
	arg.MustParse(&args)

	type Config struct {
		config.BaseConfig
		Blockchain T
	}

	cfg := Config{
		BaseConfig: config.DefaultBaseConfig,
		Blockchain: defaultConfig,
	}
	if err := config.ReadFile(args.ConfigFile, &cfg); err != nil {
		return err
	}

	db, err := database.New(&cfg.DB)
	if err != nil {
		return err
	}

	bc, err := newBlockchain(cfg.Blockchain)
	if err != nil {
		return err
	}

	indexer := indexer.New(&cfg.Indexer, db, bc)

	return indexer.Run(context.Background())
}
