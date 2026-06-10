# Changelog

## [Unreleased]

### Added

- `indexer.ErrBlockNotFound` sentinel: `BlockchainClient` implementations must return it (wrapped) for blocks genuinely missing on the node. Not-found errors are never retried, and only they may move the start block during history drop start-up; any other startup probe failure now aborts instead of silently raising the start block.

### Changed

- **Breaking:** require Go 1.26.8 (go directive and CI image); `github.com/jackc/pgx/v5` v5.9.2 and `golang.org/x/text` v0.39.0. Earlier toolchains and these earlier dependency versions carry reachable vulnerabilities (`GO-2026-5856`, `GO-2026-5972`, `GO-2026-5004`, `GO-2026-5970`). Consumers must raise their own `go` directive to build. CI now runs `govulncheck` as a non-gating job.
- **Breaking:** `SaveAllEntities` overwrites existing rows on primary-key conflict (`ON CONFLICT DO UPDATE`) instead of skipping them, so re-indexing a range repairs values derived by older code. Entities must have unique primary keys within a batch (as on a real chain).
- The first-indexed-block boundary only ever moves up (except the empty-table reset). Consumers must read a state with `first > last` (or `first == 0`) as an empty advertised range.
- Removed the internal unretried blockchain client: startup probes now retry transient errors and fail fast on `ErrBlockNotFound`.

### Fixed

- Omitting `DefaultConfig` for a pointer config type no longer leads to a nil-receiver call: a nil pointer config is allocated before TOML decoding, so `ApplyEnvOverrides` (which previously panicked as soon as the matching environment variable was set) and the blockchain client constructor never receive nil. `framework.Run` also returns a descriptive error when `NewBlockchainClient` is missing instead of panicking later.
- History drop deletion is now actually batched: Postgres does not support `LIMIT` on `DELETE` and gorm silently dropped it, so each history drop ran one unbounded delete. Batches are now selected by `ctid` in a subquery, restoring the intended 1000-row batches that avoid long-running locks.
- History drop persists the updated state: the first-indexed boundary is persisted before any rows are deleted and again on completion, and the indexer persists the merged state immediately when picking up a drop result. The stored state can no longer advertise already-deleted blocks.
- Resuming past unindexed blocks (e.g. downtime longer than the retention window) moves and persists the coverage boundary before indexing begins instead of advertising the gap as covered.
- The zero reset after a drop empties the database is no longer ignored, and the first indexed block is re-established by the next saved batch.

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
