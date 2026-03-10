---
summary: "Audio input/output and TTS for swarmstr nodes"
read_when:
  - Adding voice capabilities to swarmstr
  - Configuring sherpa-onnx for TTS
  - Setting up STT (speech-to-text) on a node device
title: "Audio & TTS"
---

# Audio & TTS

swarmstr supports text-to-speech (TTS) and speech-to-text (STT) via node devices and skills.

## Text-to-Speech (TTS)

### sherpa-onnx TTS Skill

The primary TTS mechanism is the `sherpa-onnx-tts` skill, which uses on-device ONNX models for offline TTS.

**Setup:**

```bash
# Install sherpa-onnx
pip install sherpa-onnx

# Download a TTS model (e.g., VITS)
wget https://huggingface.co/csukuangfj/vits-piper-en_US-amy-low/...
```

Configure in the skill's `SKILL.md`:

```yaml
metadata:
  openclaw:
    name: sherpa-onnx-tts
    description: "Offline TTS via sherpa-onnx"
    requires:
      bins: ["sherpa-onnx-tts"]
```

**Usage:**

The agent calls the TTS skill to speak a message via the connected node's audio output.

### Cloud TTS Providers

For cloud-based TTS, configure OpenAI TTS or another provider:

```json5
{
  "tools": {
    "tts": {
      "provider": "openai",
      "voice": "nova",
      "model": "tts-1"
    }
  }
}
```

## Speech-to-Text (STT)

### Whisper STT

The agent can transcribe audio from node devices using OpenAI Whisper:

```json5
{
  "tools": {
    "stt": {
      "provider": "openai",
      "model": "whisper-1"
    }
  }
}
```

### Local Whisper (whisper.cpp)

For offline STT:

```bash
# Install whisper.cpp
git clone https://github.com/ggerganov/whisper.cpp
cd whisper.cpp
make

# Download model
bash ./models/download-ggml-model.sh base.en
```

Configure:

```json5
{
  "tools": {
    "stt": {
      "provider": "whisper.cpp",
      "binary": "~/.local/bin/whisper-cli",
      "model": "~/.whisper/ggml-base.en.bin"
    }
  }
}
```

## Node Audio Commands

When a node with audio capabilities is paired:

```bash
# Play TTS on the node
swarmstr nodes invoke --node mypi --command audio.tts \
  --params '{"text": "Hello from swarmstr", "voice": "amy"}'

# Record from node microphone
swarmstr nodes invoke --node mypi --command audio.record \
  --params '{"duration": 5000}'
```

## Voice Wake Word

For always-listening wake word detection (e.g., "Hey swarmstr"):

See [Location & VoiceWake](/nodes/location) for voicewake configuration.

## Audio in the Agent

When audio input is received (from a user's voice message or node microphone):

1. The audio file is received (Nostr media attachment or node audio stream)
2. STT transcribes the audio to text
3. The agent processes the transcribed text as a normal turn
4. The agent can optionally respond with TTS audio

## Configuration Reference

```json5
{
  "nodes": {
    "audio": {
      "tts": {
        "provider": "sherpa-onnx",   // "sherpa-onnx" | "openai" | "whisper.cpp"
        "defaultVoice": "en_US-amy-low"
      },
      "stt": {
        "provider": "whisper.cpp",
        "model": "base.en"
      }
    }
  }
}
```

## See Also

- [Nodes Overview](/nodes/)
- [Skills](/tools/skills)
- [TTS](/tts)
