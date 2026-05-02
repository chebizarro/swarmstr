package agent

import (
	"testing"
)

func TestContextTier_String(t *testing.T) {
	tests := []struct {
		tier ContextTier
		want string
	}{
		{TierMicro, "micro"},
		{TierSmall, "small"},
		{TierStandard, "standard"},
		{ContextTier(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.tier.String(); got != tt.want {
			t.Errorf("ContextTier(%d).String() = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestTierFromContextWindowTokens(t *testing.T) {
	tests := []struct {
		tokens int
		want   ContextTier
	}{
		{0, TierStandard},     // zero → standard
		{-1, TierStandard},    // negative → standard
		{2048, TierMicro},     // tiny
		{4096, TierMicro},     // typical micro
		{8191, TierMicro},     // just below small boundary
		{8192, TierSmall},     // small boundary
		{16384, TierSmall},    // upper small boundary
		{16385, TierStandard}, // just above small
		{200_000, TierStandard},
	}
	for _, tt := range tests {
		got := TierFromContextWindowTokens(tt.tokens)
		if got != tt.want {
			t.Errorf("TierFromContextWindowTokens(%d) = %v, want %v", tt.tokens, got, tt.want)
		}
	}
}

func TestProfileFromContextWindowTokens_ProportionalScaling(t *testing.T) {
	// 4K model: micro tier
	p4k := ProfileFromContextWindowTokens(4_096)
	if p4k.Tier != TierMicro {
		t.Errorf("4K model tier = %v, want Micro", p4k.Tier)
	}
	if p4k.MaxAgenticIterations != 5 {
		t.Errorf("4K MaxAgenticIterations = %d, want 5 (floor)", p4k.MaxAgenticIterations)
	}
	if p4k.ContextWindowTokens != 4_096 {
		t.Errorf("4K ContextWindowTokens = %d, want 4096", p4k.ContextWindowTokens)
	}

	// 32K model: should get ~5 iterations (32000/6000 = 5)
	p32k := ProfileFromContextWindowTokens(32_000)
	if p32k.MaxAgenticIterations != 5 {
		t.Errorf("32K MaxAgenticIterations = %d, want 5", p32k.MaxAgenticIterations)
	}

	// 120K model: 120000/6000 = 20
	p120k := ProfileFromContextWindowTokens(120_000)
	if p120k.MaxAgenticIterations != 20 {
		t.Errorf("120K MaxAgenticIterations = %d, want 20", p120k.MaxAgenticIterations)
	}

	// 200K+ model: capped at 30
	p200k := ProfileFromContextWindowTokens(200_000)
	if p200k.MaxAgenticIterations != 30 {
		t.Errorf("200K MaxAgenticIterations = %d, want 30 (cap)", p200k.MaxAgenticIterations)
	}
	p500k := ProfileFromContextWindowTokens(500_000)
	if p500k.MaxAgenticIterations != 30 {
		t.Errorf("500K MaxAgenticIterations = %d, want 30 (cap)", p500k.MaxAgenticIterations)
	}

	// Reserve output tokens should scale proportionally
	if p4k.ReserveOutputTokens >= p200k.ReserveOutputTokens {
		t.Errorf("4K reserve (%d) should be less than 200K reserve (%d)",
			p4k.ReserveOutputTokens, p200k.ReserveOutputTokens)
	}
	if p200k.ReserveOutputTokens != 4_096 {
		t.Errorf("200K ReserveOutputTokens = %d, want 4096", p200k.ReserveOutputTokens)
	}

	// Zero/negative tokens default to 200K
	pZero := ProfileFromContextWindowTokens(0)
	if pZero.ContextWindowTokens != 200_000 {
		t.Errorf("zero tokens should default to 200K, got %d", pZero.ContextWindowTokens)
	}
	pNeg := ProfileFromContextWindowTokens(-1)
	if pNeg.ContextWindowTokens != 200_000 {
		t.Errorf("negative tokens should default to 200K, got %d", pNeg.ContextWindowTokens)
	}
}

func TestEffectiveInputTokens(t *testing.T) {
	p := ModelContextProfile{ContextWindowTokens: 4096, ReserveOutputTokens: 512}
	if got := p.EffectiveInputTokens(); got != 3584 {
		t.Errorf("EffectiveInputTokens() = %d, want 3584", got)
	}

	// When reserve exceeds window, floor at 256
	p2 := ModelContextProfile{ContextWindowTokens: 100, ReserveOutputTokens: 500}
	if got := p2.EffectiveInputTokens(); got != 256 {
		t.Errorf("EffectiveInputTokens() floor = %d, want 256", got)
	}
}

func TestResolveModelContext_BuiltInModels(t *testing.T) {
	tests := []struct {
		modelID  string
		wantTier ContextTier
		wantCtx  int
	}{
		{"phi-3-mini-4k-instruct", TierMicro, 4096},
		{"Phi-3-Mini-4K-Instruct", TierMicro, 4096}, // case-insensitive
		{"gemma-2b-it", TierSmall, 8192},
		{"mistral-7b-instruct", TierSmall, 8192},
		{"tinyllama-1.1b", TierMicro, 2048},
		// Provider-prefixed model IDs (e.g. LM Studio / llama.cpp)
		{"lemmy-local/google_gemma-4-26B-A4B-it-Q4_K_M.gguf", TierSmall, 8192},
		{"lmstudio/gemma-2-9b-it-Q5_K_M.gguf", TierSmall, 8192},
		{"local/phi-3-mini-4k.gguf", TierMicro, 4096},
		// Bare GGUF filenames without provider prefix
		{"google_gemma-4-26B-A4B-it-Q4_K_M.gguf", TierSmall, 8192},
		{"mistral-7b-instruct-v0.2.gguf", TierSmall, 8192},
	}
	for _, tt := range tests {
		p := ResolveModelContext(tt.modelID)
		if p.Tier != tt.wantTier {
			t.Errorf("ResolveModelContext(%q).Tier = %v, want %v", tt.modelID, p.Tier, tt.wantTier)
		}
		if p.ContextWindowTokens != tt.wantCtx {
			t.Errorf("ResolveModelContext(%q).ContextWindowTokens = %d, want %d", tt.modelID, p.ContextWindowTokens, tt.wantCtx)
		}
	}
}

func TestResolveModelContext_UnknownModel(t *testing.T) {
	p := ResolveModelContext("claude-3-5-sonnet-20241022")
	if p.Tier != TierStandard {
		t.Errorf("unknown model tier = %v, want Standard", p.Tier)
	}
	if p.ContextWindowTokens != 200_000 {
		t.Errorf("unknown model window = %d, want 200000", p.ContextWindowTokens)
	}
}

func TestResolveModelContext_Empty(t *testing.T) {
	p := ResolveModelContext("")
	if p.Tier != TierStandard {
		t.Errorf("empty model tier = %v, want Standard", p.Tier)
	}
}

func TestResolveModelContext_LongestPrefixWins(t *testing.T) {
	// phi-3-mini should match before phi-3 (longer prefix)
	p := ResolveModelContext("phi-3-mini-4k")
	if p.ContextWindowTokens != 4096 {
		t.Errorf("phi-3-mini should match phi-3-mini pattern, got tokens=%d", p.ContextWindowTokens)
	}
}

func TestNormalizeModelID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"lemmy-local/google_gemma-4-26B-A4B-it-Q4_K_M.gguf", "google_gemma-4-26B-A4B-it-Q4_K_M"},
		{"lmstudio/mistral-7b.ggml", "mistral-7b"},
		{"phi-3-mini-4k", "phi-3-mini-4k"},           // no prefix or extension
		{"local/model.bin", "model"},                   // .bin extension
		{"a/b/c/model-name.gguf", "model-name"},        // nested slashes
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeModelID(tt.input)
		if got != tt.want {
			t.Errorf("normalizeModelID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestProfileFromContextWindowTokens_MonotonicScaling(t *testing.T) {
	// Verify that as tokens increase, iterations and reserves increase monotonically
	prevIter := 0
	prevReserve := 0
	for _, tokens := range []int{2048, 4096, 8192, 16384, 32000, 64000, 128000, 200000} {
		p := ProfileFromContextWindowTokens(tokens)
		if p.MaxAgenticIterations < prevIter {
			t.Errorf("MaxAgenticIterations decreased at %d tokens: %d < %d", tokens, p.MaxAgenticIterations, prevIter)
		}
		if p.ReserveOutputTokens < prevReserve {
			t.Errorf("ReserveOutputTokens decreased at %d tokens: %d < %d", tokens, p.ReserveOutputTokens, prevReserve)
		}
		prevIter = p.MaxAgenticIterations
		prevReserve = p.ReserveOutputTokens
	}
}
