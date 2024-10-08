package indexer

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/database"
)

type blockchainWithBackoff[B database.Block, T database.Transaction] struct {
	client  BlockchainClient[B, T]
	backoff backoff.BackOff
}

func newBlockchainWithBackoff[B database.Block, T database.Transaction](
	client BlockchainClient[B, T], backoff backoff.BackOff,
) *blockchainWithBackoff[B, T] {
	return &blockchainWithBackoff[B, T]{
		client:  client,
		backoff: backoff,
	}
}

func (bwb *blockchainWithBackoff[B, T]) GetLatestBlockInfo(ctx context.Context) (*BlockInfo, error) {
	var blockInfo *BlockInfo

	err := backoff.RetryNotify(
		func() (err error) {
			blockInfo, err = bwb.client.GetLatestBlockInfo(ctx)
			return err
		},
		bwb.backoff,
		func(err error, d time.Duration) {
			log.Errorf("GetLatestBlockInfo error: %v. Will retry after %v", err, d)
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "GetLatestBlockInfo failed")
	}

	return blockInfo, nil
}

func (bwb *blockchainWithBackoff[B, T]) GetBlockResult(ctx context.Context, blockNumber uint64) (*BlockResult[B, T], error) {
	var blockResult *BlockResult[B, T]

	err := backoff.RetryNotify(
		func() (err error) {
			blockResult, err = bwb.client.GetBlockResult(ctx, blockNumber)
			return err
		},
		bwb.backoff,
		func(err error, d time.Duration) {
			log.Errorf("GetBlockResult error: %v. Will retry after %v", err, d)
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "GetBlockResult failed")
	}

	return blockResult, nil
}

func (bwb *blockchainWithBackoff[B, T]) GetBlockTimestamp(ctx context.Context, blockNumber uint64) (uint64, error) {
	var timestamp uint64

	err := backoff.RetryNotify(
		func() (err error) {
			timestamp, err = bwb.client.GetBlockTimestamp(ctx, blockNumber)
			return err
		},
		bwb.backoff,
		func(err error, d time.Duration) {
			log.Errorf("GetBlockTimestamp error: %v. Will retry after %v", err, d)
		},
	)
	if err != nil {
		return 0, errors.Wrap(err, "GetBlockTimestamp failed")
	}

	return timestamp, nil
}
