package agent

import (
	"strings"
	"testing"
)

func TestSelectCompactSystemPrompt_TierVariants(t *testing.T) {
	micro := SelectCompactSystemPrompt(TierMicro)
	small := SelectCompactSystemPrompt(TierSmall)
	standard := SelectCompactSystemPrompt(TierStandard)

	// Micro should be shortest
	if len(micro) >= len(small) {
		t.Errorf("micro prompt (%d chars) should be shorter than small (%d chars)", len(micro), len(small))
	}
	if len(small) >= len(standard) {
		t.Errorf("small prompt (%d chars) should be shorter than standard (%d chars)", len(small), len(standard))
	}

	// All should contain section markers
	if !strings.Contains(micro, "TASK") || !strings.Contains(micro, "STATE") {
		t.Error("micro prompt should contain TASK and STATE sections")
	}
	if !strings.Contains(standard, "PRIMARY REQUEST") {
		t.Error("standard prompt should contain PRIMARY REQUEST section")
	}
}

func TestSelectCompactUserPrompt_Truncation(t *testing.T) {
	longTranscript := strings.Repeat("x", 10000)

	// Micro with limit
	result := SelectCompactUserPrompt(TierMicro, longTranscript, 500)
	if len(result) > 600 { // 500 + prompt text
		t.Errorf("micro user prompt too long: %d chars", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Error("truncated transcript should contain truncation marker")
	}

	// No limit
	noLimit := SelectCompactUserPrompt(TierStandard, longTranscript, 0)
	if !strings.Contains(noLimit, strings.Repeat("x", 100)) {
		t.Error("no limit should preserve transcript")
	}
}

func TestCompactOutputMaxTokens_TierScaling(t *testing.T) {
	micro := CompactOutputMaxTokens(TierMicro)
	small := CompactOutputMaxTokens(TierSmall)
	standard := CompactOutputMaxTokens(TierStandard)

	if micro >= small || small >= standard {
		t.Errorf("max tokens should increase with tier: micro=%d, small=%d, standard=%d", micro, small, standard)
	}
	if micro != 256 {
		t.Errorf("micro max tokens = %d, want 256", micro)
	}
	if standard != 2048 {
		t.Errorf("standard max tokens = %d, want 2048", standard)
	}
}

func TestSelectCompactSystemPromptForBudget_Thresholds(t *testing.T) {
	tests := []struct {
		effectiveChars int
		wantContains   string
		desc           string
	}{
		{3000, "TASK", "very small budget → micro prompt"},
		{5999, "TASK", "just under threshold → micro prompt"},
		{6000, "TASK:", "at small threshold"},
		{20000, "TASK:", "mid range → small prompt"},
		{25000, "PRIMARY REQUEST", "at standard threshold"},
		{100000, "PRIMARY REQUEST", "large budget → standard prompt"},
	}

	for _, tt := range tests {
		budget := ContextBudget{EffectiveChars: tt.effectiveChars}
		prompt := SelectCompactSystemPromptForBudget(budget)
		if !strings.Contains(prompt, tt.wantContains) {
			t.Errorf("%s (effective=%d): prompt should contain %q", tt.desc, tt.effectiveChars, tt.wantContains)
		}
	}
}

func TestSelectCompactUserPromptForBudget_TranscriptTruncation(t *testing.T) {
	longTranscript := strings.Repeat("x", 50000)

	// Small budget should heavily truncate
	smallBudget := ContextBudget{
		EffectiveChars: 5000,
		Profile:        ModelContextProfile{ContextWindowTokens: 4096},
	}
	result := SelectCompactUserPromptForBudget(smallBudget, longTranscript)
	if len(result) > 5000 {
		t.Errorf("small budget user prompt too long: %d chars", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Error("truncated transcript should contain marker")
	}

	// Large budget allows more
	largeBudget := ContextBudget{
		EffectiveChars: 200000,
		Profile:        ModelContextProfile{ContextWindowTokens: 200000},
	}
	largeResult := SelectCompactUserPromptForBudget(largeBudget, longTranscript)
	if len(largeResult) < len(result) {
		t.Error("large budget should allow more transcript than small budget")
	}
}

func TestCompactOutputMaxTokensForBudget_Scaling(t *testing.T) {
	small := CompactOutputMaxTokensForBudget(ContextBudget{
		Profile: ModelContextProfile{ContextWindowTokens: 4096},
	})
	large := CompactOutputMaxTokensForBudget(ContextBudget{
		Profile: ModelContextProfile{ContextWindowTokens: 200000},
	})

	if small >= large {
		t.Errorf("small budget tokens (%d) should be less than large (%d)", small, large)
	}
	if small < 256 {
		t.Errorf("min tokens should be 256, got %d", small)
	}
	if large > 2048 {
		t.Errorf("max tokens should be 2048, got %d", large)
	}
}
