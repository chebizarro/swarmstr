package agent

import (
	"context"
	"strings"

	"metiq/internal/policy"
)

type toolPolicyContextKey struct{}

type toolPolicyContextValue struct {
	policy  *policy.ToolPolicy
	agentID string
}

// ContextWithToolPolicy stores the per-tool policy used by central tool
// execution. A nil policy returns ctx unchanged so legacy call sites keep their
// current behavior.
func ContextWithToolPolicy(ctx context.Context, p *policy.ToolPolicy, agentID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, toolPolicyContextKey{}, toolPolicyContextValue{policy: p, agentID: strings.TrimSpace(agentID)})
}

// ToolPolicyFromContext returns the policy and optional agent ID associated
// with this tool execution context.
func ToolPolicyFromContext(ctx context.Context) (*policy.ToolPolicy, string) {
	if ctx == nil {
		return nil, ""
	}
	v, _ := ctx.Value(toolPolicyContextKey{}).(toolPolicyContextValue)
	return v.policy, v.agentID
}
