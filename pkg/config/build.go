package config

import (
	"os"
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
	buildDate, err := time.Parse(time.RFC3339, string(projectBuildDateBytes))
	if err != nil {
		return nil, err
	}

	return &BuildConfig{
		GitTag:    string(projectVersionBytes),
		GitHash:   string(projectCommitBytes),
		BuildDate: uint64(buildDate.Unix()),
	}, nil
}
