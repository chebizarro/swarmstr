---
summary: "Nodes: companion devices that extend swarmstr with audio, camera, and sensor capabilities"
read_when:
  - Pairing a device node to swarmstr
  - Using audio/camera/location from a companion device
  - Understanding the node protocol
title: "Nodes Overview"
---

# Nodes

A **node** is a companion device that connects to swarmstr to provide additional capabilities: audio input/output, camera, location, and other sensors.

Unlike openclaw (which has iOS/Android/macOS companion apps), swarmstr nodes are headless services that run on Linux devices. The primary use cases are:

- **Raspberry Pi**: audio in/out (TTS/STT), camera
- **Linux server**: remote exec target
- **IoT devices**: sensors, location

## Node Protocol

Nodes connect to the swarmstr daemon via WebSocket with `role: "node"`. They expose a command surface via `node.invoke`.

```
Node Device ──WebSocket──► swarmstrd
                             │
                        Node commands
                        (camera, audio,
                         location, exec)
```

## Pairing Nodes

```bash
# On the node device, start the node host
swarmstr node run --host <gateway-host> --port 18789

# On the gateway, approve the pairing request
swarmstr nodes pending
swarmstr nodes approve <requestId>

# Verify
swarmstr nodes status
```

## Available Node Commands

Once paired, the agent can invoke node commands:

### Audio / TTS

```bash
# The agent uses the sherpa-onnx-tts skill to generate audio
# and delivers it via the node's audio output
```

See [Audio & TTS](/nodes/audio).

### Camera

```bash
# Capture a photo from the node's camera
swarmstr nodes camera snap --node <name>
```

The agent can request a camera snapshot for visual context. See [Camera](/nodes/camera).

### System Commands

```bash
# Run a command on the node
swarmstr nodes run --node <name> ls -la /home
```

This is useful when the gateway runs on a different machine from the target system.

### Location

```bash
# Get GPS location from the node
swarmstr nodes location get --node <name>
```

See [Location](/nodes/location).

## Node CLI

```bash
# List connected nodes
swarmstr nodes status
swarmstr nodes list

# Describe a specific node
swarmstr nodes describe --node <id|name>

# Run a command on a node
swarmstr nodes run --node <name> <command>

# Camera
swarmstr nodes camera list --node <name>
swarmstr nodes camera snap --node <name>

# Location
swarmstr nodes location get --node <name>
```

## Node Service Management

```bash
# Install node service on a device
swarmstr node install --host <gateway-host>

# Start/stop/restart
swarmstr node start
swarmstr node stop
swarmstr node restart
swarmstr node status
```

## Headless Node Host

Run a headless node host that exposes the current machine's capabilities to the gateway:

```bash
swarmstr node run --host <gateway-ip-or-hostname> --port 18789
```

This is useful for:
- Routing `exec` calls to a specific machine
- Providing audio/camera from a remote device
- Separating the gateway from compute nodes

## Security

- Nodes require pairing approval (`swarmstr nodes approve`)
- Commands execute with the node host's user permissions
- Exec approvals are enforced via `~/.swarmstr/exec-approvals.json` on the node
- All communication is over the local network or Tailscale (not public internet)

## See Also

- [Audio & TTS](/nodes/audio)
- [Camera & Images](/nodes/camera)
- [Location](/nodes/location)
- [Skills: sherpa-onnx-tts](/tools/skills)
- [Exec Tool](/tools/exec)
