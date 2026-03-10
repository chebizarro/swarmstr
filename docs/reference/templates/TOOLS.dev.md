---
summary: "Dev agent tools notes (swarmstr dev)"
read_when:
  - Using the dev agent templates
  - Updating the default dev agent identity
---

# TOOLS.md - User Tool Notes (editable)

This file is for _your_ notes about external tools and conventions.
It does not define which tools exist; swarmstr provides built-in tools internally.

## Examples

### nostr_fetch / nostr_publish

- Fetch or publish Nostr events. Specify kind, filter, and relay hints.
- Always confirm before publishing public events.

### nostr_send_dm

- Send encrypted DMs to an npub. Use for notifications or handoffs.
- Keep messages concise; avoid sending secrets in DM body.

### canvas_update

- Update the WebSocket canvas with HTML, JSON, or Markdown.
- Provide `canvas_id`, `content_type`, and `data`.

### swarmstr dm-send

- CLI tool for sending a DM to an npub from scripts/cron.
- Usage: `swarmstr dm-send --to <npub> --text "<message>"`

Add whatever else you want the assistant to know about your local toolchain.
