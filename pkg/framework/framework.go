package framework

import (
	"context"

	"github.com/alexflint/go-arg"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/indexer"
)

type CLIArgs struct {
	ConfigFile string `arg:"--config,env:CONFIG_FILE" default:"config.toml"`
}

type Input[B database.Block, C any, T database.Transaction] struct {
	DefaultConfig       C
	NewBlockchainClient func(*C) (indexer.BlockchainClient[B, T], error)
}

func Run[B database.Block, C any, T database.Transaction](input Input[B, C, T]) error {
	var args CLIArgs
	arg.MustParse(&args)

	return runWithArgs(input, args)
}

func runWithArgs[B database.Block, C any, T database.Transaction](input Input[B, C, T], args CLIArgs) error {
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

	config.ApplyEnvOverrides(&cfg.BaseConfig)

	if err := config.CheckParameters(&cfg.BaseConfig); err != nil {
		return err
	}

	logger.Set(cfg.Logger)

	db, err := database.New(&cfg.DB, database.ExternalEntities[B, T]{
		Block:       new(B),
		Transaction: new(T),
	})
	if err != nil {
		return err
	}

	bc, err := input.NewBlockchainClient(cfg.Blockchain)
	if err != nil {
		return err
	}

	ctx := context.Background()

	indexer := indexer.New(&cfg.BaseConfig, db, bc)

	return indexer.Run(ctx)
}
