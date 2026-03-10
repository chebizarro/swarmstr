---
summary: "Debugging swarmstr: logs, diagnostics, and common issues"
read_when:
  - swarmstr is not responding or behaving unexpectedly
  - Diagnosing relay connection issues
  - Agent turns are failing or timing out
title: "Debugging"
---

# Debugging

## Command ladder

Start here when something isn't working:

```bash
swarmstr status          # Daemon health + relay connection status
swarmstr health          # Quick health check (returns 0 if healthy)
swarmstr logs --follow   # Real-time log stream
swarmstr doctor          # Diagnostic check: config, keys, relay connectivity
```

## Relay connection issues

### Agent not receiving DMs

```bash
swarmstr relay list      # Show configured relays and connection state
swarmstr relay ping wss://relay.damus.io  # Ping a specific relay
swarmstr logs --follow   # Watch for relay connection errors
```

Check:
- Relay URLs use `wss://` (not `ws://` for public relays).
- The nsec key is valid: `nak key public nsec1...` should return your npub.
- Relays are reachable: `curl -I https://relay.damus.io` should return 200.

### DMs sent but not received

- Some relays have write rate limits. Try adding more relays.
- Verify the DM is addressed to the correct npub.
- Use NIP-17 gift-wrap for better relay delivery.
- Check the sender's client is actually connected to a relay the agent monitors.

## Agent turn issues

### No response to DM

```bash
swarmstr logs --follow   # Look for "Processing DM from..." or error lines
swarmstr sessions --json | jq '.'  # Check session state
```

Common causes:
- DM from unapproved sender (pairing mode: check for pairing challenge DM).
- API key invalid or exhausted: check model provider status.
- Agent timeout (default 600s): increase `agents.defaults.timeoutSeconds`.

### API errors

```bash
swarmstr logs --follow  # Look for model provider errors
swarmstr models status  # Check API key status
```

Common errors:
- `401 Unauthorized` — API key invalid or expired.
- `429 Too Many Requests` — rate limit; configure key rotation or use a different model.
- `context deadline exceeded` — agent timeout; increase `timeoutSeconds`.

## Cron and heartbeat issues

See [Automation Troubleshooting](/automation/troubleshooting).

## Log locations

| Method | Command/Path |
| ------ | ------------ |
| CLI | `swarmstr logs --follow` |
| systemd | `journalctl -u swarmstrd -f` |
| File | `~/.swarmstr/logs/swarmstrd.log` |

## Debug mode

Enable verbose logging:

```bash
SWARMSTR_LOG_LEVEL=debug swarmstrd
```

Or set in config:

```json
{
  "log": {
    "level": "debug"
  }
}
```

## Common issues

### "invalid nsec" on startup

Verify the key format:
```bash
# Should output an npub
nak key public "$NOSTR_PRIVATE_KEY"
```

If it fails, the nsec is malformed. Regenerate with `nak key generate`.

### "relay connection refused"

The relay URL may be wrong or the relay is down. Try:
```bash
swarmstr relay ping wss://relay.damus.io
swarmstr relay info wss://relay.damus.io
```

Switch to a different relay temporarily.

### "session not found"

The session was pruned (maintenance cleanup). The next DM will create a fresh session.
Session history lives at `~/.swarmstr/agents/<id>/sessions/`.

### Agent says wrong thing / uses wrong persona

Check bootstrap files in `~/.swarmstr/workspace/`:
```bash
cat ~/.swarmstr/workspace/SOUL.md
cat ~/.swarmstr/workspace/AGENTS.md
```

If files were recently edited, the change takes effect on the next session start.
Send `/new` to force a fresh session load.

## Getting help

- Check [FAQ](/help/faq) for common questions.
- Open an issue on GitHub with logs and config (redact keys).
- Include output of `swarmstr doctor` in bug reports.
