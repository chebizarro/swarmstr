package main

import (
	"encoding/json"
	"fmt"
	"os"

	"metiq/internal/logging"
)

// CLI Output Helpers with Cyberwave Theme
// These functions provide colored, themed output for the metiq CLI.

// ─── Styled Output ────────────────────────────────────────────────────────────

// printSuccess prints a success message in neon green
func printSuccess(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(logging.Theme.Success(msg))
}

// printInfo prints an info message in electric cyan
func printInfo(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(logging.Theme.Info(msg))
}

// printWarn prints a warning message in electric gold
func printWarn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(logging.Theme.Warn(msg))
}

// printError prints an error message in hot neon pink
func printError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, logging.Theme.Error(msg))
}

// printAccent prints a message in bright electric purple (for headers/emphasis)
func printAccent(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(logging.Theme.Accent(msg))
}

// printMuted prints a muted message in purple-gray (for secondary info)
func printMuted(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(logging.Theme.Muted(msg))
}

// printHeading prints a bold heading in electric purple
func printHeading(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(logging.Theme.Heading(msg))
}

// ─── Specialized Formatters ───────────────────────────────────────────────────

// printField prints a key-value pair with themed colors
// Key in muted, value in info
func printField(key string, value interface{}) {
	fmt.Printf("%s %s\n",
		logging.Theme.Muted(key+":"),
		logging.Theme.Info(fmt.Sprintf("%v", value)))
}

// printFieldSuccess prints a key-value pair with success coloring
func printFieldSuccess(key string, value interface{}) {
	fmt.Printf("%s %s\n",
		logging.Theme.Muted(key+":"),
		logging.Theme.Success(fmt.Sprintf("%v", value)))
}

// printFieldWarn prints a key-value pair with warning coloring
func printFieldWarn(key string, value interface{}) {
	fmt.Printf("%s %s\n",
		logging.Theme.Muted(key+":"),
		logging.Theme.Warn(fmt.Sprintf("%v", value)))
}

// printFieldError prints a key-value pair with error coloring
func printFieldError(key string, value interface{}) {
	fmt.Printf("%s %s\n",
		logging.Theme.Muted(key+":"),
		logging.Theme.Error(fmt.Sprintf("%v", value)))
}

// ─── Status & Icons ───────────────────────────────────────────────────────────

// statusIcon returns a colored icon based on status
func statusIcon(status string) string {
	switch status {
	case "success", "ok", "connected", "active", "running":
		return logging.Theme.Success("✓")
	case "error", "failed", "disconnected", "stopped":
		return logging.Theme.Error("✗")
	case "warning", "warn", "pending":
		return logging.Theme.Warn("⚠")
	case "info":
		return logging.Theme.Info("ℹ")
	default:
		return logging.Theme.Muted("•")
	}
}

// printStatus prints a status line with icon
func printStatus(status, message string) {
	fmt.Printf("%s %s\n", statusIcon(status), message)
}

// ─── Lists & Tables ───────────────────────────────────────────────────────────

// printListHeader prints a list section header
func printListHeader(title string) {
	fmt.Println(logging.Theme.Heading("━━━ " + title + " ━━━"))
}

// printListItem prints an item with a bullet point
func printListItem(text string) {
	fmt.Printf("%s %s\n", logging.Theme.AccentDim("•"), text)
}

// printListItemMuted prints a muted item
func printListItemMuted(text string) {
	fmt.Printf("%s %s\n", logging.Theme.Muted("•"), logging.Theme.Muted(text))
}

// ─── Banners & Branding ───────────────────────────────────────────────────────

// printBanner prints the metiq banner with cyberwave styling
func printBanner() {
	banner := `
    __  ___     __  _      
   /  |/  /__  / /_(_)___ _
  / /|_/ / _ \/ __/ / __ ~/
 / /  / /  __/ /_/ / /_/ / 
/_/  /_/\___/\__/_/\__, /  
                    /_/    
`
	fmt.Println(logging.Theme.Accent(banner))
	fmt.Println(logging.Theme.Info("  ⚡ AI Agent Runtime"))
	fmt.Println()
}

// printVersion prints version info with styling
func printVersion(version string) {
	fmt.Printf("%s %s\n",
		logging.Theme.Muted("metiq version"),
		logging.Theme.AccentBright(version))
}

// ─── JSON Output ──────────────────────────────────────────────────────────────

// printJSONStyled prints JSON with syntax highlighting (basic)
func printJSONStyled(data interface{}) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	// Simple JSON coloring - full syntax highlighting would be more complex
	fmt.Println(logging.Theme.Muted(string(b)))
	return nil
}

// printJSON is defined in cli_admin.go - using that version

// ─── Progress & Spinners ──────────────────────────────────────────────────────

// printProgress prints a progress message
func printProgress(message string) {
	fmt.Printf("%s %s\n",
		logging.Theme.AccentDim("⋯"),
		logging.Theme.Muted(message))
}

// ─── Separators ───────────────────────────────────────────────────────────────

// printSeparator prints a visual separator
func printSeparator() {
	fmt.Println(logging.Theme.Muted("────────────────────────────────────────"))
}

// printBlankLine prints a blank line (for spacing)
func printBlankLine() {
	fmt.Println()
}

// ─── Command/Option Formatting ────────────────────────────────────────────────

// printCommand formats a command for display
func printCommand(cmd string) string {
	return logging.Theme.Command(cmd)
}

// printOption formats an option/flag for display
func printOption(opt string) string {
	return logging.Theme.Option(opt)
}

// ─── Helper Functions ─────────────────────────────────────────────────────────

// colorize conditionally applies color
func colorize(useColor bool, text string, colorFunc func(string, ...interface{}) string) string {
	if useColor {
		return colorFunc(text)
	}
	return text
}

// stringField is defined in cli_admin.go - using that version
