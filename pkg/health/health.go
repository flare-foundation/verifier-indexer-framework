// Package health serves the framework's optional readiness endpoint.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
)

// Reported statuses. A closed set: an error value is never interpolated into a
// response, because a database error carries the connection string.
const (
	StatusReady        = "ready"
	StatusInitializing = "initializing"
	StatusCatchingUp   = "catching_up"
	StatusStalled      = "stalled"
	StatusUnavailable  = "unavailable"
)

// StateSource reads the persisted indexer state. The name follows the existing
// database.DB and indexer.DB method; its signature carries no type parameters,
// so one *database.DB satisfies this for any entity types.
type StateSource interface {
	GetState(context.Context) (*database.State, error)
}

// Options configures the readiness predicate.
type Options struct {
	// Confirmations is the indexer's confirmation depth; a caught-up indexer's lag
	// rests at exactly this value, so it gates the progress-age check.
	Confirmations uint64
	// MaxBlockLag is the largest tolerated gap behind the chain head.
	MaxBlockLag uint64
	// MaxProgressAge is how stale the last progress write may be, once blocks are
	// known to be pending, before the indexer counts as stalled.
	MaxProgressAge time.Duration
	// QueryTimeout bounds one state read.
	QueryTimeout time.Duration
	// CacheTTL bounds how often a request reaches the database, measured from the
	// end of the previous read. Zero reads on every request.
	CacheTTL time.Duration
}

// Report is the response body. Every field is always present; the block and age
// fields read zero when the status is StatusUnavailable, so alerts must key on
// `ready` or the status code, never on a number alone.
type Report struct {
	Status                  string `json:"status"`
	Ready                   bool   `json:"ready"`
	FirstIndexedBlockNumber uint64 `json:"first_indexed_block_number"`
	LastIndexedBlockNumber  uint64 `json:"last_indexed_block_number"`
	LastChainBlockNumber    uint64 `json:"last_chain_block_number"`
	BlockLag                uint64 `json:"block_lag"`
	ProgressAgeSeconds      uint64 `json:"progress_age_seconds"`
	MaxBlockLag             uint64 `json:"max_block_lag"`
	MaxProgressAgeSeconds   uint64 `json:"max_progress_age_seconds"`
	CheckedAt               int64  `json:"checked_at"`
}

// checker evaluates the predicate behind a short-lived cache.
type checker struct {
	source StateSource
	opts   Options
	log    logger.Logger
	now    func() time.Time

	mu       sync.Mutex
	cached   Report
	cachedAt time.Time
	hasCache bool
}

var _ http.Handler = &checker{}

// Handler returns the readiness handler, which is safe for concurrent use. It
// answers 200 when the advertised range is current and 503 otherwise, including
// when the state cannot be read.
func Handler(source StateSource, opts Options, log logger.Logger) (http.Handler, error) {
	if source == nil {
		return nil, errors.New("health: state source must be provided")
	}

	if log == nil {
		return nil, errors.New("health: logger must be provided")
	}

	if opts.QueryTimeout <= 0 {
		return nil, errors.New("health: query timeout must be positive")
	}

	if opts.MaxBlockLag == 0 {
		return nil, errors.New("health: max block lag must be positive")
	}

	if opts.MaxProgressAge <= 0 {
		return nil, errors.New("health: max progress age must be positive")
	}

	return &checker{source: source, opts: opts, log: log, now: time.Now}, nil
}

// ServeHTTP guards the method itself so the handler stays correct when mounted
// on a pattern that does not.
func (c *checker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)

		return
	}

	report := c.read(r.Context())

	// Marshalled before the status is written so a failure cannot commit a 200
	// with a truncated body.
	body, err := json.Marshal(report)
	if err != nil {
		c.log.Errorf("health: failed to encode report: %v", err)
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	status := http.StatusServiceUnavailable
	if report.Ready {
		status = http.StatusOK
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// A proxy caching a 200 across an outage is the failure this prevents.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

// read returns the current report, reusing a recent one within the TTL. The
// lock is held across the read so a burst of requests costs one query, and
// failures are cached too — an outage must not queue one query per probe.
func (c *checker) read(ctx context.Context) Report {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasCache && c.opts.CacheTTL > 0 && c.now().Sub(c.cachedAt) < c.opts.CacheTTL {
		return c.cached
	}

	report := c.evaluate(ctx)

	c.cached = report
	c.cachedAt = c.now()
	c.hasCache = true

	return report
}

// evaluate applies the predicate top-down; the first match wins.
func (c *checker) evaluate(ctx context.Context) Report {
	report := Report{
		MaxBlockLag:           c.opts.MaxBlockLag,
		MaxProgressAgeSeconds: uint64(c.opts.MaxProgressAge.Seconds()),
		CheckedAt:             c.now().Unix(),
	}

	queryCtx, cancel := context.WithTimeout(ctx, c.opts.QueryTimeout)
	defer cancel()

	state, err := c.source.GetState(queryCtx)
	if err != nil {
		c.log.Errorf("health: failed to read indexer state: %v", err)
		report.Status = StatusUnavailable

		return report
	}

	report.FirstIndexedBlockNumber = state.FirstIndexedBlockNumber
	report.LastIndexedBlockNumber = state.LastIndexedBlockNumber
	report.LastChainBlockNumber = state.LastChainBlockNumber
	report.BlockLag = blockLag(state)
	report.ProgressAgeSeconds = c.progressAge(state)

	switch {
	case emptyRange(state):
		report.Status = StatusInitializing
	case report.BlockLag > c.opts.MaxBlockLag:
		report.Status = StatusCatchingUp
	case report.BlockLag > c.opts.Confirmations && report.ProgressAgeSeconds > report.MaxProgressAgeSeconds:
		// Gated on the lag: a caught-up indexer persists nothing between polls,
		// so its progress stamp ages even though nothing is wrong.
		report.Status = StatusStalled
	default:
		report.Status = StatusReady
		report.Ready = true
	}

	return report
}

// blockLag returns how far the last indexed block trails the chain head, never
// underflowing when a reorg leaves the head behind.
func blockLag(state *database.State) uint64 {
	if state.LastChainBlockNumber <= state.LastIndexedBlockNumber {
		return 0
	}

	return state.LastChainBlockNumber - state.LastIndexedBlockNumber
}

// emptyRange reports whether the state advertises no usable range, per the
// contract on database.State.
func emptyRange(state *database.State) bool {
	return state.FirstIndexedBlockNumber == 0 ||
		state.FirstIndexedBlockNumber > state.LastIndexedBlockNumber
}

// progressAge returns the age of the last progress write. A stamp in the future
// reads as zero rather than wrapping.
func (c *checker) progressAge(state *database.State) uint64 {
	if state.LastIndexedBlockUpdated == 0 {
		return 0
	}

	now := c.now().Unix()
	written := int64(state.LastIndexedBlockUpdated)

	if now <= written {
		return 0
	}

	return uint64(now - written)
}
