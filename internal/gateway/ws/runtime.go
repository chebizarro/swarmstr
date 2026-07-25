package ws

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"metiq/internal/gateway/protocol"
	"metiq/internal/policy"
)

const (
	MethodEventsSubscribe             = "events.subscribe"
	MethodEventsUnsubscribe           = "events.unsubscribe"
	MethodEventsList                  = "events.list"
	MethodSessionsSubscribe           = "sessions.subscribe"
	MethodSessionsUnsubscribe         = "sessions.unsubscribe"
	MethodSessionsMessagesSubscribe   = "sessions.messages.subscribe"
	MethodSessionsMessagesUnsubscribe = "sessions.messages.unsubscribe"

	defaultClientEventBufferSize = 32
)

// ErrDisabled is returned by Start when no listen address is configured. It is a
// sentinel so callers can distinguish an intentionally-disabled gateway from a
// real startup failure via errors.Is, instead of receiving a misleading
// (nil, nil) result that looks initialized while nothing is listening.
var ErrDisabled = errors.New("gateway ws: disabled (no listen address configured)")

type RequestHandler func(context.Context, protocol.RequestFrame) (any, *protocol.ErrorShape)

type ConnectionInfo struct {
	ID        string
	Principal ControlPrincipal
}

// DeviceTokenDecision is returned by the daemon's persisted-pairing validator.
// A successful decision binds the presented token to one device, role, and
// approved scope set before the signed connect proof is accepted.
type DeviceTokenDecision struct {
	OK      bool
	Role    string
	Scopes  []string
	Subject string
	Reason  string
	Code    string
}

type DeviceTokenValidator func(protocol.ConnectParams, string) DeviceTokenDecision

type RuntimeOptions struct {
	Addr                    string
	Path                    string
	Token                   string
	Methods                 []string
	Events                  []string
	MethodDescriptors       []protocol.MethodDescriptor
	Version                 string
	HandshakeTTL            time.Duration
	MaxPayloadSize          int64
	AuthRateLimitPerMin     int
	UnauthorizedBurstMax    int
	ControlWriteLimitPerMin int
	AllowedOrigins          []string
	TrustedProxies          []string
	AllowInsecureControlUI  bool
	DeviceAuthSignatureSkew time.Duration
	WriteTimeout            time.Duration
	// EventBufferSize bounds each client's async broadcast queue. When a
	// subscribed client cannot keep up and its queue fills, the runtime drops
	// that client instead of blocking fanout to other subscribers.
	EventBufferSize       int
	DeltaCoalesceInterval time.Duration
	// EventAuthorizer, when non-nil, filters broadcast fanout per connection.
	// Returning false suppresses delivery of one event to one client without
	// affecting other subscribers. It runs on the broadcast path and must be
	// fast and non-blocking.
	EventAuthorizer     func(principal ControlPrincipal, event string, payload any) bool
	HandleRequest       RequestHandler
	ValidateDeviceToken DeviceTokenValidator
	OnDisconnect        func(context.Context, ConnectionInfo)
	// StartupReady reports whether sidecar-backed methods may be dispatched.
	// Nil means ready, which matches runtimes started after daemon initialization.
	StartupReady func() bool
	// StaticHandler, when non-nil, is mounted at "/" in the same HTTP server
	// as the WebSocket endpoint.  It is called only when the request path
	// does not match Path (the WS path).
	StaticHandler http.Handler
}

type Runtime struct {
	opts RuntimeOptions
	srv  *http.Server

	mu      sync.RWMutex
	clients map[string]*client
	seq     int64 // state-version counter (event delivery uses per-client seq)

	rateMu            sync.Mutex
	rateState         map[string]rateWindow
	allowedMethods    map[string]struct{}
	methodDescriptors map[string]protocol.MethodDescriptor
	coalesceMu        sync.Mutex
	chatCoalesce      map[string]*chatChunkCoalescer
}

const defaultDeltaCoalesceInterval = 100 * time.Millisecond

type chatChunkCoalescer struct {
	payload ChatDeltaEvent
	timer   *time.Timer
}

type client struct {
	id        string
	conn      *websocket.Conn
	connected protocol.ConnectParams

	subMu              sync.RWMutex
	subscriptions      map[string]struct{}
	watchedSessions    map[string]struct{}
	allSessionMessages bool

	writeMu sync.Mutex

	eventQueue     chan any
	eventDone      chan struct{}
	closeOnce      sync.Once
	disconnectOnce sync.Once
	principal      ControlPrincipal

	authMu       sync.Mutex
	unauthorized int
	seq          int64

	controlWriteMu     sync.Mutex
	controlWriteWindow rateWindow
}

type rateWindow struct {
	count   int
	resetAt time.Time
}

type eventSubscriptionRequest struct {
	Events []string `json:"events"`
}

type sessionMessageSubscriptionRequest struct {
	Key              string `json:"key"`
	AgentID          string `json:"agentId,omitempty"`
	IncludeApprovals bool   `json:"includeApprovals,omitempty"`
}

var sessionSubscriptionEvents = []string{
	EventChat,
	EventChatMessage,
	EventAgentStatus,
	EventToolStart,
	EventToolProgress,
	EventToolResult,
	EventToolError,
}

func Start(ctx context.Context, opts RuntimeOptions) (*Runtime, error) {
	if strings.TrimSpace(opts.Addr) == "" {
		return nil, ErrDisabled
	}
	if strings.TrimSpace(opts.Path) == "" {
		opts.Path = "/ws"
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.HandshakeTTL <= 0 {
		opts.HandshakeTTL = 10 * time.Second
	}
	if opts.MaxPayloadSize <= 0 {
		opts.MaxPayloadSize = 1 << 20
	}
	if opts.AuthRateLimitPerMin <= 0 {
		opts.AuthRateLimitPerMin = 60
	}
	if opts.UnauthorizedBurstMax <= 0 {
		opts.UnauthorizedBurstMax = 8
	}
	if opts.ControlWriteLimitPerMin < 0 {
		opts.ControlWriteLimitPerMin = 0
	}
	if opts.DeviceAuthSignatureSkew <= 0 {
		opts.DeviceAuthSignatureSkew = 2 * time.Minute
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 5 * time.Second
	}
	if opts.EventBufferSize <= 0 {
		opts.EventBufferSize = defaultClientEventBufferSize
	}
	if opts.DeltaCoalesceInterval < 0 {
		opts.DeltaCoalesceInterval = 0
	}

	if err := validateExposure(opts.Addr, opts.Token); err != nil {
		return nil, err
	}

	descriptors, err := buildMethodDescriptors(opts.MethodDescriptors, opts.Methods)
	if err != nil {
		return nil, err
	}

	r := &Runtime{
		opts:              opts,
		clients:           map[string]*client{},
		rateState:         map[string]rateWindow{},
		allowedMethods:    buildAllowedMethods(opts.Methods),
		methodDescriptors: descriptors,
		chatCoalesce:      map[string]*chatChunkCoalescer{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(opts.Path, r.handleWS)
	if opts.StaticHandler != nil {
		mux.Handle("/", opts.StaticHandler)
	}

	r.srv = &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.srv.Shutdown(shutdownCtx)
	}()

	go func() {
		if err := r.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("gateway ws runtime error: %v", err)
		}
	}()

	// Periodic cleanup of expired rate limit windows to prevent memory leak
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.cleanupExpiredRateLimits()
			}
		}
	}()

	return r, nil
}

func (r *Runtime) Broadcast(event string, payload any) {
	if event == EventChat && r.opts.DeltaCoalesceInterval > 0 {
		if p, ok := payload.(ChatDeltaEvent); ok && p.RunID != "" && !p.Replace {
			r.enqueueCoalescedChatChunk(p)
			return
		}
		if p, ok := payload.(ChatDeltaEvent); ok && p.RunID != "" && p.Replace {
			r.enqueueCoalescedChatChunk(p)
			return
		}
		if key := terminalChatCoalesceKey(payload); key != "" {
			// Keep the coalescer lock across both enqueue operations. A timer flush
			// cannot remove the pending delta and then lose the enqueue race to this
			// terminal event.
			r.coalesceMu.Lock()
			r.flushCoalescedChatChunkLocked(key)
			r.broadcastImmediate(event, payload)
			r.coalesceMu.Unlock()
			return
		}
	}
	r.broadcastImmediate(event, payload)
}

// EmitToConnection delivers one event to exactly one connection, bypassing
// event subscriptions: targeted streams (terminal output) are requested by
// the owning connection via their opening method rather than events.subscribe.
// It reports false when the connection is gone or its queue overflowed.
func (r *Runtime) EmitToConnection(connID, event string, payload any) bool {
	r.mu.RLock()
	c := r.clients[connID]
	r.mu.RUnlock()
	if c == nil {
		return false
	}
	if r.opts.EventAuthorizer != nil && !r.opts.EventAuthorizer(c.principal, event, payload) {
		return false
	}
	seq := atomic.AddInt64(&c.seq, 1)
	frame := map[string]any{
		"type":    protocol.FrameTypeEvent,
		"event":   event,
		"seq":     seq,
		"payload": payload,
	}
	if err := c.enqueueEvent(frame); err != nil {
		r.dropClient(c, err.Error())
		return false
	}
	return true
}

// IsConnectionActive reports whether connID is still registered.
func (r *Runtime) IsConnectionActive(connID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.clients[connID]
	return ok
}

func (r *Runtime) broadcastImmediate(event string, payload any) {
	r.mu.RLock()
	clients := make([]*client, 0, len(r.clients))
	for _, c := range r.clients {
		clients = append(clients, c)
	}
	r.mu.RUnlock()

	emit := func(name string, emitPayload any) {
		for _, c := range clients {
			if !c.isSubscribed(name) {
				continue
			}
			if isSessionMessageEvent(name) && !c.acceptsSessionMessagePayload(emitPayload) {
				continue
			}
			if r.opts.EventAuthorizer != nil && !r.opts.EventAuthorizer(c.principal, name, emitPayload) {
				continue
			}
			seq := atomic.AddInt64(&c.seq, 1)
			frame := map[string]any{
				"type":    protocol.FrameTypeEvent,
				"event":   name,
				"seq":     seq,
				"payload": emitPayload,
			}
			if err := c.enqueueEvent(frame); err != nil {
				r.dropClient(c, err.Error())
			}
		}
	}

	emit(event, payload)
	for _, proj := range compatibilityEventProjections(event, payload) {
		emit(proj.Event, proj.Payload)
	}
}

func (r *Runtime) enqueueCoalescedChatChunk(payload ChatDeltaEvent) {
	key := chatChunkCoalesceKey(payload.SessionKey, payload.RunID)
	r.coalesceMu.Lock()
	entry := r.chatCoalesce[key]
	if entry == nil {
		entry = &chatChunkCoalescer{payload: payload}
		entry.timer = time.AfterFunc(r.opts.DeltaCoalesceInterval, func() { r.flushCoalescedChatChunk(key) })
		r.chatCoalesce[key] = entry
		r.coalesceMu.Unlock()
		return
	}
	if payload.Replace {
		entry.payload.DeltaText = payload.DeltaText
		entry.payload.Replace = true
	} else {
		entry.payload.DeltaText += payload.DeltaText
	}
	entry.payload.Seq = payload.Seq
	entry.payload.Message = payload.Message
	entry.payload.Usage = payload.Usage
	if payload.AgentID != "" {
		entry.payload.AgentID = payload.AgentID
	}
	r.coalesceMu.Unlock()
}

func chatChunkCoalesceKey(sessionID, runID string) string {
	if sessionID == "" {
		sessionID = "__global__"
	}
	return sessionID + "\x00" + runID
}

func terminalChatCoalesceKey(payload any) string {
	switch p := payload.(type) {
	case ChatFinalEvent:
		return chatChunkCoalesceKey(p.SessionKey, p.RunID)
	case ChatAbortedEvent:
		return chatChunkCoalesceKey(p.SessionKey, p.RunID)
	case ChatErrorEvent:
		return chatChunkCoalesceKey(p.SessionKey, p.RunID)
	default:
		return ""
	}
}

func (r *Runtime) flushCoalescedChatChunk(key string) {
	r.coalesceMu.Lock()
	r.flushCoalescedChatChunkLocked(key)
	r.coalesceMu.Unlock()
}

// flushCoalescedChatChunkLocked removes and broadcasts one pending delta while
// coalesceMu remains held. Holding the lock through enqueue is required so a
// concurrent terminal Broadcast cannot overtake a timer-owned delta.
func (r *Runtime) flushCoalescedChatChunkLocked(key string) {
	entry := r.chatCoalesce[key]
	if entry == nil {
		return
	}
	delete(r.chatCoalesce, key)
	if entry.timer != nil {
		entry.timer.Stop()
	}
	if entry.payload.DeltaText != "" {
		r.broadcastImmediate(EventChat, entry.payload)
	}
}

func (r *Runtime) handleWS(w http.ResponseWriter, req *http.Request) {
	if err := validateOrigin(req, r.opts.AllowedOrigins); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	remoteIP := clientIP(req.RemoteAddr)
	if !r.allowHandshake(remoteIP) {
		http.Error(w, "too many handshake attempts", http.StatusTooManyRequests)
		return
	}

	// We run explicit origin policy via validateOrigin() above.
	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	conn.SetReadLimit(r.opts.MaxPayloadSize)

	connID := randomID()
	nonce := randomID()
	challenge := map[string]any{
		"type":    protocol.FrameTypeEvent,
		"event":   "connect.challenge",
		"payload": map[string]any{"nonce": nonce, "ts": time.Now().UnixMilli()},
	}
	if err := writeFrame(req.Context(), conn, challenge); err != nil {
		return
	}

	handshakeCtx, cancel := context.WithTimeout(req.Context(), r.opts.HandshakeTTL)
	defer cancel()
	_, raw, err := conn.Read(handshakeCtx)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "handshake required")
		return
	}

	frameAny, err := protocol.ParseGatewayFrame(raw)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "invalid handshake")
		return
	}
	reqFrame, ok := frameAny.(protocol.RequestFrame)
	if !ok || reqFrame.Method != "connect" {
		_ = writeFrame(req.Context(), conn, map[string]any{
			"type":  protocol.FrameTypeResponse,
			"id":    safeRequestID(reqFrame.ID),
			"ok":    false,
			"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, "first request must be connect", nil),
		})
		_ = conn.Close(websocket.StatusPolicyViolation, "invalid handshake")
		return
	}

	var connect protocol.ConnectParams
	if err := decodeStrict(reqFrame.Params, &connect); err != nil {
		_ = writeFrame(req.Context(), conn, map[string]any{
			"type":  protocol.FrameTypeResponse,
			"id":    reqFrame.ID,
			"ok":    false,
			"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, "invalid connect params", nil),
		})
		_ = conn.Close(websocket.StatusPolicyViolation, "invalid connect")
		return
	}
	negotiated, err := protocol.NegotiateProtocol(connect.MinProtocol, connect.MaxProtocol)
	if err != nil {
		message := "protocol mismatch"
		reason := "protocol_mismatch"
		closeReason := "protocol mismatch"
		data := map[string]any{
			"reason":        reason,
			"requested_min": connect.MinProtocol,
			"requested_max": connect.MaxProtocol,
			"supported_min": protocol.MinProtocolVersion,
			"supported_max": protocol.CurrentProtocolVersion,
		}
		if errors.Is(err, protocol.ErrInvalidProtocolRange) {
			message = "invalid protocol range"
			closeReason = message
			data["reason"] = "invalid_protocol_range"
		}
		_ = writeFrame(req.Context(), conn, map[string]any{
			"type":  protocol.FrameTypeResponse,
			"id":    reqFrame.ID,
			"ok":    false,
			"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, message, data),
		})
		_ = conn.Close(websocket.StatusPolicyViolation, closeReason)
		return
	}
	if err := connect.Validate(); err != nil {
		_ = writeFrame(req.Context(), conn, map[string]any{
			"type":  protocol.FrameTypeResponse,
			"id":    reqFrame.ID,
			"ok":    false,
			"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, err.Error(), nil),
		})
		_ = conn.Close(websocket.StatusPolicyViolation, "invalid connect")
		return
	}

	if connect.Auth == nil || strings.TrimSpace(connect.Auth.Nonce) == "" || connect.Auth.Nonce != nonce {
		_ = writeFrame(req.Context(), conn, map[string]any{
			"type": protocol.FrameTypeResponse,
			"id":   reqFrame.ID,
			"ok":   false,
			"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, "invalid connect nonce", map[string]any{
				"code":   "DEVICE_AUTH_NONCE_MISMATCH",
				"reason": "device-nonce-mismatch",
			}),
		})
		_ = conn.Close(websocket.StatusPolicyViolation, "invalid connect nonce")
		return
	}

	decision := r.evaluateAuth(req, connect)
	if !decision.OK {
		_ = writeFrame(req.Context(), conn, map[string]any{
			"type":  protocol.FrameTypeResponse,
			"id":    reqFrame.ID,
			"ok":    false,
			"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, "unauthorized", map[string]any{"reason": decision.Reason, "code": decision.Code}),
		})
		_ = conn.Close(websocket.StatusPolicyViolation, "unauthorized")
		return
	}

	if err := r.validateDevicePolicy(req, connect, nonce, decision); err != nil {
		_ = writeFrame(req.Context(), conn, map[string]any{
			"type":  protocol.FrameTypeResponse,
			"id":    reqFrame.ID,
			"ok":    false,
			"error": err,
		})
		_ = conn.Close(websocket.StatusPolicyViolation, "device policy")
		return
	}
	principal := r.controlPrincipal(req, connect, decision)

	c := &client{
		id:              connID,
		conn:            conn,
		connected:       connect,
		subscriptions:   map[string]struct{}{},
		watchedSessions: map[string]struct{}{},
		eventQueue:      make(chan any, r.eventBufferSize()),
		eventDone:       make(chan struct{}),
		principal:       principal,
	}
	go c.runEventWriter(r)
	r.mu.Lock()
	r.clients[c.id] = c
	r.mu.Unlock()
	defer func() {
		c.closeEventQueue()
		r.mu.Lock()
		delete(r.clients, c.id)
		r.mu.Unlock()
		r.notifyDisconnect(c)
		r.broadcastPresence()
	}()

	r.broadcastPresence()

	hello := protocol.HelloOK{
		Type:     "hello-ok",
		Protocol: negotiated,
		Server: protocol.ServerInfo{
			Version: r.opts.Version,
			ConnID:  connID,
		},
		Features: protocol.FeatureSet{
			Methods:           append([]string{}, r.opts.Methods...),
			Events:            append([]string{}, r.opts.Events...),
			MethodDescriptors: r.listMethodDescriptors(),
		},
		Snapshot: r.snapshot(),
		Auth: &protocol.HelloAuth{
			Role:       principal.Role,
			Scopes:     append([]string{}, principal.Scopes...),
			IssuedAtMS: time.Now().UnixMilli(),
		},
		Policy: protocol.HelloPolicy{
			MaxPayload:       int(r.opts.MaxPayloadSize),
			MaxBufferedBytes: r.maxBufferedBytes(),
			TickIntervalMS:   1000,
		},
	}

	_ = c.writeFrame(req.Context(), r.writeTimeout(), map[string]any{
		"type":    protocol.FrameTypeResponse,
		"id":      reqFrame.ID,
		"ok":      true,
		"payload": hello,
	})

	for {
		_, data, err := conn.Read(req.Context())
		if err != nil {
			return
		}
		frameAny, err := protocol.ParseGatewayFrame(data)
		if err != nil {
			_ = c.writeFrame(req.Context(), r.writeTimeout(), map[string]any{
				"type":  protocol.FrameTypeResponse,
				"id":    "invalid",
				"ok":    false,
				"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, err.Error(), nil),
			})
			continue
		}
		reqFrame, ok := frameAny.(protocol.RequestFrame)
		if !ok {
			continue
		}
		if !r.isMethodAllowed(reqFrame.Method) {
			_ = c.writeFrame(req.Context(), r.writeTimeout(), map[string]any{
				"type":  protocol.FrameTypeResponse,
				"id":    reqFrame.ID,
				"ok":    false,
				"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, fmt.Sprintf("unknown method %q", strings.TrimSpace(reqFrame.Method)), nil),
			})
			continue
		}
		if shape := r.admitMethod(c, principal, reqFrame.Method); shape != nil {
			c.bumpUnauthorized(shape)
			_ = c.writeFrame(req.Context(), r.writeTimeout(), map[string]any{"type": protocol.FrameTypeResponse, "id": reqFrame.ID, "ok": false, "error": shape})
			if c.shouldClose(r.opts.UnauthorizedBurstMax) {
				_ = conn.Close(websocket.StatusPolicyViolation, "repeated unauthorized requests")
				return
			}
			continue
		}
		if handled, payload, shape := r.handleInternalRequest(c, reqFrame); handled {
			if shape != nil {
				_ = c.writeFrame(req.Context(), r.writeTimeout(), map[string]any{"type": protocol.FrameTypeResponse, "id": reqFrame.ID, "ok": false, "error": shape})
			} else {
				_ = c.writeFrame(req.Context(), r.writeTimeout(), map[string]any{"type": protocol.FrameTypeResponse, "id": reqFrame.ID, "ok": true, "payload": payload})
			}
			continue
		}
		if r.opts.HandleRequest == nil {
			_ = c.writeFrame(req.Context(), r.writeTimeout(), map[string]any{
				"type":  protocol.FrameTypeResponse,
				"id":    reqFrame.ID,
				"ok":    false,
				"error": protocol.NewError(protocol.ErrorCodeUnavailable, "no request handler configured", nil),
			})
			continue
		}
		reqCtx := contextWithControlConnection(contextWithControlPrincipal(req.Context(), principal), c.id)
		payload, shape := r.opts.HandleRequest(reqCtx, reqFrame)
		if shape != nil {
			c.bumpUnauthorized(shape)
			_ = c.writeFrame(req.Context(), r.writeTimeout(), map[string]any{"type": protocol.FrameTypeResponse, "id": reqFrame.ID, "ok": false, "error": shape})
			if c.shouldClose(r.opts.UnauthorizedBurstMax) {
				_ = conn.Close(websocket.StatusPolicyViolation, "repeated unauthorized requests")
				return
			}
			continue
		}
		c.resetUnauthorized()
		_ = c.writeFrame(req.Context(), r.writeTimeout(), map[string]any{"type": protocol.FrameTypeResponse, "id": reqFrame.ID, "ok": true, "payload": payload})
	}
}

func (r *Runtime) handleInternalRequest(c *client, req protocol.RequestFrame) (bool, any, *protocol.ErrorShape) {
	switch strings.TrimSpace(req.Method) {
	case MethodEventsList:
		return true, map[string]any{"events": c.listSubscriptions()}, nil
	case MethodEventsSubscribe:
		var sub eventSubscriptionRequest
		if err := decodeStrict(req.Params, &sub); err != nil {
			return true, nil, protocol.NewError(protocol.ErrorCodeInvalidRequest, "invalid subscribe params", nil)
		}
		normalized, err := normalizeEventList(sub.Events, r.opts.Events)
		if err != nil {
			return true, nil, protocol.NewError(protocol.ErrorCodeInvalidRequest, err.Error(), nil)
		}
		c.addSubscriptions(normalized)
		if containsSessionMessageEvent(normalized) {
			c.setAllSessionMessages(true)
		}
		return true, map[string]any{"events": c.listSubscriptions()}, nil
	case MethodEventsUnsubscribe:
		var sub eventSubscriptionRequest
		if err := decodeStrict(req.Params, &sub); err != nil {
			return true, nil, protocol.NewError(protocol.ErrorCodeInvalidRequest, "invalid unsubscribe params", nil)
		}
		normalized, err := normalizeEventList(sub.Events, r.opts.Events)
		if err != nil {
			return true, nil, protocol.NewError(protocol.ErrorCodeInvalidRequest, err.Error(), nil)
		}
		c.removeSubscriptions(normalized)
		return true, map[string]any{"events": c.listSubscriptions()}, nil
	case MethodSessionsSubscribe:
		events := availableEventSubset(sessionSubscriptionEvents, r.opts.Events)
		c.addSubscriptions(events)
		c.setAllSessionMessages(true)
		return true, map[string]any{"subscribed": true, "events": events}, nil
	case MethodSessionsUnsubscribe:
		events := availableEventSubset(sessionSubscriptionEvents, r.opts.Events)
		c.removeSubscriptions(events)
		c.setAllSessionMessages(false)
		return true, map[string]any{"subscribed": false, "events": events}, nil
	case MethodSessionsMessagesSubscribe:
		var sub sessionMessageSubscriptionRequest
		if err := decodeStrict(req.Params, &sub); err != nil || strings.TrimSpace(sub.Key) == "" {
			return true, nil, protocol.NewError(protocol.ErrorCodeInvalidRequest, "key is required", nil)
		}
		key := strings.TrimSpace(sub.Key)
		events := availableEventSubset([]string{EventChat, EventChatMessage}, r.opts.Events)
		c.addSubscriptions(events)
		c.addWatchedSession(key)
		return true, map[string]any{"ok": true, "key": key, "events": events}, nil
	case MethodSessionsMessagesUnsubscribe:
		var sub sessionMessageSubscriptionRequest
		if err := decodeStrict(req.Params, &sub); err != nil || strings.TrimSpace(sub.Key) == "" {
			return true, nil, protocol.NewError(protocol.ErrorCodeInvalidRequest, "key is required", nil)
		}
		key := strings.TrimSpace(sub.Key)
		c.removeWatchedSession(key)
		if !c.hasWatchedSessions() && !c.hasAllSessionMessages() {
			c.removeSubscriptions([]string{EventChat, EventChatMessage})
		}
		return true, map[string]any{"ok": true, "key": key}, nil
	default:
		return false, nil, nil
	}
}

func (r *Runtime) snapshot() protocol.Snapshot {
	r.mu.RLock()
	presence := make([]protocol.PresenceEntry, 0, len(r.clients))
	for _, c := range r.clients {
		presence = append(presence, protocol.PresenceEntry{
			Host:       c.connected.Client.ID,
			Mode:       c.connected.Client.Mode,
			Platform:   c.connected.Client.Platform,
			Version:    c.connected.Client.Version,
			InstanceID: c.connected.Client.InstanceID,
			TS:         time.Now().UnixMilli(),
		})
	}
	r.mu.RUnlock()

	return protocol.Snapshot{
		Presence: presence,
		Health:   map[string]any{"ok": true},
		StateVersion: protocol.StateVersion{
			Presence: int(atomic.LoadInt64(&r.seq)),
			Health:   0,
		},
		UptimeMS: 0,
	}
}

func (r *Runtime) broadcastPresence() {
	atomic.AddInt64(&r.seq, 1)
	r.Broadcast("presence.updated", map[string]any{"presence": r.snapshot().Presence})
}

type authDecision struct {
	OK             bool
	Method         string
	Reason         string
	Code           string
	Role           string
	Scopes         []string
	ScopesEnforced bool
	Subject        string
}

type ControlPrincipal struct {
	Authenticated  bool
	PubKey         string
	Subject        string
	Method         string
	Role           string
	Scopes         []string
	ScopesEnforced bool
}

type controlPrincipalContextKey struct{}
type controlConnectionContextKey struct{}

func PrincipalFromContext(ctx context.Context) (ControlPrincipal, bool) {
	if ctx == nil {
		return ControlPrincipal{}, false
	}
	principal, ok := ctx.Value(controlPrincipalContextKey{}).(ControlPrincipal)
	return principal, ok
}

func contextWithControlPrincipal(ctx context.Context, principal ControlPrincipal) context.Context {
	return context.WithValue(ctx, controlPrincipalContextKey{}, principal)
}

// ContextWithControlPrincipal attaches a control principal to ctx. It is the
// exported seam used by daemon-side tests that exercise principal-scoped
// request handling outside a live WS connection.
func ContextWithControlPrincipal(ctx context.Context, principal ControlPrincipal) context.Context {
	return contextWithControlPrincipal(ctx, principal)
}

// ContextWithConnectionID attaches a gateway connection id to ctx. It is the
// exported seam used by daemon-side tests for connection-scoped methods.
func ContextWithConnectionID(ctx context.Context, connectionID string) context.Context {
	return contextWithControlConnection(ctx, connectionID)
}

func ConnectionIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(controlConnectionContextKey{}).(string)
	return id, ok && strings.TrimSpace(id) != ""
}

func contextWithControlConnection(ctx context.Context, connectionID string) context.Context {
	return context.WithValue(ctx, controlConnectionContextKey{}, strings.TrimSpace(connectionID))
}

func (r *Runtime) evaluateAuth(req *http.Request, connect protocol.ConnectParams) authDecision {
	token := connectAuthCredential(connect.Auth)
	role := normalizedConnectRole(connect.Role)
	if r.isTrustedProxyAuth(req) {
		scopes := normalizedScopes(connect.Scopes)
		if role == "operator" && len(scopes) == 0 {
			scopes = defaultOperatorScopes()
		}
		return authDecision{OK: true, Method: "trusted-proxy", Role: role, Scopes: scopes, ScopesEnforced: true}
	}

	configuredToken := strings.TrimSpace(r.opts.Token)
	if configuredToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(configuredToken)) == 1 {
		scopes := normalizedScopes(connect.Scopes)
		if role == "operator" && len(scopes) == 0 {
			scopes = defaultOperatorScopes()
		}
		return authDecision{OK: true, Method: "token", Role: role, Scopes: scopes, ScopesEnforced: true}
	}

	if r.opts.ValidateDeviceToken != nil && hasDeviceIdentity(connect.Device) {
		paired := r.opts.ValidateDeviceToken(connect, token)
		if paired.OK {
			pairedRole := normalizedConnectRole(paired.Role)
			if pairedRole != role {
				return authDecision{Reason: "device_role_mismatch", Code: "DEVICE_AUTH_ROLE_MISMATCH"}
			}
			return authDecision{
				OK:             true,
				Method:         "device-token",
				Role:           pairedRole,
				Scopes:         normalizedScopes(paired.Scopes),
				ScopesEnforced: true,
				Subject:        strings.TrimSpace(paired.Subject),
			}
		}
		if paired.Code != "" || paired.Reason != "" {
			return authDecision{Reason: nonEmpty(paired.Reason, "device_token_mismatch"), Code: nonEmpty(paired.Code, "DEVICE_AUTH_TOKEN_MISMATCH")}
		}
	}

	if configuredToken != "" {
		if token == "" {
			return authDecision{Reason: "token_missing", Code: "AUTH_TOKEN_MISSING"}
		}
		return authDecision{Reason: "token_mismatch", Code: "AUTH_TOKEN_MISMATCH"}
	}
	return authDecision{OK: true, Method: "none", Role: role, Scopes: normalizedScopes(connect.Scopes)}
}

func (r *Runtime) controlPrincipal(req *http.Request, connect protocol.ConnectParams, auth authDecision) ControlPrincipal {
	principal := ControlPrincipal{
		Authenticated:  auth.OK,
		Method:         auth.Method,
		Role:           normalizedConnectRole(nonEmpty(auth.Role, connect.Role)),
		Scopes:         append([]string{}, auth.Scopes...),
		ScopesEnforced: auth.ScopesEnforced,
		Subject:        strings.TrimSpace(auth.Subject),
	}
	if strings.EqualFold(strings.TrimSpace(auth.Method), "none") {
		principal.Authenticated = false
	}
	if auth.Method == "trusted-proxy" {
		user := strings.TrimSpace(req.Header.Get("X-Metiq-Proxy-User"))
		principal.PubKey = strings.ToLower(user)
		principal.Subject = user
		return principal
	}
	if hasNostrAuthorizationHeader(req) {
		if nip98 := policy.AuthenticateControlCall(req, nil, r.opts.HandshakeTTL); nip98.Authenticated {
			principal.Authenticated = true
			principal.PubKey = strings.ToLower(strings.TrimSpace(nip98.CallerPubKey))
			principal.Subject = principal.PubKey
			principal.Method = "nip98"
			return principal
		}
	}
	if hasDeviceIdentity(connect.Device) {
		deviceID := strings.ToLower(strings.TrimSpace(connect.Device.ID))
		principal.PubKey = deviceID
		if principal.Subject == "" {
			principal.Subject = deviceID
		}
		principal.Authenticated = true
		if strings.EqualFold(strings.TrimSpace(principal.Method), "") || strings.EqualFold(strings.TrimSpace(principal.Method), "none") || strings.EqualFold(strings.TrimSpace(principal.Method), "token") {
			principal.Method = "device"
		}
	}
	return principal
}

func connectAuthCredential(auth *protocol.ConnectAuth) string {
	if auth == nil {
		return ""
	}
	for _, value := range []string{auth.Token, auth.DeviceToken, auth.Password} {
		if token := strings.TrimSpace(value); token != "" {
			return token
		}
	}
	return ""
}

func hasNostrAuthorizationHeader(req *http.Request) bool {
	if req == nil {
		return false
	}
	for _, name := range []string{"X-Nostr-Authorization", "Authorization"} {
		value := strings.TrimSpace(req.Header.Get(name))
		if value == "" {
			continue
		}
		parts := strings.SplitN(value, " ", 2)
		return len(parts) == 2 && strings.EqualFold(parts[0], "nostr")
	}
	return false
}

func (r *Runtime) validateDevicePolicy(req *http.Request, connect protocol.ConnectParams, nonce string, auth authDecision) *protocol.ErrorShape {
	role := strings.ToLower(strings.TrimSpace(connect.Role))
	if role == "" {
		role = "operator"
	}
	isControlUI := strings.EqualFold(strings.TrimSpace(connect.Client.ID), "control-ui")
	isLocalClient := isLoopbackRemote(clientIP(req.RemoteAddr)) && isLocalOrigin(req.Header.Get("Origin"))
	requireDevice := role == "node"
	if isControlUI && !isLocalClient && !r.opts.AllowInsecureControlUI && auth.Method != "trusted-proxy" {
		requireDevice = true
	}

	hasDevice := hasDeviceIdentity(connect.Device)
	if !hasDevice {
		if requireDevice {
			code := "DEVICE_IDENTITY_REQUIRED"
			if isControlUI {
				code = "CONTROL_UI_DEVICE_IDENTITY_REQUIRED"
			}
			return protocol.NewError(protocol.ErrorCodeInvalidRequest, "device identity required", map[string]any{"code": code})
		}
		return nil
	}

	device := connect.Device
	if strings.TrimSpace(device.Nonce) == "" {
		return protocol.NewError(protocol.ErrorCodeInvalidRequest, "device nonce required", map[string]any{"code": "DEVICE_AUTH_NONCE_REQUIRED", "reason": "device-nonce-missing"})
	}
	if device.Nonce != nonce {
		return protocol.NewError(protocol.ErrorCodeInvalidRequest, "device nonce mismatch", map[string]any{"code": "DEVICE_AUTH_NONCE_MISMATCH", "reason": "device-nonce-mismatch"})
	}
	derivedID, err := deriveDeviceIDFromPublicKey(device.PublicKey)
	if err != nil {
		return protocol.NewError(protocol.ErrorCodeInvalidRequest, "device public key invalid", map[string]any{"code": "DEVICE_AUTH_PUBLIC_KEY_INVALID", "reason": "device-public-key"})
	}
	if derivedID != strings.TrimSpace(device.ID) {
		return protocol.NewError(protocol.ErrorCodeInvalidRequest, "device identity mismatch", map[string]any{"code": "DEVICE_AUTH_ID_MISMATCH", "reason": "device-id-mismatch"})
	}
	if device.SignedAt <= 0 {
		return protocol.NewError(protocol.ErrorCodeInvalidRequest, "device signature expired", map[string]any{"code": "DEVICE_AUTH_SIGNATURE_EXPIRED", "reason": "device-signature-stale"})
	}
	skewWindow := r.opts.DeviceAuthSignatureSkew
	if skewWindow <= 0 {
		skewWindow = 2 * time.Minute
	}
	if skew := time.Since(time.UnixMilli(device.SignedAt)); skew > skewWindow || skew < -skewWindow {
		return protocol.NewError(protocol.ErrorCodeInvalidRequest, "device signature expired", map[string]any{"code": "DEVICE_AUTH_SIGNATURE_EXPIRED", "reason": "device-signature-stale"})
	}
	token := connectAuthCredential(connect.Auth)
	if !verifyDeviceSignatureForConnect(device, connect, role, token) {
		return protocol.NewError(protocol.ErrorCodeInvalidRequest, "device signature invalid", map[string]any{"code": "DEVICE_AUTH_SIGNATURE_INVALID", "reason": "device-signature"})
	}
	return nil
}

func (r *Runtime) isTrustedProxyAuth(req *http.Request) bool {
	if !isTrustedProxyRemote(clientIP(req.RemoteAddr), r.opts.TrustedProxies) {
		return false
	}
	marker := strings.ToLower(strings.TrimSpace(req.Header.Get("X-Metiq-Trusted-Auth")))
	if marker != "1" && marker != "true" && marker != "yes" {
		return false
	}
	return strings.TrimSpace(req.Header.Get("X-Metiq-Proxy-User")) != ""
}

func (r *Runtime) allowHandshake(ip string) bool {
	if r.opts.AuthRateLimitPerMin <= 0 {
		return true
	}
	now := time.Now()
	key := strings.TrimSpace(ip)
	if key == "" {
		key = "unknown"
	}
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	window := r.rateState[key]
	if window.resetAt.IsZero() || now.After(window.resetAt) {
		window = rateWindow{count: 0, resetAt: now.Add(time.Minute)}
	}
	if window.count >= r.opts.AuthRateLimitPerMin {
		r.rateState[key] = window
		return false
	}
	window.count++
	r.rateState[key] = window
	return true
}

func (r *Runtime) cleanupExpiredRateLimits() {
	now := time.Now()
	r.rateMu.Lock()
	defer r.rateMu.Unlock()
	for key, window := range r.rateState {
		if !window.resetAt.IsZero() && now.After(window.resetAt.Add(5*time.Minute)) {
			delete(r.rateState, key)
		}
	}
}

func safeRequestID(id string) string {
	if strings.TrimSpace(id) == "" {
		return "handshake"
	}
	return id
}

func randomID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (r *Runtime) writeTimeout() time.Duration {
	if r == nil || r.opts.WriteTimeout <= 0 {
		return 5 * time.Second
	}
	return r.opts.WriteTimeout
}

func (r *Runtime) eventBufferSize() int {
	if r == nil || r.opts.EventBufferSize <= 0 {
		return defaultClientEventBufferSize
	}
	return r.opts.EventBufferSize
}

func (r *Runtime) maxBufferedBytes() int {
	if r == nil || r.opts.MaxPayloadSize <= 0 {
		return defaultClientEventBufferSize * (1 << 20)
	}
	buffered := r.opts.MaxPayloadSize * int64(r.eventBufferSize())
	if buffered > int64(^uint(0)>>1) {
		return int(^uint(0) >> 1)
	}
	return int(buffered)
}

func (c *client) writeFrame(ctx context.Context, timeout time.Duration, frame any) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	writeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeFrame(writeCtx, c.conn, frame)
}

func (c *client) enqueueEvent(frame any) error {
	if c == nil {
		return fmt.Errorf("event client unavailable")
	}
	if c.eventQueue == nil || c.eventDone == nil {
		return fmt.Errorf("event writer unavailable")
	}
	select {
	case <-c.eventDone:
		return fmt.Errorf("event client closed")
	case c.eventQueue <- frame:
		return nil
	default:
		return fmt.Errorf("event backlog exceeded")
	}
}

func (c *client) runEventWriter(r *Runtime) {
	if c == nil || c.eventQueue == nil {
		return
	}
	for {
		select {
		case <-c.eventDone:
			return
		case frame := <-c.eventQueue:
			if err := c.writeFrame(context.Background(), r.writeTimeout(), frame); err != nil {
				r.dropClient(c, "event write failed")
				return
			}
		}
	}
}

func (c *client) closeEventQueue() {
	if c == nil || c.eventDone == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.eventDone)
	})
}

func (r *Runtime) notifyDisconnect(c *client) {
	if c == nil || r.opts.OnDisconnect == nil {
		return
	}
	c.disconnectOnce.Do(func() { r.opts.OnDisconnect(context.Background(), ConnectionInfo{ID: c.id, Principal: c.principal}) })
}

func (r *Runtime) dropClient(c *client, reason string) {
	if c == nil {
		return
	}
	c.closeEventQueue()
	if c.conn != nil {
		_ = c.conn.Close(websocket.StatusPolicyViolation, reason)
	}
	r.mu.Lock()
	delete(r.clients, c.id)
	r.mu.Unlock()
	r.notifyDisconnect(c)
}

func writeFrame(ctx context.Context, conn *websocket.Conn, frame any) error {
	body, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, body)
}

func decodeStrict(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing data")
	}
	return nil
}

func validateExposure(addr string, token string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid gateway ws addr %q: %w", addr, err)
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
	return fmt.Errorf("gateway token required for non-loopback bind address")
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return strings.TrimSpace(remoteAddr)
	}
	return host
}

func buildMethodDescriptors(descriptors []protocol.MethodDescriptor, methods []string) (map[string]protocol.MethodDescriptor, error) {
	out := make(map[string]protocol.MethodDescriptor, len(descriptors))
	if len(descriptors) == 0 {
		return out, nil
	}
	validScopes := map[string]bool{
		protocol.MethodScopeOperatorRead:      true,
		protocol.MethodScopeOperatorWrite:     true,
		protocol.MethodScopeOperatorAdmin:     true,
		protocol.MethodScopeOperatorApprovals: true,
		protocol.MethodScopeOperatorQuestions: true,
		protocol.MethodScopeOperatorPairing:   true,
		protocol.MethodScopeNode:              true,
		protocol.MethodScopeDynamic:           true,
	}
	for _, descriptor := range descriptors {
		name := strings.TrimSpace(descriptor.Name)
		if name == "" {
			return nil, fmt.Errorf("gateway method descriptor name is required")
		}
		if !validScopes[descriptor.Scope] {
			return nil, fmt.Errorf("gateway method descriptor %q has invalid scope %q", name, descriptor.Scope)
		}
		if descriptor.Startup != "" && descriptor.Startup != protocol.MethodStartupUnavailableUntilSidecars {
			return nil, fmt.Errorf("gateway method descriptor %q has invalid startup availability %q", name, descriptor.Startup)
		}
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("duplicate gateway method descriptor %q", name)
		}
		descriptor.Name = name
		out[name] = descriptor
	}
	for name := range buildAllowedMethods(methods) {
		if _, ok := out[name]; !ok {
			return nil, fmt.Errorf("gateway method %q is missing a descriptor", name)
		}
	}
	return out, nil
}

func (r *Runtime) listMethodDescriptors() []protocol.MethodDescriptor {
	if r == nil || len(r.methodDescriptors) == 0 {
		return nil
	}
	out := make([]protocol.MethodDescriptor, 0, len(r.methodDescriptors))
	for _, descriptor := range r.methodDescriptors {
		out = append(out, descriptor)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Runtime) admitMethod(c *client, principal ControlPrincipal, method string) *protocol.ErrorShape {
	if r == nil || len(r.methodDescriptors) == 0 {
		return nil
	}
	descriptor, ok := r.methodDescriptors[strings.TrimSpace(method)]
	if !ok {
		return protocol.NewError(protocol.ErrorCodeInvalidRequest, "method descriptor unavailable", map[string]any{"code": "METHOD_DESCRIPTOR_MISSING"})
	}
	role := normalizedConnectRole(principal.Role)
	if descriptor.Scope == protocol.MethodScopeNode {
		if role != "node" || !principal.Authenticated {
			return protocol.NewError(protocol.ErrorCodeInvalidRequest, "node role required", map[string]any{"code": "METHOD_NODE_ROLE_REQUIRED", "method": descriptor.Name})
		}
	} else {
		if role == "node" {
			return protocol.NewError(protocol.ErrorCodeInvalidRequest, "operator role required", map[string]any{"code": "METHOD_OPERATOR_ROLE_REQUIRED", "method": descriptor.Name})
		}
		if !principal.Authenticated && descriptor.Scope != protocol.MethodScopeOperatorRead {
			return protocol.NewError(protocol.ErrorCodeInvalidRequest, "authentication required", map[string]any{"code": "METHOD_AUTH_REQUIRED", "method": descriptor.Name})
		}
		if descriptor.Scope != protocol.MethodScopeDynamic && principal.ScopesEnforced && !scopeAllowed(descriptor.Scope, principal.Scopes) {
			return protocol.NewError(protocol.ErrorCodeInvalidRequest, "missing scope: "+descriptor.Scope, map[string]any{"code": "METHOD_SCOPE_REQUIRED", "method": descriptor.Name, "scope": descriptor.Scope})
		}
	}
	if descriptor.Startup == protocol.MethodStartupUnavailableUntilSidecars && r.opts.StartupReady != nil && !r.opts.StartupReady() {
		shape := protocol.NewError(protocol.ErrorCodeUnavailable, "gateway startup sidecars are not ready", map[string]any{"reason": "startup-sidecars", "method": descriptor.Name})
		shape.Retryable = true
		shape.RetryAfterMS = 1000
		return shape
	}
	if descriptor.ControlPlaneWrite && r.opts.ControlWriteLimitPerMin > 0 && !c.allowControlWrite(r.opts.ControlWriteLimitPerMin) {
		shape := protocol.NewError(protocol.ErrorCodeUnavailable, "control-plane write rate limit exceeded", map[string]any{"reason": "control-plane-write-flood", "method": descriptor.Name})
		shape.Retryable = true
		shape.RetryAfterMS = 1000
		return shape
	}
	return nil
}

func (c *client) allowControlWrite(limit int) bool {
	if c == nil || limit <= 0 {
		return true
	}
	now := time.Now()
	c.controlWriteMu.Lock()
	defer c.controlWriteMu.Unlock()
	window := c.controlWriteWindow
	if window.resetAt.IsZero() || now.After(window.resetAt) {
		window = rateWindow{resetAt: now.Add(time.Minute)}
	}
	if window.count >= limit {
		c.controlWriteWindow = window
		return false
	}
	window.count++
	c.controlWriteWindow = window
	return true
}

func normalizedConnectRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "node" {
		return role
	}
	return "operator"
}

func normalizedScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, raw := range scopes {
		scope := strings.ToLower(strings.TrimSpace(raw))
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

func defaultOperatorScopes() []string {
	return []string{
		protocol.MethodScopeOperatorAdmin,
		protocol.MethodScopeOperatorApprovals,
		protocol.MethodScopeOperatorPairing,
		protocol.MethodScopeOperatorQuestions,
		protocol.MethodScopeOperatorRead,
		protocol.MethodScopeOperatorWrite,
	}
}

func scopeAllowed(required string, scopes []string) bool {
	for _, scope := range scopes {
		if scope == required || (required == protocol.MethodScopeOperatorRead && scope == protocol.MethodScopeOperatorWrite) {
			return true
		}
	}
	return false
}

func nonEmpty(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func buildAllowedMethods(methods []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, method := range methods {
		m := strings.TrimSpace(method)
		if m == "" {
			continue
		}
		out[m] = struct{}{}
	}
	out[MethodEventsList] = struct{}{}
	out[MethodEventsSubscribe] = struct{}{}
	out[MethodEventsUnsubscribe] = struct{}{}
	out[MethodSessionsSubscribe] = struct{}{}
	out[MethodSessionsUnsubscribe] = struct{}{}
	out[MethodSessionsMessagesSubscribe] = struct{}{}
	out[MethodSessionsMessagesUnsubscribe] = struct{}{}
	return out
}

func (r *Runtime) isMethodAllowed(method string) bool {
	m := strings.TrimSpace(method)
	if m == "" {
		return false
	}
	allowed := r.allowedMethods
	if len(allowed) == 0 {
		allowed = buildAllowedMethods(r.opts.Methods)
	}
	_, ok := allowed[m]
	return ok
}

func validateOrigin(req *http.Request, allowedOrigins []string) error {
	origin := strings.TrimSpace(req.Header.Get("Origin"))
	if origin == "" {
		return nil
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("invalid origin")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	allow := map[string]struct{}{}
	for _, item := range allowedOrigins {
		v := strings.TrimSpace(item)
		if v != "" {
			allow[v] = struct{}{}
		}
	}
	if len(allow) == 0 {
		return fmt.Errorf("origin not allowed")
	}
	if _, ok := allow[origin]; ok {
		return nil
	}
	return fmt.Errorf("origin not allowed")
}

func hasDeviceIdentity(device *protocol.ConnectDevice) bool {
	if device == nil {
		return false
	}
	return strings.TrimSpace(device.ID) != "" && strings.TrimSpace(device.PublicKey) != "" && strings.TrimSpace(device.Signature) != ""
}

func isTrustedProxyRemote(remoteIP string, trustedProxies []string) bool {
	ip := net.ParseIP(strings.TrimSpace(remoteIP))
	if ip == nil {
		return false
	}
	for _, proxy := range trustedProxies {
		p := strings.TrimSpace(proxy)
		if p == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(p); err == nil {
			if cidr != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		if pip := net.ParseIP(p); pip != nil && pip.Equal(ip) {
			return true
		}
	}
	return false
}

func isLoopbackRemote(remoteIP string) bool {
	ip := net.ParseIP(strings.TrimSpace(remoteIP))
	return ip != nil && ip.IsLoopback()
}

func isLocalOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func availableEventSubset(wanted, available []string) []string {
	allow := make(map[string]struct{}, len(available))
	for _, event := range available {
		allow[strings.TrimSpace(event)] = struct{}{}
	}
	out := make([]string, 0, len(wanted))
	for _, event := range wanted {
		if _, ok := allow[event]; ok {
			out = append(out, event)
		}
	}
	return out
}

func normalizeEventList(events []string, allowed []string) ([]string, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("events are required")
	}
	allow := map[string]struct{}{}
	for _, event := range allowed {
		e := strings.TrimSpace(event)
		if e != "" {
			allow[e] = struct{}{}
		}
	}
	out := make([]string, 0, len(events))
	seen := map[string]struct{}{}
	for _, event := range events {
		e := strings.TrimSpace(event)
		if e == "" {
			continue
		}
		if _, ok := allow[e]; !ok {
			return nil, fmt.Errorf("unsupported event %q", e)
		}
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("events are required")
	}
	return out, nil
}

func (c *client) isSubscribed(event string) bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	_, ok := c.subscriptions[event]
	return ok
}

func (c *client) addSubscriptions(events []string) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for _, event := range events {
		c.subscriptions[event] = struct{}{}
	}
}

func (c *client) removeSubscriptions(events []string) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for _, event := range events {
		delete(c.subscriptions, event)
	}
}

func (c *client) listSubscriptions() []string {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	out := make([]string, 0, len(c.subscriptions))
	for event := range c.subscriptions {
		out = append(out, event)
	}
	return out
}

func containsSessionMessageEvent(events []string) bool {
	for _, event := range events {
		if isSessionMessageEvent(event) {
			return true
		}
	}
	return false
}

func isSessionMessageEvent(event string) bool {
	return event == EventChat || event == EventChatMessage
}

func (c *client) setAllSessionMessages(enabled bool) {
	c.subMu.Lock()
	c.allSessionMessages = enabled
	c.subMu.Unlock()
}

func (c *client) hasAllSessionMessages() bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	return c.allSessionMessages
}

func (c *client) addWatchedSession(key string) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	if c.watchedSessions == nil {
		c.watchedSessions = map[string]struct{}{}
	}
	c.watchedSessions[strings.TrimSpace(key)] = struct{}{}
}

func (c *client) removeWatchedSession(key string) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	delete(c.watchedSessions, strings.TrimSpace(key))
}

func (c *client) hasWatchedSessions() bool {
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	return len(c.watchedSessions) > 0
}

func (c *client) acceptsSessionMessagePayload(payload any) bool {
	key := sessionMessagePayloadKey(payload)
	c.subMu.RLock()
	defer c.subMu.RUnlock()
	if c.allSessionMessages || len(c.watchedSessions) == 0 {
		return true
	}
	_, ok := c.watchedSessions[key]
	return ok
}

func sessionMessagePayloadKey(payload any) string {
	switch p := payload.(type) {
	case ChatStatusEvent:
		return p.SessionKey
	case ChatDeltaEvent:
		return p.SessionKey
	case ChatFinalEvent:
		return p.SessionKey
	case ChatAbortedEvent:
		return p.SessionKey
	case ChatErrorEvent:
		return p.SessionKey
	case ChatMessagePayload:
		return p.SessionID
	default:
		return ""
	}
}

func (c *client) bumpUnauthorized(shape *protocol.ErrorShape) {
	if shape == nil {
		return
	}
	code := strings.TrimSpace(shape.Code)
	if strings.EqualFold(code, protocol.ErrorCodeNotLinked) || strings.EqualFold(code, protocol.ErrorCodeNotPaired) || strings.Contains(strings.ToLower(shape.Message), "forbidden") || strings.Contains(strings.ToLower(shape.Message), "unauthorized") {
		c.authMu.Lock()
		c.unauthorized++
		c.authMu.Unlock()
	}
}

func (c *client) resetUnauthorized() {
	c.authMu.Lock()
	c.unauthorized = 0
	c.authMu.Unlock()
}

func (c *client) shouldClose(limit int) bool {
	if limit <= 0 {
		return false
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()
	return c.unauthorized >= limit
}
