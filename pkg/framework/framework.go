package framework

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/alexflint/go-arg"
	"github.com/flare-foundation/go-flare-common/pkg/logger"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/indexer"
)

// CLIArgs holds the command-line arguments for the framework.
type CLIArgs struct {
	ConfigFile string `arg:"--config,env:CONFIG_FILE" default:"config.toml"`
}

// Input provides the user-defined configuration and blockchain client constructor
// needed to initialize the indexer framework.
type Input[B database.Block, C config.EnvOverrideable, T database.Transaction, E database.Event] struct {
	DefaultConfig       C
	NewBlockchainClient func(C) (indexer.BlockchainClient[B, T, E], error)
}

// Run parses CLI arguments, loads configuration, connects to the database,
// and starts the indexer loop. It blocks until the context is cancelled or
// an error occurs.
func Run[B database.Block, C config.EnvOverrideable, T database.Transaction, E database.Event](input Input[B, C, T, E]) error {
	var args CLIArgs
	arg.MustParse(&args)

	return runWithArgs(input, args)
}

// runWithArgs initializes the full framework stack from the provided input and
// CLI arguments, then runs the indexer until completion or cancellation.
func runWithArgs[B database.Block, C config.EnvOverrideable, T database.Transaction, E database.Event](input Input[B, C, T, E], args CLIArgs) error {
	type Config struct {
		config.Base
		Blockchain C
	}

	cfg := Config{
		Base:       config.DefaultBase,
		Blockchain: input.DefaultConfig,
	}

	if err := config.ReadFile(args.ConfigFile, &cfg); err != nil {
		return err
	}

	if err := cfg.ApplyEnvOverrides(); err != nil {
		return err
	}

	if err := cfg.Blockchain.ApplyEnvOverrides(); err != nil {
		return err
	}

	if err := config.CheckParameters(&cfg.Base); err != nil {
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
	defer db.Close() //nolint:errcheck // best-effort cleanup on shutdown

	bc, err := input.NewBlockchainClient(cfg.Blockchain)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err = saveVersion(ctx, db, bc, &cfg.Base)
	if err != nil {
		return err
	}

	indexer := indexer.New(&cfg.Base, db, bc)

	return indexer.Run(ctx)
}

// saveVersion persists build metadata and blockchain node version to the database.
func saveVersion[B database.Block, T database.Transaction, E database.Event](
	ctx context.Context, db *database.DB[B, T, E], blockchain indexer.BlockchainClient[B, T, E], cfg *config.Base,
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
