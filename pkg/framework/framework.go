package framework

import (
	"context"

	"github.com/alexflint/go-arg"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/config"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/database"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/indexer"
	"gitlab.com/flarenetwork/libs/go-flare-common/pkg/logger"
)

var log = logger.GetLogger()

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

	bc, err := input.NewBlockchainClient(cfg.Blockchain)
	if err != nil {
		return err
	}

	ctx := context.Background()

	// if the starting block number is set bellow the interval that gets dropped by history, fix it
	if cfg.DB.HistoryDrop > 0 {
		cfg.Indexer.StartBlockNumber, err = GetMinBlockWithinHistoryInterval(ctx, cfg.Indexer.StartBlockNumber, cfg.DB.HistoryDrop, bc)
		if err != nil {
			return err
		}
		log.Infof("new starting block number set to %d due to history drop", cfg.Indexer.StartBlockNumber)
	}

	// obtain the current state of the database
	err = database.GlobalStates.GetAndUpdate(ctx, db)
	if err != nil {
		return err
	}

	// if data already exists in the database, initial history drop and a sanity check is needed
	if database.GlobalStates.States[database.FirstDatabaseIndexState] != nil {
		if cfg.DB.HistoryDrop > 0 {
			cfg.Indexer.StartBlockNumber, err = initialHistoryDrop(ctx, db, bc, cfg.BaseConfig)
			if err != nil {
				return err
			}
		}

		err = sanityCheck(cfg.Indexer.StartBlockNumber, database.GlobalStates)
		if err != nil {
			return err
		}
	}

	if cfg.DB.HistoryDrop > 0 {
		go DropHistoryRunner(
			ctx, db, cfg.DB.HistoryDrop, bc,
		)
	}
	indexer := indexer.New(&cfg.BaseConfig, db, bc)

	return indexer.Run(context.Background())
}
