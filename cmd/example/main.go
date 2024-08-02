package main

import (
	"context"

	"github.com/alexflint/go-arg"
	"gitlab.com/ryancollingham/flare-common/pkg/logger"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/blockchain"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/config"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/database"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/indexer"
)

var log = logger.GetLogger()

type CLIArgs struct {
	ConfigFile string `arg:"--config,env:CONFIG_FILE" default:"config.toml"`
}

type Config struct {
	config.BaseConfig
	Blockchain blockchain.ExampleConfig
}

func main() {
	var args CLIArgs
	arg.MustParse(&args)

	if err := run(context.Background(), args); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args CLIArgs) error {
	cfg := Config{
		BaseConfig: config.DefaultBaseConfig,
	}
	if err := config.ReadFile(args.ConfigFile, &cfg); err != nil {
		return err
	}

	db, err := database.New(&cfg.DB)
	if err != nil {
		return err
	}

	bc := blockchain.NewExample(&cfg.Blockchain)

	indexer := indexer.New(&cfg.Indexer, db, bc)

	return indexer.Run(ctx)
}
