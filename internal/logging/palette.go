package logging

// CYBERWAVE_PALETTE defines the neon/electric color scheme for metiq logs.
// Purple, electric blue, neon, cyberwave aesthetic.
var CYBERWAVE_PALETTE = struct {
	Accent       string // Bright electric purple
	AccentBright string // Lighter neon purple
	AccentDim    string // Deep dark purple
	Info         string // Electric cyan/blue
	Success      string // Neon green
	Warn         string // Electric gold
	Error        string // Hot neon pink
	Muted        string // Muted purple-gray
}{
	Accent:       "#B026FF",
	AccentBright: "#D580FF",
	AccentDim:    "#7A1CAC",
	Info:         "#00D4FF",
	Success:      "#39FF14",
	Warn:         "#FFD700",
	Error:        "#FF006E",
	Muted:        "#9D9DAF",
}
