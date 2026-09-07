//go:build integration

package framework

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/indexer"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/logger"
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

// resumeConfig copies the integration config, keeps the tables and starts at
// startBlock, which the caller places above the retention window's first block.
func resumeConfig(t *testing.T, startBlock uint64) string {
	t.Helper()

	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		configFile = defaultConfigFile
	}

	body, err := os.ReadFile(configFile)
	require.NoError(t, err)

	text := strings.Replace(string(body), "drop_table_at_start = true", "drop_table_at_start = false", 1)
	text = strings.Replace(text, "start_block_number = 0", "start_block_number = "+strconv.FormatUint(startBlock, 10), 1)
	require.Contains(t, text, "drop_table_at_start = false")
	require.Contains(t, text, "start_block_number = "+strconv.FormatUint(startBlock, 10))

	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))

	return path
}

// TestRunPurgesRowsBelowResumeStart restarts over a database a prior run left
// behind, with start_block_number raised above its last indexed block. The
// stale rows sit inside the retention window, so no scheduled drop removes
// them; only the resume path can. A consumer gates coverage on the block row,
// so a surviving stale row would vouch for the never-indexed gap.
func TestRunPurgesRowsBelowResumeStart(t *testing.T) {
	if err := godotenv.Load(); err != nil {
		t.Log("No .env file found, proceeding without it")
	}

	// TestBlockchain: block n has timestamp n+500, the tip starts at 500 and
	// moves one block per second, retention is 200 s. The window opens near
	// block 300, so 400 lies above it, and rows 360-390 stay inside the window
	// for the whole run.
	const resumeStart, staleFirst, staleLast = uint64(400), uint64(360), uint64(390)

	configFile := resumeConfig(t, resumeStart)

	cfg := config.Base{}
	require.NoError(t, config.ReadFile(configFile, &cfg))
	require.NoError(t, cfg.ApplyEnvOverrides())

	seedCfg := cfg.DB
	seedCfg.DropTableAtStart = true
	seedDB, err := database.New(&seedCfg, database.ExternalEntities[dbBlock, dbTransaction, struct{}]{
		Block:       new(dbBlock),
		Transaction: new(dbTransaction),
		Event:       new(struct{}),
	}, logger.Nop{})
	require.NoError(t, err)

	blocks := make([]*dbBlock, 0, staleLast-staleFirst+1)
	transactions := make([]*dbTransaction, 0, staleLast-staleFirst+1)
	for n := staleFirst; n <= staleLast; n++ {
		blocks = append(blocks, &dbBlock{Hash: testHash("0", n), BlockNumber: n, Timestamp: n + 500})
		transactions = append(transactions, &dbTransaction{Hash: testHash("e", n), BlockNumber: n, Timestamp: n + 500})
	}

	now := uint64(time.Now().Unix())
	priorRun := &database.State{
		ID:                         1,
		LastChainBlockNumber:       staleLast + 1,
		LastChainBlockTimestamp:    staleLast + 501,
		LastChainBlockUpdated:      now,
		LastIndexedBlockNumber:     staleLast,
		LastIndexedBlockTimestamp:  staleLast + 500,
		LastIndexedBlockUpdated:    now,
		FirstIndexedBlockNumber:    staleFirst,
		FirstIndexedBlockTimestamp: staleFirst + 500,
	}
	require.NoError(t, seedDB.SaveAllEntities(context.Background(), blocks, transactions, nil, priorRun))
	require.NoError(t, seedDB.Close())

	input := Input[dbBlock, *ExampleConfig, dbTransaction, struct{}]{
		NewBlockchainClient: NewTestBlockchain,
	}
	require.NoError(t, runWithArgs(input, CLIArgs{ConfigFile: configFile}))

	conn, err := database.Connect(&cfg.DB)
	require.NoError(t, err)

	state := new(database.State)
	require.NoError(t, conn.First(state, 1).Error)
	assert.Equal(t, resumeStart, state.FirstIndexedBlockNumber)
	assert.GreaterOrEqual(t, state.LastIndexedBlockNumber, uint64(509))

	var staleBlocks, staleTransactions int64
	require.NoError(t, conn.Model(&dbBlock{}).Where("block_number < ?", resumeStart).Count(&staleBlocks).Error)
	require.NoError(t, conn.Model(&dbTransaction{}).Where("block_number < ?", resumeStart).Count(&staleTransactions).Error)
	assert.Zero(t, staleBlocks, "block rows below the boundary remain and vouch for the gap")
	assert.Zero(t, staleTransactions, "transaction rows below the boundary remain")

	var indexed int64
	require.NoError(t, conn.Model(&dbBlock{}).Where("block_number >= ?", resumeStart).Count(&indexed).Error)
	assert.GreaterOrEqual(t, indexed, int64(110), "the range from the new start must be indexed")
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
	return []database.Deletable{emptyBlock, emptyTransaction}
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
