# ⚡ Cyberwave Logging Theme

## Overview

The Metiq logging package brings a futuristic cyberwave aesthetic to your logs with electric purples, neon blues, and vibrant colors - inspired by cyberpunk and synthetic intelligence.

## Color Palette 🎨

```
#B026FF  ⚡ Bright Electric Purple   [Accent]
#D580FF  💜 Lighter Neon Purple      [AccentBright]
#7A1CAC  🔮 Deep Dark Purple         [AccentDim]
#00D4FF  💎 Electric Cyan/Blue       [Info]
#39FF14  💚 Neon Green               [Success]
#FFD700  💛 Electric Gold            [Warn]
#FF006E  💗 Hot Neon Pink            [Error]
#9D9DAF  ⚪ Muted Purple-Gray        [Muted]
```

## Quick Start

```go
import "metiq/internal/logging"

// Basic logging
logging.LogInfo("Server started on port %d", 8080)
logging.LogSuccess("Database connected")
logging.LogWarn("Memory usage: %d%%", 85)
logging.LogError("Connection failed: %v", err)
logging.LogDebug("Request ID: %s", reqID)

// With subsystem prefixes
logging.LogInfo("http: request received")
logging.LogSuccess("db: migration complete")
logging.LogWarn("cache: eviction triggered")
```

## Visual Examples

When you run metiq, you'll see:

```
⚡ Metiq Agent Runtime v1.0
🌐 Server listening on :8080
✓ Database connection established
⚠ Rate limit: 90%
✗ Plugin initialization failed
```

Each line rendered in its signature cyberwave color:
- First line: **Bright electric purple** (#B026FF)
- Second line: **Electric cyan** (#00D4FF)  
- Third line: **Neon green** (#39FF14)
- Fourth line: **Electric gold** (#FFD700)
- Fifth line: **Hot neon pink** (#FF006E)

## Demo

Run the interactive demo to see all colors:

```bash
go run cmd/logging-demo/main.go
```

## Architecture

### Files
- `palette.go` - Color hex definitions
- `theme.go` - Color function generators using fatih/color
- `logger.go` - Wrapped logging functions
- `README.md` - Documentation
- `MIGRATION.md` - Migration guide from log.Printf
- `THEME_COMPARISON.md` - Comparison with OpenClaw's lobster theme

### Design Pattern
Inspired by OpenClaw's logging architecture:
1. **Palette** - Central color definitions
2. **Theme** - Color function factory
3. **Logger** - Semantic logging functions

## Environment Variables

- `NO_COLOR` - Disable colors (standard convention)
- `FORCE_COLOR` - Force colors even if NO_COLOR is set

## Why Cyberwave?

Metiq is an AI agent runtime - it lives in the digital realm, processes synthetic intelligence, and orchestrates complex distributed workflows. The cyberwave theme reflects this futuristic, high-tech nature:

- **Purple** - Synthetic intelligence, neural networks
- **Electric Blue** - Digital signals, data flow
- **Neon Green** - Success states, active processes
- **Hot Pink** - Critical alerts, system errors
- **Electric Gold** - Warnings, caution zones

It's not just colored logs - it's a visual identity that says "advanced AI infrastructure."

## Comparison to OpenClaw

| Aspect | OpenClaw 🦞 | Metiq ⚡ |
|--------|------------|---------|
| Theme | Lobster (warm) | Cyberwave (cool) |
| Primary | Orange/Red | Purple/Blue |
| Vibe | Friendly, approachable | Futuristic, powerful |
| Use case | User-facing tools | AI agent runtime |

## Next Steps

1. **Try the demo**: `go run cmd/logging-demo/main.go`
2. **Read migration guide**: See `MIGRATION.md`
3. **Start using**: Import and replace `log.Printf` calls
4. **Enjoy the glow**: Watch your logs light up in cyberwave! ⚡💜

---

Built with inspiration from OpenClaw's excellent logging architecture.
Themed for the future of AI agent infrastructure.
