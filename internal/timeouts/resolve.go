// Package timeouts provides centralised timeout resolution from config.
// Every public function accepts a TimeoutsConfig (value, not pointer) and returns
// a time.Duration, falling back to a built-in default when the config value is 0.
package timeouts

import (
	"time"

	"metiq/internal/store/state"
)

// ── helpers ─────────────────────────────────────────────────────────────────

func secs(v, fallback int) time.Duration {
	if v > 0 {
		return time.Duration(v) * time.Second
	}
	return time.Duration(fallback) * time.Second
}

// ── per-agent resolvers ─────────────────────────────────────────────────────

// TurnTimeout returns the effective turn timeout for an agent.
// Uses the per-agent TurnTimeoutSecs, falling back to defaultSecs.
func TurnTimeout(ac state.AgentConfig, defaultSecs int) time.Duration {
	if ac.TurnTimeoutSecs > 0 {
		return time.Duration(ac.TurnTimeoutSecs) * time.Second
	}
	if ac.TurnTimeoutSecs < 0 {
		// Negative = no timeout (caller should skip context.WithTimeout).
		return 0
	}
	return secs(0, defaultSecs)
}

// MaxAgenticIterations returns the configured per-agent max iterations,
// or 0 if unset (caller should fall back to model-tier default).
func MaxAgenticIterations(ac state.AgentConfig) int {
	if ac.MaxAgenticIterations > 0 {
		return ac.MaxAgenticIterations
	}
	return 0
}

// ── global timeout resolvers ────────────────────────────────────────────────

func SessionMemoryExtraction(t state.TimeoutsConfig) time.Duration {
	return secs(t.SessionMemoryExtractionSecs, 600)
}

func SessionCompactSummary(t state.TimeoutsConfig) time.Duration {
	return secs(t.SessionCompactSummarySecs, 180)
}

func GrepSearch(t state.TimeoutsConfig) time.Duration {
	return secs(t.GrepSearchSecs, 60)
}

func ImageFetch(t state.TimeoutsConfig) time.Duration {
	return secs(t.ImageFetchSecs, 30)
}

func ToolChainExec(t state.TimeoutsConfig) time.Duration {
	return secs(t.ToolChainExecSecs, 120)
}

func GitOps(t state.TimeoutsConfig) time.Duration {
	return secs(t.GitOpsSecs, 15)
}

func LLMProviderHTTP(t state.TimeoutsConfig) time.Duration {
	return secs(t.LLMProviderHTTPSecs, 600)
}

func WebhookWake(t state.TimeoutsConfig) time.Duration {
	return secs(t.WebhookWakeSecs, 30)
}

func WebhookAgentStart(t state.TimeoutsConfig) time.Duration {
	return secs(t.WebhookAgentStartSecs, 120)
}

func SignerConnect(t state.TimeoutsConfig) time.Duration {
	return secs(t.SignerConnectSecs, 30)
}

func MemoryPersist(t state.TimeoutsConfig) time.Duration {
	return secs(t.MemoryPersistSecs, 30)
}

func SubagentDefault(t state.TimeoutsConfig) time.Duration {
	return secs(t.SubagentDefaultSecs, 600)
}

func CronJobExec(cfg state.CronConfig) time.Duration {
	return secs(cfg.JobTimeoutSecs, 300)
}
