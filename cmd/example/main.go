package main

import (
	"context"
	"errors"

	"gitlab.com/ryancollingham/flare-common/pkg/logger"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/framework"
	"gitlab.com/ryancollingham/flare-indexer-framework/pkg/indexer"
)

var log = logger.GetLogger()

func main() {
	if err := framework.Run(NewExample, new(ExampleConfig)); err != nil {
		log.Fatal(err)
	}
}

type ExampleBlockchain struct{}

func NewExample(cfg *ExampleConfig) indexer.BlockchainClient {
	return ExampleBlockchain{}
}

func (e ExampleBlockchain) GetLatestBlockNumber(context.Context) (uint64, error) {
	return 0, errors.New("not implemented")
}

func (e ExampleBlockchain) GetBlockResult(context.Context, uint64) (*indexer.BlockResult, error) {
	return nil, errors.New("not implemented")
}

type ExampleConfig struct{}
