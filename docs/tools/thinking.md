---
summary: "Extended thinking mode in swarmstr: enabling deep reasoning for complex tasks"
read_when:
  - Enabling or configuring thinking mode
  - Understanding when thinking is used and its token cost
title: "Thinking Mode"
---

# Thinking Mode

swarmstr supports extended thinking mode for Anthropic's Claude models (claude-3-7-sonnet and newer). Thinking mode gives the model additional tokens to reason through complex problems before responding.

## Enabling Thinking

### Per-Session (Slash Command)

Toggle thinking on or off for the current session:

```
/set thinking on
/set thinking off
```

To clear the override and revert to the agent default:

```
/unset thinking
```

When enabled via `/set thinking on`, swarmstr uses the `medium` thinking level (10,000 budget tokens) by default.

### Per-Agent Default (Config)

Configure a default thinking level for a specific agent in `config.json`:

```json
{
  "agents": [
    {
      "id": "main",
      "thinking_level": "high"
    }
  ]
}
```

Global default for all agents (when no per-agent override):

```json
{
  "agent": {
    "thinking": "medium"
  }
}
```

## Thinking Levels

| Level | Budget tokens | Use case |
|-------|--------------|----------|
| `off` | 0 | Standard responses |
| `minimal` | 1,024 | Light reflection |
| `low` | 5,000 | Simple reasoning |
| `medium` | 10,000 | Complex tasks (default when thinking is on) |
| `high` | 20,000 | Deep analysis |
| `xhigh` | 40,000 | Maximum reasoning |

> Thinking tokens count toward your API usage. High/xhigh thinking significantly increases costs.

## What Thinking Does

When thinking is enabled, Claude:
1. Reasons through the problem in an internal scratchpad (the thinking block)
2. Uses that reasoning to produce a better response
3. Returns both the thinking trace and the final response

The thinking trace is included in the transcript but not sent back to the user via Nostr DM (only the final response is delivered).

## Thinking and Tool Use

Thinking mode works alongside tool use. The model can think, then call a tool, then think again based on the result. This is particularly useful for:

- Complex multi-step Nostr queries
- Architecture decisions with many trade-offs
- Debugging intricate Go concurrency issues
- Planning long chains of cron jobs

## Transcript Storage

Thinking blocks are stored in the session transcript as `thinking` content blocks. They count toward token usage but are not transmitted to the user.

## Model Support

Currently supported with:
- `anthropic/claude-opus-4-5`
- `anthropic/claude-sonnet-4-5`
- `anthropic/claude-haiku-4-5`
- Any Claude 3.7+ model

Not supported with OpenAI, Ollama, or other providers.

## See Also

- [Model Providers](/providers/)
- [Session Management](/concepts/session)
- [Slash Commands](/tools/slash-commands)
