// Package hooks implements the metiq hook event system.
//
// Hooks are event-driven handlers loaded from HOOK.md files.  They fire on
// named events (command:new, agent:bootstrap, gateway:startup, …) and can
// be enabled/disabled by the user.
//
// Bundled hooks ship with the daemon; managed hooks live in
// ~/.metiq/hooks/.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ────────────────────────────────────────────────────────────────────────────
// HOOK.md types
// ────────────────────────────────────────────────────────────────────────────

// HookManifest is the parsed YAML frontmatter from a HOOK.md file.
type HookManifest struct {
	Name        string        `yaml:"name"`
	Description string        `yaml:"description"`
	Homepage    string        `yaml:"homepage"`
	Metadata    *HookMetaWrap `yaml:"metadata"`
	Body        string        `yaml:"-"` // markdown body after frontmatter
}

// HookMetaWrap is the `metadata:` block.
type HookMetaWrap struct {
	OpenClaw *OpenClawHookMeta `yaml:"openclaw"`
}

// OpenClawHookMeta is the `metadata.openclaw:` block.
type OpenClawHookMeta struct {
	Emoji    string            `yaml:"emoji"`
	Events   []string          `yaml:"events"`
	Always   bool              `yaml:"always"`
	Requires *HookRequires     `yaml:"requires"`
	Install  []HookInstallSpec `yaml:"install"`
}

// HookRequires lists requirements for a hook to be eligible.
type HookRequires struct {
	Bins    []string `yaml:"bins"`
	AnyBins []string `yaml:"anyBins"`
	Env     []string `yaml:"env"`
	Config  []string `yaml:"config"`
	OS      []string `yaml:"os"`
}

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
type Source string

const (
	SourceBundled   Source = "metiq-bundled"
	SourceManaged   Source = "metiq-managed"
	SourceWorkspace Source = "metiq-workspace"
)

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
func (h *Hook) Emoji() string {
	if h.Manifest.Metadata != nil && h.Manifest.Metadata.OpenClaw != nil {
		if e := h.Manifest.Metadata.OpenClaw.Emoji; e != "" {
			return e
		}
	}
	return "🪝"
}

// Events returns the event list this hook subscribes to.
func (h *Hook) Events() []string {
	if h.Manifest.Metadata != nil && h.Manifest.Metadata.OpenClaw != nil {
		return h.Manifest.Metadata.OpenClaw.Events
	}
	return nil
}

// Always reports whether the hook fires even when disabled.
func (h *Hook) Always() bool {
	if h.Manifest.Metadata != nil && h.Manifest.Metadata.OpenClaw != nil {
		return h.Manifest.Metadata.OpenClaw.Always
	}
	return false
}

// Requires returns the hook's requirements (may be nil).
func (h *Hook) Requires() *HookRequires {
	if h.Manifest.Metadata != nil && h.Manifest.Metadata.OpenClaw != nil {
		return h.Manifest.Metadata.OpenClaw.Requires
	}
	return nil
}

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
type HookHandler func(event *Event) error

// ────────────────────────────────────────────────────────────────────────────
// Manager
// ────────────────────────────────────────────────────────────────────────────

// Manager manages loaded hooks and dispatches events.
type Manager struct {
	mu      sync.RWMutex
	hooks   []*Hook         // all loaded hooks
	enabled map[string]bool // overrides: hookKey → enabled
}

// NewManager creates an empty Manager.
func NewManager() *Manager {
	return &Manager{enabled: map[string]bool{}}
}

// Register adds a hook to the manager.
func (m *Manager) Register(h *Hook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = append(m.hooks, h)
}

// SetEnabled persists an enable/disable override for hookKey.
func (m *Manager) SetEnabled(hookKey string, enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled[hookKey] = enabled
}

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
func LoadHookMD(path string, src Source) (*Hook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fm, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("HOOK.md frontmatter: %w", err)
	}

	// Pre-process JSON5 quirks (same format as SKILL.md).
	fm = preprocessFrontmatter(fm)

	var m HookManifest
	if err := yaml.Unmarshal(fm, &m); err != nil {
		return nil, fmt.Errorf("HOOK.md yaml: %w", err)
	}
	m.Body = string(bytes.TrimSpace(body))

	hookKey := filepath.Base(filepath.Dir(path))
	if m.Name == "" {
		m.Name = hookKey
	}

	return &Hook{
		HookKey:  hookKey,
		Manifest: m,
		Source:   src,
		FilePath: path,
		BaseDir:  filepath.Dir(path),
	}, nil
}

// ScanDir scans a directory for hooks.  Each immediate subdirectory that
// contains a HOOK.md file is treated as one hook.
func ScanDir(dir string, src Source) ([]*Hook, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var hooks []*Hook
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		hookMD := filepath.Join(dir, e.Name(), "HOOK.md")
		if _, err := os.Stat(hookMD); os.IsNotExist(err) {
			continue
		}
		h, err := LoadHookMD(hookMD, src)
		if err != nil {
			continue // skip malformed entries
		}
		hooks = append(hooks, h)
	}
	return hooks, nil
}

// BundledHooksDir returns the directory containing bundled hooks.
// Resolution order:
//  1. METIQ_BUNDLED_HOOKS_DIR env
//  2. hooks/ sibling to the running binary
//  3. Walk up from cwd looking for hooks/ (dev mode)
func BundledHooksDir() string {
	if d := os.Getenv("METIQ_BUNDLED_HOOKS_DIR"); d != "" {
		return d
	}
	// Binary sibling
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "hooks")
		if looksLikeBundledHooksDir(candidate) {
			return candidate
		}
	}
	// Walk up from cwd (repo dev mode)
	cwd, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(cwd, "hooks")
		if looksLikeBundledHooksDir(candidate) {
			return candidate
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}
		cwd = parent
	}
	return ""
}

func looksLikeBundledHooksDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "HOOK.md")); err == nil {
			return true
		}
	}
	return false
}

// ManagedHooksDir returns the directory for user-managed hooks.
// METIQ_MANAGED_HOOKS_DIR env overrides the default.
func ManagedHooksDir() string {
	if d := os.Getenv("METIQ_MANAGED_HOOKS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".metiq", "hooks")
}

// ────────────────────────────────────────────────────────────────────────────
// YAML frontmatter helpers (mirrors skills package)
// ────────────────────────────────────────────────────────────────────────────

func parseFrontmatter(data []byte) (fm []byte, body []byte, err error) {
	data = bytes.TrimSpace(data)
	if !bytes.HasPrefix(data, []byte("---")) {
		return nil, data, nil
	}
	rest := data[3:]
	// Find closing ---
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return data, nil, nil
	}
	fm = rest[:idx]
	body = rest[idx+4:]
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
	return fm, body, nil
}

func preprocessFrontmatter(data []byte) []byte {
	data = joinFlowOnNextLine(data)
	for {
		next := trailingCommaPass(data)
		if bytes.Equal(next, data) {
			break
		}
		data = next
	}
	return data
}

func joinFlowOnNextLine(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := bytes.TrimRight(line, " \t")
		if bytes.HasSuffix(trimmed, []byte(":")) && i+1 < len(lines) {
			next := bytes.TrimLeft(lines[i+1], " \t")
			if bytes.HasPrefix(next, []byte("{")) || bytes.HasPrefix(next, []byte("[")) {
				joined := append(bytes.TrimRight(line, " \t"), ' ')
				joined = append(joined, next...)
				out = append(out, joined)
				i++
				continue
			}
		}
		out = append(out, line)
	}
	return bytes.Join(out, []byte("\n"))
}

func trailingCommaPass(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	out := make([][]byte, len(lines))
	for i, line := range lines {
		stripped := bytes.TrimRight(line, " \t")
		if bytes.HasSuffix(stripped, []byte(",")) {
			// Look at next non-empty line.
			for j := i + 1; j < len(lines); j++ {
				next := bytes.TrimLeft(lines[j], " \t")
				if len(next) == 0 {
					continue
				}
				if bytes.HasPrefix(next, []byte("}")) || bytes.HasPrefix(next, []byte("]")) {
					stripped = stripped[:len(stripped)-1]
				}
				break
			}
			out[i] = stripped
		} else {
			out[i] = line
		}
	}
	return bytes.Join(out, []byte("\n"))
}

// ────────────────────────────────────────────────────────────────────────────
// Serialise hook status for RPC
// ────────────────────────────────────────────────────────────────────────────

// StatusToMap converts a HookStatus to a map[string]any for JSON-RPC responses.
func StatusToMap(s HookStatus) map[string]any {
	install := make([]map[string]any, 0, len(s.Install))
	for _, sp := range s.Install {
		install = append(install, map[string]any{
			"id":    sp.ID,
			"kind":  sp.Kind,
			"label": sp.Label,
		})
	}

	var req map[string]any
	if s.Requires != nil {
		req = map[string]any{
			"bins":    s.Requires.Bins,
			"anyBins": s.Requires.AnyBins,
			"env":     s.Requires.Env,
			"config":  s.Requires.Config,
			"os":      s.Requires.OS,
		}
	}

	m := map[string]any{
		"hookKey":     s.HookKey,
		"name":        s.Name,
		"description": s.Description,
		"source":      string(s.Source),
		"emoji":       s.Emoji,
		"homepage":    s.Homepage,
		"events":      s.Events,
		"always":      s.Always,
		"enabled":     s.Enabled,
		"eligible":    s.Eligible,
		"filePath":    s.FilePath,
		"install":     install,
	}
	if req != nil {
		m["requires"] = req
	}
	return m
}

// MarshalStatus is a convenience wrapper for JSON encoding.
func MarshalStatus(statuses []HookStatus) (json.RawMessage, error) {
	list := make([]map[string]any, len(statuses))
	for i, s := range statuses {
		list[i] = StatusToMap(s)
	}
	return json.Marshal(list)
}

// ────────────────────────────────────────────────────────────────────────────
// Shell hook execution
// ────────────────────────────────────────────────────────────────────────────

// ShellHandlerTimeout is the maximum time a shell hook handler may run.
// Override via the METIQ_HOOK_TIMEOUT_SEC environment variable.
const ShellHandlerTimeout = 30 * time.Second

// MakeShellHandler returns a HookHandler that executes scriptPath as a shell
// script, passing event data via environment variables:
//
//   - HOOK_NAME         — full event name (e.g. "command:new")
//   - HOOK_TYPE         — event type (e.g. "command")
//   - HOOK_ACTION       — event action (e.g. "new")
//   - HOOK_SESSION_KEY  — session key identifier
//   - HOOK_CONTEXT      — JSON-encoded event context map
//   - HOOK_TIMESTAMP    — RFC3339 timestamp of the event
//
// If the event context map contains known Nostr message fields, they are also
// exported as dedicated variables:
//
//   - HOOK_FROM_PUBKEY  — sender Nostr pubkey hex (context["from_pubkey"])
//   - HOOK_TO_PUBKEY    — recipient Nostr pubkey hex (context["to_pubkey"])
//   - HOOK_EVENT_ID     — Nostr event ID hex (context["event_id"])
//   - HOOK_RELAY        — relay URL the event arrived from (context["relay"])
//   - HOOK_CHANNEL_ID   — channel identifier e.g. "nostr" (context["channel_id"])
//   - HOOK_CONTENT      — message content (context["content"])
//
// If the script exits with a non-zero code, the error is returned.
// stdout and stderr are collected but not returned (the manager logs them at
// debug level when the handler returns an error).
func MakeShellHandler(scriptPath string) HookHandler {
	return func(event *Event) error {
		ctx, cancel := context.WithTimeout(context.Background(), ShellHandlerTimeout)
		defer cancel()

		ctxJSON, _ := json.Marshal(event.Context)
		env := append(os.Environ(),
			"HOOK_NAME="+event.Name,
			"HOOK_TYPE="+event.EventType,
			"HOOK_ACTION="+event.Action,
			"HOOK_SESSION_KEY="+event.SessionKey,
			"HOOK_CONTEXT="+string(ctxJSON),
			"HOOK_TIMESTAMP="+event.Timestamp.UTC().Format(time.RFC3339),
		)

		// Promote well-known context fields to dedicated env vars for convenience.
		for _, kv := range []struct{ key, envVar string }{
			{"from_pubkey", "HOOK_FROM_PUBKEY"},
			{"to_pubkey", "HOOK_TO_PUBKEY"},
			{"event_id", "HOOK_EVENT_ID"},
			{"relay", "HOOK_RELAY"},
			{"channel_id", "HOOK_CHANNEL_ID"},
			{"content", "HOOK_CONTENT"},
		} {
			if v, ok := event.Context[kv.key]; ok {
				env = append(env, kv.envVar+"="+fmt.Sprintf("%v", v))
			}
		}

		cmd := exec.CommandContext(ctx, "sh", scriptPath) //nolint:gosec // scriptPath is admin-controlled
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("shell hook %s: %w\n%s", filepath.Base(scriptPath), err, strings.TrimSpace(string(out)))
		}
		// Append any output lines as hook messages.
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				event.Messages = append(event.Messages, line)
			}
		}
		return nil
	}
}

// AttachShellHandlers scans each registered hook whose Handler is nil for a
// handler.sh file in its BaseDir.  When found, a shell handler is attached.
// This is called after LoadBundledHooks / ScanDir so that managed and workspace
// hooks with a handler.sh automatically become executable.
func AttachShellHandlers(mgr *Manager) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for _, h := range mgr.hooks {
		if h.Handler != nil {
			continue // already has a Go handler
		}
		if h.BaseDir == "" {
			continue
		}
		scriptPath := filepath.Join(h.BaseDir, "handler.sh")
		if _, err := os.Stat(scriptPath); err == nil {
			h.Handler = MakeShellHandler(scriptPath)
		}
	}
}
