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

type Input[T any] struct {
	DefaultConfig T
	Entities      database.ExternalEntities
	NewBlockchain func(T) (indexer.BlockchainClient, error)
}

func Run[T any](input Input[T]) error {
	var args CLIArgs
	arg.MustParse(&args)

	type Config struct {
		config.BaseConfig
		Blockchain T
	}

	cfg := Config{
		BaseConfig: config.DefaultBaseConfig,
		Blockchain: input.DefaultConfig,
	}
	if err := config.ReadFile(args.ConfigFile, &cfg); err != nil {
		return err
	}

	db, err := database.New(&cfg.DB, input.Entities)
	if err != nil {
		return err
	}

	bc, err := input.NewBlockchain(cfg.Blockchain)
	if err != nil {
		return err
	}

	indexer := indexer.New(&cfg.Indexer, db, bc)

	return indexer.Run(context.Background())
}
