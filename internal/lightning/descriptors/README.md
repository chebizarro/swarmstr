# Bundled LND and tapd descriptors

`lnd.pb` and `tapd.pb` are protobuf descriptor sets with imports. Metiq embeds
them and uses dynamic protobuf invocation, so building and running Metiq does
not require `protoc`, generated LND/tapd Go clients, or gRPC reflection.

`tools.json` pins the upstream repositories, tags, commits, checksums, exact RPC
allowlists, stable tool names, toolsets, and safety traits. The Go tests load
every descriptor and fail if any curated RPC is absent or duplicated.

## Deterministic regeneration

Install protobuf compiler 34.1, then run:

```sh
internal/lightning/descriptors/regenerate.sh --check
# or, after intentionally updating pins/checksums:
internal/lightning/descriptors/regenerate.sh
```

The script downloads only the pinned source commits, invokes `protoc` with
fixed inputs and `--include_imports`, verifies SHA-256 checksums, and refreshes
the upstream MIT license copies. Descriptor upgrades are reviewed repository
changes: update the commits, tags, expected checksums, manifest methods, and
licenses together.

`protoc` and network access are regeneration-only dependencies.

## Runtime interoperability

The opt-in container-backed regtest target and pinned image versions are
documented in [`../integration/README.md`](../integration/README.md). It is
excluded from the default unit suite.
