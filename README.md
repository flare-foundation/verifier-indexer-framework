<div align="center">
  <a href="https://flare.network/" target="blank">
    <img src="https://content.flare.network/Flare-2.svg" width="300" alt="Flare Logo" />
  </a>
  <br />
  <a href="CONTRIBUTING.md">Contributing</a>
  ·
  <a href="SECURITY.md">Security</a>
  ·
  <a href="CHANGELOG.md">Changelog</a>
</div>

# Verifier Indexer Framework

A generic, blockchain-agnostic framework for building blockchain indexers.
It works with any chain whose blocks are numbered sequentially and contain timestamps — including EVM and UTXO-based chains.

The framework provides a complete indexing pipeline: configuration loading, database management, a concurrent block-fetching loop with retry logic, state tracking, history pruning, and graceful shutdown.
You supply the blockchain-specific pieces — a client that knows how to talk to your chain, and database entity types that describe what to store.

## Installation

The framework should be installed as a dependency using `go get`.
For an example of how to integrate please see the `cmd/example` directory.

## What the Framework Does

### Configuration (`pkg/config`)

- Loads a TOML config file (default `config.toml`, overridable via `--config` flag or `CONFIG_FILE` env var).
- Provides sensible defaults for database connection pooling, indexer concurrency, timeouts, and logging.
- Overrides database credentials from environment variables (`DB_HOST`, `DB_PORT`, `DB_USERNAME`, `DB_PASSWORD`, `DB_NAME`).
- Validates required parameters before starting.

### Database (`pkg/database`)

- Connects to PostgreSQL via GORM.
- Auto-migrates your block, transaction, and event tables plus internal `state` and `version` tables.
- Persists blocks, transactions, and events in a single atomic transaction, overwriting existing rows on primary-key conflict (entity rows are deterministically derived from immutable chain data, so re-indexing a range repairs them).
- Tracks indexer state: last/first indexed block, latest chain head, last history drop timestamp.
- Saves build and runtime version metadata.
- Optionally drops all tables on startup (`drop_table_at_start`).

### Indexer Loop (`pkg/indexer`)

- Polls the chain head, determines the next range of blocks to fetch, and fetches them concurrently up to `max_concurrency`.
- Respects a configurable number of `confirmations` — only indexes blocks that are at least N blocks behind the chain tip.
- Limits each iteration to `max_block_range` blocks.
- Wraps every blockchain call with exponential backoff and per-request timeouts.
- Sleeps with exponential backoff when the indexer is caught up, then resumes immediately when new blocks appear.
- Runs history pruning asynchronously in the background at the configured frequency.
- Handles `SIGINT`/`SIGTERM` for graceful shutdown.
- Optionally stops at a configured `end_block_number`.

### Framework Entrypoint (`pkg/framework`)

- Parses CLI arguments.
- Composes all of the above: loads config, connects to the DB, constructs your blockchain client, saves version info, and starts the indexer loop.
- Exposes a single function `framework.Run` as the entrypoint.

## How It Works

### Startup

1. `framework.Run` parses CLI arguments, loads the TOML config file, applies environment variable overrides, and validates parameters.
2. The framework connects to PostgreSQL and auto-migrates all tables — your block, transaction, and event tables plus the internal `state` and `version` tables.
3. It calls your `NewBlockchainClient` constructor to create the blockchain client.
4. Build metadata (git tag, commit hash, build date) and the blockchain node version are saved to the `version` table.
5. The indexer loads its persisted state from the database to determine where to resume.

#### Determining the Start Block

The start block depends on whether history drop is enabled:

- **History drop disabled:**
If the database already contains indexed blocks, the indexer resumes from the block after the last indexed one.
On a fresh database, it starts from the configured `start_block_number`.
- **History drop enabled:**
The indexer performs a binary search on the chain to find the earliest block whose timestamp falls within the `history_drop` interval of the current chain tip.
If the database already has blocks indexed past that point, it resumes from where it left off.
Otherwise, it starts from the calculated block — meaning it will not waste time indexing blocks that would be immediately pruned.
When that skips ahead of previously indexed data (e.g., downtime longer than the retention window), the blocks in between are never indexed: the first-indexed-block boundary is moved up to the new start and persisted before indexing begins, so the state never advertises the gap as covered.
Any older rows still in the database sit outside the advertised range until history drops remove them.
This boundary move happens **at startup only** — the start block is not re-derived against the retention window while the indexer runs. An indexer that cannot keep up with the chain will have each history drop delete the range it has just indexed; that is a capacity problem, and the fix is more throughput, not a different boundary.
Until the first new batch is saved, the persisted state may have a first indexed block greater than the last indexed block — consumers must read that (like a zero first indexed block) as an empty advertised range.
If the configured `start_block_number` is no longer available on the node (e.g., the node has pruned it), the framework binary-searches for the lowest block the node still serves and uses that as the effective start instead.

### Main Loop

Each iteration of the main loop performs these steps:

1. **Update chain state.**
Fetch the latest block number and timestamp from the blockchain node.
This is retried with exponential backoff on failure.
2. **Poll history drop results.**
Check (non-blocking) whether a background history drop has completed.
If so, apply the updated first-indexed-block state and persist it immediately.
3. **Maybe start history drop.**
If history drop is enabled and enough time has elapsed since the last drop, start a new one in a background goroutine.
Only one history drop runs at a time.
4. **Compute block range.**
The next range to index starts after the last indexed block and ends at `chain_tip - confirmations`, capped at `max_block_range` blocks per iteration.
If the range is empty, the indexer is up to date.
5. **Fetch blocks.**
All blocks in the range are fetched concurrently via `GetBlockResult`, bounded by `max_concurrency`.
Each call is individually wrapped with a per-request timeout and exponential backoff.
6. **Persist.**
The fetched blocks, transactions, events, and updated state are written to the database in a single atomic transaction.
Rows that already exist are overwritten — entity rows are deterministically derived from immutable chain data, so re-indexing a range repairs values previously derived by older code.
7. **Repeat.**
If a configured `end_block_number` has been reached, the indexer exits.
Otherwise, it loops back to step 1.

If any step fails after exhausting retries, the indexer returns a fatal error and shuts down.

### When Up to Date

When the indexer has processed all confirmed blocks, the computed block range is empty.
Instead of busy-looping, it sleeps with exponential backoff — starting with short pauses and gradually increasing up to a maximum interval.
As soon as the next iteration detects new confirmed blocks on the chain, the backoff resets and the indexer resumes fetching at full speed.

### History Drop

When `history_drop` is configured (in seconds), the indexer periodically prunes blocks and related entities older than that interval behind the chain tip:

- A history drop is triggered when `history_drop_frequency` seconds (defaults to `history_drop`) have elapsed since the last drop.
- It runs asynchronously in a background goroutine so it does not block the main indexing loop.
- Entities are deleted in the order returned by `HistoryDropOrder` to respect foreign key constraints (e.g., transactions and events before blocks).
- Deletions happen in batches of 1000 rows to avoid long-running database locks.
- Only one history drop runs at a time — if one is already in progress, the next iteration skips it.
- The first-indexed-block boundary is persisted before any rows are deleted, so the stored state never advertises blocks that have already been removed.
- On completion, the state is updated and persisted with the new first indexed block and the timestamp of the last drop.

### Error Handling and Retries

Every blockchain RPC call is wrapped with exponential backoff and a per-request timeout (`request_timeout_millis`).
If all retries are exhausted within `backoff_max_elapsed_time_seconds`, the error is considered fatal and the indexer shuts down.
The same retry strategy applies to chain state updates, block fetching, and history drops independently.

Two error classes are exempt because retrying them cannot help. An error wrapping `indexer.ErrBlockNotFound` or `indexer.ErrInvalidData` is treated as permanent and stops the retry loop immediately instead of consuming the whole backoff window. See [What You Must Implement](#4-blockchain-client) for the contract your client must honour.

Retry waits and the wait between polls when the indexer is caught up both observe context cancellation, so a shutdown signal is not delayed by an in-progress backoff.

### Graceful Shutdown

The indexer listens for `SIGINT` and `SIGTERM`.
On receiving either signal it cancels the context, which interrupts any retry or up-to-date wait in progress and lets the current iteration stop cleanly.
A signal-initiated shutdown is not an error: `framework.Run` returns `nil`, so the process exits zero.

## What You Must Implement

### 1. Block Entity

A struct that will be stored as a database row.
It must implement `database.Block`:

```go
type Block interface {
    GetTimestamp() uint64
    GetBlockNumber() uint64
    HistoryDropOrder() []Deletable
}
```

The struct should have GORM tags defining the table schema.
`HistoryDropOrder` returns the list of entity types (as zero-value instances) that should be deleted during history pruning, ordered to respect foreign key constraints (e.g., transactions before blocks).

Each entity returned by `HistoryDropOrder` must implement `database.Deletable`:

```go
type Deletable interface {
    TimestampField() string  // returns the DB column name used for timestamp-based deletion
}
```

When `history_drop` is enabled the block entity itself must implement `database.Deletable`, and it must do so on the **value** receiver — the framework reads the column name from a zero value of your block type, and a pointer-receiver method is invisible there. It is rejected at startup if missing.

Index both the `TimestampField()` column and `block_number` on every prunable entity:

```go
Timestamp   uint64 `gorm:"index"`
BlockNumber uint64 `gorm:"index"`
```

History drop deletes in 1000-row batches selected by `ctid`, and finds the surviving boundary by timestamp. Without these indexes each batch degrades into a full table scan, so a large table makes pruning progressively slower.

A child entity's `TimestampField()` column must carry its **parent block's** timestamp, not a time of its own. The advertised coverage boundary is computed from the block table alone, so a child with an independent timestamp can be pruned out from under a block that is still advertised as indexed.

### 2. Transaction Entity

Any struct with GORM tags.
There is no required interface — `database.Transaction` is defined as `any`.
The framework stores these in batches alongside their parent blocks.

**Every entity must declare a primary key**, and it must be the entity's deterministic chain identity (a hash or block number), not a generated sequence. Rows are overwritten on primary-key conflict, so the primary key is the conflict target; the framework refuses to start without one. Two consequences worth knowing:

- Rows must be unique by primary key *within* a single save, or PostgreSQL rejects the whole batch.
- A conflict on any **other** unique constraint fails the save instead of overwriting the row. The framework logs a warning at startup for entities declaring such constraints.

### 3. Event Entity (Optional)

Any struct with GORM tags, or `struct{}` if your chain does not produce events.
When `struct{}` is used, the framework skips event table creation and storage entirely.

### 4. Blockchain Client

A type implementing `indexer.BlockchainClient[B, T, E]`:

```go
type BlockchainClient[B Block, T Transaction, E Event] interface {
    GetLatestBlockInfo(context.Context) (*BlockInfo, error)
    GetBlockResult(context.Context, uint64) (*BlockResult[B, T, E], error)
    GetBlockTimestamp(context.Context, uint64) (uint64, error)
    GetServerInfo(context.Context) (string, error)
}
```

| Method | Purpose |
|---|---|
| `GetLatestBlockInfo` | Return the chain tip block number and timestamp. Called once per iteration. |
| `GetBlockResult` | Fetch a single block by number and return its parsed block, transactions, and events. Called concurrently for each block in the range. |
| `GetBlockTimestamp` | Return just the timestamp for a block number. Used during history drop start-block calculation. |
| `GetServerInfo` | Return the node's version string. Called once at startup for metadata. |

You do **not** need to implement retry logic — the framework wraps every call with exponential backoff automatically.

Methods taking a block number must return an error wrapping `indexer.ErrBlockNotFound` when the block does not exist on the node (e.g. pruned or not yet available).
The framework relies on this to distinguish missing blocks from transient failures: not-found errors are never retried, and only they may move the start block to the oldest available block during history drop start-up.

Deterministic processing failures — data in a validated block that the implementation cannot parse — must wrap `indexer.ErrInvalidData`.
Such errors are not retried and abort the indexer immediately with a clear error: retrying cannot help, and silently skipping data would corrupt the advertised coverage.
Resolving one requires operator action, usually an indexer upgrade that understands the new data format.

### 5. Blockchain Config (Optional)

A struct implementing `config.EnvOverrideable`:

```go
type EnvOverrideable interface {
    ApplyEnvOverrides() error
}
```

This holds any blockchain-specific config fields (e.g., RPC URL, API key).
It is decoded from the `[blockchain]` section of the TOML file.
`ApplyEnvOverrides` lets you override fields from environment variables.
If you have no blockchain-specific config, use a no-op implementation.

### 6. Constructor Function

A function with the signature:

```go
func NewClient(cfg *YourConfig) (indexer.BlockchainClient[B, T, E], error)
```

This is called once at startup after configuration is loaded.

### 7. Main Function

Wire everything together and call `framework.Run`:

```go
func main() {
    input := framework.Input[MyBlock, *MyConfig, MyTransaction, MyEvent]{
        DefaultConfig:       &MyConfig{/* defaults */},
        NewBlockchainClient: NewClient,
    }

    if err := framework.Run(input); err != nil {
        logger.Fatal(err)
    }
}
```

The four type parameters are: `[Block, Config, Transaction, Event]`.

Set `DefaultConfig` to provide defaults for your blockchain-specific fields — these are used before the TOML file and env overrides are applied.
If you have no defaults, this can be omitted: a nil pointer config is allocated automatically, so `ApplyEnvOverrides` and your constructor never receive a nil receiver.

## Configuration Reference

A minimal `config.toml`:

```toml
[db]
host = "localhost"
port = 5432
username = "postgres"
password = "password"
db_name = "my_indexer"

[indexer]
confirmations = 12
start_block_number = 0
```

Full reference:

```toml
[db]
host = "localhost"                  # PostgreSQL host (default: "localhost")
port = 5432                         # PostgreSQL port (default: 5432)
username = ""                       # DB username
password = ""                       # DB password
db_name = ""                        # DB name
max_open_conns = 25                 # Max open connections (default: 25)
max_idle_conns = 5                  # Max idle connections (default: 5)
conn_max_lifetime_seconds = 300     # Connection max lifetime (default: 300)
log_queries = false                 # Log all SQL queries (default: false)
drop_table_at_start = false         # Drop and recreate tables on startup (default: false)
history_drop = 0                    # Delete blocks older than this many seconds; 0 = disabled
history_drop_frequency = 0          # Seconds between history drops; defaults to history_drop value

[indexer]
confirmations = 12                  # Required. Blocks behind chain tip before indexing.
max_block_range = 1000              # Max blocks per iteration (default: 1000)
max_concurrency = 8                 # Concurrent block fetch goroutines (default: 8)
start_block_number = 0              # Block to start indexing from
end_block_number = 0                # Stop after this block; 0 = run forever

[timeout]
backoff_max_elapsed_time_seconds = 300   # Max total retry time (default: 300)
request_timeout_millis = 3000            # Per-request timeout (default: 3000)

[logger]
level = "DEBUG"                          # DEBUG, INFO, WARN, ERROR, DPANIC, PANIC or FATAL (default: DEBUG)
console = true                           # Also log to the console (default: true)
file = ""                                # Optional log file path
max_file_size = 0                        # Log file size in megabytes before rotating

[blockchain]
# Your blockchain-specific fields go here
```

All `[db]` credential fields can be overridden with environment variables: `DB_HOST`, `DB_PORT`, `DB_USERNAME`, `DB_PASSWORD`, `DB_NAME`.

## Minimal Working Example

See `cmd/example/` for a complete skeleton.
The key structure is:

1. Define your DB entity structs with GORM tags.
2. Implement `database.Block` on your block struct.
3. Implement `database.Deletable` on entities included in `HistoryDropOrder`.
4. Implement `indexer.BlockchainClient` to talk to your chain's RPC.
5. Implement `config.EnvOverrideable` for any custom config.
6. Call `framework.Run` from `main`.

## Testing

There is an integration test, which requires a running instance of Postgres with a database called `indexer_framework_db`.
You may run such a database locally with Docker:

```bash
docker-compose -f tests/docker-compose.yaml up -d
```

Then, modify the `tests/test_config.toml` file to change the `host` field to
`localhost`.

Finally, you can run the tests with:

```bash
go test --tags=integration ./...
```
