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

func TestRunWithArgsAllocatesNilPointerConfig(t *testing.T) {
	t.Setenv("FRAMEWORK_UNIT_TEST_VALUE", "from-env")

	// Config file without a [blockchain] table, so TOML decoding does not
	// allocate the pointer either. The unreachable db port stops the run right
	// after the point this test guards (the env override on the nil config).
	cfgFile := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("[indexer]\nconfirmations = 1\n\n[db]\nport = 59999\n"), 0o600))

	input := Input[unitBlock, *envConfig, unitTransaction, struct{}]{
		NewBlockchainClient: func(c *envConfig) (indexer.BlockchainClient[unitBlock, unitTransaction, struct{}], error) {
			return nil, errors.New("not reached in this test")
		},
	}

	require.NotPanics(t, func() {
		err := runWithArgs(input, CLIArgs{ConfigFile: cfgFile})
		require.Error(t, err, "run must stop at the unreachable database")
	})
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
