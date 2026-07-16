# LND/tapd regtest interoperability

This is an explicit, destructive-to-ephemeral-state integration target for the
pinned Lightning descriptors. It is excluded from the default Go test suite by
the `lightning_integration` build tag.

The runner creates a temporary Docker network with bitcoind, two LND nodes, and
one tapd node. It funds both LND wallets, opens a channel, and mints a regtest
Taproot Asset. The Go test then invokes the embedded descriptors through
`internal/agent/toolgrpc` (including file-backed macaroon hex encoding) and
verifies:

- LND `GetInfo`, wallet balance, and channel balance;
- LND invoice creation and a controlled 1,000 sat payment between the nodes;
- tapd `GetInfo`, asset listing, and a new address for the controlled mint.

The mint is setup performed with `tapcli`; asset sending is not part of this
single-tapd target.

## Requirements and pinned images

- a running Docker daemon reachable by the current user;
- Go as required by the repository;
- `bitcoin/bitcoin:28.1`;
- `lightninglabs/lnd:v0.21.1-beta`;
- `lightninglabs/taproot-assets:v0.8.0`.

The LND and tapd versions match
`internal/lightning/descriptors/tools.json`. Override image references only
for registry mirrors by setting `BITCOIND_IMAGE`, `LND_IMAGE`, or
`TAPD_IMAGE`; compatibility is asserted against the pinned descriptor
versions.

## Run

From the repository root:

```sh
internal/lightning/integration/run.sh
```

The script pulls missing images through `docker run`, binds host ports 11009,
12009, and 10029 on loopback, and deletes all containers, the network, and
temporary credentials on exit. Those ports must be free.

If Docker is missing or the daemon is unavailable, the runner and tagged Go
test fail immediately with a clear error. The default `go test ./...` does not
discover or skip this target.
