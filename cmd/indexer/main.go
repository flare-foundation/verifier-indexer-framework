package main

import (
	"context"

	"github.com/alexflint/go-arg"
	"gitlab.com/ryancollingham/flare-common/pkg/logger"
	"gitlab.com/ryancollingham/flare-indexer-framework/internal/blockchain"
	"gitlab.com/ryancollingham/flare-indexer-framework/internal/config"
	"gitlab.com/ryancollingham/flare-indexer-framework/internal/database"
	"gitlab.com/ryancollingham/flare-indexer-framework/internal/indexer"
)

var log = logger.GetLogger()

type CLIArgs struct {
	ConfigFile string `arg:"--config,env:CONFIG_FILE" default:"config.toml"`
}

func main() {
	var args CLIArgs
	arg.MustParse(&args)

	if err := run(context.Background(), args); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args CLIArgs) error {
	cfg, err := config.ReadFile(args.ConfigFile)
	if err != nil {
		return err
	}

	db, err := database.New(&cfg.DB)
	if err != nil {
		return err
	}

	bc := blockchain.NewExample()

	indexer := indexer.New(&cfg.Indexer, db, bc)

	return indexer.Run(ctx)
}
