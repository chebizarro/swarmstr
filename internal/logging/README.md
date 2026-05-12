# Metiq Logging Package 🌌

Cyberwave-themed logging with neon purple, electric blue, and vibrant colors.

## Color Palette

The **CYBERWAVE** theme features:

- **Accent** (`#B026FF`): Bright electric purple - for important highlights
- **AccentBright** (`#D580FF`): Lighter neon purple - for commands
- **AccentDim** (`#7A1CAC`): Deep dark purple - for subsystem prefixes
- **Info** (`#00D4FF`): Electric cyan/blue - for informational messages
- **Success** (`#39FF14`): Neon green - for success states
- **Warn** (`#FFD700`): Electric gold - for warnings
- **Error** (`#FF006E`): Hot neon pink - for errors
- **Muted** (`#9D9DAF`): Muted purple-gray - for debug/low-priority

## Usage

### Basic Logging

```go
import "metiq/internal/logging"

logging.LogInfo("Server started on port %d", 8080)
logging.LogSuccess("Connection established")
logging.LogWarn("Rate limit approaching")
logging.LogError("Failed to connect: %v", err)
logging.LogDebug("Processing request %s", reqID)
```

### Subsystem Prefixes

Messages with `subsystem: text` format get special treatment:

```go
logging.LogInfo("http: request received from %s", addr)
// Renders as: [http] request received from 192.168.1.1
// where [http] is in AccentDim and the message is in Info color
```

### Direct Theme Access

For custom formatting:

```go
import "metiq/internal/logging"

fmt.Println(logging.Theme.Accent("⚡ CRITICAL"))
fmt.Println(logging.Theme.Success("✓ Complete"))
```

### Migration from log.Printf

**Before:**
```go
log.Printf("server: listening on %s", addr)
log.Printf("ERROR: connection failed: %v", err)
```

**After:**
```go
logging.LogInfo("server: listening on %s", addr)
logging.LogError("connection failed: %v", err)
```

## Environment Variables

- `NO_COLOR`: Disable all colors (respects standard)
- `FORCE_COLOR`: Force colors even if NO_COLOR is set

## Comparison with OpenClaw

While OpenClaw uses a warm "lobster" palette (oranges, reds), Metiq embraces a cool, futuristic cyberwave aesthetic with purples, electric blues, and neon highlights - perfect for an AI agent runtime!

| OpenClaw (Lobster) | Metiq (Cyberwave) |
|-------------------|-------------------|
| 🦞 Warm reds/oranges | ⚡ Cool purples/blues |
| `#FF5A2D` accent | `#B026FF` accent |
| `#2FBF71` success | `#39FF14` success |
| `#FFB020` warn | `#FFD700` warn |
| `#E23D2D` error | `#FF006E` error |
