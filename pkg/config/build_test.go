package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadBuildVersion(t *testing.T) {
	t.Run("reads all build files", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, os.WriteFile(filepath.Join(dir, projectVersionFile), []byte("v0.0.2"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, projectCommitFile), []byte("c7bd102cb88a984ca2adda96544acccd27bd2cb6"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, projectBuildDateFile), []byte("2024-12-16T09:09:23+01:00"), 0644))

		t.Chdir(dir)

		config, err := ReadBuildVersion()
		require.NoError(t, err)
		require.NotNil(t, config)
		require.Equal(t, "v0.0.2", config.GitTag)
		require.Equal(t, "c7bd102cb88a984ca2adda96544acccd27bd2cb6", config.GitHash)
		require.Equal(t, uint64(1734336563), config.BuildDate)
	})

	t.Run("trims whitespace from values", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, os.WriteFile(filepath.Join(dir, projectVersionFile), []byte("  v1.0.0\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, projectCommitFile), []byte(" abc123\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, projectBuildDateFile), []byte(" 2024-01-01T00:00:00Z \n"), 0644))

		t.Chdir(dir)

		config, err := ReadBuildVersion()
		require.NoError(t, err)
		require.Equal(t, "v1.0.0", config.GitTag)
		require.Equal(t, "abc123", config.GitHash)
	})

	t.Run("missing version file", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		_, err := ReadBuildVersion()
		require.Error(t, err)
	})

	t.Run("missing commit file", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, projectVersionFile), []byte("v1.0.0"), 0644))

		t.Chdir(dir)

		_, err := ReadBuildVersion()
		require.Error(t, err)
	})

	t.Run("invalid date format", func(t *testing.T) {
		dir := t.TempDir()

		require.NoError(t, os.WriteFile(filepath.Join(dir, projectVersionFile), []byte("v1.0.0"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, projectCommitFile), []byte("abc123"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, projectBuildDateFile), []byte("not-a-date"), 0644))

		t.Chdir(dir)

		_, err := ReadBuildVersion()
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to parse build date")
	})
}
