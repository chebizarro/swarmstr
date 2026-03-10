---
summary: "Location, voicewake, and node troubleshooting for swarmstr"
read_when:
  - Using GPS location from a node device
  - Configuring voice wake word detection
  - Troubleshooting node connectivity issues
title: "Location, VoiceWake & Node Troubleshooting"
---

# Location, VoiceWake & Node Troubleshooting

## Location

### GPS Location from Node

When a node device has GPS or location capabilities, the agent can request the current location:

```bash
# Get location from node
swarmstr nodes location get --node mypi

# With accuracy preference
swarmstr nodes location get --node mypi \
  --accuracy precise \
  --max-age 60000    # accept location up to 60 seconds old
```

### Location in Agent Context

The agent can use location data for:
- Providing local time/weather context
- Geofencing (alerting when device leaves an area)
- Location-based automation

### Example: Location-Based Cron

```bash
# Cron job that runs when device enters a specific area
swarmstr cron add \
  --name "arrive-home" \
  --system-event "Check if I've arrived home based on GPS location"
```

## VoiceWake

VoiceWake enables always-listening wake word detection on a node device.

### Setup

```bash
# Install porcupine (wake word engine)
pip install pvporcupine

# Or use openWakeWord
pip install openwakeword
```

Configure in the node service:

```json5
{
  "node": {
    "voicewake": {
      "enabled": true,
      "engine": "porcupine",    // "porcupine" | "openwakeword"
      "wakeWord": "hey swarmstr",
      "sensitivity": 0.5
    }
  }
}
```

### How It Works

1. Node listens for the wake word continuously (low CPU usage)
2. When detected, node triggers an agent turn via the gateway
3. Node records audio for 5 seconds (or until silence)
4. Audio is transcribed and sent as an agent turn
5. Agent responds via TTS on the node's speaker

### Agent Commands via VoiceWake

After waking, the user speaks naturally:

```
"Hey swarmstr, what's the weather in Berlin?"
→ Agent looks up weather, responds via TTS

"Hey swarmstr, add reminder to buy milk tomorrow"  
→ Agent creates a cron job, confirms via TTS
```

## Node Troubleshooting

### Node Not Appearing After Pair

```bash
# Check pending approvals
swarmstr nodes pending

# Approve the node
swarmstr nodes approve <requestId>

# Verify it's listed
swarmstr nodes list
```

### Node Disconnecting Frequently

```bash
# Check node logs
swarmstr node status

# On the node device
journalctl -u swarmstr-node -f
```

Common causes:
- Network instability between node and gateway
- Gateway HTTP server not accessible from node network
- Firewall blocking WebSocket connections

### Node Command Failures

```bash
# Test a simple command
swarmstr nodes run --node mypi echo "test"

# Check exec approvals on node
# SSH into the node and check:
cat ~/.swarmstr/exec-approvals.json
```

### Camera Not Working

```bash
# List cameras on node
swarmstr nodes camera list --node mypi

# Verify camera is accessible on the node itself
# SSH into node and test:
libcamera-still -o /tmp/test.jpg   # Raspberry Pi
v4l2-ctl --list-devices             # USB camera
```

### Audio Not Working

```bash
# Check audio devices on node
# SSH into node and test:
aplay -l   # List playback devices
arecord -l # List recording devices
pactl list sinks   # PulseAudio sinks
```

### Network Troubleshooting

```bash
# Verify node can reach gateway
ping <gateway-host>
curl http://<gateway-host>:18789/health

# For Tailscale setups
tailscale status
tailscale ping <gateway-host>
```

## See Also

- [Nodes Overview](/nodes/)
- [Audio & TTS](/nodes/audio)
- [Camera & Images](/nodes/camera)
- [Remote Access](/gateway/remote)
