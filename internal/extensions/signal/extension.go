// Package signal implements a Signal channel extension for metiq via a
// local signal-rest-api (or signal-cli-rest-api) sidecar.
//
// Because Signal has no public API, this plugin delegates all protocol work to
// a separately-running sidecar process.  The recommended sidecar is
// bbernhard/signal-cli-rest-api (https://github.com/bbernhard/signal-cli-rest-api).
//
// Registration: import _ "metiq/internal/extensions/signal" in the daemon
// main.go to include this plugin in the binary.
//
// Config schema (under nostr_channels.<name>.config):
//
//	{
//	  "api_url":         "http://localhost:8080",  // required: sidecar base URL
//	  "account":         "+15551234567",            // required: E.164 sender number
//	  "allowed_senders": [],                        // optional: E.164 allowlist
//	  "allow_polling": false,                        // explicit opt-in REST fallback
//	  "poll_interval_ms": 3000                       // default 3000 when enabled
//	}
//
// To add a Signal channel to your metiq config:
//
//	"nostr_channels": {
//	  "signal-main": {
//	    "kind": "signal",
//	    "config": {
//	      "api_url": "http://localhost:8080",
//	      "account": "+15551234567"
//	    }
//	  }
//	}
package signal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("signal", func() sdk.ChannelPlugin { return &SignalPlugin{} })
}

// SignalPlugin is the factory for Signal channel instances.
type SignalPlugin struct{}

func (p *SignalPlugin) ID() string   { return "signal" }
func (p *SignalPlugin) Type() string { return "Signal" }

func (p *SignalPlugin) ConfigSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"api_url": map[string]any{
				"type":        "string",
				"description": "Base URL of the signal-rest-api sidecar (e.g. http://localhost:8080).",
			},
			"account": map[string]any{
				"type":        "string",
				"description": "E.164 phone number registered in the sidecar (e.g. +15551234567).",
			},
			"default_to": map[string]any{
				"type":        "string",
				"description": "Optional default Signal recipient used when no reply target is present.",
			},
			"reaction_level": map[string]any{
				"type":        "string",
				"enum":        []string{"off", "ack", "minimal", "extensive"},
				"default":     "minimal",
				"description": "Automatic agent status-reaction policy. Explicit question and approval reactions remain enabled.",
			},
			"allowed_senders": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional E.164 phone number allowlist.",
			},
			"allow_polling": map[string]any{
				"type":        "boolean",
				"description": "Explicitly allow REST receive polling if WebSocket receive is unavailable. Default false.",
			},
			"poll_interval_ms": map[string]any{
				"type":        "integer",
				"description": "Polling interval in milliseconds when explicitly enabled. Default 3000.",
			},
		},
		"required": []string{"api_url", "account"},
	}
}

func (p *SignalPlugin) Capabilities() sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:       true,
		Reactions:    true,
		MultiAccount: true,
	}
}

func (p *SignalPlugin) GatewayMethods() []sdk.GatewayMethod {
	return channels.AccountScopedGatewayMethods(p.ID(), []sdk.GatewayMethod{
		signalSendGatewayMethod("signal.send", "Send a Signal message", ""),
		signalSendGatewayMethod("signal.send_question", "Send a Signal question and route configured emoji choices", "question"),
		signalSendGatewayMethod("signal.send_approval", "Send a Signal approval prompt and route approve/deny reactions", "approval"),
		{
			Method:      "signal.remove_reaction_route",
			Description: "Remove a pending Signal question or approval reaction route",
			Handle: func(_ context.Context, params map[string]any) (map[string]any, error) {
				accountID, _ := params["account_id"].(string)
				routeID, _ := params["route_id"].(string)
				if accountID == "" || strings.TrimSpace(routeID) == "" {
					return nil, fmt.Errorf("signal.remove_reaction_route: account_id and route_id are required")
				}
				bot := registeredSignalBot(accountID)
				if bot == nil {
					return nil, fmt.Errorf("signal.remove_reaction_route: account %q is not connected", accountID)
				}
				bot.removeReactionRoute(routeID)
				return map[string]any{"ok": true, "route_id": routeID}, nil
			},
		},
	})
}

func signalSendGatewayMethod(name, description, routeKind string) sdk.GatewayMethod {
	return sdk.GatewayMethod{
		Method: name, Description: description,
		Handle: func(ctx context.Context, params map[string]any) (map[string]any, error) {
			apiURL := strings.TrimRight(signalString(params, "api_url"), "/")
			account := strings.TrimSpace(signalString(params, "account"))
			to := strings.TrimSpace(firstSignalString(params, "to", "default_to"))
			text := signalString(params, "text")
			if apiURL == "" || account == "" || to == "" || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("%s: api_url, account, to/default_to, and text are required", name)
			}
			var accountID, routeID string
			var bot *signalBot
			var choices map[string]string
			if routeKind != "" {
				accountID = strings.TrimSpace(signalString(params, "account_id"))
				routeID = strings.TrimSpace(signalString(params, "route_id"))
				if accountID == "" || routeID == "" {
					return nil, fmt.Errorf("%s: connected account_id and route_id are required", name)
				}
				bot = registeredSignalBot(accountID)
				if bot == nil {
					return nil, fmt.Errorf("%s: account %q is not connected for reaction routing", name, accountID)
				}
				var err error
				choices, err = signalRouteChoices(routeKind, params)
				if err != nil {
					return nil, fmt.Errorf("%s: invalid reaction route: %w", name, err)
				}
			}
			result, err := sendSignalText(ctx, &http.Client{Timeout: 15 * time.Second}, apiURL, account, to, text)
			if err != nil {
				return nil, err
			}
			messageID := result.ID
			if result.Timestamp > 0 {
				messageID = signalEventID(account, result.Timestamp)
			}
			out := map[string]any{"ok": true, "message_id": messageID}
			if routeKind == "" {
				return out, nil
			}
			if result.Timestamp <= 0 {
				return nil, fmt.Errorf("%s: message %q sent but sidecar response omitted the timestamp required for reaction routing", name, messageID)
			}
			bot.registerReactionRoute(signalReactionRoute{ID: routeID, Kind: routeKind, TargetID: signalEventID(account, result.Timestamp), Choices: choices})
			out["route_id"] = routeID
			return out, nil
		},
	}
}

func signalRouteChoices(kind string, params map[string]any) (map[string]string, error) {
	if kind == "approval" {
		approve := strings.TrimSpace(firstSignalString(params, "approve_emoji"))
		deny := strings.TrimSpace(firstSignalString(params, "deny_emoji"))
		if approve == "" {
			approve = "✅"
		}
		if deny == "" {
			deny = "❌"
		}
		if approve == deny {
			return nil, fmt.Errorf("approval emoji choices must be distinct")
		}
		return map[string]string{approve: "approve", deny: "deny"}, nil
	}
	choices := map[string]string{}
	values, _ := params["choices"].([]any)
	for _, raw := range values {
		choice, _ := raw.(map[string]any)
		emoji := strings.TrimSpace(signalString(choice, "emoji"))
		value := strings.TrimSpace(signalString(choice, "value"))
		if emoji == "" || value == "" {
			return nil, fmt.Errorf("each question choice requires emoji and value")
		}
		if _, exists := choices[emoji]; exists {
			return nil, fmt.Errorf("duplicate choice emoji %q", emoji)
		}
		choices[emoji] = value
	}
	if len(choices) == 0 {
		return nil, fmt.Errorf("question choices are required")
	}
	return choices, nil
}

func (b *signalBot) registerReactionRoute(route signalReactionRoute) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.routesByID == nil {
		b.routesByID = map[string]signalReactionRoute{}
	}
	if b.routesByTarget == nil {
		b.routesByTarget = map[string]string{}
	}
	if old, exists := b.routesByID[route.ID]; exists {
		delete(b.routesByTarget, old.TargetID)
	}
	if oldID, exists := b.routesByTarget[route.TargetID]; exists {
		delete(b.routesByID, oldID)
	}
	b.routesByID[route.ID] = route
	b.routesByTarget[route.TargetID] = route.ID
}

func (b *signalBot) removeReactionRoute(routeID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if route, exists := b.routesByID[routeID]; exists {
		delete(b.routesByTarget, route.TargetID)
		delete(b.routesByID, routeID)
	}
}

func (b *signalBot) routeReaction(sender string, timestamp int64, reaction signalReaction) string {
	if reaction.Remove || strings.TrimSpace(reaction.Emoji) == "" || reaction.TargetSentTimestamp <= 0 || strings.TrimSpace(reaction.TargetAuthor) == "" {
		return ""
	}
	targetID := signalEventID(reaction.TargetAuthor, reaction.TargetSentTimestamp)
	b.mu.Lock()
	routeID := b.routesByTarget[targetID]
	route, exists := b.routesByID[routeID]
	value := route.Choices[reaction.Emoji]
	if exists && value != "" {
		delete(b.routesByID, routeID)
		delete(b.routesByTarget, targetID)
	}
	b.mu.Unlock()
	if !exists || value == "" {
		return ""
	}
	return fmt.Sprintf("[Signal %s reaction] route_id=%s value=%s target=%s reactor=%s at=%d", route.Kind, route.ID, value, targetID, sender, timestamp)
}

func signalString(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return value
}

func firstSignalString(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(signalString(params, key)); value != "" {
			return value
		}
	}
	return ""
}

func (p *SignalPlugin) Connect(
	ctx context.Context,
	channelID string,
	cfg map[string]any,
	onMessage func(sdk.InboundChannelMessage),
) (sdk.ChannelHandle, error) {
	apiURL, _ := cfg["api_url"].(string)
	account, _ := cfg["account"].(string)
	if apiURL == "" || account == "" {
		return nil, fmt.Errorf("signal channel %q: api_url and account are required", channelID)
	}
	apiURL = strings.TrimRight(apiURL, "/")
	reactionLevel := strings.ToLower(strings.TrimSpace(signalString(cfg, "reaction_level")))
	if reactionLevel == "" {
		reactionLevel = "minimal"
	}
	if reactionLevel != "off" && reactionLevel != "ack" && reactionLevel != "minimal" && reactionLevel != "extensive" {
		return nil, fmt.Errorf("signal channel %q: reaction_level must be off, ack, minimal, or extensive", channelID)
	}

	allowedSenders := map[string]bool{}
	switch v := cfg["allowed_senders"].(type) {
	case []interface{}:
		for _, s := range v {
			if n, ok := s.(string); ok && n != "" {
				allowedSenders[n] = true
			}
		}
	}

	pollInterval := 3 * time.Second
	switch v := cfg["poll_interval_ms"].(type) {
	case float64:
		if v > 0 {
			pollInterval = time.Duration(v) * time.Millisecond
		}
	case int:
		if v > 0 {
			pollInterval = time.Duration(v) * time.Millisecond
		}
	}

	bot := &signalBot{
		channelID:      channelID,
		apiURL:         apiURL,
		account:        account,
		defaultTo:      strings.TrimSpace(signalString(cfg, "default_to")),
		reactionLevel:  reactionLevel,
		allowedSenders: allowedSenders,
		allowPolling:   channels.PollingFallbackEnabled(cfg),
		pollInterval:   pollInterval,
		onMessage:      onMessage,
		done:           make(chan struct{}),
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		routesByID:     map[string]signalReactionRoute{},
		routesByTarget: map[string]string{},
		activeTyping:   map[string]bool{},
	}

	// Prefer the event-driven signal-cli JSON-RPC WebSocket receive stream. REST
	// /v1/receive polling requires explicit allow_polling opt-in.
	runCtx, cancel := context.WithCancel(ctx)
	bot.cancel = cancel
	registerSignalBot(channelID, bot)
	go bot.run(runCtx)
	return bot, nil
}

// ─── Bot implementation ───────────────────────────────────────────────────────

type signalReactionRoute struct {
	ID       string
	Kind     string
	TargetID string
	Choices  map[string]string
}

var signalBotRegistry = struct {
	sync.RWMutex
	bots map[string]*signalBot
}{bots: map[string]*signalBot{}}

func registerSignalBot(accountID string, bot *signalBot) {
	signalBotRegistry.Lock()
	signalBotRegistry.bots[accountID] = bot
	signalBotRegistry.Unlock()
}

func registeredSignalBot(accountID string) *signalBot {
	signalBotRegistry.RLock()
	defer signalBotRegistry.RUnlock()
	return signalBotRegistry.bots[accountID]
}

type signalBot struct {
	mu             sync.Mutex
	channelID      string
	apiURL         string
	account        string
	defaultTo      string
	reactionLevel  string
	allowedSenders map[string]bool
	allowPolling   bool
	pollInterval   time.Duration
	onMessage      func(sdk.InboundChannelMessage)
	done           chan struct{}
	cancel         context.CancelFunc
	httpClient     *http.Client
	routesByID     map[string]signalReactionRoute
	routesByTarget map[string]string
	activeTyping   map[string]bool
}

func (b *signalBot) ID() string { return b.channelID }

// ReactionLevel is an additive optional policy surface consumed by the shared
// status-reaction controller without widening the plugin SDK.
func (b *signalBot) ReactionLevel() string { return b.reactionLevel }

func (b *signalBot) Close() {
	signalBotRegistry.Lock()
	if signalBotRegistry.bots[b.channelID] == b {
		delete(signalBotRegistry.bots, b.channelID)
	}
	signalBotRegistry.Unlock()
	if b.cancel != nil {
		b.cancel()
	}
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

// ─── Polling (explicit opt-in fallback) ─────────────────────────────────────
//
// poll is a wait-and-check loop over the signal-cli REST receive endpoint. It is
// non-event-driven and must only run when allow_polling is explicitly enabled.
func (b *signalBot) poll(ctx context.Context) {
	backoff := b.pollInterval
	if backoff <= 0 {
		backoff = 3 * time.Second
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-time.After(backoff):
		}
		if err := b.receive(ctx); err != nil {
			log.Printf("signal: receive error channel=%s: %v", b.channelID, err)
			if backoff < 60*time.Second {
				backoff *= 2
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
			}
			continue
		}
		backoff = b.pollInterval
		if backoff <= 0 {
			backoff = 3 * time.Second
		}
	}
}

// signalEnvelope is the top-level object returned by GET /v1/receive/{account}.
type signalEnvelope struct {
	Envelope struct {
		Source      string `json:"source"`
		Timestamp   int64  `json:"timestamp"`
		DataMessage *struct {
			Message     string             `json:"message"`
			Timestamp   int64              `json:"timestamp"`
			Attachments []signalAttachment `json:"attachments"`
			Reaction    *signalReaction    `json:"reaction"`
		} `json:"dataMessage"`
	} `json:"envelope"`
}

type signalReaction struct {
	Emoji               string `json:"emoji"`
	TargetAuthor        string `json:"targetAuthor"`
	TargetSentTimestamp int64  `json:"targetSentTimestamp"`
	Remove              bool   `json:"isRemove"`
}

type signalAttachment struct {
	ID           string `json:"id"`
	AttachmentID string `json:"attachmentId"`
	Filename     string `json:"filename"`
	ContentType  string `json:"contentType"`
	MIMEType     string `json:"mimeType"`
}

func (b *signalBot) receive(ctx context.Context) error {
	url := fmt.Sprintf("%s/v1/receive/%s", b.apiURL, b.account)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	// The sidecar may return a JSON array of envelopes or a newline-delimited
	// stream.  Try array first, then fall back to NDJSON.
	var envelopes []signalEnvelope
	if err := json.Unmarshal(raw, &envelopes); err != nil {
		// Try NDJSON.
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var env signalEnvelope
			if jsonErr := json.Unmarshal([]byte(line), &env); jsonErr == nil {
				envelopes = append(envelopes, env)
			}
		}
	}

	for _, env := range envelopes {
		b.deliverEnvelope(env)
	}
	return nil
}

// deliverEnvelope applies allowlist filtering and delivers a Signal envelope to
// the agent. It is shared by the JSON-RPC WebSocket path and the polling
// fallback.
func (b *signalBot) deliverEnvelope(env signalEnvelope) {
	dm := env.Envelope.DataMessage
	if dm == nil {
		return
	}
	sender := env.Envelope.Source
	if len(b.allowedSenders) > 0 && !b.allowedSenders[sender] {
		return
	}
	text := strings.TrimSpace(dm.Message)
	if dm.Reaction != nil {
		if routed := b.routeReaction(sender, env.Envelope.Timestamp, *dm.Reaction); routed != "" {
			b.onMessage(sdk.InboundChannelMessage{
				ChannelID: b.channelID,
				SenderID:  sender,
				Text:      routed,
				EventID:   signalEventID(sender, env.Envelope.Timestamp),
			})
		}
		return
	}
	mediaURL, mediaMIME := firstSignalAttachment(dm.Attachments)
	if text == "" && mediaURL == "" {
		return
	}
	b.onMessage(sdk.InboundChannelMessage{
		ChannelID: b.channelID,
		SenderID:  sender,
		Text:      text,
		EventID:   signalEventID(sender, env.Envelope.Timestamp),
		MediaURL:  mediaURL,
		MediaMIME: mediaMIME,
	})
}

// ─── Event-driven receive (signal-cli JSON-RPC WebSocket) ─────────────────────

const signalMaxReconnects = 10

// run prefers the event-driven JSON-RPC WebSocket receive stream. REST polling
// is available only with explicit allow_polling opt-in.
func (b *signalBot) run(ctx context.Context) {
	conn, err := b.dialWS(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if !b.allowPolling {
			log.Printf("signal: channel=%s JSON-RPC WebSocket receive unavailable (%v); REST polling fallback disabled (set allow_polling=true to opt in)", b.channelID, err)
			return
		}
		log.Printf("signal: channel=%s JSON-RPC WebSocket receive unavailable (%v); using explicitly enabled REST /v1/receive polling fallback (account=%s, sidecar=%s)", b.channelID, err, b.account, b.apiURL)
		b.poll(ctx)
		return
	}
	log.Printf("signal: channel=%s connected to signal-cli JSON-RPC WebSocket receive (account=%s, sidecar=%s)", b.channelID, b.account, b.apiURL)
	b.serveWS(ctx, conn)
}

func (b *signalBot) wsURL() string {
	u := b.apiURL
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	return u + "/v1/receive/" + b.account
}

func (b *signalBot) dialWS(ctx context.Context) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, b.wsURL(), nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(1 << 20)
	return conn, nil
}

// serveWS reads streamed envelopes, reconnecting with backoff. After
// signalMaxReconnects failures it stops unless REST polling was explicitly enabled.
func (b *signalBot) serveWS(ctx context.Context, conn *websocket.Conn) {
	backoff := b.pollInterval
	if backoff <= 0 {
		backoff = 3 * time.Second
	}
	attempts := 0
	for {
		err := b.readWS(ctx, conn)
		_ = conn.Close(websocket.StatusNormalClosure, "reconnect")
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		default:
		}
		log.Printf("signal: channel=%s WebSocket receive ended (%v); reconnecting", b.channelID, err)
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.done:
				return
			case <-time.After(backoff):
			}
			attempts++
			newConn, derr := b.dialWS(ctx)
			if derr == nil {
				conn = newConn
				attempts = 0
				break
			}
			log.Printf("signal: channel=%s WebSocket reconnect failed (%v)", b.channelID, derr)
			if attempts >= signalMaxReconnects {
				if !b.allowPolling {
					log.Printf("signal: channel=%s giving up on WebSocket after %d attempts; REST polling fallback disabled", b.channelID, attempts)
					return
				}
				log.Printf("signal: channel=%s giving up on WebSocket after %d attempts; using explicitly enabled REST /v1/receive polling fallback", b.channelID, attempts)
				b.poll(ctx)
				return
			}
		}
	}
}

// signalWSFrame accepts both a bare envelope ({"envelope":{...}}) and a
// signal-cli JSON-RPC notification ({"jsonrpc":"2.0","method":"receive",
// "params":{"envelope":{...}}}).
type signalWSFrame struct {
	Method string          `json:"method"`
	Params *signalEnvelope `json:"params"`
	signalEnvelope
}

func (b *signalBot) readWS(ctx context.Context, conn *websocket.Conn) error {
	for {
		var frame signalWSFrame
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			return err
		}
		env := frame.signalEnvelope
		if frame.Params != nil {
			env = *frame.Params
		}
		b.deliverEnvelope(env)
	}
}

// ─── Send ─────────────────────────────────────────────────────────────────────

// signalSendRequest is the body for POST /v2/send.
type signalSendRequest struct {
	Number     string   `json:"number"`
	Recipients []string `json:"recipients"`
	Message    string   `json:"message"`
}

type signalSendResult struct {
	Timestamp int64  `json:"timestamp"`
	ID        string `json:"id"`
}

func signalEventID(author string, timestamp int64) string {
	return fmt.Sprintf("signal-%s-%d", author, timestamp)
}

func (b *signalBot) Send(ctx context.Context, text string) error {
	_, err := b.SendWithReceipt(ctx, text)
	return err
}

func (b *signalBot) SendWithReceipt(ctx context.Context, text string) (channels.DeliveryReceipt, error) {
	receipt := channels.DeliveryReceipt{ChannelID: b.channelID, Provider: "signal", Attempts: 1, CreatedAt: time.Now()}
	recipient, err := b.target(ctx)
	if err != nil {
		receipt.Status, receipt.Error = channels.DeliveryFailed, err.Error()
		return receipt, err
	}
	defer b.clearTypingIfActive(ctx, recipient)
	result, err := sendSignalText(ctx, b.httpClient, b.apiURL, b.account, recipient, text)
	if err != nil {
		receipt.Status, receipt.Error = channels.DeliveryFailed, err.Error()
		return receipt, err
	}
	if result.ID != "" {
		receipt.MessageID = result.ID
	} else if result.Timestamp > 0 {
		receipt.MessageID = signalEventID(b.account, result.Timestamp)
	}
	receipt.Status, receipt.DeliveredAt = channels.DeliveryDelivered, time.Now()
	return receipt, nil
}

func (b *signalBot) target(ctx context.Context) (string, error) {
	recipient := strings.TrimSpace(sdk.ChannelReplyTarget(ctx))
	if recipient == "" {
		recipient = b.defaultTo
	}
	if recipient == "" {
		return "", fmt.Errorf("signal %s: target is required; configure default_to or use a reply target", b.channelID)
	}
	return recipient, nil
}

func sendSignalText(ctx context.Context, client *http.Client, apiURL, account, recipient, text string) (signalSendResult, error) {
	var result signalSendResult
	body, _ := json.Marshal(signalSendRequest{Number: account, Recipients: []string{recipient}, Message: text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(apiURL, "/")+"/v2/send", bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("signal send: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("signal send: status %d: %s", resp.StatusCode, string(raw))
	}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return result, fmt.Errorf("signal send: invalid response: %w", err)
		}
	}
	return result, nil
}

func firstSignalAttachment(attachments []signalAttachment) (string, string) {
	for _, a := range attachments {
		id := strings.TrimSpace(firstNonEmptySignal(a.ID, a.AttachmentID))
		if id == "" {
			continue
		}
		mime := strings.TrimSpace(firstNonEmptySignal(a.ContentType, a.MIMEType))
		return "signal://attachment/" + id, mime
	}
	return "", ""
}

func firstNonEmptySignal(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (b *signalBot) ResolveMedia(ctx context.Context, ref string) (channels.MediaBlob, error) {
	id := strings.TrimSpace(strings.TrimPrefix(ref, "signal://attachment/"))
	if id == "" || id == ref {
		return channels.MediaBlob{}, fmt.Errorf("signal media: invalid attachment ref %q", ref)
	}
	apiURL := fmt.Sprintf("%s/v1/attachments/%s", b.apiURL, urlPathEscapeSignal(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return channels.MediaBlob{}, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return channels.MediaBlob{}, fmt.Errorf("signal media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return channels.MediaBlob{}, fmt.Errorf("signal media: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20+1))
	if err != nil {
		return channels.MediaBlob{}, err
	}
	if len(data) > 25<<20 {
		return channels.MediaBlob{}, fmt.Errorf("signal media: attachment exceeds 25MiB")
	}
	return channels.MediaBlob{URL: ref, MIME: resp.Header.Get("Content-Type"), Data: data}, nil
}

func urlPathEscapeSignal(s string) string {
	r := strings.NewReplacer("%", "%25", "/", "%2F", "?", "%3F", "#", "%23")
	return r.Replace(s)
}

func (b *signalBot) SendTyping(ctx context.Context, _ int) error {
	recipient, err := b.target(ctx)
	if err != nil {
		return err
	}
	if err := b.sendTypingState(ctx, recipient, false); err != nil {
		return err
	}
	b.mu.Lock()
	b.activeTyping[recipient] = true
	b.mu.Unlock()
	return nil
}

// ClearTyping explicitly ends Signal's typing lifecycle via the sidecar DELETE contract.
func (b *signalBot) ClearTyping(ctx context.Context) error {
	recipient, err := b.target(ctx)
	if err != nil {
		return err
	}
	return b.clearTyping(ctx, recipient)
}

func (b *signalBot) sendTypingState(ctx context.Context, recipient string, stop bool) error {
	body, _ := json.Marshal(map[string]any{"recipient": recipient})
	method := http.MethodPut
	if stop {
		method = http.MethodDelete
	}
	req, err := http.NewRequestWithContext(ctx, method, b.apiURL+"/v1/typing-indicator/"+b.account, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal typing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("signal typing: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (b *signalBot) clearTypingIfActive(ctx context.Context, recipient string) {
	b.mu.Lock()
	active := b.activeTyping[recipient]
	b.mu.Unlock()
	if active {
		_ = b.clearTyping(ctx, recipient)
	}
}

func (b *signalBot) clearTyping(ctx context.Context, recipient string) error {
	if err := b.sendTypingState(ctx, recipient, true); err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.activeTyping, recipient)
	b.mu.Unlock()
	return nil
}

// ─── ReactionHandle ───────────────────────────────────────────────────────────

// AddReaction sends an emoji reaction via POST /v1/react.
// eventID must be of the form "signal-{sender}-{timestamp}".
func (b *signalBot) AddReaction(ctx context.Context, eventID, emoji string) error {
	// Parse sender and timestamp from eventID.
	if !strings.HasPrefix(eventID, "signal-") {
		return fmt.Errorf("signal: invalid eventID format %q", eventID)
	}
	parts := strings.SplitN(strings.TrimPrefix(eventID, "signal-"), "-", 2)
	if len(parts) != 2 {
		return fmt.Errorf("signal: invalid eventID format %q", eventID)
	}
	sender, timestamp := parts[0], parts[1]
	recipient, err := b.target(ctx)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{
		"recipient":             recipient,
		"reaction":              emoji,
		"target_author":         sender,
		"target_sent_timestamp": timestamp,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.apiURL+"/v1/react/"+b.account, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal react: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("signal react: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// RemoveReaction removes an emoji reaction by sending a remove flag.
func (b *signalBot) RemoveReaction(ctx context.Context, eventID, emoji string) error {
	if !strings.HasPrefix(eventID, "signal-") {
		return fmt.Errorf("signal: invalid eventID format %q", eventID)
	}
	parts := strings.SplitN(strings.TrimPrefix(eventID, "signal-"), "-", 2)
	if len(parts) != 2 {
		return fmt.Errorf("signal: invalid eventID format %q", eventID)
	}
	sender, timestamp := parts[0], parts[1]
	recipient, err := b.target(ctx)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{
		"recipient":             recipient,
		"reaction":              emoji,
		"target_author":         sender,
		"target_sent_timestamp": timestamp,
		"remove":                true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		b.apiURL+"/v1/react/"+b.account, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signal remove reaction: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("signal remove reaction: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
