package main

import (
	"context"
	"errors"

	"gitlab.com/ryancollingham/flare-common/pkg/logger"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/framework"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/indexer"
)

var log = logger.GetLogger()

func main() {
	input := framework.Input[dbBlock, ExampleConfig, dbTransaction]{
		NewBlockchain: NewExample,
	}

	if err := framework.Run(input); err != nil {
		log.Fatal(err)
	}
}

type ExampleBlockchain struct{}

func NewExample(cfg *ExampleConfig) (indexer.BlockchainClient[dbBlock, dbTransaction], error) {
	return ExampleBlockchain{}, nil
}

func (e ExampleBlockchain) GetLatestBlockNumber(context.Context) (uint64, error) {
	return 0, errors.New("not implemented")
}

func (e ExampleBlockchain) GetBlockResult(context.Context, uint64) (*indexer.BlockResult[dbBlock, dbTransaction], error) {
	return nil, errors.New("not implemented")
}

type ExampleConfig struct{}

type dbBlock struct{}
type dbTransaction struct{}
