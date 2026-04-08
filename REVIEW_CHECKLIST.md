# Code Review Checklist — verifier-indexer-framework

## Bugs Found by Automated Review

All items from the original automated review have been resolved:

- [x] **`tests/test_config.toml:18`** — Key `timeout_millis` renamed to `request_timeout_millis`
- [x] **`.gitlab-ci.yml:3`** — `GOLANG_VERSION` now matches `go.mod` (`go 1.25.5`)
- [x] **`pkg/config/config.go`** — `ApplyEnvOverrides` now returns `error` instead of logging and swallowing
- [x] **`pkg/database/history_drop.go`** — `return &newState, err` replaced with `return &newState, nil`
- [x] **`pkg/indexer/history_drop.go`** — Uint64 subtractions are guarded by comparison checks
- [x] **`pkg/indexer/indexer.go`** — Redundant `blockNum := i` removed (Go 1.22+ scoping)
- [x] **`pkg/indexer/indexer.go`** — `getBlockRange` signature simplified (no longer returns unused error)
- [x] **`pkg/database/states.go`** — Empty file removed
- [x] **`pkg/database/database.go`** — `return &DB{g: db}, err` replaced with `return &DB{g: db}, nil`

## Fixes Applied During Review

- [x] **Connection leak in `database.New`** — `Close()` method added to `DB`; connection is closed on `DropTable`/`AutoMigrate` failure; framework defers `db.Close()` on shutdown
- [x] **Missing context check in `deleteInBatches`** — Loop now checks `ctx.Done()` between batch iterations
- [x] **Semaphore ignoring context** — Channel-based semaphore replaced with `sync/semaphore.Weighted` which respects context cancellation
- [x] **`EnvOverrideable` silently swallowing errors** — Interface changed to `ApplyEnvOverrides() error`; errors propagate to caller
- [x] **Missing validation** — `CheckParameters` now rejects `end_block_number < start_block_number`
- [x] **Binary search error message** — Now includes the block number for easier debugging
- [x] **Inconsistent range variable in `saveData`** — `for j := range results.blockResults[i].Events` changed to `for j := range resEvents`
- [x] **Test file bug** — `config.BaseConfig` reference in integration test corrected to `config.Base`
- [x] **Doc comments** — Added to all exported and unexported functions, methods, interfaces, and types

## Manual Review Areas

### Configuration & Build

- [ ] Verify `config.ReadFile` behavior when TOML file has unknown keys (does BurntSushi/toml silently ignore them or warn?)
- [ ] Verify `config.ReadBuildVersion` works correctly when run from different working directories (reads relative paths: `PROJECT_VERSION`, `PROJECT_BUILD_DATE`, `PROJECT_COMMIT_HASH`)
- [ ] Check that `DefaultBase` defaults are sensible for production use
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
- [ ] Review the up-to-date backoff pattern in `runIteration` — `time.Sleep` inside a retry operation is unconventional; verify it doesn't interact badly with the outer backoff
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

- [ ] Confirm `golangci-lint` version in CI is compatible with Go version and `.golangci.yml` config
- [ ] Review whether `go get github.com/boumenot/gocover-cobertura` should be pinned to a version
- [ ] Confirm test coverage artifacts are correctly generated and reported

### API / Interface Design

- [ ] Review `Block` interface — is `HistoryDropOrder() []Deletable` the right place for deletion ordering? (couples block type to deletion strategy)
- [ ] Review `Transaction` and `Event` being defined as `any` — consider whether minimal interfaces would improve type safety
- [ ] Review `Input` struct — `DefaultConfig C` is a pointer type constraint but the zero value is used when not set
