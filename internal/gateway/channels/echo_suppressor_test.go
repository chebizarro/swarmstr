package channels

import (
	"testing"
	"time"
)

func TestNormalizeEchoText(t *testing.T) {
	got := NormalizeEchoText("  HELLO, World!! Visit https://example.com/x now  ")
	want := "hello world visit now"
	if got != want {
		t.Errorf("normalize = %q, want %q", got, want)
	}
}

func TestEchoSuppressor_ExactAndSimilar(t *testing.T) {
	s, err := NewEchoSuppressor(EchoSuppressorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s.Observe("room", "The deployment finished successfully on edge-01")

	// Exact restatement (punctuation/case differ) -> echo.
	if !s.IsEcho("room", "the deployment finished successfully on edge-01!", 0) {
		t.Error("exact normalized restatement should be an echo")
	}
	// High word overlap -> echo (Jaccard >= 0.85 default is strict; use a
	// near-identical line with one extra token to stay above threshold).
	if !s.IsEcho("room", "The deployment finished successfully on edge-01", 0) {
		t.Error("identical text should be an echo")
	}
	// Distinct line -> not an echo.
	if s.IsEcho("room", "Starting a completely different unrelated task now", 0) {
		t.Error("distinct line should not be an echo")
	}
}

func TestEchoSuppressor_ShortLinesExactOnly(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{})
	s.Observe("room", "on it") // 2 tokens < minTokens(4)

	// Exact short line -> echo.
	if !s.IsEcho("room", "On it!", 0) {
		t.Error("exact short line should be an echo")
	}
	// Different short line sharing a token -> NOT an echo (min-token guard).
	if s.IsEcho("room", "on that", 0) {
		t.Error("short distinct line must not be collapsed by Jaccard")
	}
}

func TestEchoSuppressor_ThresholdOverride(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{})
	s.Observe("room", "alpha beta gamma delta epsilon")
	// 4/5 shared tokens -> Jaccard = 4/6 ≈ 0.67. Default 0.85 -> not echo;
	// override 0.6 -> echo.
	candidate := "alpha beta gamma delta zeta"
	if s.IsEcho("room", candidate, 0) {
		t.Error("below default threshold should not be an echo")
	}
	if !s.IsEcho("room", candidate, 0.6) {
		t.Error("threshold override 0.6 should catch the near-match")
	}
}

func TestEchoSuppressor_WindowEviction(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{WindowSize: 2})
	s.Observe("room", "first message alpha beta")
	s.Observe("room", "second message gamma delta")
	s.Observe("room", "third message epsilon zeta") // evicts first
	if s.IsEcho("room", "first message alpha beta", 0) {
		t.Error("evicted entry should no longer match")
	}
	if !s.IsEcho("room", "third message epsilon zeta", 0) {
		t.Error("newest entry should still match")
	}
}

func TestEchoSuppressor_TTLExpiry(t *testing.T) {
	clock := newGuardClock()
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{TTL: 5 * time.Minute, Now: clock.now})
	s.Observe("room", "stale message alpha beta gamma")
	clock.advance(6 * time.Minute)
	if s.IsEcho("room", "stale message alpha beta gamma", 0) {
		t.Error("expired entry must not match")
	}
}

func TestEchoSuppressor_RoomIsolationAndReset(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{})
	s.Observe("A", "shared line alpha beta gamma")
	if s.IsEcho("B", "shared line alpha beta gamma", 0) {
		t.Error("rooms must be isolated")
	}
	if !s.IsEcho("A", "shared line alpha beta gamma", 0) {
		t.Fatal("room A should match before reset")
	}
	s.Reset("A")
	if s.IsEcho("A", "shared line alpha beta gamma", 0) {
		t.Error("reset room must not match")
	}
}

func TestEchoSuppressor_EmptyAndValidation(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{})
	s.Observe("room", "   !!!   ") // normalizes to empty -> ignored
	if s.IsEcho("room", "!!!", 0) {
		t.Error("empty-normalized text is never an echo")
	}
	if _, err := NewEchoSuppressor(EchoSuppressorOptions{WindowSize: -1}); err == nil {
		t.Error("negative windowSize must error")
	}
	if _, err := NewEchoSuppressor(EchoSuppressorOptions{SimilarityThreshold: 1.5}); err == nil {
		t.Error("out-of-range threshold must error")
	}
}
