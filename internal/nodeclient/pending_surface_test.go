package nodeclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"metiq/internal/gateway/nodepending"
)

type recordingSurfaceHandler struct {
	calls  []string
	tools  []any
	skills []any
	fail   map[string]error
}

func (h *recordingSurfaceHandler) RefreshPluginSurface(context.Context) error {
	h.calls = append(h.calls, CommandPluginSurfaceRefresh)
	return h.fail[CommandPluginSurfaceRefresh]
}
func (h *recordingSurfaceHandler) UpdatePluginTools(_ context.Context, tools []any) error {
	h.calls = append(h.calls, CommandPluginToolsUpdate)
	h.tools = tools
	return h.fail[CommandPluginToolsUpdate]
}
func (h *recordingSurfaceHandler) UpdateSkills(_ context.Context, skills []any) error {
	h.calls = append(h.calls, CommandSkillsUpdate)
	h.skills = skills
	return h.fail[CommandSkillsUpdate]
}

type recordingAcknowledger struct {
	ids    []string
	failID string
}

func (a *recordingAcknowledger) Ack(_ context.Context, ids []string) error {
	if len(ids) == 1 && ids[0] == a.failID {
		return errors.New("gateway unavailable")
	}
	a.ids = append(a.ids, ids...)
	return nil
}

func TestProcessPendingActionsHandlesSurfaceCommandsAndAcknowledgesSuccess(t *testing.T) {
	handler := &recordingSurfaceHandler{fail: map[string]error{}}
	ack := &recordingAcknowledger{}
	actions := []nodepending.Action{
		{ID: "refresh", Command: CommandPluginSurfaceRefresh},
		{ID: "tools", Command: CommandPluginToolsUpdate, Args: map[string]any{"tools": []any{map[string]any{"name": "search"}}}},
		{ID: "skills", Command: CommandSkillsUpdate, Args: map[string]any{"skills": []any{map[string]any{"name": "review"}}}},
	}

	result := ProcessPendingActions(context.Background(), actions, handler, ack)
	if !reflect.DeepEqual(result.AckedIDs, []string{"refresh", "tools", "skills"}) || result.Failures != nil {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !reflect.DeepEqual(ack.ids, result.AckedIDs) {
		t.Fatalf("acked %v, want %v", ack.ids, result.AckedIDs)
	}
	if len(handler.tools) != 1 || len(handler.skills) != 1 {
		t.Fatalf("typed replacements not delivered: tools=%v skills=%v", handler.tools, handler.skills)
	}
}

func TestProcessPendingActionsLeavesFailedAndUnknownCommandsPending(t *testing.T) {
	handler := &recordingSurfaceHandler{fail: map[string]error{CommandSkillsUpdate: errors.New("disk full")}}
	ack := &recordingAcknowledger{failID: "tools"}
	actions := []nodepending.Action{
		{ID: "bad-shape", Command: CommandPluginToolsUpdate, Args: map[string]any{"tools": "not-an-array"}},
		{ID: "skills", Command: CommandSkillsUpdate, Args: map[string]any{"skills": []any{}}},
		{ID: "unknown", Command: "notify"},
		{ID: "tools", Command: CommandPluginToolsUpdate, Args: map[string]any{"tools": []any{}}},
	}

	result := ProcessPendingActions(context.Background(), actions, handler, ack)
	if len(result.AckedIDs) != 0 || len(result.Failures) != 4 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(ack.ids) != 0 {
		t.Fatalf("failed actions were acknowledged: %v", ack.ids)
	}
}
