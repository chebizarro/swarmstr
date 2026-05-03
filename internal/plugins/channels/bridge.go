package channels

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/plugins/runtime"
	"metiq/internal/plugins/sdk"
)

// BridgeOptions describes one OpenClaw channel registration to expose through
// Swarmstr's Go channel SDK.
type BridgeOptions struct {
	Host         *runtime.OpenClawPluginHost
	PluginID     string
	ChannelID    string
	ChannelType  string
	ConfigSchema map[string]any
	Capabilities sdk.ChannelCapabilities
	Raw          map[string]any
}

// PluginChannelBridge adapts an OpenClaw channel plugin to sdk.ChannelPlugin.
type PluginChannelBridge struct {
	channelID    string
	pluginID     string
	channelType  string
	host         *runtime.OpenClawPluginHost
	configSchema map[string]any
	capabilities sdk.ChannelCapabilities
	raw          map[string]any

	mu      sync.Mutex
	handles map[string]*PluginChannelHandle
}

var _ sdk.ChannelPlugin = (*PluginChannelBridge)(nil)
var _ sdk.ChannelPluginWithCapabilities = (*PluginChannelBridge)(nil)

// NewPluginChannelBridge creates a bridge for one OpenClaw channel capability.
func NewPluginChannelBridge(opts BridgeOptions) (*PluginChannelBridge, error) {
	if opts.Host == nil {
		return nil, fmt.Errorf("openclaw channel bridge host is required")
	}
	if strings.TrimSpace(opts.ChannelID) == "" {
		return nil, fmt.Errorf("openclaw channel bridge channel id is required")
	}
	caps := opts.Capabilities
	if rawCaps, ok := opts.Raw["capabilities"]; ok {
		caps = mergeCapabilities(caps, ParseCapabilities(rawCaps))
	}
	return &PluginChannelBridge{
		channelID:    opts.ChannelID,
		pluginID:     opts.PluginID,
		channelType:  firstNonEmpty(opts.ChannelType, opts.ChannelID),
		host:         opts.Host,
		configSchema: cloneMap(opts.ConfigSchema),
		capabilities: caps,
		raw:          cloneMap(opts.Raw),
		handles:      map[string]*PluginChannelHandle{},
	}, nil
}

// NewPluginChannelBridgeFromRegistration builds a bridge directly from an
// OpenClaw channel CapabilityRegistration captured by the Node.js host.
func NewPluginChannelBridgeFromRegistration(host *runtime.OpenClawPluginHost, reg runtime.CapabilityRegistration) (*PluginChannelBridge, error) {
	pluginID := reg.PluginID
	channelID := firstNonEmpty(reg.ID, stringFromMap(reg.Raw, "id"))
	return NewPluginChannelBridge(BridgeOptions{
		Host:         host,
		PluginID:     pluginID,
		ChannelID:    channelID,
		ChannelType:  firstNonEmpty(stringFromMap(reg.Raw, "channelType"), stringFromMap(reg.Raw, "type")),
		ConfigSchema: mapFromMap(reg.Raw, "configSchema"),
		Capabilities: ParseCapabilities(reg.Raw["capabilities"]),
		Raw:          reg.Raw,
	})
}

func (p *PluginChannelBridge) ID() string   { return p.channelID }
func (p *PluginChannelBridge) Type() string { return p.channelType }

func (p *PluginChannelBridge) ConfigSchema() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := p.host.InvokeChannel(ctx, p.channelID, "config_schema", nil)
	if err == nil {
		if schema, ok := result.(map[string]any); ok {
			return cloneMap(schema)
		}
	}
	return cloneMap(p.configSchema)
}

func (p *PluginChannelBridge) Capabilities() sdk.ChannelCapabilities {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := p.host.InvokeChannel(ctx, p.channelID, "capabilities", nil)
	if err == nil {
		return mergeCapabilities(p.capabilities, ParseCapabilities(result))
	}
	return p.capabilities
}

func (p *PluginChannelBridge) Connect(ctx context.Context, channelID string, cfg map[string]any, onMessage func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	if strings.TrimSpace(channelID) == "" {
		return nil, fmt.Errorf("channel instance id is required")
	}
	callbackID := fmt.Sprintf("%s:%s:%d", p.channelID, channelID, time.Now().UnixNano())
	p.host.RegisterCallback(callbackID, func(msg any) {
		if onMessage == nil {
			return
		}
		if inbound, ok := ParseInboundMessage(msg); ok {
			if inbound.ChannelID == "" {
				inbound.ChannelID = channelID
			}
			onMessage(inbound)
		}
	})

	result, err := p.host.InvokeChannel(ctx, p.channelID, "connect", map[string]any{
		"channel_id":  channelID,
		"config":      cloneMap(cfg),
		"callback_id": callbackID,
	})
	if err != nil {
		p.host.UnregisterCallback(callbackID)
		return nil, err
	}
	handleID := stringFromAnyMap(result, "handle_id")
	if handleID == "" {
		handleID = stringFromAnyMap(result, "handleId")
	}
	if handleID == "" {
		p.host.UnregisterCallback(callbackID)
		return nil, fmt.Errorf("openclaw channel %q connect returned no handle_id", p.channelID)
	}

	handle := &PluginChannelHandle{id: channelID, handleID: handleID, callbackID: callbackID, host: p.host, pluginID: p.pluginID, channelID: p.channelID}
	p.mu.Lock()
	p.handles[channelID] = handle
	p.mu.Unlock()
	return wrapHandleByCapabilities(handle, p.Capabilities()), nil
}

// PluginChannelHandle wraps a connected OpenClaw channel instance.
type PluginChannelHandle struct {
	id         string
	handleID   string
	callbackID string
	host       *runtime.OpenClawPluginHost
	pluginID   string
	channelID  string
}

var _ sdk.ChannelHandle = (*PluginChannelHandle)(nil)

func (h *PluginChannelHandle) ID() string { return h.id }

func (h *PluginChannelHandle) Send(ctx context.Context, text string) error {
	params := map[string]any{"text": text}
	if target := sdk.ChannelReplyTarget(ctx); target != "" {
		params["reply_target"] = target
	}
	_, err := h.host.InvokeChannel(ctx, h.handleID, "send", params)
	return err
}

func (h *PluginChannelHandle) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = h.host.InvokeChannel(ctx, h.handleID, "close", nil)
	h.host.UnregisterCallback(h.callbackID)
}

func (h *PluginChannelHandle) sendTyping(ctx context.Context, durationMS int) error {
	_, err := h.host.InvokeChannel(ctx, h.handleID, "send_typing", map[string]any{"duration_ms": durationMS})
	return err
}

func (h *PluginChannelHandle) addReaction(ctx context.Context, eventID, emoji string) error {
	_, err := h.host.InvokeChannel(ctx, h.handleID, "add_reaction", map[string]any{"event_id": eventID, "emoji": emoji})
	return err
}

func (h *PluginChannelHandle) removeReaction(ctx context.Context, eventID, emoji string) error {
	_, err := h.host.InvokeChannel(ctx, h.handleID, "remove_reaction", map[string]any{"event_id": eventID, "emoji": emoji})
	return err
}

func (h *PluginChannelHandle) sendInThread(ctx context.Context, threadID, text string) error {
	_, err := h.host.InvokeChannel(ctx, h.handleID, "send_in_thread", map[string]any{"thread_id": threadID, "text": text})
	return err
}

func (h *PluginChannelHandle) sendAudio(ctx context.Context, audio []byte, format string) error {
	_, err := h.host.InvokeChannel(ctx, h.handleID, "send_audio", map[string]any{"audio_base64": base64.StdEncoding.EncodeToString(audio), "format": format})
	return err
}

func (h *PluginChannelHandle) editMessage(ctx context.Context, eventID, newText string) error {
	_, err := h.host.InvokeChannel(ctx, h.handleID, "edit_message", map[string]any{"event_id": eventID, "text": newText})
	return err
}

// WebhookRequest is a JSON-safe HTTP callback envelope for webhook-backed
// OpenClaw channels. HTTP mounting code can pass requests here without needing
// to understand the plugin's platform-specific payload.
type WebhookRequest struct {
	Method  string              `json:"method,omitempty"`
	Path    string              `json:"path,omitempty"`
	Query   map[string][]string `json:"query,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

// WebhookResult is the optional response returned by a webhook channel plugin.
type WebhookResult struct {
	StatusCode int               `json:"status_code,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body,omitempty"`
	Payload    any               `json:"payload,omitempty"`
}

func (h *PluginChannelHandle) HandleWebhook(ctx context.Context, req WebhookRequest) (WebhookResult, error) {
	result, err := h.host.InvokeChannel(ctx, h.handleID, "webhook", map[string]any{
		"method":      req.Method,
		"path":        req.Path,
		"query":       req.Query,
		"headers":     req.Headers,
		"body":        string(req.Body),
		"body_base64": base64.StdEncoding.EncodeToString(req.Body),
	})
	if err != nil {
		return WebhookResult{}, err
	}
	return parseWebhookResult(result), nil
}

// ParseInboundMessage converts JSON-safe Node.js channel messages into the SDK
// type used by native Go channels.
func ParseInboundMessage(msg any) (sdk.InboundChannelMessage, bool) {
	m, ok := msg.(map[string]any)
	if !ok {
		return sdk.InboundChannelMessage{}, false
	}
	created := int64FromMap(m, "created_at")
	if created == 0 {
		created = int64FromMap(m, "createdAt")
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	in := sdk.InboundChannelMessage{
		ChannelID:      firstNonEmpty(stringFromMap(m, "channel_id"), stringFromMap(m, "channelId")),
		SenderID:       firstNonEmpty(stringFromMap(m, "sender_id"), stringFromMap(m, "senderId"), stringFromMap(m, "from"), stringFromMap(m, "from_id")),
		Text:           firstNonEmpty(stringFromMap(m, "text"), stringFromMap(m, "content"), stringFromMap(m, "message")),
		EventID:        firstNonEmpty(stringFromMap(m, "event_id"), stringFromMap(m, "eventId"), stringFromMap(m, "message_id"), stringFromMap(m, "messageId")),
		CreatedAt:      created,
		ThreadID:       firstNonEmpty(stringFromMap(m, "thread_id"), stringFromMap(m, "threadId"), stringFromMap(m, "conversation_id"), stringFromMap(m, "conversationId")),
		ReplyToEventID: firstNonEmpty(stringFromMap(m, "reply_to_event_id"), stringFromMap(m, "replyToEventId"), stringFromMap(m, "reply_to"), stringFromMap(m, "replyTo")),
		MediaURL:       firstNonEmpty(stringFromMap(m, "media_url"), stringFromMap(m, "mediaUrl")),
		MediaMIME:      firstNonEmpty(stringFromMap(m, "media_mime"), stringFromMap(m, "mediaMime"), stringFromMap(m, "media_type"), stringFromMap(m, "mediaType")),
	}
	return in, in.ChannelID != "" || in.SenderID != "" || in.Text != "" || in.EventID != ""
}

// ParseCapabilities converts JSON-safe channel capability metadata into the SDK type.
func ParseCapabilities(raw any) sdk.ChannelCapabilities {
	m, ok := raw.(map[string]any)
	if !ok {
		return sdk.ChannelCapabilities{}
	}
	return sdk.ChannelCapabilities{
		Typing:        boolFromMap(m, "typing", "send_typing", "sendTyping", "typingIndicators", "startTyping"),
		Reactions:     boolFromMap(m, "reactions", "reaction", "addReaction", "emojiReactions"),
		Threads:       boolFromMap(m, "threads", "threadReplies", "sendInThread", "thread_replies"),
		Audio:         boolFromMap(m, "audio", "voice", "sendAudio"),
		Edit:          boolFromMap(m, "edit", "edits", "editMessage"),
		MultiAccount:  boolFromMap(m, "multiAccount", "multi_account"),
		E2EEncryption: boolFromMap(m, "e2eEncryption", "e2e_encryption", "encrypted"),
	}
}

func parseWebhookResult(raw any) WebhookResult {
	m, ok := raw.(map[string]any)
	if !ok {
		return WebhookResult{Payload: raw}
	}
	body := []byte(stringFromMap(m, "body"))
	if b64 := stringFromMap(m, "body_base64"); b64 != "" {
		if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
			body = decoded
		}
	}
	return WebhookResult{
		StatusCode: int(int64FromMap(m, "status_code")),
		Headers:    stringMapFromMap(m, "headers"),
		Body:       body,
		Payload:    m["payload"],
	}
}

func mergeCapabilities(a, b sdk.ChannelCapabilities) sdk.ChannelCapabilities {
	return sdk.ChannelCapabilities{
		Typing:        a.Typing || b.Typing,
		Reactions:     a.Reactions || b.Reactions,
		Threads:       a.Threads || b.Threads,
		Audio:         a.Audio || b.Audio,
		Edit:          a.Edit || b.Edit,
		MultiAccount:  a.MultiAccount || b.MultiAccount,
		E2EEncryption: a.E2EEncryption || b.E2EEncryption,
	}
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mapFromMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return cloneMap(v)
}

func stringMapFromMap(m map[string]any, key string) map[string]string {
	raw, ok := m[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func stringFromAnyMap(v any, key string) string {
	m, _ := v.(map[string]any)
	return stringFromMap(m, key)
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func boolFromMap(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch b := v.(type) {
			case bool:
				return b
			case string:
				return strings.EqualFold(b, "true") || b == "1" || strings.EqualFold(b, "yes")
			}
		}
	}
	return false
}

func int64FromMap(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case string:
		var out int64
		_, _ = fmt.Sscanf(v, "%d", &out)
		return out
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
