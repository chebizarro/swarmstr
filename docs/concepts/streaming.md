---
summary: "Streaming responses and chunking for Nostr DMs in swarmstr"
read_when:
  - Understanding how swarmstr handles long or streaming responses
  - Configuring response chunking for Nostr DMs
title: "Streaming & Chunking"
---

# Streaming & Chunking

## Nostr DM Delivery

swarmstr streams responses from the model API but delivers to Nostr as complete messages (not partial streaming). This is intentional:

- Nostr events are immutable once published — you can't "update" a partially-sent event
- Sending partial content as multiple events creates a confusing reading experience
- The agent waits for the complete response before publishing the DM

## Response Chunking

For very long responses, swarmstr splits the reply into multiple Nostr DMs automatically. Chunks are numbered: `[1/3] First chunk...`, `[2/3] Second chunk...`, `[3/3] Final chunk.`

The chunk size is governed by Nostr event size limits — responses that would produce events over ~64KB are split.

## Internal Streaming

Although Nostr delivery is non-streaming, the model API is called in streaming mode internally:

1. LLM token stream is buffered internally
2. Tool calls are executed as they're identified (streaming tool use)
3. When the full response is ready, it's delivered as one Nostr DM

This gives faster tool execution while maintaining clean Nostr delivery.

## Canvas Streaming

For the webchat and canvas, streaming is fully supported:

- Tokens stream to the browser via WebSocket as they arrive
- The canvas panel updates in real-time
- No chunking needed (WebSocket is stateful)

## Loop Detection

swarmstr monitors for infinite tool-use loops. If the agent calls the same tool with the same parameters more than 3 times in a row:

1. The loop is detected and broken
2. The agent is told it appears to be looping
3. The current turn is completed without further tool calls

Loop detection is always active and requires no configuration.

## Retry Policy

For transient failures (network errors, relay disconnections), swarmstr retries:

- Model API calls: 3 retries with exponential backoff (1s, 2s, 4s)
- Nostr DM delivery: 3 retries, then drops and logs
- Relay reconnection: exponential backoff up to 5 minutes

## See Also

- [Nostr Channel](/channels/nostr)
- [Agent Loop](/concepts/agent-loop)
- [Canvas](/tools/canvas)
