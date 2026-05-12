package context_test

import (
	"context"
	"testing"

	ctxengine "metiq/internal/context"
)

func TestWindowedEngineIngestAssemble(t *testing.T) {
	eng, err := ctxengine.NewEngine("windowed", "sess-1", map[string]any{"max_messages": float64(10)})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	for i, role := range []string{"user", "assistant", "user"} {
		res, err := eng.Ingest(ctx, "sess-1", ctxengine.Message{
			Role:    role,
			Content: "message text",
			ID:      "id-" + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatalf("Ingest[%d]: %v", i, err)
		}
		if !res.Ingested {
			t.Errorf("Ingest[%d]: expected Ingested=true", i)
		}
	}

	assembled, err := eng.Assemble(ctx, "sess-1", 100_000)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(assembled.Messages) != 3 {
		t.Errorf("Assemble: expected 3 messages, got %d", len(assembled.Messages))
	}
}

func TestWindowedEngineDeduplication(t *testing.T) {
	eng, err := ctxengine.NewEngine("windowed", "sess-2", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	msg := ctxengine.Message{Role: "user", Content: "hello", ID: "dup-1"}

	r1, _ := eng.Ingest(ctx, "sess-2", msg)
	r2, _ := eng.Ingest(ctx, "sess-2", msg)

	if !r1.Ingested {
		t.Error("first ingest should be accepted")
	}
	if r2.Ingested {
		t.Error("duplicate ingest should be rejected")
	}
}

func TestWindowedEngineBootstrap(t *testing.T) {
	eng, err := ctxengine.NewEngine("windowed", "sess-3", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	msgs := []ctxengine.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	br, err := eng.Bootstrap(ctx, "sess-3", msgs)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !br.Bootstrapped {
		t.Error("Bootstrap: expected Bootstrapped=true")
	}
	if br.ImportedMessages != 2 {
		t.Errorf("Bootstrap: expected 2 imported, got %d", br.ImportedMessages)
	}
}

func TestWindowedEngineSlidingWindow(t *testing.T) {
	eng, err := ctxengine.NewEngine("windowed", "sess-4", map[string]any{"max_messages": float64(3)})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = eng.Ingest(ctx, "sess-4", ctxengine.Message{
			Role:    "user",
			Content: "msg",
			ID:      string(rune('a' + i)),
		})
	}

	assembled, err := eng.Assemble(ctx, "sess-4", 0)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(assembled.Messages) != 3 {
		t.Errorf("sliding window: expected 3 messages (capped), got %d", len(assembled.Messages))
	}
}

type testActiveRecallProvider struct{}

func (testActiveRecallProvider) AssembleActiveRecall(ctx context.Context, sessionID string, latest ctxengine.Message, recent []ctxengine.Message, maxChars int) (string, error) {
	return "## Active Memory Recall\n- remembered context", nil
}

func TestWindowedEngineActiveRecallProvider(t *testing.T) {
	eng, err := ctxengine.NewEngine("windowed", "sess-active", map[string]any{
		"active_recall": testActiveRecallProvider{},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()
	_, _ = eng.Ingest(context.Background(), "sess-active", ctxengine.Message{Role: "user", Content: "what should I remember?"})
	assembled, err := eng.Assemble(context.Background(), "sess-active", 0)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if assembled.SystemPromptAddition == "" {
		t.Fatal("expected active recall prompt addition")
	}
}

func TestListContextEngines(t *testing.T) {
	engines := ctxengine.ListContextEngines()
	found := false
	for _, name := range engines {
		if name == "windowed" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListContextEngines: expected 'windowed', got %v", engines)
	}
}

func TestNewEngineUnknown(t *testing.T) {
	_, err := ctxengine.NewEngine("not-a-real-engine", "s", nil)
	if err == nil {
		t.Error("expected error for unknown engine name")
	}
}
