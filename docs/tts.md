---
summary: "TTS (text-to-speech) configuration and providers for swarmstr"
read_when:
  - Setting up TTS for swarmstr
  - Configuring cloud TTS for agent responses
  - Adding voice output to agent responses
title: "Text-to-Speech (TTS)"
---

# Text-to-Speech (TTS)

swarmstr supports multiple TTS providers for agent voice output.

## Providers

Built-in providers (selected based on available credentials):

| Provider | ID | Env var required |
|----------|----|-----------------|
| OpenAI TTS | `openai` | `OPENAI_API_KEY` |
| Kokoro | `kokoro` | `KOKORO_API_KEY` (or local server URL) |
| Google TTS | `google` | `GOOGLE_APPLICATION_CREDENTIALS` |
| ElevenLabs | `elevenlabs` | `ELEVENLABS_API_KEY` |

## Configuration

Enable TTS and set the default provider/voice in `config.json`:

```json
{
  "tts": {
    "enabled": true,
    "provider": "openai",
    "voice": "nova"
  }
}
```

### OpenAI TTS

Set the `OPENAI_API_KEY` environment variable (or reference it in the secrets store).
Available voices: `alloy`, `echo`, `fable`, `onyx`, `nova`, `shimmer`.

```json
{
  "tts": {
    "enabled": true,
    "provider": "openai",
    "voice": "nova"
  }
}
```

### ElevenLabs

Set `ELEVENLABS_API_KEY` and configure the voice ID:

```json
{
  "tts": {
    "enabled": true,
    "provider": "elevenlabs",
    "voice": "pNInz6obpgDQGcFmaJgB"
  }
}
```

### Kokoro (Local)

Kokoro is a local TTS server. Run it on your machine and point swarmstr at it via
`KOKORO_BASE_URL` (or configure in provider settings).

## Listing & Switching Providers

```bash
# List available TTS providers and their configured status
swarmstr gw tts.providers

# Switch active provider
swarmstr gw tts.set_provider '{"provider": "elevenlabs", "voice": "pNInz6obpgDQGcFmaJgB"}'
```

## TTS with Node Devices

When a node device is paired with audio output:

```bash
# Invoke TTS on a remote node
swarmstr nodes invoke --node mypi --command audio.tts \
  --args '{"text": "Hello", "provider": "openai"}'
```

## TTS Tool

The agent can generate speech via the `tts` tool. When enabled and a TTS provider is configured, the agent calls this tool in response to voice requests.

## Voice Quality vs Cost

| Provider | Quality | Cost | Notes |
|----------|---------|------|-------|
| OpenAI TTS-1 | Great | $0.015/1K chars | Cloud |
| OpenAI TTS-1-HD | Excellent | $0.030/1K chars | Cloud |
| ElevenLabs | Excellent | Varies | Cloud |
| Kokoro | Good | Free | Local server |
| Google TTS | Good | Varies | Cloud |

## See Also

- [Audio & TTS](/nodes/audio)
- [Nodes Overview](/nodes/)
- [Skills](/tools/skills)
