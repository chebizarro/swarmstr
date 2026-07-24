package methods

import (
	"encoding/json"
	"testing"
)

func TestDecodeTerminalOpenParamsCamelAndSnake(t *testing.T) {
	// camelCase agentId is normalized to agent_id via the alias table.
	req, err := DecodeTerminalOpenParams(json.RawMessage(`{"agentId":"main","cols":80,"rows":24}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.AgentID != "main" || req.Cols != 80 || req.Rows != 24 {
		t.Fatalf("unexpected: %+v", req)
	}
}

func TestTerminalOpenRejectsBadDimensions(t *testing.T) {
	req, _ := DecodeTerminalOpenParams(json.RawMessage(`{"cols":0,"rows":24}`))
	if _, err := req.Normalize(); err == nil {
		t.Fatal("expected dimension error")
	}
	req, _ = DecodeTerminalOpenParams(json.RawMessage(`{"cols":80,"rows":9000}`))
	if _, err := req.Normalize(); err == nil {
		t.Fatal("expected out-of-range rows error")
	}
}

func TestTerminalInputRequiresSession(t *testing.T) {
	req, _ := DecodeTerminalInputParams(json.RawMessage(`{"data":"x"}`))
	if _, err := req.Normalize(); err == nil {
		t.Fatal("expected missing sessionId error")
	}
	req, err := DecodeTerminalInputParams(json.RawMessage(`{"sessionId":"term-1","data":"ls\n"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.SessionID != "term-1" || req.Data != "ls\n" {
		t.Fatalf("unexpected: %+v err=%v", req, err)
	}
}

func TestWorktreesCreateRequiresRepoRoot(t *testing.T) {
	req, _ := DecodeWorktreesCreateParams(json.RawMessage(`{"name":"x"}`))
	if _, err := req.Normalize(); err == nil {
		t.Fatal("expected repoRoot error")
	}
	req, err := DecodeWorktreesCreateParams(json.RawMessage(`{"repoRoot":"/tmp/r","name":"feat","baseRef":"main"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.RepoRoot != "/tmp/r" || req.Name != "feat" || req.BaseRef != "main" {
		t.Fatalf("unexpected: %+v err=%v", req, err)
	}
}

func TestFSListDirDecode(t *testing.T) {
	req, err := DecodeFSListDirParams(json.RawMessage(`{"path":"src","agentId":"main"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.Path != "src" || req.AgentID != "main" {
		t.Fatalf("unexpected: %+v err=%v", req, err)
	}
}

func TestWorkspaceSurfaceMethodsRegistered(t *testing.T) {
	supported := map[string]struct{}{}
	for _, m := range SupportedMethods() {
		supported[m] = struct{}{}
	}
	for _, m := range []string{
		MethodTerminalOpen, MethodTerminalInput, MethodTerminalResize, MethodTerminalClose,
		MethodFSListDir,
		MethodWorktreesList, MethodWorktreesBranches, MethodWorktreesCreate,
		MethodWorktreesRemove, MethodWorktreesRestore, MethodWorktreesGc,
	} {
		if _, ok := supported[m]; !ok {
			t.Fatalf("method not registered as supported: %s", m)
		}
	}
	// Terminal methods must remain operator.admin.
	for _, m := range []string{MethodTerminalOpen, MethodTerminalInput, MethodTerminalResize, MethodTerminalClose, MethodFSListDir} {
		if d := MethodDescriptor(m); d.Scope != "operator.admin" {
			t.Fatalf("%s expected operator.admin, got %s", m, d.Scope)
		}
	}
}
