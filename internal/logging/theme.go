package logging

import (
	"os"
	"strconv"

	"github.com/fatih/color"
)

// hasForceColor checks if FORCE_COLOR is explicitly set to enable colors
func hasForceColor() bool {
	val := os.Getenv("FORCE_COLOR")
	if val == "" {
		return false
	}
	if val == "0" {
		return false
	}
	return true
}

// shouldUseColor determines if we should output colors based on env vars
func shouldUseColor() bool {
	if os.Getenv("NO_COLOR") != "" && !hasForceColor() {
		return false
	}
	return true
}

// hexToRGB converts hex color string to RGB values
func hexToRGB(hex string) (int, int, int) {
	if len(hex) != 7 || hex[0] != '#' {
		return 255, 255, 255 // fallback to white
	}
	
	r, _ := strconv.ParseInt(hex[1:3], 16, 64)
	g, _ := strconv.ParseInt(hex[3:5], 16, 64)
	b, _ := strconv.ParseInt(hex[5:7], 16, 64)
	
	return int(r), int(g), int(b)
}

// createColorFunc creates a color function from hex string
func createColorFunc(hexColor string) func(string, ...interface{}) string {
	r, g, b := hexToRGB(hexColor)
	c := color.RGB(r, g, b)
	
	if !shouldUseColor() {
		c.DisableColor()
	}
	
	return c.Sprintf
}

// Theme provides themed color functions for logging
var Theme = struct {
	Accent       func(string, ...interface{}) string
	AccentBright func(string, ...interface{}) string
	AccentDim    func(string, ...interface{}) string
	Info         func(string, ...interface{}) string
	Success      func(string, ...interface{}) string
	Warn         func(string, ...interface{}) string
	Error        func(string, ...interface{}) string
	Muted        func(string, ...interface{}) string
	Heading      func(string, ...interface{}) string
	Command      func(string, ...interface{}) string
	Option       func(string, ...interface{}) string
}{
	Accent:       createColorFunc(CYBERWAVE_PALETTE.Accent),
	AccentBright: createColorFunc(CYBERWAVE_PALETTE.AccentBright),
	AccentDim:    createColorFunc(CYBERWAVE_PALETTE.AccentDim),
	Info:         createColorFunc(CYBERWAVE_PALETTE.Info),
	Success:      createColorFunc(CYBERWAVE_PALETTE.Success),
	Warn:         createColorFunc(CYBERWAVE_PALETTE.Warn),
	Error:        createColorFunc(CYBERWAVE_PALETTE.Error),
	Muted:        createColorFunc(CYBERWAVE_PALETTE.Muted),
	Heading: func() func(string, ...interface{}) string {
		r, g, b := hexToRGB(CYBERWAVE_PALETTE.Accent)
		c := color.RGB(r, g, b).Add(color.Bold)
		if !shouldUseColor() {
			c.DisableColor()
		}
		return c.Sprintf
	}(),
	Command: createColorFunc(CYBERWAVE_PALETTE.AccentBright),
	Option:  createColorFunc(CYBERWAVE_PALETTE.Warn),
}

// IsRich returns true if terminal supports color output
func IsRich() bool {
	return shouldUseColor()
}

// Colorize conditionally applies color based on rich terminal support
func Colorize(rich bool, colorFunc func(string, ...interface{}) string, value string) string {
	if rich {
		return colorFunc(value)
	}
	return value
}
