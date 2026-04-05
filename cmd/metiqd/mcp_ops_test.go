package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	mcppkg "metiq/internal/mcp"
	secretspkg "metiq/internal/secrets"
	"metiq/internal/store/state"
)

func TestMCPOpsControllerApplyListIncludesRuntimeAndAuthMetadata(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Extra: map[string]any{
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"remote": map[string]any{
					"enabled": true,
					"type":    "http",
					"url":     "https://mcp.example.com/http",
					"headers": map[string]any{"X-Tenant": "tenant-a"},
					"oauth": map[string]any{
						"enabled":       true,
						"client_id":     "client-1",
						"authorize_url": "https://mcp.example.com/oauth/authorize",
						"token_url":     "https://mcp.example.com/oauth/token",
						"scopes":        []any{"profile"},
					},
				},
			},
		},
	}})
	resolved := mcppkg.ResolveConfigDoc(cfgState.Get())
	mgr := mcppkg.NewManager()
	mgr.SetConnectFunc(func(_ context.Context, name string, _ mcppkg.ServerConfig) (*mcppkg.ServerConnection, error) {
		return &mcppkg.ServerConnection{
			Name:         name,
			Tools:        []*sdkmcp.Tool{{Name: "search"}},
			Capabilities: mcppkg.CapabilitySnapshot{Tools: true, Resources: true, Prompts: true},
			ServerInfo:   &mcppkg.ServerInfoSnapshot{Name: "remote", Version: "1.2.3"},
		}, nil
	})
	if err := mgr.ApplyConfig(context.Background(), resolved); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}
	secrets := secretspkg.NewStore(nil)
	secrets.SetMCPAuthPath(t.TempDir() + "/mcp-auth.json")
	if err := secrets.PutMCPCredential(mcppkg.CredentialKey(resolved.Servers["remote"].ServerConfig), secretspkg.MCPAuthCredential{
		AccessToken:  "token-a",
		RefreshToken: "refresh-a",
		TokenType:    "Bearer",
		Expiry:       time.Now().UTC().Add(time.Hour),
		UpdatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("PutMCPCredential error: %v", err)
	}
	auth := newMCPAuthController(&mgr, agent.NewToolRegistry(), secrets, func() state.ConfigDoc { return cfgState.Get() })
	ctrl := newMCPOpsController(&mgr, agent.NewToolRegistry(), auth, cfgState, state.NewDocsRepository(newTestStore(), "author"))

	result, err := ctrl.applyList(context.Background(), methods.MCPListRequest{})
	if err != nil {
		t.Fatalf("applyList error: %v", err)
	}
	servers, ok := result["servers"].([]map[string]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("unexpected servers payload: %#v", result["servers"])
	}
	server := servers[0]
	if server["name"] != "remote" || server["state"] != mcppkg.ConnectionStateConnected {
		t.Fatalf("unexpected server state payload: %#v", server)
	}
	if server["oauth_configured"] != true || server["has_credentials"] != true {
		t.Fatalf("expected oauth metadata, got %#v", server)
	}
	if got := server["tool_count"]; got != 1 {
		t.Fatalf("expected tool_count=1, got %#v", got)
	}
}

func TestMCPOpsControllerApplyPutAndRemovePersistConfig(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Relays: state.RelayPolicy{Read: []string{"wss://relay.example"}, Write: []string{"wss://relay.example"}}})
	docs := state.NewDocsRepository(newTestStore(), "author")
	ctrl := newMCPOpsController(nil, nil, nil, cfgState, docs)

	putResult, err := ctrl.applyPut(context.Background(), methods.MCPPutRequest{
		Server: "demo",
		Config: map[string]any{"type": "stdio", "command": "npx", "args": []string{"-y", "server-filesystem", "/tmp"}},
	})
	if err != nil {
		t.Fatalf("applyPut error: %v", err)
	}
	if putResult["ok"] != true {
		t.Fatalf("expected ok result, got %#v", putResult)
	}
	resolved := mcppkg.ResolveConfigDoc(cfgState.Get())
	if !resolved.Enabled || resolved.Servers["demo"].Command != "npx" {
		t.Fatalf("expected persisted mcp config, got %#v", resolved)
	}

	removeResult, err := ctrl.applyRemove(context.Background(), methods.MCPRemoveRequest{Server: "demo"})
	if err != nil {
		t.Fatalf("applyRemove error: %v", err)
	}
	if removeResult["removed"] != true {
		t.Fatalf("expected removed result, got %#v", removeResult)
	}
	resolved = mcppkg.ResolveConfigDoc(cfgState.Get())
	if _, ok := resolved.Servers["demo"]; ok {
		t.Fatalf("expected server removal, got %#v", resolved.Servers)
	}
}

func TestMCPOpsControllerApplyPutReturnsPolicyFilteredServer(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		Relays:  state.RelayPolicy{Read: []string{"wss://relay.example"}, Write: []string{"wss://relay.example"}},
		Extra: map[string]any{
			"mcp": map[string]any{
				"policy": map[string]any{
					"denied": []any{
						map[string]any{"name": "demo"},
					},
				},
			},
		},
	})
	docs := state.NewDocsRepository(newTestStore(), "author")
	ctrl := newMCPOpsController(nil, nil, nil, cfgState, docs)

	putResult, err := ctrl.applyPut(context.Background(), methods.MCPPutRequest{
		Server: "demo",
		Config: map[string]any{"type": "stdio", "command": "npx"},
	})
	if err != nil {
		t.Fatalf("applyPut error: %v", err)
	}
	server, _ := putResult["server"].(map[string]any)
	if server["policy_status"] != mcppkg.PolicyStatusBlocked || server["policy_reason"] != mcppkg.PolicyReasonDenied {
		t.Fatalf("expected blocked server payload after put, got %#v", server)
	}
}

func TestMCPOpsControllerApplyListIncludesPolicyFilteredServers(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Extra: map[string]any{
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"remote": map[string]any{
					"enabled": true,
					"type":    "http",
					"url":     "https://mcp.example.com/http",
				},
			},
			"policy": map[string]any{
				"require_remote_approval": true,
				"approved_servers":        []string{},
			},
		},
	}})
	ctrl := newMCPOpsController(nil, agent.NewToolRegistry(), nil, cfgState, state.NewDocsRepository(newTestStore(), "author"))

	result, err := ctrl.applyList(context.Background(), methods.MCPListRequest{})
	if err != nil {
		t.Fatalf("applyList error: %v", err)
	}
	servers, ok := result["servers"].([]map[string]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("unexpected servers payload: %#v", result["servers"])
	}
	server := servers[0]
	if server["name"] != "remote" || server["policy_status"] != mcppkg.PolicyStatusApprovalRequired {
		t.Fatalf("expected approval-required policy payload, got %#v", server)
	}
	if server["state"] != string(mcppkg.PolicyStatusApprovalRequired) || server["runtime_present"] != false {
		t.Fatalf("unexpected approval-required runtime payload: %#v", server)
	}
}

func TestMCPOpsControllerApplyTestReturnsFailurePayload(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}, Relays: state.RelayPolicy{Read: []string{"wss://relay.example"}, Write: []string{"wss://relay.example"}}})
	ctrl := newMCPOpsController(nil, nil, nil, cfgState, state.NewDocsRepository(newTestStore(), "author"))
	ctrl.managerFactory = func() *mcppkg.Manager {
		mgr := mcppkg.NewManager()
		mgr.SetConnectFunc(func(_ context.Context, name string, _ mcppkg.ServerConfig) (*mcppkg.ServerConnection, error) {
			return nil, fmt.Errorf("dial %s failed", name)
		})
		return mgr
	}
	result, err := ctrl.applyTest(context.Background(), methods.MCPTestRequest{
		Server: "demo",
		Config: map[string]any{"type": "stdio", "command": "demo-mcp"},
	})
	if err != nil {
		t.Fatalf("applyTest error: %v", err)
	}
	if result["ok"] != false {
		t.Fatalf("expected failed test result, got %#v", result)
	}
	if !strings.Contains(fmt.Sprint(result["error"]), "dial demo failed") {
		t.Fatalf("expected connect error, got %#v", result)
	}
}

func TestMCPOpsControllerApplyReconnectReturnsUpdatedSnapshot(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Extra: map[string]any{
		"mcp": map[string]any{
			"enabled": true,
			"servers": map[string]any{
				"demo": map[string]any{"enabled": true, "type": "stdio", "command": "demo-mcp"},
			},
		},
	}})
	resolved := mcppkg.ResolveConfigDoc(cfgState.Get())
	mgr := mcppkg.NewManager()
	connectCount := 0
	mgr.SetConnectFunc(func(_ context.Context, name string, _ mcppkg.ServerConfig) (*mcppkg.ServerConnection, error) {
		connectCount++
		return &mcppkg.ServerConnection{
			Name:         name,
			Tools:        []*sdkmcp.Tool{{Name: fmt.Sprintf("tool-%d", connectCount)}},
			Capabilities: mcppkg.CapabilitySnapshot{Tools: true},
		}, nil
	})
	if err := mgr.ApplyConfig(context.Background(), resolved); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}
	tools := agent.NewToolRegistry()
	ctrl := newMCPOpsController(&mgr, tools, nil, cfgState, state.NewDocsRepository(newTestStore(), "author"))

	result, err := ctrl.applyReconnect(context.Background(), methods.MCPReconnectRequest{Server: "demo"})
	if err != nil {
		t.Fatalf("applyReconnect error: %v", err)
	}
	if result["ok"] != true {
		t.Fatalf("expected ok reconnect result, got %#v", result)
	}
	server, _ := result["server"].(map[string]any)
	if server["state"] != mcppkg.ConnectionStateConnected || intField(server["tool_count"]) != 1 {
		t.Fatalf("unexpected reconnect payload: %#v", server)
	}
}

func intField(v any) int {
	switch raw := v.(type) {
	case int:
		return raw
	case float64:
		return int(raw)
	default:
		return 0
	}
}
