package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	adminpkg "metiq/internal/admin"
	attachpkg "metiq/internal/gateway/attach"
	"metiq/internal/gateway/methods"
	terminalpkg "metiq/internal/gateway/terminal"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

type recordingTerminalEmitter struct {
	mu     sync.Mutex
	events []struct {
		conn    string
		event   string
		payload any
	}
}

func (e *recordingTerminalEmitter) EmitTo(connID, event string, payload any) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, struct {
		conn    string
		event   string
		payload any
	}{connID, event, payload})
	return true
}

func (e *recordingTerminalEmitter) drain() []struct {
	conn    string
	event   string
	payload any
} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]struct {
		conn    string
		event   string
		payload any
	}{}, e.events...)
}

func terminalSurfaceCall(t *testing.T, h controlRPCHandler, connID, method, params string) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	ctx := context.Background()
	if connID != "" {
		ctx = gatewayws.ContextWithConnectionID(ctx, connID)
	}
	result, handled, err := h.handleWorkspaceSurfaceRPC(ctx, nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("method %s was not handled by workspace surface dispatch", method)
	}
	return result, err
}

func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestTerminalHelperRPCLifecycle(t *testing.T) {
	em := &recordingTerminalEmitter{}
	manager := terminalpkg.NewManager(terminalpkg.Options{Emitter: em})
	defer manager.Shutdown()
	h := newControlRPCHandler(controlRPCDeps{terminalManager: manager})

	cwd := t.TempDir()
	opened, err := manager.Open(terminalpkg.OpenRequest{ConnID: "conn-a", AgentID: "agent-1", Shell: "/bin/sh", Cwd: cwd, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !manager.Write("conn-a", opened.SessionID, "echo helper-marker\n") {
		t.Fatal("seed write failed")
	}
	waitForCondition(t, func() bool {
		snap, ok := manager.Snapshot(opened.SessionID)
		return ok && strings.Contains(snap, "helper-marker")
	})

	// terminal.list reports the session with ownership metadata.
	result, err := terminalSurfaceCall(t, h, "conn-b", methods.MethodTerminalList, `{}`)
	if err != nil {
		t.Fatalf("terminal.list: %v", err)
	}
	sessions, ok := result.Result.(map[string]any)["sessions"].([]terminalpkg.SessionInfo)
	if !ok || len(sessions) != 1 || sessions[0].SessionID != opened.SessionID || sessions[0].Owner != "conn" {
		t.Fatalf("unexpected list result: %#v", result.Result)
	}

	// terminal.list without a WS connection is refused.
	if _, err := terminalSurfaceCall(t, h, "", methods.MethodTerminalList, `{}`); err == nil {
		t.Fatal("terminal.list without connection succeeded")
	}

	// terminal.text snapshots the buffer as plain text without attaching.
	result, err = terminalSurfaceCall(t, h, "conn-b", methods.MethodTerminalText, fmt.Sprintf(`{"sessionId":%q}`, opened.SessionID))
	if err != nil {
		t.Fatalf("terminal.text: %v", err)
	}
	if text, _ := result.Result.(map[string]any)["text"].(string); !strings.Contains(text, "helper-marker") {
		t.Fatalf("unexpected text result: %#v", result.Result)
	}
	if _, err := terminalSurfaceCall(t, h, "conn-b", methods.MethodTerminalText, `{"sessionId":"nope"}`); err == nil {
		t.Fatal("terminal.text for unknown session succeeded")
	}

	// terminal.attach takes over ownership and replays the buffer.
	result, err = terminalSurfaceCall(t, h, "conn-b", methods.MethodTerminalAttach, fmt.Sprintf(`{"sessionId":%q}`, opened.SessionID))
	if err != nil {
		t.Fatalf("terminal.attach: %v", err)
	}
	attachResult := result.Result.(map[string]any)
	if attachResult["sessionId"] != opened.SessionID || attachResult["cwd"] == "" {
		t.Fatalf("unexpected attach result: %#v", attachResult)
	}
	if buffer, _ := attachResult["buffer"].(string); !strings.Contains(buffer, "helper-marker") {
		t.Fatalf("attach replay missing marker: %#v", attachResult)
	}
	if seq, _ := attachResult["seq"].(int64); seq <= 0 {
		t.Fatalf("attach seq not advanced: %#v", attachResult)
	}
	waitForCondition(t, func() bool {
		for _, ev := range em.drain() {
			if ev.conn != "conn-a" || ev.event != terminalpkg.EventExit {
				continue
			}
			if exit, ok := ev.payload.(terminalpkg.ExitEvent); ok && exit.Reason == terminalpkg.ExitReasonDetached {
				return true
			}
		}
		return false
	})
	if _, err := terminalSurfaceCall(t, h, "conn-b", methods.MethodTerminalAttach, `{"sessionId":"nope"}`); err == nil {
		t.Fatal("terminal.attach for unknown session succeeded")
	}

	// terminal.upload stages bytes into the session cwd; only the owner may.
	if _, err := terminalSurfaceCall(t, h, "conn-a", methods.MethodTerminalUpload,
		fmt.Sprintf(`{"sessionId":%q,"name":"notes.txt","contentBase64":"aGVsbG8="}`, opened.SessionID)); err == nil {
		t.Fatal("terminal.upload by detached previous owner succeeded")
	}
	result, err = terminalSurfaceCall(t, h, "conn-b", methods.MethodTerminalUpload,
		fmt.Sprintf(`{"sessionId":%q,"name":"notes.txt","contentBase64":"aGVsbG8="}`, opened.SessionID))
	if err != nil {
		t.Fatalf("terminal.upload: %v", err)
	}
	uploadResult := result.Result.(map[string]any)
	stagedPath, _ := uploadResult["path"].(string)
	if uploadResult["size"] != 5 || filepath.Base(stagedPath) != "notes.txt" {
		t.Fatalf("unexpected upload result: %#v", uploadResult)
	}
	data, err := os.ReadFile(stagedPath)
	if err != nil || string(data) != "hello" {
		t.Fatalf("staged file unreadable: %v %q", err, data)
	}
}

func TestTerminalHelperRPCWithoutManager(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	// list degrades to an empty result; the other helpers error.
	result, err := terminalSurfaceCall(t, h, "conn-a", methods.MethodTerminalList, `{}`)
	if err != nil {
		t.Fatalf("terminal.list: %v", err)
	}
	if sessions := result.Result.(map[string]any)["sessions"].([]terminalpkg.SessionInfo); len(sessions) != 0 {
		t.Fatalf("expected empty session list: %#v", sessions)
	}
	if _, err := terminalSurfaceCall(t, h, "conn-a", methods.MethodTerminalAttach, `{"sessionId":"x"}`); err == nil {
		t.Fatal("terminal.attach without manager succeeded")
	}
	if _, err := terminalSurfaceCall(t, h, "conn-a", methods.MethodTerminalText, `{"sessionId":"x"}`); err == nil {
		t.Fatal("terminal.text without manager succeeded")
	}
	if _, err := terminalSurfaceCall(t, h, "conn-a", methods.MethodTerminalUpload, `{"sessionId":"x","name":"a","contentBase64":""}`); err == nil {
		t.Fatal("terminal.upload without manager succeeded")
	}
}

func TestAttachGrantAndRevokeRPC(t *testing.T) {
	store := attachpkg.NewStore()
	h := newControlRPCHandler(controlRPCDeps{attachGrants: store})

	// Without an active MCP loopback runtime the grant is refused (OpenClaw
	// parity: a grant is only useful against a live loopback surface).
	if _, err := terminalSurfaceCall(t, h, "", methods.MethodAttachGrant, `{"sessionKey":"sess-1"}`); err == nil {
		t.Fatal("attach.grant without loopback runtime succeeded")
	}

	loopbackToken, err := adminpkg.GenerateMCPLoopbackToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	adminpkg.SetActiveMCPLoopbackRuntime(adminpkg.MCPLoopbackRuntime{Port: 18789, Token: loopbackToken})
	defer adminpkg.ClearActiveMCPLoopbackRuntime(loopbackToken)

	result, err := terminalSurfaceCall(t, h, "", methods.MethodAttachGrant, `{"sessionKey":"sess-1","ttlMs":60000}`)
	if err != nil {
		t.Fatalf("attach.grant: %v", err)
	}
	grantResult := result.Result.(map[string]any)
	token, _ := grantResult["token"].(string)
	if grantResult["sessionKey"] != "sess-1" || token == "" {
		t.Fatalf("unexpected grant result: %#v", grantResult)
	}
	if expires, _ := grantResult["expiresAtMs"].(int64); expires <= time.Now().UnixMilli() {
		t.Fatalf("grant already expired: %#v", grantResult)
	}
	if env, _ := grantResult["env"].(map[string]string); env["METIQ_MCP_TOKEN"] != token {
		t.Fatalf("env token mismatch: %#v", grantResult)
	}
	if grantResult["mcpConfig"] == nil {
		t.Fatalf("missing mcpConfig: %#v", grantResult)
	}
	if resolved, ok := store.Resolve(token); !ok || resolved.SessionKey != "sess-1" {
		t.Fatalf("grant not stored: %#v %v", resolved, ok)
	}

	if _, err := terminalSurfaceCall(t, h, "", methods.MethodAttachGrant, `{"sessionKey":""}`); err == nil {
		t.Fatal("attach.grant without sessionKey succeeded")
	}

	result, err = terminalSurfaceCall(t, h, "", methods.MethodAttachRevoke, fmt.Sprintf(`{"token":%q}`, token))
	if err != nil {
		t.Fatalf("attach.revoke: %v", err)
	}
	if result.Result.(map[string]any)["revoked"] != true {
		t.Fatalf("expected revoked=true: %#v", result.Result)
	}
	result, err = terminalSurfaceCall(t, h, "", methods.MethodAttachRevoke, fmt.Sprintf(`{"token":%q}`, token))
	if err != nil {
		t.Fatalf("attach.revoke repeat: %v", err)
	}
	if result.Result.(map[string]any)["revoked"] != false {
		t.Fatalf("expected revoked=false on second revoke: %#v", result.Result)
	}

	// Without a store both methods are unavailable.
	bare := newControlRPCHandler(controlRPCDeps{})
	if _, err := terminalSurfaceCall(t, bare, "", methods.MethodAttachGrant, `{"sessionKey":"sess-1"}`); err == nil {
		t.Fatal("attach.grant without store succeeded")
	}
	if _, err := terminalSurfaceCall(t, bare, "", methods.MethodAttachRevoke, `{"token":"tok"}`); err == nil {
		t.Fatal("attach.revoke without store succeeded")
	}
}
