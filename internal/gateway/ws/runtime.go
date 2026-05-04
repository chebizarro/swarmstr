package ws

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"metiq/internal/gateway/protocol"
	"metiq/internal/policy"
)

const (
	MethodEventsSubscribe   = "events.subscribe"
	MethodEventsUnsubscribe = "events.unsubscribe"
	MethodEventsList        = "events.list"
)

type RequestHandler func(context.Context, protocol.RequestFrame) (any, *protocol.ErrorShape)

type RuntimeOptions struct {
	Addr                    string
	Path                    string
	Token                   string
	Methods                 []string
	Events                  []string
	Version                 string
	HandshakeTTL            time.Duration
	MaxPayloadSize          int64
	AuthRateLimitPerMin     int
	UnauthorizedBurstMax    int
	AllowedOrigins          []string
	TrustedProxies          []string
	AllowInsecureControlUI  bool
	DeviceAuthSignatureSkew time.Duration
	HandleRequest           RequestHandler
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
	seq     int64

	rateMu         sync.Mutex
	rateState      map[string]rateWindow
	allowedMethods map[string]struct{}
}

type client struct {
	id        string
	conn      *websocket.Conn
	connected protocol.ConnectParams

	subMu         sync.RWMutex
	subscriptions map[string]struct{}

	authMu       sync.Mutex
	unauthorized int
}

type rateWindow struct {
	count   int
	resetAt time.Time
}

type eventSubscriptionRequest struct {
	Events []string `json:"events"`
}

func Start(ctx context.Context, opts RuntimeOptions) (*Runtime, error) {
	if strings.TrimSpace(opts.Addr) == "" {
		return nil, nil
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
	if opts.DeviceAuthSignatureSkew <= 0 {
		opts.DeviceAuthSignatureSkew = 2 * time.Minute
	}

	if err := validateExposure(opts.Addr, opts.Token); err != nil {
		return nil, err
	}

	r := &Runtime{
		opts:           opts,
		clients:        map[string]*client{},
		rateState:      map[string]rateWindow{},
		allowedMethods: buildAllowedMethods(opts.Methods),
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
	r.mu.RLock()
	clients := make([]*client, 0, len(r.clients))
	for _, c := range r.clients {
		clients = append(clients, c)
	}
	r.mu.RUnlock()

	emit := func(name string) {
		for _, c := range clients {
			if !c.isSubscribed(name) {
				continue
			}
			seq := atomic.AddInt64(&r.seq, 1)
			_ = writeFrame(context.Background(), c.conn, map[string]any{
				"type":    protocol.FrameTypeEvent,
				"event":   name,
				"seq":     seq,
				"payload": payload,
			})
		}
	}

	emit(event)
	for _, proj := range compatibilityEventProjections(event, payload) {
		emitWithPayload := proj.Payload
		emitEvent := proj.Event
		for _, c := range clients {
			if !c.isSubscribed(emitEvent) {
				continue
			}
			seq := atomic.AddInt64(&r.seq, 1)
			_ = writeFrame(context.Background(), c.conn, map[string]any{
				"type":    protocol.FrameTypeEvent,
				"event":   emitEvent,
				"seq":     seq,
				"payload": emitWithPayload,
			})
		}
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
		_ = writeFrame(req.Context(), conn, map[string]any{
			"type": protocol.FrameTypeResponse,
			"id":   reqFrame.ID,
			"ok":   false,
			"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, "protocol mismatch", map[string]any{
				"supported_min": protocol.MinProtocolVersion,
				"supported_max": protocol.CurrentProtocolVersion,
			}),
		})
		_ = conn.Close(websocket.StatusPolicyViolation, "protocol mismatch")
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
		id:            connID,
		conn:          conn,
		connected:     connect,
		subscriptions: map[string]struct{}{},
	}
	r.mu.Lock()
	r.clients[c.id] = c
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.clients, c.id)
		r.mu.Unlock()
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
		Features: protocol.FeatureSet{Methods: append([]string{}, r.opts.Methods...), Events: append([]string{}, r.opts.Events...)},
		Snapshot: r.snapshot(),
		Policy: protocol.HelloPolicy{
			MaxPayload:       int(r.opts.MaxPayloadSize),
			MaxBufferedBytes: int(r.opts.MaxPayloadSize),
			TickIntervalMS:   1000,
		},
	}

	_ = writeFrame(req.Context(), conn, map[string]any{
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
			_ = writeFrame(req.Context(), conn, map[string]any{
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
			_ = writeFrame(req.Context(), conn, map[string]any{
				"type":  protocol.FrameTypeResponse,
				"id":    reqFrame.ID,
				"ok":    false,
				"error": protocol.NewError(protocol.ErrorCodeInvalidRequest, fmt.Sprintf("unknown method %q", strings.TrimSpace(reqFrame.Method)), nil),
			})
			continue
		}
		if handled, payload, shape := r.handleInternalRequest(c, reqFrame); handled {
			if shape != nil {
				_ = writeFrame(req.Context(), conn, map[string]any{"type": protocol.FrameTypeResponse, "id": reqFrame.ID, "ok": false, "error": shape})
			} else {
				_ = writeFrame(req.Context(), conn, map[string]any{"type": protocol.FrameTypeResponse, "id": reqFrame.ID, "ok": true, "payload": payload})
			}
			continue
		}
		if r.opts.HandleRequest == nil {
			_ = writeFrame(req.Context(), conn, map[string]any{
				"type":  protocol.FrameTypeResponse,
				"id":    reqFrame.ID,
				"ok":    false,
				"error": protocol.NewError(protocol.ErrorCodeUnavailable, "no request handler configured", nil),
			})
			continue
		}
		reqCtx := contextWithControlPrincipal(req.Context(), principal)
		payload, shape := r.opts.HandleRequest(reqCtx, reqFrame)
		if shape != nil {
			c.bumpUnauthorized(shape)
			_ = writeFrame(req.Context(), conn, map[string]any{"type": protocol.FrameTypeResponse, "id": reqFrame.ID, "ok": false, "error": shape})
			if c.shouldClose(r.opts.UnauthorizedBurstMax) {
				_ = conn.Close(websocket.StatusPolicyViolation, "repeated unauthorized requests")
				return
			}
			continue
		}
		c.resetUnauthorized()
		_ = writeFrame(req.Context(), conn, map[string]any{"type": protocol.FrameTypeResponse, "id": reqFrame.ID, "ok": true, "payload": payload})
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
	r.Broadcast("presence.updated", map[string]any{"presence": r.snapshot().Presence})
}

type authDecision struct {
	OK     bool
	Method string
	Reason string
	Code   string
}

type ControlPrincipal struct {
	Authenticated bool
	PubKey        string
	Subject       string
	Method        string
}

type controlPrincipalContextKey struct{}

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

func (r *Runtime) evaluateAuth(req *http.Request, connect protocol.ConnectParams) authDecision {
	token := ""
	if connect.Auth != nil {
		token = strings.TrimSpace(connect.Auth.Token)
	}
	configuredToken := strings.TrimSpace(r.opts.Token)
	if configuredToken != "" {
		if r.isTrustedProxyAuth(req) {
			return authDecision{OK: true, Method: "trusted-proxy"}
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(configuredToken)) == 1 {
			return authDecision{OK: true, Method: "token"}
		}
		if token == "" {
			return authDecision{Reason: "token_missing", Code: "AUTH_TOKEN_MISSING"}
		}
		return authDecision{Reason: "token_mismatch", Code: "AUTH_TOKEN_MISMATCH"}
	}
	if r.isTrustedProxyAuth(req) {
		return authDecision{OK: true, Method: "trusted-proxy"}
	}
	return authDecision{OK: true, Method: "none"}
}

func (r *Runtime) controlPrincipal(req *http.Request, connect protocol.ConnectParams, auth authDecision) ControlPrincipal {
	principal := ControlPrincipal{Authenticated: auth.OK, Method: auth.Method}
	if auth.Method == "trusted-proxy" {
		user := strings.TrimSpace(req.Header.Get("X-Metiq-Proxy-User"))
		principal.PubKey = strings.ToLower(user)
		principal.Subject = user
		return principal
	}
	if hasNostrAuthorizationHeader(req) {
		if nip98 := policy.AuthenticateControlCall(req, nil, r.opts.HandshakeTTL); nip98.Authenticated {
			principal.PubKey = strings.ToLower(strings.TrimSpace(nip98.CallerPubKey))
			principal.Subject = principal.PubKey
			principal.Method = "nip98"
			return principal
		}
	}
	if hasDeviceIdentity(connect.Device) {
		principal.Subject = strings.TrimSpace(connect.Device.ID)
	}
	return principal
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
	token := ""
	if connect.Auth != nil {
		token = strings.TrimSpace(connect.Auth.Token)
		if token == "" {
			token = strings.TrimSpace(connect.Auth.DeviceToken)
		}
	}
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

func (c *client) bumpUnauthorized(shape *protocol.ErrorShape) {
	if shape == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(shape.Code), protocol.ErrorCodeNotLinked) || strings.Contains(strings.ToLower(shape.Message), "forbidden") || strings.Contains(strings.ToLower(shape.Message), "unauthorized") {
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
