---
summary: "Debugging metiq: logs, diagnostics, and common issues"
read_when:
  - metiq is not responding or behaving unexpectedly
  - Diagnosing relay connection issues
  - Agent turns are failing or timing out
title: "Debugging"
---

# Debugging

## Command ladder

Start here when something isn't working:

```bash
metiq status          # Daemon health + relay connection status
metiq health          # Quick health check (returns 0 if healthy)
metiq logs --lines 100  # Recent log lines
metiq doctor          # Diagnostic check: config, keys, relay connectivity
```

## Relay connection issues

### Agent not receiving DMs

```bash
metiq channels status   # Show configured channels and connection state
metiq logs --lines 100  # Watch for relay connection errors
```

Check:
- Relay URLs use `wss://` (not `ws://` for public relays).
- The nsec key is valid: `nak key public nsec1...` should return your npub.
- Relays are reachable: `metiq relay ping wss://<relay-1>` should succeed.

### DMs sent but not received

- Some relays have write rate limits. Try adding more relays.
- Verify the DM is addressed to the correct npub.
- Use NIP-17 gift-wrap for better relay delivery.
- Check the sender's client is actually connected to a relay the agent monitors.

## Agent turn issues

### No response to DM

```bash
metiq logs --lines 100          # Look for "Processing DM from..." or error lines
metiq sessions list --json      # Check session state
```

Common causes:
- DM from unapproved sender (pairing mode): sender gets a notice; add them to `dm.allow_from`.
- API key invalid or exhausted: check model provider.
- Agent timeout: the turn timed out; check logs for details.

### API errors

```bash
metiq logs --lines 100  # Look for model provider errors
metiq models list       # Check configured models
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
| CLI | `metiq logs --lines 100` |
| systemd | `journalctl -u metiqd -f` |
| File | `~/.metiq/logs/metiqd.log` |

## Debug mode

Enable verbose logging for a specific session:

```
/set verbose on
```

For daemon-level verbosity, run metiqd in a terminal and watch stderr. To capture:

```bash
metiqd 2>&1 | tee /tmp/metiqd-debug.log
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
metiq relay ping wss://<relay-1>
metiq relay info wss://<relay-1>
```

Switch to a different relay temporarily.

### "session not found"

The session was pruned (maintenance cleanup). The next DM will create a fresh session.
Session history lives at `~/.metiq/agents/<id>/sessions/`.

### Agent says wrong thing / uses wrong persona

Check bootstrap files in `~/.metiq/workspace/`:
```bash
cat ~/.metiq/workspace/SOUL.md
cat ~/.metiq/workspace/AGENTS.md
```

If files were recently edited, the change takes effect on the next session start.
Send `/new` to force a fresh session load.

## Getting help

- Check [FAQ](/help/faq) for common questions.
- Open an issue on GitHub with logs and config (redact keys).
- Include output of `metiq doctor` in bug reports.
