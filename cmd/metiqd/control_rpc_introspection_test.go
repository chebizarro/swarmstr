package main

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func introspectionCall(t *testing.T, h controlRPCHandler, method, params string) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handleIntrospectionRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("method %s was not handled by introspection dispatch", method)
	}
	return result, err
}

func TestSystemInfoReportsIdentityAndCapabilities(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{startedAt: time.Now().Add(-5 * time.Second)})
	res, err := introspectionCall(t, h, methods.MethodSystemInfo, `{}`)
	if err != nil {
		t.Fatalf("system.info: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["version"] != daemonVersionString() {
		t.Fatalf("version = %v", p["version"])
	}
	if p["uptime_seconds"].(int) < 4 {
		t.Fatalf("expected uptime >= 4s, got %v", p["uptime_seconds"])
	}
	platform := p["platform"].(map[string]any)
	if platform["os"] != runtime.GOOS || platform["arch"] != runtime.GOARCH {
		t.Fatalf("platform = %+v", platform)
	}
	caps := p["capabilities"].(map[string]any)
	if caps["permissions"] != false {
		t.Fatalf("expected permissions capability false with no engine, got %+v", caps)
	}
}

func TestDiagnosticsStabilityReportsRuntime(t *testing.T) {
	jobs := newAgentJobRegistry()
	jobs.Begin("run-1", "sess-1")
	restartCh := make(chan int, 1)
	h := newControlRPCHandler(controlRPCDeps{startedAt: time.Now(), agentJobs: jobs, restartCh: restartCh})
	res, err := introspectionCall(t, h, methods.MethodDiagnosticsStability, `{}`)
	if err != nil {
		t.Fatalf("diagnostics.stability: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["goroutines"].(int) <= 0 {
		t.Fatalf("expected goroutines > 0, got %v", p["goroutines"])
	}
	if p["restart_scheduler_ready"] != true {
		t.Fatalf("expected restart scheduler ready")
	}
	if p["active_runs"].(int) != 1 {
		t.Fatalf("expected active_runs 1, got %v", p["active_runs"])
	}
	if _, ok := p["memory"].(map[string]any); !ok {
		t.Fatalf("expected memory snapshot, got %+v", p)
	}
}

func TestCommandsListReturnsCatalogAndFilters(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	res, err := introspectionCall(t, h, methods.MethodCommandsList, `{}`)
	if err != nil {
		t.Fatalf("commands.list: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["count"].(int) <= 0 {
		t.Fatalf("expected commands, got %+v", p)
	}
	// Unknown source filters everything out.
	res, err = introspectionCall(t, h, methods.MethodCommandsList, `{"source":"does-not-exist"}`)
	if err != nil {
		t.Fatalf("commands.list filtered: %v", err)
	}
	if res.Result.(map[string]any)["count"].(int) != 0 {
		t.Fatalf("expected 0 commands for unknown source")
	}
}

func TestUpdateStatusWithoutCheckerIsHonest(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	res, err := introspectionCall(t, h, methods.MethodUpdateStatus, `{}`)
	if err != nil {
		t.Fatalf("update.status: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["update_available"] != false || p["checked"] != false {
		t.Fatalf("expected honest no-update status, got %+v", p)
	}
	if p["current_version"] != daemonVersionString() {
		t.Fatalf("current_version = %v", p["current_version"])
	}
	if _, ok := p["note"]; !ok {
		t.Fatalf("expected a note explaining the missing checker")
	}
}

func TestToolsInvokeExecutesAndRejectsUnknown(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterTool("echo", agent.ToolRegistration{
		Func: func(_ context.Context, args map[string]any) (string, error) {
			if m, ok := args["msg"].(string); ok {
				return m, nil
			}
			return "", nil
		},
		Descriptor: agent.ToolDescriptor{Name: "echo", Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindBuiltin}},
	})
	h := newControlRPCHandler(controlRPCDeps{tools: reg})

	res, err := introspectionCall(t, h, methods.MethodToolsInvoke, `{"tool":"echo","args":{"msg":"hi"}}`)
	if err != nil {
		t.Fatalf("tools.invoke: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["ok"] != true || p["result"] != "hi" {
		t.Fatalf("unexpected invoke result: %+v", p)
	}

	if _, err := introspectionCall(t, h, methods.MethodToolsInvoke, `{"tool":"nope"}`); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestAuditListWithoutEngineIsHonest(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	res, err := introspectionCall(t, h, methods.MethodAuditList, `{}`)
	if err != nil {
		t.Fatalf("audit.list: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["available"] != false || p["count"].(int) != 0 {
		t.Fatalf("expected honest empty audit list, got %+v", p)
	}
	// activity feed degrades the same way.
	res, err = introspectionCall(t, h, methods.MethodAuditActivityList, `{}`)
	if err != nil {
		t.Fatalf("audit.activity.list: %v", err)
	}
	if res.Result.(map[string]any)["available"] != false {
		t.Fatalf("expected honest empty activity feed")
	}
}

func TestAgentsWorkspaceListAndGet(t *testing.T) {
	ctx := context.Background()
	docs := state.NewDocsRepository(newTestStore(), "author")
	if _, err := docs.PutAgent(ctx, "a1", state.AgentDoc{Version: 1, AgentID: "a1", Name: "One", Workspace: "w1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.PutAgent(ctx, "a2", state.AgentDoc{Version: 1, AgentID: "a2", Name: "Two", Workspace: "w2"}); err != nil {
		t.Fatal(err)
	}
	h := newControlRPCHandler(controlRPCDeps{docsRepo: docs})

	res, err := introspectionCall(t, h, methods.MethodAgentsWorkspaceList, `{"workspace":"w1"}`)
	if err != nil {
		t.Fatalf("agents.workspace.list: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["count"].(int) != 1 {
		t.Fatalf("expected 1 agent in w1, got %+v", p)
	}

	res, err = introspectionCall(t, h, methods.MethodAgentsWorkspaceGet, `{"agent_id":"a2"}`)
	if err != nil {
		t.Fatalf("agents.workspace.get: %v", err)
	}
	agentDetail := res.Result.(map[string]any)["agent"].(map[string]any)
	if agentDetail["workspace"] != "w2" || agentDetail["name"] != "Two" {
		t.Fatalf("unexpected agent detail: %+v", agentDetail)
	}

	if _, err := introspectionCall(t, h, methods.MethodAgentsWorkspaceGet, `{"agent_id":"missing"}`); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestApprovalHistoryReturnsResolvedOnly(t *testing.T) {
	reg := newExecApprovalsRegistry()
	rec := reg.Request(methods.ExecApprovalRequestRequest{Command: "git status", TimeoutMS: 60_000})
	reg.Request(methods.ExecApprovalRequestRequest{Command: "still pending", TimeoutMS: 60_000})
	if _, err := reg.ResolveOwned(rec.ID, "exec", "approve", "test"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	h := newControlRPCHandler(controlRPCDeps{execApprovals: reg})
	res, err := introspectionCall(t, h, methods.MethodApprovalHistory, `{}`)
	if err != nil {
		t.Fatalf("approval.history: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["count"].(int) != 1 {
		t.Fatalf("expected 1 resolved approval, got %+v", p)
	}
	approvals := p["approvals"].([]map[string]any)
	if approvals[0]["id"] != rec.ID || approvals[0]["status"] != "resolved" {
		t.Fatalf("unexpected resolved record: %+v", approvals[0])
	}
}

func TestToolsEffectiveReturnsProfileFilteredSet(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{tools: agent.NewToolRegistry()})
	res, err := introspectionCall(t, h, methods.MethodToolsEffective, `{"profile":"full"}`)
	if err != nil {
		t.Fatalf("tools.effective: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["profile"] != "full" {
		t.Fatalf("expected full profile, got %v", p["profile"])
	}
	if p["count"].(int) <= 0 {
		t.Fatalf("expected a non-empty effective tool set, got %+v", p)
	}
	if p["permissionsEnabled"] != false {
		t.Fatalf("expected permissionsEnabled false with no engine")
	}
	// An unknown profile is rejected.
	if _, err := introspectionCall(t, h, methods.MethodToolsEffective, `{"profile":"not-a-profile"}`); err == nil {
		t.Fatal("expected error for unknown profile")
	}
}

func TestUICommandUnknownAndDispatch(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{
		configState: newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}}),
	})
	// Unknown command → error before any dispatch.
	if _, err := introspectionCall(t, h, methods.MethodUICommand, `{"command":"/bogus"}`); err == nil {
		t.Fatal("expected error for unknown ui command")
	}
	// /help resolves to commands.list, which needs no extra deps.
	res, err := introspectionCall(t, h, methods.MethodUICommand, `{"command":"/help"}`)
	if err != nil {
		t.Fatalf("ui.command /help: %v", err)
	}
	p := res.Result.(map[string]any)
	if p["method"] != methods.MethodCommandsList {
		t.Fatalf("expected /help → commands.list, got %+v", p)
	}
	inner, ok := p["result"].(map[string]any)
	if !ok || inner["count"].(int) <= 0 {
		t.Fatalf("expected dispatched commands.list result, got %+v", p["result"])
	}
}
