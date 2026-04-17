# Changelog

## [Unreleased]

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
