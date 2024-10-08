package main

import (
	"context"
	"errors"

	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/framework"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/indexer"
	"gitlab.com/flarenetwork/libs/go-flare-common/pkg/logger"
)

var log = logger.GetLogger()

func main() {
	input := framework.Input[dbBlock, ExampleConfig, dbTransaction]{
		NewBlockchainClient: NewExample,
	}

	if err := framework.Run(input); err != nil {
		log.Fatal(err)
	}
}

type ExampleBlockchain struct{}

func NewExample(cfg *ExampleConfig) (indexer.BlockchainClient[dbBlock, dbTransaction], error) {
	return ExampleBlockchain{}, nil
}

func (e ExampleBlockchain) GetLatestBlockInfo(context.Context) (*indexer.BlockInfo, error) {
	return nil, errors.New("not implemented")
}

func (e ExampleBlockchain) GetBlockResult(context.Context, uint64) (*indexer.BlockResult[dbBlock, dbTransaction], error) {
	return nil, errors.New("not implemented")
}

func (e ExampleBlockchain) GetBlockTimestamp(context.Context, uint64) (uint64, error) {
	return 0, errors.New("not implemented")
}

type ExampleConfig struct{}

type dbBlock struct{}

func (e dbBlock) GetBlockNumber() uint64 {
	return 0
}
func (e dbBlock) GetTimestamp() uint64 {
	return 0
}

type dbTransaction struct{}
