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

type Input[B database.Block, C config.EnvOverrideable, T database.Transaction, E database.Event] struct {
	DefaultConfig       C
	NewBlockchainClient func(C) (indexer.BlockchainClient[B, T, E], error)
}

func Run[B database.Block, C config.EnvOverrideable, T database.Transaction, E database.Event](input Input[B, C, T, E]) error {
	var args CLIArgs
	arg.MustParse(&args)

	return runWithArgs(input, args)
}

func runWithArgs[B database.Block, C config.EnvOverrideable, T database.Transaction, E database.Event](input Input[B, C, T, E], args CLIArgs) error {
	type Config struct {
		config.BaseConfig
		Blockchain C
	}

	cfg := Config{
		BaseConfig: config.DefaultBaseConfig,
		Blockchain: input.DefaultConfig,
	}
	if err := config.ReadFile(args.ConfigFile, &cfg); err != nil {
		return err
	}

	cfg.ApplyEnvOverrides()
	cfg.Blockchain.ApplyEnvOverrides()

	if err := config.CheckParameters(&cfg.BaseConfig); err != nil {
		return err
	}

	logger.Set(cfg.Logger)

	db, err := database.New(&cfg.DB, database.ExternalEntities[B, T, E]{
		Block:       new(B),
		Transaction: new(T),
		Event:       new(E),
	})
	if err != nil {
		return err
	}

	bc, err := input.NewBlockchainClient(cfg.Blockchain)
	if err != nil {
		return err
	}

	ctx := context.Background()

	err = saveVersion(ctx, db, bc, &cfg.BaseConfig)
	if err != nil {
		return err
	}

	indexer := indexer.New(&cfg.BaseConfig, db, bc)

	return indexer.Run(ctx)
}

func saveVersion[B database.Block, T database.Transaction, E database.Event](
	ctx context.Context, db *database.DB[B, T, E], blockchain indexer.BlockchainClient[B, T, E], cfg *config.BaseConfig,
) error {
	version := database.InitVersion()
	version.NumConfirmations = cfg.Indexer.Confirmations
	version.HistorySeconds = cfg.DB.HistoryDrop

	buildVersion, err := config.ReadBuildVersion()
	if err != nil {
		logger.Warn("failed to read the project build info")
	} else {
		version.GitTag = buildVersion.GitTag
		version.GitHash = buildVersion.GitHash
		version.BuildDate = buildVersion.BuildDate
	}

	nodeVersion, err := blockchain.GetServerInfo(ctx)
	if err != nil {
		logger.Warn("failed to fetch blockchain node info")
	} else {
		version.NodeVersion = nodeVersion
	}

	return db.SaveVersion(ctx, version)
}
