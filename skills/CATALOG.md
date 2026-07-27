# Skills Catalog Provenance

This catalog documents how `swarmstr/skills/` relates to the reference skill collections in `openclaw` and `src`.

## Sources

### Directly carried or closely aligned with OpenClaw-style disk skills
Most directory-based skills in `swarmstr/skills/` follow the same SKILL.md structure used by OpenClaw and are intended to remain disk-compatible where practical.

### Adapted from `src` bundled skills
The following skills were added as static SKILL.md adaptations of high-value bundled prompts from `src/skills/bundled/`:
- `verify`
- `debug`
- `simplify`
- `skillify`
- `remember`

These were adapted instead of copied verbatim so they only reference workflows and tooling that exist in `swarmstr/metiq`.

## Intentionally rejected or still deferred `src` bundled skills
These were reviewed and are still not imported as-is:
- `batch` — depends on a dedicated plan-mode/subagent/worktree/PR orchestration runtime that swarmstr does not expose as a clean bundled-skill surface.
- `stuck` — targets Claude Code process forensics and Slack reporting workflows, including environment-specific ANT-only behavior that does not map to swarmstr.
- `updateConfig` — is tightly coupled to Claude settings files and schema semantics, not metiq/swarmstr runtime config.

## Memory-specific adaptation note
- `remember` was adapted only after mapping it to swarmstr's real memory tools: `memory_pinned`, `memory_search`, `memory_pin`, `memory_store`, and `memory_delete`.
- The port intentionally avoids CLAUDE.md / auto-memory concepts from `src` because those are not swarmstr runtime primitives.

## Plugin and dynamic-source boundary
- Plugin installs and plugin manifests are **not** currently treated as skill sources.
- Swarmstr also does **not** implement `src`-style dynamic or conditional skill generation.
- This is intentional for now: skill discovery remains explicit and filesystem-backed so prompt injection stays predictable, inspectable, and workspace-mirrorable.
- If a plugin needs a companion skill, ship that skill through bundled skills, `extra.skills.extra_dirs`, the managed skills directory, or an agent workspace.
- `nostr-workroom` was ported from the `openclaw-nostr` plugin's companion skill as a static disk skill and **adapted to the Metiq harness**: config paths (`nostr_channels.<room>.config`), the trusted-body surfacing of SenderIsBot / the ambient scan wrapper / mentions-another-participant, the `allowBots` gate + bot-loop pair guard + echo-suppression behavior, and Metiq's coordination targets (the `taskflow` skill, the `bd`/beads tracker, ContextVM). It maps to real swarmstr runtime behavior per the rule below.

### Possible V2 follow-on
A plausible V2 would add **opt-in** plugin-derived or conditionally activated skills, but only if swarmstr can preserve the same guarantees the current catalog has today: explicit source reporting, deterministic precedence, bounded prompt injection, and a workspace-safe readable representation of any injected skill content.

## Rule for future additions
Do not import a skill from `openclaw` or `src` unchanged unless its instructions map cleanly to actual swarmstr tools, runtime behavior, and operator workflows.
