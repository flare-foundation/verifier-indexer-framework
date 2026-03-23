package indexer

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/pkg/errors"
)

type blockchainWithBackoff[B database.Block, T database.Transaction, E database.Event] struct {
	client         BlockchainClient[B, T, E]
	maxElapsedTime time.Duration
	requestTimeout time.Duration
}

func newBlockchainWithBackoff[B database.Block, T database.Transaction, E database.Event](
	client BlockchainClient[B, T, E], maxElapsedTime, requestTimeout time.Duration,
) *blockchainWithBackoff[B, T, E] {
	return &blockchainWithBackoff[B, T, E]{
		client:         client,
		maxElapsedTime: maxElapsedTime,
		requestTimeout: requestTimeout,
	}
}

func retryWithBackoff[B database.Block, T database.Transaction, E database.Event, R any](
	ctx context.Context,
	bwb *blockchainWithBackoff[B, T, E],
	opName string,
	op func(context.Context) (R, error),
) (R, error) {
	var result R
	err := backoff.RetryNotify(
		func() (err error) {
			ctx, cancel := context.WithTimeout(ctx, bwb.requestTimeout)
			defer cancel()

			result, err = op(ctx)
			return err
		},
		bwb.newBackoff(ctx),
		func(err error, d time.Duration) {
			logger.Errorf("%s error: %v. Will retry after %v", opName, err, d)
		},
	)
	if err != nil {
		var zero R
		return zero, errors.Wrap(err, opName+" failed")
	}

	return result, nil
}

func (bwb *blockchainWithBackoff[B, T, E]) GetLatestBlockInfo(ctx context.Context) (*BlockInfo, error) {
	return retryWithBackoff[B, T, E](ctx, bwb, "GetLatestBlockInfo", bwb.client.GetLatestBlockInfo)
}

func (bwb *blockchainWithBackoff[B, T, E]) GetBlockResult(ctx context.Context, blockNumber uint64) (*BlockResult[B, T, E], error) {
	return retryWithBackoff[B, T, E](ctx, bwb, "GetBlockResult", func(ctx context.Context) (*BlockResult[B, T, E], error) {
		return bwb.client.GetBlockResult(ctx, blockNumber)
	})
}

func (bwb *blockchainWithBackoff[B, T, E]) GetBlockTimestamp(ctx context.Context, blockNumber uint64) (uint64, error) {
	return retryWithBackoff[B, T, E](ctx, bwb, "GetBlockTimestamp", func(ctx context.Context) (uint64, error) {
		return bwb.client.GetBlockTimestamp(ctx, blockNumber)
	})
}

func (bwb *blockchainWithBackoff[B, T, E]) GetServerInfo(ctx context.Context) (string, error) {
	return retryWithBackoff[B, T, E](ctx, bwb, "GetServerInfo", func(ctx context.Context) (string, error) {
		return bwb.client.GetServerInfo(ctx)
	})
}

func (bwb *blockchainWithBackoff[B, T, E]) newBackoff(ctx context.Context) backoff.BackOff {
	return backoff.WithContext(backoff.NewExponentialBackOff(
		backoff.WithMaxElapsedTime(bwb.maxElapsedTime),
	), ctx)
}
