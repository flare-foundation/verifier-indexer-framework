package framework

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"github.com/stretchr/testify/require"
)

// staticSource serves a fixed state, standing in for the database.
type staticSource struct {
	state database.State
}

func (s staticSource) GetState(context.Context) (*database.State, error) {
	return &s.state, nil
}

func healthTestConfig(address string) *config.Base {
	cfg := config.DefaultBase
	cfg.Indexer.Confirmations = 12
	cfg.Health.Enabled = true
	cfg.Health.ListenAddress = address

	return &cfg
}

func TestHealthOptions(t *testing.T) {
	t.Run("derives both allowances from the indexer configuration", func(t *testing.T) {
		opts := healthOptions(healthTestConfig(":0"))

		require.Equal(t, uint64(12+1000), opts.MaxBlockLag)
		// ceil(1000/8) rounds to 125 fetch rounds of 3s, doubled.
		require.Equal(t, 750*time.Second, opts.MaxProgressAge)
		// The 375s iteration beats the 90s jittered poll wait; plus one 3s poll, doubled.
		require.Equal(t, 756*time.Second, opts.MaxChainAge)
		require.Equal(t, 3*time.Second, opts.QueryTimeout)
		require.Equal(t, time.Second, opts.CacheTTL)
		require.Equal(t, uint64(12), opts.Confirmations)
	})

	t.Run("explicit values win", func(t *testing.T) {
		cfg := healthTestConfig(":0")
		cfg.Health.MaxBlockLag = 50
		cfg.Health.MaxProgressAgeSeconds = 90
		cfg.Health.MaxChainAgeSeconds = 45
		cfg.Health.CacheMillis = 0

		opts := healthOptions(cfg)

		require.Equal(t, uint64(50), opts.MaxBlockLag)
		require.Equal(t, 90*time.Second, opts.MaxProgressAge)
		require.Equal(t, 45*time.Second, opts.MaxChainAge)
		require.Zero(t, opts.CacheTTL)
	})

	t.Run("a short iteration lets the poll wait bound the chain allowance", func(t *testing.T) {
		cfg := healthTestConfig(":0")
		cfg.Indexer.MaxBlockRange = 8

		// One 3s fetch round is under the 90s jittered poll wait; plus one 3s poll, doubled.
		require.Equal(t, 186*time.Second, healthOptions(cfg).MaxChainAge)
	})

	t.Run("a low concurrency lengthens the derived progress allowance", func(t *testing.T) {
		cfg := healthTestConfig(":0")
		cfg.Indexer.MaxConcurrency = 2

		// 500 rounds of 3s, doubled: deriving this from the retry window instead
		// would report 600s and fire on a healthy indexer.
		require.Equal(t, 3000*time.Second, healthOptions(cfg).MaxProgressAge)
	})
}

func TestRunPairStopsBothSides(t *testing.T) {
	indexerErr := errors.New("indexer failed")
	endpointErr := errors.New("endpoint failed")

	tests := []struct {
		name        string
		runIndexer  func(context.Context) error
		serve       func(context.Context) error
		cancelFirst bool
		expectedErr error
	}{
		{
			name:       "indexer finishing cleanly stops the endpoint",
			runIndexer: func(context.Context) error { return nil },
			serve:      waitForContext,
		},
		{
			name:        "indexer error wins",
			runIndexer:  func(context.Context) error { return indexerErr },
			serve:       waitForContext,
			expectedErr: indexerErr,
		},
		{
			name:        "endpoint error wins",
			runIndexer:  waitForContext,
			serve:       func(context.Context) error { return endpointErr },
			expectedErr: endpointErr,
		},
		{
			name:        "a cancelled parent stops both",
			runIndexer:  func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			serve:       waitForContext,
			cancelFirst: true,
			expectedErr: context.Canceled,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if tc.cancelFirst {
				cancelled, cancelNow := context.WithCancel(ctx)
				cancelNow()
				ctx = cancelled
			}

			err := runPair(ctx, tc.runIndexer, tc.serve)
			if tc.expectedErr == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tc.expectedErr)
		})
	}
}

// waitForContext models a serve function that stops cleanly on cancellation.
func waitForContext(ctx context.Context) error {
	<-ctx.Done()

	return nil
}

func TestHealthServerServesAndShutsDown(t *testing.T) {
	cfg := healthTestConfig("127.0.0.1:0")
	source := staticSource{state: database.State{
		FirstIndexedBlockNumber: 1,
		LastIndexedBlockNumber:  1000,
		LastChainBlockNumber:    1012,
		LastIndexedBlockUpdated: uint64(time.Now().Unix()),
	}}

	hs, err := newHealthServer(cfg, source, logger.Nop{})
	require.NoError(t, err)

	address := hs.listener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- hs.run(ctx)
	}()

	resp, err := http.Get("http://" + address + "/health") //nolint:noctx // a test request to a local listener
	require.NoError(t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), `"status":"ready"`)

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err, "a cancelled run is a clean shutdown")
	case <-time.After(5 * time.Second):
		t.Fatal("health server did not shut down")
	}

	_, err = net.DialTimeout("tcp", address, time.Second)
	require.Error(t, err, "the listener must be closed after shutdown")
}

func TestNewHealthServerRejectsUnusableAddress(t *testing.T) {
	hs, err := newHealthServer(healthTestConfig("256.256.256.256:1"), staticSource{}, logger.Nop{})
	require.Error(t, err)
	require.Nil(t, hs)
}
