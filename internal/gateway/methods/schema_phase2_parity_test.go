package methods

import (
	"encoding/json"
	"testing"

	"metiq/internal/gateway/protocol"
)

func TestPhase2PriorityMethodCatalogAndScopes(t *testing.T) {
	want := map[string]string{
		MethodSessionsGet:              protocol.MethodScopeOperatorRead,
		MethodSessionsResolve:          protocol.MethodScopeOperatorRead,
		MethodSessionsRecover:          protocol.MethodScopeOperatorWrite,
		MethodSessionsSteer:            protocol.MethodScopeOperatorWrite,
		MethodSessionsViewersSet:       protocol.MethodScopeOperatorRead,
		MethodSessionsAssignOwner:      protocol.MethodScopeOperatorWrite,
		MethodSessionsGroupsDefaults:   protocol.MethodScopeOperatorWrite,
		MethodSessionsGroupsUpdate:     protocol.MethodScopeOperatorWrite,
		MethodTasksRetry:               protocol.MethodScopeOperatorWrite,
		MethodTasksDismiss:             protocol.MethodScopeOperatorWrite,
		MethodExecApprovalGrantsList:   protocol.MethodScopeOperatorApprovals,
		MethodExecApprovalGrantsRevoke: protocol.MethodScopeOperatorApprovals,
	}
	catalog := map[string]bool{}
	for _, method := range SupportedMethods() {
		catalog[method] = true
	}
	for method, scope := range want {
		if !catalog[method] {
			t.Errorf("priority catalog missing %q", method)
		}
		if got := MethodDescriptor(method).Scope; got != scope {
			t.Errorf("%s scope=%q want %q", method, got, scope)
		}
	}
	unadvertised := map[string]string{
		MethodDevicePairSetupCode:        protocol.MethodScopeOperatorAdmin,
		MethodDevicePairSetupStatus:      protocol.MethodScopeOperatorAdmin,
		MethodDeviceScopesRequestUpgrade: protocol.MethodScopeOperatorRead,
		MethodDeviceScopesWaitUpgrade:    protocol.MethodScopeOperatorRead,
		MethodNodeRunnerInventoryUpdate:  protocol.MethodScopeNode,
	}
	for method, scope := range unadvertised {
		if catalog[method] {
			t.Errorf("internal or unavailable method must not be advertised: %q", method)
		}
		if got := MethodDescriptor(method).Scope; got != scope {
			t.Errorf("%s internal scope=%q want %q", method, got, scope)
		}
	}
}

func TestPhase2PriorityRequestContracts(t *testing.T) {
	viewers, err := DecodeSessionsViewersSetParams(json.RawMessage(`{"agentId":"main","sessionKeys":["a","a","b"]}`))
	if err != nil {
		t.Fatal(err)
	}
	viewers, err = viewers.Normalize()
	if err != nil || len(viewers.SessionKeys) != 2 || viewers.AgentID != "main" {
		t.Fatalf("viewers=%+v err=%v", viewers, err)
	}
	recovery, err := DecodeTasksRecoveryParams(json.RawMessage(`{"taskIds":["one","two"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recovery.Normalize(); err != nil {
		t.Fatal(err)
	}
	setup, err := DecodeDevicePairSetupCodeParams(json.RawMessage(`{"publicUrl":"https://gateway.example","preferRemoteUrl":true,"includeQr":true,"bootstrapProfile":"node"}`))
	if err != nil {
		t.Fatal(err)
	}
	if setup, err = setup.Normalize(); err != nil || setup.BootstrapProfile != "node" {
		t.Fatalf("setup=%+v err=%v", setup, err)
	}
	status, err := DecodeDevicePairSetupStatusParams(json.RawMessage(`{"setupId":"setup-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if status, err = status.Normalize(); err != nil || status.SetupID != "setup-1" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	grant, err := DecodeExecApprovalGrantsRevokeParams(json.RawMessage(`{"grantId":"grant-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if grant, err = grant.Normalize(); err != nil || grant.GrantID != "grant-1" {
		t.Fatalf("grant=%+v err=%v", grant, err)
	}
	inventory, err := DecodeNodeRunnerInventoryUpdateParams(json.RawMessage(`{"protocolFeatures":["node-worker-supervisor-v6"],"workerHost":{"enabled":true,"capacity":{"total":2,"available":1}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inventory.Normalize(); err != nil {
		t.Fatal(err)
	}
	recoverReq, err := DecodeSessionsRecoverParams(json.RawMessage(`{"key":"s","agentId":"main"}`))
	if err != nil || recoverReq.AgentID != "main" {
		t.Fatalf("recover=%+v err=%v", recoverReq, err)
	}
	if _, err := DecodeSessionsRecoverParams(json.RawMessage(`{"key":"s","unexpected":true}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
}
