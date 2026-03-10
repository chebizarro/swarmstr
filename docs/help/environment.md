---
summary: "swarmstr environment variables reference"
read_when:
  - Customizing swarmstr paths or behavior via environment
  - Running swarmstr as a service account
title: "Environment Variables"
---

# Environment Variables

swarmstr reads the following environment variables directly.

## CLI connectivity

| Variable             | Description                                                         |
|----------------------|---------------------------------------------------------------------|
| `SWARMSTR_ADMIN_ADDR`  | Admin API address (e.g. `127.0.0.1:18788`). Overrides bootstrap.json `admin_listen_addr`. |
| `SWARMSTR_ADMIN_TOKEN` | Admin API bearer token. Overrides bootstrap.json `admin_token`.   |

## Workspace

| Variable             | Default                    | Description                          |
|----------------------|----------------------------|--------------------------------------|
| `SWARMSTR_WORKSPACE` | `~/.swarmstr/workspace`    | Agent workspace directory override   |

## Model provider

| Variable                    | Description                                              |
|-----------------------------|----------------------------------------------------------|
| `SWARMSTR_AGENT_PROVIDER`   | Provider alias for the default agent runtime             |
| `SWARMSTR_AGENT_HTTP_URL`   | Base URL for the custom HTTP provider mode               |
| `SWARMSTR_AGENT_HTTP_API_KEY` | API key for the custom HTTP provider                  |

## Browser tool

| Variable                | Description                                      |
|-------------------------|--------------------------------------------------|
| `SWARMSTR_BROWSER_URL`  | URL of the browser CDP proxy (enables browser tool) |
| `SWARMSTR_BROWSER_TOKEN`| Auth token for the browser CDP proxy             |

## Secret references in config files

`bootstrap.json` and `config.json` support `${VAR_NAME}` interpolation — the value is read from the environment at startup. This is how secrets are typically passed in:

```json
{
  "private_key": "${NOSTR_NSEC}",
  "admin_token": "${SWARMSTR_ADMIN_TOKEN}"
}
```

Any environment variable can be used this way. Common examples:

| Variable                | Description                                      |
|-------------------------|--------------------------------------------------|
| `NOSTR_NSEC`            | Nostr private key (nsec format)                  |
| `ANTHROPIC_API_KEY`     | Anthropic API key                                |
| `OPENAI_API_KEY`        | OpenAI API key                                   |
| `OPENROUTER_API_KEY`    | OpenRouter API key                               |
| `BRAVE_SEARCH_API_KEY`  | Brave Search API key                             |
| `SERPER_API_KEY`        | Serper web search API key                        |

Store these in `~/.swarmstr/.env` and load via the systemd `EnvironmentFile=` directive.

## What does NOT exist

These env vars are **not** supported by swarmstr and have no effect:

- `SWARMSTR_HOME` — no effect; home dir is system `$HOME`
- `SWARMSTR_STATE_DIR` — no effect; state is always at `~/.swarmstr`
- `SWARMSTR_CONFIG_PATH` — no effect; use `--config` CLI flag instead
- `SWARMSTR_LOG_LEVEL` — no effect; verbosity is set per-session with `/set verbose on`
- `SWARMSTR_SKIP_CRON` — no effect; disable cron via `cron.enabled: false` in config
