package mcp

import (
	"strings"
	"testing"

	"metiq/internal/store/state"
)

func TestParseMCPConfig_empty(t *testing.T) {
	cfg := ParseMCPConfig(nil)
	if cfg.Enabled {
		t.Error("expected disabled")
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("expected no servers, got %d", len(cfg.Servers))
	}
	if len(cfg.Suppressed) != 0 {
		t.Errorf("expected no suppressed servers, got %d", len(cfg.Suppressed))
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
	if len(cfg.Suppressed) != 0 {
		t.Fatalf("expected no suppressed servers, got %d", len(cfg.Suppressed))
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
	if fs.Source != ConfigSourceExtraMCP {
		t.Errorf("filesystem: source = %q, want %q", fs.Source, ConfigSourceExtraMCP)
	}
	if fs.Precedence != extraMCPPrecedence {
		t.Errorf("filesystem: precedence = %d, want %d", fs.Precedence, extraMCPPrecedence)
	}
	if fs.Signature == "" {
		t.Error("filesystem: expected non-empty signature")
	}

	remote := cfg.Servers["remote"]
	if remote.URL != "https://mcp.example.com/sse" {
		t.Errorf("remote: url = %q", remote.URL)
	}
	if remote.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("remote: auth header = %q", remote.Headers["Authorization"])
	}
	if !strings.Contains(remote.Signature, "https://mcp.example.com/sse") {
		t.Errorf("remote: signature = %q", remote.Signature)
	}
}

func TestResolveSourceConfigs_namePrecedence(t *testing.T) {
	cfg := ResolveSourceConfigs(
		SourceConfig{
			Source:     "low",
			Enabled:    true,
			Precedence: 10,
			Servers: map[string]ServerConfig{
				"shared": {Enabled: true, Command: "low-cmd"},
			},
		},
		SourceConfig{
			Source:     "high",
			Enabled:    true,
			Precedence: 20,
			Servers: map[string]ServerConfig{
				"shared": {Enabled: true, Command: "high-cmd"},
				"other":  {Enabled: true, URL: "https://example.com/sse"},
			},
		},
	)

	if !cfg.Enabled {
		t.Fatal("expected resolved config enabled")
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 resolved servers, got %d", len(cfg.Servers))
	}
	shared := cfg.Servers["shared"]
	if shared.Command != "high-cmd" {
		t.Fatalf("expected highest precedence server to win, got %#v", shared)
	}
	if len(cfg.Suppressed) != 1 {
		t.Fatalf("expected 1 suppressed server, got %d", len(cfg.Suppressed))
	}
	if cfg.Suppressed[0].Reason != SuppressionReasonNameConflict {
		t.Fatalf("expected name conflict suppression, got %#v", cfg.Suppressed[0])
	}
	if cfg.Suppressed[0].Name != "shared" || cfg.Suppressed[0].Source != "low" {
		t.Fatalf("unexpected suppressed server metadata: %#v", cfg.Suppressed[0])
	}
}

func TestResolveSourceConfigs_duplicateSignature(t *testing.T) {
	cfg := ResolveSourceConfigs(SourceConfig{
		Source:     "extra.mcp",
		Enabled:    true,
		Precedence: 100,
		Servers: map[string]ServerConfig{
			"filesystem": {Enabled: true, Command: "npx", Args: []string{"-y", "server-filesystem", "/tmp"}},
			"duplicate":  {Enabled: true, Command: "npx", Args: []string{"-y", "server-filesystem", "/tmp"}},
		},
	})

	if len(cfg.Servers) != 1 {
		t.Fatalf("expected 1 resolved server after dedup, got %d", len(cfg.Servers))
	}
	if len(cfg.Suppressed) != 1 {
		t.Fatalf("expected 1 suppressed server, got %d", len(cfg.Suppressed))
	}
	if cfg.Suppressed[0].Reason != SuppressionReasonDuplicateSignature {
		t.Fatalf("expected duplicate signature suppression, got %#v", cfg.Suppressed[0])
	}
	if cfg.Suppressed[0].DuplicateOf != "duplicate" && cfg.Suppressed[0].DuplicateOf != "filesystem" {
		t.Fatalf("expected duplicate-of metadata, got %#v", cfg.Suppressed[0])
	}
}

func TestResolveSourceConfigs_distinctConnectionMetadataNotSuppressed(t *testing.T) {
	cfg := ResolveSourceConfigs(SourceConfig{
		Source:     "extra.mcp",
		Enabled:    true,
		Precedence: 100,
		Servers: map[string]ServerConfig{
			"env-a":    {Enabled: true, Command: "npx", Args: []string{"-y", "server-filesystem", "/tmp"}, Env: map[string]string{"MODE": "a"}},
			"env-b":    {Enabled: true, Command: "npx", Args: []string{"-y", "server-filesystem", "/tmp"}, Env: map[string]string{"MODE": "b"}},
			"remote-a": {Enabled: true, URL: "https://mcp.example.com/sse", Headers: map[string]string{"Authorization": "Bearer a"}},
			"remote-b": {Enabled: true, URL: "https://mcp.example.com/sse", Headers: map[string]string{"Authorization": "Bearer b"}},
		},
	})

	if len(cfg.Servers) != 4 {
		t.Fatalf("expected 4 resolved servers, got %d", len(cfg.Servers))
	}
	if len(cfg.Suppressed) != 0 {
		t.Fatalf("expected no suppressed servers, got %#v", cfg.Suppressed)
	}
}

func TestResolveConfigDoc(t *testing.T) {
	doc := state.ConfigDoc{Extra: map[string]any{
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"remote": map[string]any{
					"enabled": true,
					"type":    "HTTP",
					"url":     " https://mcp.example.com/http ",
				},
			},
		},
	}}
	cfg := ResolveConfigDoc(doc)
	remote := cfg.Servers["remote"]
	if remote.Type != "http" {
		t.Fatalf("expected normalized transport type, got %#v", remote)
	}
	if remote.URL != "https://mcp.example.com/http" {
		t.Fatalf("expected trimmed URL, got %#v", remote)
	}
	if !strings.Contains(remote.Signature, "https://mcp.example.com/http") {
		t.Fatalf("unexpected signature: %#v", remote)
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
