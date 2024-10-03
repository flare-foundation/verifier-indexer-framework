package framework

import (
	"context"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/config"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/database"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/indexer"
	"gorm.io/gorm"
)

const historyDropIntervalCheck = 60 * 30 // every 30 min

func GetMinBlockWithinHistoryInterval[B database.Block, T database.Transaction](
	ctx context.Context, firstBlockNumber, intervalSeconds uint64, client indexer.BlockchainClient[B, T],
) (uint64, error) {
	firstBlockTime, err := client.GetBlockTimestamp(ctx, firstBlockNumber)
	if err != nil {
		return 0, err
	}

	lastBlockNumber, lastBlockTime, err := client.GetLatestBlockInfo(ctx)
	if err != nil {
		return 0, err
	}

	if lastBlockTime-firstBlockTime < intervalSeconds {
		return firstBlockNumber, nil
	}

	var newBlockTime uint64
	for lastBlockNumber-firstBlockNumber > 1 {
		newBlockNumber := (firstBlockNumber + lastBlockNumber) / 2

		err = backoff.RetryNotify(
			func() error {
				newBlockTime, err = client.GetBlockTimestamp(ctx, newBlockNumber)
				if err != nil {
					return err
				}
				return nil
			},
			backoff.NewExponentialBackOff(),
			func(err error, d time.Duration) {
				log.Errorf("error getting block timestamp: %w. Will retry after %v", err, d)
			},
		)
		if err != nil {
			return 0, err
		}
		if lastBlockTime-newBlockTime <= intervalSeconds {
			lastBlockNumber = newBlockNumber
		} else {
			firstBlockNumber = newBlockNumber
		}
	}

	return lastBlockNumber, nil
}

func DropHistoryRunner[B database.Block, T database.Transaction](
	ctx context.Context, db *gorm.DB, intervalSeconds uint64, client indexer.BlockchainClient[B, T],
) {
	for {
		// start with sleep, since the first iteration is run separately
		time.Sleep(time.Duration(historyDropIntervalCheck) * time.Second)

		log.Info("starting DropHistory iteration")

		startTime := time.Now()
		_, lastBlockTimestamp, err := client.GetLatestBlockInfo(ctx)
		if err != nil {
			log.Errorf("DropHistory error: %s", err)
		}

		_, err = database.DropHistoryIteration[B, T](ctx, db, intervalSeconds, lastBlockTimestamp)
		if err == nil {
			duration := time.Since(startTime)
			log.Infof("finished DropHistory iteration in %v", duration)
		} else {
			log.Errorf("DropHistory error: %s", err)
		}
	}
}

func initialHistoryDrop[B database.Block, T database.Transaction](
	ctx context.Context, db *gorm.DB, bc indexer.BlockchainClient[B, T], cfg config.BaseConfig,
) (uint64, error) {
	newStartingBlockNumber := cfg.Indexer.StartBlockNumber
	// Run an initial iteration of the history drop. This could take some
	// time if it has not been run in a while after an outage - running
	// separately avoids database clashes with the indexer.
	log.Info("running initial DropHistory iteration")

	var firstBlockNumber uint64
	expBackOff := backoff.NewExponentialBackOff()
	expBackOff.MaxInterval = time.Duration(cfg.Timeout.BackoffMaxElapsedTimeSeconds) * time.Second
	err := backoff.RetryNotify(
		func() (err error) {
			_, lastBlockTimestamp, err := bc.GetLatestBlockInfo(ctx)
			if err != nil {
				return err
			}
			firstBlockNumber, err = database.DropHistoryIteration[B, T](ctx, db, cfg.DB.HistoryDrop, lastBlockTimestamp)

			return err
		},
		expBackOff,
		func(err error, d time.Duration) {
			log.Error("DropHistory error: %s. Will retry after %s", err, d)
		},
	)
	if err != nil {
		return 0, errors.Wrap(err, "startup DropHistory error")
	}

	log.Info("initial DropHistory iteration finished")

	// if nothing remains in the dataset, firstBlockNumber is 0
	if firstBlockNumber > cfg.Indexer.StartBlockNumber {
		log.Infof("new fist block in DB due to history drop: %d", firstBlockNumber)
		newStartingBlockNumber = firstBlockNumber
	}

	return newStartingBlockNumber, nil
}

func sanityCheck(indexerStartBlock uint64, states *database.DBStates) error {
	if states.States[database.LastDatabaseIndexState] != nil &&
		(states.States[database.LastDatabaseIndexState].Index+1 < indexerStartBlock ||
			indexerStartBlock < states.States[database.FirstDatabaseIndexState].Index) {
		return errors.New("unable to continue with the historic data in the DB, drop tables before start")
	}

	return nil
}
