package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunObserveCallsRuntimeObserve(t *testing.T) {
	var gotMethod string
	var gotParams map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotMethod, _ = req["method"].(string)
		gotParams, _ = req["params"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"events": map[string]any{
					"cursor": 13,
					"events": []map[string]any{{"event": "tool.start"}},
				},
				"timed_out": false,
				"waited_ms": 12,
			},
		})
	}))
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	oldAddr := os.Getenv("METIQ_ADMIN_ADDR")
	defer os.Setenv("METIQ_ADMIN_ADDR", oldAddr)
	_ = os.Setenv("METIQ_ADMIN_ADDR", addr)

	out, err := captureStdout(t, func() error {
		return runObserve([]string{
			"--include-logs=false",
			"--event", "tool.start,turn.finish",
			"--event", "relay.health",
			"--agent", "main",
			"--session", "sess-1",
			"--channel", "nostr",
			"--direction", "inbound",
			"--subsystem", "tool",
			"--source", "reply",
			"--event-cursor", "11",
			"--log-cursor", "12",
			"--event-limit", "5",
			"--log-limit", "6",
			"--max-bytes", "4096",
			"--wait", "1500ms",
		})
	})
	if err != nil {
		t.Fatalf("runObserve: %v", err)
	}
	if gotMethod != "runtime.observe" {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotParams["include_events"] != true {
		t.Fatalf("expected include_events=true, got %#v", gotParams["include_events"])
	}
	if gotParams["include_logs"] != false {
		t.Fatalf("expected include_logs=false, got %#v", gotParams["include_logs"])
	}
	if gotParams["event_cursor"].(float64) != 11 || gotParams["log_cursor"].(float64) != 12 {
		t.Fatalf("unexpected cursors: %#v", gotParams)
	}
	if gotParams["event_limit"].(float64) != 5 || gotParams["log_limit"].(float64) != 6 {
		t.Fatalf("unexpected limits: %#v", gotParams)
	}
	if gotParams["max_bytes"].(float64) != 4096 || gotParams["wait_timeout_ms"].(float64) != 1500 {
		t.Fatalf("unexpected sizing/wait params: %#v", gotParams)
	}
	events, _ := gotParams["events"].([]any)
	if len(events) != 3 || events[0] != "tool.start" || events[1] != "turn.finish" || events[2] != "relay.health" {
		t.Fatalf("unexpected event filters: %#v", gotParams["events"])
	}
	if gotParams["agent_id"] != "main" || gotParams["session_id"] != "sess-1" || gotParams["channel_id"] != "nostr" {
		t.Fatalf("unexpected id filters: %#v", gotParams)
	}
	if gotParams["direction"] != "inbound" || gotParams["subsystem"] != "tool" || gotParams["source"] != "reply" {
		t.Fatalf("unexpected metadata filters: %#v", gotParams)
	}
	if !strings.Contains(out, "\"timed_out\": false") || !strings.Contains(out, "\"tool.start\"") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRunObserveUsesGatewayTransport(t *testing.T) {
	oldResolver := resolveGWClientFn
	defer func() { resolveGWClientFn = oldResolver }()

	stub := &stubGatewayClient{result: map[string]any{"timed_out": false}}
	var gotTransport, gotTarget, gotSigner string
	var gotTimeout time.Duration
	resolveGWClientFn = func(transport, addrFlag, tokenFlag, bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (gatewayCaller, error) {
		gotTransport = transport
		gotTarget = controlTargetPubKey
		gotSigner = controlSignerURL
		gotTimeout = timeout
		return stub, nil
	}

	_, err := captureStdout(t, func() error {
		return runObserve([]string{
			"--transport", "nostr",
			"--control-target-pubkey", "npub1target",
			"--control-signer-url", "env://METIQ_CONTROL_SIGNER",
			"--timeout", "10",
			"--wait", "15s",
			"--include-logs=false",
		})
	})
	if err != nil {
		t.Fatalf("runObserve gateway transport: %v", err)
	}
	if gotTransport != "nostr" {
		t.Fatalf("unexpected transport: %q", gotTransport)
	}
	if gotTarget != "npub1target" || gotSigner != "env://METIQ_CONTROL_SIGNER" {
		t.Fatalf("unexpected control routing: target=%q signer=%q", gotTarget, gotSigner)
	}
	if gotTimeout != 20*time.Second {
		t.Fatalf("expected wait-adjusted timeout of 20s, got %v", gotTimeout)
	}
	if stub.method != "runtime.observe" {
		t.Fatalf("unexpected method: %s", stub.method)
	}
}

func TestRunObserveRejectsDisabledSections(t *testing.T) {
	err := runObserve([]string{"--include-events=false", "--include-logs=false"})
	if err == nil {
		t.Fatal("expected error when both observe sections are disabled")
	}
	if !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseObserveWait(t *testing.T) {
	ms, err := parseObserveWait("1500ms")
	if err != nil {
		t.Fatalf("parseObserveWait duration: %v", err)
	}
	if ms != 1500 {
		t.Fatalf("expected 1500ms, got %d", ms)
	}
	ms, err = parseObserveWait("2500")
	if err != nil {
		t.Fatalf("parseObserveWait integer: %v", err)
	}
	if ms != 2500 {
		t.Fatalf("expected 2500ms, got %d", ms)
	}
	if _, err := parseObserveWait("nope"); err == nil {
		t.Fatal("expected invalid wait parse to fail")
	}
}

func TestUsageIncludesObserve(t *testing.T) {
	out, err := captureStdout(t, func() error {
		usage()
		return nil
	})
	if err != nil {
		t.Fatalf("usage output failed: %v", err)
	}
	if !strings.Contains(out, "observe") {
		t.Fatalf("usage missing observe command: %s", out)
	}
}
