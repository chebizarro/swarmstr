---
summary: "kind:30317 capability advertisement schema for metiq-compatible runtimes"
read_when:
  - Implementing Nostr-native capability publishing in another runtime
  - Debugging fleet capability discovery
  - Looking up the kind:30317 tag schema
title: "Capability Advertisement"
sidebarTitle: "Capabilities"
---

# Capability Advertisement

metiq publishes a parameterized replaceable `kind:30317` event so fleet peers can discover runtime metadata without relying on a manually maintained `TOOLS.md` file.

## Event Shape

The event is authored by the agent pubkey and uses that same pubkey as its `d` tag:

```json
{
  "kind": 30317,
  "content": "",
  "tags": [
    ["d", "<agent-pubkey-hex>"],
    ["runtime", "metiq", "<version>"],
    ["dm_schemes", "nip17", "nip44", "giftwrap", "nip04"],
    ["acp_version", "1"],
    ["tools", "memory_search", "nostr_agent_rpc", "web_search"],
    ["relay", "wss://<relay-4>"],
    ["relay", "wss://<relay-5>"]
  ]
}
```

## Tags

- `d`
  - Required.
  - Must be the publishing agent's hex pubkey.
  - Makes the event parameterized-replaceable so the latest capability descriptor wins.
- `runtime`
  - Required.
  - Format: `["runtime", "<runtime-name>", "<runtime-version>"]`
  - `runtime-version` is optional but recommended.
- `dm_schemes`
  - Optional.
  - Lists supported inbound/outbound DM schemes.
  - metiq currently advertises values such as `nip17`, `nip44`, `giftwrap`, and `nip04` depending on active transports.
  - Consumers should treat this tag as a set of supported schemes, not an ordered preference list.
- `acp_version`
  - Optional but recommended.
  - Stringified ACP wire version.
- `tools`
  - Optional.
  - Lists the runtime's provider-visible tool names after profile/allowlist filtering.
- `relay`
  - Optional and repeatable.
  - Each tag gives a relay URL where the runtime expects capability discovery or related traffic.

## Publishing Rules

- Publish once at startup.
- Re-publish when config changes affect relays, DM transport support, or visible tool surface.
- Keep `content` empty; the schema is tag-based for easy relay-side filtering and low overhead.

## Discovery Rules

- Fleet members subscribe to known peers' `kind:30317` events and should filter on the canonical `d=<peer-pubkey-hex>` value.
- Consumers should keep the newest event per pubkey using `created_at`, with event ID as a deterministic tie-breaker for same-second collisions.
- Relay subscriptions should include both the local fleet relays and any per-peer relay hints already learned from directory events.
- Unknown tags must be ignored so runtimes can extend the schema safely.

## Compatibility Notes

- Other runtimes can publish the same tag layout with a different `runtime` value.
- Consumers should not assume every tag is present.
- If a runtime exposes multiple internal personas behind one pubkey, `tools` should describe the default externally reachable tool surface for that pubkey.
- metiq's ACP `auto` transport mode consults `dm_schemes` to choose a compatible DM family.
- Because `dm_schemes` is set-like today, not preference-ordered, metiq currently prefers `nip17` when both `nip17` and `nip04` are advertised, and otherwise falls back to `nip04` when that is the only discovered compatible scheme.
- When no capability metadata is available yet, metiq's ACP `auto` mode uses a compatibility-first fallback and prefers `nip04` before `nip17`.
- For runtimes that only implement NIP-04 ACP delivery, publish `dm_schemes` including `nip04` and configure metiq peers with `acp.transport: nip04` when you need an explicit compatibility profile.
