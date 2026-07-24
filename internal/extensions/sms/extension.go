// Package sms implements an event-driven Twilio SMS channel extension.
package sms

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- Twilio's webhook protocol requires HMAC-SHA1.
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

const twilioAPIBase = "https://api.twilio.com/2010-04-01/Accounts"

func init() {
	sdk.RegisterChannelConstructor("sms", func() sdk.ChannelPlugin { return &SMSPlugin{} })
}

// SMSPlugin is the Twilio-backed SMS channel factory.
type SMSPlugin struct{}

func (p *SMSPlugin) ID() string   { return "sms" }
func (p *SMSPlugin) Type() string { return "SMS (Twilio)" }

func (p *SMSPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_sid":           map[string]any{"type": "string", "description": "Twilio Account SID."},
			"auth_token":            map[string]any{"type": "string", "description": "Twilio auth token."},
			"from_number":           map[string]any{"type": "string", "description": "Twilio sender in E.164 format."},
			"messaging_service_sid": map[string]any{"type": "string", "description": "Twilio Messaging Service SID used instead of from_number."},
			"default_to":            map[string]any{"type": "string", "description": "Optional default recipient in E.164 format."},
			"public_webhook_url":    map[string]any{"type": "string", "description": "Exact public webhook URL configured in Twilio."},
			"allowed_senders":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"dangerously_disable_signature_validation": map[string]any{"type": "boolean", "description": "Only permitted with a loopback webhook URL."},
		},
		"required": []string{"account_sid", "auth_token", "public_webhook_url"},
		"oneOf": []any{
			map[string]any{"required": []string{"from_number"}},
			map[string]any{"required": []string{"messaging_service_sid"}},
		},
	}
}

func (p *SMSPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{MultiAccount: true}
}

func (p *SMSPlugin) GatewayMethods() []sdk.GatewayMethod {
	return channels.AccountScopedGatewayMethods(p.ID(), []sdk.GatewayMethod{{
		Method:      "sms.send",
		Description: "Send a text message through a configured Twilio SMS account",
		Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
			account, err := smsAccountFromParams(params)
			if err != nil {
				return nil, fmt.Errorf("sms.send: %w", err)
			}
			to, err := normalizePhone(firstString(params, "to", "default_to"))
			if err != nil {
				return nil, fmt.Errorf("sms.send: to: %w", err)
			}
			text, _ := params["text"].(string)
			if strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("sms.send: text is required")
			}
			result, err := sendTwilio(ctx, nil, twilioAPIBase, account, to, text)
			if err != nil {
				return nil, err
			}
			out := map[string]any{"ok": true, "message_id": result.SID, "to": result.To}
			if result.Status != "" {
				out["status"] = result.Status
			}
			return out, nil
		},
	}})
}

type smsAccount struct {
	AccountSID       string
	AuthToken        string
	FromNumber       string
	MessagingService string
	DefaultTo        string
	PublicWebhookURL string
	DisableSignature bool
}

func smsAccountFromParams(cfg map[string]any) (smsAccount, error) {
	a := smsAccount{
		AccountSID:       strings.TrimSpace(stringValue(cfg, "account_sid")),
		AuthToken:        stringValue(cfg, "auth_token"),
		MessagingService: strings.TrimSpace(stringValue(cfg, "messaging_service_sid")),
		PublicWebhookURL: strings.TrimSpace(stringValue(cfg, "public_webhook_url")),
	}
	if a.AccountSID == "" || a.AuthToken == "" {
		return a, fmt.Errorf("account_sid and auth_token are required")
	}
	var err error
	if raw := strings.TrimSpace(stringValue(cfg, "from_number")); raw != "" {
		a.FromNumber, err = normalizePhone(raw)
		if err != nil {
			return a, fmt.Errorf("from_number: %w", err)
		}
	}
	if raw := strings.TrimSpace(stringValue(cfg, "default_to")); raw != "" {
		a.DefaultTo, err = normalizePhone(raw)
		if err != nil {
			return a, fmt.Errorf("default_to: %w", err)
		}
	}
	if a.FromNumber == "" && a.MessagingService == "" {
		return a, fmt.Errorf("from_number or messaging_service_sid is required")
	}
	if v, ok := cfg["dangerously_disable_signature_validation"].(bool); ok {
		a.DisableSignature = v
	}
	if a.PublicWebhookURL != "" {
		u, parseErr := url.Parse(a.PublicWebhookURL)
		if parseErr != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Fragment != "" {
			return a, fmt.Errorf("public_webhook_url must be an absolute HTTP(S) URL without a fragment")
		}
		if a.DisableSignature && !isLoopbackHost(u.Hostname()) {
			return a, fmt.Errorf("signature validation can only be disabled for a loopback webhook URL")
		}
	}
	return a, nil
}

func (p *SMSPlugin) Connect(ctx context.Context, channelID string, cfg map[string]any, onMessage func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	account, err := smsAccountFromParams(cfg)
	if err != nil {
		return nil, fmt.Errorf("sms channel %q: %w", channelID, err)
	}
	if account.PublicWebhookURL == "" {
		return nil, fmt.Errorf("sms channel %q: public_webhook_url is required for authenticated inbound delivery", channelID)
	}
	allowed := map[string]bool{}
	for _, raw := range stringSlice(cfg["allowed_senders"]) {
		phone, phoneErr := normalizePhone(raw)
		if phoneErr != nil {
			return nil, fmt.Errorf("sms channel %q: allowed_senders: %w", channelID, phoneErr)
		}
		allowed[phone] = true
	}
	bot := &smsBot{
		channelID: channelID, account: account, allowedSenders: allowed,
		onMessage: onMessage, done: make(chan struct{}), httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBase: twilioAPIBase, seen: make(map[string]struct{}),
	}
	webhookMu.Lock()
	if _, exists := webhookHandlers[channelID]; exists {
		webhookMu.Unlock()
		return nil, fmt.Errorf("sms channel %q: webhook already registered", channelID)
	}
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
	log.Printf("sms: Twilio webhook registered channel=%s", channelID)
	_ = ctx // webhook lifecycle is owned by the returned handle.
	return bot, nil
}

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*smsBot{}
)

// HandleWebhook dispatches Twilio callbacks by configured channel ID.
func HandleWebhook(channelID string, w http.ResponseWriter, r *http.Request) {
	webhookMu.RLock()
	bot := webhookHandlers[channelID]
	webhookMu.RUnlock()
	if bot == nil {
		http.Error(w, "unknown channel", http.StatusNotFound)
		return
	}
	bot.handleWebhook(w, r)
}

type smsBot struct {
	mu             sync.Mutex
	channelID      string
	account        smsAccount
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	httpClient     *http.Client
	apiBase        string
	seen           map[string]struct{}
	seenOrder      []string
}

func (b *smsBot) ID() string { return b.channelID }

func (b *smsBot) Close() {
	b.mu.Lock()
	select {
	case <-b.done:
		b.mu.Unlock()
		return
	default:
		close(b.done)
	}
	b.mu.Unlock()
	webhookMu.Lock()
	if webhookHandlers[b.channelID] == b {
		delete(webhookHandlers, b.channelID)
	}
	webhookMu.Unlock()
}

func (b *smsBot) Send(ctx context.Context, text string) error {
	_, err := b.SendWithReceipt(ctx, text)
	return err
}

func (b *smsBot) SendWithReceipt(ctx context.Context, text string) (channels.DeliveryReceipt, error) {
	to := strings.TrimSpace(sdk.ChannelReplyTarget(ctx))
	if to == "" {
		to = b.account.DefaultTo
	}
	receipt := channels.DeliveryReceipt{ChannelID: b.channelID, Provider: "sms", Attempts: 1, CreatedAt: time.Now()}
	normalized, err := normalizePhone(to)
	if err != nil {
		err = fmt.Errorf("sms %s: target is required and must be E.164: %w", b.channelID, err)
		receipt.Status, receipt.Error = channels.DeliveryFailed, err.Error()
		return receipt, err
	}
	apiBase := b.apiBase
	if apiBase == "" {
		apiBase = twilioAPIBase
	}
	result, err := sendTwilio(ctx, b.httpClient, apiBase, b.account, normalized, text)
	if err != nil {
		receipt.Status, receipt.Error = channels.DeliveryFailed, err.Error()
		return receipt, err
	}
	receipt.MessageID = result.SID
	receipt.Status, receipt.DeliveredAt = channels.DeliveryDelivered, time.Now()
	return receipt, nil
}

func (b *smsBot) handleWebhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, "<Response></Response>")
		return
	}
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/x-www-form-urlencoded" {
		http.Error(w, "invalid content type", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<10+1))
	if err != nil || len(body) > 32<<10 {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if !b.account.DisableSignature && !verifyTwilioSignature(b.account.AuthToken, b.signatureURL(r), form, r.Header.Get("X-Twilio-Signature")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	if sid := strings.TrimSpace(form.Get("AccountSid")); sid != "" && sid != b.account.AccountSID {
		http.Error(w, "account mismatch", http.StatusUnauthorized)
		return
	}
	messageSID := firstNonBlank(form.Get("MessageSid"), form.Get("SmsSid"), form.Get("SmsMessageSid"))
	from, phoneErr := normalizeInboundPhone(form.Get("From"))
	to := strings.TrimSpace(form.Get("To"))
	text := form.Get("Body")
	if messageSID == "" || phoneErr != nil || to == "" || text == "" {
		http.Error(w, "malformed SMS callback", http.StatusBadRequest)
		return
	}
	if len(b.allowedSenders) > 0 && !b.allowedSenders[from] {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "<Response></Response>")
		return
	}
	if !b.markSeen(messageSID) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "<Response></Response>")
		return
	}
	b.onMessage(sdk.InboundChannelMessage{ChannelID: b.channelID, SenderID: from, Text: text, EventID: messageSID})
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "<Response></Response>")
}

func (b *smsBot) signatureURL(r *http.Request) string {
	if strings.Contains(b.account.PublicWebhookURL, "?") || r.URL.RawQuery == "" {
		return b.account.PublicWebhookURL
	}
	return b.account.PublicWebhookURL + "?" + r.URL.RawQuery
}

func (b *smsBot) markSeen(sid string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.seen[sid]; exists {
		return false
	}
	b.seen[sid] = struct{}{}
	b.seenOrder = append(b.seenOrder, sid)
	if len(b.seenOrder) > 1024 {
		delete(b.seen, b.seenOrder[0])
		b.seenOrder = b.seenOrder[1:]
	}
	return true
}

func verifyTwilioSignature(token, webhookURL string, form url.Values, signature string) bool {
	if token == "" || webhookURL == "" || signature == "" {
		return false
	}
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var signed strings.Builder
	signed.WriteString(webhookURL)
	for _, key := range keys {
		values := append([]string(nil), form[key]...)
		sort.Strings(values)
		for _, value := range values {
			signed.WriteString(key)
			signed.WriteString(value)
		}
	}
	mac := hmac.New(sha1.New, []byte(token)) // #nosec G401 -- mandated by Twilio.
	_, _ = mac.Write([]byte(signed.String()))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

type twilioSendResult struct {
	SID    string `json:"sid"`
	To     string `json:"to"`
	From   string `json:"from"`
	Status string `json:"status"`
}

func sendTwilio(ctx context.Context, client *http.Client, apiBase string, account smsAccount, to, text string) (twilioSendResult, error) {
	var result twilioSendResult
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	form := url.Values{"To": {to}, "Body": {text}}
	if account.FromNumber != "" {
		form.Set("From", account.FromNumber)
	} else {
		form.Set("MessagingServiceSid", account.MessagingService)
	}
	endpoint := fmt.Sprintf("%s/%s/Messages.json", strings.TrimRight(apiBase, "/"), url.PathEscape(account.AccountSID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return result, err
	}
	req.SetBasicAuth(account.AccountSID, account.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("sms Twilio send: %w", err)
	}
	defer resp.Body.Close()
	limit := int64(1 << 20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limit = 8 << 10
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if readErr != nil {
		return result, fmt.Errorf("sms Twilio send: read response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("sms Twilio send: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), int(limit))])))
	}
	if len(raw) > int(limit) {
		return result, fmt.Errorf("sms Twilio send: response exceeds %d bytes", limit)
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&result); err != nil {
		return result, fmt.Errorf("sms Twilio send: invalid response: %w", err)
	}
	if strings.TrimSpace(result.SID) == "" {
		return result, fmt.Errorf("sms Twilio send: response did not include a Message SID")
	}
	if result.To == "" {
		result.To = to
	}
	return result, nil
}

func normalizePhone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("phone number is empty")
	}
	var digits strings.Builder
	for i, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == '+' && i == 0:
		default:
			return "", fmt.Errorf("%q is not an E.164 phone number", raw)
		}
	}
	if !strings.HasPrefix(raw, "+") || digits.Len() < 8 || digits.Len() > 15 || strings.HasPrefix(digits.String(), "0") {
		return "", fmt.Errorf("%q is not an E.164 phone number", raw)
	}
	return "+" + digits.String(), nil
}

func normalizeInboundPhone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if idx := strings.IndexByte(raw, ':'); idx >= 0 {
		if !strings.EqualFold(raw[:idx], "rcs") {
			return "", fmt.Errorf("unsupported SMS sender scheme")
		}
		raw = raw[idx+1:]
	}
	return normalizePhone(raw)
}

func stringSlice(v any) []string {
	switch values := v.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s, ok := value.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func stringValue(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(m, key)); value != "" {
			return value
		}
	}
	return ""
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
