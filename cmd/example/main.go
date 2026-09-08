package main

import (
	"context"
	"errors"
	"fmt"

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

// errNotImplemented marks the stub methods of this example.
var errNotImplemented = errors.New("not implemented")

// ExampleBlockchain is a stub blockchain client for demonstration purposes.
type ExampleBlockchain struct{}

// NewExample creates a new ExampleBlockchain client from the given configuration.
func NewExample(cfg *ExampleConfig) (indexer.BlockchainClient[dbBlock, dbTransaction, struct{}], error) {
	return ExampleBlockchain{}, nil
}

// GetLatestBlockInfo returns the latest block info from the example blockchain.
func (e ExampleBlockchain) GetLatestBlockInfo(context.Context) (*indexer.BlockInfo, error) {
	return nil, errNotImplemented
}

// GetBlockResult returns the block result for the given block number.
//
// A missing block must wrap indexer.ErrBlockNotFound and unparseable data
// indexer.ErrInvalidData; every other error is retried.
func (e ExampleBlockchain) GetBlockResult(_ context.Context, blockNumber uint64) (*indexer.BlockResult[dbBlock, dbTransaction, struct{}], error) {
	return nil, fmt.Errorf("block %d: %w", blockNumber, indexer.ErrBlockNotFound)
}

// GetBlockTimestamp returns the timestamp for the given block number.
//
// A missing block must wrap indexer.ErrBlockNotFound — the framework uses it to
// find the oldest block the node still serves when the start block is pruned.
func (e ExampleBlockchain) GetBlockTimestamp(_ context.Context, blockNumber uint64) (uint64, error) {
	return 0, fmt.Errorf("block %d: %w", blockNumber, indexer.ErrBlockNotFound)
}

// GetServerInfo returns the server version string of the example blockchain node.
func (e ExampleBlockchain) GetServerInfo(context.Context) (string, error) {
	return "", errNotImplemented
}

// ExampleConfig holds the blockchain-specific configuration for the example.
type ExampleConfig struct{}

// ApplyEnvOverrides is a no-op required to satisfy the EnvOverrideable interface.
func (c *ExampleConfig) ApplyEnvOverrides() error { return nil }

// dbBlock is the example block entity. Entities need a primary key: it is the
// conflict target rows are overwritten on. autoIncrement:false keeps gorm from
// making a sole integer key a sequence, which would write block 0 as DEFAULT.
type dbBlock struct {
	BlockNumber uint64 `gorm:"primaryKey;autoIncrement:false"`
	Timestamp   uint64 `gorm:"index"`
}

// GetBlockNumber returns the block number for this example block.
func (e dbBlock) GetBlockNumber() uint64 {
	return e.BlockNumber
}

// GetTimestamp returns the timestamp for this example block.
func (e dbBlock) GetTimestamp() uint64 {
	return e.Timestamp
}

// HistoryDropOrder returns the deletion order: blocks first, so a block row
// never outlives its transactions.
func (b dbBlock) HistoryDropOrder() []database.Deletable {
	return []database.Deletable{dbBlock{}, dbTransaction{}}
}

// TimestampField returns the database column name used for timestamp-based deletion.
func (e dbBlock) TimestampField() string {
	return "timestamp"
}

// dbTransaction is the example transaction entity. Its timestamp is the parent
// block's, so a drop cannot prune it out from under a still-advertised block.
type dbTransaction struct {
	Hash        string `gorm:"primaryKey"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

// TimestampField returns the database column name used for timestamp-based deletion.
func (t dbTransaction) TimestampField() string {
	return "timestamp"
}
