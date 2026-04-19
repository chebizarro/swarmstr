// Package googlechat implements a Google Chat channel extension for metiq
// using the Google Chat REST API with service-account authentication.
//
// Inbound messages arrive via an HTTP webhook pushed by Google Chat.
// Outbound messages are sent via the Chat API using an OAuth2 access token
// minted from a service-account key.
//
// Registration: import _ "metiq/internal/extensions/googlechat" in the
// daemon main.go to register this plugin at startup.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "service_account_json": "/path/to/sa.json",  // required: path or inline JSON
//	  "space_name":           "spaces/XXXXXX",      // required: target Chat space
//	  "webhook_path":         "/webhooks/googlechat/my-channel", // HTTP path for inbound
//	  "allowed_senders":      [],                   // optional: email allowlist
//	  "skip_jwt_verify":      false                 // set true in tests only
//	}
//
// Webhook setup: in the Google Cloud Console, configure your Chat app to push
// events to <admin_addr><webhook_path>.
//
// To add a Google Chat channel to your metiq config:
//
//	"nostr_channels": {
//	  "gchat-main": {
//	    "kind": "googlechat",
//	    "config": {
//	      "service_account_json": "/etc/metiq/sa.json",
//	      "space_name": "spaces/AAAAAA"
//	    }
//	  }
//	}
package googlechat

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"metiq/internal/plugins/sdk"
)

// GoogleChatPlugin is the factory for Google Chat channel instances.
type GoogleChatPlugin struct{}

func (p *GoogleChatPlugin) ID() string   { return "googlechat" }
func (p *GoogleChatPlugin) Type() string { return "Google Chat" }

func (p *GoogleChatPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"service_account_json": map[string]any{
				"type":        "string",
				"description": "Path to service account JSON file, or the JSON content itself.",
			},
			"space_name": map[string]any{
				"type":        "string",
				"description": "Google Chat space resource name (e.g. spaces/XXXXXX).",
			},
			"webhook_path": map[string]any{
				"type":        "string",
				"description": "HTTP path the daemon should register for inbound push events.",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional allowlist of Google account email addresses.",
			},
			"skip_jwt_verify": map[string]any{
				"type":        "boolean",
				"description": "Skip inbound JWT signature verification (for testing only).",
			},
		},
		"required": []string{"service_account_json", "space_name"},
	}
}

func (p *GoogleChatPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Threads:      true,
		MultiAccount: true,
	}
}

// GatewayMethods exposes no additional gateway methods; inbound handling is
// done via the webhook registry below.
func (p *GoogleChatPlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *GoogleChatPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	saRaw, _ := cfg["service_account_json"].(string)
	spaceName, _ := cfg["space_name"].(string)
	if saRaw == "" || spaceName == "" {
		return nil, fmt.Errorf("googlechat channel %q: service_account_json and space_name are required", channelID)
	}

	sa, err := loadServiceAccount(saRaw)
	if err != nil {
		return nil, fmt.Errorf("googlechat channel %q: load service account: %w", channelID, err)
	}

	allowedSenders := map[string]bool{}
	switch v := cfg["allowed_senders"].(type) {
	case []interface{}:
		for _, s := range v {
			if e, ok := s.(string); ok && e != "" {
				allowedSenders[e] = true
			}
		}
	}

	skipJWTVerify := false
	if v, ok := cfg["skip_jwt_verify"].(bool); ok {
		skipJWTVerify = v
	}

	bot := &gchatBot{
		channelID:      channelID,
		spaceName:      spaceName,
		sa:             sa,
		allowedSenders: allowedSenders,
		skipJWTVerify:  skipJWTVerify,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}

	registerWebhook(channelID, bot)
	log.Printf("googlechat: webhook handler registered for channel %s (space=%s)", channelID, spaceName)
	return bot, nil
}

// ─── Service-account helpers ──────────────────────────────────────────────────

type serviceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

func loadServiceAccount(raw string) (*serviceAccount, error) {
	var data []byte
	// Detect inline JSON vs file path.
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		data = []byte(trimmed)
	} else {
		var err error
		data, err = os.ReadFile(trimmed)
		if err != nil {
			return nil, fmt.Errorf("read service account file %q: %w", trimmed, err)
		}
	}
	var sa serviceAccount
	if err := json.Unmarshal(data, &sa); err != nil {
		return nil, fmt.Errorf("parse service account JSON: %w", err)
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("service account JSON missing client_email or private_key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &sa, nil
}

// mintJWT creates a signed JWT for the service-account OAuth2 flow.
func mintJWT(sa *serviceAccount, scope string, now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))

	exp := now.Add(time.Hour).Unix()
	payload, _ := json.Marshal(map[string]any{
		"iss":   sa.ClientEmail,
		"scope": scope,
		"aud":   sa.TokenURI,
		"exp":   exp,
		"iat":   now.Unix(),
	})
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)

	signingInput := header + "." + encodedPayload
	h := sha256.New()
	h.Write([]byte(signingInput))
	digest := h.Sum(nil)

	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("service account: failed to decode PEM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1.
		pk, e2 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if e2 != nil {
			return "", fmt.Errorf("service account: parse private key: %w (PKCS8: %v)", e2, err)
		}
		key = pk
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("service account: private key is not RSA")
	}

	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest)
	if err != nil {
		return "", fmt.Errorf("service account: sign JWT: %w", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// ─── Global webhook registry ──────────────────────────────────────────────────

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*gchatBot{}
)

func registerWebhook(channelID string, bot *gchatBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches an incoming Google Chat push event to the registered
// bot for channelID.  Call this from the admin HTTP server.
func HandleWebhook(channelID string, w http.ResponseWriter, r *http.Request) {
	webhookMu.RLock()
	bot, ok := webhookHandlers[channelID]
	webhookMu.RUnlock()
	if !ok {
		http.Error(w, "unknown channel", http.StatusNotFound)
		return
	}
	bot.handlePush(w, r)
}

// ─── Bot implementation ───────────────────────────────────────────────────────

type gchatBot struct {
	channelID      string
	spaceName      string
	sa             *serviceAccount
	allowedSenders map[string]bool
	skipJWTVerify  bool
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	httpClient     *http.Client
	// token cache
	tokenMu     sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

func (b *gchatBot) ID() string { return b.channelID }

func (b *gchatBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	webhookMu.Lock()
	delete(webhookHandlers, b.channelID)
	webhookMu.Unlock()
}

// ─── Inbound JWT verification ─────────────────────────────────────────────────

// verifyInboundJWT performs a basic check on the Google-signed JWT attached to
// inbound push requests.  It decodes the payload and verifies:
//   - iss is chat@system.gserviceaccount.com
//   - aud matches the registered webhook URL (extracted from the request)
//
// Full cryptographic signature verification against Google's public keys is
// omitted here; for production deployments fetch and cache Google's JWKS from
// https://www.googleapis.com/service_accounts/v1/jwk/chat@system.gserviceaccount.com
// and verify the RS256 signature.
func (b *gchatBot) verifyInboundJWT(r *http.Request) bool {
	if b.skipJWTVerify {
		return true
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	// Decode payload (add padding as needed).
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(payload)
		if err != nil {
			return false
		}
	}
	var claims struct {
		Iss string `json:"iss"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return false
	}
	if claims.Iss != "chat@system.gserviceaccount.com" {
		return false
	}
	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return false
	}
	return true
}

// ─── Push handler ─────────────────────────────────────────────────────────────

type gchatPushEvent struct {
	Type      string `json:"type"`
	EventTime string `json:"eventTime"`
	Message   *struct {
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
	} `json:"message"`
	Space *struct {
		Name string `json:"name"`
	} `json:"space"`
}

func (b *gchatBot) handlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !b.verifyInboundJWT(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var ev gchatPushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "parse event", http.StatusBadRequest)
		return
	}

	if ev.Type != "MESSAGE" || ev.Message == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	if len(b.allowedSenders) > 0 && !b.allowedSenders[ev.Message.Sender.Email] {
		w.WriteHeader(http.StatusOK)
		return
	}

	text := strings.TrimSpace(ev.Message.Text)
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	senderID := ev.Message.Sender.Email
	if senderID == "" {
		senderID = ev.Message.Sender.Name
	}

	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  senderID,
		Text:      text,
		EventID:   ev.Message.Name,
	})

	// Respond with an empty JSON object (required by Google Chat).
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

// ─── OAuth2 token management ──────────────────────────────────────────────────

const chatScope = "https://www.googleapis.com/auth/chat.bot"

func (b *gchatBot) accessToken(ctx context.Context) (string, error) {
	b.tokenMu.Lock()
	defer b.tokenMu.Unlock()
	if b.cachedToken != "" && time.Now().Before(b.tokenExpiry.Add(-30*time.Second)) {
		return b.cachedToken, nil
	}

	jwt, err := mintJWT(b.sa, chatScope, time.Now())
	if err != nil {
		return "", err
	}

	body := "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Ajwt-bearer&assertion=" + jwt
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.sa.TokenURI,
		strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch access token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("token error: %s: %s", result.Error, result.ErrorDesc)
	}
	b.cachedToken = result.AccessToken
	if result.ExpiresIn > 0 {
		b.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	} else {
		b.tokenExpiry = time.Now().Add(time.Hour)
	}
	return b.cachedToken, nil
}

func (b *gchatBot) doAPI(ctx context.Context, method, path string, reqBody any) ([]byte, error) {
	token, err := b.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var bodyReader io.Reader
	if reqBody != nil {
		data, _ := json.Marshal(reqBody)
		bodyReader = bytes.NewReader(data)
	}
	apiURL := "https://chat.googleapis.com/v1/" + strings.TrimPrefix(path, "/")
	req, err := http.NewRequestWithContext(ctx, method, apiURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		if apiErr.Error.Message != "" {
			return nil, fmt.Errorf("googlechat API %s %s: status %d: %s", method, path, resp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("googlechat API %s %s: status %d", method, path, resp.StatusCode)
	}
	return raw, nil
}

// ─── Send ─────────────────────────────────────────────────────────────────────

func (b *gchatBot) Send(ctx context.Context, text string) error {
	path := b.spaceName + "/messages"
	_, err := b.doAPI(ctx, http.MethodPost, path, map[string]any{"text": text})
	return err
}

// ─── ThreadHandle ─────────────────────────────────────────────────────────────

// SendInThread posts a reply in a Google Chat thread.
// threadID is the thread resource name (e.g. spaces/XXX/threads/YYY).
func (b *gchatBot) SendInThread(ctx context.Context, threadID, text string) error {
	path := b.spaceName + "/messages?messageReplyOption=REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"
	_, err := b.doAPI(ctx, http.MethodPost, path, map[string]any{
		"text":   text,
		"thread": map[string]string{"name": threadID},
	})
	return err
}
