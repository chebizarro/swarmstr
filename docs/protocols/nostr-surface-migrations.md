# Nostr surface migration notes

## NIP-90

Treat NIP-90 as a compatibility surface rather than the default abstraction for new work. ContextVM is the default for MCP/control workloads; it is not a feature-for-feature replacement for NIP-90 marketplace, payment, or status semantics.

Legacy NIP-90 support is **off by default**. Setting `extra.dvm.enabled=true` enables both the outbound `nostr_dvm_request` tool and the inbound 5xxx-job provider. The value is read at startup, so changing it requires a daemon restart. Enabling the provider expands the daemon's externally reachable Nostr surface; keep accepted kinds narrowly configured. This compatibility flag and both legacy paths are scheduled for removal in the next breaking release. Until then, disabling the flag is the rollback to the ContextVM-only default. Do not introduce a single replacement RPC-style layer.

## NIP-96

Make Blossom the default upload and media-storage path for new configuration. Keep NIP-96 as an explicitly selected compatibility path while existing deployments migrate server discovery, authorization, upload, and deletion workflows. Advertised capability metadata must distinguish the configured default from compatibility support.
