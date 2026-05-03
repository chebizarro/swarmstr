package agent

import (
	"testing"
)

func TestIsLikelyMutatingToolName(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"empty", "", false},
		{"read tool", "read_file", false},
		{"write tool", "write", true},
		{"edit tool", "edit", true},
		{"bash", "bash", true},
		{"bash_exec", "bash_exec", true},
		{"exec", "exec", true},
		{"nostr_publish", "nostr_publish", true},
		{"nostr_dm", "nostr_dm", true}, // in name set
		{"sessions_send", "sessions_send", true},
		{"cron_add", "cron_add", true},
		{"actions suffix", "file_actions", true},
		{"message prefix", "message_send", true},
		{"contains send", "send_notification", true},
		{"contains publish", "publish_event", true},
		{"contains write", "write_data", true},
		{"contains create", "create_user", true},
		{"contains delete", "delete_record", true},
		{"search tool", "search", false},
		{"get tool", "get_data", false},
		{"list tool", "list_files", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsLikelyMutatingToolName(tt.toolName)
			if result != tt.expected {
				t.Errorf("IsLikelyMutatingToolName(%q) = %v, want %v", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestIsMutatingToolCall(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		args     interface{}
		expected bool
	}{
		{"write always mutates", "write", nil, true},
		{"edit always mutates", "edit", nil, true},
		{"bash always mutates", "bash", nil, true},
		{"bash_exec always mutates", "bash_exec", nil, true},
		{"process read", "process", map[string]interface{}{"action": "read"}, false},
		{"process write", "process", map[string]interface{}{"action": "write"}, true},
		{"process kill", "process", map[string]interface{}{"action": "kill"}, true},
		{"message send", "message", map[string]interface{}{"action": "send"}, true},
		{"message with content", "message", map[string]interface{}{"content": "hello"}, true},
		{"cron list", "cron", map[string]interface{}{"action": "list"}, false},
		{"cron add", "cron", map[string]interface{}{"action": "add"}, true},
		{"cron no action", "cron", nil, true},
		{"subagents kill", "subagents", map[string]interface{}{"action": "kill"}, true},
		{"subagents list", "subagents", map[string]interface{}{"action": "list"}, false},
		{"session_status with model", "session_status", map[string]interface{}{"model": "gpt-4"}, true},
		{"session_status without model", "session_status", map[string]interface{}{}, false},
		{"nodes list", "nodes", map[string]interface{}{"action": "list"}, false},
		{"nodes create", "nodes", map[string]interface{}{"action": "create"}, true},
		{"unknown read", "unknown_tool", nil, false},
		{"file_actions get", "file_actions", map[string]interface{}{"action": "get"}, false},
		{"file_actions delete", "file_actions", map[string]interface{}{"action": "delete"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsMutatingToolCall(tt.toolName, tt.args)
			if result != tt.expected {
				t.Errorf("IsMutatingToolCall(%q, %v) = %v, want %v",
					tt.toolName, tt.args, result, tt.expected)
			}
		})
	}
}

func TestBuildToolActionFingerprint(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		args        interface{}
		meta        string
		wantEmpty   bool
		wantContain []string
	}{
		{
			"non-mutating returns empty",
			"read_file", nil, "", true, nil,
		},
		{
			"write with path",
			"write",
			map[string]interface{}{"path": "/tmp/test.txt"},
			"",
			false,
			[]string{"tool=write", "path=/tmp/test.txt"},
		},
		{
			"edit with file",
			"edit",
			map[string]interface{}{"file": "main.go"},
			"",
			false,
			[]string{"tool=edit", "path=main.go"},
		},
		{
			"cron_add with action",
			"cron_add",
			map[string]interface{}{"action": "add", "id": "job-123"},
			"",
			false,
			[]string{"tool=cron_add", "action=add", "id=job-123"},
		},
		{
			"message with target",
			"message",
			map[string]interface{}{"action": "send", "to": "user@example.com"},
			"",
			false,
			[]string{"tool=message", "action=send", "to=user@example.com"},
		},
		{
			"nostr_publish with pubkey",
			"nostr_publish",
			map[string]interface{}{"pubkey": "npub123"},
			"",
			false,
			[]string{"tool=nostr_publish", "pubkey=npub123"},
		},
		{
			"bash with meta fallback",
			"bash",
			map[string]interface{}{},
			"run script",
			false,
			[]string{"tool=bash", "meta=run script"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildToolActionFingerprint(tt.toolName, tt.args, tt.meta)

			if tt.wantEmpty {
				if result != "" {
					t.Errorf("Expected empty fingerprint, got %q", result)
				}
				return
			}

			if result == "" {
				t.Error("Expected non-empty fingerprint")
				return
			}

			for _, want := range tt.wantContain {
				if !containsSubstring(result, want) {
					t.Errorf("Fingerprint %q should contain %q", result, want)
				}
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestBuildToolMutationState(t *testing.T) {
	state := BuildToolMutationState("write", map[string]interface{}{"path": "test.txt"}, "")
	if !state.IsMutating {
		t.Error("write should be mutating")
	}
	if state.ActionFingerprint == "" {
		t.Error("write should have fingerprint")
	}

	state = BuildToolMutationState("read_file", nil, "")
	if state.IsMutating {
		t.Error("read_file should not be mutating")
	}
	if state.ActionFingerprint != "" {
		t.Error("read_file should not have fingerprint")
	}
}

func TestIsSameToolMutationAction(t *testing.T) {
	tests := []struct {
		name     string
		existing ToolActionRef
		next     ToolActionRef
		expected bool
	}{
		{
			"same fingerprint",
			ToolActionRef{ActionFingerprint: "tool=write|path=/test"},
			ToolActionRef{ActionFingerprint: "tool=write|path=/test"},
			true,
		},
		{
			"different fingerprint",
			ToolActionRef{ActionFingerprint: "tool=write|path=/test1"},
			ToolActionRef{ActionFingerprint: "tool=write|path=/test2"},
			false,
		},
		{
			"one empty fingerprint",
			ToolActionRef{ActionFingerprint: "tool=write|path=/test"},
			ToolActionRef{ActionFingerprint: ""},
			false,
		},
		{
			"both empty - same name",
			ToolActionRef{ToolName: "read", Meta: "file.txt"},
			ToolActionRef{ToolName: "read", Meta: "file.txt"},
			true,
		},
		{
			"both empty - different name",
			ToolActionRef{ToolName: "read", Meta: "file.txt"},
			ToolActionRef{ToolName: "write", Meta: "file.txt"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsSameToolMutationAction(tt.existing, tt.next)
			if result != tt.expected {
				t.Errorf("IsSameToolMutationAction() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestMutationTracker(t *testing.T) {
	tracker := NewMutationTracker()

	// First call is not a duplicate
	isDup := tracker.Track("tool=write|path=/test")
	if isDup {
		t.Error("First track should not be duplicate")
	}

	// Same fingerprint is a duplicate
	isDup = tracker.Track("tool=write|path=/test")
	if !isDup {
		t.Error("Second track should be duplicate")
	}

	// Different fingerprint is not a duplicate
	isDup = tracker.Track("tool=write|path=/other")
	if isDup {
		t.Error("Different fingerprint should not be duplicate")
	}

	// Check count
	if tracker.Count("tool=write|path=/test") != 2 {
		t.Errorf("Count should be 2, got %d", tracker.Count("tool=write|path=/test"))
	}

	// Empty fingerprint is never tracked
	isDup = tracker.Track("")
	if isDup {
		t.Error("Empty fingerprint should not be tracked as duplicate")
	}
	isDup = tracker.Track("")
	if isDup {
		t.Error("Empty fingerprint should not be tracked as duplicate")
	}

	// Reset clears all
	tracker.Reset()
	if tracker.Count("tool=write|path=/test") != 0 {
		t.Error("Reset should clear all counts")
	}
}

func TestMutationTrackerTrackToolCall(t *testing.T) {
	tracker := NewMutationTracker()

	// First write to path
	isDup := tracker.TrackToolCall("write", map[string]interface{}{"path": "/test.txt"}, "")
	if isDup {
		t.Error("First call should not be duplicate")
	}

	// Same write to same path
	isDup = tracker.TrackToolCall("write", map[string]interface{}{"path": "/test.txt"}, "")
	if !isDup {
		t.Error("Second call to same path should be duplicate")
	}

	// Write to different path
	isDup = tracker.TrackToolCall("write", map[string]interface{}{"path": "/other.txt"}, "")
	if isDup {
		t.Error("Write to different path should not be duplicate")
	}

	// Read is not tracked (non-mutating)
	isDup = tracker.TrackToolCall("read_file", nil, "")
	if isDup {
		t.Error("Read should not be tracked as duplicate")
	}
}

func TestAsRecord(t *testing.T) {
	// Map input
	m := map[string]interface{}{"key": "value"}
	result := asRecord(m)
	if result["key"] != "value" {
		t.Error("Map should pass through")
	}

	// JSON string input
	result = asRecord(`{"key": "value"}`)
	if result["key"] != "value" {
		t.Error("JSON string should be parsed")
	}

	// Nil input
	result = asRecord(nil)
	if result != nil {
		t.Error("Nil should return nil")
	}

	// Invalid JSON
	result = asRecord("not json")
	if result != nil {
		t.Error("Invalid JSON should return nil")
	}
}
