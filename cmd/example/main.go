package main

import (
	"context"
	"errors"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
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

// ExampleBlockchain is a stub blockchain client for demonstration purposes.
type ExampleBlockchain struct{}

// NewExample creates a new ExampleBlockchain client from the given configuration.
func NewExample(cfg *ExampleConfig) (indexer.BlockchainClient[dbBlock, dbTransaction, struct{}], error) {
	return ExampleBlockchain{}, nil
}

// GetLatestBlockInfo returns the latest block info from the example blockchain.
func (e ExampleBlockchain) GetLatestBlockInfo(context.Context) (*indexer.BlockInfo, error) {
	return nil, errors.New("not implemented")
}

// GetBlockResult returns the block result for the given block number.
func (e ExampleBlockchain) GetBlockResult(context.Context, uint64) (*indexer.BlockResult[dbBlock, dbTransaction, struct{}], error) {
	return nil, errors.New("not implemented")
}

// GetBlockTimestamp returns the timestamp for the given block number.
func (e ExampleBlockchain) GetBlockTimestamp(context.Context, uint64) (uint64, error) {
	return 0, errors.New("not implemented")
}

// GetServerInfo returns the server version string of the example blockchain node.
func (e ExampleBlockchain) GetServerInfo(context.Context) (string, error) {
	return "", errors.New("not implemented")
}

// ExampleConfig holds the blockchain-specific configuration for the example.
type ExampleConfig struct{}

// ApplyEnvOverrides is a no-op required to satisfy the EnvOverrideable interface.
func (c *ExampleConfig) ApplyEnvOverrides() error { return nil }

type dbBlock struct{}

// GetBlockNumber returns the block number for this example block.
func (e dbBlock) GetBlockNumber() uint64 {
	return 0
}

// GetTimestamp returns the timestamp for this example block.
func (e dbBlock) GetTimestamp() uint64 {
	return 0
}

// HistoryDropOrder returns the entity deletion order for history pruning.
func (b dbBlock) HistoryDropOrder() []database.Deletable {
	var emptyBlock dbBlock
	return []database.Deletable{emptyBlock}
}

// TimestampField returns the database column name used for timestamp-based deletion.
func (e dbBlock) TimestampField() string {
	return "timestamp"
}

type dbTransaction struct{}
