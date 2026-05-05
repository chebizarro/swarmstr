package ws

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"

	nostr "fiatjaf.com/nostr"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"metiq/internal/gateway/protocol"
)

func TestHandshakeConnectSuccess(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 3,
		},
		clients:   map[string]*client{},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, srv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	writeConnect(t, ctx, conn, "secret", nonce)

	res := readUntilResponse(t, ctx, conn)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("expected ok response, got %#v", res)
	}
	helloPayload, _ := res["payload"].(map[string]any)
	if helloPayload["type"] != "hello-ok" {
		t.Fatalf("expected hello-ok payload, got %#v", helloPayload)
	}
}

func TestHandshakeRejectsInvalidToken(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:               "secret",
			Version:             "test",
			HandshakeTTL:        2 * time.Second,
			MaxPayloadSize:      1 << 20,
			AuthRateLimitPerMin: 20,
		},
		clients:   map[string]*client{},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, srv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	writeConnect(t, ctx, conn, "wrong", nonce)

	res := readUntilResponse(t, ctx, conn)
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("expected auth failure, got %#v", res)
	}
}

func TestTrustedProxyAuthAllowsMissingToken(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 3,
			TrustedProxies:       []string{"127.0.0.1"},
		},
		clients:   map[string]*client{},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	headers := http.Header{}
	headers.Set("X-Metiq-Trusted-Auth", "true")
	headers.Set("X-Metiq-Proxy-User", "proxy-user")
	conn := dialWSWithHeaders(t, ctx, srv.URL, headers)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	writeConnect(t, ctx, conn, "", nonce)

	res := readUntilResponse(t, ctx, conn)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("expected trusted-proxy auth success, got %#v", res)
	}
}

func TestEvaluateAuthTrustedProxyOverridesTokenOutcomes(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{Token: "secret", TrustedProxies: []string{"10.0.0.0/8"}}}
	connect := protocol.ConnectParams{Auth: &protocol.ConnectAuth{Token: "wrong"}}
	req := httptest.NewRequest(http.MethodGet, "http://example/ws", nil)
	req.RemoteAddr = "10.1.2.3:1234"
	req.Header.Set("X-Metiq-Trusted-Auth", "true")
	req.Header.Set("X-Metiq-Proxy-User", "proxy-user")

	decision := r.evaluateAuth(req, connect)
	if !decision.OK || decision.Method != "trusted-proxy" {
		t.Fatalf("unexpected auth decision: %+v", decision)
	}

	connect.Auth.Token = ""
	decision = r.evaluateAuth(req, connect)
	if !decision.OK || decision.Method != "trusted-proxy" {
		t.Fatalf("unexpected auth decision for missing token: %+v", decision)
	}
}

func TestEvaluateAuthRejectsSpoofedProxyHeadersFromUntrustedRemote(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{Token: "secret", TrustedProxies: []string{"10.0.0.0/8"}}}
	connect := protocol.ConnectParams{Auth: &protocol.ConnectAuth{Token: ""}}
	req := httptest.NewRequest(http.MethodGet, "http://example/ws", nil)
	req.RemoteAddr = "192.168.1.15:9999"
	req.Header.Set("X-Metiq-Trusted-Auth", "true")
	req.Header.Set("X-Metiq-Proxy-User", "proxy-user")

	decision := r.evaluateAuth(req, connect)
	if decision.OK || decision.Code != "AUTH_TOKEN_MISSING" {
		t.Fatalf("unexpected auth decision: %+v", decision)
	}
}

func TestEvaluateAuthAcceptsDeviceTokenAndPasswordAliases(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{Token: "secret"}}
	req := httptest.NewRequest(http.MethodGet, "http://example/ws", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	for name, auth := range map[string]*protocol.ConnectAuth{
		"deviceToken": {DeviceToken: "secret"},
		"password":    {Password: "secret"},
	} {
		decision := r.evaluateAuth(req, protocol.ConnectParams{Auth: auth})
		if !decision.OK || decision.Method != "token" {
			t.Fatalf("%s alias did not authenticate: %+v", name, decision)
		}
	}
}

func TestControlPrincipalDoesNotAuthenticateNoneMethod(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{HandshakeTTL: 2 * time.Second}}
	req := httptest.NewRequest(http.MethodGet, "http://example/ws", nil)
	principal := r.controlPrincipal(req, protocol.ConnectParams{}, authDecision{OK: true, Method: "none"})
	if principal.Authenticated || principal.Method != "none" {
		t.Fatalf("none auth should not create authenticated principal: %+v", principal)
	}
}

func TestControlPrincipalRestoresNIP98AuthenticationWhenTokenless(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{HandshakeTTL: 2 * time.Second}}
	req := httptest.NewRequest(http.MethodGet, "http://example/ws", nil)
	hash := sha256.Sum256(nil)
	var secret nostr.SecretKey
	secret[31] = 1
	evt := nostr.Event{
		Kind:      nostr.Kind(27235),
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"method", http.MethodGet},
			{"u", "http://example/ws"},
			{"payload", nostr.HexEncodeToString(hash[:])},
		},
	}
	if err := evt.Sign(secret); err != nil {
		t.Fatalf("sign nip98 event: %v", err)
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal nip98 event: %v", err)
	}
	req.Header.Set("Authorization", "Nostr "+base64.StdEncoding.EncodeToString(raw))
	principal := r.controlPrincipal(req, protocol.ConnectParams{}, authDecision{OK: true, Method: "none"})
	if !principal.Authenticated || principal.Method != "nip98" || principal.PubKey == "" {
		t.Fatalf("expected authenticated nip98 principal, got %+v", principal)
	}
}

func TestControlPrincipalUsesDeviceIDAsPolicyIdentity(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{HandshakeTTL: 2 * time.Second}}
	req := httptest.NewRequest(http.MethodGet, "http://example/ws", nil)
	deviceID := "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
	principal := r.controlPrincipal(req, protocol.ConnectParams{Device: &protocol.ConnectDevice{ID: deviceID, PublicKey: "pub", Signature: "sig"}}, authDecision{OK: true, Method: "token"})
	want := strings.ToLower(deviceID)
	if !principal.Authenticated || principal.Method != "device" || principal.PubKey != want || principal.Subject != want {
		t.Fatalf("expected signed device identity to drive policy principal, got %+v", principal)
	}
}

func TestBumpUnauthorizedCountsNotPaired(t *testing.T) {
	c := &client{}
	c.bumpUnauthorized(protocol.NewError(protocol.ErrorCodeNotPaired, "not paired", nil))
	if !c.shouldClose(1) {
		t.Fatal("expected NOT_PAIRED to count toward unauthorized burst")
	}
}

func TestEvaluateAuthRejectsTrustedProxyWithoutProxyUser(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{Token: "secret", TrustedProxies: []string{"10.0.0.0/8"}}}
	connect := protocol.ConnectParams{Auth: &protocol.ConnectAuth{Token: "wrong"}}
	req := httptest.NewRequest(http.MethodGet, "http://example/ws", nil)
	req.RemoteAddr = "10.1.2.3:1234"
	req.Header.Set("X-Metiq-Trusted-Auth", "true")

	decision := r.evaluateAuth(req, connect)
	if decision.OK || decision.Code != "AUTH_TOKEN_MISMATCH" {
		t.Fatalf("unexpected auth decision: %+v", decision)
	}
}

func TestControlPrincipalPrefersTrustedProxyOverNIP98Header(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{HandshakeTTL: 2 * time.Second}}
	req := httptest.NewRequest(http.MethodGet, "http://example/ws", nil)
	req.Header.Set("Authorization", "Nostr something")
	req.Header.Set("X-Metiq-Proxy-User", "Proxy-User")
	principal := r.controlPrincipal(req, protocol.ConnectParams{}, authDecision{OK: true, Method: "trusted-proxy"})
	if !principal.Authenticated || principal.Method != "trusted-proxy" || principal.PubKey != "proxy-user" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
}

func TestTrustedProxyPrincipalPropagatesToRequestContext(t *testing.T) {
	principalCh := make(chan ControlPrincipal, 1)
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get"},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 3,
			TrustedProxies:       []string{"127.0.0.1"},
			HandleRequest: func(ctx context.Context, _ protocol.RequestFrame) (any, *protocol.ErrorShape) {
				principal, ok := PrincipalFromContext(ctx)
				if !ok {
					return nil, protocol.NewError(protocol.ErrorCodeInvalidRequest, "missing principal", nil)
				}
				principalCh <- principal
				return map[string]any{"ok": true}, nil
			},
		},
		clients:        map[string]*client{},
		rateState:      map[string]rateWindow{},
		allowedMethods: buildAllowedMethods([]string{"status.get"}),
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	headers := http.Header{}
	headers.Set("X-Metiq-Trusted-Auth", "true")
	headers.Set("X-Metiq-Proxy-User", "operator-pubkey")
	conn := dialWSWithHeaders(t, ctx, srv.URL, headers)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	writeConnect(t, ctx, conn, "wrong", nonce)
	if res := readUntilResponse(t, ctx, conn); res["ok"] != true {
		t.Fatalf("handshake failed: %#v", res)
	}
	writeJSON(t, ctx, conn, map[string]any{"type": protocol.FrameTypeRequest, "id": "status", "method": "status.get"})
	if res := readUntilResponse(t, ctx, conn); res["ok"] != true {
		t.Fatalf("request failed: %#v", res)
	}

	select {
	case principal := <-principalCh:
		if !principal.Authenticated || principal.PubKey != "operator-pubkey" || principal.Method != "trusted-proxy" {
			t.Fatalf("unexpected principal: %+v", principal)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for principal")
	}
}

func TestNodeRoleRequiresDeviceIdentity(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 3,
		},
		clients:   map[string]*client{},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, srv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	writeConnectCustom(t, ctx, conn, map[string]any{
		"minProtocol": 1,
		"maxProtocol": 3,
		"client":      map[string]any{"id": "metiq-cli", "version": "0.1.0", "platform": "darwin", "mode": "local"},
		"role":        "node",
		"auth":        map[string]any{"token": "secret", "nonce": nonce},
	})
	res := readUntilResponse(t, ctx, conn)
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("expected node device-identity requirement failure, got %#v", res)
	}
}

func TestNodeRoleAcceptsValidDeviceSignature(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 3,
		},
		clients:   map[string]*client{},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, srv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	params := baseNodeConnectParams(nonce)
	params["device"] = signedTestDevice(t, params, "node", []string{}, "secret", nonce)
	writeConnectCustom(t, ctx, conn, params)

	res := readUntilResponse(t, ctx, conn)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("expected valid signed device to pass, got %#v", res)
	}
}

func TestNodeRoleRejectsInvalidDeviceSignature(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 3,
		},
		clients:   map[string]*client{},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, srv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	params := baseNodeConnectParams(nonce)
	device := signedTestDevice(t, params, "node", []string{}, "secret", nonce)
	device["signature"] = "invalid"
	params["device"] = device
	writeConnectCustom(t, ctx, conn, params)

	res := readUntilResponse(t, ctx, conn)
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("expected invalid signature rejection, got %#v", res)
	}
	errMap, _ := res["error"].(map[string]any)
	details, _ := errMap["details"].(map[string]any)
	if got, _ := details["reason"].(string); got != "device-signature" {
		t.Fatalf("expected device-signature reason, got %#v", details)
	}
}

func TestNodeRoleRejectsDeviceIDMismatch(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 3,
		},
		clients:   map[string]*client{},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, srv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	params := baseNodeConnectParams(nonce)
	device := signedTestDevice(t, params, "node", []string{}, "secret", nonce)
	device["id"] = "bad-device-id"
	params["device"] = device
	writeConnectCustom(t, ctx, conn, params)

	res := readUntilResponse(t, ctx, conn)
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("expected device id mismatch rejection, got %#v", res)
	}
	errMap, _ := res["error"].(map[string]any)
	details, _ := errMap["details"].(map[string]any)
	if got, _ := details["reason"].(string); got != "device-id-mismatch" {
		t.Fatalf("expected device-id-mismatch reason, got %#v", details)
	}
}

func TestControlUIRemoteRequiresDeviceIdentityUnlessAllowed(t *testing.T) {
	newRuntime := func(allow bool) *Runtime {
		return &Runtime{opts: RuntimeOptions{
			Token:                  "secret",
			Methods:                []string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList},
			Events:                 []string{"presence.updated"},
			Version:                "test",
			HandshakeTTL:           2 * time.Second,
			MaxPayloadSize:         1 << 20,
			AuthRateLimitPerMin:    20,
			UnauthorizedBurstMax:   3,
			AllowedOrigins:         []string{"https://app.example.com"},
			AllowInsecureControlUI: allow,
		}, clients: map[string]*client{}, rateState: map[string]rateWindow{}}
	}

	headers := http.Header{}
	headers.Set("Origin", "https://app.example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r1 := newRuntime(false)
	s1 := httptest.NewServer(http.HandlerFunc(r1.handleWS))
	defer s1.Close()
	c1 := dialWSWithHeaders(t, ctx, s1.URL, headers)
	defer c1.Close(websocket.StatusNormalClosure, "done")
	nonce := readChallengeNonce(t, ctx, c1)
	writeConnectCustom(t, ctx, c1, map[string]any{
		"minProtocol": 1,
		"maxProtocol": 3,
		"client":      map[string]any{"id": "control-ui", "version": "0.1.0", "platform": "darwin", "mode": "local"},
		"role":        "operator",
		"auth":        map[string]any{"token": "secret", "nonce": nonce},
	})
	res := readUntilResponse(t, ctx, c1)
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("expected control-ui remote device requirement failure, got %#v", res)
	}

	r2 := newRuntime(true)
	s2 := httptest.NewServer(http.HandlerFunc(r2.handleWS))
	defer s2.Close()
	c2 := dialWSWithHeaders(t, ctx, s2.URL, headers)
	defer c2.Close(websocket.StatusNormalClosure, "done")
	nonce2 := readChallengeNonce(t, ctx, c2)
	writeConnectCustom(t, ctx, c2, map[string]any{
		"minProtocol": 1,
		"maxProtocol": 3,
		"client":      map[string]any{"id": "control-ui", "version": "0.1.0", "platform": "darwin", "mode": "local"},
		"role":        "operator",
		"auth":        map[string]any{"token": "secret", "nonce": nonce2},
	})
	res2 := readUntilResponse(t, ctx, c2)
	if ok, _ := res2["ok"].(bool); !ok {
		t.Fatalf("expected control-ui allow_insecure success, got %#v", res2)
	}
}

func TestEventSubscriptionControlsBroadcast(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 3,
			HandleRequest: func(context.Context, protocol.RequestFrame) (any, *protocol.ErrorShape) {
				return map[string]any{"ok": true}, nil
			},
		},
		clients:   map[string]*client{},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, srv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	writeConnect(t, ctx, conn, "secret", nonce)
	_ = readUntilResponse(t, ctx, conn)

	writeJSON(t, ctx, conn, map[string]any{
		"type":   protocol.FrameTypeRequest,
		"id":     "list-1",
		"method": MethodEventsList,
	})
	listRes := readUntilResponse(t, ctx, conn)
	payloadList, _ := listRes["payload"].(map[string]any)
	if events, _ := payloadList["events"].([]any); len(events) != 0 {
		t.Fatalf("expected no subscriptions before subscribe, got %#v", payloadList)
	}

	writeJSON(t, ctx, conn, map[string]any{
		"type":   protocol.FrameTypeRequest,
		"id":     "sub-1",
		"method": MethodEventsSubscribe,
		"params": map[string]any{"events": []string{"presence.updated"}},
	})
	res := readUntilResponse(t, ctx, conn)
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("subscribe failed: %#v", res)
	}

	r.Broadcast("presence.updated", map[string]any{"k": "v2"})
	frame := readUntilEvent(t, ctx, conn, "presence.updated")
	if frame == nil {
		t.Fatal("expected subscribed event")
	}
}

func TestBroadcastDropsFullClientQueueWithoutBlockingFanout(t *testing.T) {
	r := &Runtime{
		opts:    RuntimeOptions{EventBufferSize: 1, WriteTimeout: 10 * time.Millisecond},
		clients: map[string]*client{},
	}
	slow := &client{
		id:            "slow",
		subscriptions: map[string]struct{}{"presence.updated": {}},
		eventQueue:    make(chan any, 1),
		eventDone:     make(chan struct{}),
	}
	slow.eventQueue <- map[string]any{"queued": true}
	fast := &client{
		id:            "fast",
		subscriptions: map[string]struct{}{"presence.updated": {}},
		eventQueue:    make(chan any, 1),
		eventDone:     make(chan struct{}),
	}
	r.clients[slow.id] = slow
	r.clients[fast.id] = fast

	done := make(chan struct{})
	go func() {
		r.Broadcast("presence.updated", map[string]any{"k": "v"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("broadcast blocked behind a full client event queue")
	}

	select {
	case raw := <-fast.eventQueue:
		frame, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("unexpected fast frame type: %T", raw)
		}
		if frame["type"] != protocol.FrameTypeEvent || frame["event"] != "presence.updated" {
			t.Fatalf("unexpected fast frame: %#v", frame)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("fast client did not receive broadcast after slow client queue filled")
	}

	r.mu.RLock()
	_, slowPresent := r.clients[slow.id]
	_, fastPresent := r.clients[fast.id]
	r.mu.RUnlock()
	if slowPresent {
		t.Fatal("slow client should be dropped when its bounded event queue fills")
	}
	if !fastPresent {
		t.Fatal("fast client should remain connected")
	}
	select {
	case <-slow.eventDone:
	default:
		t.Fatal("slow client event writer should be closed after drop")
	}
}

func TestUnauthorizedBurstClosesConnection(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get"},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 2,
			HandleRequest: func(context.Context, protocol.RequestFrame) (any, *protocol.ErrorShape) {
				return nil, protocol.NewError(protocol.ErrorCodeNotLinked, "forbidden", nil)
			},
		},
		clients:   map[string]*client{},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, srv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	writeConnect(t, ctx, conn, "secret", nonce)
	_ = readUntilResponse(t, ctx, conn)

	writeJSON(t, ctx, conn, map[string]any{"type": protocol.FrameTypeRequest, "id": "a", "method": "status.get"})
	_ = readUntilResponse(t, ctx, conn)
	writeJSON(t, ctx, conn, map[string]any{"type": protocol.FrameTypeRequest, "id": "b", "method": "status.get"})
	_ = readUntilResponse(t, ctx, conn)

	readCtx, readCancel := context.WithTimeout(context.Background(), time.Second)
	defer readCancel()
	_, _, err := conn.Read(readCtx)
	if err == nil {
		t.Fatal("expected connection close after unauthorized burst")
	}
}

func TestUnknownMethodRejected(t *testing.T) {
	r := &Runtime{
		opts: RuntimeOptions{
			Token:                "secret",
			Methods:              []string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList},
			Events:               []string{"presence.updated"},
			Version:              "test",
			HandshakeTTL:         2 * time.Second,
			MaxPayloadSize:       1 << 20,
			AuthRateLimitPerMin:  20,
			UnauthorizedBurstMax: 3,
			HandleRequest: func(context.Context, protocol.RequestFrame) (any, *protocol.ErrorShape) {
				return map[string]any{"ok": true}, nil
			},
		},
		clients:        map[string]*client{},
		rateState:      map[string]rateWindow{},
		allowedMethods: buildAllowedMethods([]string{"status.get", MethodEventsSubscribe, MethodEventsUnsubscribe, MethodEventsList}),
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialWS(t, ctx, srv.URL)
	defer conn.Close(websocket.StatusNormalClosure, "done")

	nonce := readChallengeNonce(t, ctx, conn)
	writeConnect(t, ctx, conn, "secret", nonce)
	_ = readUntilResponse(t, ctx, conn)

	writeJSON(t, ctx, conn, map[string]any{"type": protocol.FrameTypeRequest, "id": "x1", "method": "unknown.method"})
	res := readUntilResponse(t, ctx, conn)
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("expected unknown method failure, got %#v", res)
	}
}

func TestValidateOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://localhost/ws", nil)
	req.Header.Set("Origin", "https://example.com")
	if err := validateOrigin(req, nil); err == nil {
		t.Fatal("expected origin rejection without allowlist")
	}
	if err := validateOrigin(req, []string{"https://example.com"}); err != nil {
		t.Fatalf("expected allowlisted origin to pass: %v", err)
	}
	localReq := httptest.NewRequest(http.MethodGet, "http://localhost/ws", nil)
	localReq.Header.Set("Origin", "http://localhost:5173")
	if err := validateOrigin(localReq, nil); err != nil {
		t.Fatalf("expected localhost origin to pass: %v", err)
	}
}

func TestValidateExposure(t *testing.T) {
	if err := validateExposure("127.0.0.1:9000", ""); err != nil {
		t.Fatalf("expected loopback without token to pass: %v", err)
	}
	if err := validateExposure("0.0.0.0:9000", "secret"); err != nil {
		t.Fatalf("expected tokenized non-loopback to pass: %v", err)
	}
	if err := validateExposure("0.0.0.0:9000", ""); err == nil {
		t.Fatal("expected non-loopback without token to fail")
	}
}

func TestAllowHandshakeRateLimit(t *testing.T) {
	r := &Runtime{opts: RuntimeOptions{AuthRateLimitPerMin: 2}, rateState: map[string]rateWindow{}}
	if !r.allowHandshake("1.2.3.4") || !r.allowHandshake("1.2.3.4") {
		t.Fatal("expected first two handshakes to pass")
	}
	if r.allowHandshake("1.2.3.4") {
		t.Fatal("expected third handshake to be rate-limited")
	}
}

func TestHandleWSRateLimitReturnsHTTP429(t *testing.T) {
	r := &Runtime{
		opts:      RuntimeOptions{AuthRateLimitPerMin: 2},
		rateState: map[string]rateWindow{},
	}
	srv := httptest.NewServer(http.HandlerFunc(r.handleWS))
	defer srv.Close()

	for i := 0; i < 2; i++ {
		res, err := http.Get(srv.URL)
		if err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("unexpected early rate limit on request %d", i+1)
		}
	}

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("third request failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusTooManyRequests)
	}
}

func dialWS(t *testing.T, ctx context.Context, baseURL string) *websocket.Conn {
	t.Helper()
	return dialWSWithHeaders(t, ctx, baseURL, nil)
}

func dialWSWithHeaders(t *testing.T, ctx context.Context, baseURL string, headers http.Header) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, strings.Replace(baseURL, "http", "ws", 1), &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func readChallengeNonce(t *testing.T, ctx context.Context, conn *websocket.Conn) string {
	t.Helper()
	challenge := readJSON(t, ctx, conn)
	if challenge["type"] != protocol.FrameTypeEvent || challenge["event"] != "connect.challenge" {
		t.Fatalf("unexpected challenge frame: %#v", challenge)
	}
	payload, _ := challenge["payload"].(map[string]any)
	nonce, _ := payload["nonce"].(string)
	if strings.TrimSpace(nonce) == "" {
		t.Fatalf("challenge nonce missing: %#v", challenge)
	}
	return nonce
}

func writeConnect(t *testing.T, ctx context.Context, conn *websocket.Conn, token string, nonce string) {
	t.Helper()
	writeConnectCustom(t, ctx, conn, map[string]any{
		"minProtocol": 1,
		"maxProtocol": 3,
		"client": map[string]any{
			"id":       "metiq-cli",
			"version":  "0.1.0",
			"platform": "darwin",
			"mode":     "local",
		},
		"auth": map[string]any{"token": token, "nonce": nonce},
	})
}

func baseNodeConnectParams(nonce string) map[string]any {
	return map[string]any{
		"minProtocol": 1,
		"maxProtocol": 3,
		"client": map[string]any{
			"id":       "metiq-cli",
			"version":  "0.1.0",
			"platform": "darwin",
			"mode":     "local",
		},
		"role": "node",
		"auth": map[string]any{"token": "secret", "nonce": nonce},
	}
}

func signedTestDevice(t *testing.T, connectParams map[string]any, role string, scopes []string, token string, nonce string) map[string]any {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	publicKey := base64.RawURLEncoding.EncodeToString(pub)
	hash := sha256.Sum256(pub)
	deviceID := hex.EncodeToString(hash[:])
	signedAt := time.Now().UnixMilli()
	clientMap, _ := connectParams["client"].(map[string]any)
	client := protocol.ConnectClient{
		ID:           stringValue(clientMap["id"]),
		Version:      stringValue(clientMap["version"]),
		Platform:     stringValue(clientMap["platform"]),
		Mode:         stringValue(clientMap["mode"]),
		DeviceFamily: stringValue(clientMap["deviceFamily"]),
	}
	connect := protocol.ConnectParams{
		Client: client,
		Scopes: scopes,
	}
	device := &protocol.ConnectDevice{
		ID:        deviceID,
		PublicKey: publicKey,
		SignedAt:  signedAt,
		Nonce:     nonce,
	}
	payload := buildDeviceAuthPayloadV3(device, connect, role, token)
	sig := ed25519.Sign(priv, []byte(payload))
	return map[string]any{
		"id":        deviceID,
		"publicKey": publicKey,
		"signature": base64.RawURLEncoding.EncodeToString(sig),
		"signedAt":  signedAt,
		"nonce":     nonce,
	}
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func writeConnectCustom(t *testing.T, ctx context.Context, conn *websocket.Conn, params map[string]any) {
	t.Helper()
	writeJSON(t, ctx, conn, map[string]any{
		"type":   protocol.FrameTypeRequest,
		"id":     "1",
		"method": "connect",
		"params": params,
	})
}

func readUntilResponse(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	for {
		out := readJSON(t, ctx, conn)
		if out["type"] == protocol.FrameTypeResponse {
			return out
		}
	}
}

func readUntilEvent(t *testing.T, ctx context.Context, conn *websocket.Conn, event string) map[string]any {
	t.Helper()
	for {
		out := readJSON(t, ctx, conn)
		if out["type"] == protocol.FrameTypeEvent && out["event"] == event {
			return out
		}
	}
}

func readJSON(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	return out
}

func writeJSON(t *testing.T, ctx context.Context, conn *websocket.Conn, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}
