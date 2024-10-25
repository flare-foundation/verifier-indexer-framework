Verifier Indexer Framework
==========================

This is a generic framework for creating a blockchain indexer. As far as
possible it is designed to be blockchain-agnostic, so that it may be used
with a wide variety of blockchains including EVM or UTXO-based chains. It
assumes that the blockchain contains blocks which are numbered sequentially
and contain timestamps. It is also assumed that each block contains some
number of transactions, each of which has a unique identifier.

Installation
------------

The framework should be installed as a dependency using `go get`. For an
example of how to integrate please see the `cmd/example` directory.
Applications must define types which implement a particular interface and call
the `framework.Run` method to run the indexer.

Testing
-------

There is an integration test, which requires a running instance of Postgres
with a database called `verifier_indexer_test`. You may run such a database
locally with Docker:

```bash
docker-compose -f tests/docker-compose.yaml up -d
```

Then, modify the `tests/test_config.yaml` file to change the `host` field to
`localhost`.

Finally, you can run the tests with:

```bash
go test --tags=integration ./...
```
