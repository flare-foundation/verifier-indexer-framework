# Contributing

Thank you for considering improving out source code.
All contributions are welcome.

## Issues

_Sensitive security-related issues should not be reported publicly.
See [Security](SECURITY.md)._

To share ideas, considerations, or concerned open an issue.
Before filing an issue make sure the issue has not been already raised.
In the issue, answer the following questions:

- What is the issue?
- Why it is an issue?
- How do you propose to change it?

### Pull request

Before opening a pull request open an issue on why the request is needed.

To contribute: fork the repo, make your improvements, commit and open a pull request.
The maintainers will review the request.

The request must:

- Reference the relevant issue,
- Follow standard golang guidelines,
- Be well documented,
- Be well tested,
- Compile,
- Pass all the tests,
- Pass all the linters,
- Be based on and opened against `main` branch.

## Setting the environment

Make sure you are using go with higher or equal version to the one specified in go.mod.

Get all the dependencies

```bash
go mod tidy
```

### Tests

Run unit tests with

```bash
go test ./...
```

There is an integration test, which requires a running instance of PostgreSQL
with a database called `verifier_indexer_test`.
You may run such a database locally with Docker:

```bash
docker-compose -f tests/docker-compose.yaml up -d
```

Then, modify the `tests/test_config.yaml` file to change the `host` field to
`localhost`.

Finally, you can run the integration tests with:

```bash
go test --tags=integration ./...
```

### Linting

Run linters (make sure you have [golangci-lint](https://golangci-lint.run/) installed) with

```bash
golangci-lint run
```

The linting configs are specified in [.golangci.yml](.golangci.yml).

## AI Assistance

Any significant use of the AI assistance in the contribution MUST be disclosed in the pull request along with the extent of the use.

An example disclosure:

> This PR was written primarily by Claude Code.

Or a more detailed disclosure:

> I consulted ChatGPT for the following code snippets: ...
