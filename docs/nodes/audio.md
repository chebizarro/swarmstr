---
summary: "Audio input/output and TTS for metiq nodes"
read_when:
  - Adding voice capabilities to metiq
  - Configuring TTS or STT (speech-to-text)
  - Setting up audio on a node device
title: "Audio & TTS"
---

# Audio & TTS

metiq supports text-to-speech (TTS) and speech-to-text (STT) via built-in providers
and node device plugins.

## Text-to-Speech (TTS)

Configure TTS in `config.json` under the `tts` key:

```json
{
  "tts": {
    "enabled": true,
    "provider": "openai",
    "voice": "nova"
  }
}
```

Supported providers: `openai`, `kokoro`, `google`, `elevenlabs`.

See [TTS Configuration](/tts) for full details.

### Per-Session TTS

Enable TTS for a specific DM session:

```
/set tts on
```

The agent will speak its replies via the configured TTS provider.

## Speech-to-Text (STT)

metiq automatically transcribes audio attachments sent via Nostr DM using **OpenAI Whisper** (`whisper-1` model). No configuration is required — it is enabled automatically when `OPENAI_API_KEY` is set.

```bash
# In ~/.metiq/.env
OPENAI_API_KEY=sk-...
```

When a user sends a voice message or audio file as a Nostr media attachment, metiq:

1. Downloads the audio file
2. Transcribes it via Whisper
3. Passes the transcribed text to the agent as the user's message

## Node Audio Commands

When a metiq node device (e.g., Raspberry Pi) has audio hardware and exposes audio commands via ACP, you can invoke them via the CLI or agent tools.

### Via CLI

```bash
# Invoke TTS on a paired node
metiq nodes invoke --node <node-id> --command audio.tts \
  --args '{"text": "Hello from metiq"}'

# Record from node microphone (duration in ms)
metiq nodes invoke --node <node-id> --command audio.record \
  --args '{"duration_ms": 5000}'
```

The `--node` flag takes the node ID as shown by `metiq nodes list`.

### Via Agent Tool

The `node_invoke` tool lets the agent send commands to nodes:

```
node_invoke(
  node_pubkey="<hex-pubkey-of-node>",
  instructions="Play TTS: Hello from metiq",
  timeout_seconds=10
)
```

## Node Plugin: Audio Capabilities

Audio commands (`audio.tts`, `audio.record`) are implemented by the node device itself.
The node must:

1. Be paired with the daemon (`metiq nodes pending` → `metiq nodes approve <id>`)
2. Implement audio capabilities and expose them via ACP DM replies

See the [Nodes Overview](/nodes/) for pairing setup.

## See Also

- [TTS Configuration](/tts) — provider setup and voices
- [Nodes Overview](/nodes/) — pairing and node management
- [Skills](/tools/skills) — custom skill-based audio tools
