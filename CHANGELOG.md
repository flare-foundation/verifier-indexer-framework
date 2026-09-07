# Changelog

## [Unreleased]

### Added

- `indexer.ErrBlockNotFound` sentinel: `BlockchainClient` implementations should return it (wrapped) for blocks genuinely missing on the node. Not-found errors are never retried, and a pruned start block is searched for with retried probes. A client that reports a pruned start block with a plain error still works: once the retry window is exhausted the framework warns and falls back to v1.1.1's unretried search, where any failure counts as absent, so a persistent plain failure on exactly the start block can still move it as it did then.
- `indexer.StateSaver` and `indexer.ChainTipSaver`: optional interfaces a `DB` implementation may satisfy (the framework's own does) for the authoritative state write and the per-poll chain-tip write. Without them the indexer writes the state through `SaveAllEntities` and the chain tip only with batch saves, and warns at startup.
- `config.Decode` returns the configuration keys the file declares but the framework does not know, so a caller can warn or reject; `config.ReadFile` keeps ignoring them. The framework warns about them at startup.
- `indexer.ErrInvalidData` sentinel for deterministic processing failures (e.g. a transaction in a validated block that fails to parse). Such errors abort the indexer immediately with a clear error instead of being retried through the full backoff window before each crash.

- Optional readiness endpoint: with `[health] enabled = true` the framework serves `GET /health` on `listen_address` (default `:8080`), returning 200 when the advertised indexed range is current and 503 with a JSON status otherwise. Off by default — no port is opened, no goroutine starts and no code path changes unless it is enabled. The predicate is exported as `health.Handler` for consumers wiring their own stack.
- `chain_stale` health status and `max_chain_age_seconds`: the endpoint answers 503 when the last successful chain poll is older than the allowance, which derives from the worst-case iteration and the up-to-date poll interval when left at zero. The response carries `chain_age_seconds` and `max_chain_age_seconds`.

### Changed

- Require Go 1.26.8 (go directive and CI image); `github.com/jackc/pgx/v5` v5.9.2 and `golang.org/x/text` v0.39.0. Earlier toolchains and these earlier dependency versions carry reachable vulnerabilities (`GO-2026-5856`, `GO-2026-5972`, `GO-2026-5004`, `GO-2026-5970`). `go get` raises a consumer's own `go` directive to match. CI now runs `govulncheck` as a non-gating job.
- `SaveAllEntities` overwrites existing rows on primary-key conflict (`ON CONFLICT DO UPDATE`) instead of skipping them, so re-indexing a range repairs values derived by older code. Rows must be unique by primary key within a single save. An entity without a primary key, or with a unique constraint the primary key cannot arbitrate, keeps v1.1.1's skip-on-conflict clause and is warned about at startup, so re-indexing cannot repair it. A column the current code leaves empty is reset to its default rather than keeping a stale value.
- The chain tip (`last_chain_block_number`, `last_chain_block_timestamp`, `last_chain_block_updated`) is persisted after every successful poll instead of only with a batch save, so readers of the state row see it move on a caught-up indexer.
- When `history_drop` is enabled the block table's timestamp column is taken from `database.Deletable` on the method set of the type the framework is instantiated with (pointer receivers count for a pointer block type), or from the `HistoryDropOrder` entry for the block's own table as on v1.1.1; a block with neither is rejected at startup instead of failing at the first drop.
- When `history_drop` is enabled, `HistoryDropOrder` must be non-empty and list the block entity first; the framework refuses to start otherwise. Deleting transactions before blocks left block rows whose transactions were already gone for the duration of every drop, and indefinitely after a crash mid-drop; a consumer that gates coverage on the block row then attests payments in that window as nonexistent. The README and example taught the transaction-first order.
- The first-indexed-block boundary only ever moves up (except the empty-table reset). Consumers must read a state with `first > last` (or `first == 0`) as an empty advertised range.

### Fixed

- Omitting `DefaultConfig` for a pointer config type no longer leads to a nil-receiver call: a nil pointer config is allocated before TOML decoding, so `ApplyEnvOverrides` (which previously panicked as soon as the matching environment variable was set) and the blockchain client constructor never receive nil. `framework.Run` also returns a descriptive error when `NewBlockchainClient` is missing instead of panicking later.
- History drop deletion is now actually batched: Postgres does not support `LIMIT` on `DELETE` and gorm silently dropped it, so each history drop ran one unbounded delete. Batches are now selected by `ctid` in a subquery, restoring the intended 1000-row batches that avoid long-running locks.
- History drop persists the updated state: the first-indexed boundary is persisted before any rows are deleted and again on completion, and the indexer persists the merged state immediately when picking up a drop result. The stored state can no longer advertise already-deleted blocks.
- A regular indexing-loop save can no longer overwrite the first-indexed boundary a concurrent history drop raised before deleting. `SaveAllEntities` now upserts only the state columns the loop owns and raises the boundary only from the empty sentinel — it never lowers it or touches `last_history_drop` — so the boundary the drop persists before deletion survives the whole deletion window instead of being clobbered mid-drop by an in-flight save. Applying a completed drop result and resuming past unindexed blocks use the new authoritative `SaveState`.
- Resuming past unindexed blocks (e.g. downtime longer than the retention window) moves and persists the coverage boundary before indexing begins instead of advertising the gap as covered.
- The zero reset after a drop empties the database is no longer ignored, and the first indexed block is re-established by the next saved batch.
- A history drop that leaves no surviving block now empties the advertised boundary *before* deleting, instead of after: the stored state no longer advertises the whole range while it is being removed.
- Block 0 is no longer skipped when `start_block_number` is 0 on a fresh database. A zero last-indexed block number is disambiguated from "nothing indexed" by the update timestamp.
- Retry waits and the wait between polls when the indexer is caught up now observe context cancellation, so `SIGINT`/`SIGTERM` are no longer delayed by up to the full backoff or poll interval.
- A signal-initiated shutdown returns `nil` from `framework.Run` instead of the context error, so the process exits zero.
- The startup node-version probe is bounded by `request_timeout_millis`; an unresponsive node no longer hangs startup indefinitely.
- `GetBlockResult` and `GetLatestBlockInfo` returning `(nil, nil)` are reported as a contract violation instead of panicking.
- The blockchain client is constructed before the database is opened, so an invalid blockchain configuration fails before `drop_table_at_start` can drop anything.

## [v1.1.1] - 2026-4-17

### Added

- Fallback in `getMinBlockWithinHistoryInterval`: when the configured `start_block_number` is not available on the node (e.g., pruned), binary-search for the lowest block the node still serves and use it as the effective start.

## [v1.1.0] - 2026-4-15

### Added

- `Close()` method on `database.DB` for proper connection lifecycle management.
- Context cancellation check in `deleteInBatches` between batch iterations.
- Validation that `end_block_number >= start_block_number` in `CheckParameters`.
- Doc comments on all exported and unexported functions, methods, interfaces, and types.
- README usage guide covering framework responsibilities, user implementation requirements, configuration reference, and indexer lifecycle.
- Review checklist documenting manual review areas and resolved findings.
- CLAUDE.md and GOAI.md coding guide for AI-assisted development.

### Changed

- **Breaking:** `EnvOverrideable.ApplyEnvOverrides()` now returns `error` instead of logging and swallowing failures silently.
- Replaced channel-based semaphore with `golang.org/x/sync/semaphore` in `getBlockResults` for context-aware concurrency limiting.
- Replaced `github.com/pkg/errors` with stdlib `errors` and `fmt.Errorf` throughout.
- Bumped Go version and dependencies.
- Enabled additional golangci-lint linters.

### Fixed

- Database connection leak in `database.New` when `DropTable` or `AutoMigrate` fails after a successful connection.
- Framework now defers `db.Close()` to release the connection on shutdown.
- `ApplyEnvOverrides` no longer silently ignores environment variable parse errors.
- Binary search error message in `binarySearchBlockByTime` now includes the block number.
- Inconsistent range variable in `saveData` (`range results.blockResults[i].Events` changed to `range resEvents`).
- `config.BaseConfig` reference in integration test corrected to `config.Base`.

### Removed

- `github.com/pkg/errors` dependency.
- Empty `pkg/database/states.go` file.
