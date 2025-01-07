package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuild(t *testing.T) {
	f, err := os.Create(projectVersionFile)
	require.NoError(t, err)
	_, err = f.WriteString("v0.0.2")
	require.NoError(t, err)

	f, err = os.Create(projectCommitFile)
	require.NoError(t, err)
	_, err = f.WriteString("c7bd102cb88a984ca2adda96544acccd27bd2cb6")
	require.NoError(t, err)

	f, err = os.Create(projectBuildDateFile)
	require.NoError(t, err)
	_, err = f.WriteString("2024-12-16T09:09:23+01:00")
	require.NoError(t, err)

	config, err := ReadBuildVersion()
	require.NoError(t, err)
	require.NotNil(t, config)

	err = os.Remove(projectVersionFile)
	require.NoError(t, err)
	err = os.Remove(projectCommitFile)
	require.NoError(t, err)
	err = os.Remove(projectBuildDateFile)
	require.NoError(t, err)
}
