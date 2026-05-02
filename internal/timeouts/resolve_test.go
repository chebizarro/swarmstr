package timeouts

import (
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestSecsHelper(t *testing.T) {
	if got := secs(10, 30); got != 10*time.Second {
		t.Errorf("secs(10,30) = %v, want 10s", got)
	}
	if got := secs(0, 30); got != 30*time.Second {
		t.Errorf("secs(0,30) = %v, want 30s (fallback)", got)
	}
	if got := secs(-5, 30); got != 30*time.Second {
		t.Errorf("secs(-5,30) = %v, want 30s (fallback)", got)
	}
}

func TestTurnTimeout(t *testing.T) {
	tests := []struct {
		name       string
		ac         state.AgentConfig
		defaultS   int
		want       time.Duration
	}{
		{"zero uses default", state.AgentConfig{}, 180, 180 * time.Second},
		{"positive overrides", state.AgentConfig{TurnTimeoutSecs: 60}, 180, 60 * time.Second},
		{"negative means no timeout", state.AgentConfig{TurnTimeoutSecs: -1}, 180, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TurnTimeout(tt.ac, tt.defaultS); got != tt.want {
				t.Errorf("TurnTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaxAgenticIterations(t *testing.T) {
	if got := MaxAgenticIterations(state.AgentConfig{}); got != 0 {
		t.Errorf("empty config = %d, want 0", got)
	}
	if got := MaxAgenticIterations(state.AgentConfig{MaxAgenticIterations: 50}); got != 50 {
		t.Errorf("set to 50 = %d, want 50", got)
	}
	if got := MaxAgenticIterations(state.AgentConfig{MaxAgenticIterations: -1}); got != 0 {
		t.Errorf("negative = %d, want 0 (unset)", got)
	}
}

func TestGlobalTimeoutDefaults(t *testing.T) {
	zero := state.TimeoutsConfig{}

	cases := []struct {
		name string
		fn   func(state.TimeoutsConfig) time.Duration
		want time.Duration
	}{
		{"SessionMemoryExtraction", SessionMemoryExtraction, 600 * time.Second},
		{"SessionCompactSummary", SessionCompactSummary, 180 * time.Second},
		{"GrepSearch", GrepSearch, 60 * time.Second},
		{"ImageFetch", ImageFetch, 30 * time.Second},
		{"ToolChainExec", ToolChainExec, 120 * time.Second},
		{"GitOps", GitOps, 15 * time.Second},
		{"LLMProviderHTTP", LLMProviderHTTP, 600 * time.Second},
		{"WebhookWake", WebhookWake, 30 * time.Second},
		{"WebhookAgentStart", WebhookAgentStart, 120 * time.Second},
		{"SignerConnect", SignerConnect, 30 * time.Second},
		{"MemoryPersist", MemoryPersist, 30 * time.Second},
		{"SubagentDefault", SubagentDefault, 600 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/default", func(t *testing.T) {
			if got := tc.fn(zero); got != tc.want {
				t.Errorf("default = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGlobalTimeoutOverrides(t *testing.T) {
	cfg := state.TimeoutsConfig{
		SessionMemoryExtractionSecs: 90,
		SessionCompactSummarySecs:   60,
		GrepSearchSecs:              15,
		ImageFetchSecs:              10,
		ToolChainExecSecs:           300,
		GitOpsSecs:                  30,
		LLMProviderHTTPSecs:         240,
		WebhookWakeSecs:             45,
		WebhookAgentStartSecs:       180,
		SignerConnectSecs:           60,
		MemoryPersistSecs:           15,
		SubagentDefaultSecs:         120,
	}

	cases := []struct {
		name string
		fn   func(state.TimeoutsConfig) time.Duration
		want time.Duration
	}{
		{"SessionMemoryExtraction", SessionMemoryExtraction, 90 * time.Second},
		{"SessionCompactSummary", SessionCompactSummary, 60 * time.Second},
		{"GrepSearch", GrepSearch, 15 * time.Second},
		{"ImageFetch", ImageFetch, 10 * time.Second},
		{"ToolChainExec", ToolChainExec, 300 * time.Second},
		{"GitOps", GitOps, 30 * time.Second},
		{"LLMProviderHTTP", LLMProviderHTTP, 240 * time.Second},
		{"WebhookWake", WebhookWake, 45 * time.Second},
		{"WebhookAgentStart", WebhookAgentStart, 180 * time.Second},
		{"SignerConnect", SignerConnect, 60 * time.Second},
		{"MemoryPersist", MemoryPersist, 15 * time.Second},
		{"SubagentDefault", SubagentDefault, 120 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/override", func(t *testing.T) {
			if got := tc.fn(cfg); got != tc.want {
				t.Errorf("override = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCronJobExec(t *testing.T) {
	if got := CronJobExec(state.CronConfig{}); got != 300*time.Second {
		t.Errorf("default = %v, want 300s", got)
	}
	if got := CronJobExec(state.CronConfig{JobTimeoutSecs: 600}); got != 600*time.Second {
		t.Errorf("override = %v, want 600s", got)
	}
}
