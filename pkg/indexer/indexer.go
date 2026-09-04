package indexer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// ErrBlockNotFound marks a requested block as not existing on the node, e.g.
// because it was pruned from the node's history or is not yet available.
// BlockchainClient implementations must return an error wrapping it in that
// case so the indexer can distinguish missing blocks from transient failures.
var ErrBlockNotFound = errors.New("block not found on the node")

// ErrInvalidData marks node data that the implementation cannot process, e.g.
// a transaction in a validated block that fails to parse. Such failures are
// deterministic: BlockchainClient implementations must return an error
// wrapping it so the indexer aborts immediately with a clear error instead of
// retrying for the full backoff window. Skipping the data is not an option for
// an attestation indexer, so resolving this requires operator action (usually
// an indexer upgrade that understands the new data format).
var ErrInvalidData = errors.New("invalid data from the node")

// BlockchainClient defines the operations the indexer requires from a blockchain node.
//
// Methods taking a block number must return an error wrapping ErrBlockNotFound
// when the block does not exist on the node; such errors are not retried.
// Deterministic processing failures must wrap ErrInvalidData; they are not
// retried and abort the indexer. All other errors are treated as transient.
type BlockchainClient[B database.Block, T database.Transaction, E database.Event] interface {
	// GetLatestBlockInfo returns the number and timestamp of the latest block.
	GetLatestBlockInfo(context.Context) (*BlockInfo, error)
	// GetBlockResult returns the full block data for the given block number.
	GetBlockResult(context.Context, uint64) (*BlockResult[B, T, E], error)
	// GetBlockTimestamp returns the timestamp for the given block number.
	GetBlockTimestamp(context.Context, uint64) (uint64, error)
	// GetServerInfo returns the version string of the blockchain node.
	GetServerInfo(context.Context) (string, error)
}

// DB defines the database operations required by the indexer.
type DB[B database.Block, T database.Transaction, E database.Event] interface {
	// SaveAllEntities persists blocks, transactions, events, and state atomically.
	// It establishes the first-indexed boundary only from the empty sentinel and
	// never lowers it, so a regular save cannot overwrite a boundary a concurrent
	// history drop raised.
	SaveAllEntities(
		ctx context.Context,
		blocks []*B,
		transactions []*T,
		events []*E,
		state *database.State,
	) error
	// SaveState persists the full state row authoritatively, used when the caller
	// holds the authoritative first-indexed boundary (applying a history drop
	// result or resuming past unindexed blocks).
	SaveState(ctx context.Context, state *database.State) error
	// GetState retrieves the current indexer state.
	GetState(ctx context.Context) (*database.State, error)
	// DropHistoryIteration deletes entities older than the given interval.
	DropHistoryIteration(
		ctx context.Context,
		state *database.State,
		intervalSeconds, lastBlockTime uint64,
	) (*database.State, error)
}

// BlockInfo holds the block number and timestamp for a single block.
type BlockInfo struct {
	BlockNumber uint64
	Timestamp   uint64
}

// iterationResult holds the block results and updated state produced by a single
// indexer iteration.
type iterationResult[B database.Block, T database.Transaction, E database.Event] struct {
	blockResults []BlockResult[B, T, E]
	state        *database.State
}

// BlockResult contains the block, its transactions, and its events as fetched
// from the blockchain.
type BlockResult[B database.Block, T database.Transaction, E database.Event] struct {
	Block        B
	Transactions []T
	Events       []E
}

// New creates an Indexer configured from the provided base configuration,
// database, and blockchain client.
func New[B database.Block, T database.Transaction, E database.Event](
	cfg *config.Base, db DB[B, T, E], blockchain BlockchainClient[B, T, E], log logger.Logger,
) Indexer[B, T, E] {
	backoffMaxElapsedTime := time.Duration(cfg.Timeout.BackoffMaxElapsedTimeSeconds) * time.Second
	historyDropFrequency := cfg.DB.HistoryDropFrequency
	if historyDropFrequency == 0 {
		historyDropFrequency = cfg.DB.HistoryDrop
	}

	return Indexer[B, T, E]{
		blockchain: newBlockchainWithBackoff(
			blockchain, backoffMaxElapsedTime,
			time.Duration(cfg.Timeout.RequestTimeoutMillis)*time.Millisecond,
			log,
		),
		confirmations:         cfg.Indexer.Confirmations,
		db:                    db,
		maxBlockRange:         cfg.Indexer.MaxBlockRange,
		maxConcurrency:        cfg.Indexer.MaxConcurrency,
		startBlockNumber:      cfg.Indexer.StartBlockNumber,
		endBlockNumber:        cfg.Indexer.EndBlockNumber,
		historyDropInterval:   cfg.DB.HistoryDrop,
		historyDropFrequency:  historyDropFrequency,
		backoffMaxElapsedTime: backoffMaxElapsedTime,
		log:                   log,
	}
}

// Indexer continuously fetches blocks from a blockchain and stores them in a
// database, with support for history pruning and configurable concurrency.
type Indexer[B database.Block, T database.Transaction, E database.Event] struct {
	blockchain            BlockchainClient[B, T, E]
	confirmations         uint64
	db                    DB[B, T, E]
	maxBlockRange         uint64
	maxConcurrency        int
	startBlockNumber      uint64
	computedStartBlock    uint64
	endBlockNumber        uint64
	historyDropInterval   uint64
	historyDropFrequency  uint64
	backoffMaxElapsedTime time.Duration
	log                   logger.Logger
}

// Run starts the indexer loop, fetching and persisting blocks until the context
// is cancelled or the configured end block is reached.
func (ix *Indexer[B, T, E]) Run(ctx context.Context) error {
	upToDateBackoff := backoff.NewExponentialBackOff(backoff.WithMaxElapsedTime(0))
	historyDropResults := make(chan *database.State, 1)
	var historyDropLock sync.Mutex

	state, err := ix.db.GetState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get indexer state from database: %w", err)
	}

	startBlockNumber, err := ix.getInitialStartBlockNumber(ctx, state)
	if err != nil {
		return fmt.Errorf("failed to get initial start block number: %w", err)
	}

	ix.computedStartBlock = startBlockNumber

	for {
		select {
		case <-ctx.Done():
			ix.log.Info("indexer shutting down")
			return ctx.Err()
		default:
		}

		state, err = ix.runIteration(ctx, state, &historyDropLock, historyDropResults, upToDateBackoff)
		if err != nil {
			return err
		}

		// If an ending block number was configured, stop indexing when reached.
		if ix.endBlockNumber != 0 && ix.endBlockNumber <= state.LastIndexedBlockNumber {
			return nil
		}
	}
}

// getInitialStartBlockNumber determines which block to begin indexing from,
// considering the database state and history drop configuration.
func (ix *Indexer[B, T, E]) getInitialStartBlockNumber(ctx context.Context, state *database.State) (uint64, error) {
	// If history drop is disabled: we either start from after the last indexed block, or else we start
	// from the configured start block number if the DB is empty.
	if ix.historyDropInterval == 0 {
		if !indexedNothing(state) {
			ix.log.Infof("resuming after last indexed block from the database: %d", state.LastIndexedBlockNumber)
			return state.LastIndexedBlockNumber + 1, nil
		}

		ix.log.Infof("no blocks indexed yet, starting from configured start block number: %d", ix.startBlockNumber)
		return ix.startBlockNumber, nil
	}

	// History drop is enabled so calculate the start index based on it.
	historyDropStartBlock, err := ix.getMinBlockWithinHistoryInterval(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate start block number based on history drop interval: %w", err)
	}

	if indexedNothing(state) {
		ix.log.Infof("no blocks indexed yet within history drop interval, starting from block number: %d", historyDropStartBlock)
		return historyDropStartBlock, nil
	}

	if state.LastIndexedBlockNumber+1 >= historyDropStartBlock {
		ix.log.Infof("resuming after last indexed block from the database: %d", state.LastIndexedBlockNumber)
		return state.LastIndexedBlockNumber + 1, nil
	}

	// Indexing resumes ahead of the last indexed block, so the blocks in between
	// are never indexed. Move the advertised coverage boundary to the new start
	// block and persist it before indexing begins, so the state never claims the
	// gap or the stale rows below it as covered.
	firstBlockTime, err := ix.blockchain.GetBlockTimestamp(ctx, historyDropStartBlock)
	if err != nil {
		return 0, fmt.Errorf("failed to get timestamp for resume start block %d: %w", historyDropStartBlock, err)
	}

	ix.log.Warnf(
		"resuming from block %d leaves blocks %d to %d unindexed, moving the first indexed block boundary",
		historyDropStartBlock, state.LastIndexedBlockNumber+1, historyDropStartBlock-1,
	)

	state.FirstIndexedBlockNumber = historyDropStartBlock
	state.FirstIndexedBlockTimestamp = firstBlockTime

	if err := ix.db.SaveState(ctx, state); err != nil {
		return 0, fmt.Errorf("failed to persist state when resuming past unindexed blocks: %w", err)
	}

	return historyDropStartBlock, nil
}

// indexedNothing reports whether no block has ever been saved.
// LastIndexedBlockNumber zero is ambiguous — nothing indexed, or block 0
// indexed — so the update stamp, written by every save, breaks the tie.
func indexedNothing(state *database.State) bool {
	return state.LastIndexedBlockNumber == 0 && state.LastIndexedBlockUpdated == 0
}

// runIteration executes a single indexer cycle: updates chain state, checks for
// history drop results, optionally triggers a new history drop, and fetches and
// persists any new blocks.
func (ix *Indexer[B, T, E]) runIteration(
	ctx context.Context,
	state *database.State,
	historyDropLock *sync.Mutex,
	historyDropResults chan *database.State,
	upToDateBackoff *backoff.ExponentialBackOff,
) (*database.State, error) {
	ix.log.Debug("starting indexer iteration")

	err := backoff.RetryNotify(
		func() error {
			newState, err := ix.updateChainState(ctx, state)
			if err != nil {
				return permanentIfSentinel(err)
			}

			ix.log.Debugf("updated chain state: %+v", newState)
			state = newState
			return nil
		},
		ix.newBackoff(ctx),
		func(err error, d time.Duration) {
			ix.log.Errorf("indexer update chain state error: %v. Will retry after %v", err, d)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("fatal error in indexer: %w", err)
	}

	if err := ix.pollHistoryDropResults(ctx, historyDropLock, historyDropResults, state); err != nil {
		return nil, fmt.Errorf("pollHistoryDropResults failed: %w", err)
	}

	ix.maybeRunHistoryDrop(ctx, historyDropLock, historyDropResults, state)

	err = backoff.RetryNotify(
		func() error {
			results, err := ix.getIterationResults(ctx, state)
			if err != nil {
				return permanentIfSentinel(err)
			}

			if results == nil {
				ix.log.Debug("no new blocks to index, indexer is up to date")
				nextInterval := upToDateBackoff.NextBackOff()

				select {
				case <-time.After(nextInterval):
				case <-ctx.Done():
					return backoff.Permanent(ctx.Err())
				}

				return nil
			}

			upToDateBackoff.Reset()

			err = ix.saveData(ctx, results)
			if err != nil {
				return err
			}

			ix.log.Infof("successfully processed up to block %d", results.state.LastIndexedBlockNumber)
			state = results.state

			return nil
		},
		ix.newBackoff(ctx),
		func(err error, d time.Duration) {
			ix.log.Errorf("indexer iteration error: %v. Will retry after %v", err, d)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("fatal error in indexer: %w", err)
	}

	return state, nil
}

// maybeRunHistoryDrop starts an asynchronous history drop if one is not already
// in progress and the configured frequency threshold has been reached.
func (ix *Indexer[B, T, E]) maybeRunHistoryDrop(
	ctx context.Context,
	historyDropLock *sync.Mutex,
	historyDropResults chan *database.State,
	state *database.State,
) {
	if !historyDropLock.TryLock() {
		// Another history drop is in progress
		return
	}

	if !ix.shouldRunHistoryDrop(state) {
		// Nothing to do so release the lock
		historyDropLock.Unlock()
		return
	}

	// Start the history drop in a separate goroutine.
	//
	// We pass a copy of the current state by value to avoid data races.
	//
	// Updates to the state will be applied when the results
	// are returned via the results channel.
	go func(state database.State) {
		var newState *database.State
		defer func() {
			select {
			case historyDropResults <- newState:
			case <-ctx.Done():
				// Context cancelled; unlock so the main loop can exit.
				// The main loop will not receive from the channel in this case
				// because it exits via the ctx.Done() check.
				historyDropLock.Unlock()
			}
		}()

		err := backoff.RetryNotify(
			func() (err error) {
				newState, err = ix.runHistoryDrop(ctx, &state)
				return err
			},
			ix.newBackoff(ctx),
			func(err error, d time.Duration) {
				ix.log.Errorf("indexer history drop error: %v. Will retry after %v", err, d)
			},
		)
		if err != nil {
			ix.log.Errorf("fatal error in indexer history drop: %v", err)
			return
		}
	}(*state)

	// The lock will stay held until the history drop results are
	// returned via the results channel.
}

// pollHistoryDropResults checks for completed history drop results without blocking
// and applies and persists the updated state if available.
func (ix *Indexer[B, T, E]) pollHistoryDropResults(
	ctx context.Context,
	historyDropLock *sync.Mutex,
	historyDropResults chan *database.State,
	state *database.State,
) error {
	// Check if history drop results are available each iteration but do
	// not block.
	select {
	case newState := <-historyDropResults:
		// Unlock the history drop lock after processing the results.
		defer historyDropLock.Unlock()

		if newState == nil {
			return errors.New("history drop failed")
		}

		ix.log.Debugf("history drop completed, new state: %+v", newState)
		state.LastHistoryDrop = newState.LastHistoryDrop
		ix.mergeFirstIndexed(state, newState)

		// Persist the authoritative boundary immediately with SaveState (a full
		// write): the next regular save runs only when new blocks arrive, and a
		// regular save can only ever raise the boundary from zero, so it cannot
		// move it to the value computed here — including a reset back to zero
		// when the drop emptied the database.
		if err := ix.db.SaveState(ctx, state); err != nil {
			return fmt.Errorf("failed to save state after history drop: %w", err)
		}

	// default case to avoid blocking if results not available
	default:
	}

	return nil
}

// mergeFirstIndexed reconciles the first-indexed boundary computed by a history
// drop with the in-memory state, which a concurrent iteration may have advanced
// past the drop's database read. The boundary only ever moves up — rows older
// than it may exist (e.g. after resuming past unindexed blocks) but are outside
// the advertised contiguous range — except for the zero reset when the drop
// emptied the database and the in-memory boundary does not survive the drop's
// deletion threshold.
func (ix *Indexer[B, T, E]) mergeFirstIndexed(state, dropState *database.State) {
	// The drop deleted blocks with timestamps below this boundary; the drop's
	// state copy preserves the chain timestamp the boundary was derived from.
	var boundary uint64
	if dropState.LastChainBlockTimestamp > ix.historyDropInterval {
		boundary = dropState.LastChainBlockTimestamp - ix.historyDropInterval
	}

	memSurvives := state.FirstIndexedBlockNumber > 0 && state.FirstIndexedBlockTimestamp >= boundary

	if memSurvives && dropState.FirstIndexedBlockNumber <= state.FirstIndexedBlockNumber {
		return
	}

	state.FirstIndexedBlockNumber = dropState.FirstIndexedBlockNumber
	state.FirstIndexedBlockTimestamp = dropState.FirstIndexedBlockTimestamp
}

// getIterationResults determines the next block range to index, fetches the block
// data concurrently, and returns the results with an updated state.
func (ix *Indexer[B, T, E]) getIterationResults(
	ctx context.Context, state *database.State,
) (*iterationResult[B, T, E], error) {
	blkRange := ix.getBlockRange(state)

	switch blkRange.len() {
	case 0:
		return nil, nil

	case 1:
		ix.log.Debugf("indexing block %d, latest block on chain %d", blkRange.start, state.LastChainBlockNumber)

	default:
		ix.log.Debugf("indexing from block %d to %d, latest block on chain %d", blkRange.start, blkRange.end-1, state.LastChainBlockNumber)
	}

	blockResults, err := ix.getBlockResults(ctx, blkRange)
	if err != nil {
		return nil, err
	}

	newState := updateState(blockResults, state)

	return &iterationResult[B, T, E]{
		blockResults: blockResults,
		state:        newState,
	}, nil
}

// blockRange represents an inclusive-start, exclusive-end range of block numbers.
type blockRange struct {
	start uint64
	end   uint64
}

// len returns the number of blocks in the range.
func (br blockRange) len() uint64 {
	// this should never happen, safety check
	if br.start > br.end {
		return 0
	}

	return br.end - br.start
}

// getBlockRange computes the next range of blocks to index based on the current state.
func (ix *Indexer[B, T, E]) getBlockRange(state *database.State) *blockRange {
	result := new(blockRange)
	result.start = ix.getStartBlock(state)
	result.end = ix.getEndBlock(state, result.start)

	return result
}

// getStartBlock returns the block number to begin indexing from in the current iteration.
func (ix *Indexer[B, T, E]) getStartBlock(state *database.State) uint64 {
	if indexedNothing(state) || state.LastIndexedBlockNumber < ix.computedStartBlock {
		return ix.computedStartBlock
	}

	return state.LastIndexedBlockNumber + 1
}

// getEndBlock returns the exclusive upper bound of the block range to index,
// capped by confirmations and the maximum block range.
func (ix *Indexer[B, T, E]) getEndBlock(state *database.State, start uint64) uint64 {
	if state.LastChainBlockNumber < ix.confirmations {
		return start
	}

	latestConfirmedNum := state.LastChainBlockNumber - ix.confirmations

	if latestConfirmedNum < start {
		return start
	}

	numBlocks := latestConfirmedNum + 1 - start
	if numBlocks > ix.maxBlockRange {
		return start + ix.maxBlockRange
	}

	return latestConfirmedNum + 1
}

// getBlockResults fetches block data for the given range concurrently, bounded
// by the configured max concurrency.
func (ix *Indexer[B, T, E]) getBlockResults(
	ctx context.Context, blkRange *blockRange,
) ([]BlockResult[B, T, E], error) {
	sem := semaphore.NewWeighted(int64(ix.maxConcurrency))
	eg, ctx := errgroup.WithContext(ctx)

	l := blkRange.len()

	results := make([]BlockResult[B, T, E], l)

	for i := blkRange.start; i < blkRange.end; i++ {
		eg.Go(func() error {
			if err := sem.Acquire(ctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			res, err := ix.blockchain.GetBlockResult(ctx, i)
			if err != nil {
				return err
			}

			if res == nil {
				return fmt.Errorf("%w: GetBlockResult returned no result for block %d", ErrInvalidData, i)
			}

			results[i-blkRange.start] = *res
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

// saveData extracts blocks, transactions, and events from the iteration results
// and persists them to the database.
func (ix *Indexer[B, T, E]) saveData(ctx context.Context, results *iterationResult[B, T, E]) error {
	blocks := make([]*B, len(results.blockResults))
	totalTxs := 0
	totalEvents := 0
	for i := range results.blockResults {
		totalTxs += len(results.blockResults[i].Transactions)
		totalEvents += len(results.blockResults[i].Events)
	}
	transactions := make([]*T, 0, totalTxs)
	events := make([]*E, 0, totalEvents)

	for i := range results.blockResults {
		blocks[i] = &results.blockResults[i].Block

		resTxs := results.blockResults[i].Transactions
		for j := range resTxs {
			transactions = append(transactions, &resTxs[j])
		}

		resEvents := results.blockResults[i].Events
		for j := range resEvents {
			events = append(events, &resEvents[j])
		}
	}

	ix.log.Debugf("fetched %d blocks with %d transactions from the chain", len(results.blockResults), len(transactions))

	err := ix.db.SaveAllEntities(ctx, blocks, transactions, events, results.state)
	if err != nil {
		return fmt.Errorf("failed to save entities to database: %w", err)
	}

	ix.log.Debug("data saved to the DB")

	return nil
}

// updateChainState fetches the latest block info from the chain and returns
// an updated state with the current chain head.
func (ix *Indexer[B, T, E]) updateChainState(ctx context.Context, state *database.State) (*database.State, error) {
	newState := *state
	newState.LastChainBlockUpdated = uint64(time.Now().Unix())

	blockInfo, err := ix.blockchain.GetLatestBlockInfo(ctx)
	if err != nil {
		return nil, err
	}

	if blockInfo == nil {
		return nil, fmt.Errorf("%w: GetLatestBlockInfo returned no result", ErrInvalidData)
	}

	newState.LastChainBlockNumber = blockInfo.BlockNumber
	newState.LastChainBlockTimestamp = blockInfo.Timestamp

	return &newState, nil
}

// newBackoff creates a new exponential backoff with the indexer's configured max
// elapsed time. Wrapped in ctx so retry sleeps do not outlive a shutdown signal.
func (ix *Indexer[B, T, E]) newBackoff(ctx context.Context) backoff.BackOff {
	return backoff.WithContext(
		backoff.NewExponentialBackOff(backoff.WithMaxElapsedTime(ix.backoffMaxElapsedTime)),
		ctx,
	)
}

// permanentIfSentinel marks the BlockchainClient error sentinels as permanent so
// a retry loop stops on them instead of burning the whole backoff window.
func permanentIfSentinel(err error) error {
	if errors.Is(err, ErrInvalidData) || errors.Is(err, ErrBlockNotFound) {
		return backoff.Permanent(err)
	}

	return err
}

// updateState returns a new State reflecting the last and first indexed blocks
// from the given results.
func updateState[B database.Block, T database.Transaction, E database.Event](
	results []BlockResult[B, T, E], state *database.State,
) *database.State {
	if len(results) == 0 {
		return state
	}

	newState := *state

	lastIndexedBlock := results[len(results)-1].Block
	newState.LastIndexedBlockNumber = lastIndexedBlock.GetBlockNumber()
	newState.LastIndexedBlockTimestamp = lastIndexedBlock.GetTimestamp()

	// Set the first indexed block on the first iteration and re-establish it
	// after a history drop has emptied the database.
	if state.LastIndexedBlockNumber == 0 || state.FirstIndexedBlockNumber == 0 {
		firstIndexedBlock := results[0].Block
		newState.FirstIndexedBlockNumber = firstIndexedBlock.GetBlockNumber()
		newState.FirstIndexedBlockTimestamp = firstIndexedBlock.GetTimestamp()
	}

	newState.LastIndexedBlockUpdated = uint64(time.Now().Unix())

	return &newState
}
