package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/require"
)

// fakeSource stands in for the database, counting reads so the cache can be
// observed.
type fakeSource struct {
	mu     sync.Mutex
	state  database.State
	err    error
	reads  int
	onRead func()
}

func (f *fakeSource) GetState(context.Context) (*database.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.reads++
	if f.onRead != nil {
		f.onRead()
	}

	if f.err != nil {
		return nil, f.err
	}

	state := f.state

	return &state, nil
}

func (f *fakeSource) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.reads
}

var _ StateSource = &fakeSource{}

// The real database satisfies the interface for any entity types.
var _ StateSource = (*database.DB[database.Block, database.Transaction, database.Event])(nil)

func testOptions() Options {
	return Options{
		Confirmations:  12,
		MaxBlockLag:    1012,
		MaxProgressAge: 600 * time.Second,
		QueryTimeout:   time.Second,
	}
}

// newTestChecker builds a checker with a fixed clock.
func newTestChecker(t *testing.T, source StateSource, opts Options, now time.Time) *checker {
	t.Helper()

	handler, err := Handler(source, opts, logger.Nop{})
	require.NoError(t, err)

	c, ok := handler.(*checker)
	require.True(t, ok)

	c.now = func() time.Time { return now }

	return c
}

func TestHandlerRejectsUnusableInput(t *testing.T) {
	tests := []struct {
		name   string
		source StateSource
		log    logger.Logger
		opts   Options
	}{
		{name: "no source", source: nil, log: logger.Nop{}, opts: testOptions()},
		{name: "no logger", source: &fakeSource{}, log: nil, opts: testOptions()},
		{name: "no query timeout", source: &fakeSource{}, log: logger.Nop{}, opts: Options{MaxBlockLag: 1, MaxProgressAge: time.Second}},
		{name: "no lag allowance", source: &fakeSource{}, log: logger.Nop{}, opts: Options{QueryTimeout: time.Second, MaxProgressAge: time.Second}},
		{name: "no progress allowance", source: &fakeSource{}, log: logger.Nop{}, opts: Options{QueryTimeout: time.Second, MaxBlockLag: 1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := Handler(tc.source, tc.opts, tc.log)
			require.Error(t, err)
			require.Nil(t, handler)
		})
	}
}

func TestReportPredicate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	recent := uint64(now.Add(-4 * time.Second).Unix())
	stale := uint64(now.Add(-900 * time.Second).Unix())

	tests := []struct {
		name           string
		state          database.State
		expectedStatus string
		expectedCode   int
		expectedLag    uint64
	}{
		{
			name: "caught up",
			state: database.State{
				FirstIndexedBlockNumber: 780000, LastIndexedBlockNumber: 781234,
				LastChainBlockNumber: 781246, LastIndexedBlockUpdated: recent,
			},
			expectedStatus: StatusReady, expectedCode: http.StatusOK, expectedLag: 12,
		},
		{
			name:           "fresh database",
			state:          database.State{},
			expectedStatus: StatusInitializing, expectedCode: http.StatusServiceUnavailable,
		},
		{
			name: "inverted range after resuming past unindexed blocks",
			state: database.State{
				FirstIndexedBlockNumber: 900, LastIndexedBlockNumber: 800,
				LastChainBlockNumber: 900, LastIndexedBlockUpdated: recent,
			},
			expectedStatus: StatusInitializing, expectedCode: http.StatusServiceUnavailable, expectedLag: 100,
		},
		{
			name: "one block past the allowance",
			state: database.State{
				FirstIndexedBlockNumber: 1, LastIndexedBlockNumber: 1000,
				LastChainBlockNumber: 2013, LastIndexedBlockUpdated: recent,
			},
			expectedStatus: StatusCatchingUp, expectedCode: http.StatusServiceUnavailable, expectedLag: 1013,
		},
		{
			name: "exactly at the allowance is ready",
			state: database.State{
				FirstIndexedBlockNumber: 1, LastIndexedBlockNumber: 1000,
				LastChainBlockNumber: 2012, LastIndexedBlockUpdated: recent,
			},
			expectedStatus: StatusReady, expectedCode: http.StatusOK, expectedLag: 1012,
		},
		{
			name: "blocks pending with no recent progress is stalled",
			state: database.State{
				FirstIndexedBlockNumber: 1, LastIndexedBlockNumber: 1000,
				LastChainBlockNumber: 1050, LastIndexedBlockUpdated: stale,
			},
			expectedStatus: StatusStalled, expectedCode: http.StatusServiceUnavailable, expectedLag: 50,
		},
		{
			name: "idle indexer is not punished for an ageing stamp",
			state: database.State{
				FirstIndexedBlockNumber: 1, LastIndexedBlockNumber: 1000,
				LastChainBlockNumber: 1012, LastIndexedBlockUpdated: stale,
			},
			expectedStatus: StatusReady, expectedCode: http.StatusOK, expectedLag: 12,
		},
		{
			name: "chain head behind indexed head reports no lag",
			state: database.State{
				FirstIndexedBlockNumber: 1, LastIndexedBlockNumber: 1000,
				LastChainBlockNumber: 900, LastIndexedBlockUpdated: recent,
			},
			expectedStatus: StatusReady, expectedCode: http.StatusOK, expectedLag: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestChecker(t, &fakeSource{state: tc.state}, testOptions(), now)

			rec := httptest.NewRecorder()
			c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

			require.Equal(t, tc.expectedCode, rec.Code)
			require.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
			require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

			var report Report
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
			require.Equal(t, tc.expectedStatus, report.Status)
			require.Equal(t, tc.expectedCode == http.StatusOK, report.Ready)
			require.Equal(t, tc.expectedLag, report.BlockLag)
			require.Equal(t, uint64(1012), report.MaxBlockLag, "the effective limits must be echoed")
			require.Equal(t, uint64(600), report.MaxProgressAgeSeconds)
			require.Equal(t, now.Unix(), report.CheckedAt)
		})
	}
}

// TestReportHidesDatabaseErrorDetail checks that a database error's DSN never reaches the response.
func TestReportHidesDatabaseErrorDetail(t *testing.T) {
	dsn := "failed to connect to postgres://indexer:s3cret@db.internal:5432/flare_xrp_indexer"
	c := newTestChecker(t, &fakeSource{err: errors.New(dsn)}, testOptions(), time.Unix(1_700_000_000, 0))

	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	body := rec.Body.String()
	require.Contains(t, body, StatusUnavailable)
	for _, secret := range []string{"s3cret", "db.internal", "indexer:", "flare_xrp_indexer"} {
		require.NotContains(t, body, secret)
	}
}

func TestHandlerGuardsMethod(t *testing.T) {
	c := newTestChecker(t, &fakeSource{}, testOptions(), time.Unix(1_700_000_000, 0))

	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/health", nil))

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
	require.Empty(t, rec.Body.String())
}

func TestReadCollapsesRequestsOntoOneQuery(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	source := &fakeSource{state: database.State{FirstIndexedBlockNumber: 1, LastIndexedBlockNumber: 10, LastChainBlockNumber: 12}}

	opts := testOptions()
	opts.CacheTTL = time.Second

	c := newTestChecker(t, source, opts, now)

	for range 5 {
		c.read(context.Background())
	}
	require.Equal(t, 1, source.readCount(), "requests inside the TTL share one read")

	c.now = func() time.Time { return now.Add(2 * time.Second) }
	c.read(context.Background())
	require.Equal(t, 2, source.readCount(), "a read past the TTL refreshes")
}

// TestReadCachesFailures proves a database outage cannot queue one query per
// probe.
func TestReadCachesFailures(t *testing.T) {
	source := &fakeSource{err: errors.New("connection refused")}

	opts := testOptions()
	opts.CacheTTL = time.Second

	c := newTestChecker(t, source, opts, time.Unix(1_700_000_000, 0))

	for range 5 {
		report := c.read(context.Background())
		require.Equal(t, StatusUnavailable, report.Status)
	}

	require.Equal(t, 1, source.readCount())
}

// TestReadTTLRunsFromCompletion pins the TTL to the end of the previous read: a
// read slower than the TTL would otherwise expire the moment it finished.
func TestReadTTLRunsFromCompletion(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	source := &fakeSource{state: database.State{FirstIndexedBlockNumber: 1, LastIndexedBlockNumber: 10}}

	opts := testOptions()
	opts.CacheTTL = time.Second

	c := newTestChecker(t, source, opts, now)

	source.onRead = func() {
		now = now.Add(3 * opts.CacheTTL)
	}
	c.now = func() time.Time { return now }

	for range 5 {
		c.read(context.Background())
	}

	require.Equal(t, 1, source.readCount(), "the TTL must start when the read completes")
}

// TestReportOmitsAgeWhenProgressWasNeverWritten guards against reporting the
// whole Unix epoch as an age.
func TestReportOmitsAgeWhenProgressWasNeverWritten(t *testing.T) {
	c := newTestChecker(t, &fakeSource{state: database.State{}}, testOptions(), time.Unix(1_700_000_000, 0))

	require.Zero(t, c.read(context.Background()).ProgressAgeSeconds)
}

func TestReadIsSafeForConcurrentRequests(t *testing.T) {
	source := &fakeSource{state: database.State{FirstIndexedBlockNumber: 1, LastIndexedBlockNumber: 10, LastChainBlockNumber: 12}}

	opts := testOptions()
	opts.CacheTTL = time.Millisecond

	handler, err := Handler(source, opts, logger.Nop{})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 20 {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
			}
		}()
	}

	wg.Wait()
}
