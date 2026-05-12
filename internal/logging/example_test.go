package logging_test

import (
	"fmt"
	"testing"

	"metiq/internal/logging"
)

// Example_cyberwave demonstrates the cyberwave color theme
func Example_cyberwave() {
	// Direct theme usage
	fmt.Println(logging.Theme.Accent("⚡ Metiq Agent Runtime"))
	fmt.Println(logging.Theme.Info("🌐 Server listening on :8080"))
	fmt.Println(logging.Theme.Success("✓ Connection established"))
	fmt.Println(logging.Theme.Warn("⚠ Rate limit: 90%"))
	fmt.Println(logging.Theme.Error("✗ Failed to connect"))
	fmt.Println(logging.Theme.Muted("• Debug: processing request"))
	
	// With subsystem prefixes
	logging.LogInfo("http: request received from 192.168.1.1")
	logging.LogSuccess("db: migration complete")
	logging.LogWarn("cache: memory usage high")
	logging.LogError("plugin: initialization failed")
}

// Example_migration shows how to migrate from log.Printf
func Example_migration() {
	// Before: log.Printf("server: listening on %s", addr)
	logging.LogInfo("server: listening on %s", ":8080")
	
	// Before: log.Printf("ERROR: connection failed: %v", err)
	logging.LogError("connection failed: %v", "timeout")
	
	// Before: log.Printf("WARNING: rate limit approaching")
	logging.LogWarn("rate limit approaching")
	
	// Before: log.Printf("DEBUG: processing request %s", id)
	logging.LogDebug("processing request %s", "req-123")
}

func TestPaletteValues(t *testing.T) {
	// Verify all colors are defined
	if logging.CYBERWAVE_PALETTE.Accent == "" {
		t.Error("Accent color not defined")
	}
	if logging.CYBERWAVE_PALETTE.Info == "" {
		t.Error("Info color not defined")
	}
	if logging.CYBERWAVE_PALETTE.Success == "" {
		t.Error("Success color not defined")
	}
	if logging.CYBERWAVE_PALETTE.Warn == "" {
		t.Error("Warn color not defined")
	}
	if logging.CYBERWAVE_PALETTE.Error == "" {
		t.Error("Error color not defined")
	}
}

func TestColorFunctions(t *testing.T) {
	// Verify theme functions work
	result := logging.Theme.Accent("test")
	if result == "" {
		t.Error("Theme.Accent returned empty string")
	}
	
	result = logging.Theme.Info("test")
	if result == "" {
		t.Error("Theme.Info returned empty string")
	}
}
