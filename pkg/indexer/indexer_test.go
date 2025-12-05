package indexer

import (
	"context"
	"sync"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

type mockDB struct {
	blocks       [][]*dbBlock
	transactions [][]*dbTransaction
	states       []*database.State
}

func (m *mockDB) SaveAllEntities(
	ctx context.Context,
	blocks []*dbBlock,
	transactions []*dbTransaction,
	events []*struct{},
	state *database.State,
) error {
	m.blocks = append(m.blocks, blocks)
	m.transactions = append(m.transactions, transactions)
	m.states = append(m.states, state)

	return nil
}

func (m mockDB) GetState(ctx context.Context) (*database.State, error) {
	return &database.State{}, nil
}

func (m mockDB) DropHistoryIteration(
	ctx context.Context,
	state *database.State,
	intervalSeconds, lastBlockTime uint64,
) (*database.State, error) {
	return state, nil
}

type dbBlock struct {
	BlockNumber uint64
	Timestamp   uint64
}

func (b dbBlock) GetBlockNumber() uint64 {
	return b.BlockNumber
}
func (b dbBlock) GetTimestamp() uint64 {
	return b.Timestamp
}

func (b dbBlock) HistoryDropOrder() []database.Deletable {
	return nil
}

type dbTransaction struct{}

type mockBlockchain struct{}

func (m mockBlockchain) GetLatestBlockInfo(context.Context) (*BlockInfo, error) {
	return &BlockInfo{
		BlockNumber: 102,
		Timestamp:   102000,
	}, nil
}

func (m mockBlockchain) GetBlockResult(ctx context.Context, blockNumber uint64) (*BlockResult[dbBlock, dbTransaction, struct{}], error) {
	if blockNumber != 101 {
		return nil, errors.New("block not found")
	}

	return &BlockResult[dbBlock, dbTransaction, struct{}]{
		Block:        dbBlock{BlockNumber: 101, Timestamp: 101000},
		Transactions: []dbTransaction{{}, {}, {}},
	}, nil
}

func (m mockBlockchain) GetBlockTimestamp(context.Context, uint64) (uint64, error) {
	return 0, nil
}

func (m mockBlockchain) GetServerInfo(context.Context) (string, error) {
	return "mock-server", nil
}

func TestIndexer(t *testing.T) {
	cfg := config.BaseConfig{
		Indexer: config.Indexer{
			Confirmations:  1,
			MaxBlockRange:  10,
			MaxConcurrency: 1,
		},
	}

	db := &mockDB{}
	chain := &mockBlockchain{}

	indexer := New(&cfg, db, chain)
	require.NotNil(t, indexer)

	require.Equal(t, uint64(1), indexer.confirmations)

	ctx := context.Background()
	state := &database.State{
		LastIndexedBlockNumber: 100,
	}

	var historyDropLock sync.Mutex
	historyDropResults := make(chan *database.State, 1)
	upToDateBackoff := backoff.NewExponentialBackOff()

	state, err := indexer.runIteration(ctx, state, &historyDropLock, historyDropResults, upToDateBackoff)
	require.NoError(t, err)
	require.NotNil(t, state)

	// We should have indexed up to block 101 since the required number of confirmations is 1
	require.Equal(t, uint64(101), state.LastIndexedBlockNumber)
	require.Equal(t, uint64(101000), state.LastIndexedBlockTimestamp)
	require.Equal(t, uint64(102), state.LastChainBlockNumber)
	require.Equal(t, uint64(102000), state.LastChainBlockTimestamp)

	require.Len(t, db.blocks, 1)
	require.Len(t, db.blocks[0], 1) // One block
	require.Len(t, db.transactions, 1)
	require.Len(t, db.transactions[0], 3) // Three transactions
	require.Len(t, db.states, 1)
	require.Equal(t, state, db.states[0])
}

func TestGetInitialStartBlockNumber(t *testing.T) {
	ctx := context.Background()

	t.Run("returns zero when no previous state exists", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{}
		var state database.State

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(0), startBlock)
	})

	t.Run("returns last processed block number plus one when previous state exists", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{}
		state := database.State{LastIndexedBlockNumber: 42}

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(43), startBlock)
	})
}
