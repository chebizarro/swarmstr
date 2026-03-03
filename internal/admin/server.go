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
	SearchMemory       func(query string, limit int) []memory.IndexedMemory
	GetCheckpoint      func(context.Context, string) (state.CheckpointDoc, error)
	SendDM             func(context.Context, string, string) error
	GetSession         func(context.Context, string) (state.SessionDoc, error)
	ListTranscript     func(context.Context, string, int) ([]state.TranscriptEntryDoc, error)
	GetList            func(context.Context, string) (state.ListDoc, error)
	PutList            func(context.Context, string, state.ListDoc) error
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
		writeJSON(w, http.StatusOK, map[string]any{
			"pubkey":         opts.Status.PubKey,
			"relays":         opts.Status.Relays,
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
		if len(q) > 256 {
			q = q[:256]
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
	case methods.MethodStatus:
		dmPolicy := opts.Status.DMPolicy
		if opts.StatusDMPolicy != nil {
			dmPolicy = opts.StatusDMPolicy()
		}
		return methods.StatusResponse{
			PubKey:        opts.Status.PubKey,
			Relays:        opts.Status.Relays,
			DMPolicy:      dmPolicy,
			UptimeSeconds: int(time.Since(opts.Status.Started).Seconds()),
		}, http.StatusOK, nil
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
