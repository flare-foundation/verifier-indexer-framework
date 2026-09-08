package database

import (
	"testing"

	"github.com/flare-foundation/verifier-indexer-framework/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestFormatDSN(t *testing.T) {
	t.Run("basic credentials", func(t *testing.T) {
		cfg := &config.DB{
			Host:     "localhost",
			Port:     5432,
			Username: "user",
			Password: "pass",
			DBName:   "mydb",
		}

		dsn := formatDSN(cfg)
		require.Equal(t, "postgres://user:pass@localhost:5432/mydb?default_query_exec_mode=cache_describe", dsn)
	})

	t.Run("operator parameters are appended and win over the default", func(t *testing.T) {
		cfg := &config.DB{
			Host:             "localhost",
			Port:             5432,
			Username:         "user",
			Password:         "pass",
			DBName:           "mydb",
			ConnectionParams: "sslmode=verify-full&application_name=xrp indexer&default_query_exec_mode=simple_protocol",
		}

		dsn := formatDSN(cfg)
		require.Equal(t, "postgres://user:pass@localhost:5432/mydb"+
			"?application_name=xrp+indexer&default_query_exec_mode=simple_protocol&sslmode=verify-full", dsn)
	})

	t.Run("special characters in password", func(t *testing.T) {
		cfg := &config.DB{
			Host:     "localhost",
			Port:     5432,
			Username: "user",
			Password: "p@ss:w/rd",
			DBName:   "mydb",
		}

		dsn := formatDSN(cfg)
		require.Contains(t, dsn, "p%40ss%3Aw%2Frd")
	})

	t.Run("custom port", func(t *testing.T) {
		cfg := &config.DB{
			Host:     "db.example.com",
			Port:     9999,
			Username: "admin",
			Password: "secret",
			DBName:   "prod",
		}

		dsn := formatDSN(cfg)
		require.Contains(t, dsn, "db.example.com:9999")
		require.Contains(t, dsn, "/prod")
	})
}

func TestGetGormLogLevel(t *testing.T) {
	t.Run("silent when log queries disabled", func(t *testing.T) {
		cfg := &config.DB{LogQueries: false}
		// gormlogger.Silent == 1
		require.Equal(t, getGormLogLevel(cfg), getGormLogLevel(&config.DB{LogQueries: false}))
	})

	t.Run("info when log queries enabled", func(t *testing.T) {
		cfg := &config.DB{LogQueries: true}
		require.NotEqual(t, getGormLogLevel(cfg), getGormLogLevel(&config.DB{LogQueries: false}))
	})
}

func TestIsEmptyStruct(t *testing.T) {
	t.Run("struct{} is empty", func(t *testing.T) {
		require.True(t, isEmptyStruct[struct{}]())
	})

	t.Run("int is not empty struct", func(t *testing.T) {
		require.False(t, isEmptyStruct[int]())
	})

	t.Run("string is not empty struct", func(t *testing.T) {
		require.False(t, isEmptyStruct[string]())
	})

	type named struct{ X int }
	t.Run("named struct is not empty struct", func(t *testing.T) {
		require.False(t, isEmptyStruct[named]())
	})
}

func TestInitState(t *testing.T) {
	state := initState()
	require.Equal(t, uint64(globalStateID), state.ID)
	require.Equal(t, uint64(0), state.LastIndexedBlockNumber)
}

func TestInitVersion(t *testing.T) {
	version := InitVersion()
	require.Equal(t, uint64(globalVersionID), version.ID)
	require.Equal(t, "", version.NodeVersion)
}
