package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuild(t *testing.T) {
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
}
