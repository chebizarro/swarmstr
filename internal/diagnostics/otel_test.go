package diagnostics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOTelDiagnosticsNoopWhenEndpointEmpty(t *testing.T) {
	t.Setenv("METIQ_OTEL_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	d := NewOTelDiagnostics(OTelConfig{})
	if d.Enabled() {
		t.Fatal("expected disabled diagnostics without endpoint")
	}
	_, span := d.StartAgentTurn(context.Background(), "s", "t")
	if span != nil {
		t.Fatal("disabled diagnostics should return nil span")
	}
	if err := d.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOTelDiagnosticsSpansCountersAndFlush(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	d := NewOTelDiagnostics(OTelConfig{Endpoint: srv.URL, Now: func() time.Time { now = now.Add(time.Millisecond); return now }})
	_, turn := d.StartAgentTurn(context.Background(), "s1", "t1")
	turn.End(nil)
	_, tool := d.StartToolCall(context.Background(), "s1", "t1", "read_file")
	tool.End(context.DeadlineExceeded)
	if d.Counter("tool.calls") != 1 || d.Counter("diagnostics.errors") != 1 {
		t.Fatalf("bad counters")
	}
	if err := d.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans, ok := got["spans"].([]any)
	if !ok || len(spans) != 2 {
		t.Fatalf("expected two spans in payload, got %#v", got)
	}
}
