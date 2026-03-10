---
summary: "swarmstr environment variables reference"
read_when:
  - Customizing swarmstr paths or behavior via environment
  - Running swarmstr as a service account
title: "Environment Variables"
---

# Environment Variables

swarmstr reads environment variables to override default paths and behavior.

## Core paths

| Variable                | Default                    | Description                                      |
| ----------------------- | -------------------------- | ------------------------------------------------ |
| `SWARMSTR_HOME`         | `$HOME`                    | Home directory used for internal path resolution |
| `SWARMSTR_STATE_DIR`    | `~/.swarmstr`              | State directory (config, sessions, credentials)  |
| `SWARMSTR_CONFIG_PATH`  | `~/.swarmstr/config.json`  | Config file path                                 |
| `SWARMSTR_WORKSPACE`    | `~/.swarmstr/workspace`    | Agent workspace directory override               |

## Auth and keys

| Variable                | Description                                      |
| ----------------------- | ------------------------------------------------ |
| `NOSTR_PRIVATE_KEY`     | Nostr private key (nsec or hex). Referenced in config as `${NOSTR_PRIVATE_KEY}` |
| `ANTHROPIC_API_KEY`     | Anthropic API key                                |
| `OPENAI_API_KEY`        | OpenAI API key                                   |
| `SWARMSTR_GATEWAY_TOKEN`| HTTP/WebSocket API auth token                    |

## Behavior flags

| Variable                 | Description                                      |
| ------------------------ | ------------------------------------------------ |
| `SWARMSTR_SKIP_CRON`     | Set to `1` to disable the cron scheduler         |
| `SWARMSTR_LOG_LEVEL`     | Log level: `debug`, `info`, `warn`, `error`      |
| `SWARMSTR_PORT`          | Override default HTTP/WS port (default: `18789`) |

## Model providers

| Variable                | Description                                      |
| ----------------------- | ------------------------------------------------ |
| `OPENAI_API_KEYS`       | Comma-separated list for key rotation            |
| `ANTHROPIC_API_KEYS`    | Comma-separated list for key rotation            |
| `OPENROUTER_API_KEY`    | OpenRouter API key                               |
| `GEMINI_API_KEY`        | Google Gemini API key                            |
| `OLLAMA_API_KEY`        | Ollama API key (usually `ollama-local`)          |

## Nostr-specific

| Variable                | Description                                      |
| ----------------------- | ------------------------------------------------ |
| `NOSTR_PRIVATE_KEY`     | Primary agent Nostr private key                  |
| `NOSTR_RELAYS`          | Comma-separated relay URLs (overrides config)    |

## Docker/container environments

When running in a container, set:

```bash
SWARMSTR_STATE_DIR=/data/.swarmstr
SWARMSTR_CONFIG_PATH=/data/.swarmstr/config.json
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
```

## Using environment variables in config

swarmstr supports `${VAR_NAME}` interpolation in `config.json`:

```json
{
  "channels": {
    "nostr": {
      "privateKey": "${NOSTR_PRIVATE_KEY}"
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "apiKey": "${ANTHROPIC_API_KEY}"
      }
    }
  }
}
```

This keeps secrets out of the config file and in the environment.
