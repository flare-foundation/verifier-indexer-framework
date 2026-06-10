//go:build integration

package framework

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/indexer"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const defaultConfigFile = "../../tests/test_config.toml"

func TestRun(t *testing.T) {
	err := godotenv.Load()
	if err != nil {
		t.Log("No .env file found, proceeding without it")
	}

	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		configFile = defaultConfigFile
	}

	input := Input[dbBlock, *ExampleConfig, dbTransaction, struct{}]{
		NewBlockchainClient: NewTestBlockchain,
	}

	args := CLIArgs{ConfigFile: configFile}
	err = runWithArgs(input, args)
	require.NoError(t, err)

	cfg := config.Base{}
	err = config.ReadFile(configFile, &cfg)
	require.NoError(t, err)

	err = cfg.ApplyEnvOverrides()
	require.NoError(t, err)

	t.Log("Applied env overrides to config: ", cfg)

	db, err := database.Connect(&cfg.DB)
	require.NoError(t, err)

	state := new(database.State)
	err = db.First(state, 1).Error
	require.NoError(t, err)

	assert.GreaterOrEqual(t, state.FirstIndexedBlockNumber, uint64(300))
	assert.GreaterOrEqual(t, uint64(315), state.FirstIndexedBlockNumber)
	assert.GreaterOrEqual(t, state.LastIndexedBlockNumber, uint64(509))
	assert.GreaterOrEqual(t, uint64(512), state.LastIndexedBlockNumber)
}

type TestBlockchain struct {
	startTime time.Time
}

func NewTestBlockchain(cfg *ExampleConfig) (indexer.BlockchainClient[dbBlock, dbTransaction, struct{}], error) {
	return TestBlockchain{startTime: time.Now()}, nil
}

func (e TestBlockchain) GetLatestBlockInfo(context.Context) (*indexer.BlockInfo, error) {
	timeSince := uint64(time.Since(e.startTime).Seconds())

	return &indexer.BlockInfo{BlockNumber: timeSince + 500, Timestamp: timeSince + 1000}, nil
}

func (e TestBlockchain) GetBlockResult(ctx context.Context, blockNum uint64) (*indexer.BlockResult[dbBlock, dbTransaction, struct{}], error) {
	block := dbBlock{
		BlockNumber: blockNum,
		Timestamp:   blockNum + 500,
		Hash:        testHash("0", blockNum),
	}

	// Transaction hashes are unique per block, as on a real chain.
	transactions := []dbTransaction{
		{Hash: testHash("e", blockNum), Timestamp: blockNum + 500, BlockNumber: blockNum},
		{Hash: testHash("f", blockNum), Timestamp: blockNum + 500, BlockNumber: blockNum},
	}

	return &indexer.BlockResult[dbBlock, dbTransaction, struct{}]{Block: block, Transactions: transactions}, nil
}

// testHash builds a 64-character hash from a one-character prefix and a number.
func testHash(prefix string, n uint64) string {
	num := strconv.FormatUint(n, 10)
	return prefix + strings.Repeat("0", 63-len(num)) + num
}

func (e TestBlockchain) GetBlockTimestamp(ctx context.Context, blockNum uint64) (uint64, error) {
	return blockNum + 500, nil
}

func (e TestBlockchain) GetServerInfo(ctx context.Context) (string, error) {
	return "0.0.1_test", nil
}

type ExampleConfig struct{}

// Required for interface but not used in this example.
func (e *ExampleConfig) ApplyEnvOverrides() error { return nil }

type dbBlock struct {
	Hash        string `gorm:"primaryKey;type:varchar(64)"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

func (b dbBlock) GetBlockNumber() uint64 {
	return b.BlockNumber
}

func (b dbBlock) GetTimestamp() uint64 {
	return b.Timestamp
}

func (b dbBlock) HistoryDropOrder() []database.Deletable {
	var emptyBlock dbBlock
	var emptyTransaction dbTransaction
	return []database.Deletable{emptyTransaction, emptyBlock}
}

// Required for Deletable interface.
func (b dbBlock) TimestampField() string {
	return "timestamp"
}

type dbTransaction struct {
	Hash        string `gorm:"primaryKey;type:varchar(64)"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

// Required for Deletable interface.
func (t dbTransaction) TimestampField() string {
	return "timestamp"
}
