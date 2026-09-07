package framework

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/health"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/indexer"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
)

// healthShutdownGrace is added to the query timeout to bound the drain.
const healthShutdownGrace = 2 * time.Second

// maxHealthHeaderBytes caps request headers; the endpoint takes no input.
const maxHealthHeaderBytes = 8 << 10

// healthServer owns the listener and HTTP server for the readiness endpoint.
type healthServer struct {
	server   *http.Server
	listener net.Listener
	drain    time.Duration
	log      logger.Logger
}

// newHealthServer binds the listen address and builds the endpoint. Binding
// happens here rather than in the serve goroutine so an unusable address is a
// plain startup error instead of one racing a running indexer.
func newHealthServer(cfg *config.Base, source health.StateSource, log logger.Logger) (*healthServer, error) {
	queryTimeout := time.Duration(cfg.Timeout.RequestTimeoutMillis) * time.Millisecond

	handler, err := health.Handler(source, healthOptions(cfg), log)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("GET /health", handler)

	listener, err := net.Listen("tcp", cfg.Health.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on health address %q: %w", cfg.Health.ListenAddress, err)
	}

	return &healthServer{
		server: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: queryTimeout,
			ReadTimeout:       queryTimeout,
			WriteTimeout:      queryTimeout + healthShutdownGrace,
			IdleTimeout:       queryTimeout + healthShutdownGrace,
			MaxHeaderBytes:    maxHealthHeaderBytes,
		},
		listener: listener,
		drain:    queryTimeout + healthShutdownGrace,
		log:      log,
	}, nil
}

// healthOptions resolves the predicate's thresholds, deriving any left at zero
// from configuration the operator already had to set.
func healthOptions(cfg *config.Base) health.Options {
	queryTimeout := time.Duration(cfg.Timeout.RequestTimeoutMillis) * time.Millisecond

	maxBlockLag := cfg.Health.MaxBlockLag
	if maxBlockLag == 0 {
		maxBlockLag = cfg.Indexer.Confirmations + cfg.Indexer.MaxBlockRange
	}

	maxProgressAge := time.Duration(cfg.Health.MaxProgressAgeSeconds) * time.Second
	if maxProgressAge == 0 {
		maxProgressAge = 2 * worstCaseIteration(cfg)
	}

	maxChainAge := time.Duration(cfg.Health.MaxChainAgeSeconds) * time.Second
	if maxChainAge == 0 {
		// twice the longest gap between two successful polls: a worst-case
		// iteration while catching up, or the longest jittered wait while caught up
		maxChainAge = 2 * (max(worstCaseIteration(cfg), indexer.UpToDatePollMaxWait) + queryTimeout)
	}

	return health.Options{
		Confirmations:  cfg.Indexer.Confirmations,
		MaxBlockLag:    maxBlockLag,
		MaxProgressAge: maxProgressAge,
		MaxChainAge:    maxChainAge,
		QueryTimeout:   queryTimeout,
		CacheTTL:       time.Duration(cfg.Health.CacheMillis) * time.Millisecond,
	}
}

// worstCaseIteration bounds how long one iteration can legitimately take: every
// block in the range timing out, fetched max_concurrency at a time. Derived from
// the iteration rather than the retry window, which measures something else.
func worstCaseIteration(cfg *config.Base) time.Duration {
	concurrency := uint64(cfg.Indexer.MaxConcurrency)
	if concurrency == 0 {
		concurrency = 1
	}

	rounds := (cfg.Indexer.MaxBlockRange + concurrency - 1) / concurrency
	if rounds == 0 {
		rounds = 1
	}

	return time.Duration(rounds) * time.Duration(cfg.Timeout.RequestTimeoutMillis) * time.Millisecond
}

// run serves until the context is cancelled, then drains. It returns nil on a
// clean shutdown: reporting cancellation as an error would turn the indexer's
// own clean finish into a failure.
func (h *healthServer) run(ctx context.Context) error {
	h.log.Infof("health endpoint listening on %s/health", h.listener.Addr())

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- h.server.Serve(h.listener)
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("health endpoint failed: %w", err)
	case <-ctx.Done():
	}

	// The parent context is already cancelled, so a derived one would make
	// Shutdown return without draining.
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.drain)
	defer cancel()

	if err := h.server.Shutdown(drainCtx); err != nil {
		h.log.Warnf("health endpoint shutdown: %v", err)
	}

	return nil
}

// runPair runs the indexer and the endpoint together, returning once both have
// stopped. The indexer decides when to stop: errgroup cancels only on a non-nil
// error, and the indexer returns nil at end_block_number, so an explicit cancel
// is what stops the endpoint on a clean finish.
func runPair(ctx context.Context, runIndexer, serve func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return serve(egCtx)
	})

	eg.Go(func() error {
		defer cancel()

		return runIndexer(egCtx)
	})

	return eg.Wait()
}
