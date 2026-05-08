package agent

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type failingProvider struct {
	err error
}

func (p *failingProvider) Chat(_ context.Context, _ []LLMMessage, _ []ToolDefinition, _ ChatOptions) (*LLMResponse, error) {
	return nil, p.err
}

type succeedingProvider struct {
	content string
}

func (p *succeedingProvider) Chat(_ context.Context, _ []LLMMessage, _ []ToolDefinition, _ ChatOptions) (*LLMResponse, error) {
	return &LLMResponse{Content: p.content}, nil
}

func TestFallbackChain_FirstSucceeds(t *testing.T) {
	fc := NewFallbackChain([]FallbackCandidate{
		{Name: "primary", Provider: &succeedingProvider{content: "primary response"}},
		{Name: "backup", Provider: &succeedingProvider{content: "backup response"}},
	}, nil)

	resp, err := fc.Chat(context.Background(), nil, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "primary response" {
		t.Errorf("got %q, want %q", resp.Content, "primary response")
	}
}

func TestFallbackChain_FallsBackOnError(t *testing.T) {
	fc := NewFallbackChain([]FallbackCandidate{
		{Name: "primary", Provider: &failingProvider{err: fmt.Errorf("429 rate limit")}},
		{Name: "backup", Provider: &succeedingProvider{content: "backup response"}},
	}, nil)

	resp, err := fc.Chat(context.Background(), nil, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "backup response" {
		t.Errorf("got %q, want %q", resp.Content, "backup response")
	}
}

type cacheProfileCapturingProvider struct {
	profile PromptCacheProfile
	content string
	err     error
	opts    []ChatOptions
}

func (p *cacheProfileCapturingProvider) PromptCacheProfile() PromptCacheProfile {
	return p.profile
}

func (p *cacheProfileCapturingProvider) Chat(_ context.Context, _ []LLMMessage, _ []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	p.opts = append(p.opts, opts)
	if p.err != nil {
		return nil, p.err
	}
	return &LLMResponse{Content: p.content}, nil
}

func TestFallbackChain_RecomputesNativeCacheOptionsPerCandidate(t *testing.T) {
	primary := &cacheProfileCapturingProvider{
		profile: PromptCacheProfile{Enabled: true, Backend: PromptCacheBackendVLLM, DynamicContextPlacement: DynamicContextPlacementLateUser},
		err:     fmt.Errorf("429 rate limit"),
	}
	backup := &cacheProfileCapturingProvider{
		profile: PromptCacheProfile{Enabled: true, UseAnthropicCacheControl: true, DynamicContextPlacement: DynamicContextPlacementSystem},
		content: "backup response",
	}
	fc := NewFallbackChain([]FallbackCandidate{
		{Name: "primary", Provider: primary},
		{Name: "backup", Provider: backup},
	}, nil)

	resp, err := fc.Chat(context.Background(), nil, nil, ChatOptions{CacheSystem: false, CacheTools: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "backup response" {
		t.Fatalf("got %q, want backup response", resp.Content)
	}
	if len(primary.opts) != 1 || primary.opts[0].CacheSystem || primary.opts[0].CacheTools {
		t.Fatalf("expected primary vLLM candidate to keep Anthropic cache flags disabled, got %#v", primary.opts)
	}
	if len(backup.opts) != 1 || !backup.opts[0].CacheSystem || !backup.opts[0].CacheTools {
		t.Fatalf("expected backup Anthropic candidate to re-enable native cache flags, got %#v", backup.opts)
	}
}

func TestFallbackChain_AllFail(t *testing.T) {
	fc := NewFallbackChain([]FallbackCandidate{
		{Name: "a", Provider: &failingProvider{err: fmt.Errorf("429 rate limit")}},
		{Name: "b", Provider: &failingProvider{err: fmt.Errorf("529 overloaded")}},
	}, nil)

	_, err := fc.Chat(context.Background(), nil, nil, ChatOptions{})
	if err == nil {
		t.Error("expected error when all candidates fail")
	}
}

func TestFallbackChain_RespectsCooldown(t *testing.T) {
	cooldown := NewCooldownTracker()
	// Put primary on cooldown
	cooldown.SetCooldown("primary", 5*time.Minute)

	fc := NewFallbackChain([]FallbackCandidate{
		{Name: "primary", Provider: &succeedingProvider{content: "primary"}},
		{Name: "backup", Provider: &succeedingProvider{content: "backup"}},
	}, cooldown)

	resp, err := fc.Chat(context.Background(), nil, nil, ChatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Primary is on cooldown, should use backup
	if resp.Content != "backup" {
		t.Errorf("got %q, want %q (primary should be on cooldown)", resp.Content, "backup")
	}
}

func TestCooldownTracker_ExpiresCorrectly(t *testing.T) {
	ct := NewCooldownTracker()
	ct.SetCooldown("test", 1*time.Millisecond)

	if !ct.IsOnCooldown("test") {
		t.Error("should be on cooldown immediately after setting")
	}

	time.Sleep(5 * time.Millisecond)
	if ct.IsOnCooldown("test") {
		t.Error("cooldown should have expired")
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		err    string
		reason failoverReason
	}{
		{"401 Unauthorized", reasonAuth},
		{"429 rate_limit exceeded", reasonRateLimit},
		{"529 overloaded", reasonTimeout}, // 529 is transient HTTP; "overloaded_error" matches overloaded
		{"context deadline exceeded", reasonTimeout},
		{"400 invalid request", reasonFormat},
		{"something else went wrong", reasonUnknown},
		// New comprehensive patterns:
		{"too many requests", reasonRateLimit},
		{"exceeded your current quota", reasonRateLimit},
		{"resource_exhausted", reasonRateLimit},
		{"overloaded_error", reasonOverloaded},
		{"payment required", reasonBilling},
		{"insufficient credits", reasonBilling},
		{"402 payment required", reasonBilling},
		{"invalid api key", reasonAuth},
		{"incorrect api key", reasonAuth},
		{"token has expired", reasonAuth},
		{"access denied", reasonAuth},
		{"no api key found", reasonAuth},
		{"string should match pattern", reasonFormat},
		{"timed out", reasonTimeout},
		{"status: 503", reasonTimeout},
		{"HTTP/1.1 502", reasonTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.err, func(t *testing.T) {
			got := classifyError(fmt.Errorf("%s", tt.err))
			if got != tt.reason {
				t.Errorf("classifyError(%q) = %s, want %s", tt.err, got, tt.reason)
			}
		})
	}
}

func TestClassifyError_ContextCanceled(t *testing.T) {
	err := context.Canceled
	got := classifyError(err)
	if got != reasonCanceled {
		t.Fatalf("context.Canceled: got %s, want %s (non-retriable)", got, reasonCanceled)
	}
}

func TestClassifyError_ContextDeadlineExceeded(t *testing.T) {
	got := classifyError(context.DeadlineExceeded)
	if got != reasonTimeout {
		t.Errorf("context.DeadlineExceeded: got %s, want %s", got, reasonTimeout)
		t.Errorf("context.DeadlineExceeded: got %s, want timeout", got)
	}
}

func TestCooldownForReason_Billing(t *testing.T) {
	d := cooldownForReason(reasonBilling)
	if d != 10*time.Minute {
		t.Errorf("billing cooldown = %v, want 10m", d)
	}
}
