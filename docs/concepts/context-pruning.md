# Context Pruning

Long agent sessions accumulate tool results that can overflow the context window. Context pruning automatically trims old tool results when context exceeds thresholds, preserving recent context while freeing space.

## Two-Phase Pruning

### Phase 1: Soft Trimming

When context exceeds the soft trim ratio (default 30%), large tool results are truncated:
- Keep the first N characters (head)
- Keep the last N characters (tail)
- Replace the middle with "..."
- Append a note indicating truncation

**Example:**
```
HEADER...first 1500 chars...
...
...last 1500 chars...FOOTER

[Tool result trimmed: kept first 1500 chars and last 1500 chars of 50000 chars.]
```

### Phase 2: Micro-Compaction (Hard Clear)

If soft trimming isn't sufficient and context exceeds the hard clear ratio (default 50%), entire tool results are replaced with a placeholder:

```
[tool result cleared to free context]
```

## Protected Regions

Pruning respects two protected regions:

1. **Pre-User Messages**: Messages before the first user message (system prompts, identity reads) are never pruned
2. **Recent Assistants**: The last N assistant messages (default 3) and their associated tool results are protected

## Configuration

```go
cfg := DefaultContextPruningConfig()
// Returns:
// - Enabled: true
// - KeepLastAssistants: 3
// - SoftTrimRatio: 0.3 (30%)
// - HardClearRatio: 0.5 (50%)
// - MinPrunableChars: 50,000
// - SoftTrim.MaxChars: 4,000
// - SoftTrim.HeadChars: 1,500
// - SoftTrim.TailChars: 1,500
// - HardClear.Enabled: true
// - HardClear.Placeholder: "[Old tool result content cleared]"
```

## Prunable Tools

By default, only read-like tools are pruned:
- `read`, `cat`, `head`, `tail`, `grep`, `find`, `ls`, `dir`
- `search`, `glob`, `list`, `show`, `get`, `fetch`, `view`
- `describe`, `inspect`, `status`, `log`, `diff`

Mutating tools (`write`, `execute`, `publish`, etc.) are never pruned.

### Custom Tool Lists

```go
cfg := DefaultContextPruningConfig()
cfg.ToolAllowList = []string{"read_file", "grep"}  // Only these tools
cfg.ToolDenyList = []string{"read_important"}       // Exclude specific tools
```

## Integration

Context pruning is integrated into the agentic loop in `agentic_loop.go`:

1. **Before each LLM call**: Soft trim runs first
2. **If still over budget**: Micro-compaction clears oldest results
3. **Pre-flight guards**: Final enforcement before API call

## API

```go
// Check if a tool's results can be pruned
IsToolPrunable("read_file", cfg)  // true
IsToolPrunable("bash_exec", cfg)  // false

// Soft trim a single text
trimmed := PruneToolResultText(largeContent, cfg.SoftTrim)

// Soft trim all LLM messages
result := SoftTrimLLMMessagesCopy(messages, cfg.SoftTrim, contextWindowTokens)

// Full pruning pass on abstract messages
result := PruneContextMessages(messages, contextWindowTokens, cfg)
```

## Related

- [Commitment Guard](./commitment-guard.md) - Detects unbacked promises
- [ACK Fast Path](./ack-fast-path.md) - Handles approval prompts
