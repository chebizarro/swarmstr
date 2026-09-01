package nodeclient

import (
	"context"
	"fmt"

	"metiq/internal/gateway/nodepending"
)

const (
	CommandPluginSurfaceRefresh = "pluginSurface.refresh"
	CommandPluginToolsUpdate    = "pluginTools.update"
	CommandSkillsUpdate         = "skills.update"
)

// SurfaceHandler applies node-owned plugin and skill surface changes.
// Implementations must not return until the replacement is durable locally;
// successful actions are acknowledged immediately after the handler returns.
type SurfaceHandler interface {
	RefreshPluginSurface(context.Context) error
	UpdatePluginTools(context.Context, []any) error
	UpdateSkills(context.Context, []any) error
}

// Acknowledger removes successfully applied actions from the gateway pending
// queue. Failed and unknown actions are intentionally left pending.
type Acknowledger interface {
	Ack(context.Context, []string) error
}

// ProcessResult reports which actions were applied and which remain pending.
type ProcessResult struct {
	AckedIDs []string          `json:"acked_ids"`
	Failures map[string]string `json:"failures,omitempty"`
}

// ProcessPendingActions applies actions returned by node.pending.pull. Commands
// are processed in queue order and acknowledged one at a time, so a later
// failure cannot cause an earlier durable update to be replayed.
func ProcessPendingActions(ctx context.Context, actions []nodepending.Action, handler SurfaceHandler, acknowledger Acknowledger) ProcessResult {
	result := ProcessResult{AckedIDs: []string{}, Failures: map[string]string{}}
	for _, action := range actions {
		if err := ctx.Err(); err != nil {
			result.Failures[action.ID] = err.Error()
			break
		}
		if err := applyPendingAction(ctx, action, handler); err != nil {
			result.Failures[action.ID] = err.Error()
			continue
		}
		if acknowledger == nil {
			result.Failures[action.ID] = "pending command acknowledger unavailable"
			continue
		}
		if err := acknowledger.Ack(ctx, []string{action.ID}); err != nil {
			result.Failures[action.ID] = fmt.Sprintf("ack pending command: %v", err)
			continue
		}
		result.AckedIDs = append(result.AckedIDs, action.ID)
	}
	if len(result.Failures) == 0 {
		result.Failures = nil
	}
	return result
}

func applyPendingAction(ctx context.Context, action nodepending.Action, handler SurfaceHandler) error {
	if handler == nil {
		return fmt.Errorf("node surface handler unavailable")
	}
	switch action.Command {
	case CommandPluginSurfaceRefresh:
		return handler.RefreshPluginSurface(ctx)
	case CommandPluginToolsUpdate:
		tools, err := sliceArg(action.Args, "tools")
		if err != nil {
			return err
		}
		return handler.UpdatePluginTools(ctx, tools)
	case CommandSkillsUpdate:
		skills, err := sliceArg(action.Args, "skills")
		if err != nil {
			return err
		}
		return handler.UpdateSkills(ctx, skills)
	default:
		return fmt.Errorf("unsupported pending command %q", action.Command)
	}
}

func sliceArg(args map[string]any, name string) ([]any, error) {
	if args == nil {
		return nil, fmt.Errorf("pending command requires %s", name)
	}
	value, ok := args[name]
	if !ok {
		return nil, fmt.Errorf("pending command requires %s", name)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("pending command %s must be an array", name)
	}
	return append([]any(nil), items...), nil
}
