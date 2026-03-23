# Code Review Checklist — verifier-indexer-framework

## Bugs Found by Automated Review

- [ ] **`tests/test_config.toml:18`** — Key `timeout_millis` should be `request_timeout_millis` (silently ignored, works by coincidence with default value)
- [ ] **`.gitlab-ci.yml:3`** — `GOLANG_VERSION: "1.24.4"` doesn't match `go.mod` (`go 1.25.5`); CI builds with wrong Go version
- [ ] **`pkg/config/config.go:91`** — `logger.Error` used with `%v` format verb; should be `logger.Errorf`
- [ ] **`pkg/database/history_drop.go:59`** — `return &newState, err` should be `return &newState, nil` (err is always nil here but misleading)
- [ ] **`pkg/indexer/history_drop.go:51,80`** — Uint64 subtraction without underflow guard (`latestBlock.Timestamp - firstBlockTime`)
- [ ] **`pkg/indexer/indexer.go:421`** — `blockNum := i` is redundant (Go 1.22+ loop variable scoping)
- [ ] **`pkg/indexer/indexer.go:375`** — `getBlockRange` returns `error` but never produces one; simplify signature
- [ ] **`pkg/database/states.go`** — Empty file (only `package database`); remove or populate
- [ ] **`pkg/database/database.go:79`** — `return &DB[B, T, E]{g: db}, err` should be `return &DB[B, T, E]{g: db}, nil`

## Manual Review Areas

### Configuration & Build

- [ ] Verify `config.ReadFile` behavior when TOML file has unknown keys (does BurntSushi/toml silently ignore them or warn?)
- [ ] Verify `config.ReadBuildVersion` works correctly when run from different working directories (reads relative paths: `PROJECT_VERSION`, `PROJECT_BUILD_DATE`, `PROJECT_COMMIT_HASH`)
- [ ] Check that `DefaultBaseConfig` defaults are sensible for production use
- [ ] Confirm `DropTableAtStart` intentionally does NOT drop the `Version` table (only `State`, blocks, transactions, events)

### Database Layer

- [ ] Review `OnConflict{DoNothing: true}` strategy — confirm this is correct for all entity types (blocks, transactions, events)
- [ ] Review `SaveAllEntities` transaction isolation level (GORM default is `READ COMMITTED` on Postgres)
- [ ] Validate that `transactionBatchSize = 1000` and `deleteBatchSize = 1000` are appropriate for expected data volumes
- [ ] Confirm `formatDSN` properly handles special characters in username/password (uses `url.UserPassword` which should URL-encode)
- [ ] Review `isEmptyStruct` pattern (`*new(T)`) — confirm it works correctly with all generic type parameters
- [ ] Confirm `AutoMigrate` handles schema evolution correctly for production upgrades (no destructive changes)

### Indexer Logic

- [ ] Review `getInitialStartBlockNumber` logic when `historyDropInterval > 0` and `state.LastIndexedBlockNumber > 0` but falls behind the history window
- [ ] Validate `getEndBlock` arithmetic for edge cases: `confirmations > LastChainBlockNumber`, `start > latestConfirmedNum`
- [ ] Confirm `binarySearchBlockByTime` handles edge cases: equal timestamps, single-block range, all blocks within interval
- [ ] Review the up-to-date backoff pattern in `runIteration` (lines 202-209) — `time.Sleep` inside a retry operation is unconventional; verify it doesn't interact badly with the outer backoff
- [ ] Confirm `maxConcurrency` semaphore + errgroup pattern doesn't leak goroutines on context cancellation
- [ ] Review whether `updateState` correctly handles the `LastIndexedBlockNumber == 0` check — is block 0 a valid block number?

### Concurrency & History Drop

- [ ] Review `maybeRunHistoryDrop` goroutine lifecycle — confirm no goroutine leaks when context is cancelled
- [ ] Validate the `TryLock` / channel / defer unlock pattern in history drop; confirm no deadlock scenarios
- [ ] Confirm `pollHistoryDropResults` correctly merges state — check that `FirstIndexedBlockNumber` comparison is correct after concurrent indexing + history drop
- [ ] Review that the history drop goroutine's `defer` correctly handles the case where `newState` is nil (retry exhausted)

### Security

- [ ] Confirm DB credentials are never logged (check all `logger.*` calls, especially `logger.Debugf("...%+v", state)`)
- [ ] Validate `validColumnName` regex in `deleteInBatches` prevents SQL injection via `TimestampField()`
- [ ] Review that `formatDSN` doesn't log the connection string with password
- [ ] Confirm no secrets in `test_config.toml` or `docker-compose.yaml` that shouldn't be committed

### Testing

- [ ] Confirm unit tests cover error paths (not just happy paths)
- [ ] Review `TestIndexer` — does it test enough of the indexer behavior? (currently tests a single iteration)
- [ ] Confirm integration test (`TestRun`) is deterministic — time-based `GetLatestBlockInfo` could cause flaky assertions
- [ ] Check if `getBlockRange`, `getEndBlock`, `binarySearchBlockByTime`, and `deleteInBatches` need dedicated unit tests
- [ ] Verify the mock implementations in `indexer_test.go` accurately reflect real behavior

### CI/CD

- [ ] Fix Go version mismatch between CI and `go.mod`
- [ ] Confirm `golangci-lint` version in CI is compatible with Go version and `.golangci.yml` config
- [ ] Review whether `go get github.com/boumenot/gocover-cobertura` should be pinned to a version
- [ ] Confirm test coverage artifacts are correctly generated and reported

### API / Interface Design

- [ ] Review `Block` interface — is `HistoryDropOrder() []Deletable` the right place for deletion ordering? (couples block type to deletion strategy)
- [ ] Review `Transaction` and `Event` being defined as `any` — consider whether minimal interfaces would improve type safety
- [ ] Confirm `EnvOverrideable` interface is used consistently (both `BaseConfig` and user configs must implement it)
- [ ] Review `Input` struct — `DefaultConfig C` is a pointer type constraint but the zero value is used when not set

### Documentation & Style

- [ ] Confirm all exported types and functions have doc comments (per GOAI.md: "complete sentences starting with the name")
- [ ] Verify naming follows GOAI.md conventions (no `Get` prefix, shortest descriptive names)
- [ ] Run `golangci-lint run` and resolve any findings
- [ ] Run `gofmt` and confirm no formatting issues
