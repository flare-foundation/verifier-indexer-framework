package framework

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/stretchr/testify/require"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/config"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/database"
	"gitlab.com/flarenetwork/fdc/verifier-indexer-framework/pkg/indexer"
)

func TestRun(t *testing.T) {
	input := Input[dbBlock, ExampleConfig, dbTransaction]{
		NewBlockchainClient: NewTestBlockchain,
	}

	if err := Run(input); err != nil {
		logger.Fatal(err)
	}

	configFile := os.Getenv("CONFIG_FILE")
	cfg := config.BaseConfig{}
	if err := config.ReadFile(configFile, &cfg); err != nil {
		logger.Fatal(err)
	}

	db, err := database.Connect(&config.DB{Host: cfg.DB.Host, Port: cfg.DB.Port, Username: cfg.DB.Username, Password: cfg.DB.Password, DBName: cfg.DB.DBName})
	if err != nil {
		logger.Fatal(err)
	}
	state := new(database.State)
	err = db.First(state, 1).Error
	if err != nil {
		logger.Fatal(err)
	}

	require.Equal(t, uint64(300), state.FirstIndexedBlockNumber)
	require.GreaterOrEqual(t, state.LastIndexedBlockNumber, uint64(510))
	require.GreaterOrEqual(t, uint64(512), state.LastIndexedBlockNumber)
}

type TestBlockchain struct {
	startTime time.Time
}

func NewTestBlockchain(cfg *ExampleConfig) (indexer.BlockchainClient[dbBlock, dbTransaction], error) {
	return TestBlockchain{startTime: time.Now()}, nil
}

func (e TestBlockchain) GetLatestBlockInfo(context.Context) (*indexer.BlockInfo, error) {
	timeSince := uint64(time.Since(e.startTime).Seconds())

	return &indexer.BlockInfo{BlockNumber: timeSince + 500, Timestamp: timeSince + 1000}, nil
}

func (e TestBlockchain) GetBlockResult(ctx context.Context, blockNum uint64) (*indexer.BlockResult[dbBlock, dbTransaction], error) {
	hash := strconv.Itoa(int(blockNum))
	hash = strings.Repeat("0", 64-len(hash)) + hash
	block := dbBlock{
		BlockNumber: blockNum,
		Timestamp:   blockNum + 500,
		Hash:        hash,
	}

	transactions := []dbTransaction{{Hash: strings.Repeat("f", 64), Timestamp: blockNum + 500, BlockNumber: blockNum}, {Hash: strings.Repeat("e", 64), Timestamp: blockNum + 500, BlockNumber: blockNum}}

	return &indexer.BlockResult[dbBlock, dbTransaction]{Block: block, Transactions: transactions}, nil
}

func (e TestBlockchain) GetBlockTimestamp(ctx context.Context, blockNum uint64) (uint64, error) {
	return blockNum + 500, nil
}

type ExampleConfig struct{}

type dbBlock struct {
	Hash        string `gorm:"primaryKey;type:varchar(64)"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

func (e dbBlock) GetBlockNumber() uint64 {
	return e.BlockNumber
}

func (e dbBlock) GetTimestamp() uint64 {
	return e.Timestamp
}

type dbTransaction struct {
	Hash        string `gorm:"primaryKey;type:varchar(64)"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}
