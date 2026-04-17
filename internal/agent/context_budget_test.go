package agent

import (
	"math"
	"testing"
)

func TestComputeContextBudget_ProportionalScaling(t *testing.T) {
	// Verify budgets scale monotonically with window size
	sizes := []int{2048, 4096, 8192, 16384, 32000, 64000, 128000, 200000}
	var prev ContextBudget
	for _, tokens := range sizes {
		b := ComputeContextBudgetForTokens(tokens)

		if b.EffectiveChars < 1024 {
			t.Errorf("EffectiveChars at %d tokens = %d, want >= 1024", tokens, b.EffectiveChars)
		}

		// All budget zones should increase monotonically
		if b.BootstrapTotalMax < prev.BootstrapTotalMax {
			t.Errorf("BootstrapTotalMax decreased at %d tokens: %d < %d", tokens, b.BootstrapTotalMax, prev.BootstrapTotalMax)
		}
		if b.SkillsTotalMax < prev.SkillsTotalMax {
			t.Errorf("SkillsTotalMax decreased at %d tokens: %d < %d", tokens, b.SkillsTotalMax, prev.SkillsTotalMax)
		}
		if b.ToolDefsMax < prev.ToolDefsMax {
			t.Errorf("ToolDefsMax decreased at %d tokens: %d < %d", tokens, b.ToolDefsMax, prev.ToolDefsMax)
		}
		if b.SessionMemoryMax < prev.SessionMemoryMax {
			t.Errorf("SessionMemoryMax decreased at %d tokens: %d < %d", tokens, b.SessionMemoryMax, prev.SessionMemoryMax)
		}
		if b.HistoryMax < prev.HistoryMax {
			t.Errorf("HistoryMax decreased at %d tokens: %d < %d", tokens, b.HistoryMax, prev.HistoryMax)
		}
		if b.ToolResultSharePct < prev.ToolResultSharePct {
			t.Errorf("ToolResultSharePct decreased at %d tokens: %f < %f", tokens, b.ToolResultSharePct, prev.ToolResultSharePct)
		}
		if b.CompactionThreshold < prev.CompactionThreshold {
			t.Errorf("CompactionThreshold decreased at %d tokens: %f < %f", tokens, b.CompactionThreshold, prev.CompactionThreshold)
		}
		if b.MicroCompactKeepRecent < prev.MicroCompactKeepRecent {
			t.Errorf("MicroCompactKeepRecent decreased at %d tokens: %d < %d", tokens, b.MicroCompactKeepRecent, prev.MicroCompactKeepRecent)
		}
		if b.SkillsMaxCount < prev.SkillsMaxCount {
			t.Errorf("SkillsMaxCount decreased at %d tokens: %d < %d", tokens, b.SkillsMaxCount, prev.SkillsMaxCount)
		}

		prev = b
	}
}

func TestComputeContextBudget_MicroModel(t *testing.T) {
	b := ComputeContextBudgetForTokens(4_096)

	// Micro model should have tight budgets
	if b.BootstrapTotalMax > 5_000 {
		t.Errorf("4K BootstrapTotalMax = %d, want <= 5000", b.BootstrapTotalMax)
	}
	if b.SkillsTotalMax > 2_000 {
		t.Errorf("4K SkillsTotalMax = %d, want <= 2000", b.SkillsTotalMax)
	}
	if b.ToolDefsMax > 3_000 {
		t.Errorf("4K ToolDefsMax = %d, want <= 3000", b.ToolDefsMax)
	}
	if b.SkillsMaxCount > 10 {
		t.Errorf("4K SkillsMaxCount = %d, want <= 10", b.SkillsMaxCount)
	}
	if b.HistoryMax < 1_000 {
		t.Errorf("4K HistoryMax = %d, want >= 1000 (floor)", b.HistoryMax)
	}
	if b.ToolResultSharePct > 0.16 {
		t.Errorf("4K ToolResultSharePct = %f, want ~0.15", b.ToolResultSharePct)
	}
	if b.CompactionThreshold > 0.71 {
		t.Errorf("4K CompactionThreshold = %f, want ~0.70", b.CompactionThreshold)
	}
	if b.MicroCompactKeepRecent != 1 {
		t.Errorf("4K MicroCompactKeepRecent = %d, want 1", b.MicroCompactKeepRecent)
	}
}

func TestComputeContextBudget_StandardModel(t *testing.T) {
	b := ComputeContextBudgetForTokens(200_000)

	if b.ToolResultSharePct < 0.29 {
		t.Errorf("200K ToolResultSharePct = %f, want ~0.30", b.ToolResultSharePct)
	}
	if b.CompactionThreshold < 0.79 {
		t.Errorf("200K CompactionThreshold = %f, want ~0.80", b.CompactionThreshold)
	}
	if b.MicroCompactKeepRecent < 7 {
		t.Errorf("200K MicroCompactKeepRecent = %d, want >= 7", b.MicroCompactKeepRecent)
	}
	if b.SkillsMaxCount < 140 {
		t.Errorf("200K SkillsMaxCount = %d, want >= 140", b.SkillsMaxCount)
	}
}

func TestComputeContextBudget_IntermediaryModel(t *testing.T) {
	// Gemma4 at 32K — should get intermediate values, not micro/standard extremes
	b := ComputeContextBudgetForTokens(32_000)

	if b.BootstrapTotalMax <= 1_536 {
		t.Errorf("32K BootstrapTotalMax = %d, should be above micro floor", b.BootstrapTotalMax)
	}
	if b.BootstrapTotalMax >= 150_000 {
		t.Errorf("32K BootstrapTotalMax = %d, should be below standard cap", b.BootstrapTotalMax)
	}
	// ToolResultSharePct should be between extremes
	if b.ToolResultSharePct <= 0.15 || b.ToolResultSharePct >= 0.30 {
		t.Errorf("32K ToolResultSharePct = %f, want between 0.15 and 0.30", b.ToolResultSharePct)
	}
	// CompactionThreshold between extremes
	if b.CompactionThreshold <= 0.70 || b.CompactionThreshold >= 0.80 {
		t.Errorf("32K CompactionThreshold = %f, want between 0.70 and 0.80", b.CompactionThreshold)
	}
}

func TestComputeContextBudget_SystemPromptMax(t *testing.T) {
	b := ComputeContextBudgetForTokens(100_000)
	expected := b.BootstrapTotalMax + b.SkillsTotalMax + b.SessionMemoryMax
	if b.SystemPromptMax != expected {
		t.Errorf("SystemPromptMax = %d, want %d (bootstrap + skills + memory)", b.SystemPromptMax, expected)
	}
}

func TestComputeContextBudget_ZonesDoNotExceedEffective(t *testing.T) {
	for _, tokens := range []int{2048, 8192, 32000, 200000} {
		b := ComputeContextBudgetForTokens(tokens)
		fixedUse := b.BootstrapTotalMax + b.SkillsTotalMax + b.ToolDefsMax + b.SessionMemoryMax
		total := fixedUse + b.HistoryMax
		if total > b.EffectiveChars+1000 { // allow 1000 chars for history floor adjustment
			t.Errorf("at %d tokens: zones total %d exceeds effective %d", tokens, total, b.EffectiveChars)
		}
	}
}

func TestCompressionPressure(t *testing.T) {
	tests := []struct {
		budget, total int
		wantPressure  float64
	}{
		{10000, 5000, 0.0},    // budget exceeds total
		{10000, 10000, 0.0},   // budget equals total
		{5000, 10000, 0.5},    // half budget
		{1000, 10000, 0.9},    // 10% budget
		{0, 10000, 1.0},       // zero budget
		{10000, 0, 0.0},       // zero total
		{10000, -1, 0.0},      // negative total
	}
	for _, tt := range tests {
		got := CompressionPressure(tt.budget, tt.total)
		if math.Abs(got-tt.wantPressure) > 0.01 {
			t.Errorf("CompressionPressure(%d, %d) = %f, want %f", tt.budget, tt.total, got, tt.wantPressure)
		}
	}
}

func TestLerp(t *testing.T) {
	if got := lerp(0, 100, 0); got != 0 {
		t.Errorf("lerp(0,100,0) = %f", got)
	}
	if got := lerp(0, 100, 1); got != 100 {
		t.Errorf("lerp(0,100,1) = %f", got)
	}
	if got := lerp(0, 100, 0.5); got != 50 {
		t.Errorf("lerp(0,100,0.5) = %f", got)
	}
	if got := lerp(10, 20, 0.3); math.Abs(got-13) > 0.01 {
		t.Errorf("lerp(10,20,0.3) = %f, want 13", got)
	}
}

func TestClampInt(t *testing.T) {
	if got := clampInt(5, 0, 10); got != 5 {
		t.Errorf("clampInt(5,0,10) = %d", got)
	}
	if got := clampInt(-1, 0, 10); got != 0 {
		t.Errorf("clampInt(-1,0,10) = %d", got)
	}
	if got := clampInt(15, 0, 10); got != 10 {
		t.Errorf("clampInt(15,0,10) = %d", got)
	}
}

func TestClampF(t *testing.T) {
	if got := clampF(0.5, 0, 1); got != 0.5 {
		t.Errorf("clampF(0.5,0,1) = %f", got)
	}
	if got := clampF(-0.1, 0, 1); got != 0 {
		t.Errorf("clampF(-0.1,0,1) = %f", got)
	}
	if got := clampF(1.5, 0, 1); got != 1 {
		t.Errorf("clampF(1.5,0,1) = %f", got)
	}
}
