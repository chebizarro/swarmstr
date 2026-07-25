package main

// control_rpc_chat_surface.go — control-RPC handlers for the chat control-UI
// surface (chat.startup / chat.metadata / chat.message.get / chat.toolTitles,
// swarmstr-viqq). Backed by the existing docs/transcript session subsystem
// (DocsRepository + TranscriptRepository), mirroring OpenClaw
// src/gateway/server-methods/chat*.ts contracts.

import (
	"context"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// chatStartupDefaultLimit bounds the bootstrap transcript window when the caller
// does not specify a limit.
const chatStartupDefaultLimit = 50

func (h controlRPCHandler) handleChatSurfaceRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	_ = cfg
	docsRepo := h.deps.docsRepo
	transcriptRepo := h.deps.transcriptRepo

	switch method {
	case methods.MethodChatStartup:
		req, err := methods.DecodeChatStartupParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		limit := req.Limit
		if limit == 0 {
			limit = chatStartupDefaultLimit
		}
		session, err := docsRepo.GetSession(ctx, req.Key)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		entries, err := transcriptRepo.ListSession(ctx, session.SessionID, limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result := methods.ApplyCompatResponseAliases(map[string]any{
			"ok":         true,
			"startup":    true,
			"session_id": session.SessionID,
			"session":    session,
			"entries":    entries,
		})
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	case methods.MethodChatMetadata:
		req, err := methods.DecodeChatMetadataParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		session, err := docsRepo.GetSession(ctx, req.Key)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		entries, err := transcriptRepo.ListSessionAll(ctx, session.SessionID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		userCount, assistantCount := 0, 0
		var lastUnix int64
		for _, e := range entries {
			switch e.Role {
			case "user":
				userCount++
			case "assistant":
				assistantCount++
			}
			if e.Unix > lastUnix {
				lastUnix = e.Unix
			}
		}
		metadata := map[string]any{
			"session_id":      session.SessionID,
			"peer_pubkey":     session.PeerPubKey,
			"last_inbound_at": session.LastInboundAt,
			"last_reply_at":   session.LastReplyAt,
			"message_count":   len(entries),
			"user_count":      userCount,
			"assistant_count": assistantCount,
			"last_message_at": lastUnix,
		}
		if session.Meta != nil {
			metadata["meta"] = session.Meta
		}
		result := methods.ApplyCompatResponseAliases(map[string]any{
			"ok":       true,
			"metadata": metadata,
		})
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	case methods.MethodChatMessageGet:
		req, err := methods.DecodeChatMessageGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		session, err := docsRepo.GetSession(ctx, req.Key)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		entries, err := transcriptRepo.ListSessionAll(ctx, session.SessionID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		for _, e := range entries {
			if e.EntryID != req.MessageID {
				continue
			}
			text := e.Text
			if req.MaxChars > 0 {
				text = truncateRunes(text, req.MaxChars)
			}
			message := map[string]any{
				"entry_id":        e.EntryID,
				"parent_entry_id": e.ParentEntryID,
				"session_id":      e.SessionID,
				"role":            e.Role,
				"text":            text,
				"unix":            e.Unix,
			}
			if e.Meta != nil {
				message["meta"] = e.Meta
			}
			result := methods.ApplyCompatResponseAliases(map[string]any{
				"ok":      true,
				"message": message,
			})
			return nostruntime.ControlRPCResult{Result: result}, true, nil
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": false, "unavailableReason": "not_found"}}, true, nil

	case methods.MethodChatToolTitles:
		req, err := methods.DecodeChatToolTitlesParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if req, err = req.Normalize(); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"titles": methods.BuildToolTitles(req.Items)}}, true, nil
	}
	return nostruntime.ControlRPCResult{}, false, nil
}
