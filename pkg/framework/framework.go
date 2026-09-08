package framework

import (
	"context"
	"errors"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/alexflint/go-arg"
	commonlogger "github.com/flare-foundation/go-flare-common/pkg/logger"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/indexer"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
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

// allocateIfNil replaces a nil pointer config with a freshly allocated value so
// TOML decoding and ApplyEnvOverrides never operate on a nil receiver when
// DefaultConfig is omitted for a pointer config type.
func allocateIfNil[C any](c C) C {
	v := reflect.ValueOf(c)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		// The assertion cannot fail: a freshly allocated *Elem is exactly C.
		if allocated, ok := reflect.New(v.Type().Elem()).Interface().(C); ok {
			return allocated
		}
	}

	return c
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
	if input.NewBlockchainClient == nil {
		return errors.New("framework input: NewBlockchainClient must be provided")
	}

	type Config struct {
		config.Base
		Blockchain C
	}

	cfg := Config{
		Base:       config.DefaultBase,
		Blockchain: allocateIfNil(input.DefaultConfig),
	}

	unknownKeys, err := config.Decode(args.ConfigFile, &cfg)
	if err != nil {
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

	commonlogger.Set(cfg.Logger)
	log := logger.Adapter{}

	if len(unknownKeys) != 0 {
		log.Warnf("unknown configuration keys ignored: %s", strings.Join(unknownKeys, ", "))
	}

	// before the database: a bad blockchain config must not reach drop_table_at_start
	bc, err := input.NewBlockchainClient(cfg.Blockchain)
	if err != nil {
		return err
	}

	db, err := database.New(&cfg.DB, database.ExternalEntities[B, T, E]{
		Block:       new(B),
		Transaction: new(T),
		Event:       new(E),
	}, log)
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck // best-effort cleanup on shutdown

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err = saveVersion(ctx, db, bc, &cfg.Base, log)
	if err != nil {
		return err
	}

	ix := indexer.New(&cfg.Base, db, bc, log)

	run := ix.Run
	if cfg.Health.Enabled {
		hs, err := newHealthServer(&cfg.Base, db, log)
		if err != nil {
			return err
		}

		// Joined here so no handler outlives the deferred db.Close.
		run = func(ctx context.Context) error { return runPair(ctx, ix.Run, hs.run) }
	}

	// Cancellation is the documented way to shut down, so it must not exit non-zero.
	if err := run(ctx); !errors.Is(err, context.Canceled) {
		return err
	}

	log.Info("indexer shutting down")

	return nil
}

// saveVersion persists build metadata and blockchain node version to the database.
func saveVersion[B database.Block, T database.Transaction, E database.Event](
	ctx context.Context, db *database.DB[B, T, E], blockchain indexer.BlockchainClient[B, T, E], cfg *config.Base, log logger.Logger,
) error {
	version := database.InitVersion()
	version.NumConfirmations = cfg.Indexer.Confirmations
	version.HistorySeconds = cfg.DB.HistoryDrop

	buildVersion, err := config.ReadBuildVersion()
	if err != nil {
		log.Warnf("failed to read the project build info: %v", err)
	} else {
		version.GitTag = buildVersion.GitTag
		version.GitHash = buildVersion.GitHash
		version.BuildDate = buildVersion.BuildDate
	}

	// Best-effort metadata, but bounded: an unresponsive node must not hang
	// startup indefinitely.
	infoCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Timeout.RequestTimeoutMillis)*time.Millisecond)
	defer cancel()

	nodeVersion, err := blockchain.GetServerInfo(infoCtx)
	if err != nil {
		log.Warnf("failed to fetch blockchain node info: %v", err)
	} else {
		version.NodeVersion = nodeVersion
	}

	return db.SaveVersion(ctx, version)
}
