# Go Coding Guide for Claude

This is the Flare Go style guide. Follow these conventions when writing Go code for Flare projects.
Where these rules conflict with general Go conventions, these rules take precedence.

## Linting

Command `golangci-lint run` must be used before the end of each edit and issues must be resolved.

A `.golangci.yml` config is provided in this directory — use it as a template for new projects.

When ignoring a linter, always include a reason:

```go
//nolint:<linterName>,<otherLinter> // reason for ignoring
```

Place it directly above a block (package, struct, function) or inline on a single line.

## Naming

- Use the shortest name that sufficiently describes the item. Omit words clear from context.
- The smaller the scope, the shorter the name. Common abbreviations (`db`, `cfg`) are fine.
- Names should describe purpose, not value.
- Only camelCase and PascalCase are allowed. Never use `_` in any identifier, including test function names.
- For initialisms and acronyms, all letters must have the same case: `ID`, `URL`, `HTTP` — not `Id`, `Url`, `Http`.

### Functions and Methods

- Do not use `Get` as a prefix. Use `Compute`, `Fetch`, or a descriptive verb — and only when the operation takes meaningful time. For simple accessors, just name the concept directly.
- Receiver variables must be very short (one or two letters) and consistent across all methods of the same type.

### Modules

All Flare module names must match their public repo path:

```
github.com/flare-foundation/<name-of-the-repo>
```

### Packages

- Package name and its folder name must be identical — no underscores in folder names (except `_test` packages).
- Avoid generic names like `utils` for exported packages.
- Name package contents with the package name in mind, and vice versa — the full reference is `package.Name`, so avoid redundancy.

## Repo Organization

- `pkg/` — exported packages
- `internal/` — unexported packages
- `cmd/` — executables
- Tests and test helpers go in `_test.go` files or `/internal`, never exported.

## Errors

- Prefer `errors.New` over `fmt.Errorf` when there are no format arguments.
- Use `%w` for error wrapping, `%v` for error formatting in log messages.
- Error types end with `Error` or `error`; error variables start with `Err`.
- `errors.New("text")` and another `errors.New("text")` are distinct errors. Use package-level variables when code needs to match on a specific error.
- Never expose internal error details through a server API.
- Always handle errors. If intentionally ignoring one, document it:

```go
//nolint:errcheck,gosec // reason for ignoring
```

## Comments

- Doc comments are complete sentences starting with the name of the item they describe.
- Describe what the item does, not how. Only include "how" if it is not obvious from the code.

## Logging

- Use the logger from [`go-flare-common`](https://github.com/flare-foundation/go-flare-common).
- Exported code must not log directly. If logging is needed, accept a logger as an interface and let the caller configure it.

## Dependencies

Prefer the standard library. Add external dependencies only when necessary.

### go-flare-common

[`go-flare-common`](https://github.com/flare-foundation/go-flare-common) is the shared library across Flare Go projects. It includes:

- Logging utilities
- abigen contract bindings
- Common helpers reused across repos

All changes to this repo must be backwards compatible and well tested.

### go-ethereum and avalanchego

These are largely interchangeable in the Flare ecosystem. Prefer them for:

- `abi` — working with ABIs
- `crypto` — Ethereum-ecosystem cryptography
- `common` — general helpers
- `hexutil` — marshaling of byte slices

#### abigen

Use `abigen` to generate type-safe Go bindings from smart contract ABIs.

Standard practice: create a directory named after the package (derived from the contract name). Place the ABI file and a Go file with a `go:generate` directive there:

```go
//go:generate abigen --abi=neki.abi --pkg=neki --type Neki --out=autogen.go

package neki
```

All bindings can then be regenerated with:

```bash
go generate ./...
```

## Testing

- Use [testify](https://pkg.go.dev/github.com/stretchr/testify): `require` for fatal assertions, `assert` for non-fatal, `mock` for mocking.
- When a unit test needs an external database (SQL, Redis, …), use an in-memory mock instead of a real database.
- Tests must be independent — they must pass in any order and potentially in parallel.
- Use `t.Helper()` in auxiliary test functions.
- Prefer [table-driven tests](https://go.dev/wiki/TableDrivenTests) for cases with multiple inputs.

## Pitfalls

### Time durations

Never use raw integers for durations. Always multiply by the time unit:

```go
x = 12 * time.Second  // correct
x = 12                // wrong
```

### Variable shadowing with `:=`

When a variable is declared outside a block and set inside using `:=` alongside an error return, the outer variable is silently shadowed:

```go
// Wrong — outer x is never set
var x int
if something {
    x, err := f()  // x here is a new variable scoped to the if block
}

// Correct
var x int
if something {
    var err error
    x, err = f()
}
```

### Slice initialization

When the final length is known, initialize with capacity to avoid reallocations:

```go
x := make([]T, 0, n)  // then append
// or
x := make([]T, n)     // then assign by index
```

### Interface satisfaction check

To assert at compile time that `*X` implements interface `Y`:

```go
var _ Y = &X{}
```

Place this near the type definition. A missing method causes a compile error immediately.
