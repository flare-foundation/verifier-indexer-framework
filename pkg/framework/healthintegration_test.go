//go:build integration

package framework

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

// freePort returns a port that was free a moment ago.
func freePort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

// healthEnabledConfig copies the integration config and enables the endpoint.
func healthEnabledConfig(t *testing.T, address string) string {
	t.Helper()

	configFile := os.Getenv("CONFIG_FILE")
	if configFile == "" {
		configFile = defaultConfigFile
	}

	body, err := os.ReadFile(configFile)
	require.NoError(t, err)

	// A short window keeps the test from waiting on the endpoint's cache.
	withHealth := string(body) + "\n[health]\nenabled = true\nlisten_address = \"" +
		address + "\"\ncache_millis = 10\n"

	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(withHealth), 0o600))

	return path
}

// TestRunServesHealthEndpoint exercises the endpoint over a real socket against
// a real database, which is the only way to cover *database.DB behind
// health.StateSource and the drain-before-Close ordering in runWithArgs.
func TestRunServesHealthEndpoint(t *testing.T) {
	if err := godotenv.Load(); err != nil {
		t.Log("No .env file found, proceeding without it")
	}

	address := freePort(t)
	args := CLIArgs{ConfigFile: healthEnabledConfig(t, address)}

	input := Input[dbBlock, *ExampleConfig, dbTransaction, struct{}]{
		NewBlockchainClient: NewTestBlockchain,
	}

	done := make(chan error, 1)
	go func() {
		done <- runWithArgs(input, args)
	}()

	// The endpoint answers while the indexer is still catching up to
	// end_block_number, so poll until the listener is up.
	var body string
	var status int
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://" + address + "/health") //nolint:noctx // a test request to a local listener
		if err != nil {
			return false
		}

		defer resp.Body.Close() //nolint:errcheck // best-effort cleanup in a test

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return false
		}

		body = string(raw)
		status = resp.StatusCode

		return true
	}, 20*time.Second, 100*time.Millisecond, "health endpoint never answered")

	// A bounded run is behind its own end block for its whole duration, so the
	// status is expected to be catching_up rather than ready.
	require.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, status)
	require.Contains(t, body, `"last_indexed_block_number"`)
	require.Contains(t, body, `"max_block_lag"`)
	require.Contains(t, body, `"max_chain_age_seconds"`)
	require.NotContains(t, body, "password", "the response must not leak connection details")

	require.NoError(t, <-done, "a bounded run must finish cleanly with the endpoint enabled")

	// The endpoint must not outlive the run: it shares the database handle that
	// runWithArgs closes on return.
	_, err := net.DialTimeout("tcp", address, time.Second)
	require.Error(t, err, "the health listener must be closed once the run returns")
}
