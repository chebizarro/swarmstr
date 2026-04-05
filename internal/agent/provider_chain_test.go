package agent

import (
	"context"
	"fmt"
	"testing"
)

func TestBuildChatProviderForModel_Anthropic(t *testing.T) {
	cp, err := BuildChatProviderForModel("claude-sonnet-4-5", "test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cp.(*AnthropicChatProvider); !ok {
		t.Errorf("expected *AnthropicChatProvider, got %T", cp)
	}
}

func TestBuildChatProviderForModel_Gemini(t *testing.T) {
	cp, err := BuildChatProviderForModel("gemini-2.0-flash", "test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cp.(*GeminiChatProvider); !ok {
		t.Errorf("expected *GeminiChatProvider, got %T", cp)
	}
}

func TestBuildChatProviderForModel_OpenAI(t *testing.T) {
	cp, err := BuildChatProviderForModel("gpt-4o", "test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cp.(*OpenAIChatProviderChat); !ok {
		t.Errorf("expected *OpenAIChatProviderChat, got %T", cp)
	}
}

func TestBuildChatProviderForModel_CopilotCLI(t *testing.T) {
	p, err := BuildChatProviderForModel("copilot-cli", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := p.(*CopilotCLIChatProvider); !ok {
		t.Errorf("expected *CopilotCLIChatProvider, got %T", p)
	}
}

func TestBuildChatProviderForModel_CopilotCLIWithModel(t *testing.T) {
	p, err := BuildChatProviderForModel("copilot-cli/claude-sonnet-4", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cp, ok := p.(*CopilotCLIChatProvider)
	if !ok {
		t.Fatalf("expected *CopilotCLIChatProvider, got %T", p)
	}
	if cp.Model != "claude-sonnet-4" {
		t.Errorf("model = %q, want claude-sonnet-4", cp.Model)
	}
}

func TestBuildChatProviderForModel_Unknown(t *testing.T) {
	_, err := BuildChatProviderForModel("unknown-model-xyz", "", "")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestFallbackChainProvider_Generate(t *testing.T) {
	mock := &mockChatProvider{
		responses: []*LLMResponse{
			{Content: "ok from fallback chain"},
		},
	}

	chain := NewFallbackChain([]FallbackCandidate{
		{Name: "primary", Model: "test", Provider: mock},
	}, NewCooldownTracker())

	fcp := &FallbackChainProvider{
		chain:     chain,
		logPrefix: "test",
	}

	result, err := fcp.Generate(context.Background(), Turn{UserText: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if mock.callCount != 1 {
		t.Errorf("expected mock to be called once, got %d", mock.callCount)
	}
	if result.Text != "ok from fallback chain" {
		t.Errorf("expected 'ok from fallback chain', got %q", result.Text)
	}
}

func TestRoutedProvider_UsesLightModel(t *testing.T) {
	primaryCalled := false
	lightCalled := false

	primary := &mockProvider{
		fn: func(ctx context.Context, turn Turn) (ProviderResult, error) {
			primaryCalled = true
			return ProviderResult{Text: "primary"}, nil
		},
	}
	light := &mockProvider{
		fn: func(ctx context.Context, turn Turn) (ProviderResult, error) {
			lightCalled = true
			return ProviderResult{Text: "light"}, nil
		},
	}

	rp := &RoutedProvider{
		primary:      primary,
		light:        light,
		router:       NewModelRouter("light-model", 0.3),
		primaryModel: "primary-model",
	}

	// Simple greeting should route to light model.
	result, err := rp.Generate(context.Background(), Turn{UserText: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !lightCalled {
		t.Error("expected light model to be called for simple greeting")
	}
	if primaryCalled {
		t.Error("expected primary model NOT to be called for simple greeting")
	}
	if result.Text != "light" {
		t.Errorf("expected 'light', got %q", result.Text)
	}
}

func TestRoutedProvider_UsesPrimaryForComplex(t *testing.T) {
	primaryCalled := false
	lightCalled := false

	primary := &mockProvider{
		fn: func(ctx context.Context, turn Turn) (ProviderResult, error) {
			primaryCalled = true
			return ProviderResult{Text: "primary"}, nil
		},
	}
	light := &mockProvider{
		fn: func(ctx context.Context, turn Turn) (ProviderResult, error) {
			lightCalled = true
			return ProviderResult{Text: "light"}, nil
		},
	}

	rp := &RoutedProvider{
		primary:      primary,
		light:        light,
		router:       NewModelRouter("light-model", 0.3),
		primaryModel: "primary-model",
	}

	// Complex request should route to primary model (code block triggers heavy model).
	result, err := rp.Generate(context.Background(), Turn{
		UserText: "Please analyze the following code:\n```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\nExplain the architecture of this system, including all the design patterns.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !primaryCalled {
		t.Error("expected primary model to be called for complex request")
	}
	if lightCalled {
		t.Error("expected light model NOT to be called for complex request")
	}
	if result.Text != "primary" {
		t.Errorf("expected 'primary', got %q", result.Text)
	}
}

func TestRoutedProvider_UsesPrimaryWhenHistoryShowsToolCalls(t *testing.T) {
	primaryCalled := false
	lightCalled := false

	rp := &RoutedProvider{
		primary: &mockProvider{fn: func(ctx context.Context, turn Turn) (ProviderResult, error) {
			primaryCalled = true
			return ProviderResult{Text: "primary"}, nil
		}},
		light: &mockProvider{fn: func(ctx context.Context, turn Turn) (ProviderResult, error) {
			lightCalled = true
			return ProviderResult{Text: "light"}, nil
		}},
		router:       NewModelRouter("light-model", 0.05),
		primaryModel: "primary-model",
	}

	_, err := rp.Generate(context.Background(), Turn{
		UserText: "continue",
		History:  []ConversationMessage{{Role: "assistant", Content: "running tools", ToolCalls: []ToolCallRef{{ID: "1", Name: "bash"}, {ID: "2", Name: "read"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !primaryCalled {
		t.Fatal("expected primary model to be used when tool history raises complexity")
	}
	if lightCalled {
		t.Fatal("expected light model to stay unused when tool history is present")
	}
}

func TestRoutedProvider_UsesPrimaryWhenTurnHasImages(t *testing.T) {
	primaryCalled := false
	lightCalled := false

	rp := &RoutedProvider{
		primary: &mockProvider{fn: func(ctx context.Context, turn Turn) (ProviderResult, error) {
			primaryCalled = true
			return ProviderResult{Text: "primary"}, nil
		}},
		light: &mockProvider{fn: func(ctx context.Context, turn Turn) (ProviderResult, error) {
			lightCalled = true
			return ProviderResult{Text: "light"}, nil
		}},
		router:       NewModelRouter("light-model", 0.9),
		primaryModel: "primary-model",
	}

	_, err := rp.Generate(context.Background(), Turn{
		UserText: "describe this",
		Images:   []ImageRef{{URL: "https://example.com/cat.png", MimeType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !primaryCalled {
		t.Fatal("expected primary model for image turns")
	}
	if lightCalled {
		t.Fatal("expected light model to stay unused for image turns")
	}
}

// mockProvider implements Provider for testing.
type mockProvider struct {
	fn func(ctx context.Context, turn Turn) (ProviderResult, error)
}

func (m *mockProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	if m.fn != nil {
		return m.fn(ctx, turn)
	}
	return ProviderResult{}, fmt.Errorf("not implemented")
}
