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

func (bwb *blockchainWithBackoff[B, T, E]) GetLatestBlockInfo(ctx context.Context) (*BlockInfo, error) {
	var blockInfo *BlockInfo
	err := backoff.RetryNotify(
		func() (err error) {
			ctx, cancel := context.WithTimeout(ctx, bwb.requestTimeout)
			defer cancel()

			blockInfo, err = bwb.client.GetLatestBlockInfo(ctx)
			return err
		},
		bwb.newBackoff(ctx),
		func(err error, d time.Duration) {
			logger.Errorf("GetLatestBlockInfo error: %v. Will retry after %v", err, d)
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "GetLatestBlockInfo failed")
	}

	return blockInfo, nil
}

func (bwb *blockchainWithBackoff[B, T, E]) GetBlockResult(ctx context.Context, blockNumber uint64) (*BlockResult[B, T, E], error) {
	var blockResult *BlockResult[B, T, E]

	err := backoff.RetryNotify(
		func() (err error) {
			ctx, cancel := context.WithTimeout(ctx, bwb.requestTimeout)
			defer cancel()

			blockResult, err = bwb.client.GetBlockResult(ctx, blockNumber)
			return err
		},
		bwb.newBackoff(ctx),
		func(err error, d time.Duration) {
			logger.Errorf("GetBlockResult error: %v. Will retry after %v", err, d)
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "GetBlockResult failed")
	}

	return blockResult, nil
}

func (bwb *blockchainWithBackoff[B, T, E]) GetBlockTimestamp(ctx context.Context, blockNumber uint64) (uint64, error) {
	var timestamp uint64

	err := backoff.RetryNotify(
		func() (err error) {
			ctx, cancel := context.WithTimeout(ctx, bwb.requestTimeout)
			defer cancel()

			timestamp, err = bwb.client.GetBlockTimestamp(ctx, blockNumber)
			return err
		},
		bwb.newBackoff(ctx),
		func(err error, d time.Duration) {
			logger.Errorf("GetBlockTimestamp error: %v. Will retry after %v", err, d)
		},
	)
	if err != nil {
		return 0, errors.Wrap(err, "GetBlockTimestamp failed")
	}

	return timestamp, nil
}

func (bwb *blockchainWithBackoff[B, T, E]) GetServerInfo(ctx context.Context) (string, error) {
	var serverInfo string
	err := backoff.RetryNotify(
		func() (err error) {
			ctx, cancel := context.WithTimeout(ctx, bwb.requestTimeout)
			defer cancel()

			serverInfo, err = bwb.client.GetServerInfo(ctx)
			return err
		},
		bwb.newBackoff(ctx),
		func(err error, d time.Duration) {
			logger.Errorf("GetServerInfo error: %v. Will retry after %v", err, d)
		},
	)
	if err != nil {
		return "", errors.Wrap(err, "GetServerInfo failed")
	}

	return serverInfo, nil
}

func (bwb *blockchainWithBackoff[B, T, E]) newBackoff(ctx context.Context) backoff.BackOff {
	return backoff.WithContext(backoff.NewExponentialBackOff(
		backoff.WithMaxElapsedTime(bwb.maxElapsedTime),
	), ctx)
}
