package agent

import (
	"context"
	"testing"
)

func TestLookupProfile(t *testing.T) {
	tests := []struct {
		input   string
		wantID  string
		wantNil bool
	}{
		{"full", "full", false},
		{"FULL", "full", false},
		{"  minimal  ", "minimal", false},
		{"coding", "coding", false},
		{"messaging", "messaging", false},
		{"unknown", "", true},
		{"", "", true},
	}
	for _, tc := range tests {
		got := LookupProfile(tc.input)
		if tc.wantNil {
			if got != nil {
				t.Errorf("LookupProfile(%q) = %+v, want nil", tc.input, got)
			}
		} else {
			if got == nil {
				t.Errorf("LookupProfile(%q) = nil, want %q", tc.input, tc.wantID)
			} else if got.ID != tc.wantID {
				t.Errorf("LookupProfile(%q).ID = %q, want %q", tc.input, got.ID, tc.wantID)
			}
		}
	}
}

func TestBuiltinProfilesCount(t *testing.T) {
	if len(BuiltinProfiles) != 4 {
		t.Errorf("expected 4 built-in profiles, got %d", len(BuiltinProfiles))
	}
}

func TestProfilesAsResponse(t *testing.T) {
	resp := ProfilesAsResponse()
	if len(resp) != len(BuiltinProfiles) {
		t.Errorf("ProfilesAsResponse length = %d, want %d", len(resp), len(BuiltinProfiles))
	}
	for _, item := range resp {
		if _, ok := item["id"]; !ok {
			t.Errorf("ProfilesAsResponse item missing 'id': %v", item)
		}
		if _, ok := item["label"]; !ok {
			t.Errorf("ProfilesAsResponse item missing 'label': %v", item)
		}
		if _, ok := item["description"]; !ok {
			t.Errorf("ProfilesAsResponse item missing 'description': %v", item)
		}
	}
}

func TestProfileListSorted(t *testing.T) {
	ids := ProfileListSorted()
	for i := 1; i < len(ids); i++ {
		if ids[i] < ids[i-1] {
			t.Errorf("ProfileListSorted not sorted at index %d: %q > %q", i, ids[i-1], ids[i])
		}
	}
	if len(ids) != len(BuiltinProfiles) {
		t.Errorf("ProfileListSorted length = %d, want %d", len(ids), len(BuiltinProfiles))
	}
}

// testGroups builds a minimal catalog groups slice for testing.
func testGroups() []map[string]any {
	return []map[string]any{
		{
			"id":    "identity",
			"label": "Identity",
			"tools": []map[string]any{
				{"id": "session_status", "label": "Session Status", "defaultProfiles": []string{"minimal", "coding", "messaging"}},
			},
		},
		{
			"id":    "filesystem",
			"label": "Filesystem",
			"tools": []map[string]any{
				{"id": "file_read", "label": "File Read", "defaultProfiles": []string{"coding"}},
				{"id": "file_write", "label": "File Write", "defaultProfiles": []string{"coding"}},
			},
		},
		{
			"id":    "messaging",
			"label": "Messaging",
			"tools": []map[string]any{
				{"id": "dm_send", "label": "DM Send", "defaultProfiles": []string{"messaging"}},
			},
		},
	}
}

func TestFilterCatalogByProfile_full(t *testing.T) {
	groups := testGroups()
	result := FilterCatalogByProfile(groups, "full")
	// Full allows everything – should return all 3 groups.
	if len(result) != 3 {
		t.Errorf("full profile: expected 3 groups, got %d", len(result))
	}
	totalTools := 0
	for _, g := range result {
		totalTools += len(g["tools"].([]map[string]any))
	}
	if totalTools != 4 {
		t.Errorf("full profile: expected 4 tools total, got %d", totalTools)
	}
}

func TestFilterCatalogByProfile_minimal(t *testing.T) {
	groups := testGroups()
	result := FilterCatalogByProfile(groups, "minimal")
	// minimal should include only session_status.
	if len(result) != 1 {
		t.Errorf("minimal profile: expected 1 group, got %d", len(result))
	}
	tools := result[0]["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Errorf("minimal profile: expected 1 tool, got %d", len(tools))
	}
	if tools[0]["id"] != "session_status" {
		t.Errorf("minimal profile: expected session_status, got %v", tools[0]["id"])
	}
}

func TestFilterCatalogByProfile_coding(t *testing.T) {
	groups := testGroups()
	result := FilterCatalogByProfile(groups, "coding")
	// coding includes coding + minimal profiles, so session_status + file_read + file_write.
	totalTools := 0
	for _, g := range result {
		totalTools += len(g["tools"].([]map[string]any))
	}
	if totalTools != 3 {
		t.Errorf("coding profile: expected 3 tools (session_status+file_read+file_write), got %d", totalTools)
	}
}

func TestFilterCatalogByProfile_messaging(t *testing.T) {
	groups := testGroups()
	result := FilterCatalogByProfile(groups, "messaging")
	// messaging includes messaging + minimal → dm_send + session_status.
	totalTools := 0
	for _, g := range result {
		totalTools += len(g["tools"].([]map[string]any))
	}
	if totalTools != 2 {
		t.Errorf("messaging profile: expected 2 tools (session_status+dm_send), got %d", totalTools)
	}
}

func TestFilterCatalogByProfile_unknown(t *testing.T) {
	groups := testGroups()
	result := FilterCatalogByProfile(groups, "nonexistent")
	// Unknown profile falls back to allowing everything.
	if len(result) != 3 {
		t.Errorf("unknown profile: expected all groups, got %d", len(result))
	}
}

func TestAllowedToolIDs(t *testing.T) {
	groups := testGroups()
	allowed := AllowedToolIDs(groups, "minimal")
	if !allowed["session_status"] {
		t.Error("minimal profile should allow session_status")
	}
	if allowed["file_read"] {
		t.Error("minimal profile should NOT allow file_read")
	}
	if allowed["dm_send"] {
		t.Error("minimal profile should NOT allow dm_send")
	}
}

func TestProfileFilteredExecutor_blocks(t *testing.T) {
	base := &echoExecutor{}
	exec := &ProfileFilteredExecutor{
		Base:    base,
		Allowed: map[string]bool{"allowed_tool": true},
	}
	_, err := exec.Execute(context.Background(), ToolCall{Name: "blocked_tool"})
	if err == nil {
		t.Error("expected error for blocked tool, got nil")
	}
}

func TestProfileFilteredExecutor_passes(t *testing.T) {
	base := &echoExecutor{}
	exec := &ProfileFilteredExecutor{
		Base:    base,
		Allowed: map[string]bool{"my_tool": true},
	}
	result, err := exec.Execute(context.Background(), ToolCall{Name: "my_tool", Args: map[string]any{"text": "hello"}})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestProfileFilteredExecutor_nilAllowed(t *testing.T) {
	base := &echoExecutor{}
	exec := &ProfileFilteredExecutor{Base: base, Allowed: nil}
	// nil allowed means pass everything through.
	_, err := exec.Execute(context.Background(), ToolCall{Name: "anything"})
	if err != nil {
		t.Errorf("nil Allowed should pass all tools, got err: %v", err)
	}
}

func TestProfileFilteredExecutor_DefinitionsFiltered(t *testing.T) {
	base := &echoExecutor{}
	exec := &ProfileFilteredExecutor{Base: base, Allowed: map[string]bool{"allowed_tool": true}}
	defs := exec.Definitions()
	if len(defs) != 1 || defs[0].Name != "allowed_tool" {
		t.Fatalf("unexpected definitions: %+v", defs)
	}
	descs := exec.Descriptors()
	if len(descs) != 1 || descs[0].Name != "allowed_tool" {
		t.Fatalf("unexpected descriptors: %+v", descs)
	}
}

// echoExecutor is a minimal ToolExecutor stub for testing.
type echoExecutor struct{}

func (e *echoExecutor) Execute(_ context.Context, call ToolCall) (string, error) {
	return "echo:" + call.Name, nil
}

func (e *echoExecutor) Definitions() []ToolDefinition {
	return []ToolDefinition{{Name: "allowed_tool"}, {Name: "blocked_tool"}}
}

func (e *echoExecutor) Descriptors() []ToolDescriptor {
	return []ToolDescriptor{{Name: "allowed_tool"}, {Name: "blocked_tool"}}
}
