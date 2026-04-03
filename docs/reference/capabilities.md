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
    ["tools", "memory_search", "nostr_agent_rpc", "web_search", "contextvm_resources_read"],
    ["contextvm_features", "discover", "resources_read", "prompts_get"],
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
  - This remains the compatibility-safe superset that older consumers can use.
- `contextvm_features`
  - Optional.
  - Lists the normalized ContextVM surface exposed by the runtime.
  - metiq currently emits feature tokens derived from provider-visible tools:
    - `discover`
    - `tools_list`
    - `tools_call`
    - `resources_list`
    - `resources_read`
    - `prompts_list`
    - `prompts_get`
    - `raw`
  - Consumers should treat this tag as a set, not an ordered preference list.
  - When this tag is absent, metiq still derives a compatibility fallback from advertised `tools` for the built-in `contextvm_*` tool family.
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

## ACP Routing Contract

metiq now uses capability metadata for peer selection when multiple discovered peers could satisfy the same ACP target name.

Routing rules:
- registration in the local ACP peer registry is still required
- DM-family compatibility is evaluated before capability compatibility
- if the caller provides `tool_profile` and/or `enabled_tools`, metiq derives the required tool surface from that contract
- required ContextVM features are derived from requested tool names, not from free-text instructions
- peers that explicitly advertise an incompatible surface are rejected
- peers with no usable capability metadata remain eligible as `unknown`, so discovery lag does not hard-break dispatch

In practice this means:
- a peer advertising `tools=["contextvm_resources_read"]` or `contextvm_features=["resources_read"]` can be preferred for ACP tasks constrained to that surface
- a peer advertising only `tools=["memory_search"]` can be rejected for the same task
- `tools` remains the broad compatibility signal; `contextvm_features` is the precise signal for MCP/ContextVM routing
