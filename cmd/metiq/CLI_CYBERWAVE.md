# 🌌 Metiq CLI - Cyberwave Theme

The Metiq CLI now features cyberwave-themed output with electric purples, neon blues, and vibrant colors throughout all commands.

## Overview

All CLI commands have been updated to use the new `cli_output.go` helper functions, which provide themed, color-coded output that matches the terminal logging and web UI aesthetics.

## Color Usage

### Status Messages
- **Success**: Neon green (#39FF14) - `printSuccess("✓ Operation complete")`
- **Info**: Electric cyan (#00D4FF) - `printInfo("Server started")`
- **Warning**: Electric gold (#FFD700) - `printWarn("⚠ Rate limit approaching")`
- **Error**: Hot neon pink (#FF006E) - `printError("✗ Connection failed")`

### UI Elements
- **Headings**: Bold electric purple - `printHeading("Section Title")`
- **Accent**: Bright purple - `printAccent("⚡ Important")`
- **Muted**: Purple-gray - `printMuted("Secondary info")`
- **Commands**: Bright purple - `printCommand("metiq start")`
- **Options**: Electric gold - `printOption("--bootstrap")`

## New Output Functions

### Basic Output

```go
printSuccess("✓ Daemon healthy")
printInfo("Server listening on :8080")
printWarn("⚠ Update available")
printError("✗ Connection failed")
printMuted("(Optional hint text)")
printAccent("⚡ Important notice")
printHeading("━━━ Configuration ━━━")
```

### Field Output

```go
printField("current", "v1.0.0")        // Key in muted, value in info
printFieldSuccess("status", "active")  // Value in success color
printFieldWarn("warning", "pending")   // Value in warning color
printFieldError("error", err)          // Value in error color
```

### Status Icons

```go
statusIcon("success")      // ✓ in neon green
statusIcon("error")        // ✗ in hot pink
statusIcon("warning")      // ⚠ in electric gold
statusIcon("info")         // ℹ in electric cyan
printStatus("success", "Database connected")  // Icon + message
```

### Lists & Structure

```go
printListHeader("Available Commands")  // Heading with separator
printListItem("config get")            // Bullet point item
printListItemMuted("(deprecated)")     // Muted bullet point
printSeparator()                       // Visual divider
printBlankLine()                       // Spacing
```

### Branding

```go
printBanner()              // ASCII art logo in cyberwave colors
printVersion("1.0.0")      // Styled version output
```

### Progress

```go
printProgress("Loading configuration...")
```

### JSON Output

```go
printJSON(data)            // Raw JSON for --json flag
printJSONStyled(data)      // Syntax-highlighted JSON
```

## Updated Commands

### ✅ Fully Updated

- **`metiq`** (usage/help) - Cyberwave banner and themed help text
- **`metiq version`** - Gradient version display
- **`metiq update`** - Colored status fields
- **`metiq health`** - Success/error icons
- **`metiq security audit`** - Severity-colored findings
- **`metiq init`** - Themed progress messages
- **`metiq qr`** - Colored field output
- **`metiq agents list`** - Colored table output

### 🔄 Pattern Established

The following files demonstrate the pattern and can be extended:

- `admin_ops_cmd.go` - Update, security, health commands
- `agents_plugins_cmd.go` - Agent listing with colors
- `init.go` - Workspace initialization
- `misc_cmd.go` - QR code display
- `cli_cmds.go` - Version command

### 📝 Recommended Migration

Other command files can follow the same pattern:

**Before:**
```go
fmt.Printf("current: %s\n", current)
fmt.Println("up to date")
```

**After:**
```go
printField("current", current)
printSuccess("✓ Up to date")
```

## Example Output

### `metiq` (help)

```
    __  ___     __  _      
   /  |/  /__  / /_(_)___ _
  / /|_/ / _ \/ __/ / __ ~/
 / /  / /  __/ /_/ / /_/ / 
/_/  /_/\___/\__/_/\__, /  
                    /_/    

  ⚡ AI Agent Runtime

metiq version 1.0.0

Usage: metiq <command> [flags]

━━━ Daemon Status ━━━
  status             show daemon status (pubkey, uptime, relays)
  health             ping daemon health endpoint
  ...
```

### `metiq update`

```
current: 1.0.0
latest:  1.1.0
⚡ Update available!
  Run: curl -fsSL https://raw.githubusercontent.com/metiq/metiq/main/scripts/install.sh | bash
```

### `metiq health`

```
✓ Daemon healthy
```

### `metiq security audit`

```
✗ [critical] exposed-token: Admin token exposed in environment
  → Move token to secrets store
! [warn] weak-password: Password strength below recommended
  → Use at least 16 characters with mixed case

⚠ 2 findings (1 critical, 1 warn)
```

### `metiq agents list`

```
ID      MODEL                   STATUS
main    claude-sonnet-4-5      active
helper  claude-opus-4          running
```

### `metiq init`

```
  ✓ wrote BOOTSTRAP.md
  ✓ wrote SOUL.md
  ✓ wrote IDENTITY.md
  ✓ wrote USER.md
  ✓ wrote AGENTS.md

⚡ Workspace initialised at: /Users/name/.metiq/workspace

Next steps:
  1. Edit SOUL.md     — define who your agent is
  2. Edit IDENTITY.md — name, vibe, emoji
  ...
```

## Implementation Details

### File Structure

```
cmd/metiq/
├── cli_output.go          # NEW: Cyberwave output helpers
├── admin_ops_cmd.go       # UPDATED: Uses themed output
├── agents_plugins_cmd.go  # UPDATED: Colored tables
├── cli_cmds.go            # UPDATED: Version command
├── init.go                # UPDATED: Workspace init
├── misc_cmd.go            # UPDATED: QR display
├── main.go                # UPDATED: Banner in usage()
└── ...                    # Other commands (can be migrated)
```

### Import Pattern

All command files that use themed output should import the logging package:

```go
import (
	"metiq/internal/logging"
)
```

The `cli_output.go` functions automatically use the logging theme, so you just call the functions directly.

### JSON Output Handling

Commands that support `--json` should preserve raw output:

```go
if jsonOut {
	return printJSON(result)  // Raw JSON, no colors
}

// Otherwise use themed output
printSuccess("✓ Complete")
```

## Environment Variables

The CLI respects the same environment variables as the logging package:

- `NO_COLOR` - Disable all colors
- `FORCE_COLOR` - Force colors even if NO_COLOR is set

## Migration Guide

### Step 1: Replace Basic Output

**Before:**
```go
fmt.Println("operation completed")
fmt.Printf("status: %s\n", status)
```

**After:**
```go
printSuccess("✓ Operation completed")
printField("status", status)
```

### Step 2: Use Status Icons

**Before:**
```go
if success {
	fmt.Println("✓ OK")
} else {
	fmt.Println("✗ Failed")
}
```

**After:**
```go
printStatus(status, message)
// or
if success {
	printSuccess("✓ OK")
} else {
	printError("✗ Failed")
}
```

### Step 3: Structure Output

**Before:**
```go
fmt.Println("\nConfiguration:")
fmt.Println("  key: value")
fmt.Println("  key2: value2")
```

**After:**
```go
printBlankLine()
printListHeader("Configuration")
printField("key", "value")
printField("key2", "value2")
```

### Step 4: Add Banner

**Before:**
```go
func usage() {
	fmt.Printf("metiq %s\n\n", version)
	fmt.Println("Usage: ...")
}
```

**After:**
```go
func usage() {
	printBanner()
	printVersion(version)
	printBlankLine()
	printInfo("Usage: %s", printCommand("metiq <command>"))
	// ...
}
```

## Benefits

✅ **Visual hierarchy** - Different message types instantly recognizable  
✅ **Consistent branding** - Matches logging and web UI  
✅ **Better UX** - Easier to scan output  
✅ **Professional appearance** - Modern, polished CLI  
✅ **Status clarity** - Icons and colors show status at a glance  
✅ **Easy migration** - Simple function calls replace fmt.Print*

## Testing

Test the CLI output:

```bash
# Basic commands
metiq
metiq version
metiq update
metiq health
metiq security audit

# With color disabled
NO_COLOR=1 metiq

# With JSON output
metiq agents list --json
```

## Future Enhancements

Potential additions:

- Progress bars with cyberwave styling
- Animated spinners
- Interactive prompts with color
- Diff output with syntax highlighting
- Table borders with custom characters

---

⚡ **The CLI experience is now as electric as the runtime itself!** 💜
