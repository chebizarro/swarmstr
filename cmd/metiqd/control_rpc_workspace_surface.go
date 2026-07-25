package main

import (
	"context"
	"fmt"
	"strings"

	"metiq/internal/gateway/methods"
	terminalpkg "metiq/internal/gateway/terminal"
	worktreespkg "metiq/internal/gateway/worktrees"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

// gatewayTerminalEmitter bridges terminal session output to the WS runtime's
// targeted per-connection delivery. Terminal streams are never broadcast: only
// the connection that opened the session receives its data/exit events.
type gatewayTerminalEmitter struct {
	rt *gatewayws.Runtime
}

func (e gatewayTerminalEmitter) EmitTo(connID, event string, payload any) bool {
	if e.rt == nil {
		return false
	}
	return e.rt.EmitToConnection(connID, event, payload)
}

// handleWorkspaceSurfaceRPC dispatches the WS-A/A7 workspace-interactive
// surfaces that were deferred from the initial WS-A pass: the terminal PTY
// family and the workspace-rooted fs.listDir folder picker. Terminal methods
// require a live WebSocket connection because output is streamed back to the
// owning connection as terminal.data/terminal.exit events.
func (h controlRPCHandler) handleWorkspaceSurfaceRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	switch method {
	case methods.MethodTerminalOpen:
		return h.handleTerminalOpen(ctx, in, cfg)
	case methods.MethodTerminalInput:
		return h.handleTerminalInput(ctx, in)
	case methods.MethodTerminalResize:
		return h.handleTerminalResize(ctx, in)
	case methods.MethodTerminalClose:
		return h.handleTerminalClose(ctx, in)
	case methods.MethodTerminalAttach:
		return h.handleTerminalAttach(ctx, in)
	case methods.MethodTerminalList:
		return h.handleTerminalList(ctx, in)
	case methods.MethodTerminalText:
		return h.handleTerminalText(ctx, in)
	case methods.MethodTerminalUpload:
		return h.handleTerminalUpload(ctx, in)
	case methods.MethodAttachGrant:
		return h.handleAttachGrant(ctx, in)
	case methods.MethodAttachRevoke:
		return h.handleAttachRevoke(ctx, in)
	case methods.MethodFSListDir:
		return h.handleFSListDir(ctx, in, cfg)
	case methods.MethodWorktreesList:
		return h.handleWorktreesList(ctx, in)
	case methods.MethodWorktreesBranches:
		return h.handleWorktreesBranches(ctx, in)
	case methods.MethodWorktreesCreate:
		return h.handleWorktreesCreate(ctx, in)
	case methods.MethodWorktreesRemove:
		return h.handleWorktreesRemove(ctx, in)
	case methods.MethodWorktreesRestore:
		return h.handleWorktreesRestore(ctx, in)
	case methods.MethodWorktreesGc:
		return h.handleWorktreesGc(ctx, in)
	case methods.MethodBoardGet:
		return h.handleBoardGet(ctx, in)
	case methods.MethodBoardUpdate:
		return h.handleBoardUpdate(ctx, in)
	case methods.MethodBoardWidgetPut:
		return h.handleBoardWidgetPut(ctx, in)
	case methods.MethodBoardWidgetGrant:
		return h.handleBoardWidgetGrant(ctx, in)
	case methods.MethodBoardEvent:
		return h.handleBoardEvent(ctx, in)
	case methods.MethodConversationsList:
		return h.handleConversationsList(ctx, in)
	case methods.MethodConversationsSend:
		return h.handleConversationsSend(ctx, in)
	case methods.MethodConversationsTurn:
		return h.handleConversationsTurn(ctx, in)
	case methods.MethodConversationsTurnCancel:
		return h.handleConversationsTurnCancel(ctx, in)
	case methods.MethodArtifactsList:
		return h.handleArtifactsList(ctx, in, cfg)
	case methods.MethodArtifactsGet:
		return h.handleArtifactsGet(ctx, in, cfg)
	case methods.MethodArtifactsDownload:
		return h.handleArtifactsDownload(ctx, in, cfg)
	case methods.MethodEnvironmentsList:
		return h.handleEnvironmentsList(ctx, in, cfg)
	case methods.MethodEnvironmentsStatus:
		return h.handleEnvironmentsStatus(ctx, in)
	case methods.MethodEnvironmentsCreate:
		return h.handleEnvironmentsCreate(ctx, in, cfg)
	case methods.MethodEnvironmentsDestroy:
		return h.handleEnvironmentsDestroy(ctx, in)
	case methods.MethodQuestionRequest:
		return h.handleQuestionRequest(ctx, in)
	case methods.MethodQuestionWaitAnswer:
		return h.handleQuestionWaitAnswer(ctx, in)
	case methods.MethodQuestionResolve:
		return h.handleQuestionResolve(ctx, in)
	case methods.MethodQuestionGet:
		return h.handleQuestionGet(ctx, in)
	case methods.MethodQuestionList:
		return h.handleQuestionList(ctx, in)
	case methods.MethodTaskSuggestionsList:
		return h.handleTaskSuggestionsList(ctx, in)
	case methods.MethodTaskSuggestionsCreate:
		return h.handleTaskSuggestionsCreate(ctx, in)
	case methods.MethodTaskSuggestionsAccept:
		return h.handleTaskSuggestionsAccept(ctx, in, cfg)
	case methods.MethodTaskSuggestionsDismiss:
		return h.handleTaskSuggestionsDismiss(ctx, in)
	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}

func terminalConnID(ctx context.Context) (string, error) {
	connID, ok := gatewayws.ConnectionIDFromContext(ctx)
	if !ok || strings.TrimSpace(connID) == "" {
		return "", fmt.Errorf("terminal requires an authenticated WebSocket connection")
	}
	return connID, nil
}

func (h controlRPCHandler) handleTerminalOpen(ctx context.Context, in nostruntime.ControlRPCInbound, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	if h.deps.terminalManager == nil {
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("terminal is not available")
	}
	req, err := methods.DecodeTerminalOpenParams(in.Params)
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
	cwd := workspace.ResolveWorkspaceDir(cfg, req.AgentID)
	result, err := h.deps.terminalManager.Open(terminalpkg.OpenRequest{
		ConnID:  connID,
		AgentID: req.AgentID,
		Cwd:     cwd,
		Cols:    req.Cols,
		Rows:    req.Rows,
	})
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"sessionId": result.SessionID,
		"agentId":   result.AgentID,
		"shell":     result.Shell,
		"cwd":       result.Cwd,
		"confined":  result.Confined,
	}}, true, nil
}

func (h controlRPCHandler) handleTerminalInput(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTerminalInputParams(in.Params)
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
	ok := false
	if h.deps.terminalManager != nil {
		ok = h.deps.terminalManager.Write(connID, req.SessionID, req.Data)
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"ok": ok}}, true, nil
}

func (h controlRPCHandler) handleTerminalResize(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTerminalResizeParams(in.Params)
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
	ok := false
	if h.deps.terminalManager != nil {
		ok = h.deps.terminalManager.Resize(connID, req.SessionID, req.Cols, req.Rows)
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"ok": ok}}, true, nil
}

func (h controlRPCHandler) handleTerminalClose(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeTerminalCloseParams(in.Params)
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
	ok := false
	if h.deps.terminalManager != nil {
		ok = h.deps.terminalManager.Close(connID, req.SessionID)
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"ok": ok}}, true, nil
}

func worktreeRecordMap(r worktreespkg.Record) map[string]any {
	m := map[string]any{
		"id":              r.ID,
		"name":            r.Name,
		"repoFingerprint": r.RepoFingerprint,
		"repoRoot":        r.RepoRoot,
		"path":            r.Path,
		"branch":          r.Branch,
		"baseRef":         r.BaseRef,
		"ownerKind":       r.OwnerKind,
		"createdAt":       r.CreatedAt,
		"lastActiveAt":    r.LastActiveAt,
	}
	if r.OwnerID != "" {
		m["ownerId"] = r.OwnerID
	}
	if r.SnapshotRef != "" {
		m["snapshotRef"] = r.SnapshotRef
	}
	if r.RemovedAt != 0 {
		m["removedAt"] = r.RemovedAt
	}
	return m
}

func (h controlRPCHandler) worktreeService() (*worktreespkg.Service, error) {
	if h.deps.worktrees == nil {
		return nil, fmt.Errorf("worktrees are not available")
	}
	return h.deps.worktrees, nil
}

func (h controlRPCHandler) handleWorktreesList(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	if _, err := methods.DecodeWorktreesListParams(in.Params); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	svc, err := h.worktreeService()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	records, err := svc.List(ctx)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	out := make([]map[string]any, 0, len(records))
	for _, r := range records {
		out = append(out, worktreeRecordMap(r))
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{"worktrees": out}}, true, nil
}

func (h controlRPCHandler) handleWorktreesBranches(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeWorktreesBranchesParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	svc, err := h.worktreeService()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	listing, err := svc.Branches(ctx, req.RepoRoot, req.IncludeRepositoryStatus)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	result := map[string]any{"branches": listing.Branches}
	if listing.DefaultBranch != "" {
		result["defaultBranch"] = listing.DefaultBranch
	}
	if listing.HeadBranch != "" {
		result["headBranch"] = listing.HeadBranch
	}
	if listing.RepositoryStatus != "" {
		result["repositoryStatus"] = listing.RepositoryStatus
	}
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}

func (h controlRPCHandler) handleWorktreesCreate(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeWorktreesCreateParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	svc, err := h.worktreeService()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	rec, err := svc.Create(ctx, worktreespkg.CreateParams{RepoRoot: req.RepoRoot, Name: req.Name, BaseRef: req.BaseRef})
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: worktreeRecordMap(rec)}, true, nil
}

func (h controlRPCHandler) handleWorktreesRemove(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeWorktreesRemoveParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	svc, err := h.worktreeService()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	res, err := svc.Remove(ctx, req.ID, req.Force)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	result := map[string]any{"removed": res.Removed}
	if res.SnapshotRef != "" {
		result["snapshotRef"] = res.SnapshotRef
	}
	if res.SnapshotError != "" {
		result["snapshotError"] = res.SnapshotError
	}
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}

func (h controlRPCHandler) handleWorktreesRestore(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeWorktreesRestoreParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	svc, err := h.worktreeService()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	rec, err := svc.Restore(ctx, req.ID)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: worktreeRecordMap(rec)}, true, nil
}

func (h controlRPCHandler) handleWorktreesGc(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, bool, error) {
	if _, err := methods.DecodeWorktreesGcParams(in.Params); err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	svc, err := h.worktreeService()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	res, err := svc.Gc(ctx)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	return nostruntime.ControlRPCResult{Result: map[string]any{
		"removed":         res.Removed,
		"orphansDeleted":  res.OrphansDeleted,
		"snapshotsPruned": res.SnapshotsPruned,
	}}, true, nil
}

func (h controlRPCHandler) handleFSListDir(ctx context.Context, in nostruntime.ControlRPCInbound, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	req, err := methods.DecodeFSListDirParams(in.Params)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	req, err = req.Normalize()
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	if req.NodeID != "" {
		// Metiq deviation: directory browsing is workspace-rooted and does not
		// proxy to connected nodes yet.
		return nostruntime.ControlRPCResult{}, true, fmt.Errorf("node directory browsing is not supported")
	}
	listing, err := workspace.ListWorkspaceDirs(ctx, cfg, req.AgentID, req.Path)
	if err != nil {
		return nostruntime.ControlRPCResult{}, true, err
	}
	entries := make([]methods.FSDirEntry, 0, len(listing.Entries))
	for _, e := range listing.Entries {
		entries = append(entries, methods.FSDirEntry{Name: e.Name, Path: e.Path, Hidden: e.Hidden})
	}
	result := methods.FSListDirResult{
		Path:    listing.Path,
		Home:    "",
		Entries: entries,
	}
	if listing.Parent != nil {
		result.Parent = *listing.Parent
	}
	return nostruntime.ControlRPCResult{Result: result}, true, nil
}
