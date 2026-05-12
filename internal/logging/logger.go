package logging

import (
	"fmt"
	"log"
	"regexp"
)

// subsystemPrefixRe matches "subsystem: message" pattern
var subsystemPrefixRe = regexp.MustCompile(`^([a-z][a-z0-9-]{1,20}):\s+(.*)$`)

// splitSubsystem extracts subsystem prefix from message if present
func splitSubsystem(message string) (subsystem string, rest string, ok bool) {
	matches := subsystemPrefixRe.FindStringSubmatch(message)
	if len(matches) != 3 {
		return "", "", false
	}
	return matches[1], matches[2], true
}

// formatWithSubsystem formats a message with optional subsystem prefix coloring
func formatWithSubsystem(message string, colorFunc func(string, ...interface{}) string) string {
	if subsystem, rest, ok := splitSubsystem(message); ok {
		// Color the subsystem prefix differently (using accent dim)
		return fmt.Sprintf("%s %s", Theme.AccentDim("[%s]", subsystem), colorFunc(rest))
	}
	return colorFunc(message)
}

// LogInfo logs an informational message in electric blue
func LogInfo(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	formatted := formatWithSubsystem(message, Theme.Info)
	log.Print(formatted)
}

// LogWarn logs a warning message in electric gold
func LogWarn(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	formatted := formatWithSubsystem(message, Theme.Warn)
	log.Print(formatted)
}

// LogSuccess logs a success message in neon green
func LogSuccess(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	formatted := formatWithSubsystem(message, Theme.Success)
	log.Print(formatted)
}

// LogError logs an error message in hot neon pink
func LogError(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	formatted := formatWithSubsystem(message, Theme.Error)
	log.Print(formatted)
}

// LogDebug logs a debug message in muted purple-gray
func LogDebug(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	formatted := formatWithSubsystem(message, Theme.Muted)
	log.Print(formatted)
}

// LogAccent logs a message in bright electric purple (for emphasis)
func LogAccent(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	formatted := formatWithSubsystem(message, Theme.Accent)
	log.Print(formatted)
}
