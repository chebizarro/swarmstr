package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchSessions(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodChatSend:
		if opts.SendDM == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("send dm not configured")
		}
		req, err := methods.DecodeChatSendParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if err := opts.SendDM(ctx, req.To, req.Text); err != nil {
			log.Printf("admin method chat.send failed: %v", err)
			return nil, http.StatusBadGateway, fmt.Errorf("send failed")
		}
		result := map[string]any{"ok": true, "status": "sent"}
		if req.RunID != "" {
			result["run_id"] = req.RunID
		}
		return methods.ApplyCompatResponseAliases(result), http.StatusOK, nil
	case methods.MethodChatHistory:
		if opts.GetSession == nil || opts.ListTranscript == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("session providers not configured")
		}
		req, err := methods.DecodeChatHistoryParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if _, err := opts.GetSession(ctx, req.SessionID); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		transcript, err := opts.ListTranscript(ctx, req.SessionID, req.Limit)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(map[string]any{"session_id": req.SessionID, "key": req.SessionID, "entries": transcript}), http.StatusOK, nil
	case methods.MethodSessionGet:
		if opts.GetSession == nil || opts.ListTranscript == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("session providers not configured")
		}
		req, err := methods.DecodeSessionGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		session, err := opts.GetSession(ctx, req.SessionID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		transcript, err := opts.ListTranscript(ctx, req.SessionID, req.Limit)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.SessionGetResponse{Session: session, Transcript: transcript}, http.StatusOK, nil
	case methods.MethodSessionsList:
		if opts.ListSessions == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("sessions provider not configured")
		}
		req, err := methods.DecodeSessionsListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		cfg := state.ConfigDoc{}
		if opts.GetConfig != nil {
			cfg, _ = opts.GetConfig(ctx)
		}
		result, err := BuildSessionsListResponse(ctx, req, SessionsListResponseOptions{
			Config:         cfg,
			SessionStore:   opts.SessionStore,
			ListSessions:   opts.ListSessions,
			ListTranscript: opts.ListTranscript,
		})
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(result), http.StatusOK, nil
	case methods.MethodSessionsPreview:
		if opts.GetSession == nil || opts.ListTranscript == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("session providers not configured")
		}
		req, err := methods.DecodeSessionsPreviewParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if len(req.Keys) > 0 {
			previews := make([]map[string]any, 0, len(req.Keys))
			for _, key := range req.Keys {
				session, err := opts.GetSession(ctx, key)
				if err != nil {
					if errors.Is(err, state.ErrNotFound) {
						previews = append(previews, map[string]any{"key": key, "status": "missing", "items": []map[string]any{}})
						continue
					}
					log.Printf("sessions.preview: failed to get session %q: %v", key, err)
					previews = append(previews, map[string]any{"key": key, "status": "error", "items": []map[string]any{}})
					continue
				}
				transcript, err := opts.ListTranscript(ctx, session.SessionID, req.Limit)
				if err != nil {
					log.Printf("sessions.preview: failed to list transcript for session %q: %v", key, err)
					previews = append(previews, map[string]any{"key": key, "status": "error", "items": []map[string]any{}})
					continue
				}
				items := make([]map[string]any, 0, len(transcript))
				for _, entry := range transcript {
					items = append(items, map[string]any{"role": entry.Role, "text": entry.Text})
				}
				statusValue := "ok"
				if len(items) == 0 {
					statusValue = "empty"
				}
				previews = append(previews, map[string]any{"key": key, "status": statusValue, "items": items})
			}
			return map[string]any{"ts": time.Now().UnixMilli(), "previews": previews}, http.StatusOK, nil
		}

		session, err := opts.GetSession(ctx, req.SessionID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		transcript, err := opts.ListTranscript(ctx, req.SessionID, req.Limit)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		items := make([]map[string]any, 0, len(transcript))
		for _, entry := range transcript {
			items = append(items, map[string]any{"role": entry.Role, "text": entry.Text})
		}
		statusValue := "ok"
		if len(items) == 0 {
			statusValue = "empty"
		}
		return map[string]any{
			"session": session,
			"preview": transcript,
			"ts":      time.Now().UnixMilli(),
			"previews": []map[string]any{{
				"key":    req.SessionID,
				"status": statusValue,
				"items":  items,
			}},
		}, http.StatusOK, nil
	case methods.MethodSessionsPatch:
		if opts.GetSession == nil || opts.PutSession == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("session providers not configured")
		}
		req, err := methods.DecodeSessionsPatchParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		session, err := opts.GetSession(ctx, req.SessionID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		session.Meta = mergeSessionMeta(session.Meta, req.Meta)
		if err := opts.PutSession(ctx, req.SessionID, session); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true, "key": req.SessionID, "session": session}, http.StatusOK, nil
	case methods.MethodSessionsReset:
		if opts.GetSession == nil || opts.PutSession == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("session providers not configured")
		}
		req, err := methods.DecodeSessionsResetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		session, err := opts.GetSession(ctx, req.SessionID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		session.LastInboundAt = 0
		session.LastReplyAt = 0
		session.Meta = map[string]any{}
		if err := opts.PutSession(ctx, req.SessionID, session); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true, "key": req.SessionID, "session": session}, http.StatusOK, nil
	case methods.MethodSessionsDelete:
		if opts.GetSession == nil || opts.PutSession == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("session providers not configured")
		}
		req, err := methods.DecodeSessionsDeleteParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		session, err := opts.GetSession(ctx, req.SessionID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		session.Meta = mergeSessionMeta(session.Meta, map[string]any{"deleted": true, "deleted_at": time.Now().Unix()})
		if err := opts.PutSession(ctx, req.SessionID, session); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "key": req.SessionID, "deleted": true}), http.StatusOK, nil
	case methods.MethodSessionsCompact:
		if opts.GetSession == nil || opts.PutSession == nil || opts.ListTranscript == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("session providers not configured")
		}
		req, err := methods.DecodeSessionsCompactParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		session, err := opts.GetSession(ctx, req.SessionID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		entries, err := opts.ListTranscript(ctx, req.SessionID, 2000)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		dropped := len(entries) - req.Keep
		if dropped < 0 {
			dropped = 0
		}
		session.Meta = mergeSessionMeta(session.Meta, map[string]any{
			"compacted_at":              time.Now().Unix(),
			"compacted_keep":            req.Keep,
			"compacted_from_entries":    len(entries),
			"compacted_dropped_entries": dropped,
		})
		if err := opts.PutSession(ctx, req.SessionID, session); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "key": req.SessionID, "kept": req.Keep, "from_entries": len(entries), "dropped": dropped}), http.StatusOK, nil
	case methods.MethodSessionsPrune:
		if opts.SessionsPrune == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("sessions prune provider not configured")
		}
		var req methods.SessionsPruneRequest
		if len(call.Params) > 0 {
			if err := json.Unmarshal(call.Params, &req); err != nil {
				return nil, http.StatusBadRequest, err
			}
		}
		if !req.All && req.OlderThanDays <= 0 {
			return nil, http.StatusBadRequest, fmt.Errorf("older_than_days must be > 0 unless all=true")
		}
		out, err := opts.SessionsPrune(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSessionsExport, methods.MethodSessionsSpawn:
		return delegateControlCall(ctx, opts, method, call.Params, "session provider not configured")
	default:
		return nil, 0, nil
	}
}
