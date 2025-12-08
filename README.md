<div align="center">
  <a href="https://flare.network/" target="blank">
    <img src="https://content.flare.network/Flare-2.svg" width="300" alt="Flare Logo" />
  </a>
  <br/>
  <a href="CONTRIBUTING.md">Contributing</a>
  ·
  <a href="SECURITY.md">Security</a>
  ·
  <a href="CHANGELOG.md">Changelog</a>
</div>

# Verifier Indexer Framework

This is a generic framework for creating a blockchain indexer.
As far as possible it is designed to be blockchain-agnostic, so that it may be used
with a wide variety of blockchains including EVM or UTXO-based chains.
It assumes that the blockchain contains blocks which are numbered sequentially
and contain timestamps.
It is also assumed that each block contains some
number of transactions, each of which has a unique identifier.

[![API Reference](https://pkg.go.dev/badge/github.com/flare-foundation/verifier-indexer-framework)](https://pkg.go.dev/github.com/flare-foundation/verifier-indexer-framework?tab=doc)

## Installation

The framework should be installed as a dependency using `go get`.
For an example of how to integrate it, please see the `cmd/example` directory.
Applications must define types which implement a particular interface and call
the `framework.Run` method to run the indexer.
