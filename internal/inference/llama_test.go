package inference

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
)

// ─── SlotID Tests ─────────────────────────────────────────────────────────────

func TestSlotID_ReturnsValidRange(t *testing.T) {
	testCases := []struct {
		name    string
		agentID string
	}{
		{"pubkey1", "npub1abc123"},
		{"pubkey2", "npub1def456"},
		{"pubkey3", "npub1ghi789"},
		{"short", "a"},
		{"long", "npub1verylongpublickeywithlotsofcharacterstotest"},
		{"special", "test@example.com"},
		{"uuid", "550e8400-e29b-41d4-a716-446655440000"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			slot := SlotID(tc.agentID)
			if slot < 0 || slot >= SlotCount {
				t.Errorf("SlotID(%q) = %d, want value in range [0, %d]", tc.agentID, slot, SlotCount-1)
			}
		})
	}
}

func TestSlotID_Deterministic(t *testing.T) {
	agentID := "npub1test123"

	// Call SlotID multiple times with the same input
	results := make([]int, 100)
	for i := 0; i < 100; i++ {
		results[i] = SlotID(agentID)
	}

	// All results should be identical
	first := results[0]
	for i, slot := range results {
		if slot != first {
			t.Errorf("SlotID is not deterministic: call %d returned %d, expected %d", i, slot, first)
		}
	}
}

func TestSlotID_DifferentInputsProduceDifferentSlots(t *testing.T) {
	// Test that different agent IDs produce different slots (with high probability)
	slots := make(map[int]string)
	agents := []string{
		"npub1aaa",
		"npub1bbb",
		"npub1ccc",
		"npub1ddd",
		"npub1eee",
		"npub1fff",
	}

	for _, agentID := range agents {
		slot := SlotID(agentID)
		if existing, ok := slots[slot]; ok {
			t.Logf("Collision: %q and %q both map to slot %d (this is expected occasionally)", agentID, existing, slot)
		}
		slots[slot] = agentID
	}

	// We should have at least 2 different slots from 6 inputs
	if len(slots) < 2 {
		t.Errorf("Expected at least 2 different slots from %d inputs, got %d", len(agents), len(slots))
	}
}

func TestSlotID_EmptyAgentID(t *testing.T) {
	slot := SlotID("")
	if slot != DynamicSlot {
		t.Errorf("SlotID(\"\") = %d, want %d (DynamicSlot)", slot, DynamicSlot)
	}
}

// ─── BuildLlamaRequest Tests ──────────────────────────────────────────────────

func TestBuildLlamaRequest_SetsAllFields(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	agentID := "npub1test"
	model := "llama-3-8b"
	maxTokens := 2048

	req := BuildLlamaRequest(messages, agentID, model, maxTokens)

	if req.Model != model {
		t.Errorf("Model = %q, want %q", req.Model, model)
	}
	if len(req.Messages) != len(messages) {
		t.Errorf("Messages length = %d, want %d", len(req.Messages), len(messages))
	}
	if req.MaxTokens != maxTokens {
		t.Errorf("MaxTokens = %d, want %d", req.MaxTokens, maxTokens)
	}
	if !req.Stream {
		t.Error("Stream = false, want true")
	}
	if !req.CachePrompt {
		t.Error("CachePrompt = false, want true")
	}
}

func TestBuildLlamaRequest_SetsValidSlot(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	agentID := "npub1test"

	req := BuildLlamaRequest(messages, agentID, "llama-3-8b", 2048)

	if req.IDSlot < 0 || req.IDSlot >= SlotCount {
		t.Errorf("IDSlot = %d, want value in range [0, %d]", req.IDSlot, SlotCount-1)
	}
}

func TestBuildLlamaRequest_EmptyAgentIDProducesDynamicSlot(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}

	req := BuildLlamaRequest(messages, "", "llama-3-8b", 2048)

	if req.IDSlot != DynamicSlot {
		t.Errorf("IDSlot = %d, want %d (DynamicSlot) for empty agentID", req.IDSlot, DynamicSlot)
	}
}

func TestBuildLlamaRequest_CachePromptAlwaysTrue(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}

	// Test with agent ID
	req1 := BuildLlamaRequest(messages, "npub1test", "llama-3-8b", 2048)
	if !req1.CachePrompt {
		t.Error("CachePrompt = false, want true (with agent ID)")
	}

	// Test without agent ID
	req2 := BuildLlamaRequest(messages, "", "llama-3-8b", 2048)
	if !req2.CachePrompt {
		t.Error("CachePrompt = false, want true (without agent ID)")
	}
}

// ─── BuildAnthropicRequest Tests ──────────────────────────────────────────────

func TestBuildAnthropicRequest_SetsAllFields(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	model := "claude-sonnet-4"
	maxTokens := 4096

	req := BuildAnthropicRequest(messages, model, maxTokens)

	if req.Model != model {
		t.Errorf("Model = %q, want %q", req.Model, model)
	}
	if len(req.Messages) != len(messages) {
		t.Errorf("Messages length = %d, want %d", len(req.Messages), len(messages))
	}
	if req.MaxTokens != maxTokens {
		t.Errorf("MaxTokens = %d, want %d", req.MaxTokens, maxTokens)
	}
	if !req.Stream {
		t.Error("Stream = false, want true")
	}
}

// ─── JSON Serialization Tests ─────────────────────────────────────────────────

func TestLlamaRequest_JSONContainsSlotFields(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	req := BuildLlamaRequest(messages, "npub1test", "llama-3-8b", 2048)

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal LlamaRequest: %v", err)
	}

	jsonStr := string(data)

	// Check that id_slot and cache_prompt are present
	if !contains(jsonStr, "id_slot") {
		t.Error("JSON missing 'id_slot' field")
	}
	if !contains(jsonStr, "cache_prompt") {
		t.Error("JSON missing 'cache_prompt' field")
	}
}

func TestAnthropicRequest_JSONExcludesSlotFields(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	req := BuildAnthropicRequest(messages, "claude-sonnet-4", 4096)

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal AnthropicRequest: %v", err)
	}

	jsonStr := string(data)

	// Check that id_slot and cache_prompt are NOT present
	if contains(jsonStr, "id_slot") {
		t.Error("JSON contains 'id_slot' field (should not be present)")
	}
	if contains(jsonStr, "cache_prompt") {
		t.Error("JSON contains 'cache_prompt' field (should not be present)")
	}
}

// ─── Streaming Tests ─────────────────────────────────────────────────────────

func TestCompleteStream_LlamaDeliversChunksIncrementally(t *testing.T) {
	serverMayFinish := make(chan struct{})
	firstChunkWritten := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var captured LlamaRequest
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !captured.Stream {
			t.Error("request stream=false, want true")
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Accept = %q, want text/event-stream", r.Header.Get("Accept"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not flush")
		}
		_, _ = w.Write([]byte("data: {\"content\":\"Hel\"}\n\n"))
		flusher.Flush()
		close(firstChunkWritten)

		<-serverMayFinish
		_, _ = w.Write([]byte("data: {\"content\":\"lo\"}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	client := &Client{LlamaURL: srv.URL, HTTPClient: srv.Client()}
	gotFirst := make(chan struct{})
	done := make(chan error, 1)
	var chunks []string

	go func() {
		done <- client.CompleteStream(context.Background(), BackendLlama, testMessages(), "agent", "model", 32, func(chunk []byte) error {
			chunks = append(chunks, string(chunk))
			if string(chunk) == "Hel" {
				close(gotFirst)
			}
			return nil
		})
	}()

	waitFor(t, firstChunkWritten, "server to write first chunk")
	waitFor(t, gotFirst, "first chunk callback before stream end")
	close(serverMayFinish)

	if err := waitErr(t, done, "stream completion"); err != nil {
		t.Fatalf("CompleteStream error: %v", err)
	}
	if got := strings.Join(chunks, ""); got != "Hello" {
		t.Fatalf("chunks = %q, want Hello", got)
	}
}

func TestCompleteStream_AnthropicParsesContentBlockDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var captured AnthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !captured.Stream {
			t.Error("request stream=false, want true")
		}
		if got := r.Header.Get("X-API-Key"); got != "test-key" {
			t.Errorf("X-API-Key = %q, want test-key", got)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: content_block_delta\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	client := &Client{AnthropicAPIKey: "test-key", HTTPClient: rewriteHostClient(srv.Client(), srv.URL)}
	var got strings.Builder
	err := client.CompleteStream(context.Background(), BackendAnthropic, testMessages(), "ignored", "claude", 32, func(chunk []byte) error {
		_, _ = got.Write(chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteStream error: %v", err)
	}
	if got.String() != "Hi" {
		t.Fatalf("chunks = %q, want Hi", got.String())
	}
}

func TestComplete_BuffersStreamingChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"content\":\"one\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"content\":\"two\"}\n\n"))
	}))
	defer srv.Close()

	client := &Client{LlamaURL: srv.URL, HTTPClient: srv.Client()}
	got, err := client.Complete(context.Background(), BackendLlama, testMessages(), "", "model", 32)
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if string(got) != "onetwo" {
		t.Fatalf("Complete = %q, want onetwo", got)
	}
}

func TestCompleteStream_CallbackErrorStopsStream(t *testing.T) {
	wantErr := errors.New("consumer stopped")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"content\":\"chunk\"}\n\n"))
	}))
	defer srv.Close()

	client := &Client{LlamaURL: srv.URL, HTTPClient: srv.Client()}
	err := client.CompleteStream(context.Background(), BackendLlama, testMessages(), "", "model", 32, func(chunk []byte) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestCompleteStream_ContextCancellationStopsStream(t *testing.T) {
	requestCancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not flush")
		}
		_, _ = w.Write([]byte("data: {\"content\":\"first\"}\n\n"))
		flusher.Flush()

		<-r.Context().Done()
		close(requestCancelled)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{LlamaURL: srv.URL, HTTPClient: srv.Client()}
	err := client.CompleteStream(ctx, BackendLlama, testMessages(), "", "model", 32, func(chunk []byte) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	waitFor(t, requestCancelled, "server request context cancellation")
}

func TestCompleteStream_DoneTerminatesWithoutWaitingForEOF(t *testing.T) {
	requestClosed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not flush")
		}
		_, _ = w.Write([]byte("data: {\"content\":\"done\"}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()

		<-r.Context().Done()
		close(requestClosed)
	}))
	defer srv.Close()

	client := &Client{LlamaURL: srv.URL, HTTPClient: srv.Client()}
	var got strings.Builder
	err := client.CompleteStream(context.Background(), BackendLlama, testMessages(), "", "model", 32, func(chunk []byte) error {
		_, _ = got.Write(chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteStream error: %v", err)
	}
	if got.String() != "done" {
		t.Fatalf("chunks = %q, want done", got.String())
	}
	waitFor(t, requestClosed, "client to close request after [DONE]")
}

func TestCompleteStream_StreamErrorEventReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: error\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"try later\"}}\n\n"))
	}))
	defer srv.Close()

	client := &Client{LlamaURL: srv.URL, HTTPClient: srv.Client()}
	err := client.CompleteStream(context.Background(), BackendLlama, testMessages(), "", "model", 32, func(chunk []byte) error {
		t.Fatalf("unexpected chunk: %q", chunk)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "overloaded_error: try later") {
		t.Fatalf("error = %v, want streamed overload error", err)
	}
}

func TestCompleteStream_NonSSESuccessWithoutChunksErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":"not framed"}`))
	}))
	defer srv.Close()

	client := &Client{LlamaURL: srv.URL, HTTPClient: srv.Client()}
	err := client.CompleteStream(context.Background(), BackendLlama, testMessages(), "", "model", 32, func(chunk []byte) error {
		t.Fatalf("unexpected chunk: %q", chunk)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "without any chunks") {
		t.Fatalf("error = %v, want no chunks error", err)
	}
}

// ─── SlotCount Configuration Tests ────────────────────────────────────────────

func TestSlotCount_IsConfigurable(t *testing.T) {
	// Test that SlotCount can be changed without recompiling
	originalSlotCount := SlotCount
	defer func() { SlotCount = originalSlotCount }()

	// Change SlotCount
	SlotCount = 12

	// Verify SlotID respects the new count
	agentID := "npub1test"
	slot := SlotID(agentID)

	if slot < 0 || slot >= SlotCount {
		t.Errorf("SlotID(%q) = %d, want value in range [0, %d] after changing SlotCount to %d",
			agentID, slot, SlotCount-1, SlotCount)
	}
}

func TestSlotCount_AffectsDistribution(t *testing.T) {
	originalSlotCount := SlotCount
	defer func() { SlotCount = originalSlotCount }()

	// Test with different SlotCount values
	for _, count := range []int{3, 6, 12} {
		SlotCount = count

		// Generate slots for multiple agents
		slots := make(map[int]int)
		for i := 0; i < 100; i++ {
			agentID := "npub1agent" + string(rune('a'+i))
			slot := SlotID(agentID)

			if slot < 0 || slot >= SlotCount {
				t.Errorf("SlotID produced slot %d outside range [0, %d) with SlotCount=%d",
					slot, SlotCount, SlotCount)
			}
			slots[slot]++
		}

		t.Logf("SlotCount=%d: distribution across %d slots", count, len(slots))
	}
}

// ─── Helper Functions ─────────────────────────────────────────────────────────

func testMessages() []agent.LLMMessage {
	return []agent.LLMMessage{{Role: "user", Content: "Hello"}}
}

func waitFor(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitErr(t *testing.T, ch <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func rewriteHostClient(base *http.Client, rawURL string) *http.Client {
	target, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		return transport.RoundTrip(req)
	})}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
