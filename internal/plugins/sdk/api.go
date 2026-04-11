// Package sdk defines the host API surface exposed to Goja (JS) plugins.
//
// Plugins receive a single object – the "host" – whose namespaced methods
// are described here as Go interfaces.  The Goja runtime wires concrete
// implementations of these interfaces into the JS global scope.
//
// JS plugin entry-point contract:
//
//	exports.manifest = {
//	    id:          "my-plugin",
//	    version:     "1.0.0",
//	    description: "what this plugin does",
//	    tools:       [{ name: "tool_name", description: "...", parameters: {...} }],
//	};
//
//	exports.invoke = async function(toolName, args, ctx) {
//	    // return any JSON-serialisable value
//	};
package sdk

import (
	"context"
	"fmt"
	"strings"
)

type contextKey string

const channelReplyTargetContextKey contextKey = "channel-reply-target"

// WithChannelReplyTarget stores a channel-specific recipient/session hint in ctx.
// ChannelHandle implementations that need explicit recipient routing (for
// example email) can read this value in Send() to avoid cross-session races.
func WithChannelReplyTarget(ctx context.Context, target string) context.Context {
	if ctx == nil || target == "" {
		return ctx
	}
	return context.WithValue(ctx, channelReplyTargetContextKey, target)
}

// ChannelReplyTarget returns the recipient/session hint previously set by
// WithChannelReplyTarget.
func ChannelReplyTarget(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(channelReplyTargetContextKey).(string)
	return v
}

// ─── Host API namespaces ──────────────────────────────────────────────────────

// NostrHost provides Nostr I/O to a plugin.
type NostrHost interface {
	// Publish broadcasts a signed Nostr event.  The map must be a valid NIP-01
	// event object; the host signs it with the agent's key.
	Publish(ctx context.Context, event map[string]any) error

	// FetchOne fetches up to limit events matching filter from configured relays.
	// filter follows NIP-01 REQ filter schema (kinds, authors, #e, etc.).
	Fetch(ctx context.Context, filter map[string]any, limit int) ([]map[string]any, error)

	// Encrypt encrypts content for recipientPubkey using NIP-04.
	Encrypt(ctx context.Context, recipientPubkey, content string) (string, error)

	// Decrypt decrypts a NIP-04 encrypted payload from senderPubkey.
	Decrypt(ctx context.Context, senderPubkey, ciphertext string) (string, error)
}

// ConfigHost provides read-only config access to a plugin.
// Plugins may not write config; that requires a control-plane event.
type ConfigHost interface {
	// Get returns the value at dot-notation key (e.g. "agent.default_model").
	// Returns nil if the key does not exist.
	Get(key string) any
}

// HTTPHost provides outbound HTTP to a plugin.
type HTTPHost interface {
	// Get performs an HTTP GET. Returns (statusCode, bodyBytes, error).
	Get(ctx context.Context, url string, headers map[string]string) (int, []byte, error)

	// Post performs an HTTP POST with body. Returns (statusCode, bodyBytes, error).
	Post(ctx context.Context, url string, body []byte, headers map[string]string) (int, []byte, error)
}

// StorageHost provides durable key-value storage scoped to a plugin.
type StorageHost interface {
	// Get returns the stored value for key, or nil if not set.
	Get(ctx context.Context, key string) ([]byte, error)

	// Set stores value under key.
	Set(ctx context.Context, key string, value []byte) error

	// Del removes key.
	Del(ctx context.Context, key string) error
}

// LogHost provides structured logging from plugin code.
type LogHost interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// AgentHost provides LLM completion from within a plugin.
type AgentHost interface {
	// Complete sends prompt to the configured LLM and returns the text reply.
	Complete(ctx context.Context, prompt string, opts CompletionOpts) (string, error)
}

// ─── Supporting types ─────────────────────────────────────────────────────────

// CompletionOpts controls how a plugin-initiated completion is run.
type CompletionOpts struct {
	// Model overrides the default model for this call.  Empty = use default.
	Model string `json:"model,omitempty"`

	// SystemPrompt prepended before the user prompt.
	SystemPrompt string `json:"system_prompt,omitempty"`

	// MaxTokens limits the response length.  0 = provider default.
	MaxTokens int `json:"max_tokens,omitempty"`
}

// ─── Host bundle ─────────────────────────────────────────────────────────────

// Host bundles all namespace APIs passed to a plugin VM on initialisation.
type Host struct {
	Nostr   NostrHost
	Config  ConfigHost
	HTTP    HTTPHost
	Storage StorageHost
	Log     LogHost
	Agent   AgentHost
}

// ─── Plugin manifest & invocation ────────────────────────────────────────────

// Manifest describes a Goja plugin (read from exports.manifest in the script).
type Manifest struct {
	ID          string       `json:"id"`
	Version     string       `json:"version"`
	Description string       `json:"description,omitempty"`
	Tools       []ToolSchema `json:"tools,omitempty"`
}

// ValidateManifest checks the basic plugin manifest contract expected by the
// runtime and tool registry.
func ValidateManifest(m Manifest) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("manifest.id is required")
	}
	seenTools := make(map[string]struct{}, len(m.Tools))
	for i, tool := range m.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return fmt.Errorf("manifest.tools[%d].name is required", i)
		}
		if _, exists := seenTools[name]; exists {
			return fmt.Errorf("manifest.tools[%d].name %q is duplicated", i, name)
		}
		seenTools[name] = struct{}{}
		if err := ValidateToolSchema(tool); err != nil {
			return fmt.Errorf("manifest.tools[%d]: %w", i, err)
		}
	}
	return nil
}

// ToolSchema describes a single tool that the plugin exposes to the agent.
type ToolSchema struct {
	// Name is the tool identifier used in tool-call dispatch.
	Name string `json:"name"`

	// Description is surfaced to the LLM to help it decide when to use the tool.
	Description string `json:"description"`

	// Parameters is a JSON Schema object describing the tool's arguments.
	Parameters map[string]any `json:"parameters,omitempty"`
}

// ValidateToolSchema checks a plugin tool declaration for structural problems
// that would break prompt exposure or runtime schema validation.
func ValidateToolSchema(tool ToolSchema) error {
	if strings.TrimSpace(tool.Name) == "" {
		return fmt.Errorf("tool name is required")
	}
	if len(tool.Parameters) == 0 {
		return nil
	}
	if rawType, ok := tool.Parameters["type"]; ok {
		typeName, ok := rawType.(string)
		if !ok || strings.TrimSpace(typeName) == "" {
			return fmt.Errorf("parameters.type must be a non-empty string")
		}
		if !strings.EqualFold(typeName, "object") {
			return fmt.Errorf("parameters.type must be object")
		}
	}
	if rawProps, ok := tool.Parameters["properties"]; ok {
		if _, ok := rawProps.(map[string]any); !ok {
			return fmt.Errorf("parameters.properties must be an object")
		}
	}
	if rawRequired, ok := tool.Parameters["required"]; ok {
		switch vals := rawRequired.(type) {
		case []string:
			for _, item := range vals {
				if strings.TrimSpace(item) == "" {
					return fmt.Errorf("parameters.required must not contain empty items")
				}
			}
		case []any:
			for _, item := range vals {
				s, ok := item.(string)
				if !ok || strings.TrimSpace(s) == "" {
					return fmt.Errorf("parameters.required must be an array of strings")
				}
			}
		default:
			return fmt.Errorf("parameters.required must be an array of strings")
		}
	}
	return nil
}

// InvokeRequest is the structured call passed to exports.invoke().
type InvokeRequest struct {
	// Tool is the tool name selected by the LLM.
	Tool string `json:"tool"`

	// Args is the validated, parsed argument map.
	Args map[string]any `json:"args"`

	// Meta carries optional caller metadata (session_id, caller_pubkey, etc.).
	Meta map[string]any `json:"meta,omitempty"`
}

// InvokeResult wraps the return value from exports.invoke().
type InvokeResult struct {
	// Value is any JSON-serialisable return value.
	Value any `json:"value"`

	// Error is set when the JS function threw or returned an Error object.
	Error string `json:"error,omitempty"`
}

// ─── Channel plugin interface ─────────────────────────────────────────────────

// InboundChannelMessage is a normalised inbound message delivered by a channel plugin.
// It mirrors channels.InboundMessage but is defined here to avoid import cycles.
type InboundChannelMessage struct {
	// ChannelID is the registry key for the channel instance.
	ChannelID string
	// SenderID is a string identifier for the sender (platform-specific).
	SenderID string
	// Text is the plain-text content of the message.
	Text string
	// EventID is an optional platform-native message ID.
	EventID string
	// CreatedAt is the UNIX timestamp of the message.
	CreatedAt int64
	// ThreadID is an optional thread or conversation ID (platform-specific).
	ThreadID string
	// ReplyToEventID is the EventID of the message being replied to, if any.
	ReplyToEventID string
	// MediaURL is an optional URL of an attached media file (image, audio, etc.).
	MediaURL string
	// MediaMIME is the MIME type of the attached media, if present.
	MediaMIME string
}

// ─── Channel capabilities ─────────────────────────────────────────────────────

// ChannelCapabilities declares which optional features a channel plugin supports.
// Callers check capabilities before invoking optional ChannelHandle methods.
type ChannelCapabilities struct {
	// Typing indicates the channel supports sending typing indicators.
	Typing bool
	// Reactions indicates the channel supports adding emoji reactions to messages.
	Reactions bool
	// Threads indicates the channel supports threaded replies.
	Threads bool
	// Audio indicates the channel can deliver voice/audio messages.
	Audio bool
	// Edit indicates the channel supports editing previously sent messages.
	Edit bool
	// MultiAccount indicates the plugin supports multiple account instances.
	MultiAccount bool
	// E2EEncryption indicates the channel supports end-to-end encrypted messages
	// via NIP-44. When true the runtime will wrap the handle with EncryptedHandle.
	E2EEncryption bool
}

// ChannelPluginWithCapabilities is an optional extension of ChannelPlugin.
// Plugins that implement this interface advertise their capabilities so the
// dispatcher can gate optional features without runtime errors.
type ChannelPluginWithCapabilities interface {
	ChannelPlugin
	// Capabilities returns the feature set supported by this plugin.
	Capabilities() ChannelCapabilities
}

// ─── Optional ChannelHandle extensions ───────────────────────────────────────
//
// These interfaces are optional extensions for ChannelHandle.  The dispatcher
// uses interface assertions to check for support before calling them.  Existing
// plugins that only implement ChannelHandle (Send + Close) continue to work.

// TypingHandle is implemented by channels that support typing indicators.
type TypingHandle interface {
	ChannelHandle
	// SendTyping sends a typing indicator to the channel.  Duration hints how
	// long the indicator should be displayed; 0 means "brief".
	SendTyping(ctx context.Context, durationMS int) error
}

// ReactionHandle is implemented by channels that support emoji reactions.
type ReactionHandle interface {
	ChannelHandle
	// AddReaction attaches an emoji reaction to a message.
	// eventID is the platform-native ID of the target message.
	AddReaction(ctx context.Context, eventID, emoji string) error
	// RemoveReaction removes a previously added reaction.
	RemoveReaction(ctx context.Context, eventID, emoji string) error
}

// AudioHandle is implemented by channels that can deliver audio messages.
type AudioHandle interface {
	ChannelHandle
	// SendAudio delivers raw audio bytes to the channel.
	// format is the audio format (e.g. "mp3", "ogg", "wav").
	SendAudio(ctx context.Context, audio []byte, format string) error
}

// EditHandle is implemented by channels that support message editing.
type EditHandle interface {
	ChannelHandle
	// EditMessage replaces the content of a previously sent message.
	EditMessage(ctx context.Context, eventID, newText string) error
}

// ThreadHandle is implemented by channels that support threaded replies.
type ThreadHandle interface {
	ChannelHandle
	// SendInThread sends a reply within an existing thread.
	// threadID is the platform-native thread or conversation ID.
	SendInThread(ctx context.Context, threadID, text string) error
}

// ChannelPlugin is the factory interface for an external channel integration.
//
// Built-in implementations (e.g. telegram, discord) call RegisterChannelPlugin
// in their package init() functions.  User-installed JS plugins can also declare
// a channel plugin by exporting a channelPlugin object in their manifest.
//
// Lifecycle for each configured channel instance:
//
//	plugin.Connect(ctx, channelID, cfg, onMessage) → Channel
//	channel.Send(ctx, text) for outbound
//	channel.Close() on shutdown
type ChannelPlugin interface {
	// ID returns the unique plugin identifier, e.g. "telegram" or "discord".
	ID() string

	// Type returns a human-readable name, e.g. "Telegram Bot".
	Type() string

	// ConfigSchema returns a JSON Schema object describing the configuration
	// fields required to set up a channel instance of this type.  This is
	// surfaced via config.schema.lookup and used by the setup wizard.
	ConfigSchema() map[string]any

	// Connect creates and starts a channel instance.  cfg is the per-channel
	// configuration map (token, webhook_url, etc.).  onMessage is called for
	// each inbound message.  The returned ChannelHandle must be closed on
	// daemon shutdown.
	Connect(ctx context.Context, channelID string, cfg map[string]any, onMessage func(InboundChannelMessage)) (ChannelHandle, error)
}

// ChannelHandle represents a running channel instance.
type ChannelHandle interface {
	// ID returns the channel instance identifier.
	ID() string

	// Send posts a text message from the agent to the channel.
	Send(ctx context.Context, text string) error

	// Close terminates the channel subscription and frees resources.
	Close()
}

// GatewayMethod is an additional gateway RPC method contributed by a channel plugin.
type GatewayMethod struct {
	// Method is the full method name, e.g. "telegram.send".
	Method string
	// Description is a human-readable description for documentation.
	Description string
	// Handle is called when the method is invoked.
	Handle func(ctx context.Context, params map[string]any) (map[string]any, error)
}

// ChannelPluginWithMethods is an optional extension of ChannelPlugin that allows
// a channel plugin to register additional gateway methods.
type ChannelPluginWithMethods interface {
	ChannelPlugin
	// GatewayMethods returns extra methods to register on the gateway.
	GatewayMethods() []GatewayMethod
}
