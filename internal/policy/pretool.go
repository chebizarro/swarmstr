package policy

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// PreToolAction is returned by PreToolUse hooks.
type PreToolAction string

const (
	PreToolAllow PreToolAction = "allow"
	PreToolWarn  PreToolAction = "warn"
	PreToolAsk   PreToolAction = "ask"
	PreToolBlock PreToolAction = "block"
)

// PreToolContext is provided to hooks before a tool is executed.
type PreToolContext struct {
	ToolPolicyRequest
	SessionID      string             `json:"session_id,omitempty"`
	TurnID         string             `json:"turn_id,omitempty"`
	PolicyDecision ToolPolicyDecision `json:"policy_decision,omitempty"`
	Metadata       map[string]any     `json:"metadata,omitempty"`
}

// PreToolResult is a hook verdict.
type PreToolResult struct {
	Action      PreToolAction  `json:"action"`
	Message     string         `json:"message,omitempty"`
	MutatedArgs map[string]any `json:"mutated_args,omitempty"`
}

// PreToolHook is implemented by native hook providers.
type PreToolHook interface {
	ID() string
	Priority() int
	ValidatePreToolUse(context.Context, PreToolContext) (PreToolResult, error)
}

// PreToolHookFunc adapts a function into a PreToolHook.
type PreToolHookFunc struct {
	HookID       string
	HookPriority int
	Fn           func(context.Context, PreToolContext) (PreToolResult, error)
}

func (f PreToolHookFunc) ID() string    { return f.HookID }
func (f PreToolHookFunc) Priority() int { return f.HookPriority }
func (f PreToolHookFunc) ValidatePreToolUse(ctx context.Context, payload PreToolContext) (PreToolResult, error) {
	if f.Fn == nil {
		return PreToolResult{Action: PreToolAllow}, nil
	}
	return f.Fn(ctx, payload)
}

// PreToolGate composes the per-tool policy decision with extensible hooks.
type PreToolGate struct {
	Policy ToolPolicy
	Hooks  []PreToolHook
}

// Evaluate returns the final pre-execution decision. Policy deny always blocks
// before hooks; ask remains ask unless a hook blocks. Hook block wins over warn.
func (g PreToolGate) Evaluate(ctx context.Context, payload PreToolContext) (PreToolResult, error) {
	payload.PolicyDecision = g.Policy.Evaluate(payload.ToolPolicyRequest)
	switch payload.PolicyDecision.Action {
	case ToolPolicyDeny:
		return PreToolResult{Action: PreToolBlock, Message: payload.PolicyDecision.Reason}, nil
	case ToolPolicyAsk:
		payload.Metadata = cloneMetadata(payload.Metadata)
		payload.Metadata["policy_requires_approval"] = true
	}

	hooks := append([]PreToolHook(nil), g.Hooks...)
	sort.SliceStable(hooks, func(i, j int) bool {
		if hooks[i].Priority() == hooks[j].Priority() {
			return hooks[i].ID() < hooks[j].ID()
		}
		return hooks[i].Priority() < hooks[j].Priority()
	})

	final := PreToolResult{Action: PreToolAllow}
	if payload.PolicyDecision.Action == ToolPolicyAsk {
		final.Action = PreToolAsk
		final.Message = payload.PolicyDecision.Reason
	}
	for _, hook := range hooks {
		if hook == nil {
			continue
		}
		res, err := hook.ValidatePreToolUse(ctx, payload)
		if err != nil {
			return PreToolResult{Action: PreToolBlock, Message: fmt.Sprintf("pre-tool hook %q failed: %v", hook.ID(), err)}, err
		}
		switch normalizePreToolAction(res.Action) {
		case PreToolBlock:
			if strings.TrimSpace(res.Message) == "" {
				res.Message = fmt.Sprintf("blocked by pre-tool hook %q", hook.ID())
			}
			res.Action = PreToolBlock
			return res, nil
		case PreToolWarn:
			if final.Action == PreToolAllow {
				final = res
				final.Action = PreToolWarn
			}
		case PreToolAsk:
			if final.Action == PreToolAllow || final.Action == PreToolWarn {
				final = res
				final.Action = PreToolAsk
			}
		case PreToolAllow:
			if len(res.MutatedArgs) > 0 {
				final.MutatedArgs = mergeArgs(final.MutatedArgs, res.MutatedArgs)
			}
		}
	}
	return final, nil
}

func normalizePreToolAction(action PreToolAction) PreToolAction {
	switch action {
	case "", PreToolAllow:
		return PreToolAllow
	case PreToolWarn, PreToolAsk, PreToolBlock:
		return action
	default:
		return PreToolBlock
	}
}

func cloneMetadata(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeArgs(base, patch map[string]any) map[string]any {
	if len(base) == 0 {
		base = map[string]any{}
	}
	for k, v := range patch {
		base[k] = v
	}
	return base
}
