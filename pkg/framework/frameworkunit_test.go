package framework

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/database"
	"github.com/flare-foundation/verifier-indexer-framework/pkg/indexer"
	"github.com/stretchr/testify/require"
)

type unitBlock struct {
	Hash        string `gorm:"primaryKey;type:varchar(64)"`
	BlockNumber uint64 `gorm:"index"`
	Timestamp   uint64 `gorm:"index"`
}

func (b unitBlock) GetBlockNumber() uint64                 { return b.BlockNumber }
func (b unitBlock) GetTimestamp() uint64                   { return b.Timestamp }
func (b unitBlock) HistoryDropOrder() []database.Deletable { return nil }

type unitTransaction struct {
	Hash string `gorm:"primaryKey;type:varchar(64)"`
}

// envConfig assigns a field in ApplyEnvOverrides, so calling it on a nil
// receiver panics — the regression this test suite guards against.
type envConfig struct {
	Value string
}

func (c *envConfig) ApplyEnvOverrides() error {
	if v, ok := os.LookupEnv("FRAMEWORK_UNIT_TEST_VALUE"); ok {
		c.Value = v
	}

	return nil
}

// The framework's own database carries both optional writers.
var (
	_ indexer.StateSaver    = (*database.DB[unitBlock, unitTransaction, struct{}])(nil)
	_ indexer.ChainTipSaver = (*database.DB[unitBlock, unitTransaction, struct{}])(nil)
)

// errStopAtClient ends a run inside the blockchain client constructor.
var errStopAtClient = errors.New("stop at client")

// writeUnitConfig writes a config without a [blockchain] table, so TOML
// decoding never allocates a pointer config, and with an unreachable database.
func writeUnitConfig(t *testing.T, extra string) string {
	t.Helper()

	cfgFile := filepath.Join(t.TempDir(), "config.toml")
	body := "[indexer]\nconfirmations = 1\n\n[db]\nport = 59999\nusername = \"unit\"\ndb_name = \"unit\"\n" + extra
	require.NoError(t, os.WriteFile(cfgFile, []byte(body), 0o600))

	return cfgFile
}

func TestRunWithArgsAllocatesNilPointerConfig(t *testing.T) {
	t.Setenv("FRAMEWORK_UNIT_TEST_VALUE", "from-env")

	var seen *envConfig
	input := Input[unitBlock, *envConfig, unitTransaction, struct{}]{
		NewBlockchainClient: func(c *envConfig) (indexer.BlockchainClient[unitBlock, unitTransaction, struct{}], error) {
			seen = c
			return nil, errStopAtClient
		},
	}

	require.NotPanics(t, func() {
		err := runWithArgs(input, CLIArgs{ConfigFile: writeUnitConfig(t, "")})
		require.ErrorIs(t, err, errStopAtClient)
	})

	require.NotNil(t, seen)
	require.Equal(t, "from-env", seen.Value)
}

// TestRunWithArgsBuildsClientBeforeDatabase pins the order: with the database
// first, drop_table_at_start would run before a bad blockchain config fails.
func TestRunWithArgsBuildsClientBeforeDatabase(t *testing.T) {
	input := Input[unitBlock, *envConfig, unitTransaction, struct{}]{
		NewBlockchainClient: func(*envConfig) (indexer.BlockchainClient[unitBlock, unitTransaction, struct{}], error) {
			return nil, errStopAtClient
		},
	}

	err := runWithArgs(input, CLIArgs{ConfigFile: writeUnitConfig(t, "drop_table_at_start = true\n")})
	require.ErrorIs(t, err, errStopAtClient, "the unreachable database must not be touched first")
}

func TestRunWithArgsRequiresConstructor(t *testing.T) {
	var input Input[unitBlock, *envConfig, unitTransaction, struct{}]

	err := runWithArgs(input, CLIArgs{ConfigFile: "does-not-exist.toml"})
	require.ErrorContains(t, err, "NewBlockchainClient")
}

func TestAllocateIfNil(t *testing.T) {
	t.Run("allocates nil pointer", func(t *testing.T) {
		var c *envConfig
		got := allocateIfNil(c)
		require.NotNil(t, got)
	})

	t.Run("keeps existing pointer", func(t *testing.T) {
		c := &envConfig{Value: "set"}
		require.Same(t, c, allocateIfNil(c))
	})

	t.Run("passes non-pointer through", func(t *testing.T) {
		ctx := context.Background()
		require.Equal(t, ctx, allocateIfNil(ctx))
	})
}
