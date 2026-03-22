package toolbuiltin

import "testing"

func TestNormalizeFleetRPCTimeout_DefaultAndClamp(t *testing.T) {
	if got := normalizeFleetRPCTimeout(map[string]any{}); got != defaultFleetRPCTimeoutSeconds {
		t.Fatalf("default timeout = %d, want %d", got, defaultFleetRPCTimeoutSeconds)
	}
	if got := normalizeFleetRPCTimeout(map[string]any{"timeout_seconds": float64(10)}); got != 10 {
		t.Fatalf("explicit timeout = %d, want 10", got)
	}
	if got := normalizeFleetRPCTimeout(map[string]any{"timeout_seconds": 12}); got != 12 {
		t.Fatalf("int timeout = %d, want 12", got)
	}
	if got := normalizeFleetRPCTimeout(map[string]any{"timeout_seconds": float64(0.5)}); got != defaultFleetRPCTimeoutSeconds {
		t.Fatalf("sub-second timeout = %d, want default %d", got, defaultFleetRPCTimeoutSeconds)
	}
	if got := normalizeFleetRPCTimeout(map[string]any{"timeout_seconds": float64(999)}); got != maxFleetRPCTimeoutSeconds {
		t.Fatalf("clamped timeout = %d, want %d", got, maxFleetRPCTimeoutSeconds)
	}
}
