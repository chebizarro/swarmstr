---
summary: "Nodes: companion devices that extend metiq with audio, camera, and sensor capabilities"
read_when:
  - Pairing a device node to metiq
  - Using audio/camera/location from a companion device
  - Understanding the node protocol
title: "Nodes Overview"
---

# Nodes

A **node** is a companion device that connects to metiq to provide additional capabilities: audio input/output, camera, location, and other sensors.

Unlike openclaw (which has iOS/Android/macOS companion apps), metiq nodes are headless services that run on Linux devices. The primary use cases are:

- **Raspberry Pi**: audio in/out (TTS/STT), camera
- **Linux server**: remote exec target
- **IoT devices**: sensors, location

## Node Protocol

Nodes connect to the metiq daemon via WebSocket with `role: "node"`. They expose a command surface via `node.invoke`.

```
Node Device ──WebSocket──► metiqd
                             │
                        Node commands
                        (camera, audio,
                         location, exec)
```

## Pairing Nodes

```bash
# View pending pairing requests
metiq nodes pending

# Approve a pairing request
metiq nodes approve <requestId>

# Reject a pairing request
metiq nodes reject <requestId>

# Verify
metiq nodes status
```

## Available Node Commands

Once paired, the agent can invoke node commands via `metiq nodes invoke`.
The available commands depend on what capabilities the node host exposes.

### Audio / TTS

See [Audio & TTS](/nodes/audio).

### Camera

```bash
metiq nodes invoke --node <node-id> --command camera.snap
```

See [Camera](/nodes/camera).

### Location

```bash
metiq nodes invoke --node <node-id> --command location.get
```

See [Location](/nodes/location).

## Node CLI

```bash
# List registered nodes
metiq nodes list
metiq nodes status

# Describe a specific node
metiq nodes describe <node-id>

# Rename a node
metiq nodes rename <node-id> <new-name>

# Send a message to a node
metiq nodes send <node-id> <message>

# Invoke a command on a node
metiq nodes invoke --node <node-id> --command <cmd> [--args '{...}']

# Pairing management
metiq nodes pending
metiq nodes approve <request-id>
metiq nodes reject <request-id>
```

## Headless Node Host

A node host runs on a remote device and connects to the metiq gateway over WebSocket.
The node host registers capabilities (audio, camera, location, exec) and makes them
accessible via `metiq nodes invoke`.

The node host can be run via the `metiqd` binary on the remote device, or any
WebSocket client that speaks the node protocol.

## Security

- Nodes require pairing approval (`metiq nodes approve`)
- Commands execute with the node host's user permissions
- Exec approvals are enforced via `~/.metiq/exec-approvals.json` on the node
- All communication is over the local network or Tailscale (not public internet)

## See Also

- [Audio & TTS](/nodes/audio)
- [Camera & Images](/nodes/camera)
- [Location](/nodes/location)
- [Audio & TTS Providers](/nodes/audio)
- [Exec Tool](/tools/exec)
