package main

import (
	"context"
	"fmt"
	"time"

	adminpkg "metiq/internal/admin"
	attachpkg "metiq/internal/gateway/attach"
	"metiq/internal/gateway/methods"
	terminalpkg "metiq/internal/gateway/terminal"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/workspace"
)

// Terminal helper (terminal.attach/list/text/upload) and attach.* grant RPC
// handlers (WS-A/A7 follow-up slice, swarmstr-fokh). The terminal helpers sit
// directly on the connection-owned PTY manager: attach is single-owner
// take-over with replay catch-up, list enumerates sessions with ownership
// metadata, text snapshots the ring buffer as plain text, and upload stages
// file bytes into the session cwd under os.Root containment (metiq deviation
// from OpenClaw's host temp-dir staging).

func (h controlRPCHandler) requireTerminalManager() (*terminalpkg.Manager, error) {
	if h.deps.terminalManager == nil {
		return nil, fmt.Errorf("terminal is not available")
	}
	return h.deps.terminalManager, nil
}

func (h controlRPCHandler) handleTerminalAttach(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTerminalAttachParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	connID, err := terminalConnID(ctx)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	manager, err := h.requireTerminalManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	attached, ok := manager.Attach(connID, req.SessionID)
	if !ok {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown terminal session %q", req.SessionID)
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"sessionId": attached.SessionID,
		"agentId":   attached.AgentID,
		"shell":     attached.Shell,
		"cwd":       attached.Cwd,
		"confined":  false,
		"buffer":    attached.Buffer,
		"seq":       attached.Seq,
	}}, true, nil
}

func (h controlRPCHandler) handleTerminalList(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	if _, err := methods.DecodeTerminalListParams(in.Params); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if _, err := terminalConnID(ctx); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	// An empty list (not an error) when the surface is off/unwired keeps the
	// reconnect flow simple: clients just fall back to opening fresh sessions.
	sessions := []terminalpkg.SessionInfo{}
	if h.deps.terminalManager != nil {
		sessions = h.deps.terminalManager.List()
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"sessions": sessions}}, true, nil
}

func (h controlRPCHandler) handleTerminalText(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTerminalTextParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if _, err := terminalConnID(ctx); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	manager, err := h.requireTerminalManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	raw, ok := manager.Snapshot(req.SessionID)
	if !ok {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown terminal session %q", req.SessionID)
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"text": terminalpkg.RenderText(raw)}}, true, nil
}

func (h controlRPCHandler) handleTerminalUpload(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTerminalUploadParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	content, err := req.Content()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	connID, err := terminalConnID(ctx)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	manager, err := h.requireTerminalManager()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	cwd, ok := manager.SessionCwd(connID, req.SessionID)
	if !ok {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("unknown terminal session %q", req.SessionID)
	}
	staged, err := workspace.StageTerminalUpload(cwd, req.Name, content)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"path": staged.Path,
		"size": staged.Size,
	}}, true, nil
}

func (h controlRPCHandler) attachGrantStore() (*attachpkg.Store, error) {
	if h.deps.attachGrants == nil {
		return nil, fmt.Errorf("attach grants are not available")
	}
	return h.deps.attachGrants, nil
}

func (h controlRPCHandler) handleAttachGrant(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeAttachGrantParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	store, err := h.attachGrantStore()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	// Mirrors OpenClaw: a grant is only useful against a live MCP loopback
	// surface, so refuse to mint one when no loopback runtime is active.
	runtime := adminpkg.GetActiveMCPLoopbackRuntime()
	if runtime == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp loopback server unavailable")
	}
	grant, err := store.Mint(req.SessionKey, time.Duration(req.TTLMs)*time.Millisecond)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"sessionKey":  grant.SessionKey,
		"token":       grant.Token,
		"expiresAtMs": grant.ExpiresAt.UnixMilli(),
		"mcpConfig":   adminpkg.MCPLoopbackServerConfig(runtime.Port),
		"env": map[string]string{
			"METIQ_MCP_TOKEN": grant.Token,
		},
	}}, true, nil
}

func (h controlRPCHandler) handleAttachRevoke(_ context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeAttachRevokeParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	store, err := h.attachGrantStore()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"revoked": store.Revoke(req.Token)}}, true, nil
}
