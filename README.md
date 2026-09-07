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

### Health Endpoint (`pkg/health`, optional)

- Serves `GET /health` when `[health] enabled = true`; off by default, so nothing listens and no query is issued unless you turn it on.
- Answers 200 when the advertised range is current and 503 with a JSON status otherwise.

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
The chain tip is persisted right away, independently of any batch save, so the state row keeps moving while the indexer is caught up.
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

### Health Endpoint

Off by default. With no `[health]` table nothing listens, no goroutine starts, and no extra query is issued.

`GET /health` (and `HEAD`) returns 200 when the indexer is ready and 503 otherwise, with a JSON body. Other methods get 405, other paths 404.

The predicate is evaluated top-down; the first match wins:

| Status | Meaning |
|---|---|
| `unavailable` | The indexer state could not be read. |
| `initializing` | The advertised range is empty (`first == 0` or `first > last`). |
| `chain_stale` | The last successful chain poll is older than `max_chain_age_seconds`, so the stored chain tip and every check resting on it cannot be trusted. |
| `catching_up` | The chain head is more than `max_block_lag` ahead of the last indexed block. |
| `stalled` | Confirmed blocks are pending *and* the last progress write is older than `max_progress_age_seconds`. |
| `ready` | None of the above. |

All three allowances derive from configuration you already set when left at zero: `max_block_lag` becomes `confirmations + max_block_range` (one full iteration behind the confirmed head); `max_progress_age_seconds` becomes twice the worst-case iteration, `ceil(max_block_range / max_concurrency) x request_timeout_millis`; and `max_chain_age_seconds` becomes twice the longest gap between two polls, which is the worst-case iteration or the longest jittered up-to-date wait (90 s), plus one `request_timeout_millis`. Lower `max_concurrency` therefore *lengthens* the allowances, because an iteration can legitimately take longer. The effective values are echoed in every response, so you can read your real steady-state lag off the endpoint and tighten from there.

The `stalled` check is deliberately gated on the lag exceeding `confirmations`. A caught-up indexer makes no progress between polls, so its progress stamp ages even though nothing is wrong; without the gate the check would fire on any quiet chain.

Kubernetes: use `readinessProbe` and `startupProbe`, and **no `livenessProbe`**. The indexer exits on a persistent error, so the container runtime's restart-on-exit already is the liveness mechanism, and a legitimate backfill answers 503 for its whole duration — a liveness probe would restart-loop the pod. A bounded run (`end_block_number` set) is behind its own end block throughout, so leave the endpoint disabled for such jobs.

Two operational cautions:

- **Alert on `ready` or the status code, never on a number alone.** The block and age fields read `0` when the status is `unavailable`.
- The port is unauthenticated and `:8080` binds all interfaces. Restrict it with a network policy, or set `listen_address` to a specific interface. Loopback is not the default because a kubelet `httpGet` probe targets the pod IP.

The chain tip is persisted after every successful poll, so a node outage becomes `chain_stale` even while the indexer is caught up, and a restart against an unreachable node answers 503 for the same reason. A custom `DB` implementation without `indexer.ChainTipSaver` keeps the old blind spot.

`verifier-indexer-api`'s `GET /api/health` is a different service on a different port: it returns the state row with no predicate, answering what data it can serve rather than whether this indexer is caught up.

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

When `history_drop` is enabled the framework needs the block table's timestamp column at startup. It takes it from `database.Deletable` on the method set of the type you instantiate the framework with (a pointer type such as `*Block` may use pointer receivers), or, as on v1.1.1, from the `HistoryDropOrder` entry that maps to the block's own table. A block with neither is rejected at startup rather than at the first drop.

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

**Declare a primary key that is the entity's deterministic chain identity** (a hash or block number), not a generated sequence. Rows are overwritten on primary-key conflict, so re-indexing a range repairs values derived by older code. Two consequences worth knowing:

- Rows must be unique by primary key *within* a single save, or PostgreSQL rejects the whole batch.
- An entity without a primary key, or with a unique constraint the primary key cannot arbitrate (a sequence id next to a unique hash, say), keeps v1.1.1's behaviour: conflicting rows are skipped and never repaired. The framework warns about such entities at startup.

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

Methods taking a block number should return an error wrapping `indexer.ErrBlockNotFound` when the block does not exist on the node (e.g. pruned or not yet available).
The framework relies on this to distinguish missing blocks from transient failures: not-found errors are never retried, and a pruned start block is searched for with retried probes.
A client that reports a pruned start block with a plain error still works: once the retry window is exhausted the framework warns and falls back to v1.1.1's unretried search, in which any failure counts as absent, so a persistent plain failure on exactly the start block can move it as it did then.

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

[health]
enabled = false                          # Opt-in readiness endpoint; nothing listens unless true
listen_address = ":8080"                 # Listen address (default ":8080"); binds all interfaces
max_block_lag = 0                        # Max tolerated lag behind the chain head; 0 derives confirmations + max_block_range
max_progress_age_seconds = 0             # Max age of the last progress write; 0 derives twice the worst-case iteration
max_chain_age_seconds = 0                # Max age of the last successful chain poll; 0 derives twice the longest poll gap
cache_millis = 1000                      # Min interval between database reads (default: 1000); 0 reads every request

[logger]
level = "DEBUG"                          # DEBUG, INFO, WARN, ERROR, DPANIC, PANIC or FATAL (default: DEBUG)
console = true                           # Also log to the console (default: true)
file = ""                                # Optional log file path
max_file_size = 0                        # Log file size in megabytes before rotating

[blockchain]
# Your blockchain-specific fields go here
```

All `[db]` credential fields can be overridden with environment variables: `DB_HOST`, `DB_PORT`, `DB_USERNAME`, `DB_PASSWORD`, `DB_NAME`.

Keys the framework does not know are logged as a warning at startup and ignored, so a mistyped key is visible without failing a v1.1.1 deployment.

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
