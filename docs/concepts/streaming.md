---
summary: "Streaming responses and chunking for Nostr DMs in metiq"
read_when:
  - Understanding how metiq handles long or streaming responses
  - Configuring response chunking for Nostr DMs
title: "Streaming & Chunking"
---

# Streaming & Chunking

## Nostr DM Delivery

metiq streams responses from the model API but delivers to Nostr as complete messages (not partial streaming). This is intentional:

- Nostr events are immutable once published — you can't "update" a partially-sent event
- Sending partial content as multiple events creates a confusing reading experience
- The agent waits for the complete response before publishing the DM

## Response Chunking

For very long responses, metiq splits the reply into multiple Nostr DMs automatically. Chunks are numbered: `[1/3] First chunk...`, `[2/3] Second chunk...`, `[3/3] Final chunk.`

The chunk size is governed by Nostr event size limits — responses that would produce events over ~64KB are split.

## Internal Streaming

Although Nostr delivery is non-streaming, the model API is called in streaming mode internally:

1. LLM token stream is buffered internally
2. Tool calls are executed as they're identified (streaming tool use)
3. When the full response is ready, it's delivered as one Nostr DM

This gives faster tool execution while maintaining clean Nostr delivery.

Active-run steering happens inside this internal loop: additional user input is drained only after current tool results and before the next model call. Nostr output remains non-streaming for DMs, and inbound steering still arrives through normal event subscriptions rather than polling.

## Canvas Streaming

For the webchat and canvas, streaming is fully supported:

- Tokens stream to the browser via WebSocket as they arrive
- The canvas panel updates in real-time
- No chunking needed (WebSocket is stateful)

## Interruption and Steering Boundaries

- `/stop`, `/kill`, and `chat.abort` cancel the active turn context unconditionally.
- Queue mode `steer` does not publish partial output or start a concurrent turn. It enqueues local steering input and lets the active model loop consume it at the next safe boundary.
- Queue mode `interrupt` cancels only when no active tool is blocking. If any active tool is marked blocking, the newest input is deferred as urgent steering and injected at the next boundary or residual follow-up.

See [Active-Run Steering Architecture](/plan/active-run-steering-architecture).

## Loop Detection

metiq monitors for infinite tool-use loops. If the agent calls the same tool with the same parameters more than 3 times in a row:

1. The loop is detected and broken
2. The agent is told it appears to be looping
3. The current turn is completed without further tool calls

Loop detection is always active and requires no configuration.

## Retry Policy

For transient failures (network errors, relay disconnections), metiq retries:

- Model API calls: 3 retries with exponential backoff (1s, 2s, 4s)
- Nostr DM delivery: 3 retries, then drops and logs
- Relay reconnection: exponential backoff up to 5 minutes

## See Also

- [Nostr Channel](/channels/nostr)
- [Agent Loop](/concepts/agent-loop)
- [Canvas](/tools/canvas)
