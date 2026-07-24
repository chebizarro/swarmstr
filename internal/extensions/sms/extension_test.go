package sms

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

func TestSMSPluginSurface(t *testing.T) {
	p := &SMSPlugin{}
	if p.ID() != "sms" || p.Type() == "" {
		t.Fatalf("unexpected identity: %q %q", p.ID(), p.Type())
	}
	if !p.Capabilities().MultiAccount || p.Capabilities().Typing || p.Capabilities().Reactions {
		t.Fatalf("unexpected capabilities: %+v", p.Capabilities())
	}
	props := p.ConfigSchema()["properties"].(map[string]any)
	for _, key := range []string{"account_sid", "auth_token", "from_number", "messaging_service_sid", "default_to", "public_webhook_url"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing schema property %s", key)
		}
	}
	methods := p.GatewayMethods()
	if len(methods) != 1 || methods[0].Method != "sms.send" {
		t.Fatalf("unexpected methods: %+v", methods)
	}
	var _ sdk.ChannelPlugin = p
	var _ sdk.ChannelPluginWithMethods = p
	var _ sdk.ChannelPluginWithCapabilities = p
}

func TestSMSWebhookSignatureAllowlistAndDedup(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	p := &SMSPlugin{}
	handle, err := p.Connect(context.Background(), "sms-main", map[string]any{
		"account_sid": "AC123", "auth_token": "secret", "from_number": "+15550001111",
		"public_webhook_url": "https://example.test/webhooks/sms/sms-main",
		"allowed_senders":    []any{"+15552223333"},
	}, func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) })
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	form := url.Values{
		"AccountSid": {"AC123"}, "MessageSid": {"SM1"}, "From": {"+15552223333"},
		"To": {"+15550001111"}, "Body": {" hello "},
	}
	sig := signatureForTest("secret", "https://example.test/webhooks/sms/sms-main", form)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/sms/sms-main", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Twilio-Signature", sig)
		w := httptest.NewRecorder()
		HandleWebhook("sms-main", w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("callback %d: status=%d body=%s", i, w.Code, w.Body.String())
		}
	}
	if len(delivered) != 1 || delivered[0].EventID != "SM1" || delivered[0].Text != " hello " || delivered[0].SenderID != "+15552223333" {
		t.Fatalf("unexpected delivery: %+v", delivered)
	}
}

func TestSMSWebhookRejectsInvalidSignatureAndFiltersSender(t *testing.T) {
	var delivered []sdk.InboundChannelMessage
	p := &SMSPlugin{}
	handle, err := p.Connect(context.Background(), "sms-secure", map[string]any{
		"account_sid": "AC1", "auth_token": "secret", "messaging_service_sid": "MG1",
		"public_webhook_url": "https://example.test/sms", "allowed_senders": []string{"+15550000001"},
	}, func(msg sdk.InboundChannelMessage) { delivered = append(delivered, msg) })
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()

	form := url.Values{"MessageSid": {"SM2"}, "From": {"+15550000002"}, "To": {"+15550000003"}, "Body": {"blocked"}}
	req := httptest.NewRequest(http.MethodPost, "/sms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", "wrong")
	w := httptest.NewRecorder()
	HandleWebhook("sms-secure", w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/sms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", signatureForTest("secret", "https://example.test/sms", form))
	w = httptest.NewRecorder()
	HandleWebhook("sms-secure", w, req)
	if w.Code != http.StatusOK || len(delivered) != 0 {
		t.Fatalf("filtered sender should ack without delivery: status=%d delivered=%+v", w.Code, delivered)
	}
}

func TestSMSWebhookUsesExactPublicURLWithQuery(t *testing.T) {
	form := url.Values{"MessageSid": {"SM"}}
	if !verifyTwilioSignature("token", "https://public.example/hook?tenant=a", form,
		signatureForTest("token", "https://public.example/hook?tenant=a", form)) {
		t.Fatal("expected exact configured URL signature to verify")
	}
}

func TestSMSConnectValidation(t *testing.T) {
	p := &SMSPlugin{}
	cases := []map[string]any{
		{},
		{"account_sid": "AC", "auth_token": "x", "from_number": "+15550000000"},
		{"account_sid": "AC", "auth_token": "x", "from_number": "555", "public_webhook_url": "https://x/h"},
		{"account_sid": "AC", "auth_token": "x", "from_number": "+15550000000", "public_webhook_url": "https://x/h", "dangerously_disable_signature_validation": true},
	}
	for i, cfg := range cases {
		if _, err := p.Connect(context.Background(), "bad", cfg, func(sdk.InboundChannelMessage) {}); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestSendTwilioUsesBasicAuthAndSender(t *testing.T) {
	var auth, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sid":"SM123","to":"+15552223333","from":"+15550001111","status":"queued"}`)
	}))
	defer srv.Close()
	result, err := sendTwilio(context.Background(), srv.Client(), srv.URL, smsAccount{
		AccountSID: "AC123", AuthToken: "secret", FromNumber: "+15550001111",
	}, "+15552223333", "hello")
	if err != nil {
		t.Fatal(err)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("AC123:secret"))
	if auth != wantAuth || !strings.Contains(body, "From=%2B15550001111") || !strings.Contains(body, "Body=hello") || result.SID != "SM123" {
		t.Fatalf("unexpected request/result auth=%q body=%q result=%+v", auth, body, result)
	}
}

func TestSMSSendUsesReplyTarget(t *testing.T) {
	var to string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		values, _ := url.ParseQuery(string(raw))
		to = values.Get("To")
		_, _ = io.WriteString(w, `{"sid":"SM-reply"}`)
	}))
	defer srv.Close()
	bot := &smsBot{channelID: "sms", account: smsAccount{AccountSID: "AC", AuthToken: "x", FromNumber: "+15550000000", DefaultTo: "+15559990000"}, httpClient: srv.Client(), apiBase: srv.URL, done: make(chan struct{}), seen: map[string]struct{}{}}
	ctx := sdk.WithChannelReplyTarget(context.Background(), "+15551112222")
	if err := bot.Send(ctx, "reply"); err != nil {
		t.Fatal(err)
	}
	if to != "+15551112222" {
		t.Fatalf("reply target did not win: %q", to)
	}
}

func TestSMSGatewayUsesConfiguredAccountBeforeValidation(t *testing.T) {
	channels.ConfigureChannelAccounts(state.NostrChannelsConfig{"work": {Kind: "sms", Config: map[string]any{
		"account_sid": "AC", "auth_token": "secret", "from_number": "+15550000000", "default_to": "+15551112222", "public_webhook_url": "https://example.test/h", "default_account": true,
	}}})
	t.Cleanup(func() { channels.ConfigureChannelAccounts(nil) })
	method := (&SMSPlugin{}).GatewayMethods()[0]
	_, err := method.Handle(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("configured credentials/default recipient were not resolved before handler validation: %v", err)
	}
}

func TestSMSWebhookRejectsMethodAndContentType(t *testing.T) {
	bot := &smsBot{account: smsAccount{AuthToken: "secret", PublicWebhookURL: "https://example.test/sms"}}

	w := httptest.NewRecorder()
	bot.handleWebhook(w, httptest.NewRequest(http.MethodGet, "/sms", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sms", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	bot.handleWebhook(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for JSON webhook, got %d", w.Code)
	}
}

func TestSMSUnknownWebhook(t *testing.T) {
	w := httptest.NewRecorder()
	HandleWebhook("missing", w, httptest.NewRequest(http.MethodPost, "/", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func signatureForTest(token, webhookURL string, form url.Values) string {
	// Reuse the implementation to generate a fixture by trying the exact HMAC input.
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var data strings.Builder
	data.WriteString(webhookURL)
	for _, key := range keys {
		values := append([]string(nil), form[key]...)
		sort.Strings(values)
		for _, value := range values {
			data.WriteString(key)
			data.WriteString(value)
		}
	}
	mac := hmac.New(sha1.New, []byte(token)) // Twilio fixture algorithm.
	_, _ = mac.Write([]byte(data.String()))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
