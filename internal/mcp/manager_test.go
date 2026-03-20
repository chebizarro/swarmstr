package mcp

import (
	"testing"
)

func TestParseMCPConfig_empty(t *testing.T) {
	cfg := ParseMCPConfig(nil)
	if cfg.Enabled {
		t.Error("expected disabled")
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("expected no servers, got %d", len(cfg.Servers))
	}
}

func TestParseMCPConfig_full(t *testing.T) {
	extra := map[string]any{
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"filesystem": map[string]any{
					"enabled": true,
					"command": "npx",
					"args":    []any{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
					"env":     map[string]any{"NODE_ENV": "production"},
				},
				"remote": map[string]any{
					"enabled": true,
					"url":     "https://mcp.example.com/sse",
					"headers": map[string]any{"Authorization": "Bearer tok"},
				},
			},
		},
	}

	cfg := ParseMCPConfig(extra)
	if !cfg.Enabled {
		t.Error("expected enabled")
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}

	fs := cfg.Servers["filesystem"]
	if !fs.Enabled {
		t.Error("filesystem: expected enabled")
	}
	if fs.Command != "npx" {
		t.Errorf("filesystem: command = %q, want npx", fs.Command)
	}
	if len(fs.Args) != 3 {
		t.Errorf("filesystem: args count = %d, want 3", len(fs.Args))
	}
	if fs.Env["NODE_ENV"] != "production" {
		t.Errorf("filesystem: env NODE_ENV = %q", fs.Env["NODE_ENV"])
	}

	remote := cfg.Servers["remote"]
	if remote.URL != "https://mcp.example.com/sse" {
		t.Errorf("remote: url = %q", remote.URL)
	}
	if remote.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("remote: auth header = %q", remote.Headers["Authorization"])
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"My Server", "my_server"},
		{"foo--bar", "foo--bar"},
		{"foo___bar", "foo_bar"},
		{"@scope/name", "scope_name"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitize(tt.in)
		if got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractContentText(t *testing.T) {
	// No content.
	if got := extractContentText(nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestToolInputSchemaToMap_nil(t *testing.T) {
	m := toolInputSchemaToMap(nil)
	if m["type"] != "object" {
		t.Errorf("expected type=object, got %v", m["type"])
	}
}

func TestNewManager_close(t *testing.T) {
	mgr := NewManager()
	if err := mgr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Double close should be fine.
	if err := mgr.Close(); err != nil {
		t.Errorf("double Close: %v", err)
	}
}

func TestCallTool_closedManager(t *testing.T) {
	mgr := NewManager()
	mgr.Close()
	_, err := mgr.CallTool(nil, "srv", "tool", nil)
	if err == nil {
		t.Error("expected error calling tool on closed manager")
	}
}

func TestCallTool_unknownServer(t *testing.T) {
	mgr := NewManager()
	_, err := mgr.CallTool(nil, "nonexistent", "tool", nil)
	if err == nil {
		t.Error("expected error for unknown server")
	}
}
