# ACK Execution Fast Path

When users send short approval messages like "ok", "do it", or "go ahead", agents often waste a turn restating the plan instead of acting. The ACK Execution Fast Path detects these approval patterns and injects an instruction telling the agent to skip recaps and act immediately.

## Problem

User: "Can you refactor the authentication module?"
Agent: "I'll refactor the authentication module by..."
User: "ok"
Agent: "Great! So as I mentioned, I'll refactor the authentication module by first analyzing the current structure, then..."  ❌ **Wasted turn!**

## Solution

When the user message matches a known approval pattern, we inject this instruction into the turn context:

> "The latest user message is a short approval to proceed. Do not recap or restate the plan. Start with the first concrete tool action immediately. Keep any user-facing follow-up brief and natural."

## Supported Patterns

The fast path triggers on these normalized patterns (case-insensitive, punctuation-stripped):

### English
- `ok`, `okay`, `do it`, `go ahead`, `please do`, `sounds good`, `ship it`, `fix it`
- `make it so`, `yes`, `yep`, `yeah`, `sure`, `proceed`, `continue`, `approved`
- `lgtm`, `go`, `go for it`, `yes please`, `k`, `kk`, `yup`, `aye`, `bet`, `word`
- `lets go`, `send it`, `run it`

### Arabic
- `تمام`, `حسنا`, `حسنًا`, `امض قدما`, `نفذها`

### German
- `mach es`, `leg los`, `los geht s`, `weiter`

### Japanese
- `やって`, `進めて`, `そのまま進めて`

### French
- `allez y`, `vas y`, `fais le`, `continue`

### Spanish
- `hazlo`, `adelante`, `sigue`

### Portuguese
- `faz isso`, `vai em frente`, `pode fazer`

### Korean
- `해줘`, `진행해`, `계속해`

## Non-Triggers

The fast path does NOT trigger when:
- Message is empty or over 80 characters
- Message contains a newline
- Message contains a question mark (e.g., "ok?")
- Message has trailing content beyond the approval (e.g., "ok but wait")

## Integration

The fast path is integrated into `buildAgentRunTurn()` in `cmd/metiqd/memory_prompt.go`. When an ACK prompt is detected, the instruction is prepended to the turn context.

## API

```go
// Check if text is an approval prompt
agent.IsAckExecutionPrompt("ok")  // true
agent.IsAckExecutionPrompt("ok but wait")  // false

// Get the instruction (empty if not an ACK)
instruction := agent.GetAckFastPathInstruction("go ahead")
// Returns: "The latest user message is a short approval..."
```

## Related

- [Commitment Guard](./commitment-guard.md) - Detects unbacked promises
- [Planning-Only Detection](./planning-only-detection.md) - Detects turns with only planning text
