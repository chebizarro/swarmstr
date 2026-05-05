package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestProbeRelayREQSuccess(t *testing.T) {
	sawREQ := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var msg []any
		if err := wsjson.Read(context.Background(), conn, &msg); err != nil {
			t.Errorf("read req: %v", err)
			return
		}
		if len(msg) < 2 {
			t.Errorf("unexpected req frame: %#v", msg)
			return
		}
		if got, _ := msg[0].(string); got != "REQ" {
			t.Errorf("expected REQ frame, got %#v", msg)
			return
		}
		subID, _ := msg[1].(string)
		sawREQ <- subID
		if err := wsjson.Write(context.Background(), conn, []any{"EOSE", subID}); err != nil {
			t.Errorf("write eose: %v", err)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res := ProbeRelayREQ(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"))
	if !res.Reachable {
		t.Fatalf("expected reachable relay, got err=%v", res.Err)
	}
	subID := receiveBeforeTestDeadline(t, sawREQ, "relay probe REQ")
	if strings.TrimSpace(subID) == "" {
		t.Fatal("expected non-empty subscription id")
	}
}

func TestProbeRelayREQHandshakeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not a websocket relay"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := ProbeRelayREQ(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"))
	if res.Reachable {
		t.Fatal("expected unreachable relay")
	}
	if res.Err == nil {
		t.Fatal("expected probe error")
	}
}

func TestProbeRelayREQClosedEventStreamIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")

		var msg []any
		if err := wsjson.Read(context.Background(), conn, &msg); err != nil {
			t.Errorf("read req: %v", err)
			return
		}
		// Close immediately without EVENT or EOSE.
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res := ProbeRelayREQ(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"))
	if res.Reachable {
		t.Fatal("closed event stream without EVENT/EOSE should be unreachable")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "subscription ended before response") {
		t.Fatalf("expected closed-stream error, got %v", res.Err)
	}
}

func TestRelayHealthMonitorRunOnceUsesNormalizedRelays(t *testing.T) {
	monitor := NewRelayHealthMonitor([]string{" wss://one ", "wss://one", "", "wss://two"}, RelayHealthMonitorOptions{
		Probe: func(_ context.Context, relayURL string) RelayHealthResult {
			return RelayHealthResult{URL: relayURL, Reachable: true}
		},
	})

	results := monitor.RunOnce(context.Background())
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].URL != "wss://one" || results[1].URL != "wss://two" {
		t.Fatalf("unexpected result order: %#v", results)
	}

	monitor.UpdateRelays([]string{"wss://three"})
	results = monitor.RunOnce(context.Background())
	if len(results) != 1 || results[0].URL != "wss://three" {
		t.Fatalf("unexpected results after update: %#v", results)
	}
}

func TestRelayHealthMonitorStartRunsInitialAndPeriodicChecks(t *testing.T) {
	done := make(chan bool, 2)
	monitor := NewRelayHealthMonitor([]string{"wss://one"}, RelayHealthMonitorOptions{
		Interval: 0,
		Probe: func(_ context.Context, relayURL string) RelayHealthResult {
			return RelayHealthResult{URL: relayURL, Reachable: true}
		},
		OnResults: func(initial bool, results []RelayHealthResult) {
			if len(results) != 1 || results[0].URL != "wss://one" {
				t.Errorf("unexpected results: %#v", results)
				return
			}
			done <- initial
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor.Start(ctx)

	initial := receiveBeforeTestDeadline(t, done, "initial relay health callback")
	if !initial {
		t.Fatal("expected first run to be initial")
	}

	monitor.Trigger()

	initial = receiveBeforeTestDeadline(t, done, "triggered relay health callback")
	if initial {
		t.Fatal("expected triggered run after initial check")
	}
}

func TestRelayHealthMonitorCandidatesFallbackWhenAllBlocked(t *testing.T) {
	tracker := NewRelayHealthTracker()
	relays := []string{"wss://a", "wss://b"}
	tracker.Seed(relays)
	for i := 0; i < relayFailureCooldownThreshold; i++ {
		tracker.RecordFailure("wss://a")
		tracker.RecordFailure("wss://b")
	}
	candidates := tracker.Candidates(relays, time.Now())
	if len(candidates) != len(relays) {
		t.Fatalf("expected fallback to full relay list when all blocked, got %v", candidates)
	}
	if candidates[0] != "wss://a" || candidates[1] != "wss://b" {
		t.Fatalf("expected stable sorted fallback order, got %v", candidates)
	}
}

func TestRelayHealthMonitorStartIsIdempotent(t *testing.T) {
	done := make(chan bool, 2)
	monitor := NewRelayHealthMonitor([]string{"wss://one"}, RelayHealthMonitorOptions{
		Interval: 0,
		Probe: func(_ context.Context, relayURL string) RelayHealthResult {
			return RelayHealthResult{URL: relayURL, Reachable: true}
		},
		OnResults: func(initial bool, results []RelayHealthResult) {
			done <- initial
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor.Start(ctx)
	monitor.Start(ctx)

	initial := receiveBeforeTestDeadline(t, done, "initial relay health callback")
	if !initial {
		t.Fatal("expected first callback to be initial")
	}

	select {
	case initial := <-done:
		t.Fatalf("unexpected extra callback after double start: initial=%v", initial)
	default:
	}
}
