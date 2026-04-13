package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"metiq/internal/store/state"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestResolveConfigDoc_parsesOAuthConfigAndCredentialKey(t *testing.T) {
	doc := state.ConfigDoc{Extra: map[string]any{
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"remote": map[string]any{
					"enabled": true,
					"type":    "http",
					"url":     "https://mcp.example.com/http",
					"headers": map[string]any{
						"Authorization": "Bearer static",
						"X-Tenant":      "tenant-a",
					},
					"oauth": map[string]any{
						"enabled":           true,
						"client_id":         "client-1",
						"client_secret_ref": "env:MCP_SECRET",
						"authorize_url":     "https://mcp.example.com/oauth/authorize",
						"token_url":         "https://mcp.example.com/oauth/token",
						"scopes":            []any{"profile", "offline_access"},
						"callback_port":     4317,
						"use_pkce":          true,
					},
				},
			},
		},
	}}
	cfg := ResolveConfigDoc(doc)
	remote := cfg.Servers["remote"]
	if remote.OAuth == nil || !remote.OAuth.Enabled {
		t.Fatalf("expected oauth config to be parsed, got %#v", remote)
	}
	if remote.OAuth.ClientID != "client-1" || remote.OAuth.TokenURL != "https://mcp.example.com/oauth/token" {
		t.Fatalf("unexpected oauth config: %#v", remote.OAuth)
	}
	credentialKey := CredentialKey(remote.ServerConfig)
	if strings.Contains(strings.ToLower(credentialKey), "authorization") || strings.Contains(credentialKey, "Bearer static") {
		t.Fatalf("credential key should ignore Authorization header, got %q", credentialKey)
	}
	if !strings.Contains(credentialKey, "tenant-a") || !strings.Contains(credentialKey, "client-1") {
		t.Fatalf("credential key missing stable identity fields: %q", credentialKey)
	}
	if !strings.Contains(remote.Signature, "oauth") || !strings.Contains(remote.Signature, "client_secret_ref") {
		t.Fatalf("expected signature to include oauth config, got %q", remote.Signature)
	}
}

func TestResolveConfigDoc_globalDisabledPreservesInventory(t *testing.T) {
	doc := state.ConfigDoc{Extra: map[string]any{
		"mcp": map[string]any{
			"enabled": false,
			"servers": map[string]any{
				"remote": map[string]any{
					"enabled": true,
					"type":    "http",
					"url":     "https://mcp.example.com/http",
				},
			},
		},
	}}
	cfg := ResolveConfigDoc(doc)
	if cfg.Enabled {
		t.Fatalf("expected global config to remain disabled")
	}
	if _, ok := cfg.DisabledServers["remote"]; !ok {
		t.Fatalf("expected disabled inventory to preserve globally disabled server, got %#v", cfg)
	}
}

func TestParseMCPConfig_appliesAllowDenyPolicy(t *testing.T) {
	cfg := ParseMCPConfig(map[string]any{
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"filesystem": map[string]any{
					"enabled": true,
					"command": "npx",
					"args":    []any{"-y", "server-filesystem", "/tmp"},
				},
				"notes": map[string]any{
					"enabled": true,
					"type":    "http",
					"url":     "https://notes.example.com/mcp",
				},
			},
			"policy": map[string]any{
				"allowed": []any{
					map[string]any{"command": []any{"npx", "-y", "server-filesystem", "/tmp"}},
				},
				"denied": []any{
					map[string]any{"name": "notes"},
				},
			},
		},
	})

	if len(cfg.Servers) != 1 {
		t.Fatalf("expected one allowed server, got %#v", cfg.Servers)
	}
	if _, ok := cfg.Servers["filesystem"]; !ok {
		t.Fatalf("expected filesystem to remain active, got %#v", cfg.Servers)
	}
	blocked, ok := cfg.FilteredServers["notes"]
	if !ok {
		t.Fatalf("expected denied server to remain inspectable, got %#v", cfg.FilteredServers)
	}
	if blocked.PolicyStatus != PolicyStatusBlocked || blocked.PolicyReason != PolicyReasonDenied {
		t.Fatalf("unexpected denied policy outcome: %#v", blocked)
	}
}

func TestResolveConfigDoc_requiresRemoteApproval(t *testing.T) {
	cfg := ResolveConfigDoc(state.ConfigDoc{Extra: map[string]any{
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"approved-remote": map[string]any{
					"enabled": true,
					"type":    "http",
					"url":     "https://approved.example.com/mcp",
				},
				"pending-remote": map[string]any{
					"enabled": true,
					"type":    "http",
					"url":     "https://pending.example.com/mcp",
				},
				"stdio": map[string]any{
					"enabled": true,
					"command": "demo-mcp",
				},
			},
			"policy": map[string]any{
				"require_remote_approval": true,
				"approved_servers":        []any{"approved-remote"},
			},
		},
	}})

	if len(cfg.Servers) != 2 {
		t.Fatalf("expected approved remote + stdio to remain active, got %#v", cfg.Servers)
	}
	if _, ok := cfg.Servers["approved-remote"]; !ok {
		t.Fatalf("expected approved remote server to remain active, got %#v", cfg.Servers)
	}
	if _, ok := cfg.Servers["stdio"]; !ok {
		t.Fatalf("expected stdio server to bypass remote approval gate, got %#v", cfg.Servers)
	}
	pending, ok := cfg.FilteredServers["pending-remote"]
	if !ok {
		t.Fatalf("expected unapproved remote server to be filtered, got %#v", cfg.FilteredServers)
	}
	if pending.PolicyStatus != PolicyStatusApprovalRequired || pending.PolicyReason != PolicyReasonRemoteApproval {
		t.Fatalf("unexpected approval-required policy outcome: %#v", pending)
	}
}

func TestResolveConfigDoc_emptyAllowlistBlocksAllEnabledServers(t *testing.T) {
	cfg := ResolveConfigDoc(state.ConfigDoc{Extra: map[string]any{
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"filesystem": map[string]any{
					"enabled": true,
					"command": "npx",
				},
			},
			"policy": map[string]any{
				"allowed": []any{},
			},
		},
	}})

	if len(cfg.Servers) != 0 {
		t.Fatalf("expected empty allowlist to block all enabled servers, got %#v", cfg.Servers)
	}
	blocked, ok := cfg.FilteredServers["filesystem"]
	if !ok {
		t.Fatalf("expected blocked server to remain inspectable, got %#v", cfg.FilteredServers)
	}
	if blocked.PolicyStatus != PolicyStatusBlocked || blocked.PolicyReason != PolicyReasonAllowlist {
		t.Fatalf("unexpected allowlist policy outcome: %#v", blocked)
	}
}

func TestResolveSourceConfigs_preservesDisabledServers(t *testing.T) {
	cfg := ResolveSourceConfigs(
		SourceConfig{
			Source:     "high",
			Enabled:    true,
			Precedence: 20,
			Servers: map[string]ServerConfig{
				"shared":   {Enabled: false, Command: "disabled-high"},
				"disabled": {Enabled: false, Command: "disabled-only"},
			},
		},
		SourceConfig{
			Source:     "low",
			Enabled:    true,
			Precedence: 10,
			Servers: map[string]ServerConfig{
				"shared": {Enabled: true, Command: "enabled-low"},
			},
		},
	)

	shared := cfg.Servers["shared"]
	if shared.Command != "enabled-low" || !shared.Enabled {
		t.Fatalf("expected enabled server to win over disabled conflict, got %#v", shared)
	}
	disabled := cfg.DisabledServers["disabled"]
	if disabled.Command != "disabled-only" || disabled.Enabled {
		t.Fatalf("expected disabled inventory entry, got %#v", disabled)
	}
	if _, ok := cfg.DisabledServers["shared"]; ok {
		t.Fatalf("expected disabled conflict to be suppressed, got %#v", cfg.DisabledServers)
	}
}

func TestManagerReconnectTransitionsFromFailedToConnected(t *testing.T) {
	mgr := NewManager()
	attempts := 0
	mgr.connectFn = func(_ context.Context, name string, _ ServerConfig) (*ServerConnection, error) {
		attempts++
		if attempts == 1 {
			return nil, fmt.Errorf("dial tcp timeout")
		}
		return &ServerConnection{
			Name:         name,
			Tools:        []*mcp.Tool{{Name: "echo"}},
			Capabilities: CapabilitySnapshot{Tools: true, Resources: true},
		}, nil
	}

	cfg := Config{
		Enabled: true,
		Servers: map[string]ResolvedServerConfig{
			"demo": {
				Name:         "demo",
				ServerConfig: ServerConfig{Enabled: true, Command: "npx"},
				Signature:    "stdio:demo",
			},
		},
	}

	if err := mgr.ApplyConfig(context.Background(), cfg); err == nil {
		t.Fatalf("expected initial connect error")
	}
	snap := mgr.Snapshot()
	if len(snap.Servers) != 1 || snap.Servers[0].State != ConnectionStateFailed {
		t.Fatalf("expected failed snapshot after initial connect, got %#v", snap)
	}
	if snap.Servers[0].ReconnectAttempts != 1 {
		t.Fatalf("expected reconnect attempt count=1, got %#v", snap.Servers[0])
	}

	if err := mgr.ReconnectServer(context.Background(), "demo"); err != nil {
		t.Fatalf("ReconnectServer error: %v", err)
	}
	snap = mgr.Snapshot()
	if snap.Servers[0].State != ConnectionStateConnected {
		t.Fatalf("expected connected snapshot after reconnect, got %#v", snap.Servers[0])
	}
	if snap.Servers[0].ToolCount != 1 || !snap.Servers[0].Capabilities.Tools || !snap.Servers[0].Capabilities.Resources {
		t.Fatalf("expected refreshed capability/tool snapshot, got %#v", snap.Servers[0])
	}
	if snap.Servers[0].ReconnectAttempts != 2 {
		t.Fatalf("expected reconnect attempt count=2, got %#v", snap.Servers[0])
	}
}

func TestManagerApplyConfigReconnectsWhenSignatureChanges(t *testing.T) {
	mgr := NewManager()
	toolSets := [][]*mcp.Tool{{{Name: "echo"}}, {{Name: "echo"}, {Name: "sum"}}}
	attempt := 0
	mgr.connectFn = func(_ context.Context, name string, _ ServerConfig) (*ServerConnection, error) {
		tools := toolSets[attempt]
		attempt++
		return &ServerConnection{
			Name:         name,
			Tools:        tools,
			Capabilities: CapabilitySnapshot{Tools: true},
		}, nil
	}

	apply := func(signature string) {
		t.Helper()
		if err := mgr.ApplyConfig(context.Background(), Config{
			Enabled: true,
			Servers: map[string]ResolvedServerConfig{
				"demo": {
					Name:         "demo",
					ServerConfig: ServerConfig{Enabled: true, Command: "npx"},
					Signature:    signature,
				},
			},
		}); err != nil {
			t.Fatalf("ApplyConfig(%q) error: %v", signature, err)
		}
	}

	apply("sig-1")
	if got := len(mgr.GetAllTools()["demo"]); got != 1 {
		t.Fatalf("expected initial tool count=1, got %d", got)
	}
	apply("sig-2")
	if got := len(mgr.GetAllTools()["demo"]); got != 2 {
		t.Fatalf("expected refreshed tool count=2 after signature change, got %d", got)
	}
	if attempt != 2 {
		t.Fatalf("expected reconnect attempt count=2, got %d", attempt)
	}
}

func TestManagerApplyConfigClassifiesNeedsAuthAndDisabled(t *testing.T) {
	mgr := NewManager()
	mgr.connectFn = func(_ context.Context, _ string, _ ServerConfig) (*ServerConnection, error) {
		return nil, fmt.Errorf("401 unauthorized")
	}

	cfg := Config{
		Enabled: true,
		Servers: map[string]ResolvedServerConfig{
			"remote": {
				Name:         "remote",
				ServerConfig: ServerConfig{Enabled: true, Type: "http", URL: "https://mcp.example.com"},
				Signature:    "http:https://mcp.example.com",
			},
		},
	}
	if err := mgr.ApplyConfig(context.Background(), cfg); err == nil {
		t.Fatalf("expected auth connect error")
	}
	snap := mgr.Snapshot()
	if len(snap.Servers) != 1 || snap.Servers[0].State != ConnectionStateNeedsAuth {
		t.Fatalf("expected needs-auth snapshot, got %#v", snap)
	}

	if err := mgr.ApplyConfig(context.Background(), Config{
		Enabled: true,
		DisabledServers: map[string]ResolvedServerConfig{
			"remote": {
				Name:         "remote",
				ServerConfig: ServerConfig{Enabled: false, Type: "http", URL: "https://mcp.example.com"},
				Signature:    "http:https://mcp.example.com",
			},
		},
	}); err != nil {
		t.Fatalf("ApplyConfig disable error: %v", err)
	}
	snap = mgr.Snapshot()
	if snap.Servers[0].State != ConnectionStateDisabled || snap.Servers[0].Enabled {
		t.Fatalf("expected disabled snapshot, got %#v", snap.Servers[0])
	}
}

func TestManagerBuildTransportMergesDynamicAuthHeaders(t *testing.T) {
	mgr := NewManager()
	mgr.SetRemoteAuthHeaderProvider(func(_ context.Context, serverName string, cfg ServerConfig) (map[string]string, error) {
		if serverName != "remote" || cfg.URL == "" {
			t.Fatalf("unexpected auth provider inputs: %s %#v", serverName, cfg)
		}
		return map[string]string{"Authorization": "Bearer dynamic"}, nil
	})
	transport, err := mgr.buildTransport(context.Background(), "remote", ServerConfig{
		Enabled: true,
		Type:    "http",
		URL:     "https://mcp.example.com/http",
		Headers: map[string]string{"X-Tenant": "tenant-a"},
		OAuth:   &OAuthConfig{Enabled: true},
	})
	if err != nil {
		t.Fatalf("buildTransport error: %v", err)
	}
	streamable, ok := transport.(*mcp.StreamableClientTransport)
	if !ok {
		t.Fatalf("expected StreamableClientTransport, got %T", transport)
	}
	if streamable.HTTPClient == nil {
		t.Fatalf("expected HTTP client with header transport")
	}
	requestHeaders := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHeaders <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest error: %v", err)
	}
	resp, err := streamable.HTTPClient.Do(req)
	if err != nil {
		t.Fatalf("HTTPClient.Do error: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	headers := <-requestHeaders
	if got := headers.Get("Authorization"); got != "Bearer dynamic" {
		t.Fatalf("authorization header = %q", got)
	}
	if got := headers.Get("X-Tenant"); got != "tenant-a" {
		t.Fatalf("tenant header = %q", got)
	}
}

func TestManagerListResourcesRequiresCapability(t *testing.T) {
	mgr := NewManager()
	mgr.SetConnectFunc(func(_ context.Context, name string, _ ServerConfig) (*ServerConnection, error) {
		return &ServerConnection{
			Name:         name,
			Capabilities: CapabilitySnapshot{Tools: true},
		}, nil
	})
	if err := mgr.ApplyConfig(context.Background(), Config{
		Enabled: true,
		Servers: map[string]ResolvedServerConfig{
			"demo": {
				Name:         "demo",
				ServerConfig: ServerConfig{Enabled: true, Command: "npx"},
				Signature:    "sig-demo",
			},
		},
	}); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}

	_, err := mgr.ListResources(context.Background(), "demo")
	if err == nil || !strings.Contains(err.Error(), "does not support resources") {
		t.Fatalf("err = %v, want capability-gated failure", err)
	}
}

func TestManagerReadResourceReturnsResult(t *testing.T) {
	mgr := NewManager()
	mgr.SetConnectFunc(func(_ context.Context, name string, _ ServerConfig) (*ServerConnection, error) {
		return &ServerConnection{
			Name:         name,
			Capabilities: CapabilitySnapshot{Resources: true},
			ReadResourceFunc: func(_ context.Context, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
				if params == nil || params.URI != "file:///demo.txt" {
					t.Fatalf("unexpected read params: %#v", params)
				}
				return &mcp.ReadResourceResult{
					Contents: []*mcp.ResourceContents{{
						URI:      params.URI,
						MIMEType: "text/plain",
						Text:     "hello",
					}},
				}, nil
			},
		}, nil
	})
	if err := mgr.ApplyConfig(context.Background(), Config{
		Enabled: true,
		Servers: map[string]ResolvedServerConfig{
			"demo": {
				Name:         "demo",
				ServerConfig: ServerConfig{Enabled: true, Command: "npx"},
				Signature:    "sig-demo",
			},
		},
	}); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}

	result, err := mgr.ReadResource(context.Background(), "demo", "file:///demo.txt")
	if err != nil {
		t.Fatalf("ReadResource error: %v", err)
	}
	if result == nil || len(result.Contents) != 1 || result.Contents[0].Text != "hello" {
		t.Fatalf("unexpected read result: %#v", result)
	}
}

func TestManagerReadResourcePropagatesReadError(t *testing.T) {
	mgr := NewManager()
	mgr.SetConnectFunc(func(_ context.Context, name string, _ ServerConfig) (*ServerConnection, error) {
		return &ServerConnection{
			Name:         name,
			Capabilities: CapabilitySnapshot{Resources: true},
			ReadResourceFunc: func(context.Context, *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
				return nil, fmt.Errorf("boom")
			},
		}, nil
	})
	if err := mgr.ApplyConfig(context.Background(), Config{
		Enabled: true,
		Servers: map[string]ResolvedServerConfig{
			"demo": {
				Name:         "demo",
				ServerConfig: ServerConfig{Enabled: true, Command: "npx"},
				Signature:    "sig-demo",
			},
		},
	}); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}

	_, err := mgr.ReadResource(context.Background(), "demo", "file:///demo.txt")
	if err == nil || !strings.Contains(err.Error(), "failed to read resource: boom") {
		t.Fatalf("err = %v, want read failure", err)
	}
}

func TestManagerListPromptsRequiresCapability(t *testing.T) {
	mgr := NewManager()
	mgr.SetConnectFunc(func(_ context.Context, name string, _ ServerConfig) (*ServerConnection, error) {
		return &ServerConnection{
			Name:         name,
			Capabilities: CapabilitySnapshot{Tools: true},
		}, nil
	})
	if err := mgr.ApplyConfig(context.Background(), Config{
		Enabled: true,
		Servers: map[string]ResolvedServerConfig{
			"demo": {
				Name:         "demo",
				ServerConfig: ServerConfig{Enabled: true, Command: "npx"},
				Signature:    "sig-demo",
			},
		},
	}); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}

	_, err := mgr.ListPrompts(context.Background(), "demo")
	if err == nil || !strings.Contains(err.Error(), "does not support prompts") {
		t.Fatalf("err = %v, want capability-gated failure", err)
	}
}

func TestManagerGetPromptPassesArguments(t *testing.T) {
	mgr := NewManager()
	mgr.SetConnectFunc(func(_ context.Context, name string, _ ServerConfig) (*ServerConnection, error) {
		return &ServerConnection{
			Name:         name,
			Capabilities: CapabilitySnapshot{Prompts: true},
			ListPromptsFunc: func(_ context.Context, _ *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error) {
				return &mcp.ListPromptsResult{
					Prompts: []*mcp.Prompt{{
						Name: "review",
						Arguments: []*mcp.PromptArgument{{
							Name:     "topic",
							Required: true,
						}},
					}},
				}, nil
			},
			GetPromptFunc: func(_ context.Context, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
				if params == nil || params.Name != "review" {
					t.Fatalf("unexpected get prompt params: %#v", params)
				}
				if params.Arguments["topic"] != "mcp" || params.Arguments["mode"] != "full" {
					t.Fatalf("unexpected prompt arguments: %#v", params.Arguments)
				}
				return &mcp.GetPromptResult{
					Description: "review prompt",
					Messages: []*mcp.PromptMessage{{
						Role:    mcp.Role("user"),
						Content: &mcp.TextContent{Text: "Review MCP support"},
					}},
				}, nil
			},
		}, nil
	})
	if err := mgr.ApplyConfig(context.Background(), Config{
		Enabled: true,
		Servers: map[string]ResolvedServerConfig{
			"demo": {
				Name:         "demo",
				ServerConfig: ServerConfig{Enabled: true, Command: "npx"},
				Signature:    "sig-demo",
			},
		},
	}); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}

	list, err := mgr.ListPrompts(context.Background(), "demo")
	if err != nil {
		t.Fatalf("ListPrompts error: %v", err)
	}
	if list == nil || len(list.Prompts) != 1 || list.Prompts[0].Name != "review" {
		t.Fatalf("unexpected list result: %#v", list)
	}
	result, err := mgr.GetPrompt(context.Background(), "demo", "review", map[string]string{
		"topic": "mcp",
		"mode":  "full",
	})
	if err != nil {
		t.Fatalf("GetPrompt error: %v", err)
	}
	if result == nil || len(result.Messages) != 1 || result.Description != "review prompt" {
		t.Fatalf("unexpected get result: %#v", result)
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
