package main

import (
	"fmt"
	"metiq/internal/logging"
)

func main() {
	fmt.Println("\n🌌 Metiq Cyberwave Logging Demo")
	
	// Accent colors
	fmt.Println(logging.Theme.Heading("━━━ Accent Colors ━━━"))
	fmt.Println(logging.Theme.Accent("⚡ Accent: Bright electric purple (#B026FF)"))
	fmt.Println(logging.Theme.AccentBright("⚡ AccentBright: Lighter neon purple (#D580FF)"))
	fmt.Println(logging.Theme.AccentDim("⚡ AccentDim: Deep dark purple (#7A1CAC)"))
	
	// Status colors
	fmt.Println("\n" + logging.Theme.Heading("━━━ Status Colors ━━━"))
	fmt.Println(logging.Theme.Info("ℹ Info: Electric cyan/blue (#00D4FF)"))
	fmt.Println(logging.Theme.Success("✓ Success: Neon green (#39FF14)"))
	fmt.Println(logging.Theme.Warn("⚠ Warn: Electric gold (#FFD700)"))
	fmt.Println(logging.Theme.Error("✗ Error: Hot neon pink (#FF006E)"))
	fmt.Println(logging.Theme.Muted("• Muted: Purple-gray (#9D9DAF)"))
	
	// Logging functions
	fmt.Println("\n" + logging.Theme.Heading("━━━ Logging Functions ━━━"))
	logging.LogInfo("Server started on port %d", 8080)
	logging.LogSuccess("Database connection established")
	logging.LogWarn("Memory usage at %d%%", 85)
	logging.LogError("Failed to load plugin: %s", "example-plugin")
	logging.LogDebug("Request ID: %s", "req-abc-123")
	
	// With subsystem prefixes
	fmt.Println("\n" + logging.Theme.Heading("━━━ Subsystem Prefixes ━━━"))
	logging.LogInfo("http: request received from 192.168.1.1:54321")
	logging.LogSuccess("db: migration v42 complete")
	logging.LogWarn("cache: eviction threshold reached")
	logging.LogError("plugin: initialization timeout")
	logging.LogDebug("mcp: tool registration complete")
	
	// Special formatting
	fmt.Println("\n" + logging.Theme.Heading("━━━ Special Formatting ━━━"))
	fmt.Println(logging.Theme.Command("$ metiq start"))
	fmt.Println(logging.Theme.Option("--model") + " claude-sonnet-4")
	fmt.Println(logging.Theme.Heading("⚙ Configuration"))
	
	fmt.Println("\n" + logging.Theme.Muted("Run with NO_COLOR=1 to disable colors"))
	fmt.Println(logging.Theme.Muted("Run with FORCE_COLOR=1 to force colors"))
}
