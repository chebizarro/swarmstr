package toolbuiltin

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/tasks"
)

func TestFleetTasksArgGating(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{"unknown action", map[string]any{"action": "destroy"}, "'action' must be one of"},
		{"list rejects extra fields", map[string]any{"action": "list", "task_id": "x"}, "does not accept: task_id"},
		{"inspect requires task_id", map[string]any{"action": "inspect"}, `requires "task_id"`},
		{"create requires title", map[string]any{"action": "create", "task_id": "t-1"}, `requires "title"`},
		{"create rejects base_event_id", map[string]any{"action": "create", "task_id": "t-1", "title": "T", "base_event_id": strings.Repeat("a", 64)}, "does not accept: base_event_id"},
		{"claim requires base", map[string]any{"action": "claim", "task_id": "t-1", "assignee": "a"}, `requires "base_event_id"`},
		{"bad base hex", map[string]any{"action": "claim", "task_id": "t-1", "assignee": "a", "base_event_id": "zzz"}, "64-character hex"},
		{"bad task id", map[string]any{"action": "inspect", "task_id": "task:oops"}, "'task_id' must match"},
		{"bad priority", map[string]any{"action": "create", "task_id": "t-1", "title": "T", "priority": 9}, "between 0 and 4"},
		{"close requires evidence", map[string]any{"action": "close", "task_id": "t-1", "base_event_id": strings.Repeat("a", 64), "note": "done"}, `requires "evidence"`},
		{"close rejects blank evidence", map[string]any{"action": "close", "task_id": "t-1", "base_event_id": strings.Repeat("a", 64), "note": "done", "evidence": []any{"  "}}, "at least one non-blank evidence"},
		{"duplicate labels", map[string]any{"action": "create", "task_id": "t-1", "title": "T", "labels": []any{"a", "a"}}, "duplicate"},
		{"blank note", map[string]any{"action": "checkpoint", "task_id": "t-1", "base_event_id": strings.Repeat("a", 64), "note": "  "}, `"note" must not be blank`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFleetTasksArgs(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestFleetTasksArgAcceptance(t *testing.T) {
	req, err := parseFleetTasksArgs(map[string]any{
		"action": "close", "task_id": "t-1",
		"base_event_id": strings.ToUpper(strings.Repeat("ab", 32)),
		"note":          "accepted",
		"evidence":      []any{"commit:abc", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.baseEventID != strings.Repeat("ab", 32) {
		t.Fatalf("base not lowercased: %q", req.baseEventID)
	}
	if len(req.evidence) != 1 || req.evidence[0] != "commit:abc" {
		t.Fatalf("evidence=%v", req.evidence)
	}
	if _, err := parseFleetTasksArgs(map[string]any{"action": "list"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	create, err := parseFleetTasksArgs(map[string]any{"action": "create", "task_id": "t-2", "title": "T"})
	if err != nil {
		t.Fatal(err)
	}
	if create.priority != 2 {
		t.Fatalf("default priority=%d", create.priority)
	}
}

func TestFleetTasksToolInactiveBridge(t *testing.T) {
	for name, tool := range map[string]func(context.Context, map[string]any) (string, error){
		"nil func":   FleetTasksTool(nil),
		"nil bridge": FleetTasksTool(func() *tasks.FleetTaskBridge { return nil }),
	} {
		if _, err := tool(context.Background(), map[string]any{"action": "list"}); err == nil || !strings.Contains(err.Error(), "not active") {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
}
