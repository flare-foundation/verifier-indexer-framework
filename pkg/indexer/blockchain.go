package indexer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
)

// blockchainWithBackoff wraps a BlockchainClient and retries each call with
// exponential backoff on transient failures.
type blockchainWithBackoff[B database.Block, T database.Transaction, E database.Event] struct {
	client         BlockchainClient[B, T, E]
	maxElapsedTime time.Duration
	requestTimeout time.Duration
	log            logger.Logger
}

// newBlockchainWithBackoff returns a blockchainWithBackoff that wraps the given
// client with the specified backoff and request timeout durations.
func newBlockchainWithBackoff[B database.Block, T database.Transaction, E database.Event](
	client BlockchainClient[B, T, E], maxElapsedTime, requestTimeout time.Duration, log logger.Logger,
) *blockchainWithBackoff[B, T, E] {
	return &blockchainWithBackoff[B, T, E]{
		client:         client,
		maxElapsedTime: maxElapsedTime,
		requestTimeout: requestTimeout,
		log:            log,
	}
}

// isSentinel reports whether err wraps a BlockchainClient sentinel.
func isSentinel(err error) bool {
	return errors.Is(err, ErrBlockNotFound) || errors.Is(err, ErrInvalidData)
}

// isInvalidData reports whether err wraps ErrInvalidData.
func isInvalidData(err error) bool {
	return errors.Is(err, ErrInvalidData)
}

// retryWithBackoff executes the given operation with exponential backoff, applying
// a per-request timeout on each attempt. Errors the permanent predicate accepts
// are returned without retrying.
func retryWithBackoff[B database.Block, T database.Transaction, E database.Event, R any](
	ctx context.Context,
	bwb *blockchainWithBackoff[B, T, E],
	opName string,
	permanent func(error) bool,
	op func(context.Context) (R, error),
) (R, error) {
	var result R
	err := backoff.RetryNotify(
		func() (err error) {
			ctx, cancel := context.WithTimeout(ctx, bwb.requestTimeout)
			defer cancel()

			result, err = op(ctx)
			if err != nil && permanent(err) {
				return backoff.Permanent(err)
			}

			return err
		},
		bwb.newBackoff(ctx),
		func(err error, d time.Duration) {
			bwb.log.Errorf("%s error: %v. Will retry after %v", opName, err, d)
		},
	)
	if err != nil {
		var zero R
		return zero, fmt.Errorf("%s failed: %w", opName, err)
	}

	return result, nil
}

// GetLatestBlockInfo retrieves the latest block info from the blockchain with retry.
func (bwb *blockchainWithBackoff[B, T, E]) GetLatestBlockInfo(ctx context.Context) (*BlockInfo, error) {
	return retryWithBackoff[B, T, E](ctx, bwb, "GetLatestBlockInfo", isSentinel, bwb.client.GetLatestBlockInfo)
}

// GetBlockResult retrieves the block result for the given block number with retry.
// Not-found is retried: the block is below a tip the node reported, so the
// answer usually comes from a lagging backend of a load-balanced endpoint.
func (bwb *blockchainWithBackoff[B, T, E]) GetBlockResult(ctx context.Context, blockNumber uint64) (*BlockResult[B, T, E], error) {
	return retryWithBackoff[B, T, E](ctx, bwb, "GetBlockResult", isInvalidData, func(ctx context.Context) (*BlockResult[B, T, E], error) {
		return bwb.client.GetBlockResult(ctx, blockNumber)
	})
}

// GetBlockTimestamp retrieves the timestamp for the given block number with retry.
// Not-found is permanent: the start-block search probes pruned blocks and must
// not spend the backoff window on each.
func (bwb *blockchainWithBackoff[B, T, E]) GetBlockTimestamp(ctx context.Context, blockNumber uint64) (uint64, error) {
	return retryWithBackoff[B, T, E](ctx, bwb, "GetBlockTimestamp", isSentinel, func(ctx context.Context) (uint64, error) {
		return bwb.client.GetBlockTimestamp(ctx, blockNumber)
	})
}

// GetServerInfo retrieves the server version string from the blockchain node with retry.
func (bwb *blockchainWithBackoff[B, T, E]) GetServerInfo(ctx context.Context) (string, error) {
	return retryWithBackoff[B, T, E](ctx, bwb, "GetServerInfo", isSentinel, bwb.client.GetServerInfo)
}

// newBackoff creates a new context-aware exponential backoff with the configured max elapsed time.
func (bwb *blockchainWithBackoff[B, T, E]) newBackoff(ctx context.Context) backoff.BackOff {
	return backoff.WithContext(backoff.NewExponentialBackOff(
		backoff.WithMaxElapsedTime(bwb.maxElapsedTime),
	), ctx)
}
