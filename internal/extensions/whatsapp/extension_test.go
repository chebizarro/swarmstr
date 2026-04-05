package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func signWebhook(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestHandleWebhook_UnknownChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp/unknown", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	HandleWebhook("unknown-channel-xyz", w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestRegisterAndHandleWebhook_DeliversSignedMatchingMessages(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &whatsappBot{
		channelID:     "wa-main",
		verifyToken:   "verify-me",
		phoneNumberID: "phone-123",
		appSecret:     "app-secret",
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done: make(chan struct{}),
	}
	registerWebhook("wa-main", bot)
	defer func() {
		webhookMu.Lock()
		delete(webhookHandlers, "wa-main")
		webhookMu.Unlock()
	}()

	payload := `{
		"object":"whatsapp_business_account",
		"entry":[{
			"changes":[{
				"value":{
					"metadata":{"phone_number_id":"phone-123"},
					"messages":[{
						"id":"wamid-1",
						"from":"15551234567",
						"timestamp":"1712300000",
						"type":"text",
						"text":{"body":"hello from webhook"}
					}]
				}
			}]
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp/wa-main", strings.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signWebhook(payload, "app-secret"))
	w := httptest.NewRecorder()
	HandleWebhook("wa-main", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 1 || delivered[0].SenderID != "15551234567" || delivered[0].Text != "hello from webhook" {
		t.Fatalf("unexpected delivered messages: %+v", delivered)
	}
}

func TestHandleWebhook_RejectsMissingOrInvalidSignature(t *testing.T) {
	bot := &whatsappBot{channelID: "wa-main", phoneNumberID: "phone-123", appSecret: "app-secret", done: make(chan struct{})}
	registerWebhook("wa-main", bot)
	defer func() {
		webhookMu.Lock()
		delete(webhookHandlers, "wa-main")
		webhookMu.Unlock()
	}()

	payload := `{"object":"whatsapp_business_account","entry":[]}`
	for _, sig := range []string{"", "sha256=deadbeef"} {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp/wa-main", strings.NewReader(payload))
		if sig != "" {
			req.Header.Set("X-Hub-Signature-256", sig)
		}
		w := httptest.NewRecorder()
		HandleWebhook("wa-main", w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("signature %q: expected 401, got %d", sig, w.Code)
		}
	}
}

func TestHandleWebhook_DropsMismatchedPhoneNumberID(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	bot := &whatsappBot{
		channelID:     "wa-main",
		phoneNumberID: "phone-123",
		appSecret:     "app-secret",
		onMessage: func(msg sdk.InboundChannelMessage) {
			delivered = append(delivered, msg)
		},
		done: make(chan struct{}),
	}
	registerWebhook("wa-main", bot)
	defer func() {
		webhookMu.Lock()
		delete(webhookHandlers, "wa-main")
		webhookMu.Unlock()
	}()

	payload := `{
		"object":"whatsapp_business_account",
		"entry":[{
			"changes":[{
				"value":{
					"metadata":{"phone_number_id":"other-phone"},
					"messages":[{
						"id":"wamid-1",
						"from":"15551234567",
						"timestamp":"1712300000",
						"type":"text",
						"text":{"body":"wrong route"}
					}]
				}
			}]
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp/wa-main", strings.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", signWebhook(payload, "app-secret"))
	w := httptest.NewRecorder()
	HandleWebhook("wa-main", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(delivered) != 0 {
		t.Fatalf("expected mismatched metadata to be dropped, got %+v", delivered)
	}
}

func TestHandleWebhook_VerifyChallenge(t *testing.T) {
	bot := &whatsappBot{channelID: "wa-verify", verifyToken: "verify-token", appSecret: "app-secret", done: make(chan struct{})}
	registerWebhook("wa-verify", bot)
	defer func() {
		webhookMu.Lock()
		delete(webhookHandlers, "wa-verify")
		webhookMu.Unlock()
	}()

	req := httptest.NewRequest(http.MethodGet, "/webhooks/whatsapp/wa-verify?hub.mode=subscribe&hub.verify_token=verify-token&hub.challenge=abc123", nil)
	w := httptest.NewRecorder()
	HandleWebhook("wa-verify", w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "abc123" {
		t.Fatalf("unexpected challenge body: %q", w.Body.String())
	}
}

func TestWhatsAppSend_UsesChannelReplyTarget(t *testing.T) {
	var captured map[string]any
	bot := &whatsappBot{
		channelID:        "wa-main",
		token:            "token",
		appSecret:        "app-secret",
		phoneNumberID:    "phone-id",
		defaultRecipient: "+15550000000",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/v18.0/phone-id/messages" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if err := json.NewDecoder(req.Body).Decode(&captured); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			return jsonResponse(req, http.StatusOK, `{"messages":[{"id":"wamid.out"}]}`), nil
		})},
		done: make(chan struct{}),
	}

	ctx := sdk.WithChannelReplyTarget(context.Background(), "+15551112222")
	if err := bot.Send(ctx, "reply text"); err != nil {
		t.Fatalf("Send error: %v", err)
	}
	if captured["to"] != "+15551112222" {
		t.Fatalf("expected reply target recipient, got %#v", captured)
	}
}

func TestWhatsAppSend_UsesExplicitDefaultRecipientFallback(t *testing.T) {
	var captured map[string]any
	bot := &whatsappBot{
		channelID:        "wa-main",
		token:            "token",
		appSecret:        "app-secret",
		phoneNumberID:    "phone-id",
		defaultRecipient: "+15550000000",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(req.Body).Decode(&captured); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			return jsonResponse(req, http.StatusOK, `{"messages":[{"id":"wamid.out"}]}`), nil
		})},
		done: make(chan struct{}),
	}

	if err := bot.Send(context.Background(), "fallback text"); err != nil {
		t.Fatalf("Send error: %v", err)
	}
	if captured["to"] != "+15550000000" {
		t.Fatalf("expected explicit default recipient, got %#v", captured)
	}
}

func TestWhatsAppSend_RejectsAmbiguousUntargetedReply(t *testing.T) {
	bot := &whatsappBot{channelID: "wa-main", appSecret: "app-secret", done: make(chan struct{})}
	if err := bot.Send(context.Background(), "hello"); err == nil {
		t.Fatal("expected error without reply target or explicit default recipient")
	}
}
