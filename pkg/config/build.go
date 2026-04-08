package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	projectVersionFile   = "PROJECT_VERSION"
	projectBuildDateFile = "PROJECT_BUILD_DATE"
	projectCommitFile    = "PROJECT_COMMIT_HASH"
)

type BuildConfig struct {
	GitTag    string
	GitHash   string
	BuildDate uint64
}

// ReadBuildVersion reads the project version, commit hash, and build date from their respective files.
func ReadBuildVersion() (*BuildConfig, error) {
	projectVersionBytes, err := os.ReadFile(projectVersionFile)
	if err != nil {
		return nil, err
	}

	projectCommitBytes, err := os.ReadFile(projectCommitFile)
	if err != nil {
		return nil, err
	}
	projectBuildDateBytes, err := os.ReadFile(projectBuildDateFile)
	if err != nil {
		return nil, err
	}
	buildDate, err := time.Parse(time.RFC3339, strings.TrimSpace(string(projectBuildDateBytes)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse build date: %w", err)
	}

	return &BuildConfig{
		GitTag:    strings.TrimSpace(string(projectVersionBytes)),
		GitHash:   strings.TrimSpace(string(projectCommitBytes)),
		BuildDate: uint64(buildDate.Unix()),
	}, nil
}
