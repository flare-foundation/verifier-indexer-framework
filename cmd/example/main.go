package main

import (
	"context"
	"errors"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/framework"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/indexer"
)

func main() {
	input := framework.Input[dbBlock, *ExampleConfig, dbTransaction, struct{}]{
		NewBlockchainClient: NewExample,
	}

	if err := framework.Run(input); err != nil {
		logger.Fatal(err)
	}
}

type ExampleBlockchain struct{}

func NewExample(cfg *ExampleConfig) (indexer.BlockchainClient[dbBlock, dbTransaction, struct{}], error) {
	return ExampleBlockchain{}, nil
}

func (e ExampleBlockchain) GetLatestBlockInfo(context.Context) (*indexer.BlockInfo, error) {
	return nil, errors.New("not implemented")
}

func (e ExampleBlockchain) GetBlockResult(context.Context, uint64) (*indexer.BlockResult[dbBlock, dbTransaction, struct{}], error) {
	return nil, errors.New("not implemented")
}

func (e ExampleBlockchain) GetBlockTimestamp(context.Context, uint64) (uint64, error) {
	return 0, errors.New("not implemented")
}

func (e ExampleBlockchain) GetServerInfo(context.Context) (string, error) {
	return "", errors.New("not implemented")
}

type ExampleConfig struct{}

// No-op - required for interface
func (c *ExampleConfig) ApplyEnvOverrides() {}

type dbBlock struct{}

func (e dbBlock) GetBlockNumber() uint64 {
	return 0
}
func (e dbBlock) GetTimestamp() uint64 {
	return 0
}

type dbTransaction struct{}
