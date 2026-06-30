package commitments

import "time"

const (
	DefaultConfidenceThreshold = 0.72
	DefaultDailyLimit          = 3
	DefaultMaxPerHeartbeat     = 3
	DefaultDueWindow           = time.Hour
	DefaultBackoff             = 15 * time.Minute
)

// Config controls commitment extraction, persistence, and heartbeat delivery.
type Config struct {
	Enabled             bool
	ConfidenceThreshold float64
	DailyLimit          int
	MaxPerHeartbeat     int
	DueWindow           time.Duration
	AttemptBackoff      time.Duration
	StorePath           string
}

// DefaultConfig returns conservative defaults. Enabled defaults to true to keep
// the existing regex/LLM extraction behavior active unless callers opt out.
func DefaultConfig() Config {
	return Config{
		Enabled:             true,
		ConfidenceThreshold: DefaultConfidenceThreshold,
		DailyLimit:          DefaultDailyLimit,
		MaxPerHeartbeat:     DefaultMaxPerHeartbeat,
		DueWindow:           DefaultDueWindow,
		AttemptBackoff:      DefaultBackoff,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Enabled {
		d.Enabled = true
	}
	if c.ConfidenceThreshold > 0 {
		d.ConfidenceThreshold = c.ConfidenceThreshold
	}
	if c.DailyLimit > 0 {
		d.DailyLimit = c.DailyLimit
	}
	if c.MaxPerHeartbeat > 0 {
		d.MaxPerHeartbeat = c.MaxPerHeartbeat
	}
	if c.DueWindow > 0 {
		d.DueWindow = c.DueWindow
	}
	if c.AttemptBackoff > 0 {
		d.AttemptBackoff = c.AttemptBackoff
	}
	d.StorePath = c.StorePath
	return d
}
