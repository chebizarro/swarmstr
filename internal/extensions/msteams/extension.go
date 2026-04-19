// Package msteams implements a Microsoft Teams Bot channel extension for metiq.
//
// The bot uses the Azure Bot Framework REST API.  Incoming activities are
// received via a registered webhook; outbound messages are sent using the Bot
// Framework Connector Service.
//
// Registration: import _ "metiq/internal/extensions/msteams" in the daemon
// main.go to include this plugin in the binary.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "app_id":      "00000000-0000-0000-0000-000000000000",  // required: Azure Bot app ID
//	  "app_secret":  "s3cr3t",                               // required: Azure client secret
//	  "service_url": "https://smba.trafficmanager.net/amer/", // required: Teams service URL
//	  "allowed_senders": []                                  // optional: Teams user MRI allowlist
//	}
//
// Webhook configuration: the bot must be registered in the Azure Bot portal with
// the metiqd webhook endpoint at <admin_addr>/webhooks/msteams/<channel_id>.
//
// To add a Teams channel to your metiq config:
//
//	"nostr_channels": {
//	  "teams-general": {
//	    "kind": "msteams",
//	    "config": {
//	      "app_id":     "...",
//	      "app_secret": "...",
//	      "service_url": "https://smba.trafficmanager.net/amer/"
//	    }
//	  }
//	}
package msteams

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("msteams", func() sdk.ChannelPlugin { return &MSTeamsPlugin{} })
}

type botJWTClaims struct {
	Aud any    `json:"aud"`
	Iss string `json:"iss"`
	Exp int64  `json:"exp"`
	Nbf int64  `json:"nbf"`
}

func (c botJWTClaims) HasAudience(appID string) bool {
	if appID == "" {
		return false
	}
	switch aud := c.Aud.(type) {
	case string:
		return aud == appID
	case []any:
		for _, v := range aud {
			if s, ok := v.(string); ok && s == appID {
				return true
			}
		}
	}
	return false
}

func parseJWTClaims(token string) (botJWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return botJWTClaims{}, fmt.Errorf("invalid jwt format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return botJWTClaims{}, err
	}
	var claims botJWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return botJWTClaims{}, err
	}
	return claims, nil
}

// MSTeamsPlugin is the factory for Microsoft Teams Bot channel instances.
type MSTeamsPlugin struct{}

func (p *MSTeamsPlugin) ID() string   { return "msteams" }
func (p *MSTeamsPlugin) Type() string { return "Microsoft Teams" }

func (p *MSTeamsPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"app_id": map[string]any{
				"type":        "string",
				"description": "Azure Bot Framework application ID (GUID).",
			},
			"app_secret": map[string]any{
				"type":        "string",
				"description": "Azure Bot application client secret.",
			},
			"service_url": map[string]any{
				"type":        "string",
				"description": "Teams Bot Framework service URL (from inbound Activity.serviceUrl).",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional allowlist of Teams user MRIs (e.g. 29:xxx).",
			},
		},
		"required": []string{"app_id", "app_secret"},
	}
}

func (p *MSTeamsPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Reactions: true,
		Threads:   true,
		Edit:      true,
	}
}

// GatewayMethods registers the webhook endpoint that the Bot Framework calls.
func (p *MSTeamsPlugin) GatewayMethods() []sdk.GatewayMethod {
	// Webhook handling is done directly in the Connect path by registering an
	// HTTP handler via the plugin's webhook registry.  No additional gateway
	// methods are needed for basic functionality.
	return nil
}

func (p *MSTeamsPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	appID, _ := cfg["app_id"].(string)
	appSecret, _ := cfg["app_secret"].(string)
	serviceURL, _ := cfg["service_url"].(string)

	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("msteams channel %q: app_id and app_secret are required", channelID)
	}

	allowedSenders := map[string]bool{}
	switch v := cfg["allowed_senders"].(type) {
	case []interface{}:
		for _, s := range v {
			if mri, ok := s.(string); ok && mri != "" {
				allowedSenders[mri] = true
			}
		}
	}

	bot := &teamsBot{
		channelID:      channelID,
		appID:          appID,
		appSecret:      appSecret,
		serviceURL:     strings.TrimRight(serviceURL, "/"),
		allowedSenders: allowedSenders,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
	}

	// Register the webhook handler in the global Teams webhook registry.
	registerWebhook(channelID, bot)
	log.Printf("msteams: webhook handler registered for channel %s (app_id=%s)", channelID, appID)
	return bot, nil
}

// ─── Global webhook registry ──────────────────────────────────────────────────
//
// The daemon's admin HTTP server calls HandleWebhook(channelID, w, r) when an
// incoming Bot Framework POST arrives at /webhooks/msteams/<channelID>.

var (
	webhookMu       sync.RWMutex
	webhookHandlers = map[string]*teamsBot{}
)

func registerWebhook(channelID string, bot *teamsBot) {
	webhookMu.Lock()
	webhookHandlers[channelID] = bot
	webhookMu.Unlock()
}

// HandleWebhook dispatches an incoming Bot Framework Activity to the registered
// bot for channelID.  This must be called by the admin HTTP server.
func HandleWebhook(channelID string, w http.ResponseWriter, r *http.Request) {
	webhookMu.RLock()
	bot, ok := webhookHandlers[channelID]
	webhookMu.RUnlock()
	if !ok {
		http.Error(w, "unknown channel", http.StatusNotFound)
		return
	}
	bot.handleActivity(w, r)
}

// ─── Bot implementation ───────────────────────────────────────────────────────

// botFrameworkActivity is a Bot Framework Activity object (simplified).
type botFrameworkActivity struct {
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	Text         string          `json:"text"`
	ServiceURL   string          `json:"serviceUrl"`
	Conversation json.RawMessage `json:"conversation"`
	From         struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"from"`
	Recipient struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"recipient"`
	ReplyToID string `json:"replyToId,omitempty"`
	ChannelID string `json:"channelId"`
}

type teamsBot struct {
	channelID      string
	appID          string
	appSecret      string
	serviceURL     string
	allowedSenders map[string]bool
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	httpClient     *http.Client
	// lastActivity stores the most recent inbound activity for reply routing.
	lastActivity *botFrameworkActivity
	activityMu   sync.Mutex
}

func (b *teamsBot) ID() string { return b.channelID }

func (b *teamsBot) Close() {
	select {
	case <-b.done:
	default:
		close(b.done)
	}
	webhookMu.Lock()
	delete(webhookHandlers, b.channelID)
	webhookMu.Unlock()
}

// verifyInboundAuth performs lightweight bearer token checks for inbound
// Bot Framework activities. This intentionally keeps dependencies minimal and
// validates required JWT claims (aud/exp/nbf/iss), but does not yet perform
// full signature validation against Bot Framework OIDC keys.
func (b *teamsBot) verifyInboundAuth(r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if strings.TrimSpace(token) == "" {
		return false
	}

	claims, err := parseJWTClaims(token)
	if err != nil {
		return false
	}
	if !claims.HasAudience(b.appID) {
		return false
	}
	now := time.Now().Unix()
	if claims.Exp > 0 && now >= claims.Exp {
		return false
	}
	if claims.Nbf > 0 && now < claims.Nbf {
		return false
	}
	issuer := strings.ToLower(strings.TrimSpace(claims.Iss))
	if issuer == "" {
		return false
	}
	if !strings.Contains(issuer, "botframework") && !strings.Contains(issuer, "microsoft") {
		return false
	}
	return true
}

func (b *teamsBot) handleActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if !b.verifyInboundAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var activity botFrameworkActivity
	if err := json.Unmarshal(body, &activity); err != nil {
		http.Error(w, "parse activity", http.StatusBadRequest)
		return
	}

	// Only handle message activities.
	if activity.Type != "message" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if len(b.allowedSenders) > 0 && !b.allowedSenders[activity.From.ID] {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Update serviceURL from the inbound activity if not pre-configured.
	b.activityMu.Lock()
	b.lastActivity = &activity
	if b.serviceURL == "" && activity.ServiceURL != "" {
		b.serviceURL = strings.TrimRight(activity.ServiceURL, "/")
	}
	b.activityMu.Unlock()

	text := strings.TrimSpace(activity.Text)
	// Strip HTML tags that Teams sometimes includes.
	text = stripSimpleHTML(text)
	if text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  activity.From.ID,
		Text:      text,
		EventID:   activity.ID,
	})

	w.WriteHeader(http.StatusOK)
}

// ─── Outbound messaging ───────────────────────────────────────────────────────

func (b *teamsBot) Send(ctx context.Context, text string) error {
	b.activityMu.Lock()
	last := b.lastActivity
	svcURL := b.serviceURL
	b.activityMu.Unlock()

	if last == nil || svcURL == "" {
		return fmt.Errorf("msteams: no conversation context available for channel %s", b.channelID)
	}

	// Extract conversation ID from the last activity.
	var conv struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(last.Conversation, &conv)
	if conv.ID == "" {
		return fmt.Errorf("msteams: no conversation ID available")
	}

	return b.postActivity(ctx, svcURL, conv.ID, last.Recipient.ID, map[string]any{
		"type":       "message",
		"text":       text,
		"textFormat": "plain",
	})
}

func (b *teamsBot) postActivity(ctx context.Context, serviceURL, conversationID, botID string, payload map[string]any) error {
	token, err := b.acquireToken(ctx)
	if err != nil {
		return fmt.Errorf("msteams: acquire token: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v3/conversations/%s/activities", serviceURL, conversationID)
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("msteams: post activity: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// acquireToken fetches an AAD access token for the Bot Framework API using
// client credentials flow.
func (b *teamsBot) acquireToken(ctx context.Context) (string, error) {
	tokenURL := "https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token"
	data := fmt.Sprintf(
		"grant_type=client_credentials&client_id=%s&client_secret=%s&scope=https%%3A%%2F%%2Fapi.botframework.com%%2F.default",
		b.appID, b.appSecret,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("AAD token error: %s: %s", result.Error, result.ErrorDesc)
	}
	return result.AccessToken, nil
}

// ─── ReactionHandle ───────────────────────────────────────────────────────────

// AddReaction sends a reaction activity.
// eventID is the activity ID of the target message.
func (b *teamsBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	b.activityMu.Lock()
	last := b.lastActivity
	svcURL := b.serviceURL
	b.activityMu.Unlock()
	if last == nil || svcURL == "" {
		return fmt.Errorf("msteams: no conversation context")
	}
	var conv struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(last.Conversation, &conv)
	return b.postActivity(ctx, svcURL, conv.ID, last.Recipient.ID, map[string]any{
		"type":           "messageReaction",
		"reactionsAdded": []map[string]any{{"type": emoji}},
		"replyToId":      eventID,
	})
}

// RemoveReaction sends a reaction-removed activity.
func (b *teamsBot) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	b.activityMu.Lock()
	last := b.lastActivity
	svcURL := b.serviceURL
	b.activityMu.Unlock()
	if last == nil || svcURL == "" {
		return fmt.Errorf("msteams: no conversation context")
	}
	var conv struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(last.Conversation, &conv)
	return b.postActivity(ctx, svcURL, conv.ID, last.Recipient.ID, map[string]any{
		"type":             "messageReaction",
		"reactionsRemoved": []map[string]any{{"type": emoji}},
		"replyToId":        eventID,
	})
}

// ─── ThreadHandle ─────────────────────────────────────────────────────────────

// SendInThread sends a reply to a specific message in Teams.
// threadID is the activity ID of the root message.
func (b *teamsBot) SendInThread(ctx context.Context, threadID, text string) error {
	b.activityMu.Lock()
	last := b.lastActivity
	svcURL := b.serviceURL
	b.activityMu.Unlock()
	if last == nil || svcURL == "" {
		return fmt.Errorf("msteams: no conversation context")
	}
	var conv struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(last.Conversation, &conv)
	return b.postActivity(ctx, svcURL, conv.ID, last.Recipient.ID, map[string]any{
		"type":      "message",
		"text":      text,
		"replyToId": threadID,
	})
}

// ─── EditHandle ───────────────────────────────────────────────────────────────

// EditMessage updates a previously sent message by sending an update activity.
func (b *teamsBot) EditMessage(ctx context.Context, eventID, newText string) error {
	b.activityMu.Lock()
	last := b.lastActivity
	svcURL := b.serviceURL
	b.activityMu.Unlock()
	if last == nil || svcURL == "" {
		return fmt.Errorf("msteams: no conversation context")
	}
	var conv struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(last.Conversation, &conv)

	token, err := b.acquireToken(ctx)
	if err != nil {
		return fmt.Errorf("msteams: acquire token: %w", err)
	}
	endpoint := fmt.Sprintf("%s/v3/conversations/%s/activities/%s", svcURL, conv.ID, eventID)
	data, _ := json.Marshal(map[string]any{"type": "message", "text": newText})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("msteams: edit message: status %d", resp.StatusCode)
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// stripSimpleHTML removes basic HTML tags from Teams message text.
func stripSimpleHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
