package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckParameters(t *testing.T) {
	validBase := func() *Base {
		return &Base{
			Indexer: Indexer{
				Confirmations:  1,
				MaxBlockRange:  100,
				MaxConcurrency: 4,
			},
			Timeout: TimeoutConfig{
				BackoffMaxElapsedTimeSeconds: 300,
				RequestTimeoutMillis:         3000,
			},
		}
	}

	t.Run("valid config", func(t *testing.T) {
		require.NoError(t, CheckParameters(validBase()))
	})

	t.Run("zero confirmations", func(t *testing.T) {
		cfg := validBase()
		cfg.Indexer.Confirmations = 0
		require.Error(t, CheckParameters(cfg))
	})

	t.Run("zero max concurrency", func(t *testing.T) {
		cfg := validBase()
		cfg.Indexer.MaxConcurrency = 0
		require.Error(t, CheckParameters(cfg))
	})

	t.Run("negative max concurrency", func(t *testing.T) {
		cfg := validBase()
		cfg.Indexer.MaxConcurrency = -1
		require.Error(t, CheckParameters(cfg))
	})

	t.Run("zero max block range", func(t *testing.T) {
		cfg := validBase()
		cfg.Indexer.MaxBlockRange = 0
		require.Error(t, CheckParameters(cfg))
	})

	t.Run("zero request timeout", func(t *testing.T) {
		cfg := validBase()
		cfg.Timeout.RequestTimeoutMillis = 0
		require.Error(t, CheckParameters(cfg))
	})

	t.Run("zero backoff max elapsed time", func(t *testing.T) {
		cfg := validBase()
		cfg.Timeout.BackoffMaxElapsedTimeSeconds = 0
		require.Error(t, CheckParameters(cfg))
	})

	t.Run("end block before start block", func(t *testing.T) {
		cfg := validBase()
		cfg.Indexer.StartBlockNumber = 100
		cfg.Indexer.EndBlockNumber = 50
		require.Error(t, CheckParameters(cfg))
	})

	t.Run("end block equal to start block", func(t *testing.T) {
		cfg := validBase()
		cfg.Indexer.StartBlockNumber = 100
		cfg.Indexer.EndBlockNumber = 100
		require.NoError(t, CheckParameters(cfg))
	})

	t.Run("end block zero means run forever", func(t *testing.T) {
		cfg := validBase()
		cfg.Indexer.StartBlockNumber = 100
		cfg.Indexer.EndBlockNumber = 0
		require.NoError(t, CheckParameters(cfg))
	})
}

func TestReadFile(t *testing.T) {
	t.Run("valid toml file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")

		content := `
[indexer]
confirmations = 12
max_block_range = 500
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))

		var cfg Base
		require.NoError(t, ReadFile(path, &cfg))
		require.Equal(t, uint64(12), cfg.Indexer.Confirmations)
		require.Equal(t, uint64(500), cfg.Indexer.MaxBlockRange)
	})

	t.Run("missing file", func(t *testing.T) {
		var cfg Base
		require.Error(t, ReadFile("/nonexistent/config.toml", &cfg))
	})
}

func TestApplyEnvOverrides(t *testing.T) {
	t.Run("overrides db host from env", func(t *testing.T) {
		t.Setenv("DB_HOST", "custom-host")

		cfg := DefaultBase
		require.NoError(t, cfg.ApplyEnvOverrides())
		require.Equal(t, "custom-host", cfg.DB.Host)
	})

	t.Run("overrides db port from env", func(t *testing.T) {
		t.Setenv("DB_PORT", "9999")

		cfg := DefaultBase
		require.NoError(t, cfg.ApplyEnvOverrides())
		require.Equal(t, 9999, cfg.DB.Port)
	})

	t.Run("does not override when env is empty", func(t *testing.T) {
		cfg := DefaultBase
		require.NoError(t, cfg.ApplyEnvOverrides())
		require.Equal(t, "localhost", cfg.DB.Host)
		require.Equal(t, 5432, cfg.DB.Port)
	})
}
