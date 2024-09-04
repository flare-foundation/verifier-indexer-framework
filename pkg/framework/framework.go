package framework

import (
	"context"

	"github.com/alexflint/go-arg"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/config"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/database"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/indexer"
)

type CLIArgs struct {
	ConfigFile string `arg:"--config,env:CONFIG_FILE" default:"config.toml"`
}

type Input[B any, C any, T any] struct {
	DefaultConfig C
	NewBlockchain func(*C) (indexer.BlockchainClient[B, T], error)
}

func Run[B any, C any, T any](input Input[B, C, T]) error {
	var args CLIArgs
	arg.MustParse(&args)

	type Config struct {
		config.BaseConfig
		Blockchain *C
	}

	cfg := Config{
		BaseConfig: config.DefaultBaseConfig,
		Blockchain: &input.DefaultConfig,
	}
	if err := config.ReadFile(args.ConfigFile, &cfg); err != nil {
		return err
	}

	db, err := database.New(&cfg.DB, database.ExternalEntities{
		Block:       new(B),
		Transaction: new(T),
	})
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
