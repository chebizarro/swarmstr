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
# View pending pairing requests
swarmstr nodes pending

# Approve a pairing request
swarmstr nodes approve <requestId>

# Reject a pairing request
swarmstr nodes reject <requestId>

# Verify
swarmstr nodes status
```

## Available Node Commands

Once paired, the agent can invoke node commands via `swarmstr nodes invoke`.
The available commands depend on what capabilities the node host exposes.

### Audio / TTS

See [Audio & TTS](/nodes/audio).

### Camera

```bash
swarmstr nodes invoke --node <node-id> --command camera.snap
```

See [Camera](/nodes/camera).

### Location

```bash
swarmstr nodes invoke --node <node-id> --command location.get
```

See [Location](/nodes/location).

## Node CLI

```bash
# List registered nodes
swarmstr nodes list
swarmstr nodes status

# Describe a specific node
swarmstr nodes describe <node-id>

# Rename a node
swarmstr nodes rename <node-id> <new-name>

# Send a message to a node
swarmstr nodes send <node-id> <message>

# Invoke a command on a node
swarmstr nodes invoke --node <node-id> --command <cmd> [--args '{...}']

# Pairing management
swarmstr nodes pending
swarmstr nodes approve <request-id>
swarmstr nodes reject <request-id>
```

## Headless Node Host

A node host runs on a remote device and connects to the swarmstr gateway over WebSocket.
The node host registers capabilities (audio, camera, location, exec) and makes them
accessible via `swarmstr nodes invoke`.

The node host can be run via the `swarmstrd` binary on the remote device, or any
WebSocket client that speaks the node protocol.

## Security

- Nodes require pairing approval (`swarmstr nodes approve`)
- Commands execute with the node host's user permissions
- Exec approvals are enforced via `~/.swarmstr/exec-approvals.json` on the node
- All communication is over the local network or Tailscale (not public internet)

## See Also

- [Audio & TTS](/nodes/audio)
- [Camera & Images](/nodes/camera)
- [Location](/nodes/location)
- [Audio & TTS Providers](/nodes/audio)
- [Exec Tool](/tools/exec)
