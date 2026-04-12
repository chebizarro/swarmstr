package loom

import (
	"encoding/json"
	"testing"

	nostr "fiatjaf.com/nostr"
)

// ─── Constants ──────────────────────────────────────────────��─────────────────

func TestKindConstants(t *testing.T) {
	tests := []struct{ name string; got, want int }{
		{"WorkerAdvertisement", KindWorkerAdvertisement, 10100},
		{"JobRequest", KindJobRequest, 5100},
		{"JobStatus", KindJobStatus, 30100},
		{"JobResult", KindJobResult, 5101},
		{"JobCancellation", KindJobCancellation, 5102},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, tt.got, tt.want)
		}
	}
}

func TestStatusConstants(t *testing.T) {
	for _, s := range []string{StatusQueued, StatusRunning, StatusCompleted, StatusFailed, StatusCancelled, StatusTimeout} {
		if s == "" {
			t.Error("status constant is empty")
		}
	}
}

// ─── decodeWorkerEvent ────────────────────────────────────────────────────────

func TestDecodeWorkerEvent_Basic(t *testing.T) {
	ev := nostr.Event{
		Kind:      nostr.Kind(KindWorkerAdvertisement),
		CreatedAt: nostr.Timestamp(1700000000),
		Content:   `{"name":"test-worker","description":"A test worker","max_concurrent_jobs":5}`,
		Tags: nostr.Tags{
			{"A", "arm64"},
			{"default_shell", "/bin/bash"},
			{"min_duration", "10"},
			{"max_duration", "3600"},
			{"relay", "wss://relay1.example"},
			{"relay", "wss://relay2.example"},
		},
	}

	w := decodeWorkerEvent(ev)
	if w.Name != "test-worker" {
		t.Errorf("name: %q", w.Name)
	}
	if w.Description != "A test worker" {
		t.Errorf("description: %q", w.Description)
	}
	if w.MaxConcurrent != 5 {
		t.Errorf("max_concurrent: %d", w.MaxConcurrent)
	}
	if w.Architecture != "arm64" {
		t.Errorf("arch: %q", w.Architecture)
	}
	if w.DefaultShell != "/bin/bash" {
		t.Errorf("shell: %q", w.DefaultShell)
	}
	if w.MinDuration != 10 {
		t.Errorf("min_duration: %d", w.MinDuration)
	}
	if w.MaxDuration != 3600 {
		t.Errorf("max_duration: %d", w.MaxDuration)
	}
	if len(w.PreferredRelays) != 2 {
		t.Fatalf("relays: %v", w.PreferredRelays)
	}
	if w.CreatedAt != 1700000000 {
		t.Errorf("created_at: %d", w.CreatedAt)
	}
}

func TestDecodeWorkerEvent_Software(t *testing.T) {
	ev := nostr.Event{
		Kind: nostr.Kind(KindWorkerAdvertisement),
		Tags: nostr.Tags{
			{"S", "python", "3.12", "/usr/bin/python3"},
			{"S", "node", "20.0"},
			{"S", "curl"},
		},
	}
	w := decodeWorkerEvent(ev)
	if len(w.Software) != 3 {
		t.Fatalf("software count: %d", len(w.Software))
	}
	if w.Software[0].Name != "python" || w.Software[0].Version != "3.12" || w.Software[0].Path != "/usr/bin/python3" {
		t.Errorf("sw[0]: %+v", w.Software[0])
	}
	if w.Software[1].Path != "" {
		t.Errorf("sw[1] path should be empty: %q", w.Software[1].Path)
	}
	if w.Software[2].Version != "" {
		t.Errorf("sw[2] version should be empty: %q", w.Software[2].Version)
	}
}

func TestDecodeWorkerEvent_Prices(t *testing.T) {
	ev := nostr.Event{
		Kind: nostr.Kind(KindWorkerAdvertisement),
		Tags: nostr.Tags{
			{"price", "https://mint.example", "10", "sat"},
		},
	}
	w := decodeWorkerEvent(ev)
	if len(w.Prices) != 1 {
		t.Fatalf("prices: %d", len(w.Prices))
	}
	p := w.Prices[0]
	if p.MintURL != "https://mint.example" || p.PricePerSecond != "10" || p.Unit != "sat" {
		t.Errorf("price: %+v", p)
	}
}

func TestDecodeWorkerEvent_EmptyContent(t *testing.T) {
	ev := nostr.Event{Kind: nostr.Kind(KindWorkerAdvertisement)}
	w := decodeWorkerEvent(ev)
	if w.Name != "" || w.Description != "" || w.MaxConcurrent != 0 {
		t.Errorf("expected empty fields for empty content: %+v", w)
	}
}

func TestDecodeWorkerEvent_InvalidContentJSON(t *testing.T) {
	ev := nostr.Event{
		Kind:    nostr.Kind(KindWorkerAdvertisement),
		Content: "not json",
	}
	w := decodeWorkerEvent(ev)
	if w.Name != "" {
		t.Errorf("expected empty name for invalid JSON, got %q", w.Name)
	}
}

func TestDecodeWorkerEvent_ShortTags(t *testing.T) {
	ev := nostr.Event{
		Kind: nostr.Kind(KindWorkerAdvertisement),
		Tags: nostr.Tags{
			{"S"},           // too short, should skip
			{"price", "a"},  // too short for price (needs 4)
			{"A", "x86_64"}, // valid
		},
	}
	w := decodeWorkerEvent(ev)
	if w.Architecture != "x86_64" {
		t.Errorf("arch: %q", w.Architecture)
	}
	if len(w.Software) != 0 {
		t.Errorf("should have no software, got %d", len(w.Software))
	}
	if len(w.Prices) != 0 {
		t.Errorf("should have no prices, got %d", len(w.Prices))
	}
}

// ─── decodeJobStatusEvent ─────────────────────────────────────────────────────

func TestDecodeJobStatusEvent(t *testing.T) {
	ev := nostr.Event{
		Kind:      nostr.Kind(KindJobStatus),
		CreatedAt: nostr.Timestamp(1700000000),
		Content:   "Running step 3...",
		Tags: nostr.Tags{
			{"d", "job-123"},
			{"status", "running"},
			{"p", "client-pk"},
			{"queue_position", "2"},
		},
	}
	s := decodeJobStatusEvent(ev)
	if s.JobRequestID != "job-123" {
		t.Errorf("job_request_id: %q", s.JobRequestID)
	}
	if s.Status != "running" {
		t.Errorf("status: %q", s.Status)
	}
	if s.Log != "Running step 3..." {
		t.Errorf("log: %q", s.Log)
	}
	if s.ClientPubKey != "client-pk" {
		t.Errorf("client: %q", s.ClientPubKey)
	}
	if s.QueuePosition != 2 {
		t.Errorf("queue_position: %d", s.QueuePosition)
	}
}

// ─── decodeJobResultEvent ─────────────────────────────────────────────────────

func TestDecodeJobResultEvent(t *testing.T) {
	ev := nostr.Event{
		Kind:      nostr.Kind(KindJobResult),
		CreatedAt: nostr.Timestamp(1700000000),
		Tags: nostr.Tags{
			{"e", "job-456"},
			{"success", "true"},
			{"exit_code", "0"},
			{"duration", "42"},
			{"stdout", "https://blossom.example/stdout.txt"},
			{"stderr", "https://blossom.example/stderr.txt"},
			{"change", "cashuAtoken..."},
			{"error", ""},
		},
	}
	r := decodeJobResultEvent(ev)
	if r.JobRequestID != "job-456" {
		t.Errorf("job: %q", r.JobRequestID)
	}
	if !r.Success {
		t.Error("expected success=true")
	}
	if r.ExitCode != 0 {
		t.Errorf("exit_code: %d", r.ExitCode)
	}
	if r.DurationSecs != 42 {
		t.Errorf("duration: %d", r.DurationSecs)
	}
	if r.StdoutURL != "https://blossom.example/stdout.txt" {
		t.Errorf("stdout: %q", r.StdoutURL)
	}
	if r.StderrURL != "https://blossom.example/stderr.txt" {
		t.Errorf("stderr: %q", r.StderrURL)
	}
	if r.ChangeToken != "cashuAtoken..." {
		t.Errorf("change: %q", r.ChangeToken)
	}
}

func TestDecodeJobResultEvent_Failed(t *testing.T) {
	ev := nostr.Event{
		Kind: nostr.Kind(KindJobResult),
		Tags: nostr.Tags{
			{"e", "job-789"},
			{"success", "false"},
			{"exit_code", "1"},
			{"error", "command not found"},
		},
	}
	r := decodeJobResultEvent(ev)
	if r.Success {
		t.Error("expected success=false")
	}
	if r.ExitCode != 1 {
		t.Errorf("exit_code: %d", r.ExitCode)
	}
	if r.ErrorMsg != "command not found" {
		t.Errorf("error: %q", r.ErrorMsg)
	}
}

// ─── JSON round-trip for structs ──────────────────────────────────────────────

func TestWorker_JSONRoundTrip(t *testing.T) {
	w := Worker{
		PubKey:        "pk1",
		Name:          "test",
		MaxConcurrent: 3,
		Software:      []Software{{Name: "go", Version: "1.22"}},
		Prices:        []PriceEntry{{MintURL: "https://mint", PricePerSecond: "5", Unit: "sat"}},
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Worker
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Name != w.Name || decoded.MaxConcurrent != w.MaxConcurrent {
		t.Errorf("mismatch: %+v", decoded)
	}
}

func TestJobResult_JSONRoundTrip(t *testing.T) {
	r := JobResult{JobRequestID: "j1", Success: true, ExitCode: 0, DurationSecs: 10}
	b, _ := json.Marshal(r)
	var decoded JobResult
	json.Unmarshal(b, &decoded)
	if decoded != r {
		t.Errorf("mismatch: %+v vs %+v", r, decoded)
	}
}

func TestJobStatus_JSONRoundTrip(t *testing.T) {
	s := JobStatus{JobRequestID: "j1", Status: StatusRunning, QueuePosition: 3}
	b, _ := json.Marshal(s)
	var decoded JobStatus
	json.Unmarshal(b, &decoded)
	if decoded != s {
		t.Errorf("mismatch")
	}
}

// ─── MarshalJSON ──────────────────────────────────────────────────────────────

func TestMarshalJSON(t *testing.T) {
	out := MarshalJSON(map[string]int{"a": 1})
	var raw map[string]int
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if raw["a"] != 1 {
		t.Errorf("unexpected: %v", raw)
	}
}

// ─── SubmitJob validation ─────────────────────────────────────────────────────

func TestSubmitJob_Validation(t *testing.T) {
	tests := []struct {
		name string
		req  JobRequest
	}{
		{"no worker", JobRequest{Command: "echo", Payment: "token"}},
		{"no command", JobRequest{WorkerPubKey: "pk1", Payment: "token"}},
		{"no payment", JobRequest{WorkerPubKey: "pk1", Command: "echo"}},
	}
	for _, tt := range tests {
		_, err := SubmitJob(nil, nil, nil, nil, tt.req)
		if err == nil {
			t.Errorf("%s: expected error", tt.name)
		}
	}
}
