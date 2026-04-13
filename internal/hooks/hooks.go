package hooks

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// HookManifest is the parsed YAML frontmatter from a HOOK.md file.
type HookManifest struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description"`
	Homepage    string        `yaml:"homepage"`
	Metadata    *HookMetaWrap `yaml:"metadata"`
	Body        string        `yaml:"-"` // markdown body after frontmatter
}

// HookMetaWrap is the `metadata:` block.
// HookMetaWrap is the `metadata:` block.
type HookMetaWrap struct {
	OpenClaw *OpenClawHookMeta `yaml:"openclaw"`
}

// OpenClawHookMeta is the `metadata.openclaw:` block.
// OpenClawHookMeta is the `metadata.openclaw:` block.
type OpenClawHookMeta struct {
	Emoji    string            `yaml:"emoji"`
	Events   []string          `yaml:"events"`
	Always   bool              `yaml:"always"`
	Requires *HookRequires     `yaml:"requires"`
	Install  []HookInstallSpec `yaml:"install"`
}

// HookRequires lists requirements for a hook to be eligible.
// HookRequires lists requirements for a hook to be eligible.
type HookRequires struct {
	Bins    []string `yaml:"bins"`
	AnyBins []string `yaml:"anyBins"`
	Env     []string `yaml:"env"`
	Config  []string `yaml:"config"`
	OS      []string `yaml:"os"`
}

// HookInstallSpec describes how to install/locate a hook.
// HookInstallSpec describes how to install/locate a hook.
type HookInstallSpec struct {
	ID         string   `yaml:"id"`
	Kind       string   `yaml:"kind"` // "bundled" | "npm" | "git"
	Label      string   `yaml:"label"`
	Package    string   `yaml:"package"`
	Repository string   `yaml:"repository"`
	Bins       []string `yaml:"bins"`
}

// ────────────────────────────────────────────────────────────────────────────
// Hook runtime struct
// ────────────────────────────────────────────────────────────────────────────

// Source indicates where the hook was loaded from.
// Source indicates where the hook was loaded from.
type Source string

const (
	SourceBundled   Source = "metiq-bundled"
	SourceManaged   Source = "metiq-managed"
	SourceWorkspace Source = "metiq-workspace"
)

// Hook is a loaded hook definition.
// Hook is a loaded hook definition.
type Hook struct {
	// HookKey is the canonical ID (directory name for HOOK.md hooks).
	HookKey  string
	Manifest HookManifest
	Source   Source
	FilePath string // path to HOOK.md
	BaseDir  string // containing directory

	// Handler is the Go function that executes this hook.
	// Nil for hooks that have no bundled Go handler.
	Handler HookHandler
}

// Emoji returns the display emoji or a fallback.
// Emoji returns the display emoji or a fallback.
func (h *Hook) Emoji() string {
	if h.Manifest.Metadata != nil && h.Manifest.Metadata.OpenClaw != nil {
		if e := h.Manifest.Metadata.OpenClaw.Emoji; e != "" {
			return e
		}
	}
	return "🪝"
}

// Events returns the event list this hook subscribes to.
// Events returns the event list this hook subscribes to.
func (h *Hook) Events() []string {
	if h.Manifest.Metadata != nil && h.Manifest.Metadata.OpenClaw != nil {
		return h.Manifest.Metadata.OpenClaw.Events
	}
	return nil
}

// Always reports whether the hook fires even when disabled.
// Always reports whether the hook fires even when disabled.
func (h *Hook) Always() bool {
	if h.Manifest.Metadata != nil && h.Manifest.Metadata.OpenClaw != nil {
		return h.Manifest.Metadata.OpenClaw.Always
	}
	return false
}

// Requires returns the hook's requirements (may be nil).
// Requires returns the hook's requirements (may be nil).
func (h *Hook) Requires() *HookRequires {
	if h.Manifest.Metadata != nil && h.Manifest.Metadata.OpenClaw != nil {
		return h.Manifest.Metadata.OpenClaw.Requires
	}
	return nil
}

// InstallSpecs returns the install specifications.
// InstallSpecs returns the install specifications.
func (h *Hook) InstallSpecs() []HookInstallSpec {
	if h.Manifest.Metadata != nil && h.Manifest.Metadata.OpenClaw != nil {
		return h.Manifest.Metadata.OpenClaw.Install
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// Event / Handler
// ────────────────────────────────────────────────────────────────────────────

// Event carries the data passed to hook handlers.
// Event carries the data passed to hook handlers.
type Event struct {
	// EventType is the top-level type e.g. "command", "agent", "gateway".
	EventType string
	// Action is the sub-action e.g. "new", "reset", "bootstrap", "startup".
	Action string
	// Full event name is EventType + ":" + Action (or just EventType if no action).
	Name string
	// SessionKey identifies the active session.
	SessionKey string
	// Context holds arbitrary key-value data for the event.
	Context map[string]any
	// Timestamp is when the event was fired.
	Timestamp time.Time
	// Messages is a slice the handler can push user-facing messages into.
	Messages []string
}

// HookHandler is the signature of a bundled Go hook implementation.
// HookHandler is the signature of a bundled Go hook implementation.
type HookHandler func(event *Event) error

// ────────────────────────────────────────────────────────────────────────────
// Manager
// ────────────────────────────────────────────────────────────────────────────

// Manager manages loaded hooks and dispatches events.
// Manager manages loaded hooks and dispatches events.
type Manager struct {
	mu      sync.RWMutex
	hooks   []*Hook         // all loaded hooks
	enabled map[string]bool // overrides: hookKey → enabled
}

// NewManager creates an empty Manager.
// NewManager creates an empty Manager.
func NewManager() *Manager {
	return &Manager{enabled: map[string]bool{}}
}

// Register adds a hook to the manager.
// Register adds a hook to the manager.
func (m *Manager) Register(h *Hook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = append(m.hooks, h)
}

// SetEnabled persists an enable/disable override for hookKey.
// SetEnabled persists an enable/disable override for hookKey.
func (m *Manager) SetEnabled(hookKey string, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled[hookKey] = enabled
}

// isEnabled returns true if the hook should fire (default: enabled for bundled).
// isEnabled returns true if the hook should fire (default: enabled for bundled).
func (m *Manager) isEnabled(h *Hook) bool {
	if v, ok := m.enabled[h.HookKey]; ok {
		return v
	}
	// Bundled hooks default to enabled.
	return h.Source == SourceBundled
}

// Fire dispatches eventName to all eligible hooks that subscribe to it.
// It collects handler errors but continues dispatching to remaining hooks.
// Fire dispatches eventName to all eligible hooks that subscribe to it.
// It collects handler errors but continues dispatching to remaining hooks.
func (m *Manager) Fire(eventName string, sessionKey string, ctx map[string]any) []error {
	m.mu.RLock()
	hooks := make([]*Hook, len(m.hooks))
	copy(hooks, m.hooks)
	m.mu.RUnlock()

	// Parse event name into type + action.
	typ, action, _ := strings.Cut(eventName, ":")

	ev := &Event{
		EventType:  typ,
		Action:     action,
		Name:       eventName,
		SessionKey: sessionKey,
		Context:    ctx,
		Timestamp:  time.Now(),
		Messages:   []string{},
	}

	var errs []error
	for _, h := range hooks {
		if !m.subscribes(h, eventName) {
			continue
		}
		if !h.Always() && !m.isEnabled(h) {
			continue
		}
		if h.Handler == nil {
			continue
		}
		if err := h.Handler(ev); err != nil {
			errs = append(errs, fmt.Errorf("hook %s: %w", h.HookKey, err))
		}
	}
	return errs
}

// subscribes returns true if hook h listens to eventName.
// subscribes returns true if hook h listens to eventName.
func (m *Manager) subscribes(h *Hook, eventName string) bool {
	for _, e := range h.Events() {
		if e == eventName {
			return true
		}
		// "command" matches all "command:*" events.
		if !strings.Contains(e, ":") && strings.HasPrefix(eventName, e+":") {
			return true
		}
		if e == eventName {
			return true
		}
	}
	return false
}

// List returns a status snapshot of all registered hooks.
// List returns a status snapshot of all registered hooks.
func (m *Manager) List() []HookStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]HookStatus, 0, len(m.hooks))
	for _, h := range m.hooks {
		out = append(out, m.statusOf(h))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HookKey < out[j].HookKey })
	return out
}

// Info returns status for a single hook by key. Returns nil if not found.
// Info returns status for a single hook by key. Returns nil if not found.
func (m *Manager) Info(hookKey string) *HookStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, h := range m.hooks {
		if h.HookKey == hookKey {
			s := m.statusOf(h)
			return &s
		}
	}
	return nil
}

// HookStatus is the data returned by hooks.list / hooks.info.
// HookStatus is the data returned by hooks.list / hooks.info.
type HookStatus struct {
	HookKey     string            `json:"hookKey"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Source      Source            `json:"source"`
	Emoji       string            `json:"emoji"`
	Homepage    string            `json:"homepage"`
	Events      []string          `json:"events"`
	Always      bool              `json:"always"`
	Enabled     bool              `json:"enabled"`
	Eligible    bool              `json:"eligible"`
	FilePath    string            `json:"filePath"`
	Install     []HookInstallSpec `json:"install"`
	Requires    *HookRequires     `json:"requires,omitempty"`
}

func (m *Manager) statusOf(h *Hook) HookStatus {
	enabled := m.isEnabled(h)
	return HookStatus{
		HookKey:     h.HookKey,
		Name:        h.Manifest.Name,
		Description: h.Manifest.Description,
		Source:      h.Source,
		Emoji:       h.Emoji(),
		Homepage:    h.Manifest.Homepage,
		Events:      h.Events(),
		Always:      h.Always(),
		Enabled:     enabled,
		Eligible:    enabled && h.Handler != nil,
		FilePath:    h.FilePath,
		Install:     h.InstallSpecs(),
		Requires:    h.Requires(),
	}
}

// ────────────────────────────────────────────────────────────────────────────
// HOOK.md loader
// ────────────────────────────────────────────────────────────────────────────

// LoadHookMD parses a HOOK.md file and returns a Hook.
// The hookKey is taken from the directory name.
