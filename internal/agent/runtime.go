package agent

import (
	"context"
	"fmt"
	"strings"
)

type Turn struct {
	SessionID string
	UserText  string
	Context   string
	// Images carries vision content for multi-modal providers.
	// Each element is either a URL reference or inline base64 data.
	// Text-only providers (echo, http, ollama) ignore this field.
	Images []ImageRef
}

// ImageRef is a resolved image reference for passing to vision providers.
// Exactly one of URL or Base64 is set.
type ImageRef struct {
	URL      string // remote URL; provider may pass as image_url reference
	Base64   string // base64-encoded binary (no data URI prefix)
	MimeType string // e.g. "image/jpeg", "image/png", "image/webp"
}

type TurnResult struct {
	Text       string
	ToolTraces []ToolTrace
}

type Runtime interface {
	ProcessTurn(context.Context, Turn) (TurnResult, error)
}

// StreamingRuntime extends Runtime with incremental text delivery.
// Implementations call onChunk for each text token (or small group) as it
// arrives from the provider, enabling real-time display of partial responses.
type StreamingRuntime interface {
	Runtime
	// ProcessTurnStreaming processes a turn and delivers text chunks via onChunk
	// as they arrive.  The returned TurnResult is the complete response including
	// ToolTraces.  onChunk may be nil (degrades to buffered delivery).
	ProcessTurnStreaming(ctx context.Context, turn Turn, onChunk func(text string)) (TurnResult, error)
}

type ProviderRuntime struct {
	provider Provider
	tools    ToolExecutor
}

func NewProviderRuntime(provider Provider, tools ToolExecutor) (*ProviderRuntime, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	return &ProviderRuntime{provider: provider, tools: tools}, nil
}

func NewRuntimeFromEnv(tools ToolExecutor) (Runtime, error) {
	provider, err := NewProviderFromEnv()
	if err != nil {
		return nil, err
	}
	return NewProviderRuntime(provider, tools)
}

// Filtered returns a Runtime that only permits tool calls in the allowed set.
// If allowed is nil, all tools are permitted (equivalent to the original runtime).
// A non-nil empty map means deny-all (strict fail-closed).
// Only ProviderRuntime instances are filtered; other Runtime implementations are
// returned unchanged.
func (r *ProviderRuntime) Filtered(allowed map[string]bool) Runtime {
	if allowed == nil {
		return r
	}
	return &ProviderRuntime{
		provider: r.provider,
		tools:    &ProfileFilteredExecutor{Base: r.tools, Allowed: allowed},
	}
}

func (r *ProviderRuntime) ProcessTurn(ctx context.Context, turn Turn) (TurnResult, error) {
	turn.UserText = strings.TrimSpace(turn.UserText)
	if turn.UserText == "" {
		return TurnResult{}, fmt.Errorf("empty user turn")
	}
	gen, err := r.provider.Generate(ctx, turn)
	if err != nil {
		return TurnResult{}, err
	}
	return r.buildResult(ctx, gen)
}

// ProcessTurnStreaming processes a turn with incremental text delivery.
// If the underlying provider implements StreamingProvider, text tokens are
// delivered via onChunk as they arrive; otherwise Generate() is called and
// the full text is delivered in one onChunk call.  Tool calls are executed
// after streaming completes using the configured ToolExecutor.
func (r *ProviderRuntime) ProcessTurnStreaming(ctx context.Context, turn Turn, onChunk func(text string)) (TurnResult, error) {
	turn.UserText = strings.TrimSpace(turn.UserText)
	if turn.UserText == "" {
		return TurnResult{}, fmt.Errorf("empty user turn")
	}

	var gen ProviderResult
	var err error

	if sp, ok := r.provider.(StreamingProvider); ok {
		gen, err = sp.Stream(ctx, turn, onChunk)
	} else {
		gen, err = r.provider.Generate(ctx, turn)
		if err == nil && onChunk != nil {
			onChunk(gen.Text)
		}
	}
	if err != nil {
		return TurnResult{}, err
	}

	return r.buildResult(ctx, gen)
}

// buildResult executes any tool calls from gen and assembles the TurnResult.
func (r *ProviderRuntime) buildResult(ctx context.Context, gen ProviderResult) (TurnResult, error) {
	result := TurnResult{Text: strings.TrimSpace(gen.Text)}
	for _, call := range gen.ToolCalls {
		trace := ToolTrace{Call: call}
		if r.tools == nil {
			trace.Error = "no tool executor configured"
			result.ToolTraces = append(result.ToolTraces, trace)
			continue
		}
		value, err := r.tools.Execute(ctx, call)
		if err != nil {
			trace.Error = err.Error()
		} else {
			trace.Result = value
		}
		result.ToolTraces = append(result.ToolTraces, trace)
	}

	if result.Text == "" && len(result.ToolTraces) == 0 {
		return TurnResult{}, fmt.Errorf("provider returned empty response")
	}
	if result.Text == "" && len(result.ToolTraces) > 0 {
		result.Text = "tool execution complete"
	}
	return result, nil
}
