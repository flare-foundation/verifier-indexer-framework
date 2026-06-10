package indexer

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/require"
)

type mockDB struct {
	blocks       [][]*dbBlock
	transactions [][]*dbTransaction
	states       []*database.State
	saveErr      error
}

func (m *mockDB) SaveAllEntities(
	ctx context.Context,
	blocks []*dbBlock,
	transactions []*dbTransaction,
	events []*struct{},
	state *database.State,
) error {
	if m.saveErr != nil {
		return m.saveErr
	}

	m.blocks = append(m.blocks, blocks)
	m.transactions = append(m.transactions, transactions)
	stateCopy := *state
	m.states = append(m.states, &stateCopy)

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
	cfg := config.Base{
		Indexer: config.Indexer{
			Confirmations:  1,
			MaxBlockRange:  10,
			MaxConcurrency: 1,
		},
	}

	db := &mockDB{}
	chain := &mockBlockchain{}

	indexer := New(&cfg, db, chain, logger.Nop{})
	require.NotNil(t, indexer)

	require.Equal(t, uint64(1), indexer.confirmations)

	ctx := context.Background()
	state := &database.State{
		LastIndexedBlockNumber: 100,
	}

	var historyDropLock sync.Mutex
	historyDropResults := make(chan *database.State, 1)
	upToDateBackoff := backoff.NewExponentialBackOff(backoff.WithMaxElapsedTime(0))

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

func TestPollHistoryDropResults(t *testing.T) {
	ctx := context.Background()

	newLockedMutex := func() *sync.Mutex {
		var m sync.Mutex
		m.Lock() // a running history drop holds the lock until its result is processed

		return &m
	}

	t.Run("no pending result is a no-op", func(t *testing.T) {
		db := &mockDB{}
		ix := Indexer[dbBlock, dbTransaction, struct{}]{db: db, log: logger.Nop{}}
		state := &database.State{FirstIndexedBlockNumber: 10}

		err := ix.pollHistoryDropResults(ctx, &sync.Mutex{}, make(chan *database.State, 1), state)
		require.NoError(t, err)
		require.Equal(t, uint64(10), state.FirstIndexedBlockNumber)
		require.Empty(t, db.states)
	})

	t.Run("applies and persists the drop result", func(t *testing.T) {
		db := &mockDB{}
		ix := Indexer[dbBlock, dbTransaction, struct{}]{db: db, historyDropInterval: 200, log: logger.Nop{}}
		lock := newLockedMutex()

		results := make(chan *database.State, 1)
		results <- &database.State{
			FirstIndexedBlockNumber:    50,
			FirstIndexedBlockTimestamp: 50000,
			LastChainBlockTimestamp:    50200,
			LastHistoryDrop:            12345,
		}

		// The in-memory boundary references blocks below the deletion threshold
		// (50200 - 200), so the drop result must take over.
		state := &database.State{
			FirstIndexedBlockNumber:    10,
			FirstIndexedBlockTimestamp: 10000,
			LastIndexedBlockNumber:     100,
		}

		err := ix.pollHistoryDropResults(ctx, lock, results, state)
		require.NoError(t, err)
		require.Equal(t, uint64(50), state.FirstIndexedBlockNumber)
		require.Equal(t, uint64(50000), state.FirstIndexedBlockTimestamp)
		require.Equal(t, uint64(12345), state.LastHistoryDrop)
		require.Equal(t, uint64(100), state.LastIndexedBlockNumber)

		require.Len(t, db.states, 1)
		require.Equal(t, state, db.states[0])
		require.True(t, lock.TryLock(), "lock must be released after processing the result")
	})

	t.Run("takes over the zero reset when the drop emptied the database", func(t *testing.T) {
		db := &mockDB{}
		ix := Indexer[dbBlock, dbTransaction, struct{}]{db: db, historyDropInterval: 200, log: logger.Nop{}}
		lock := newLockedMutex()

		results := make(chan *database.State, 1)
		results <- &database.State{LastChainBlockTimestamp: 15000, LastHistoryDrop: 12345}

		state := &database.State{
			FirstIndexedBlockNumber:    10,
			FirstIndexedBlockTimestamp: 10000,
		}

		err := ix.pollHistoryDropResults(ctx, lock, results, state)
		require.NoError(t, err)
		require.Equal(t, uint64(0), state.FirstIndexedBlockNumber)
		require.Equal(t, uint64(0), state.FirstIndexedBlockTimestamp)
		require.Len(t, db.states, 1)
	})

	t.Run("keeps the in-memory boundary when its blocks survive the drop", func(t *testing.T) {
		// A drop that started before the first blocks were saved reads an empty
		// table and reports a zero boundary; the blocks indexed meanwhile are
		// above the deletion threshold and must not be discarded.
		db := &mockDB{}
		ix := Indexer[dbBlock, dbTransaction, struct{}]{db: db, historyDropInterval: 200, log: logger.Nop{}}
		lock := newLockedMutex()

		results := make(chan *database.State, 1)
		results <- &database.State{LastChainBlockTimestamp: 1000, LastHistoryDrop: 12345}

		state := &database.State{
			FirstIndexedBlockNumber:    300,
			FirstIndexedBlockTimestamp: 800,
			LastIndexedBlockNumber:     399,
		}

		err := ix.pollHistoryDropResults(ctx, lock, results, state)
		require.NoError(t, err)
		require.Equal(t, uint64(300), state.FirstIndexedBlockNumber)
		require.Equal(t, uint64(800), state.FirstIndexedBlockTimestamp)
		require.Equal(t, uint64(12345), state.LastHistoryDrop)
		require.Len(t, db.states, 1)
	})

	t.Run("takes the older surviving boundary from the drop", func(t *testing.T) {
		// State loaded from an older run can trail the in-memory boundary; the
		// drop's fresh database read is authoritative when it is older.
		db := &mockDB{}
		ix := Indexer[dbBlock, dbTransaction, struct{}]{db: db, historyDropInterval: 200, log: logger.Nop{}}
		lock := newLockedMutex()

		results := make(chan *database.State, 1)
		results <- &database.State{
			FirstIndexedBlockNumber:    302,
			FirstIndexedBlockTimestamp: 802,
			LastChainBlockTimestamp:    1002,
			LastHistoryDrop:            12345,
		}

		state := &database.State{
			FirstIndexedBlockNumber:    305,
			FirstIndexedBlockTimestamp: 805,
		}

		err := ix.pollHistoryDropResults(ctx, lock, results, state)
		require.NoError(t, err)
		require.Equal(t, uint64(302), state.FirstIndexedBlockNumber)
		require.Equal(t, uint64(802), state.FirstIndexedBlockTimestamp)
	})

	t.Run("nil result returns an error", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{db: &mockDB{}, log: logger.Nop{}}
		lock := newLockedMutex()

		results := make(chan *database.State, 1)
		results <- nil

		err := ix.pollHistoryDropResults(ctx, lock, results, &database.State{})
		require.Error(t, err)
		require.True(t, lock.TryLock(), "lock must be released after processing the result")
	})

	t.Run("save error is propagated", func(t *testing.T) {
		db := &mockDB{saveErr: errors.New("db down")}
		ix := Indexer[dbBlock, dbTransaction, struct{}]{db: db, log: logger.Nop{}}
		lock := newLockedMutex()

		results := make(chan *database.State, 1)
		results <- &database.State{FirstIndexedBlockNumber: 50}

		err := ix.pollHistoryDropResults(ctx, lock, results, &database.State{})
		require.ErrorIs(t, err, db.saveErr)
	})
}

func TestGetInitialStartBlockNumber(t *testing.T) {
	ctx := context.Background()

	t.Run("returns zero when no previous state exists", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{log: logger.Nop{}}
		var state database.State

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(0), startBlock)
	})

	t.Run("returns last processed block number plus one when previous state exists", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{log: logger.Nop{}}
		state := database.State{LastIndexedBlockNumber: 42}

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(43), startBlock)
	})

	t.Run("uses configured start block on fresh database", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			startBlockNumber: 500,
			log:              logger.Nop{},
		}
		var state database.State

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(500), startBlock)
	})
}

func TestGetEndBlock(t *testing.T) {
	tests := []struct {
		name          string
		chainBlock    uint64
		confirmations uint64
		start         uint64
		maxBlockRange uint64
		expectedEnd   uint64
	}{
		{
			name:          "chain tip below confirmations",
			chainBlock:    5,
			confirmations: 10,
			start:         0,
			maxBlockRange: 100,
			expectedEnd:   0,
		},
		{
			name:          "confirmed block behind start",
			chainBlock:    100,
			confirmations: 5,
			start:         96,
			maxBlockRange: 100,
			expectedEnd:   96,
		},
		{
			name:          "normal range within max",
			chainBlock:    200,
			confirmations: 5,
			start:         100,
			maxBlockRange: 1000,
			expectedEnd:   196,
		},
		{
			name:          "range capped by max block range",
			chainBlock:    2000,
			confirmations: 5,
			start:         100,
			maxBlockRange: 50,
			expectedEnd:   150,
		},
		{
			name:          "single block range",
			chainBlock:    101,
			confirmations: 1,
			start:         100,
			maxBlockRange: 1000,
			expectedEnd:   101,
		},
		{
			name:          "zero confirmations equivalent",
			chainBlock:    100,
			confirmations: 0,
			start:         50,
			maxBlockRange: 1000,
			expectedEnd:   101,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ix := Indexer[dbBlock, dbTransaction, struct{}]{
				confirmations: tc.confirmations,
				maxBlockRange: tc.maxBlockRange,
				log:           logger.Nop{},
			}
			state := &database.State{
				LastChainBlockNumber: tc.chainBlock,
			}

			result := ix.getEndBlock(state, tc.start)
			require.Equal(t, tc.expectedEnd, result)
		})
	}
}

func TestGetStartBlock(t *testing.T) {
	t.Run("returns computed start when ahead of last indexed", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			computedStartBlock: 100,
			log:                logger.Nop{},
		}
		state := &database.State{LastIndexedBlockNumber: 50}

		require.Equal(t, uint64(100), ix.getStartBlock(state))
	})

	t.Run("returns last indexed plus one when caught up", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			computedStartBlock: 100,
			log:                logger.Nop{},
		}
		state := &database.State{LastIndexedBlockNumber: 200}

		require.Equal(t, uint64(201), ix.getStartBlock(state))
	})

	t.Run("returns last indexed plus one when equal to computed start", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			computedStartBlock: 100,
			log:                logger.Nop{},
		}
		state := &database.State{LastIndexedBlockNumber: 100}

		require.Equal(t, uint64(101), ix.getStartBlock(state))
	})
}

func TestBlockRangeLen(t *testing.T) {
	t.Run("normal range", func(t *testing.T) {
		br := blockRange{start: 10, end: 20}
		require.Equal(t, uint64(10), br.len())
	})

	t.Run("empty range", func(t *testing.T) {
		br := blockRange{start: 10, end: 10}
		require.Equal(t, uint64(0), br.len())
	})

	t.Run("start exceeds end returns zero", func(t *testing.T) {
		br := blockRange{start: 20, end: 10}
		require.Equal(t, uint64(0), br.len())
	})

	t.Run("single block range", func(t *testing.T) {
		br := blockRange{start: 5, end: 6}
		require.Equal(t, uint64(1), br.len())
	})
}

func TestUpdateState(t *testing.T) {
	t.Run("empty results returns original state", func(t *testing.T) {
		state := &database.State{LastIndexedBlockNumber: 50}
		result := updateState[dbBlock, dbTransaction, struct{}](nil, state)
		require.Equal(t, state, result)
	})

	t.Run("updates last indexed block", func(t *testing.T) {
		state := &database.State{
			LastIndexedBlockNumber:     50,
			FirstIndexedBlockNumber:    10,
			FirstIndexedBlockTimestamp: 10000,
		}
		results := []BlockResult[dbBlock, dbTransaction, struct{}]{
			{Block: dbBlock{BlockNumber: 51, Timestamp: 51000}},
			{Block: dbBlock{BlockNumber: 52, Timestamp: 52000}},
		}

		newState := updateState(results, state)
		require.Equal(t, uint64(52), newState.LastIndexedBlockNumber)
		require.Equal(t, uint64(52000), newState.LastIndexedBlockTimestamp)
		// First indexed should not change after first iteration
		require.Equal(t, uint64(10), newState.FirstIndexedBlockNumber)
	})

	t.Run("sets first indexed block on first iteration", func(t *testing.T) {
		state := &database.State{
			LastIndexedBlockNumber: 0,
		}
		results := []BlockResult[dbBlock, dbTransaction, struct{}]{
			{Block: dbBlock{BlockNumber: 100, Timestamp: 100000}},
			{Block: dbBlock{BlockNumber: 101, Timestamp: 101000}},
		}

		newState := updateState(results, state)
		require.Equal(t, uint64(100), newState.FirstIndexedBlockNumber)
		require.Equal(t, uint64(100000), newState.FirstIndexedBlockTimestamp)
		require.Equal(t, uint64(101), newState.LastIndexedBlockNumber)
		require.Equal(t, uint64(101000), newState.LastIndexedBlockTimestamp)
	})

	t.Run("re-establishes first indexed block after a drop emptied the database", func(t *testing.T) {
		state := &database.State{
			LastIndexedBlockNumber:  200,
			FirstIndexedBlockNumber: 0,
		}
		results := []BlockResult[dbBlock, dbTransaction, struct{}]{
			{Block: dbBlock{BlockNumber: 201, Timestamp: 201000}},
			{Block: dbBlock{BlockNumber: 202, Timestamp: 202000}},
		}

		newState := updateState(results, state)
		require.Equal(t, uint64(201), newState.FirstIndexedBlockNumber)
		require.Equal(t, uint64(201000), newState.FirstIndexedBlockTimestamp)
	})

	t.Run("does not modify original state", func(t *testing.T) {
		state := &database.State{LastIndexedBlockNumber: 50}
		results := []BlockResult[dbBlock, dbTransaction, struct{}]{
			{Block: dbBlock{BlockNumber: 51, Timestamp: 51000}},
		}

		newState := updateState(results, state)
		require.Equal(t, uint64(50), state.LastIndexedBlockNumber)
		require.Equal(t, uint64(51), newState.LastIndexedBlockNumber)
	})
}

func TestShouldRunHistoryDrop(t *testing.T) {
	t.Run("disabled when interval is zero", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			historyDropInterval: 0,
			log:                 logger.Nop{},
		}
		state := &database.State{LastChainBlockTimestamp: 1000, LastHistoryDrop: 0}
		require.False(t, ix.shouldRunHistoryDrop(state))
	})

	t.Run("skips when chain timestamp behind last drop", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			historyDropInterval:  100,
			historyDropFrequency: 100,
			log:                  logger.Nop{},
		}
		state := &database.State{LastChainBlockTimestamp: 50, LastHistoryDrop: 100}
		require.False(t, ix.shouldRunHistoryDrop(state))
	})

	t.Run("skips when not enough time elapsed", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			historyDropInterval:  100,
			historyDropFrequency: 100,
			log:                  logger.Nop{},
		}
		state := &database.State{LastChainBlockTimestamp: 150, LastHistoryDrop: 100}
		require.False(t, ix.shouldRunHistoryDrop(state))
	})

	t.Run("runs when frequency threshold reached", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			historyDropInterval:  100,
			historyDropFrequency: 100,
			log:                  logger.Nop{},
		}
		state := &database.State{LastChainBlockTimestamp: 200, LastHistoryDrop: 100}
		require.True(t, ix.shouldRunHistoryDrop(state))
	})

	t.Run("runs on first drop when last drop is zero", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			historyDropInterval:  100,
			historyDropFrequency: 100,
			log:                  logger.Nop{},
		}
		state := &database.State{LastChainBlockTimestamp: 200, LastHistoryDrop: 0}
		require.True(t, ix.shouldRunHistoryDrop(state))
	})
}

func TestBinarySearchBlockByTime(t *testing.T) {
	timestamps := map[uint64]uint64{
		0:  1000,
		1:  1100,
		2:  1200,
		3:  1300,
		4:  1400,
		5:  1500,
		6:  1600,
		7:  1700,
		8:  1800,
		9:  1900,
		10: 2000,
	}

	blockchain := &timestampBlockchain{timestamps: timestamps}

	newIndexer := func() *Indexer[dbBlock, dbTransaction, struct{}] {
		return &Indexer[dbBlock, dbTransaction, struct{}]{
			blockchain: blockchain,
			log:        logger.Nop{},
		}
	}

	t.Run("finds first block within interval", func(t *testing.T) {
		ix := newIndexer()
		// latestTimestamp=2000, interval=500 → want first block where 2000-ts <= 500, i.e. ts >= 1500 → block 5
		result, err := ix.findEarliestBlockInInterval(context.Background(), 0, 10, 2000, 500)
		require.NoError(t, err)
		require.Equal(t, uint64(5), result)
	})

	t.Run("all blocks within interval returns low", func(t *testing.T) {
		ix := newIndexer()
		// interval=5000 covers all blocks
		result, err := ix.findEarliestBlockInInterval(context.Background(), 0, 10, 2000, 5000)
		require.NoError(t, err)
		require.Equal(t, uint64(0), result)
	})

	t.Run("single block range", func(t *testing.T) {
		ix := newIndexer()
		result, err := ix.findEarliestBlockInInterval(context.Background(), 5, 5, 2000, 500)
		require.NoError(t, err)
		require.Equal(t, uint64(5), result)
	})

	t.Run("no blocks within interval returns low", func(t *testing.T) {
		ix := newIndexer()
		// interval=0 → only exact match with latest timestamp, which is block 10
		result, err := ix.findEarliestBlockInInterval(context.Background(), 0, 10, 2000, 0)
		require.NoError(t, err)
		require.Equal(t, uint64(10), result)
	})
}

func TestFindBlockOnTheNode(t *testing.T) {
	tests := []struct {
		name      string
		available map[uint64]uint64 // block numbers the node serves
		low       uint64
		high      uint64
		expected  uint64
		wantErr   bool
	}{
		{
			name:      "finds lowest available block when lower blocks are pruned",
			available: map[uint64]uint64{5: 1, 6: 1, 7: 1, 8: 1, 9: 1, 10: 1},
			low:       1,
			high:      10,
			expected:  5,
		},
		{
			name:      "all blocks available returns low",
			available: map[uint64]uint64{1: 1, 2: 1, 3: 1, 4: 1, 5: 1},
			low:       1,
			high:      5,
			expected:  1,
		},
		{
			name:      "no blocks available returns error",
			available: map[uint64]uint64{},
			low:       1,
			high:      10,
			wantErr:   true,
		},
		{
			name:      "only the highest block available",
			available: map[uint64]uint64{10: 1},
			low:       1,
			high:      10,
			expected:  10,
		},
		{
			name:      "single block range available",
			available: map[uint64]uint64{5: 1},
			low:       5,
			high:      5,
			expected:  5,
		},
		{
			name:      "single block range not available returns error",
			available: map[uint64]uint64{},
			low:       5,
			high:      5,
			wantErr:   true,
		},
		{
			name:    "low greater than high returns error",
			low:     10,
			high:    5,
			wantErr: true,
		},
		{
			name:      "block zero available does not underflow",
			available: map[uint64]uint64{0: 1, 1: 1, 2: 1},
			low:       0,
			high:      2,
			expected:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ix := &Indexer[dbBlock, dbTransaction, struct{}]{
				blockchain:               &timestampBlockchain{timestamps: tc.available},
				blockchainWithoutBackoff: &timestampBlockchain{timestamps: tc.available},
				log:                      logger.Nop{},
			}

			result, err := ix.findBlockOnTheNode(t.Context(), tc.low, tc.high)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expected, result)
			}
		})
	}
}

// timestampBlockchain is a test helper that returns fixed timestamps by block number.
type timestampBlockchain struct {
	timestamps map[uint64]uint64
}

func (t *timestampBlockchain) GetLatestBlockInfo(context.Context) (*BlockInfo, error) {
	return nil, errors.New("not implemented")
}

func (t *timestampBlockchain) GetBlockResult(context.Context, uint64) (*BlockResult[dbBlock, dbTransaction, struct{}], error) {
	return nil, errors.New("not implemented")
}

func (t *timestampBlockchain) GetBlockTimestamp(_ context.Context, blockNumber uint64) (uint64, error) {
	ts, ok := t.timestamps[blockNumber]
	if !ok {
		return 0, errors.New("block not found")
	}
	return ts, nil
}

func (t *timestampBlockchain) GetServerInfo(context.Context) (string, error) {
	return "", nil
}
