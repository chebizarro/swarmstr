package loom

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/testutil"
)

// publishWorkerAd publishes a kind 10100 worker advertisement event to the test relay.
func publishWorkerAd(t *testing.T, pool *nostr.Pool, url string, kp testutil.TestKeyPair, name, description string) string {
	t.Helper()

	content, _ := json.Marshal(map[string]any{
		"name":                name,
		"description":         description,
		"max_concurrent_jobs": 5,
	})

	evt := nostr.Event{
		Kind:      nostr.Kind(KindWorkerAdvertisement),
		CreatedAt: nostr.Now(),
		Content:   string(content),
		Tags: nostr.Tags{
			{"S", "bash", "5.2", "/bin/bash"},
			{"arch", "x86_64"},
			{"shell", "bash"},
		},
	}
	kp.SignEvent(t, &evt)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for result := range pool.PublishMany(ctx, []string{url}, evt) {
		if result.Error != nil {
			t.Fatalf("publish worker ad: %v", result.Error)
		}
	}
	return evt.ID.Hex()
}

// ─── ListWorkers ─────────────────────────────────────────────────────────────

func TestListWorkers_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})

	kp1 := testutil.NewTestKeyPair(t)
	kp2 := testutil.NewTestKeyPair(t)

	publishWorkerAd(t, pool, url, kp1, "Worker Alpha", "does stuff")
	publishWorkerAd(t, pool, url, kp2, "Worker Beta", "does other stuff")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	workers, err := ListWorkers(ctx, pool, []string{url}, 10)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) < 2 {
		t.Fatalf("expected at least 2 workers, got %d", len(workers))
	}

	names := map[string]bool{}
	for _, w := range workers {
		names[w.Name] = true
		if w.MaxConcurrent != 5 {
			t.Errorf("worker %s: max_concurrent=%d, want 5", w.Name, w.MaxConcurrent)
		}
	}
	if !names["Worker Alpha"] || !names["Worker Beta"] {
		t.Errorf("missing workers: got %v", names)
	}
}

func TestListWorkers_Empty(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	workers, err := ListWorkers(ctx, pool, []string{url}, 10)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) != 0 {
		t.Errorf("expected 0 workers, got %d", len(workers))
	}
}

func TestListWorkers_DefaultLimit(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// limit=0 should default to 20
	workers, err := ListWorkers(ctx, pool, []string{url}, 0)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	_ = workers // just ensure no error
}

// ─── SubmitJob ───────────────────────────────────────────────────────────────

func TestSubmitJob_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	jobID, err := SubmitJob(ctx, pool, kp.Keyer, []string{url}, JobRequest{
		WorkerPubKey: "deadbeef01234567deadbeef01234567deadbeef01234567deadbeef01234567",
		Command:      "echo hello",
		Args:         []string{"-n", "hello"},
		Env:          map[string]string{"FOO": "bar"},
		Secrets:      map[string]string{"TOKEN": "secret123"},
		Payment:      "cashuA1234...",
		Stdin:        "input data",
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected non-empty job ID")
	}
}

// ─── GetJobStatus ────────────────────────────────────────────────────────────

func TestGetJobStatus_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Publish a fake job status event (kind 30100)
	jobRequestID := "aabbccdd01234567aabbccdd01234567aabbccdd01234567aabbccdd01234567"
	evt := nostr.Event{
		Kind:      nostr.Kind(KindJobStatus),
		CreatedAt: nostr.Now(),
		Content:   "compiling...", // Log is decoded from Content, not a tag
		Tags: nostr.Tags{
			{"e", jobRequestID},
			{"d", jobRequestID}, // JobRequestID decoded from d tag
			{"status", StatusRunning},
			{"queue_position", "0"},
		},
	}
	kp.SignEvent(t, &evt)

	for result := range pool.PublishMany(ctx, []string{url}, evt) {
		if result.Error != nil {
			t.Fatalf("publish status: %v", result.Error)
		}
	}

	status, err := GetJobStatus(ctx, pool, []string{url}, jobRequestID)
	if err != nil {
		t.Fatalf("GetJobStatus: %v", err)
	}
	if status.Status != StatusRunning {
		t.Errorf("status: %q", status.Status)
	}
	if status.Log != "compiling..." {
		t.Errorf("log: %q", status.Log)
	}
	if status.JobRequestID != jobRequestID {
		t.Errorf("job request ID: %q", status.JobRequestID)
	}
}

func TestGetJobStatus_NotFound(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := GetJobStatus(ctx, pool, []string{url}, "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent job")
	}
}

// ─── WaitForResult ───────────────────────────────────────────────────────────

func TestWaitForResult_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Publish a fake job result event (kind 5101)
	jobRequestID := "eeff001122334455eeff001122334455eeff001122334455eeff001122334455"
	evt := nostr.Event{
		Kind:      nostr.Kind(KindJobResult),
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"e", jobRequestID},
			{"exit_code", "0"},
			{"duration", "42"},
			{"stdout", "https://blossom.example/stdout.txt"},
			{"stderr", "https://blossom.example/stderr.txt"},
		},
		Content: `{"success":true}`,
	}
	kp.SignEvent(t, &evt)

	for result := range pool.PublishMany(ctx, []string{url}, evt) {
		if result.Error != nil {
			t.Fatalf("publish result: %v", result.Error)
		}
	}

	result, err := WaitForResult(ctx, pool, []string{url}, jobRequestID, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitForResult: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit_code: %d", result.ExitCode)
	}
	if result.StdoutURL != "https://blossom.example/stdout.txt" {
		t.Errorf("stdout: %q", result.StdoutURL)
	}
}

func TestWaitForResult_Timeout(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := WaitForResult(ctx, pool, []string{url}, "nonexistent", 500*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
}

// ─── CancelJob ───────────────────────────────────────────────────────────────

func TestCancelJob_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	eventID, err := CancelJob(ctx, pool, kp.Keyer, []string{url},
		"aabb01234567890aabb01234567890aabb01234567890aabb01234567890aabb",
		"ccdd01234567890ccdd01234567890ccdd01234567890ccdd01234567890ccdd",
	)
	if err != nil {
		t.Fatalf("CancelJob: %v", err)
	}
	if eventID == "" {
		t.Fatal("expected non-empty event ID")
	}
}

// ─── publishEvent ────────────────────────────────────────────────────────────

func TestPublishEvent_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	evt := nostr.Event{
		Kind:      nostr.Kind(KindJobCancellation),
		CreatedAt: nostr.Now(),
		Content:   "test",
	}
	kp.SignEvent(t, &evt)

	id, err := publishEvent(ctx, pool, []string{url}, evt)
	if err != nil {
		t.Fatalf("publishEvent: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty event ID")
	}
}
