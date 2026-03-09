package googlechat

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"swarmstr/internal/plugins/sdk"
)

// ── RSA key helper ────────────────────────────────────────────────────────────

func generateTestSA(t *testing.T) *serviceAccount {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	return &serviceAccount{
		ClientEmail: "test@project.iam.gserviceaccount.com",
		PrivateKey:  privPEM,
		TokenURI:    "https://oauth2.googleapis.com/token",
	}
}

// ── Plugin metadata ───────────────────────────────────────────────────────────

func TestGoogleChatPlugin_ID(t *testing.T) {
	p := &GoogleChatPlugin{}
	if p.ID() != "googlechat" {
		t.Fatalf("expected 'googlechat', got %q", p.ID())
	}
}

func TestGoogleChatPlugin_Capabilities(t *testing.T) {
	p := &GoogleChatPlugin{}
	caps := p.Capabilities()
	if !caps.Threads || !caps.MultiAccount {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestGoogleChatPlugin_ConfigSchema_Required(t *testing.T) {
	p := &GoogleChatPlugin{}
	schema := p.ConfigSchema()
	required, _ := schema["required"].([]string)
	set := map[string]bool{}
	for _, r := range required {
		set[r] = true
	}
	for _, f := range []string{"service_account_json", "space_name"} {
		if !set[f] {
			t.Fatalf("expected %q in required fields", f)
		}
	}
}

func TestGoogleChatPlugin_Connect_MissingServiceAccount(t *testing.T) {
	p := &GoogleChatPlugin{}
	_, err := p.Connect(context.Background(), "c1",
		map[string]any{"space_name": "spaces/X"},
		func(sdk.InboundChannelMessage) {})
	if err == nil {
		t.Fatal("expected error for missing service_account_json")
	}
}

// ── loadServiceAccount ────────────────────────────────────────────────────────

func TestLoadServiceAccount_InlineJSON(t *testing.T) {
	sa := generateTestSA(t)
	inline, _ := json.Marshal(sa)
	loaded, err := loadServiceAccount(string(inline))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.ClientEmail != sa.ClientEmail {
		t.Fatalf("unexpected client_email: %q", loaded.ClientEmail)
	}
}

func TestLoadServiceAccount_MissingPrivateKey(t *testing.T) {
	_, err := loadServiceAccount(`{"client_email":"a@b.com","private_key":""}`)
	if err == nil {
		t.Fatal("expected error for missing private_key")
	}
}

func TestLoadServiceAccount_DefaultTokenURI(t *testing.T) {
	sa := generateTestSA(t)
	sa.TokenURI = ""
	inline, _ := json.Marshal(sa)
	loaded, err := loadServiceAccount(string(inline))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.TokenURI != "https://oauth2.googleapis.com/token" {
		t.Fatalf("expected default token URI, got %q", loaded.TokenURI)
	}
}

// ── mintJWT ───────────────────────────────────────────────────────────────────

func TestMintJWT_ThreeParts(t *testing.T) {
	sa := generateTestSA(t)
	jwt, err := mintJWT(sa, chatScope, time.Now())
	if err != nil {
		t.Fatalf("mintJWT error: %v", err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3-part JWT, got %d", len(parts))
	}
	for i, p := range parts {
		if p == "" {
			t.Fatalf("JWT part %d is empty", i)
		}
	}
}

func TestMintJWT_PayloadClaims(t *testing.T) {
	sa := generateTestSA(t)
	jwt, err := mintJWT(sa, chatScope, time.Now())
	if err != nil {
		t.Fatalf("mintJWT error: %v", err)
	}
	parts := strings.Split(jwt, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if claims["iss"] != sa.ClientEmail {
		t.Fatalf("expected iss=%s, got %v", sa.ClientEmail, claims["iss"])
	}
	if claims["scope"] != chatScope {
		t.Fatalf("expected scope=%s, got %v", chatScope, claims["scope"])
	}
	if claims["aud"] != sa.TokenURI {
		t.Fatalf("expected aud=%s, got %v", sa.TokenURI, claims["aud"])
	}
}

func TestMintJWT_InvalidKey(t *testing.T) {
	sa := &serviceAccount{
		ClientEmail: "a@b.com",
		PrivateKey:  "not-a-pem-key",
		TokenURI:    "https://example.com/token",
	}
	_, err := mintJWT(sa, chatScope, time.Now())
	if err == nil {
		t.Fatal("expected error for invalid private key")
	}
}

// ── verifyInboundJWT ──────────────────────────────────────────────────────────

func TestVerifyInboundJWT_SkipFlag(t *testing.T) {
	bot := &gchatBot{skipJWTVerify: true}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if !bot.verifyInboundJWT(req) {
		t.Fatal("expected true when skipJWTVerify=true")
	}
}

func TestVerifyInboundJWT_MissingHeader(t *testing.T) {
	bot := &gchatBot{skipJWTVerify: false}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if bot.verifyInboundJWT(req) {
		t.Fatal("expected false for missing Authorization header")
	}
}

func TestVerifyInboundJWT_ValidIss(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"iss":"chat@system.gserviceaccount.com","exp":9999999999}`))
	token := "aaa." + payload + ".fakesig"
	bot := &gchatBot{skipJWTVerify: false}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if !bot.verifyInboundJWT(req) {
		t.Fatal("expected true for valid iss JWT")
	}
}

func TestVerifyInboundJWT_WrongIss(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"iss":"evil@attacker.com","exp":9999999999}`))
	token := "aaa." + payload + ".fakesig"
	bot := &gchatBot{skipJWTVerify: false}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if bot.verifyInboundJWT(req) {
		t.Fatal("expected false for wrong iss")
	}
}

func TestVerifyInboundJWT_Expired(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"iss":"chat@system.gserviceaccount.com","exp":1}`))
	token := "aaa." + payload + ".fakesig"
	bot := &gchatBot{skipJWTVerify: false}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if bot.verifyInboundJWT(req) {
		t.Fatal("expected false for expired token")
	}
}

// ── handlePush ────────────────────────────────────────────────────────────────

func newTestGChatBot(t *testing.T, allowedSenders ...string) (*gchatBot, *[]sdk.InboundChannelMessage) {
	var msgs []sdk.InboundChannelMessage
	allowed := map[string]bool{}
	for _, s := range allowedSenders {
		allowed[s] = true
	}
	bot := &gchatBot{
		channelID:      "gchat-test",
		spaceName:      "spaces/TEST",
		sa:             generateTestSA(t),
		allowedSenders: allowed,
		skipJWTVerify:  true,
		done:           make(chan struct{}),
		httpClient:     &http.Client{},
		onMessage:      func(m sdk.InboundChannelMessage) { msgs = append(msgs, m) },
	}
	return bot, &msgs
}

func postPushEvent(t *testing.T, bot *gchatBot, ev gchatPushEvent) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(ev)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	req.Header.Set("Authorization", "Bearer dummytoken")
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	return w
}

func makeMessageEvent(email, text string) gchatPushEvent {
	ev := gchatPushEvent{Type: "MESSAGE"}
	ev.Message = &struct {
		Name   string `json:"name"`
		Sender struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			Email       string `json:"email"`
		} `json:"sender"`
		Text   string `json:"text"`
		Thread *struct {
			Name string `json:"name"`
		} `json:"thread"`
	}{Name: "spaces/TEST/messages/m1", Text: text}
	ev.Message.Sender.Email = email
	return ev
}

func TestHandlePush_Delivers(t *testing.T) {
	bot, msgs := newTestGChatBot(t)
	w := postPushEvent(t, bot, makeMessageEvent("alice@example.com", "hello gchat"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(*msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(*msgs))
	}
	if (*msgs)[0].Text != "hello gchat" {
		t.Fatalf("unexpected text: %q", (*msgs)[0].Text)
	}
	if (*msgs)[0].SenderID != "alice@example.com" {
		t.Fatalf("unexpected senderID: %q", (*msgs)[0].SenderID)
	}
}

func TestHandlePush_SkipsNonMessage(t *testing.T) {
	bot, msgs := newTestGChatBot(t)
	postPushEvent(t, bot, gchatPushEvent{Type: "ADDED_TO_SPACE"})
	if len(*msgs) != 0 {
		t.Fatalf("expected 0 messages for non-MESSAGE event, got %d", len(*msgs))
	}
}

func TestHandlePush_EmptyText(t *testing.T) {
	bot, msgs := newTestGChatBot(t)
	postPushEvent(t, bot, makeMessageEvent("alice@example.com", "   "))
	if len(*msgs) != 0 {
		t.Fatal("expected empty text to be filtered")
	}
}

func TestHandlePush_AllowedSendersFilter(t *testing.T) {
	bot, msgs := newTestGChatBot(t, "allowed@example.com")
	postPushEvent(t, bot, makeMessageEvent("allowed@example.com", "hi"))
	postPushEvent(t, bot, makeMessageEvent("blocked@example.com", "no"))
	if len(*msgs) != 1 || (*msgs)[0].SenderID != "allowed@example.com" {
		t.Fatalf("expected only allowed sender, got %+v", *msgs)
	}
}

func TestHandlePush_MethodNotAllowed(t *testing.T) {
	bot, _ := newTestGChatBot(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandlePush_JWTMissing_Unauthorized(t *testing.T) {
	bot, _ := newTestGChatBot(t)
	bot.skipJWTVerify = false
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"type":"MESSAGE"}`))
	w := httptest.NewRecorder()
	bot.handlePush(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// ── Send / SendInThread via mock server ───────────────────────────────────────

type rewriteTransport struct {
	mockHost string
	inner    http.RoundTripper
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Host, "googleapis.com") || strings.Contains(req.URL.Host, "google") {
		req2 := req.Clone(req.Context())
		req2.URL = req.URL
		newURL := *req.URL
		newURL.Scheme = "http"
		newURL.Host = rt.mockHost
		req2.URL = &newURL
		req2.Host = rt.mockHost
		req = req2
	}
	if rt.inner != nil {
		return rt.inner.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func newMockAPIBot(t *testing.T, handler http.Handler) (*gchatBot, *httptest.Server) {
	srv := httptest.NewServer(handler)
	sa := generateTestSA(t)
	sa.TokenURI = srv.URL + "/token"
	bot := &gchatBot{
		channelID:  "gchat-test",
		spaceName:  "spaces/TEST",
		sa:         sa,
		done:       make(chan struct{}),
		httpClient: &http.Client{Transport: rewriteTransport{mockHost: srv.Listener.Addr().String(), inner: srv.Client().Transport}},
	}
	return bot, srv
}

func TestSend_PostsToMessages(t *testing.T) {
	var received map[string]any
	messageCalled := false
	bot, srv := newMockAPIBot(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
		case strings.Contains(r.URL.Path, "/messages"):
			messageCalled = true
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"spaces/TEST/messages/m1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := bot.Send(context.Background(), "hello gchat"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !messageCalled {
		t.Fatal("expected messages endpoint to be called")
	}
	if received["text"] != "hello gchat" {
		t.Fatalf("expected text='hello gchat', got %v", received["text"])
	}
}

func TestSendInThread_IncludesThreadName(t *testing.T) {
	var received map[string]any
	bot, srv := newMockAPIBot(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
		case strings.Contains(r.URL.Path, "/messages"):
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if err := bot.SendInThread(context.Background(), "spaces/TEST/threads/t1", "reply"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	thread, _ := received["thread"].(map[string]any)
	if thread["name"] != "spaces/TEST/threads/t1" {
		t.Fatalf("expected thread.name=spaces/TEST/threads/t1, got %v", thread["name"])
	}
}

// ── Webhook registry ──────────────────────────────────────────────────────────

func TestHandleWebhook_UnknownChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	HandleWebhook("no-such-channel-xyz", w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestRegisterAndHandleWebhook(t *testing.T) {
	bot, msgs := newTestGChatBot(t)
	registerWebhook("test-gchat-webhook", bot)
	defer func() {
		webhookMu.Lock()
		delete(webhookHandlers, "test-gchat-webhook")
		webhookMu.Unlock()
	}()

	ev := makeMessageEvent("alice@example.com", "webhook test")
	data, _ := json.Marshal(ev)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(data)))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	HandleWebhook("test-gchat-webhook", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(*msgs) != 1 || (*msgs)[0].Text != "webhook test" {
		t.Fatalf("unexpected messages: %+v", *msgs)
	}
}

// ── helper ────────────────────────────────────────────────────────────────────

