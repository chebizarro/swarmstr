package acp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ── Runtime handle ──────────────────────────────────────────────────────────

// RuntimeHandle is returned by EnsureSession and passed to subsequent calls.
type RuntimeHandle struct {
	// SessionKey is the canonical session identifier.
	SessionKey string `json:"session_key"`
	// Backend is the backend ID that owns this session.
	Backend string `json:"backend"`
	// RuntimeSessionName is the backend-local session name.
	RuntimeSessionName string `json:"runtime_session_name"`
	// CWD is the effective working directory for this session (optional).
	CWD string `json:"cwd,omitempty"`
	// AcpxRecordID is a backend-local record identifier (optional).
	AcpxRecordID string `json:"acpx_record_id,omitempty"`
}

// ── Session mode ────────────────────────────────────────────────────────────

// SessionMode controls session lifecycle.
type SessionMode string

const (
	// SessionModePersistent keeps the session alive across turns.
	SessionModePersistent SessionMode = "persistent"
	// SessionModeOneshot destroys the session after a single turn.
	SessionModeOneshot SessionMode = "oneshot"
)

// ── Runtime inputs ──────────────────────────────────────────────────────────

// EnsureInput is the input for creating or resuming a session.
type EnsureInput struct {
	SessionKey      string
	Agent           string
	Mode            SessionMode
	ResumeSessionID string // optional: resume a specific session
	CWD             string // optional: working directory override
	Env             map[string]string
}

// TurnAttachment is a file or data attachment for a turn.
type TurnAttachment struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"` // base64-encoded
}

// TurnInput is the input for running a turn in a session.
type TurnInput struct {
	Handle      RuntimeHandle
	Text        string
	Mode        string // "prompt" or "steer"
	RequestID   string
	Attachments []TurnAttachment
}

// CancelInput is the input for cancelling a running turn.
type CancelInput struct {
	Handle RuntimeHandle
	Reason string
}

// CloseInput is the input for closing a session.
type CloseInput struct {
	Handle                 RuntimeHandle
	Reason                 string
	DiscardPersistentState bool // if true, the session store should mark the key fresh
}

// ── Runtime events ──────────────────────────────────────────────────────────

// EventKind discriminates runtime event types.
type EventKind string

const (
	EventTextDelta EventKind = "text_delta"
	EventStatus    EventKind = "status"
	EventToolCall  EventKind = "tool_call"
	EventDone      EventKind = "done"
	EventError     EventKind = "error"
)

// IsTerminal reports whether this event kind signals the end of a turn.
func (k EventKind) IsTerminal() bool {
	return k == EventDone || k == EventError
}

// RuntimeEvent is a single event emitted during a turn.
type RuntimeEvent struct {
	Kind EventKind

	// Text carries content for text_delta, status, tool_call, and error events.
	Text string
	// Stream is "output" or "thought" for text_delta events.
	Stream string
	// Tag is an optional categorization hint (e.g. "agent_message_chunk").
	Tag string
	// StopReason is set for done events.
	StopReason string
	// Code is a machine-readable error code for error events.
	Code string
	// Retryable indicates whether an error event is retryable.
	Retryable bool
	// ToolCallID identifies a tool call for tool_call events.
	ToolCallID string
	// Title is a display title for tool_call events.
	Title string
	// Used/Size are progress indicators for status events.
	Used, Size int
}

// ── Runtime interface ───────────────────────────────────────────────────────

// BackendRuntime is the interface that ACP runtime backends must implement.
// Named BackendRuntime (not Runtime) to avoid collision with agent.Runtime.
type BackendRuntime interface {
	// EnsureSession creates or resumes a session.
	EnsureSession(ctx context.Context, input EnsureInput) (RuntimeHandle, error)
	// RunTurn executes a turn and returns a channel of events.
	// The implementation sends 0..N non-terminal events followed by exactly
	// one terminal event (EventDone or EventError), then closes the channel.
	RunTurn(ctx context.Context, input TurnInput) (<-chan RuntimeEvent, error)
	// Cancel aborts an in-progress turn.
	Cancel(ctx context.Context, input CancelInput) error
	// Close terminates a session.
	Close(ctx context.Context, input CloseInput) error
}

// ── Optional runtime capability interfaces ──────────────────────────────────

// RuntimeCapabilities describes what controls a backend supports.
type RuntimeCapabilities struct {
	// Controls lists supported control operations (e.g. "session/set_mode").
	Controls []string
	// ConfigOptionKeys lists supported config option keys (nil = accepts any).
	ConfigOptionKeys []string
}

// RuntimeControl describes a runtime-specific control operation to apply before
// a managed session turn (for example, setting mode or model options).
type RuntimeControl struct {
	Name    string         `json:"name"`
	Options map[string]any `json:"options,omitempty"`
}

// RuntimeControlInput is passed to runtimes that support manager-applied controls.
type RuntimeControlInput struct {
	Handle   RuntimeHandle    `json:"handle"`
	Controls []RuntimeControl `json:"controls,omitempty"`
}

// RuntimeControlApplier is optionally implemented by backends that support
// runtime controls coordinated by Manager.
type RuntimeControlApplier interface {
	ApplyRuntimeControls(ctx context.Context, input RuntimeControlInput) error
}

// CapabilitiesProvider is optionally implemented by backends that advertise
// their supported controls and config options.
type CapabilitiesProvider interface {
	GetCapabilities(ctx context.Context, handle *RuntimeHandle) (RuntimeCapabilities, error)
}

// RuntimeStatus is the status of a backend session.
type RuntimeStatus struct {
	Summary          string
	AcpxRecordID     string
	BackendSessionID string
	Details          map[string]any
}

// StatusProvider is optionally implemented by backends that can report
// session status.
type StatusProvider interface {
	GetStatus(ctx context.Context, handle RuntimeHandle) (RuntimeStatus, error)
}

// ── Backend entry ───────────────────────────────────────────────────────────

// BackendEntry describes a registered ACP runtime backend.
type BackendEntry struct {
	// ID is the normalized backend identifier.
	ID string
	// Runtime is the backend implementation.
	Runtime BackendRuntime
	// Healthy is an optional health check function. Nil means always healthy.
	Healthy func() bool
}

// isHealthy evaluates the backend's health.
func (e *BackendEntry) isHealthy() bool {
	if e.Healthy == nil {
		return true
	}
	defer func() { recover() }() // guard against panicking health checks
	return e.Healthy()
}

// ── Backend registry ────────────────────────────────────────────────────────

// Sentinel errors for backend lookup failures.
var (
	ErrBackendMissing     = errors.New("ACP runtime backend not configured")
	ErrBackendUnavailable = errors.New("ACP runtime backend unavailable")
)

// BackendRegistry manages ACP runtime backends by ID.
// All methods are goroutine-safe.
type BackendRegistry struct {
	mu      sync.RWMutex
	entries map[string]*BackendEntry // key: normalized id
}

// NewBackendRegistry creates an empty BackendRegistry.
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{entries: make(map[string]*BackendEntry)}
}

func normalizeBackendID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// Register adds or replaces a backend. Returns an error if ID or Runtime is missing.
func (r *BackendRegistry) Register(e BackendEntry) error {
	id := normalizeBackendID(e.ID)
	if id == "" {
		return fmt.Errorf("acp backend: id required")
	}
	if e.Runtime == nil {
		return fmt.Errorf("acp backend %q: runtime required", id)
	}
	e.ID = id
	r.mu.Lock()
	r.entries[id] = &e
	r.mu.Unlock()
	return nil
}

// Unregister removes a backend by ID.
func (r *BackendRegistry) Unregister(id string) {
	r.mu.Lock()
	delete(r.entries, normalizeBackendID(id))
	r.mu.Unlock()
}

// Get returns the backend entry by ID, or (nil, false) if not found.
func (r *BackendRegistry) Get(id string) (*BackendEntry, bool) {
	r.mu.RLock()
	e, ok := r.entries[normalizeBackendID(id)]
	r.mu.RUnlock()
	return e, ok
}

// GetHealthy returns the first healthy backend. If none are healthy, returns
// the first registered backend. Returns (nil, false) if the registry is empty.
func (r *BackendRegistry) GetHealthy() (*BackendEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.entries) == 0 {
		return nil, false
	}

	var first *BackendEntry
	for _, e := range r.entries {
		if first == nil {
			first = e
		}
		if e.isHealthy() {
			return e, true
		}
	}
	return first, true
}

// Require returns a backend by ID, or auto-selects a healthy backend if id is empty.
// Returns ErrBackendMissing if no backend matches, or ErrBackendUnavailable if
// the matching backend is unhealthy.
func (r *BackendRegistry) Require(id string) (*BackendEntry, error) {
	normalized := normalizeBackendID(id)
	if normalized == "" {
		e, ok := r.GetHealthy()
		if !ok {
			return nil, ErrBackendMissing
		}
		if !e.isHealthy() {
			return nil, ErrBackendUnavailable
		}
		return e, nil
	}

	e, ok := r.Get(normalized)
	if !ok {
		return nil, fmt.Errorf("%w: %q not registered", ErrBackendMissing, normalized)
	}
	if !e.isHealthy() {
		return nil, fmt.Errorf("%w: %q", ErrBackendUnavailable, normalized)
	}
	return e, nil
}

// List returns all registered backends.
func (r *BackendRegistry) List() []BackendEntry {
	r.mu.RLock()
	out := make([]BackendEntry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, *e)
	}
	r.mu.RUnlock()
	return out
}

// Count returns the number of registered backends.
func (r *BackendRegistry) Count() int {
	r.mu.RLock()
	n := len(r.entries)
	r.mu.RUnlock()
	return n
}

// ── Package-level default registry ──────────────────────────────────────────

var defaultBackendRegistry = NewBackendRegistry()

// RegisterBackend adds a backend to the default registry.
func RegisterBackend(e BackendEntry) error { return defaultBackendRegistry.Register(e) }

// UnregisterBackend removes a backend from the default registry.
func UnregisterBackend(id string) { defaultBackendRegistry.Unregister(id) }

// GetBackend retrieves a backend from the default registry.
func GetBackend(id string) (*BackendEntry, bool) { return defaultBackendRegistry.Get(id) }

// RequireBackend retrieves a backend from the default registry, returning an
// error if it's missing or unhealthy.
func RequireBackend(id string) (*BackendEntry, error) { return defaultBackendRegistry.Require(id) }

// ResetDefaultBackendRegistry clears the default registry (for testing).
func ResetDefaultBackendRegistry() {
	defaultBackendRegistry.mu.Lock()
	defaultBackendRegistry.entries = make(map[string]*BackendEntry)
	defaultBackendRegistry.mu.Unlock()
}
