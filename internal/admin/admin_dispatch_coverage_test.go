package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/memory"
	"metiq/internal/store/state"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// mustJSON marshals v to json.RawMessage, failing the test on error.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustJSON: %v", err)
	}
	return raw
}

// nilParams returns nil json.RawMessage (some decoders treat it as empty).
func nilParams() json.RawMessage { return nil }

// emptyParams returns a JSON "{}".
func emptyParams() json.RawMessage { return json.RawMessage(`{}`) }

// ─── dispatch_mcp.go ─────────────────────────────────────────────────────────

func TestDispatchMcp_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{} // all nil

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodMCPList, nilParams()},
		{methods.MethodMCPGet, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPPut, mustJSON(t, map[string]any{"server": "test-server", "config": map[string]any{"command": "uvx"}})},
		{methods.MethodMCPRemove, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPTest, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPReconnect, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPAuthStart, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPAuthRefresh, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPAuthClear, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodSecretsReload, nilParams()},
		{methods.MethodSecretsResolve, mustJSON(t, map[string]any{"targetIds": []string{"id1"}})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchMcp(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchMcp_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		MCPList:        func(_ context.Context, _ methods.MCPListRequest) (map[string]any, error) { return ok, nil },
		MCPGet:         func(_ context.Context, _ methods.MCPGetRequest) (map[string]any, error) { return ok, nil },
		MCPPut:         func(_ context.Context, _ methods.MCPPutRequest) (map[string]any, error) { return ok, nil },
		MCPRemove:      func(_ context.Context, _ methods.MCPRemoveRequest) (map[string]any, error) { return ok, nil },
		MCPTest:        func(_ context.Context, _ methods.MCPTestRequest) (map[string]any, error) { return ok, nil },
		MCPReconnect:   func(_ context.Context, _ methods.MCPReconnectRequest) (map[string]any, error) { return ok, nil },
		MCPAuthStart:   func(_ context.Context, _ methods.MCPAuthStartRequest) (map[string]any, error) { return ok, nil },
		MCPAuthRefresh: func(_ context.Context, _ methods.MCPAuthRefreshRequest) (map[string]any, error) { return ok, nil },
		MCPAuthClear:   func(_ context.Context, _ methods.MCPAuthClearRequest) (map[string]any, error) { return ok, nil },
		SecretsReload:  func(_ context.Context, _ methods.SecretsReloadRequest) (map[string]any, error) { return ok, nil },
		SecretsResolve: func(_ context.Context, _ methods.SecretsResolveRequest) (map[string]any, error) { return ok, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodMCPList, nilParams()},
		{methods.MethodMCPGet, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPPut, mustJSON(t, map[string]any{"server": "test-server", "config": map[string]any{"command": "uvx"}})},
		{methods.MethodMCPRemove, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPTest, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPReconnect, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPAuthStart, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPAuthRefresh, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodMCPAuthClear, mustJSON(t, map[string]any{"server": "test-server"})},
		{methods.MethodSecretsReload, nilParams()},
		{methods.MethodSecretsResolve, mustJSON(t, map[string]any{"targetIds": []string{"id1"}})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			result, status, err := dispatchMcp(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestDispatchMcp_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchMcp(ctx, ServerOptions{}, "mcp.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_exec.go ────────────────────────────────────────────────────────

func TestDispatchExec_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodExecApprovalsGet, nilParams()},
		{methods.MethodExecApprovalsSet, mustJSON(t, map[string]any{"approvals": map[string]any{}})},
		{methods.MethodExecApprovalRequest, mustJSON(t, map[string]any{"command": "ls"})},
		{methods.MethodExecApprovalWaitDecision, mustJSON(t, map[string]any{"id": "req-1"})},
		{methods.MethodExecApprovalResolve, mustJSON(t, map[string]any{"id": "req-1", "decision": "allow"})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchExec(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchExec_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		ExecApprovalsGet:         func(_ context.Context, _ methods.ExecApprovalsGetRequest) (map[string]any, error) { return ok, nil },
		ExecApprovalsSet:         func(_ context.Context, _ methods.ExecApprovalsSetRequest) (map[string]any, error) { return ok, nil },
		ExecApprovalRequest:      func(_ context.Context, _ methods.ExecApprovalRequestRequest) (map[string]any, error) { return ok, nil },
		ExecApprovalWaitDecision: func(_ context.Context, _ methods.ExecApprovalWaitDecisionRequest) (map[string]any, error) { return ok, nil },
		ExecApprovalResolve:      func(_ context.Context, _ methods.ExecApprovalResolveRequest) (map[string]any, error) { return ok, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodExecApprovalsGet, nilParams()},
		{methods.MethodExecApprovalsSet, mustJSON(t, map[string]any{"approvals": map[string]any{}})},
		{methods.MethodExecApprovalRequest, mustJSON(t, map[string]any{"command": "ls"})},
		{methods.MethodExecApprovalWaitDecision, mustJSON(t, map[string]any{"id": "req-1"})},
		{methods.MethodExecApprovalResolve, mustJSON(t, map[string]any{"id": "req-1", "decision": "allow"})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			result, status, err := dispatchExec(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestDispatchExec_WaitDecision_NotFound(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{
		ExecApprovalWaitDecision: func(_ context.Context, _ methods.ExecApprovalWaitDecisionRequest) (map[string]any, error) {
			return nil, state.ErrNotFound
		},
	}
	call := methods.CallRequest{
		Method: methods.MethodExecApprovalWaitDecision,
		Params: mustJSON(t, map[string]any{"id": "req-1"}),
	}
	_, status, _ := dispatchExec(ctx, opts, methods.MethodExecApprovalWaitDecision, call, cfg)
	if status != http.StatusNotFound {
		t.Fatalf("status=%d, want %d", status, http.StatusNotFound)
	}
}

func TestDispatchExec_Resolve_NotFound(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{
		ExecApprovalResolve: func(_ context.Context, _ methods.ExecApprovalResolveRequest) (map[string]any, error) {
			return nil, state.ErrNotFound
		},
	}
	call := methods.CallRequest{
		Method: methods.MethodExecApprovalResolve,
		Params: mustJSON(t, map[string]any{"id": "req-1", "decision": "allow"}),
	}
	_, status, _ := dispatchExec(ctx, opts, methods.MethodExecApprovalResolve, call, cfg)
	if status != http.StatusNotFound {
		t.Fatalf("status=%d, want %d", status, http.StatusNotFound)
	}
}

func TestDispatchExec_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchExec(ctx, ServerOptions{}, "exec.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_plugins.go ─────────────────────────────────────────────────────

func TestDispatchPlugins_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodPluginsInstall, mustJSON(t, map[string]any{"plugin_id": "my-plugin", "install": map[string]any{"source": "npm"}})},
		{methods.MethodPluginsUninstall, mustJSON(t, map[string]any{"plugin_id": "my-plugin"})},
		{methods.MethodPluginsUpdate, nilParams()},
		{methods.MethodPluginsRegistryList, nilParams()},
		{methods.MethodPluginsRegistryGet, mustJSON(t, map[string]any{"plugin_id": "my-plugin"})},
		{methods.MethodPluginsRegistrySearch, nilParams()},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchPlugins(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchPlugins_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		PluginsInstall:        func(_ context.Context, _ methods.PluginsInstallRequest) (map[string]any, error) { return ok, nil },
		PluginsUninstall:      func(_ context.Context, _ methods.PluginsUninstallRequest) (map[string]any, error) { return ok, nil },
		PluginsUpdate:         func(_ context.Context, _ methods.PluginsUpdateRequest) (map[string]any, error) { return ok, nil },
		PluginsRegistryList:   func(_ context.Context, _ methods.PluginsRegistryListRequest) (map[string]any, error) { return ok, nil },
		PluginsRegistryGet:    func(_ context.Context, _ methods.PluginsRegistryGetRequest) (map[string]any, error) { return ok, nil },
		PluginsRegistrySearch: func(_ context.Context, _ methods.PluginsRegistrySearchRequest) (map[string]any, error) { return ok, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodPluginsInstall, mustJSON(t, map[string]any{"plugin_id": "my-plugin", "install": map[string]any{"source": "npm"}})},
		{methods.MethodPluginsUninstall, mustJSON(t, map[string]any{"plugin_id": "my-plugin"})},
		{methods.MethodPluginsUpdate, nilParams()},
		{methods.MethodPluginsRegistryList, nilParams()},
		{methods.MethodPluginsRegistryGet, mustJSON(t, map[string]any{"plugin_id": "my-plugin"})},
		{methods.MethodPluginsRegistrySearch, nilParams()},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			result, status, err := dispatchPlugins(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestDispatchPlugins_NotFound(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}

	cases := []struct {
		name   string
		method string
		params json.RawMessage
		opts   ServerOptions
	}{
		{
			"Install_NotFound",
			methods.MethodPluginsInstall,
			mustJSON(t, map[string]any{"plugin_id": "my-plugin", "install": map[string]any{"source": "npm"}}),
			ServerOptions{
				PluginsInstall: func(_ context.Context, _ methods.PluginsInstallRequest) (map[string]any, error) {
					return nil, state.ErrNotFound
				},
			},
		},
		{
			"Uninstall_NotFound",
			methods.MethodPluginsUninstall,
			mustJSON(t, map[string]any{"plugin_id": "my-plugin"}),
			ServerOptions{
				PluginsUninstall: func(_ context.Context, _ methods.PluginsUninstallRequest) (map[string]any, error) {
					return nil, state.ErrNotFound
				},
			},
		},
		{
			"Update_NotFound",
			methods.MethodPluginsUpdate,
			emptyParams(),
			ServerOptions{
				PluginsUpdate: func(_ context.Context, _ methods.PluginsUpdateRequest) (map[string]any, error) {
					return nil, state.ErrNotFound
				},
			},
		},
		{
			"RegistryGet_NotFound",
			methods.MethodPluginsRegistryGet,
			mustJSON(t, map[string]any{"plugin_id": "my-plugin"}),
			ServerOptions{
				PluginsRegistryGet: func(_ context.Context, _ methods.PluginsRegistryGetRequest) (map[string]any, error) {
					return nil, state.ErrNotFound
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, _ := dispatchPlugins(ctx, tc.opts, tc.method, call, cfg)
			if status != http.StatusNotFound {
				t.Fatalf("status=%d, want %d", status, http.StatusNotFound)
			}
		})
	}
}

func TestDispatchPlugins_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchPlugins(ctx, ServerOptions{}, "plugins.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_nodes.go ───────────────────────────────────────────────────────

func TestDispatchNodes_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodNodePairRequest, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodNodePairList, nilParams()},
		{methods.MethodNodePairApprove, mustJSON(t, map[string]any{"request_id": "r1"})},
		{methods.MethodNodePairReject, mustJSON(t, map[string]any{"request_id": "r1"})},
		{methods.MethodNodePairVerify, mustJSON(t, map[string]any{"node_id": "n1", "token": "tok"})},
		{methods.MethodDevicePairList, nilParams()},
		{methods.MethodDevicePairApprove, mustJSON(t, map[string]any{"request_id": "r1"})},
		{methods.MethodDevicePairReject, mustJSON(t, map[string]any{"request_id": "r1"})},
		{methods.MethodDevicePairRemove, mustJSON(t, map[string]any{"device_id": "d1"})},
		{methods.MethodDeviceTokenRotate, mustJSON(t, map[string]any{"device_id": "d1", "role": "admin"})},
		{methods.MethodDeviceTokenRevoke, mustJSON(t, map[string]any{"device_id": "d1", "role": "admin"})},
		{methods.MethodNodeList, nilParams()},
		{methods.MethodNodeDescribe, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodNodeRename, mustJSON(t, map[string]any{"node_id": "n1", "name": "New"})},
		{methods.MethodNodeCanvasCapabilityRefresh, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodNodeInvoke, mustJSON(t, map[string]any{"node_id": "n1", "command": "ping"})},
		{methods.MethodNodeEvent, mustJSON(t, map[string]any{"run_id": "run-1", "node_id": "n1", "type": "test"})},
		{methods.MethodNodeResult, mustJSON(t, map[string]any{"run_id": "run-1"})},
		{methods.MethodNodePendingEnqueue, mustJSON(t, map[string]any{"node_id": "n1", "command": "test"})},
		{methods.MethodNodePendingPull, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodNodePendingAck, mustJSON(t, map[string]any{"node_id": "n1", "ids": []string{"id1"}})},
		{methods.MethodNodePendingDrain, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodExecApprovalsNodeGet, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodExecApprovalsNodeSet, mustJSON(t, map[string]any{"node_id": "n1", "approvals": map[string]any{}})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchNodes(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchNodes_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		NodePairRequest:             func(_ context.Context, _ methods.NodePairRequest) (map[string]any, error) { return ok, nil },
		NodePairList:                func(_ context.Context, _ methods.NodePairListRequest) (map[string]any, error) { return ok, nil },
		NodePairApprove:             func(_ context.Context, _ methods.NodePairApproveRequest) (map[string]any, error) { return ok, nil },
		NodePairReject:              func(_ context.Context, _ methods.NodePairRejectRequest) (map[string]any, error) { return ok, nil },
		NodePairVerify:              func(_ context.Context, _ methods.NodePairVerifyRequest) (map[string]any, error) { return ok, nil },
		DevicePairList:              func(_ context.Context, _ methods.DevicePairListRequest) (map[string]any, error) { return ok, nil },
		DevicePairApprove:           func(_ context.Context, _ methods.DevicePairApproveRequest) (map[string]any, error) { return ok, nil },
		DevicePairReject:            func(_ context.Context, _ methods.DevicePairRejectRequest) (map[string]any, error) { return ok, nil },
		DevicePairRemove:            func(_ context.Context, _ methods.DevicePairRemoveRequest) (map[string]any, error) { return ok, nil },
		DeviceTokenRotate:           func(_ context.Context, _ methods.DeviceTokenRotateRequest) (map[string]any, error) { return ok, nil },
		DeviceTokenRevoke:           func(_ context.Context, _ methods.DeviceTokenRevokeRequest) (map[string]any, error) { return ok, nil },
		NodeList:                    func(_ context.Context, _ methods.NodeListRequest) (map[string]any, error) { return ok, nil },
		NodeDescribe:                func(_ context.Context, _ methods.NodeDescribeRequest) (map[string]any, error) { return ok, nil },
		NodeRename:                  func(_ context.Context, _ methods.NodeRenameRequest) (map[string]any, error) { return ok, nil },
		NodeCanvasCapabilityRefresh: func(_ context.Context, _ methods.NodeCanvasCapabilityRefreshRequest) (map[string]any, error) { return ok, nil },
		NodeInvoke:                  func(_ context.Context, _ methods.NodeInvokeRequest) (map[string]any, error) { return ok, nil },
		NodeEvent:                   func(_ context.Context, _ methods.NodeEventRequest) (map[string]any, error) { return ok, nil },
		NodeResult:                  func(_ context.Context, _ methods.NodeResultRequest) (map[string]any, error) { return ok, nil },
		NodePendingEnqueue:          func(_ context.Context, _ methods.NodePendingEnqueueRequest) (map[string]any, error) { return ok, nil },
		NodePendingPull:             func(_ context.Context, _ methods.NodePendingPullRequest) (map[string]any, error) { return ok, nil },
		NodePendingAck:              func(_ context.Context, _ methods.NodePendingAckRequest) (map[string]any, error) { return ok, nil },
		NodePendingDrain:            func(_ context.Context, _ methods.NodePendingDrainRequest) (map[string]any, error) { return ok, nil },
		ExecApprovalsNodeGet:        func(_ context.Context, _ methods.ExecApprovalsNodeGetRequest) (map[string]any, error) { return ok, nil },
		ExecApprovalsNodeSet:        func(_ context.Context, _ methods.ExecApprovalsNodeSetRequest) (map[string]any, error) { return ok, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodNodePairRequest, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodNodePairList, nilParams()},
		{methods.MethodNodePairApprove, mustJSON(t, map[string]any{"request_id": "r1"})},
		{methods.MethodNodePairReject, mustJSON(t, map[string]any{"request_id": "r1"})},
		{methods.MethodNodePairVerify, mustJSON(t, map[string]any{"node_id": "n1", "token": "tok"})},
		{methods.MethodDevicePairList, nilParams()},
		{methods.MethodDevicePairApprove, mustJSON(t, map[string]any{"request_id": "r1"})},
		{methods.MethodDevicePairReject, mustJSON(t, map[string]any{"request_id": "r1"})},
		{methods.MethodDevicePairRemove, mustJSON(t, map[string]any{"device_id": "d1"})},
		{methods.MethodDeviceTokenRotate, mustJSON(t, map[string]any{"device_id": "d1", "role": "admin"})},
		{methods.MethodDeviceTokenRevoke, mustJSON(t, map[string]any{"device_id": "d1", "role": "admin"})},
		{methods.MethodNodeList, nilParams()},
		{methods.MethodNodeDescribe, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodNodeRename, mustJSON(t, map[string]any{"node_id": "n1", "name": "New"})},
		{methods.MethodNodeCanvasCapabilityRefresh, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodNodeInvoke, mustJSON(t, map[string]any{"node_id": "n1", "command": "ping"})},
		{methods.MethodNodeEvent, mustJSON(t, map[string]any{"run_id": "run-1", "node_id": "n1", "type": "test"})},
		{methods.MethodNodeResult, mustJSON(t, map[string]any{"run_id": "run-1"})},
		{methods.MethodNodeInvokeResult, mustJSON(t, map[string]any{"run_id": "run-1"})},
		{methods.MethodNodePendingEnqueue, mustJSON(t, map[string]any{"node_id": "n1", "command": "test"})},
		{methods.MethodNodePendingPull, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodNodePendingAck, mustJSON(t, map[string]any{"node_id": "n1", "ids": []string{"id1"}})},
		{methods.MethodNodePendingDrain, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodExecApprovalsNodeGet, mustJSON(t, map[string]any{"node_id": "n1"})},
		{methods.MethodExecApprovalsNodeSet, mustJSON(t, map[string]any{"node_id": "n1", "approvals": map[string]any{}})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			result, status, err := dispatchNodes(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestDispatchNodes_NotFound(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}

	// Test a few representative methods that wrap ErrNotFound → 404
	cases := []struct {
		name   string
		method string
		params json.RawMessage
		opts   ServerOptions
	}{
		{
			"NodePairApprove",
			methods.MethodNodePairApprove,
			mustJSON(t, map[string]any{"request_id": "r1"}),
			ServerOptions{NodePairApprove: func(_ context.Context, _ methods.NodePairApproveRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"NodeDescribe",
			methods.MethodNodeDescribe,
			mustJSON(t, map[string]any{"node_id": "n1"}),
			ServerOptions{NodeDescribe: func(_ context.Context, _ methods.NodeDescribeRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"DevicePairApprove",
			methods.MethodDevicePairApprove,
			mustJSON(t, map[string]any{"request_id": "r1"}),
			ServerOptions{DevicePairApprove: func(_ context.Context, _ methods.DevicePairApproveRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"NodeEvent",
			methods.MethodNodeEvent,
			mustJSON(t, map[string]any{"run_id": "run-1", "node_id": "n1", "type": "test"}),
			ServerOptions{NodeEvent: func(_ context.Context, _ methods.NodeEventRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"NodeResult",
			methods.MethodNodeResult,
			mustJSON(t, map[string]any{"run_id": "run-1"}),
			ServerOptions{NodeResult: func(_ context.Context, _ methods.NodeResultRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, _ := dispatchNodes(ctx, tc.opts, tc.method, call, cfg)
			if status != http.StatusNotFound {
				t.Fatalf("status=%d, want %d", status, http.StatusNotFound)
			}
		})
	}
}

func TestDispatchNodes_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchNodes(ctx, ServerOptions{}, "nodes.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_agents.go ──────────────────────────────────────────────────────

func TestDispatchAgents_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodAgent, mustJSON(t, map[string]any{"message": "test"})},
		{methods.MethodAgentWait, mustJSON(t, map[string]any{"run_id": "r1"})},
		{methods.MethodAgentsList, emptyParams()},
		{methods.MethodAgentsCreate, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodAgentsUpdate, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodAgentsDelete, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodAgentsFilesList, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodAgentsFilesGet, mustJSON(t, map[string]any{"agent_id": "a1", "name": "file.txt"})},
		{methods.MethodAgentsFilesSet, mustJSON(t, map[string]any{"agent_id": "a1", "name": "file.txt", "content": "data"})},
		{methods.MethodModelsList, emptyParams()},
		{methods.MethodToolsCatalog, emptyParams()},
		{methods.MethodToolsProfileGet, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodToolsProfileSet, mustJSON(t, map[string]any{"agent_id": "a1", "profile": "default"})},
		{methods.MethodSkillsStatus, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodSkillsBins, nilParams()},
		{methods.MethodSkillsInstall, mustJSON(t, map[string]any{"name": "my-skill", "install_id": "inst-1"})},
		{methods.MethodSkillsUpdate, mustJSON(t, map[string]any{"skill_key": "my-skill"})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchAgents(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchAgents_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		StartAgent:      func(_ context.Context, _ methods.AgentRequest) (map[string]any, error) { return ok, nil },
		WaitAgent:       func(_ context.Context, _ methods.AgentWaitRequest) (map[string]any, error) { return ok, nil },
		ListAgents:      func(_ context.Context, _ methods.AgentsListRequest) (map[string]any, error) { return ok, nil },
		CreateAgent:     func(_ context.Context, _ methods.AgentsCreateRequest) (map[string]any, error) { return ok, nil },
		UpdateAgent:     func(_ context.Context, _ methods.AgentsUpdateRequest) (map[string]any, error) { return ok, nil },
		DeleteAgent:     func(_ context.Context, _ methods.AgentsDeleteRequest) (map[string]any, error) { return ok, nil },
		ListAgentFiles:  func(_ context.Context, _ methods.AgentsFilesListRequest) (map[string]any, error) { return ok, nil },
		GetAgentFile:    func(_ context.Context, _ methods.AgentsFilesGetRequest) (map[string]any, error) { return ok, nil },
		SetAgentFile:    func(_ context.Context, _ methods.AgentsFilesSetRequest) (map[string]any, error) { return ok, nil },
		ListModels:      func(_ context.Context, _ methods.ModelsListRequest) (map[string]any, error) { return ok, nil },
		ToolsCatalog:    func(_ context.Context, _ methods.ToolsCatalogRequest) (map[string]any, error) { return ok, nil },
		ToolsProfileGet: func(_ context.Context, _ methods.ToolsProfileGetRequest) (map[string]any, error) { return ok, nil },
		ToolsProfileSet: func(_ context.Context, _ methods.ToolsProfileSetRequest) (map[string]any, error) { return ok, nil },
		SkillsStatus:    func(_ context.Context, _ methods.SkillsStatusRequest) (map[string]any, error) { return ok, nil },
		SkillsBins:      func(_ context.Context, _ methods.SkillsBinsRequest) (map[string]any, error) { return ok, nil },
		SkillsInstall:   func(_ context.Context, _ methods.SkillsInstallRequest) (map[string]any, error) { return ok, nil },
		SkillsUpdate:    func(_ context.Context, _ methods.SkillsUpdateRequest) (map[string]any, error) { return ok, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodAgent, mustJSON(t, map[string]any{"message": "test"})},
		{methods.MethodAgentWait, mustJSON(t, map[string]any{"run_id": "r1"})},
		{methods.MethodAgentsList, emptyParams()},
		{methods.MethodAgentsCreate, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodAgentsUpdate, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodAgentsDelete, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodAgentsFilesList, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodAgentsFilesGet, mustJSON(t, map[string]any{"agent_id": "a1", "name": "file.txt"})},
		{methods.MethodAgentsFilesSet, mustJSON(t, map[string]any{"agent_id": "a1", "name": "file.txt", "content": "data"})},
		{methods.MethodModelsList, emptyParams()},
		{methods.MethodToolsCatalog, emptyParams()},
		{methods.MethodToolsProfileGet, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodToolsProfileSet, mustJSON(t, map[string]any{"agent_id": "a1", "profile": "default"})},
		{methods.MethodSkillsStatus, mustJSON(t, map[string]any{"agent_id": "a1"})},
		{methods.MethodSkillsBins, nilParams()},
		{methods.MethodSkillsInstall, mustJSON(t, map[string]any{"name": "my-skill", "install_id": "inst-1"})},
		{methods.MethodSkillsUpdate, mustJSON(t, map[string]any{"skill_key": "my-skill"})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			result, status, err := dispatchAgents(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestDispatchAgents_IdentityDefaults(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}

	// AgentIdentity nil → returns default identity
	call := methods.CallRequest{
		Method: methods.MethodAgentIdentityGet,
		Params: mustJSON(t, map[string]any{"session_id": "s1"}),
	}
	result, status, err := dispatchAgents(ctx, ServerOptions{}, methods.MethodAgentIdentityGet, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d, want %d", status, http.StatusOK)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if m["agent_id"] != "main" {
		t.Errorf("agent_id=%v, want main", m["agent_id"])
	}
}

func TestDispatchAgents_GatewayIdentityDefault(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}

	// GatewayIdentity nil → returns default
	call := methods.CallRequest{
		Method: methods.MethodGatewayIdentityGet,
		Params: emptyParams(),
	}
	result, status, err := dispatchAgents(ctx, ServerOptions{}, methods.MethodGatewayIdentityGet, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d, want %d", status, http.StatusOK)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	if m["deviceId"] != "metiq" {
		t.Errorf("deviceId=%v, want metiq", m["deviceId"])
	}
}

func TestDispatchAgents_NotFound(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}

	cases := []struct {
		name   string
		method string
		params json.RawMessage
		opts   ServerOptions
	}{
		{
			"UpdateAgent",
			methods.MethodAgentsUpdate,
			mustJSON(t, map[string]any{"agent_id": "a1"}),
			ServerOptions{UpdateAgent: func(_ context.Context, _ methods.AgentsUpdateRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"DeleteAgent",
			methods.MethodAgentsDelete,
			mustJSON(t, map[string]any{"agent_id": "a1"}),
			ServerOptions{DeleteAgent: func(_ context.Context, _ methods.AgentsDeleteRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"ListAgentFiles",
			methods.MethodAgentsFilesList,
			mustJSON(t, map[string]any{"agent_id": "a1"}),
			ServerOptions{ListAgentFiles: func(_ context.Context, _ methods.AgentsFilesListRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, _ := dispatchAgents(ctx, tc.opts, tc.method, call, cfg)
			if status != http.StatusNotFound {
				t.Fatalf("status=%d, want %d", status, http.StatusNotFound)
			}
		})
	}
}

func TestDispatchAgents_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchAgents(ctx, ServerOptions{}, "agents.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_cron.go ────────────────────────────────────────────────────────

func TestDispatchCron_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodCronList, nilParams()},
		{methods.MethodCronStatus, mustJSON(t, map[string]any{"id": "cron-1"})},
		{methods.MethodCronAdd, mustJSON(t, map[string]any{"schedule": "* * * * *", "method": "status"})},
		{methods.MethodCronUpdate, mustJSON(t, map[string]any{"id": "cron-1", "schedule": "0 * * * *"})},
		{methods.MethodCronRemove, mustJSON(t, map[string]any{"id": "cron-1"})},
		{methods.MethodCronRun, mustJSON(t, map[string]any{"id": "cron-1"})},
		{methods.MethodCronRuns, nilParams()},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchCron(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchCron_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		CronList:   func(_ context.Context, _ methods.CronListRequest) (map[string]any, error) { return ok, nil },
		CronStatus: func(_ context.Context, _ methods.CronStatusRequest) (map[string]any, error) { return ok, nil },
		CronAdd:    func(_ context.Context, _ methods.CronAddRequest) (map[string]any, error) { return ok, nil },
		CronUpdate: func(_ context.Context, _ methods.CronUpdateRequest) (map[string]any, error) { return ok, nil },
		CronRemove: func(_ context.Context, _ methods.CronRemoveRequest) (map[string]any, error) { return ok, nil },
		CronRun:    func(_ context.Context, _ methods.CronRunRequest) (map[string]any, error) { return ok, nil },
		CronRuns:   func(_ context.Context, _ methods.CronRunsRequest) (map[string]any, error) { return ok, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodCronList, nilParams()},
		{methods.MethodCronStatus, mustJSON(t, map[string]any{"id": "cron-1"})},
		{methods.MethodCronAdd, mustJSON(t, map[string]any{"schedule": "* * * * *", "method": "status"})},
		{methods.MethodCronUpdate, mustJSON(t, map[string]any{"id": "cron-1", "schedule": "0 * * * *"})},
		{methods.MethodCronRemove, mustJSON(t, map[string]any{"id": "cron-1"})},
		{methods.MethodCronRun, mustJSON(t, map[string]any{"id": "cron-1"})},
		{methods.MethodCronRuns, nilParams()},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			result, status, err := dispatchCron(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestDispatchCron_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchCron(ctx, ServerOptions{}, "cron.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_runtime.go ─────────────────────────────────────��───────────────

func TestDispatchRuntime_NilProviderDefaults(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{} // nil providers

	// logs.tail with nil provider returns empty default
	call := methods.CallRequest{Method: methods.MethodLogsTail, Params: nilParams()}
	result, status, err := dispatchRuntime(ctx, opts, methods.MethodLogsTail, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d, want %d", status, http.StatusOK)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if m["truncated"] != false {
		t.Errorf("truncated=%v, want false", m["truncated"])
	}

	// runtime.observe with nil provider returns empty default
	call = methods.CallRequest{Method: methods.MethodRuntimeObserve, Params: nilParams()}
	result, status, err = dispatchRuntime(ctx, opts, methods.MethodRuntimeObserve, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d, want %d", status, http.StatusOK)
	}
}

func TestDispatchRuntime_RelayPolicy_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	call := methods.CallRequest{Method: methods.MethodRelayPolicyGet, Params: emptyParams()}
	_, status, err := dispatchRuntime(ctx, opts, methods.MethodRelayPolicyGet, call, cfg)
	if status != http.StatusNotImplemented {
		t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
	}
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestDispatchRuntime_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		TailLogs: func(_ context.Context, _ int64, _ int, _ int) (map[string]any, error) { return ok, nil },
		ObserveRuntime: func(_ context.Context, _ methods.RuntimeObserveRequest) (map[string]any, error) {
			return ok, nil
		},
		GetRelayPolicy: func(_ context.Context) (methods.RelayPolicyResponse, error) {
			return methods.RelayPolicyResponse{}, nil
		},
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodLogsTail, nilParams()},
		{methods.MethodRuntimeObserve, nilParams()},
		{methods.MethodRelayPolicyGet, emptyParams()},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchRuntime(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
		})
	}
}

func TestDispatchRuntime_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchRuntime(ctx, ServerOptions{}, "runtime.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_channels.go ────────────────────────────────────────────────────

func TestDispatchChannels_NilProviderDefaults(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	// channels.status nil → default response
	call := methods.CallRequest{Method: methods.MethodChannelsStatus, Params: nilParams()}
	result, status, err := dispatchChannels(ctx, opts, methods.MethodChannelsStatus, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d, want %d", status, http.StatusOK)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// channels.logout nil → default response
	call = methods.CallRequest{Method: methods.MethodChannelsLogout, Params: mustJSON(t, map[string]any{"channel": "nostr"})}
	result, status, err = dispatchChannels(ctx, opts, methods.MethodChannelsLogout, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d, want %d", status, http.StatusOK)
	}

	// usage.cost nil → default response
	call = methods.CallRequest{Method: methods.MethodUsageCost, Params: nilParams()}
	result, status, err = dispatchChannels(ctx, opts, methods.MethodUsageCost, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d, want %d", status, http.StatusOK)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestDispatchChannels_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		ChannelsStatus: func(_ context.Context, _ methods.ChannelsStatusRequest) (map[string]any, error) { return ok, nil },
		ChannelsLogout: func(_ context.Context, _ string) (map[string]any, error) { return ok, nil },
		UsageCost:      func(_ context.Context, _ methods.UsageCostRequest) (map[string]any, error) { return ok, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodChannelsStatus, nilParams()},
		{methods.MethodChannelsLogout, mustJSON(t, map[string]any{"channel": "nostr"})},
		{methods.MethodUsageCost, nilParams()},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			result, status, err := dispatchChannels(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestDispatchChannels_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchChannels(ctx, ServerOptions{}, "channels.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_media.go ───────────────────────────────────────────────────────

func TestDispatchMedia_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodTalkConfig, emptyParams()},
		{methods.MethodTalkMode, mustJSON(t, map[string]any{"mode": "voice"})},
		{methods.MethodBrowserRequest, mustJSON(t, map[string]any{"method": "GET", "path": "/test"})},
		{methods.MethodVoicewakeGet, emptyParams()},
		{methods.MethodVoicewakeSet, emptyParams()},
		{methods.MethodTTSStatus, emptyParams()},
		{methods.MethodTTSProviders, emptyParams()},
		{methods.MethodTTSSetProvider, mustJSON(t, map[string]any{"provider": "default"})},
		{methods.MethodTTSEnable, emptyParams()},
		{methods.MethodTTSDisable, emptyParams()},
		{methods.MethodTTSConvert, mustJSON(t, map[string]any{"text": "hello"})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchMedia(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchMedia_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		TalkConfig:     func(_ context.Context, _ methods.TalkConfigRequest) (map[string]any, error) { return ok, nil },
		TalkMode:       func(_ context.Context, _ methods.TalkModeRequest) (map[string]any, error) { return ok, nil },
		BrowserRequest: func(_ context.Context, _ methods.BrowserRequestRequest) (map[string]any, error) { return ok, nil },
		VoicewakeGet:   func(_ context.Context, _ methods.VoicewakeGetRequest) (map[string]any, error) { return ok, nil },
		VoicewakeSet:   func(_ context.Context, _ methods.VoicewakeSetRequest) (map[string]any, error) { return ok, nil },
		TTSStatus:      func(_ context.Context, _ methods.TTSStatusRequest) (map[string]any, error) { return ok, nil },
		TTSProviders:   func(_ context.Context, _ methods.TTSProvidersRequest) (map[string]any, error) { return ok, nil },
		TTSSetProvider: func(_ context.Context, _ methods.TTSSetProviderRequest) (map[string]any, error) { return ok, nil },
		TTSEnable:      func(_ context.Context, _ methods.TTSEnableRequest) (map[string]any, error) { return ok, nil },
		TTSDisable:     func(_ context.Context, _ methods.TTSDisableRequest) (map[string]any, error) { return ok, nil },
		TTSConvert:     func(_ context.Context, _ methods.TTSConvertRequest) (map[string]any, error) { return ok, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodTalkConfig, emptyParams()},
		{methods.MethodTalkMode, mustJSON(t, map[string]any{"mode": "voice"})},
		{methods.MethodBrowserRequest, mustJSON(t, map[string]any{"method": "GET", "path": "/test"})},
		{methods.MethodVoicewakeGet, emptyParams()},
		{methods.MethodVoicewakeSet, emptyParams()},
		{methods.MethodTTSStatus, emptyParams()},
		{methods.MethodTTSProviders, emptyParams()},
		{methods.MethodTTSSetProvider, mustJSON(t, map[string]any{"provider": "default"})},
		{methods.MethodTTSEnable, emptyParams()},
		{methods.MethodTTSDisable, emptyParams()},
		{methods.MethodTTSConvert, mustJSON(t, map[string]any{"text": "hello"})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			result, status, err := dispatchMedia(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
		})
	}
}

func TestDispatchMedia_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchMedia(ctx, ServerOptions{}, "media.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_system.go (selected methods) ───────────────────────────────────

func TestDispatchSystem_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodMemorySearch, mustJSON(t, map[string]any{"query": "hello"})},
		{methods.MethodSandboxRun, mustJSON(t, map[string]any{"cmd": []string{"echo", "hi"}})},
		{methods.MethodWizardStart, emptyParams()},
		{methods.MethodWizardNext, mustJSON(t, map[string]any{"id": "wiz-1"})},
		{methods.MethodWizardCancel, mustJSON(t, map[string]any{"id": "wiz-1"})},
		{methods.MethodWizardStatus, emptyParams()},
		{methods.MethodUpdateRun, emptyParams()},
		{methods.MethodLastHeartbeat, nilParams()},
		{methods.MethodSetHeartbeats, mustJSON(t, map[string]any{"enabled": false})},
		{methods.MethodWake, mustJSON(t, map[string]any{"text": "wake up"})},
		{methods.MethodSystemPresence, nilParams()},
		{methods.MethodSystemEvent, mustJSON(t, map[string]any{"text": "test event"})},
		{methods.MethodSend, mustJSON(t, map[string]any{"to": "0000000000000000000000000000000000000000000000000000000000000001", "message": "hi"})},
		{methods.MethodPoll, mustJSON(t, map[string]any{"to": "0000000000000000000000000000000000000000000000000000000000000001", "question": "A?", "options": []string{"a", "b"}})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchSystem(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchSystem_Defaults(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	// supported_methods with nil provider → returns default list
	call := methods.CallRequest{Method: methods.MethodSupportedMethods, Params: emptyParams()}
	result, status, err := dispatchSystem(ctx, opts, methods.MethodSupportedMethods, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// health always returns ok
	call = methods.CallRequest{Method: methods.MethodHealth, Params: emptyParams()}
	result, status, err = dispatchSystem(ctx, opts, methods.MethodHealth, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}

	// doctor.memory.status → returns available: false
	call = methods.CallRequest{Method: methods.MethodDoctorMemoryStatus, Params: emptyParams()}
	result, status, err = dispatchSystem(ctx, opts, methods.MethodDoctorMemoryStatus, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}

	// status → returns status info
	call = methods.CallRequest{Method: methods.MethodStatus, Params: emptyParams()}
	result, status, err = dispatchSystem(ctx, opts, methods.MethodStatus, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}

	// usage.status nil → default
	call = methods.CallRequest{Method: methods.MethodUsageStatus, Params: emptyParams()}
	result, status, err = dispatchSystem(ctx, opts, methods.MethodUsageStatus, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	// Nil UsageStatus provider returns a default response with zero totals.
	usageMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if usageMap["ok"] != true {
		t.Errorf("expected ok=true, got %v", usageMap["ok"])
	}

	// chat.abort with run_id only → no-op
	call = methods.CallRequest{Method: methods.MethodChatAbort, Params: mustJSON(t, map[string]any{"run_id": "r1"})}
	_, status, err = dispatchSystem(ctx, opts, methods.MethodChatAbort, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}

	// chat.abort with session_id → works even without AbortChat
	call = methods.CallRequest{Method: methods.MethodChatAbort, Params: mustJSON(t, map[string]any{"session_id": "s1"})}
	_, status, err = dispatchSystem(ctx, opts, methods.MethodChatAbort, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
}

func TestDispatchSystem_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}

	opts := ServerOptions{
		SearchMemory:    func(_ string, _ int) []memory.IndexedMemory { return nil },
		SandboxRun:      func(_ context.Context, _ methods.SandboxRunRequest) (map[string]any, error) { return ok, nil },
		WizardStart:     func(_ context.Context, _ methods.WizardStartRequest) (map[string]any, error) { return ok, nil },
		WizardNext:      func(_ context.Context, _ methods.WizardNextRequest) (map[string]any, error) { return ok, nil },
		WizardCancel:    func(_ context.Context, _ methods.WizardCancelRequest) (map[string]any, error) { return ok, nil },
		WizardStatus:    func(_ context.Context, _ methods.WizardStatusRequest) (map[string]any, error) { return ok, nil },
		UpdateRun:       func(_ context.Context, _ methods.UpdateRunRequest) (map[string]any, error) { return ok, nil },
		LastHeartbeat:   func(_ context.Context, _ methods.LastHeartbeatRequest) (map[string]any, error) { return ok, nil },
		SetHeartbeats:   func(_ context.Context, _ methods.SetHeartbeatsRequest) (map[string]any, error) { return ok, nil },
		Wake:            func(_ context.Context, _ methods.WakeRequest) (map[string]any, error) { return ok, nil },
		SystemPresence:  func(_ context.Context, _ methods.SystemPresenceRequest) ([]map[string]any, error) { return nil, nil },
		SystemEvent:     func(_ context.Context, _ methods.SystemEventRequest) (map[string]any, error) { return ok, nil },
		Send:            func(_ context.Context, _ methods.SendRequest) (map[string]any, error) { return ok, nil },
		SendPoll:        func(_ context.Context, _ methods.PollRequest) (map[string]any, error) { return ok, nil },
		UsageStatus:     func(_ context.Context) (map[string]any, error) { return ok, nil },
		AbortChat:       func(_ context.Context, _ string) (int, error) { return 1, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodSandboxRun, mustJSON(t, map[string]any{"cmd": []string{"echo", "hi"}})},
		{methods.MethodWizardStart, emptyParams()},
		{methods.MethodWizardNext, mustJSON(t, map[string]any{"id": "wiz-1"})},
		{methods.MethodWizardCancel, mustJSON(t, map[string]any{"id": "wiz-1"})},
		{methods.MethodWizardStatus, emptyParams()},
		{methods.MethodUpdateRun, emptyParams()},
		{methods.MethodLastHeartbeat, nilParams()},
		{methods.MethodSetHeartbeats, mustJSON(t, map[string]any{"enabled": false})},
		{methods.MethodWake, mustJSON(t, map[string]any{"text": "wake up"})},
		{methods.MethodSystemEvent, mustJSON(t, map[string]any{"text": "test event"})},
		{methods.MethodSend, mustJSON(t, map[string]any{"to": "0000000000000000000000000000000000000000000000000000000000000001", "message": "hi"})},
		{methods.MethodPoll, mustJSON(t, map[string]any{"to": "0000000000000000000000000000000000000000000000000000000000000001", "question": "A?", "options": []string{"a", "b"}})},
		{methods.MethodUsageStatus, emptyParams()},
		{methods.MethodChatAbort, mustJSON(t, map[string]any{"session_id": "s1"})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchSystem(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
		})
	}
}

func TestDispatchSystem_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchSystem(ctx, ServerOptions{}, "system.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_sessions.go ────────────────────────────────────────────────────

func TestDispatchSessions_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodChatSend, mustJSON(t, map[string]any{"to": "user1", "text": "hi"})},
		{methods.MethodChatHistory, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionGet, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsList, emptyParams()},
		{methods.MethodSessionsPreview, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsPatch, mustJSON(t, map[string]any{"session_id": "s1", "meta": map[string]any{"tag": "test"}})},
		{methods.MethodSessionsReset, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsDelete, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsCompact, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsPrune, mustJSON(t, map[string]any{"all": true})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchSessions(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchSessions_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	ok := map[string]any{"ok": true}
	session := state.SessionDoc{SessionID: "s1", Meta: map[string]any{}}

	opts := ServerOptions{
		SendDM:         func(_ context.Context, _, _ string) error { return nil },
		GetSession:     func(_ context.Context, _ string) (state.SessionDoc, error) { return session, nil },
		PutSession:     func(_ context.Context, _ string, _ state.SessionDoc) error { return nil },
		ListSessions:   func(_ context.Context, _ int) ([]state.SessionDoc, error) { return nil, nil },
		ListTranscript: func(_ context.Context, _ string, _ int) ([]state.TranscriptEntryDoc, error) { return nil, nil },
		SessionsPrune:  func(_ context.Context, _ methods.SessionsPruneRequest) (map[string]any, error) { return ok, nil },
	}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodChatSend, mustJSON(t, map[string]any{"to": "user1", "text": "hi"})},
		{methods.MethodChatHistory, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionGet, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsList, emptyParams()},
		{methods.MethodSessionsPreview, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsPatch, mustJSON(t, map[string]any{"session_id": "s1", "meta": map[string]any{"tag": "test"}})},
		{methods.MethodSessionsReset, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsDelete, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsCompact, mustJSON(t, map[string]any{"session_id": "s1"})},
		{methods.MethodSessionsPrune, mustJSON(t, map[string]any{"all": true})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchSessions(ctx, opts, tc.method, call, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status=%d, want %d", status, http.StatusOK)
			}
		})
	}
}

func TestDispatchSessions_NotFound(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	transcript := func(_ context.Context, _ string, _ int) ([]state.TranscriptEntryDoc, error) { return nil, nil }

	cases := []struct {
		name   string
		method string
		params json.RawMessage
		opts   ServerOptions
	}{
		{
			"ChatHistory",
			methods.MethodChatHistory,
			mustJSON(t, map[string]any{"session_id": "s1"}),
			ServerOptions{
				GetSession:     func(_ context.Context, _ string) (state.SessionDoc, error) { return state.SessionDoc{}, state.ErrNotFound },
				ListTranscript: transcript,
			},
		},
		{
			"SessionGet",
			methods.MethodSessionGet,
			mustJSON(t, map[string]any{"session_id": "s1"}),
			ServerOptions{
				GetSession:     func(_ context.Context, _ string) (state.SessionDoc, error) { return state.SessionDoc{}, state.ErrNotFound },
				ListTranscript: transcript,
			},
		},
		{
			"SessionsPatch",
			methods.MethodSessionsPatch,
			mustJSON(t, map[string]any{"session_id": "s1", "meta": map[string]any{}}),
			ServerOptions{
				GetSession: func(_ context.Context, _ string) (state.SessionDoc, error) { return state.SessionDoc{}, state.ErrNotFound },
				PutSession: func(_ context.Context, _ string, _ state.SessionDoc) error { return nil },
			},
		},
		{
			"SessionsDelete",
			methods.MethodSessionsDelete,
			mustJSON(t, map[string]any{"session_id": "s1"}),
			ServerOptions{
				GetSession: func(_ context.Context, _ string) (state.SessionDoc, error) { return state.SessionDoc{}, state.ErrNotFound },
				PutSession: func(_ context.Context, _ string, _ state.SessionDoc) error { return nil },
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, _ := dispatchSessions(ctx, tc.opts, tc.method, call, cfg)
			if status != http.StatusNotFound {
				t.Fatalf("status=%d, want %d", status, http.StatusNotFound)
			}
		})
	}
}

func TestDispatchSessions_PreviewBulk(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	session := state.SessionDoc{SessionID: "s1", Meta: map[string]any{}}

	opts := ServerOptions{
		GetSession: func(_ context.Context, id string) (state.SessionDoc, error) {
			if id == "missing" {
				return state.SessionDoc{}, state.ErrNotFound
			}
			return session, nil
		},
		ListTranscript: func(_ context.Context, _ string, _ int) ([]state.TranscriptEntryDoc, error) { return nil, nil },
	}

	call := methods.CallRequest{
		Method: methods.MethodSessionsPreview,
		Params: mustJSON(t, map[string]any{"keys": []string{"s1", "missing"}}),
	}
	result, status, err := dispatchSessions(ctx, opts, methods.MethodSessionsPreview, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d, want %d", status, http.StatusOK)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result)
	}
	previews, ok := m["previews"].([]map[string]any)
	if !ok || len(previews) != 2 {
		t.Fatalf("expected 2 previews, got %v", m["previews"])
	}
	if previews[1]["status"] != "missing" {
		t.Errorf("missing session status=%v, want missing", previews[1]["status"])
	}
}

func TestDispatchSessions_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchSessions(ctx, ServerOptions{}, "sessions.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_config.go (nil-provider paths) ─────────────────────────────────

func TestDispatchConfig_NilProvider(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	cases := []struct {
		method string
		params json.RawMessage
	}{
		{methods.MethodConfigGet, emptyParams()},
		{methods.MethodListGet, mustJSON(t, map[string]any{"name": "users"})},
		{methods.MethodListPut, mustJSON(t, map[string]any{"name": "users", "items": []string{"a"}})},
		{methods.MethodConfigPut, mustJSON(t, map[string]any{"config": map[string]any{}})},
		{methods.MethodConfigSet, mustJSON(t, map[string]any{"key": "dm.policy", "value": "open"})},
		{methods.MethodConfigApply, mustJSON(t, map[string]any{"config": map[string]any{}})},
		{methods.MethodConfigPatch, mustJSON(t, map[string]any{"patch": map[string]any{"dm": map[string]any{"policy": "open"}}})},
	}

	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, err := dispatchConfig(ctx, opts, tc.method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil provider")
			}
		})
	}
}

func TestDispatchConfig_ConfigGet_Success(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{
		GetConfig: func(_ context.Context) (state.ConfigDoc, error) {
			return state.ConfigDoc{}, nil
		},
	}

	call := methods.CallRequest{Method: methods.MethodConfigGet, Params: emptyParams()}
	result, status, err := dispatchConfig(ctx, opts, methods.MethodConfigGet, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	if _, exists := m["config"]; !exists {
		t.Error("missing config in response")
	}
}

func TestDispatchConfig_ConfigSchema_Default(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	// Without GetConfig, returns default schema
	call := methods.CallRequest{Method: methods.MethodConfigSchema, Params: emptyParams()}
	result, status, err := dispatchConfig(ctx, opts, methods.MethodConfigSchema, call, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestDispatchConfig_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchConfig(ctx, ServerOptions{}, "config.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_acp.go / dispatch_tasks.go (delegate-only dispatchers) ─────────

func TestDispatchACP_NilDelegate(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	methods_list := []string{
		methods.MethodACPRegister,
		methods.MethodACPUnregister,
		methods.MethodACPPeers,
		methods.MethodACPDispatch,
		methods.MethodACPPipeline,
	}

	for _, method := range methods_list {
		t.Run(method, func(t *testing.T) {
			call := methods.CallRequest{Method: method, Params: emptyParams()}
			_, status, err := dispatchACP(ctx, opts, method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil delegate")
			}
		})
	}
}

func TestDispatchACP_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchACP(ctx, ServerOptions{}, "acp.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

func TestDispatchTasks_NilDelegate(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}
	opts := ServerOptions{}

	methods_list := []string{
		methods.MethodTasksCreate,
		methods.MethodTasksGet,
		methods.MethodTasksList,
		methods.MethodTasksCancel,
		methods.MethodTasksResume,
		methods.MethodTasksDoctor,
		methods.MethodTasksSummary,
		methods.MethodTasksAuditExport,
		methods.MethodTasksTrace,
	}

	for _, method := range methods_list {
		t.Run(method, func(t *testing.T) {
			call := methods.CallRequest{Method: method, Params: emptyParams()}
			_, status, err := dispatchTasks(ctx, opts, method, call, cfg)
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
			}
			if err == nil {
				t.Fatal("expected error for nil delegate")
			}
		})
	}
}

func TestDispatchTasks_UnknownMethod(t *testing.T) {
	ctx := context.Background()
	_, status, err := dispatchTasks(ctx, ServerOptions{}, "tasks.unknown", methods.CallRequest{}, state.ConfigDoc{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d", status, http.StatusInternalServerError)
	}
	if err == nil {
		t.Fatal("expected routing error")
	}
}

// ─── dispatch_cron.go (NotFound paths) ���──────────────────────────────────────

func TestDispatchCron_NotFound(t *testing.T) {
	ctx := context.Background()
	cfg := state.ConfigDoc{}

	cases := []struct {
		name   string
		method string
		params json.RawMessage
		opts   ServerOptions
	}{
		{
			"CronStatus",
			methods.MethodCronStatus,
			mustJSON(t, map[string]any{"id": "cron-1"}),
			ServerOptions{CronStatus: func(_ context.Context, _ methods.CronStatusRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"CronUpdate",
			methods.MethodCronUpdate,
			mustJSON(t, map[string]any{"id": "cron-1", "schedule": "0 * * * *"}),
			ServerOptions{CronUpdate: func(_ context.Context, _ methods.CronUpdateRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"CronRemove",
			methods.MethodCronRemove,
			mustJSON(t, map[string]any{"id": "cron-1"}),
			ServerOptions{CronRemove: func(_ context.Context, _ methods.CronRemoveRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"CronRun",
			methods.MethodCronRun,
			mustJSON(t, map[string]any{"id": "cron-1"}),
			ServerOptions{CronRun: func(_ context.Context, _ methods.CronRunRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
		{
			"CronRuns",
			methods.MethodCronRuns,
			mustJSON(t, map[string]any{"id": "cron-1"}),
			ServerOptions{CronRuns: func(_ context.Context, _ methods.CronRunsRequest) (map[string]any, error) { return nil, state.ErrNotFound }},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call := methods.CallRequest{Method: tc.method, Params: tc.params}
			_, status, _ := dispatchCron(ctx, tc.opts, tc.method, call, cfg)
			if status != http.StatusNotFound {
				t.Fatalf("status=%d, want %d", status, http.StatusNotFound)
			}
		})
	}
}

// ─── delegateControlCall helper ──────────────────────────────────────────────

func TestDelegateControlCall_NilDelegate(t *testing.T) {
	ctx := context.Background()
	opts := ServerOptions{}
	_, status, err := delegateControlCall(ctx, opts, "some.method", nil, "not configured")
	if status != http.StatusNotImplemented {
		t.Fatalf("status=%d, want %d", status, http.StatusNotImplemented)
	}
	if err == nil || err.Error() != "not configured" {
		t.Fatalf("err=%v, want 'not configured'", err)
	}
}

func TestDelegateControlCall_Success(t *testing.T) {
	ctx := context.Background()
	opts := ServerOptions{
		DelegateControlCall: func(_ context.Context, method string, _ json.RawMessage) (any, int, error) {
			return map[string]any{"method": method}, http.StatusOK, nil
		},
	}
	result, status, err := delegateControlCall(ctx, opts, "test.method", nil, "not configured")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d, want %d", status, http.StatusOK)
	}
	m, ok := result.(map[string]any)
	if !ok || m["method"] != "test.method" {
		t.Fatalf("unexpected result: %v", result)
	}
}

// ─── canonicalMethodName helper ──────────────────────────────────────────────

func TestCanonicalMethodName(t *testing.T) {
	// Alias should map to canonical
	if got := canonicalMethodName(methods.MethodStatusAlias); got != methods.MethodStatus {
		t.Errorf("alias: got %q, want %q", got, methods.MethodStatus)
	}
	// Non-alias should pass through
	if got := canonicalMethodName("config.get"); got != "config.get" {
		t.Errorf("passthrough: got %q", got)
	}
}

// ─── mcp_loopback.go helpers ─────────────────────────────────────────────────

func TestNormalizeMCPToolCallContent(t *testing.T) {
	// Nil result
	blocks := normalizeMCPToolCallContent(nil)
	if len(blocks) != 1 || blocks[0].Text != "" {
		t.Fatalf("nil: expected 1 empty block, got %v", blocks)
	}

	// Empty content
	blocks = normalizeMCPToolCallContent(&gomcp.CallToolResult{})
	if len(blocks) != 1 || blocks[0].Text != "" {
		t.Fatalf("empty: expected 1 empty block, got %v", blocks)
	}

	// Text content
	blocks = normalizeMCPToolCallContent(&gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: "hello"},
		},
	})
	if len(blocks) != 1 || blocks[0].Text != "hello" {
		t.Fatalf("text: expected 'hello', got %v", blocks)
	}

	// Image content
	blocks = normalizeMCPToolCallContent(&gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.ImageContent{Data: []byte("data"), MIMEType: "image/png"},
		},
	})
	if len(blocks) != 1 || blocks[0].Text != "[Image: image/png]" {
		t.Fatalf("image: expected '[Image: image/png]', got %v", blocks)
	}
}

func TestBuildMCPToolInputSchema(t *testing.T) {
	// Nil/empty schema
	tool := &gomcp.Tool{}
	schema := buildMCPToolInputSchema(tool)
	if schema["type"] != "object" {
		t.Errorf("nil schema: type=%v, want object", schema["type"])
	}
	if _, ok := schema["properties"]; !ok {
		t.Error("nil schema: missing properties")
	}

	// With proper object schema
	tool = &gomcp.Tool{
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
		},
	}
	schema = buildMCPToolInputSchema(tool)
	if schema["type"] != "object" {
		t.Errorf("object schema: type=%v, want object", schema["type"])
	}

	// Non-object type gets corrected
	tool = &gomcp.Tool{
		InputSchema: map[string]any{
			"type": "string",
		},
	}
	schema = buildMCPToolInputSchema(tool)
	if schema["type"] != "object" {
		t.Errorf("non-object schema: type=%v, want object", schema["type"])
	}
}

func TestFlattenUnionSchema(t *testing.T) {
	// No variants
	raw := map[string]any{"type": "object"}
	result := flattenUnionSchema(raw)
	if result["type"] != "object" {
		t.Errorf("no variants: type=%v", result["type"])
	}

	// With anyOf
	raw = map[string]any{
		"anyOf": []any{
			map[string]any{
				"type":       "object",
				"properties": map[string]any{"a": map[string]any{"type": "string"}},
				"required":   []any{"a"},
			},
			map[string]any{
				"type":       "object",
				"properties": map[string]any{"b": map[string]any{"type": "number"}},
			},
		},
	}
	result = flattenUnionSchema(raw)
	props, ok := result["properties"].(map[string]any)
	if !ok {
		t.Fatalf("anyOf: expected properties map, got %T", result["properties"])
	}
	if _, hasA := props["a"]; !hasA {
		t.Error("anyOf: missing property 'a'")
	}
	if _, hasB := props["b"]; !hasB {
		t.Error("anyOf: missing property 'b'")
	}
}
