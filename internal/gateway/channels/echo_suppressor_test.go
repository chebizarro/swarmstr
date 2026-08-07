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

// ── Task-transition echo (R6, mirror of openclaw-nostr ocn-rb3) ──────────────

func taskEchoFixture() TaskTransitionSummary {
	return TaskTransitionSummary{
		Author: "aa11",
		TaskID: "swarmstr-31jn",
		Status: "in_progress",
		Title:  "Suppress chat shadow",
	}
}

func TestTaskEcho_AnnounceOnceThenSuppress(t *testing.T) {
	s, err := NewEchoSuppressor(EchoSuppressorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s.ObserveTaskTransition(taskEchoFixture())

	shadow := "swarmstr-31jn is now in progress — task: Suppress chat shadow"
	v := s.CheckTaskEcho("room", "aa11", shadow, 0)
	if !v.Announce || v.Suppress {
		t.Fatalf("first shadow should be the allowed compact announcement, got %+v", v)
	}
	if v.TaskID != "swarmstr-31jn" || v.Status != "in_progress" {
		t.Errorf("verdict metadata = %+v", v)
	}
	v = s.CheckTaskEcho("room", "aa11", shadow, 0)
	if !v.Suppress || v.Announce {
		t.Fatalf("second shadow should be suppressed, got %+v", v)
	}
	// Unrelated text never matches.
	if v := s.CheckTaskEcho("room", "aa11", "Working on something else entirely today", 0); v.Suppress || v.Announce {
		t.Errorf("unrelated text matched: %+v", v)
	}
}

func TestTaskEcho_SameAuthorOnly(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{})
	s.ObserveTaskTransition(taskEchoFixture())
	shadow := "swarmstr-31jn is now in progress — task: Suppress chat shadow"
	if v := s.CheckTaskEcho("room", "bb22", shadow, 0); v.Suppress || v.Announce {
		t.Fatalf("a different author's restatement is not a chat shadow: %+v", v)
	}
	// Case/whitespace-insensitive author matching.
	if v := s.CheckTaskEcho("room", " AA11 ", shadow, 0); !v.Announce {
		t.Fatalf("author matching should normalize case/space, got %+v", v)
	}
}

func TestTaskEcho_ThresholdOverride(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{})
	s.ObserveTaskTransition(taskEchoFixture())
	// 7 of 8 summary tokens (missing "task"): 0.875 coverage.
	partial := "swarmstr-31jn in progress: suppress chat shadow"
	if v := s.CheckTaskEcho("room", "aa11", partial, 0.9); v.Suppress || v.Announce {
		t.Fatalf("0.875 coverage must not match a 0.9 override: %+v", v)
	}
	if v := s.CheckTaskEcho("room", "aa11", partial, 0); !v.Announce {
		t.Fatalf("0.875 coverage should match the %v default: %+v", DefaultTaskEchoThreshold, v)
	}
}

func TestTaskEcho_AnnounceWindowExpiryReallows(t *testing.T) {
	clock := newGuardClock()
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{
		TTL:                30 * time.Minute,
		TaskAnnounceWindow: 5 * time.Minute,
		Now:                clock.now,
	})
	s.ObserveTaskTransition(taskEchoFixture())
	shadow := "swarmstr-31jn is now in progress — task: Suppress chat shadow"
	if v := s.CheckTaskEcho("room", "aa11", shadow, 0); !v.Announce {
		t.Fatalf("first shadow should announce: %+v", v)
	}
	if v := s.CheckTaskEcho("room", "aa11", shadow, 0); !v.Suppress {
		t.Fatalf("inside the window the shadow is suppressed: %+v", v)
	}
	clock.advance(6 * time.Minute) // window expired, transition entry still live
	if v := s.CheckTaskEcho("room", "aa11", shadow, 0); !v.Announce {
		t.Fatalf("an expired announce window re-allows one announcement: %+v", v)
	}
}

func TestTaskEcho_TTLExpiryAndRoomIsolation(t *testing.T) {
	clock := newGuardClock()
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{TTL: 5 * time.Minute, Now: clock.now})
	s.ObserveTaskTransition(taskEchoFixture())
	shadow := "swarmstr-31jn is now in progress — task: Suppress chat shadow"
	// The announcement throttle is per room: each room gets its own compact one.
	if v := s.CheckTaskEcho("roomA", "aa11", shadow, 0); !v.Announce {
		t.Fatalf("roomA first shadow should announce: %+v", v)
	}
	if v := s.CheckTaskEcho("roomB", "aa11", shadow, 0); !v.Announce {
		t.Fatalf("roomB keeps its own announcement allowance: %+v", v)
	}
	if v := s.CheckTaskEcho("roomA", "aa11", shadow, 0); !v.Suppress {
		t.Fatalf("roomA second shadow suppressed: %+v", v)
	}
	clock.advance(6 * time.Minute) // transition aged out entirely
	if v := s.CheckTaskEcho("roomA", "aa11", shadow, 0); v.Suppress || v.Announce {
		t.Fatalf("expired transitions must not match: %+v", v)
	}
}

func TestTaskEcho_ResetAndValidation(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{})
	s.ObserveTaskTransition(taskEchoFixture())
	shadow := "swarmstr-31jn is now in progress — task: Suppress chat shadow"
	if v := s.CheckTaskEcho("room", "aa11", shadow, 0); !v.Announce {
		t.Fatal("seed announcement")
	}
	// Per-room reset clears that room's announcement throttle only.
	s.Reset("room")
	if v := s.CheckTaskEcho("room", "aa11", shadow, 0); !v.Announce {
		t.Fatalf("per-room reset should clear the room's throttle: %+v", v)
	}
	// Full reset drops the task corpus.
	s.Reset("")
	if v := s.CheckTaskEcho("room", "aa11", shadow, 0); v.Suppress || v.Announce {
		t.Fatalf("full reset should clear the task corpus: %+v", v)
	}
	// Blank author / empty task id observations are ignored.
	s.ObserveTaskTransition(TaskTransitionSummary{TaskID: "x", Status: "open"})
	s.ObserveTaskTransition(TaskTransitionSummary{Author: "aa11", Status: "open"})
	if v := s.CheckTaskEcho("room", "aa11", "task x open", 0); v.Suppress || v.Announce {
		t.Fatalf("invalid observations must be ignored: %+v", v)
	}
	if _, err := NewEchoSuppressor(EchoSuppressorOptions{TaskWindowSize: -1}); err == nil {
		t.Error("negative taskWindowSize must error")
	}
	if _, err := NewEchoSuppressor(EchoSuppressorOptions{TaskSimilarityThreshold: 1.5}); err == nil {
		t.Error("out-of-range taskSimilarityThreshold must error")
	}
}

func TestTaskEcho_WindowEviction(t *testing.T) {
	s, _ := NewEchoSuppressor(EchoSuppressorOptions{TaskWindowSize: 1})
	s.ObserveTaskTransition(taskEchoFixture())
	next := taskEchoFixture()
	next.TaskID, next.Title = "swarmstr-zzzz", "Entirely different follow-up work"
	s.ObserveTaskTransition(next) // evicts the first transition
	if v := s.CheckTaskEcho("room", "aa11", "swarmstr-31jn is now in progress — task: Suppress chat shadow", 0); v.Suppress || v.Announce {
		t.Fatalf("evicted transition must not match: %+v", v)
	}
	if v := s.CheckTaskEcho("room", "aa11", "task swarmstr-zzzz in progress: entirely different follow-up work", 0); !v.Announce {
		t.Fatalf("retained transition should match: %+v", v)
	}
}
