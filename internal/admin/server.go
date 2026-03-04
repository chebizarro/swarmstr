package admin

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"swarmstr/internal/gateway/methods"
	"swarmstr/internal/memory"
	"swarmstr/internal/policy"
	"swarmstr/internal/store/state"
)

type StatusProvider struct {
	PubKey   string
	Relays   []string
	DMPolicy string
	Started  time.Time
}

type SendDMRequest struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

type SessionView struct {
	Session    state.SessionDoc           `json:"session"`
	Transcript []state.TranscriptEntryDoc `json:"transcript"`
}

type contextKey string

const tokenAuthContextKey contextKey = "admin-token-authenticated"

type ServerOptions struct {
	Addr               string
	Token              string
	Status             StatusProvider
	StatusDMPolicy     func() string
	StatusRelays       func() []string
	SearchMemory       func(query string, limit int) []memory.IndexedMemory
	GetCheckpoint      func(context.Context, string) (state.CheckpointDoc, error)
	StartAgent         func(context.Context, methods.AgentRequest) (map[string]any, error)
	WaitAgent          func(context.Context, methods.AgentWaitRequest) (map[string]any, error)
	AgentIdentity      func(context.Context, methods.AgentIdentityRequest) (map[string]any, error)
	SendDM             func(context.Context, string, string) error
	AbortChat          func(context.Context, string) (int, error)
	GetSession         func(context.Context, string) (state.SessionDoc, error)
	PutSession         func(context.Context, string, state.SessionDoc) error
	ListSessions       func(context.Context, int) ([]state.SessionDoc, error)
	ListTranscript     func(context.Context, string, int) ([]state.TranscriptEntryDoc, error)
	TailLogs           func(context.Context, int64, int, int) (map[string]any, error)
	ChannelsStatus     func(context.Context, methods.ChannelsStatusRequest) (map[string]any, error)
	ChannelsLogout     func(context.Context, string) (map[string]any, error)
	UsageStatus        func(context.Context) (map[string]any, error)
	UsageCost          func(context.Context, methods.UsageCostRequest) (map[string]any, error)
	GetList            func(context.Context, string) (state.ListDoc, error)
	PutList            func(context.Context, string, state.ListDoc) error
	ListAgents         func(context.Context, methods.AgentsListRequest) (map[string]any, error)
	CreateAgent        func(context.Context, methods.AgentsCreateRequest) (map[string]any, error)
	UpdateAgent        func(context.Context, methods.AgentsUpdateRequest) (map[string]any, error)
	DeleteAgent        func(context.Context, methods.AgentsDeleteRequest) (map[string]any, error)
	ListAgentFiles     func(context.Context, methods.AgentsFilesListRequest) (map[string]any, error)
	GetAgentFile       func(context.Context, methods.AgentsFilesGetRequest) (map[string]any, error)
	SetAgentFile       func(context.Context, methods.AgentsFilesSetRequest) (map[string]any, error)
	ListModels         func(context.Context, methods.ModelsListRequest) (map[string]any, error)
	ToolsCatalog       func(context.Context, methods.ToolsCatalogRequest) (map[string]any, error)
	SkillsStatus       func(context.Context, methods.SkillsStatusRequest) (map[string]any, error)
	SkillsInstall      func(context.Context, methods.SkillsInstallRequest) (map[string]any, error)
	SkillsUpdate       func(context.Context, methods.SkillsUpdateRequest) (map[string]any, error)
	NodePairRequest    func(context.Context, methods.NodePairRequest) (map[string]any, error)
	NodePairList       func(context.Context, methods.NodePairListRequest) (map[string]any, error)
	NodePairApprove    func(context.Context, methods.NodePairApproveRequest) (map[string]any, error)
	NodePairReject     func(context.Context, methods.NodePairRejectRequest) (map[string]any, error)
	NodePairVerify     func(context.Context, methods.NodePairVerifyRequest) (map[string]any, error)
	DevicePairList     func(context.Context, methods.DevicePairListRequest) (map[string]any, error)
	DevicePairApprove  func(context.Context, methods.DevicePairApproveRequest) (map[string]any, error)
	DevicePairReject   func(context.Context, methods.DevicePairRejectRequest) (map[string]any, error)
	DevicePairRemove   func(context.Context, methods.DevicePairRemoveRequest) (map[string]any, error)
	DeviceTokenRotate  func(context.Context, methods.DeviceTokenRotateRequest) (map[string]any, error)
	DeviceTokenRevoke  func(context.Context, methods.DeviceTokenRevokeRequest) (map[string]any, error)
	GetConfig          func(context.Context) (state.ConfigDoc, error)
	PutConfig          func(context.Context, state.ConfigDoc) error
	GetListWithEvent   func(context.Context, string) (state.ListDoc, state.Event, error)
	GetConfigWithEvent func(context.Context) (state.ConfigDoc, state.Event, error)
	GetRelayPolicy     func(context.Context) (methods.RelayPolicyResponse, error)
}

func Start(ctx context.Context, opts ServerOptions) error {
	if strings.TrimSpace(opts.Addr) == "" {
		return nil
	}
	if err := validateExposure(opts.Addr, opts.Token); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", withAuth(opts.Token, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	mux.HandleFunc("/status", withAuth(opts.Token, func(w http.ResponseWriter, _ *http.Request) {
		dmPolicy := opts.Status.DMPolicy
		if opts.StatusDMPolicy != nil {
			dmPolicy = opts.StatusDMPolicy()
		}
		relays := opts.Status.Relays
		if opts.StatusRelays != nil {
			relays = opts.StatusRelays()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"pubkey":         opts.Status.PubKey,
			"relays":         relays,
			"dm_policy":      dmPolicy,
			"uptime_seconds": int(time.Since(opts.Status.Started).Seconds()),
		})
	}))
	mux.HandleFunc("/memory/search", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing q"})
			return
		}
		if utf8.RuneCountInString(q) > 256 {
			q = truncateRunes(q, 256)
		}
		limit := parseLimit(r.URL.Query().Get("limit"), 20, 200)
		if opts.SearchMemory == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "memory search not configured"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": opts.SearchMemory(q, limit)})
	}))
	mux.HandleFunc("/checkpoints/", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		name := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/checkpoints/"))
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing checkpoint name"})
			return
		}
		if opts.GetCheckpoint == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "checkpoint provider not configured"})
			return
		}
		doc, err := opts.GetCheckpoint(r.Context(), name)
		if err != nil {
			handleStateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, doc)
	}))
	mux.HandleFunc("/chat/send", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if opts.SendDM == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "send dm not configured"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req SendDMRequest
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
			return
		}
		req.To = strings.TrimSpace(req.To)
		req.Text = strings.TrimSpace(req.Text)
		if req.To == "" || req.Text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "to and text are required"})
			return
		}
		if err := opts.SendDM(r.Context(), req.To, req.Text); err != nil {
			log.Printf("admin send dm failed: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "send failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	mux.HandleFunc("/sessions/", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		sessionID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/sessions/"))
		if sessionID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing session id"})
			return
		}
		if opts.GetSession == nil || opts.ListTranscript == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "session providers not configured"})
			return
		}
		limit := parseLimit(r.URL.Query().Get("limit"), 50, 500)
		session, err := opts.GetSession(r.Context(), sessionID)
		if err != nil {
			handleStateError(w, err)
			return
		}
		transcript, err := opts.ListTranscript(r.Context(), sessionID, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, SessionView{Session: session, Transcript: transcript})
	}))
	mux.HandleFunc("/call", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		result, status, err := dispatchMethodCall(r.Context(), w, r, opts)
		if isNIP86RPC(r) {
			if err != nil {
				writeNIP86JSON(w, map[string]any{"error": methods.MapNIP86Error(status, err)})
				return
			}
			writeNIP86JSON(w, map[string]any{"result": result})
			return
		}
		if err != nil {
			writeJSON(w, status, methods.CallResponse{OK: false, Error: err.Error()})
			return
		}
		writeJSON(w, status, methods.CallResponse{OK: true, Result: result})
	}))
	mux.HandleFunc("/config", withAuth(opts.Token, func(w http.ResponseWriter, r *http.Request) {
		if opts.GetConfig == nil || opts.PutConfig == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "config providers not configured"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			cfg, err := opts.GetConfig(r.Context())
			if err != nil {
				handleStateError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		case http.MethodPut:
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			var cfg state.ConfigDoc
			if err := dec.Decode(&cfg); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
				return
			}
			if cfg.Version == 0 {
				cfg.Version = 1
			}
			if err := opts.PutConfig(r.Context(), cfg); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
	}))

	srv := &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("admin API listening on %s", opts.Addr)
	err := srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func dispatchMethodCall(ctx context.Context, w http.ResponseWriter, r *http.Request, opts ServerOptions) (any, int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid request body")
	}
	var call methods.CallRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&call); err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("invalid request body")
	}
	method := strings.TrimSpace(call.Method)
	if method == "" {
		return nil, http.StatusBadRequest, fmt.Errorf("method is required")
	}

	cfg := state.ConfigDoc{}
	if opts.GetConfig != nil {
		current, err := opts.GetConfig(ctx)
		if err != nil {
			cfg.Control.RequireAuth = true
		} else {
			cfg = current
		}
	}
	auth := policy.AuthenticateControlCall(r, raw, 30*time.Second)
	if !auth.Authenticated {
		if tokenOK, _ := ctx.Value(tokenAuthContextKey).(bool); tokenOK && cfg.Control.LegacyTokenFallback && len(cfg.Control.Admins) == 0 {
			auth.Authenticated = true
			auth.CallerPubKey = "token-local"
			auth.Reason = ""
			cfg.Control.RequireAuth = false
		}
	}
	decision := policy.EvaluateControlCall(auth.CallerPubKey, method, auth.Authenticated, cfg)
	if !decision.Allowed {
		if !decision.Authenticated {
			reason := decision.Reason
			if reason == "" {
				reason = auth.Reason
			}
			if strings.TrimSpace(reason) == "" {
				reason = "authentication required"
			}
			return nil, http.StatusUnauthorized, errors.New(reason)
		}
		if strings.TrimSpace(decision.Reason) == "" {
			return nil, http.StatusForbidden, fmt.Errorf("forbidden")
		}
		return nil, http.StatusForbidden, errors.New(decision.Reason)
	}

	switch method {
	case methods.MethodSupportedMethods:
		return methods.SupportedMethods(), http.StatusOK, nil
	case methods.MethodHealth:
		return map[string]any{"ok": true}, http.StatusOK, nil
	case methods.MethodDoctorMemoryStatus:
		return map[string]any{"ok": true, "index": map[string]any{"available": opts.SearchMemory != nil}}, http.StatusOK, nil
	case methods.MethodLogsTail:
		req, err := methods.DecodeLogsTailParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.TailLogs == nil {
			return map[string]any{"cursor": req.Cursor, "lines": []string{}, "truncated": false, "reset": false}, http.StatusOK, nil
		}
		out, err := opts.TailLogs(ctx, req.Cursor, req.Limit, req.MaxBytes)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodChannelsStatus:
		req, err := methods.DecodeChannelsStatusParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ChannelsStatus == nil {
			return map[string]any{"channels": []map[string]any{{"id": "nostr", "connected": true}}}, http.StatusOK, nil
		}
		out, err := opts.ChannelsStatus(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodChannelsLogout:
		req, err := methods.DecodeChannelsLogoutParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ChannelsLogout == nil {
			return map[string]any{"ok": true, "channel": req.Channel}, http.StatusOK, nil
		}
		out, err := opts.ChannelsLogout(ctx, req.Channel)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodStatus:
		dmPolicy := opts.Status.DMPolicy
		if opts.StatusDMPolicy != nil {
			dmPolicy = opts.StatusDMPolicy()
		}
		relays := opts.Status.Relays
		if opts.StatusRelays != nil {
			relays = opts.StatusRelays()
		}
		return methods.StatusResponse{
			PubKey:        opts.Status.PubKey,
			Relays:        relays,
			DMPolicy:      dmPolicy,
			UptimeSeconds: int(time.Since(opts.Status.Started).Seconds()),
		}, http.StatusOK, nil
	case methods.MethodUsageStatus:
		if opts.UsageStatus == nil {
			return map[string]any{"ok": true, "totals": map[string]any{"requests": 0, "tokens": 0}}, http.StatusOK, nil
		}
		out, err := opts.UsageStatus(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodUsageCost:
		req, err := methods.DecodeUsageCostParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.UsageCost == nil {
			return map[string]any{"ok": true, "total_usd": 0}, http.StatusOK, nil
		}
		out, err := opts.UsageCost(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodMemorySearch:
		if opts.SearchMemory == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("memory search not configured")
		}
		req, err := methods.DecodeMemorySearchParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		return methods.MemorySearchResponse{Results: opts.SearchMemory(req.Query, req.Limit)}, http.StatusOK, nil
	case methods.MethodAgent:
		req, err := methods.DecodeAgentParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.StartAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agent runtime not configured")
		}
		out, err := opts.StartAgent(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodAgentWait:
		req, err := methods.DecodeAgentWaitParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.WaitAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agent runtime not configured")
		}
		out, err := opts.WaitAgent(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodAgentIdentityGet:
		req, err := methods.DecodeAgentIdentityParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.AgentIdentity == nil {
			return map[string]any{"agent_id": "main", "display_name": "Swarmstr Agent", "session_id": req.SessionID}, http.StatusOK, nil
		}
		out, err := opts.AgentIdentity(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
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
		return map[string]any{"ok": true}, http.StatusOK, nil
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
		return map[string]any{"session_id": req.SessionID, "entries": transcript}, http.StatusOK, nil
	case methods.MethodChatAbort:
		req, err := methods.DecodeChatAbortParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		aborted := 0
		if opts.AbortChat != nil {
			aborted, err = opts.AbortChat(ctx, req.SessionID)
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
		}
		return map[string]any{"ok": true, "session_id": req.SessionID, "aborted": aborted > 0, "aborted_count": aborted}, http.StatusOK, nil
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
		sessions, err := opts.ListSessions(ctx, req.Limit)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"sessions": sessions}, http.StatusOK, nil
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
		return map[string]any{"session": session, "preview": transcript}, http.StatusOK, nil
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
		return map[string]any{"ok": true, "session": session}, http.StatusOK, nil
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
		return map[string]any{"ok": true, "session": session}, http.StatusOK, nil
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
		return map[string]any{"ok": true, "session_id": req.SessionID, "deleted": true}, http.StatusOK, nil
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
		return map[string]any{"ok": true, "session_id": req.SessionID, "kept": req.Keep, "from_entries": len(entries), "dropped": dropped}, http.StatusOK, nil
	case methods.MethodAgentsList:
		req, err := methods.DecodeAgentsListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ListAgents == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.ListAgents(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodAgentsCreate:
		req, err := methods.DecodeAgentsCreateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.CreateAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.CreateAgent(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodAgentsUpdate:
		req, err := methods.DecodeAgentsUpdateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.UpdateAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.UpdateAgent(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodAgentsDelete:
		req, err := methods.DecodeAgentsDeleteParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DeleteAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.DeleteAgent(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodAgentsFilesList:
		req, err := methods.DecodeAgentsFilesListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ListAgentFiles == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.ListAgentFiles(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodAgentsFilesGet:
		req, err := methods.DecodeAgentsFilesGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.GetAgentFile == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.GetAgentFile(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodAgentsFilesSet:
		req, err := methods.DecodeAgentsFilesSetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SetAgentFile == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.SetAgentFile(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodModelsList:
		req, err := methods.DecodeModelsListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ListModels == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("models provider not configured")
		}
		out, err := opts.ListModels(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodToolsCatalog:
		req, err := methods.DecodeToolsCatalogParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ToolsCatalog == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tools catalog provider not configured")
		}
		out, err := opts.ToolsCatalog(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodSkillsStatus:
		req, err := methods.DecodeSkillsStatusParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SkillsStatus == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("skills provider not configured")
		}
		out, err := opts.SkillsStatus(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodSkillsInstall:
		req, err := methods.DecodeSkillsInstallParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SkillsInstall == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("skills provider not configured")
		}
		out, err := opts.SkillsInstall(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodSkillsUpdate:
		req, err := methods.DecodeSkillsUpdateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SkillsUpdate == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("skills provider not configured")
		}
		out, err := opts.SkillsUpdate(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodNodePairRequest:
		req, err := methods.DecodeNodePairRequestParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairRequest == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairRequest(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodNodePairList:
		req, err := methods.DecodeNodePairListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairList(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodNodePairApprove:
		req, err := methods.DecodeNodePairApproveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairApprove == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairApprove(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodNodePairReject:
		req, err := methods.DecodeNodePairRejectParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairReject == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairReject(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodNodePairVerify:
		req, err := methods.DecodeNodePairVerifyParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.NodePairVerify == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("node pairing provider not configured")
		}
		out, err := opts.NodePairVerify(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodDevicePairList:
		req, err := methods.DecodeDevicePairListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DevicePairList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device pairing provider not configured")
		}
		out, err := opts.DevicePairList(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodDevicePairApprove:
		req, err := methods.DecodeDevicePairApproveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DevicePairApprove == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device pairing provider not configured")
		}
		out, err := opts.DevicePairApprove(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodDevicePairReject:
		req, err := methods.DecodeDevicePairRejectParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DevicePairReject == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device pairing provider not configured")
		}
		out, err := opts.DevicePairReject(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodDevicePairRemove:
		req, err := methods.DecodeDevicePairRemoveParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DevicePairRemove == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device pairing provider not configured")
		}
		out, err := opts.DevicePairRemove(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodDeviceTokenRotate:
		req, err := methods.DecodeDeviceTokenRotateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DeviceTokenRotate == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device token provider not configured")
		}
		out, err := opts.DeviceTokenRotate(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodDeviceTokenRevoke:
		req, err := methods.DecodeDeviceTokenRevokeParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DeviceTokenRevoke == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("device token provider not configured")
		}
		out, err := opts.DeviceTokenRevoke(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return out, http.StatusOK, nil
	case methods.MethodConfigGet:
		if opts.GetConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config provider not configured")
		}
		cfg, err := opts.GetConfig(ctx)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return cfg, http.StatusOK, nil
	case methods.MethodRelayPolicyGet:
		if opts.GetRelayPolicy == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("relay policy provider not configured")
		}
		policyView, err := opts.GetRelayPolicy(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return policyView, http.StatusOK, nil
	case methods.MethodListGet:
		if opts.GetList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("list provider not configured")
		}
		req, err := methods.DecodeListGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		list, err := opts.GetList(ctx, req.Name)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return list, http.StatusOK, nil
	case methods.MethodListPut:
		if opts.PutList == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("list provider not configured")
		}
		req, err := methods.DecodeListPutParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if req.ExpectedVersion > 0 || req.ExpectedEvent != "" {
			if opts.GetListWithEvent == nil {
				return nil, http.StatusNotImplemented, fmt.Errorf("list preconditions not supported")
			}
			current, evt, err := opts.GetListWithEvent(ctx, req.Name)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					return nil, http.StatusConflict, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nil, http.StatusInternalServerError, err
			}
			if req.ExpectedVersion > 0 && current.Version != req.ExpectedVersion {
				return nil, http.StatusConflict, &methods.PreconditionConflictError{
					Resource:        "list:" + req.Name,
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nil, http.StatusConflict, &methods.PreconditionConflictError{
					Resource:        "list:" + req.Name,
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
		if err := opts.PutList(ctx, req.Name, state.ListDoc{
			Version: 1,
			Name:    req.Name,
			Items:   req.Items,
		}); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true}, http.StatusOK, nil
	case methods.MethodConfigPut:
		if opts.PutConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config provider not configured")
		}
		req, err := methods.DecodeConfigPutParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if req.ExpectedVersion > 0 || req.ExpectedEvent != "" {
			if opts.GetConfigWithEvent == nil {
				return nil, http.StatusNotImplemented, fmt.Errorf("config preconditions not supported")
			}
			current, evt, err := opts.GetConfigWithEvent(ctx)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					return nil, http.StatusConflict, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nil, http.StatusInternalServerError, err
			}
			if req.ExpectedVersion > 0 && current.Version != req.ExpectedVersion {
				return nil, http.StatusConflict, &methods.PreconditionConflictError{
					Resource:        "config",
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nil, http.StatusConflict, &methods.PreconditionConflictError{
					Resource:        "config",
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
		if err := opts.PutConfig(ctx, req.Config); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true}, http.StatusOK, nil
	case methods.MethodConfigSet:
		if opts.GetConfig == nil || opts.PutConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config providers not configured")
		}
		req, err := methods.DecodeConfigSetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		current, err := opts.GetConfig(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		next, err := methods.ApplyConfigSet(current, req.Key, req.Value)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if err := opts.PutConfig(ctx, next); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true}, http.StatusOK, nil
	case methods.MethodConfigApply:
		if opts.PutConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config provider not configured")
		}
		req, err := methods.DecodeConfigApplyParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if err := opts.PutConfig(ctx, req.Config); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true}, http.StatusOK, nil
	case methods.MethodConfigPatch:
		if opts.GetConfig == nil || opts.PutConfig == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("config providers not configured")
		}
		req, err := methods.DecodeConfigPatchParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		current, err := opts.GetConfig(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		next, err := methods.ApplyConfigPatch(current, req.Patch)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if err := opts.PutConfig(ctx, next); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return map[string]any{"ok": true}, http.StatusOK, nil
	case methods.MethodConfigSchema:
		return methods.ConfigSchema(), http.StatusOK, nil
	default:
		return nil, http.StatusNotFound, fmt.Errorf("unknown method %q", method)
	}
}

func isNIP86RPC(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	if strings.Contains(ct, "application/nostr+json+rpc") || strings.Contains(accept, "application/nostr+json+rpc") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("profile")), "nip86")
}

func parseLimit(raw string, def, max int) int {
	limit := def
	if strings.TrimSpace(raw) != "" {
		fmt.Sscanf(raw, "%d", &limit)
	}
	if limit <= 0 {
		limit = def
	}
	if limit > max {
		limit = max
	}
	return limit
}

func handleStateError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

func withAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	if strings.TrimSpace(token) == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		parts := strings.Fields(auth)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), tokenAuthContextKey, true)
		next(w, r.WithContext(ctx))
	}
}

func validateExposure(addr string, token string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid admin addr %q: %w", addr, err)
	}
	if strings.TrimSpace(token) != "" {
		return nil
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("admin token required for non-loopback bind address")
}

func writeNIP86JSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/nostr+json+rpc")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

func mergeSessionMeta(base map[string]any, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}
