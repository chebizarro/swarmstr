package state

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSessionStore_RecordTurn(t *testing.T) {
	ss, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := ss.RecordTurn("sess-1", TurnTelemetry{
		TurnID:         "turn-1",
		StartedAtMS:    100,
		EndedAtMS:      250,
		DurationMS:     150,
		Outcome:        "completed",
		StopReason:     "model_text",
		FallbackUsed:   true,
		FallbackFrom:   "a",
		FallbackTo:     "b",
		FallbackReason: "429",
		InputTokens:    10,
		OutputTokens:   5,
	}); err != nil {
		t.Fatalf("record turn: %v", err)
	}
	got, ok := ss.Get("sess-1")
	if !ok {
		t.Fatal("session not found")
	}
	if got.LastTurn == nil {
		t.Fatal("expected last turn snapshot")
	}
	if got.LastTurn.TurnID != "turn-1" || got.LastTurn.DurationMS != 150 || got.LastTurn.Outcome != "completed" {
		t.Fatalf("unexpected last turn snapshot: %+v", got.LastTurn)
	}
	if !got.LastTurn.FallbackUsed || got.LastTurn.FallbackTo != "b" {
		t.Fatalf("expected fallback data on last turn: %+v", got.LastTurn)
	}
}

func TestTurnTelemetry_JSONShape(t *testing.T) {
	raw, err := json.Marshal(TurnTelemetry{
		TurnID:      "turn-1",
		StartedAtMS: 1,
		EndedAtMS:   2,
		Outcome:     "completed",
		StopReason:  "model_text",
	})
	if err != nil {
		t.Fatalf("marshal telemetry: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal telemetry: %v", err)
	}
	for _, field := range []string{"turn_id", "started_at_ms", "ended_at_ms", "outcome", "stop_reason"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("missing field %q in telemetry JSON: %s", field, string(raw))
		}
	}
}
