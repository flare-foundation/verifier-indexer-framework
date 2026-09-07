package indexer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/require"
)

// mockDB models the real state-write semantics rather than recording calls, so
// a test can tell the raise-only indexing save apart from the authoritative
// SaveState. `states` remains the call log; `stored` is the modelled row.
type mockDB struct {
	blocks       [][]*dbBlock
	transactions [][]*dbTransaction
	states       []*database.State
	chainTips    []database.State
	stored       *database.State
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

	if state == nil {
		return nil
	}

	stateCopy := *state
	m.states = append(m.states, &stateCopy)
	m.applyIndexingSave(state)

	return nil
}

// applyIndexingSave mirrors indexingStateAssignments: the loop's own progress
// columns are written, the first-indexed boundary is established only from the
// empty sentinel, and last_history_drop belongs to the history drop.
func (m *mockDB) applyIndexingSave(state *database.State) {
	if m.stored == nil {
		stored := *state
		m.stored = &stored

		return
	}

	m.stored.LastChainBlockNumber = state.LastChainBlockNumber
	m.stored.LastChainBlockTimestamp = state.LastChainBlockTimestamp
	m.stored.LastChainBlockUpdated = state.LastChainBlockUpdated
	m.stored.LastIndexedBlockNumber = state.LastIndexedBlockNumber
	m.stored.LastIndexedBlockTimestamp = state.LastIndexedBlockTimestamp
	m.stored.LastIndexedBlockUpdated = state.LastIndexedBlockUpdated

	if m.stored.FirstIndexedBlockNumber == 0 {
		m.stored.FirstIndexedBlockNumber = state.FirstIndexedBlockNumber
		m.stored.FirstIndexedBlockTimestamp = state.FirstIndexedBlockTimestamp
	}
}

func (m *mockDB) SaveState(ctx context.Context, state *database.State) error {
	if m.saveErr != nil {
		return m.saveErr
	}

	stateCopy := *state
	m.states = append(m.states, &stateCopy)

	stored := *state
	m.stored = &stored

	return nil
}

// SaveChainTip mirrors the column-scoped upsert.
func (m *mockDB) SaveChainTip(ctx context.Context, state *database.State) error {
	if m.saveErr != nil {
		return m.saveErr
	}

	m.chainTips = append(m.chainTips, *state)

	if m.stored == nil {
		m.stored = &database.State{}
	}

	m.stored.LastChainBlockNumber = state.LastChainBlockNumber
	m.stored.LastChainBlockTimestamp = state.LastChainBlockTimestamp
	m.stored.LastChainBlockUpdated = state.LastChainBlockUpdated

	return nil
}

func (m *mockDB) GetState(ctx context.Context) (*database.State, error) {
	if m.stored == nil {
		return &database.State{}, nil
	}

	stored := *m.stored

	return &stored, nil
}

func (m *mockDB) DropHistoryIteration(
	ctx context.Context,
	state *database.State,
	intervalSeconds, lastBlockTime uint64,
) (*database.State, error) {
	newState := *state
	newState.LastHistoryDrop = lastBlockTime

	return &newState, nil
}

type dbBlock struct {
	BlockNumber uint64 `gorm:"primaryKey"`
	Timestamp   uint64 `gorm:"index"`
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

type dbTransaction struct {
	Hash        string `gorm:"primaryKey"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

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

// legacyDB exposes only the v1.1.1 DB methods.
type legacyDB struct {
	inner *mockDB
}

func (l *legacyDB) SaveAllEntities(
	ctx context.Context, blocks []*dbBlock, transactions []*dbTransaction, events []*struct{}, state *database.State,
) error {
	return l.inner.SaveAllEntities(ctx, blocks, transactions, events, state)
}

func (l *legacyDB) GetState(ctx context.Context) (*database.State, error) {
	return l.inner.GetState(ctx)
}

func (l *legacyDB) DropHistoryIteration(
	ctx context.Context, state *database.State, intervalSeconds, lastBlockTime uint64,
) (*database.State, error) {
	return l.inner.DropHistoryIteration(ctx, state, intervalSeconds, lastBlockTime)
}

var (
	_ DB[dbBlock, dbTransaction, struct{}] = &legacyDB{}
	_ StateSaver                           = &mockDB{}
	_ ChainTipSaver                        = &mockDB{}
)

// TestLegacyDBFallsBack pins the v1.1.1 paths for a database without the
// optional writers: the state goes through SaveAllEntities and the chain tip is
// not written at all.
func TestLegacyDBFallsBack(t *testing.T) {
	inner := &mockDB{}
	ix := Indexer[dbBlock, dbTransaction, struct{}]{db: &legacyDB{inner: inner}, log: logger.Nop{}}
	state := &database.State{FirstIndexedBlockNumber: 5, LastIndexedBlockNumber: 9}

	require.NoError(t, ix.saveState(t.Context(), state))
	require.Len(t, inner.states, 1)
	require.Equal(t, state, inner.states[0])

	require.NoError(t, ix.saveChainTip(t.Context(), state))
	require.Empty(t, inner.chainTips)
}

// TestRunIterationPersistsChainTipWhileUpToDate pins the write the health
// endpoint depends on.
func TestRunIterationPersistsChainTipWhileUpToDate(t *testing.T) {
	db := &mockDB{}
	ix := Indexer[dbBlock, dbTransaction, struct{}]{
		blockchain: &timestampBlockchain{
			timestamps: map[uint64]uint64{},
			latest:     &BlockInfo{BlockNumber: 5, Timestamp: 5000},
		},
		confirmations:         100, // tip below confirmations, so nothing is indexable
		db:                    db,
		maxBlockRange:         10,
		maxConcurrency:        1,
		backoffMaxElapsedTime: time.Minute,
		log:                   logger.Nop{},
	}

	upToDateBackoff := backoff.NewExponentialBackOff(
		backoff.WithMaxElapsedTime(0),
		backoff.WithInitialInterval(time.Millisecond),
		backoff.WithMaxInterval(time.Millisecond),
	)

	var historyDropLock sync.Mutex
	before := uint64(time.Now().Unix())

	state, err := ix.runIteration(
		t.Context(),
		&database.State{LastIndexedBlockNumber: 1, LastIndexedBlockUpdated: 1},
		&historyDropLock,
		make(chan *database.State, 1),
		upToDateBackoff,
	)
	require.NoError(t, err)
	require.Equal(t, uint64(5), state.LastChainBlockNumber)

	require.Empty(t, db.states, "nothing to index, so no batch save")
	require.Len(t, db.chainTips, 1)
	require.NotNil(t, db.stored)
	require.Equal(t, uint64(5), db.stored.LastChainBlockNumber)
	require.Equal(t, uint64(5000), db.stored.LastChainBlockTimestamp)
	require.GreaterOrEqual(t, db.stored.LastChainBlockUpdated, before)
	require.Zero(t, db.stored.LastIndexedBlockNumber, "only the chain-tip columns may move")
}

// TestRunIterationHonoursContextWhileUpToDate pins graceful shutdown for a
// caught-up indexer: the wait between polls must observe cancellation.
func TestRunIterationHonoursContextWhileUpToDate(t *testing.T) {
	ix := Indexer[dbBlock, dbTransaction, struct{}]{
		blockchain: &timestampBlockchain{
			timestamps: map[uint64]uint64{},
			latest:     &BlockInfo{BlockNumber: 5, Timestamp: 5000},
		},
		confirmations:         100, // tip below confirmations, so nothing is indexable
		db:                    &mockDB{},
		maxBlockRange:         10,
		maxConcurrency:        1,
		backoffMaxElapsedTime: time.Minute,
		log:                   logger.Nop{},
	}

	// A saturated up-to-date backoff would otherwise wait for the best part of
	// an hour before looking at the context again.
	upToDateBackoff := backoff.NewExponentialBackOff(
		backoff.WithMaxElapsedTime(0),
		backoff.WithInitialInterval(time.Hour),
		backoff.WithMaxInterval(time.Hour),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	var historyDropLock sync.Mutex

	start := time.Now()
	_, err := ix.runIteration(
		ctx,
		&database.State{LastIndexedBlockNumber: 1, LastIndexedBlockUpdated: 1},
		&historyDropLock,
		make(chan *database.State, 1),
		upToDateBackoff,
	)

	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), 10*time.Second, "the wait must be interrupted by cancellation")
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

	t.Run("never lowers the boundary onto rows outside the advertised range", func(t *testing.T) {
		// After resuming past unindexed blocks the boundary sits above stale
		// surviving rows; the drop's recomputed (lower) boundary must not win.
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
		require.Equal(t, uint64(305), state.FirstIndexedBlockNumber)
		require.Equal(t, uint64(805), state.FirstIndexedBlockTimestamp)
		require.Equal(t, uint64(12345), state.LastHistoryDrop)
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

	// Chain with blocks 0-100, timestamps n*10, history interval 200: the
	// history drop window starts at block 80.
	newHistoryDropIndexer := func(db *mockDB) *Indexer[dbBlock, dbTransaction, struct{}] {
		timestamps := make(map[uint64]uint64)
		for n := uint64(0); n <= 100; n++ {
			timestamps[n] = n * 10
		}

		return &Indexer[dbBlock, dbTransaction, struct{}]{
			blockchain: &timestampBlockchain{
				timestamps: timestamps,
				latest:     &BlockInfo{BlockNumber: 100, Timestamp: 1000},
			},
			db:                  db,
			historyDropInterval: 200,
			log:                 logger.Nop{},
		}
	}

	t.Run("resumes within history window from last indexed block", func(t *testing.T) {
		db := &mockDB{}
		ix := newHistoryDropIndexer(db)
		state := database.State{
			LastIndexedBlockNumber:     90,
			FirstIndexedBlockNumber:    85,
			FirstIndexedBlockTimestamp: 850,
		}

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(91), startBlock)
		require.Equal(t, uint64(85), state.FirstIndexedBlockNumber)
		require.Empty(t, db.states, "no state write needed on a normal resume")
	})

	t.Run("resumes contiguously when the window starts right after the last indexed block", func(t *testing.T) {
		db := &mockDB{}
		ix := newHistoryDropIndexer(db)
		state := database.State{
			LastIndexedBlockNumber:     79,
			FirstIndexedBlockNumber:    60,
			FirstIndexedBlockTimestamp: 600,
		}

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(80), startBlock)
		require.Equal(t, uint64(60), state.FirstIndexedBlockNumber, "contiguous coverage must be kept")
		require.Empty(t, db.states)
	})

	t.Run("resume past unindexed blocks moves and persists the coverage boundary", func(t *testing.T) {
		// The stored row already carries a boundary, so only an authoritative
		// write can move it: the raise-only indexing save cannot.
		db := &mockDB{stored: &database.State{
			LastIndexedBlockNumber:     50,
			FirstIndexedBlockNumber:    10,
			FirstIndexedBlockTimestamp: 100,
		}}
		ix := newHistoryDropIndexer(db)

		// Last indexed block 50 is far behind the history window start (80):
		// blocks 51-79 will never be indexed.
		state := database.State{
			LastIndexedBlockNumber:     50,
			FirstIndexedBlockNumber:    10,
			FirstIndexedBlockTimestamp: 100,
		}

		startBlock, err := ix.getInitialStartBlockNumber(ctx, &state)
		require.NoError(t, err)
		require.Equal(t, uint64(80), startBlock)
		require.Equal(t, uint64(80), state.FirstIndexedBlockNumber)
		require.Equal(t, uint64(800), state.FirstIndexedBlockTimestamp)

		require.Len(t, db.states, 1, "moved boundary must be persisted before indexing begins")
		require.Equal(t, &state, db.states[0])
		require.Equal(t, uint64(80), db.stored.FirstIndexedBlockNumber,
			"the boundary must reach the stored row, which a raise-only save cannot do")
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

	t.Run("indexes block zero on a fresh database", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			computedStartBlock: 0,
			log:                logger.Nop{},
		}
		var state database.State

		require.Equal(t, uint64(0), ix.getStartBlock(&state), "block 0 must not be skipped")
	})

	t.Run("moves past block zero once it is indexed", func(t *testing.T) {
		ix := Indexer[dbBlock, dbTransaction, struct{}]{
			computedStartBlock: 0,
			log:                logger.Nop{},
		}
		// The update stamp is what distinguishes this from a fresh database.
		state := &database.State{LastIndexedBlockNumber: 0, LastIndexedBlockUpdated: 1700000000}

		require.Equal(t, uint64(1), ix.getStartBlock(state))
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
				blockchain: &timestampBlockchain{timestamps: tc.available},
				log:        logger.Nop{},
			}

			result, err := ix.findBlockOnTheNode(t.Context(), tc.low, tc.high, ix.sentinelProbe)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expected, result)
			}
		})
	}

	t.Run("transient failure during search aborts instead of skipping the block", func(t *testing.T) {
		available := map[uint64]uint64{5: 1, 6: 1, 7: 1, 8: 1, 9: 1, 10: 1}
		ix := &Indexer[dbBlock, dbTransaction, struct{}]{
			blockchain: &timestampBlockchain{timestamps: available, transient: map[uint64]bool{3: true}},
			log:        logger.Nop{},
		}

		_, err := ix.findBlockOnTheNode(t.Context(), 1, 10, ix.sentinelProbe)
		require.ErrorContains(t, err, "during search")
	})

	t.Run("legacy probe counts any failure as absent", func(t *testing.T) {
		// Blocks 1 to 4 fail with a plain error, as a v1.1.1 client reports a pruned block.
		chain := &timestampBlockchain{
			timestamps: map[uint64]uint64{5: 1, 6: 1, 7: 1, 8: 1, 9: 1, 10: 1},
			transient:  map[uint64]bool{1: true, 2: true, 3: true, 4: true},
		}
		ix := &Indexer[dbBlock, dbTransaction, struct{}]{
			blockchain: chain, rawBlockchain: chain, requestTimeout: time.Second, log: logger.Nop{},
		}

		result, err := ix.findBlockOnTheNode(t.Context(), 1, 10, ix.legacyProbe)
		require.NoError(t, err)
		require.Equal(t, uint64(5), result)
	})

	t.Run("legacy probe still aborts on shutdown", func(t *testing.T) {
		chain := &timestampBlockchain{timestamps: map[uint64]uint64{5: 1}}
		ix := &Indexer[dbBlock, dbTransaction, struct{}]{
			blockchain: chain, rawBlockchain: chain, requestTimeout: time.Second, log: logger.Nop{},
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := ix.findBlockOnTheNode(ctx, 1, 10, ix.legacyProbe)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestGetMinBlockWithinHistoryInterval(t *testing.T) {
	t.Run("a plain failure on the start block falls back to the v1.1.1 search", func(t *testing.T) {
		// Blocks above the start block must be available, or the subtest passes
		// for the wrong reason: the search would fail either way.
		timestamps := make(map[uint64]uint64, 81)
		for n := uint64(20); n <= 100; n++ {
			timestamps[n] = n * 10
		}

		chain := &timestampBlockchain{
			timestamps: timestamps,
			transient:  map[uint64]bool{10: true},
			latest:     &BlockInfo{BlockNumber: 100, Timestamp: 1000},
		}
		ix := &Indexer[dbBlock, dbTransaction, struct{}]{
			blockchain:          chain,
			rawBlockchain:       chain,
			requestTimeout:      time.Second,
			startBlockNumber:    10,
			historyDropInterval: 200,
			log:                 logger.Nop{},
		}

		// A client that never wraps the sentinel: the block reads as pruned, the
		// unretried search lands on 20 and the boundary on the first block within 200s.
		result, err := ix.getMinBlockWithinHistoryInterval(t.Context())
		require.NoError(t, err)
		require.Equal(t, uint64(20), ix.startBlockNumber)
		require.Equal(t, uint64(80), result)
	})

	t.Run("a timeout on the start block aborts instead of guessing", func(t *testing.T) {
		timestamps := make(map[uint64]uint64, 81)
		for n := uint64(20); n <= 100; n++ {
			timestamps[n] = n * 10
		}

		chain := &timestampBlockchain{
			timestamps: timestamps,
			timeouts:   map[uint64]bool{10: true},
			latest:     &BlockInfo{BlockNumber: 100, Timestamp: 1000},
		}
		ix := &Indexer[dbBlock, dbTransaction, struct{}]{
			blockchain:          chain,
			rawBlockchain:       chain,
			requestTimeout:      time.Second,
			startBlockNumber:    10,
			historyDropInterval: 200,
			log:                 logger.Nop{},
		}

		_, err := ix.getMinBlockWithinHistoryInterval(t.Context())
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, uint64(10), ix.startBlockNumber, "a node that only times out must not move the start block")
	})

	t.Run("start block ahead of the chain tip waits instead of failing", func(t *testing.T) {
		ix := &Indexer[dbBlock, dbTransaction, struct{}]{
			blockchain: &timestampBlockchain{
				timestamps: map[uint64]uint64{},
				latest:     &BlockInfo{BlockNumber: 100, Timestamp: 1000},
			},
			startBlockNumber:    200,
			historyDropInterval: 200,
			log:                 logger.Nop{},
		}

		start, err := ix.getMinBlockWithinHistoryInterval(t.Context())
		require.NoError(t, err, "a start block above the tip is a wait, not an error")
		require.Equal(t, uint64(200), start)
	})

	t.Run("pruned start block falls back to the oldest available block", func(t *testing.T) {
		timestamps := make(map[uint64]uint64)
		for n := uint64(50); n <= 100; n++ {
			timestamps[n] = n * 10
		}

		ix := &Indexer[dbBlock, dbTransaction, struct{}]{
			blockchain: &timestampBlockchain{
				timestamps: timestamps,
				latest:     &BlockInfo{BlockNumber: 100, Timestamp: 1000},
			},
			startBlockNumber:    10,
			historyDropInterval: 200,
			log:                 logger.Nop{},
		}

		// Blocks below 50 are pruned: the start block moves to 50 and the
		// history boundary is the first block within 200s of the chain tip.
		result, err := ix.getMinBlockWithinHistoryInterval(t.Context())
		require.NoError(t, err)
		require.Equal(t, uint64(50), ix.startBlockNumber)
		require.Equal(t, uint64(80), result)
	})
}

func TestRetryWithBackoffBlockNotFound(t *testing.T) {
	t.Run("does not retry when the block is not found", func(t *testing.T) {
		client := &flakyBlockchain{errs: []error{fmt.Errorf("block 5: %w", ErrBlockNotFound)}}
		bwb := newBlockchainWithBackoff[dbBlock, dbTransaction, struct{}](client, 5*time.Second, 100*time.Millisecond, logger.Nop{})

		_, err := bwb.GetBlockTimestamp(t.Context(), 5)
		require.ErrorIs(t, err, ErrBlockNotFound)
		require.Equal(t, 1, client.calls)
	})

	t.Run("retries transient errors", func(t *testing.T) {
		client := &flakyBlockchain{errs: []error{errors.New("connection reset")}}
		bwb := newBlockchainWithBackoff[dbBlock, dbTransaction, struct{}](client, 5*time.Second, 100*time.Millisecond, logger.Nop{})

		ts, err := bwb.GetBlockTimestamp(t.Context(), 5)
		require.NoError(t, err)
		require.Equal(t, uint64(42), ts)
		require.Equal(t, 2, client.calls)
	})

	t.Run("does not retry invalid data", func(t *testing.T) {
		client := &flakyBlockchain{errs: []error{fmt.Errorf("bad tx: %w", ErrInvalidData)}}
		bwb := newBlockchainWithBackoff[dbBlock, dbTransaction, struct{}](client, 5*time.Second, 100*time.Millisecond, logger.Nop{})

		_, err := bwb.GetBlockTimestamp(t.Context(), 5)
		require.ErrorIs(t, err, ErrInvalidData)
		require.Equal(t, 1, client.calls)
	})

	t.Run("block fetch retries a not-found from a lagging backend", func(t *testing.T) {
		client := &flakyBlockchain{errs: []error{fmt.Errorf("block 5: %w", ErrBlockNotFound)}}
		bwb := newBlockchainWithBackoff[dbBlock, dbTransaction, struct{}](client, 5*time.Second, 100*time.Millisecond, logger.Nop{})

		result, err := bwb.GetBlockResult(t.Context(), 5)
		require.NoError(t, err)
		require.Equal(t, uint64(5), result.Block.BlockNumber)
		require.Equal(t, 2, client.calls)
	})

	t.Run("block fetch gives up on a not-found that outlasts the window", func(t *testing.T) {
		notFound := fmt.Errorf("block 5: %w", ErrBlockNotFound)
		client := &flakyBlockchain{errs: []error{notFound, notFound, notFound, notFound}}
		bwb := newBlockchainWithBackoff[dbBlock, dbTransaction, struct{}](client, time.Second, 100*time.Millisecond, logger.Nop{})

		_, err := bwb.GetBlockResult(t.Context(), 5)
		require.ErrorIs(t, err, ErrBlockNotFound, "still the sentinel, so the iteration loop stops instead of retrying again")
		require.GreaterOrEqual(t, client.calls, 2)
	})

	t.Run("block fetch does not retry invalid data", func(t *testing.T) {
		client := &flakyBlockchain{errs: []error{fmt.Errorf("bad tx: %w", ErrInvalidData)}}
		bwb := newBlockchainWithBackoff[dbBlock, dbTransaction, struct{}](client, 5*time.Second, 100*time.Millisecond, logger.Nop{})

		_, err := bwb.GetBlockResult(t.Context(), 5)
		require.ErrorIs(t, err, ErrInvalidData)
		require.Equal(t, 1, client.calls)
	})
}

func TestRunIterationAbortsOnInvalidData(t *testing.T) {
	cfg := config.Base{
		Indexer: config.Indexer{
			Confirmations:  1,
			MaxBlockRange:  1,
			MaxConcurrency: 1,
		},
		Timeout: config.TimeoutConfig{
			BackoffMaxElapsedTimeSeconds: 60,
			RequestTimeoutMillis:         1000,
		},
	}

	chain := &flakyBlockchain{
		latest: &BlockInfo{BlockNumber: 102, Timestamp: 102000},
		errs:   []error{fmt.Errorf("unmarshal transaction 0: %w", ErrInvalidData)},
	}
	ix := New(&cfg, &mockDB{}, chain, logger.Nop{})

	var historyDropLock sync.Mutex
	historyDropResults := make(chan *database.State, 1)
	upToDateBackoff := backoff.NewExponentialBackOff(backoff.WithMaxElapsedTime(0))

	state := &database.State{LastIndexedBlockNumber: 100}

	start := time.Now()
	_, err := ix.runIteration(t.Context(), state, &historyDropLock, historyDropResults, upToDateBackoff)
	require.ErrorIs(t, err, ErrInvalidData)
	require.Equal(t, 1, chain.calls, "deterministic data failures must not be retried")
	require.Less(t, time.Since(start), 10*time.Second, "must abort immediately, not after the backoff window")
}

// timestampBlockchain is a test helper that returns fixed timestamps by block
// number. Blocks listed in transient fail with a non-permanent error; absent
// blocks fail with ErrBlockNotFound.
type timestampBlockchain struct {
	timestamps map[uint64]uint64
	transient  map[uint64]bool
	timeouts   map[uint64]bool
	latest     *BlockInfo
}

func (t *timestampBlockchain) GetLatestBlockInfo(context.Context) (*BlockInfo, error) {
	if t.latest == nil {
		return nil, errors.New("not implemented")
	}
	return t.latest, nil
}

func (t *timestampBlockchain) GetBlockResult(context.Context, uint64) (*BlockResult[dbBlock, dbTransaction, struct{}], error) {
	return nil, errors.New("not implemented")
}

func (t *timestampBlockchain) GetBlockTimestamp(_ context.Context, blockNumber uint64) (uint64, error) {
	if t.transient[blockNumber] {
		return 0, errors.New("connection reset")
	}

	if t.timeouts[blockNumber] {
		return 0, context.DeadlineExceeded
	}

	ts, ok := t.timestamps[blockNumber]
	if !ok {
		return 0, fmt.Errorf("block %d: %w", blockNumber, ErrBlockNotFound)
	}
	return ts, nil
}

func (t *timestampBlockchain) GetServerInfo(context.Context) (string, error) {
	return "", nil
}

// flakyBlockchain returns queued errors from the block calls before succeeding,
// counting the calls made; latest, when set, is served without error.
type flakyBlockchain struct {
	calls  int
	errs   []error
	latest *BlockInfo
}

func (f *flakyBlockchain) GetLatestBlockInfo(context.Context) (*BlockInfo, error) {
	if f.latest == nil {
		return nil, errors.New("not implemented")
	}
	return f.latest, nil
}

func (f *flakyBlockchain) GetBlockResult(_ context.Context, blockNumber uint64) (*BlockResult[dbBlock, dbTransaction, struct{}], error) {
	if err := f.next(); err != nil {
		return nil, err
	}
	return &BlockResult[dbBlock, dbTransaction, struct{}]{Block: dbBlock{BlockNumber: blockNumber, Timestamp: 42}}, nil
}

func (f *flakyBlockchain) GetBlockTimestamp(context.Context, uint64) (uint64, error) {
	if err := f.next(); err != nil {
		return 0, err
	}
	return 42, nil
}

// next counts the call and pops the next scripted error, if any.
func (f *flakyBlockchain) next() error {
	f.calls++
	if len(f.errs) == 0 {
		return nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return err
}

func (f *flakyBlockchain) GetServerInfo(context.Context) (string, error) {
	return "", errors.New("not implemented")
}
