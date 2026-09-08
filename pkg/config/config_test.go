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

func TestDecodeReportsUnknownKeys(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		expectedKeys []string
	}{
		{
			name: "known keys decode",
			body: "[indexer]\nconfirmations = 3\n",
		},
		{
			name:         "mistyped key is reported",
			body:         "[indexer]\nconfirmation = 3\n",
			expectedKeys: []string{"indexer.confirmation"},
		},
		{
			name: "unknown table is reported",
			body: "[indexerr]\nconfirmations = 3\n",
			// BurntSushi reports the unknown table and its keys.
			expectedKeys: []string{"indexerr", "indexerr.confirmations"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))

			var cfg Base
			keys, err := Decode(path, &cfg)
			require.NoError(t, err)
			require.ElementsMatch(t, tc.expectedKeys, keys)

			// v1.1.1 callers keep decoding whatever the file declares.
			require.NoError(t, ReadFile(path, &Base{}))
		})
	}
}

func TestCheckHealth(t *testing.T) {
	validHealth := func() *Base {
		return &Base{
			Indexer: Indexer{Confirmations: 12, MaxBlockRange: 100, MaxConcurrency: 4},
			Timeout: TimeoutConfig{BackoffMaxElapsedTimeSeconds: 300, RequestTimeoutMillis: 3000},
			Health:  defaultHealth,
		}
	}

	// enabling by accident would open a port on every existing consumer
	require.False(t, DefaultBase.Health.Enabled)

	t.Run("disabled ignores its own values", func(t *testing.T) {
		cfg := validHealth()
		cfg.Health = Health{ListenAddress: "", CacheMillis: -1, MaxBlockLag: 1}
		require.NoError(t, CheckParameters(cfg))
	})

	t.Run("enabled with defaults passes", func(t *testing.T) {
		cfg := validHealth()
		cfg.Health.Enabled = true
		require.NoError(t, CheckParameters(cfg))
	})

	t.Run("enabled without a listen address", func(t *testing.T) {
		cfg := validHealth()
		cfg.Health.Enabled = true
		cfg.Health.ListenAddress = ""
		require.ErrorContains(t, CheckParameters(cfg), "listen_address")
	})

	t.Run("negative cache interval", func(t *testing.T) {
		cfg := validHealth()
		cfg.Health.Enabled = true
		cfg.Health.CacheMillis = -1
		require.ErrorContains(t, CheckParameters(cfg), "cache_millis")
	})

	t.Run("lag allowance below confirmations can never be met", func(t *testing.T) {
		cfg := validHealth()
		cfg.Health.Enabled = true
		cfg.Health.MaxBlockLag = 5
		require.ErrorContains(t, CheckParameters(cfg), "max_block_lag")
	})
}

func TestWriterLockDefaults(t *testing.T) {
	require.True(t, DefaultBase.DB.WriterLock, "the lock must be on unless a config turns it off")
	require.Equal(t, 60, DefaultBase.DB.WriterLockWaitSeconds)

	cfg := DefaultBase
	cfg.DB.WriterLockWaitSeconds = -1
	require.ErrorContains(t, CheckParameters(&cfg), "writer_lock_wait_seconds")

	cfg = DefaultBase
	cfg.DB.MaxOpenConns = 1
	require.ErrorContains(t, CheckParameters(&cfg), "max_open_conns", "the lock's connection would starve every query")

	cfg.DB.WriterLock = false
	if err := CheckParameters(&cfg); err != nil {
		require.NotContains(t, err.Error(), "max_open_conns")
	}

	// the default survives a file that does not mention the key
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("[db]\nhost = \"h\"\n"), 0644))

	decoded := DefaultBase
	require.NoError(t, ReadFile(path, &decoded))
	require.True(t, decoded.DB.WriterLock)
	require.Equal(t, 60, decoded.DB.WriterLockWaitSeconds)
}
