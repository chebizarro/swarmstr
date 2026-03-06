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

import "context"

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

// ToolSchema describes a single tool that the plugin exposes to the agent.
type ToolSchema struct {
	// Name is the tool identifier used in tool-call dispatch.
	Name string `json:"name"`

	// Description is surfaced to the LLM to help it decide when to use the tool.
	Description string `json:"description"`

	// Parameters is a JSON Schema object describing the tool's arguments.
	Parameters map[string]any `json:"parameters,omitempty"`
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
