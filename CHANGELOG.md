# Changelog

## [Unreleased]

### Added

- `indexer.ErrBlockNotFound` sentinel: `BlockchainClient` implementations must return it (wrapped) for blocks genuinely missing on the node. Not-found errors are never retried, and only they may move the start block during history drop start-up; any other startup probe failure now aborts instead of silently raising the start block.
- `indexer.ErrInvalidData` sentinel for deterministic processing failures (e.g. a transaction in a validated block that fails to parse). Such errors abort the indexer immediately with a clear error instead of being retried through the full backoff window before each crash.

- Optional readiness endpoint: with `[health] enabled = true` the framework serves `GET /health` on `listen_address` (default `:8080`), returning 200 when the advertised indexed range is current and 503 with a JSON status otherwise. Off by default — no port is opened, no goroutine starts and no code path changes unless it is enabled. The predicate is exported as `health.Handler` for consumers wiring their own stack.

### Changed

- **Breaking:** require Go 1.26.8 (go directive and CI image); `github.com/jackc/pgx/v5` v5.9.2 and `golang.org/x/text` v0.39.0. Earlier toolchains and these earlier dependency versions carry reachable vulnerabilities (`GO-2026-5856`, `GO-2026-5972`, `GO-2026-5004`, `GO-2026-5970`). Consumers must raise their own `go` directive to build. CI now runs `govulncheck` as a non-gating job.
- **Breaking:** `SaveAllEntities` overwrites existing rows on primary-key conflict (`ON CONFLICT DO UPDATE`) instead of skipping them, so re-indexing a range repairs values derived by older code. Three consequences: every entity must declare a primary key (the framework refuses to start otherwise, and PostgreSQL cannot infer a conflict target without one); rows must be unique by primary key within a single save; and a conflict on any other unique constraint now fails the save instead of being skipped — such entities are warned about at startup. A column the current code leaves empty is reset to its default rather than keeping a stale value.
- **Breaking:** `indexer.DB` gained `SaveState`, used where the caller holds the authoritative first-indexed boundary. Custom `DB` implementations must add it to compile.
- **Breaking:** unknown keys in the TOML configuration are now rejected instead of silently ignored, so a mistyped key fails at startup rather than leaving the default in place.
- When `history_drop` is enabled the block entity must implement `database.Deletable` on its value receiver; this is verified at startup instead of falling back to a hardcoded `timestamp` column.
- The first-indexed-block boundary only ever moves up (except the empty-table reset). Consumers must read a state with `first > last` (or `first == 0`) as an empty advertised range.
- Removed the internal unretried blockchain client: startup probes now retry transient errors and fail fast on `ErrBlockNotFound`.

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
