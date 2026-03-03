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
}

type TurnResult struct {
	Text       string
	ToolTraces []ToolTrace
}

type Runtime interface {
	ProcessTurn(context.Context, Turn) (TurnResult, error)
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

func (r *ProviderRuntime) ProcessTurn(ctx context.Context, turn Turn) (TurnResult, error) {
	turn.UserText = strings.TrimSpace(turn.UserText)
	if turn.UserText == "" {
		return TurnResult{}, fmt.Errorf("empty user turn")
	}
	gen, err := r.provider.Generate(ctx, turn)
	if err != nil {
		return TurnResult{}, err
	}

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
