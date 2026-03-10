---
summary: "TTS (text-to-speech) configuration and providers for swarmstr"
read_when:
  - Setting up TTS for swarmstr
  - Configuring sherpa-onnx or cloud TTS
  - Adding voice output to agent responses
title: "Text-to-Speech (TTS)"
---

# Text-to-Speech (TTS)

swarmstr supports multiple TTS providers for agent voice output.

## Providers

### sherpa-onnx (Offline, Recommended)

Run TTS fully offline with no API costs:

```bash
# Install
pip install sherpa-onnx

# Download VITS model
wget https://huggingface.co/csukuangfj/vits-piper-en_US-amy-low/resolve/main/en_US-amy-low.onnx

# Test
sherpa-onnx-tts --model en_US-amy-low.onnx "Hello from swarmstr"
```

Configure:

```json5
{
  "tools": {
    "tts": {
      "provider": "sherpa-onnx",
      "modelPath": "~/.sherpa-onnx/en_US-amy-low.onnx",
      "defaultVoice": "en_US-amy-low"
    }
  }
}
```

### OpenAI TTS

```json5
{
  "tools": {
    "tts": {
      "provider": "openai",
      "apiKey": "${OPENAI_API_KEY}",
      "voice": "nova",    // "alloy" | "echo" | "fable" | "onyx" | "nova" | "shimmer"
      "model": "tts-1"   // "tts-1" | "tts-1-hd"
    }
  }
}
```

### ElevenLabs

```json5
{
  "tools": {
    "tts": {
      "provider": "elevenlabs",
      "apiKey": "${ELEVENLABS_API_KEY}",
      "voiceId": "pNInz6obpgDQGcFmaJgB"   // Adam voice
    }
  }
}
```

## TTS with Node Devices

When a node device is paired with audio output:

```bash
# Agent calls TTS, audio plays on node
swarmstr nodes invoke --node mypi --command audio.tts \
  --params '{"text": "Hello", "provider": "sherpa-onnx"}'
```

## TTS Skill

The sherpa-onnx TTS skill enables the agent to generate audio in response to requests:

```
User: "Read me the daily summary"
Agent: [calls TTS skill, audio plays on connected node or is sent as audio file via Nostr]
```

## Voice Quality vs Cost

| Provider | Quality | Cost | Offline |
|----------|---------|------|---------|
| sherpa-onnx | Good | Free | ✅ |
| OpenAI TTS-1 | Great | $0.015/1K chars | ❌ |
| OpenAI TTS-1-HD | Excellent | $0.030/1K chars | ❌ |
| ElevenLabs | Excellent | Varies | ❌ |

For always-on agents, sherpa-onnx is recommended to avoid API costs.

## See Also

- [Audio & TTS](/nodes/audio)
- [Nodes Overview](/nodes/)
- [Skills](/tools/skills)
