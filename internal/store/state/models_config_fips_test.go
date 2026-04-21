package state

import (
	"testing"
	"time"
)

func TestFIPSConfig_EffectiveConnTimeout_Default(t *testing.T) {
	cfg := FIPSConfig{}
	if d := cfg.EffectiveConnTimeout(); d != 5*time.Second {
		t.Errorf("expected default 5s, got %v", d)
	}
}

func TestFIPSConfig_EffectiveConnTimeout_Custom(t *testing.T) {
	cfg := FIPSConfig{ConnTimeout: "10s"}
	if d := cfg.EffectiveConnTimeout(); d != 10*time.Second {
		t.Errorf("expected 10s, got %v", d)
	}
}

func TestFIPSConfig_EffectiveConnTimeout_InvalidFallsBack(t *testing.T) {
	for _, input := range []string{"", "bad", "-1s", "0s"} {
		cfg := FIPSConfig{ConnTimeout: input}
		if d := cfg.EffectiveConnTimeout(); d != 5*time.Second {
			t.Errorf("input=%q: expected default 5s, got %v", input, d)
		}
	}
}

func TestFIPSConfig_EffectiveReachCacheTTL_Default(t *testing.T) {
	cfg := FIPSConfig{}
	if d := cfg.EffectiveReachCacheTTL(); d != 30*time.Second {
		t.Errorf("expected default 30s, got %v", d)
	}
}

func TestFIPSConfig_EffectiveReachCacheTTL_Custom(t *testing.T) {
	cfg := FIPSConfig{ReachCacheTTL: "2m"}
	if d := cfg.EffectiveReachCacheTTL(); d != 2*time.Minute {
		t.Errorf("expected 2m, got %v", d)
	}
}

func TestFIPSConfig_EffectiveReachCacheTTL_InvalidFallsBack(t *testing.T) {
	for _, input := range []string{"", "garbage", "-5s", "0ms"} {
		cfg := FIPSConfig{ReachCacheTTL: input}
		if d := cfg.EffectiveReachCacheTTL(); d != 30*time.Second {
			t.Errorf("input=%q: expected default 30s, got %v", input, d)
		}
	}
}

func TestFIPSConfig_EffectiveConnTimeout_SubSecond(t *testing.T) {
	cfg := FIPSConfig{ConnTimeout: "500ms"}
	if d := cfg.EffectiveConnTimeout(); d != 500*time.Millisecond {
		t.Errorf("expected 500ms, got %v", d)
	}
}
