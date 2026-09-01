// Package board implements the per-session workspace board surface: tabs and
// widgets arranged by layout operations, guarded by a per-widget capability
// grant flow. It mirrors the OpenClaw board.* wire contract for
// board.get/update/widget.put/widget.grant/board.event plus the board.changed
// push event, and the ticket-authorized view surface (board.prompt.authorize,
// board.data.read, board.action, ticket-variant board.event) backed by the
// short-lived HMAC view tickets in ticket.go. MCP-App widget sources remain
// deferred follow-ups.
package board

import (
	"fmt"
	"regexp"
	"sort"
)

// Content kinds accepted by the board store. "canvas-doc" widgets reference an
// agent-written canvas document by id (swarmstr-5p0v item 1); the referenced
// document is host content (not untrusted plugin code), so canvas-doc widgets
// carry no sandbox capability declaration and render read-only.
const (
	ContentKindHTML      = "html"
	ContentKindPlugin    = "plugin"
	ContentKindMcpApp    = "mcp-app"
	ContentKindCanvasDoc = "canvas-doc"
)

// McpAppInteractCapability is the single declared capability gating MCP-App
// widget interactivity through the standard grant flow. Metiq deviation:
// OpenClaw declares the app-allowed tool names individually.
const McpAppInteractCapability = "mcp.app.interact"

// Grant states for widget capability declarations.
const (
	GrantNone     = "none"
	GrantPending  = "pending"
	GrantGranted  = "granted"
	GrantRejected = "rejected"
)

// Layout limits mirroring the OpenClaw board contract.
const (
	MaxWidgets         = 48
	MaxWidgetHTMLBytes = 256 * 1024
	MaxPluginPropBytes = 8 * 1024
	maxTitleLength     = 80
)

var (
	tabIDPattern       = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	widgetNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	pluginKindPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}:[a-z0-9][a-z0-9._-]{0,63}$`)
	canvasDocIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)
)

var chatDocks = map[string]bool{"left": true, "right": true, "bottom": true, "hidden": true}
var presentations = map[string]bool{"card": true, "full-bleed": true, "frameless": true}
var heightModes = map[string]bool{"auto": true, "fixed": true}

// SizePresets maps placement size hints to grid dimensions.
var SizePresets = map[string]struct{ W, H int }{
	"sm":   {3, 3},
	"md":   {6, 4},
	"lg":   {8, 6},
	"xl":   {12, 8},
	"full": {12, 8},
}

// ValidationError carries the OpenClaw board error code taxonomy.
type ValidationError struct {
	Code    string // "conflict" | "invalid_operation" | "not_found"
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func errConflict(format string, args ...any) *ValidationError {
	return &ValidationError{Code: "conflict", Message: fmt.Sprintf(format, args...)}
}

func errInvalid(format string, args ...any) *ValidationError {
	return &ValidationError{Code: "invalid_operation", Message: fmt.Sprintf(format, args...)}
}

func errNotFound(format string, args ...any) *ValidationError {
	return &ValidationError{Code: "not_found", Message: fmt.Sprintf(format, args...)}
}

// Tab is one board tab.
type Tab struct {
	TabID    string `json:"tabId"`
	Title    string `json:"title"`
	Position int    `json:"position"`
	ChatDock string `json:"chatDock"`
}

// Declared carries sandbox capability declarations for a widget.
type Declared struct {
	NetOrigins []string `json:"netOrigins,omitempty"`
	Tools      []string `json:"tools,omitempty"`
}

// PluginGrantIdentity pins one declared plugin capability to the exact package
// approved by the operator. It is internal store metadata, not a wire field.
type PluginGrantIdentity struct {
	PluginID      string
	PackageDigest string
}

// Widget is one board widget row in the snapshot.
type Widget struct {
	Name                  string                         `json:"name"`
	TabID                 string                         `json:"tabId"`
	Title                 string                         `json:"title,omitempty"`
	ContentKind           string                         `json:"contentKind"`
	PluginKind            string                         `json:"pluginKind,omitempty"`
	Props                 map[string]any                 `json:"props,omitempty"`
	Presentation          string                         `json:"presentation,omitempty"`
	HeightMode            string                         `json:"heightMode,omitempty"`
	SizeW                 int                            `json:"sizeW"`
	SizeH                 int                            `json:"sizeH"`
	Position              int                            `json:"position"`
	GrantState            string                         `json:"grantState"`
	Revision              int                            `json:"revision"`
	InstanceID            string                         `json:"instanceId,omitempty"`
	DeclaredSummary       []string                       `json:"declaredSummary,omitempty"`
	Declared              *Declared                      `json:"declared,omitempty"`
	PluginGrantIdentities map[string]PluginGrantIdentity `json:"-"`

	// View-ticket fields are ephemeral: minted per board.get response by
	// GetSnapshotWithTickets, never persisted in the store.
	FrameURL        string `json:"frameUrl,omitempty"`
	ViewTicket      string `json:"viewTicket,omitempty"`
	ViewTicketTTLMs int    `json:"viewTicketTtlMs,omitempty"`
	ViewGeneration  string `json:"viewGeneration,omitempty"`
}

// Snapshot is the full board state for one session key.
type Snapshot struct {
	SessionKey string   `json:"sessionKey"`
	Revision   int      `json:"revision"`
	Tabs       []Tab    `json:"tabs"`
	Widgets    []Widget `json:"widgets"`
}

// Op is one board layout operation (tagged union on Kind).
type Op struct {
	Kind       string   `json:"kind"`
	TabID      string   `json:"tabId,omitempty"`
	Title      string   `json:"title,omitempty"`
	ChatDock   string   `json:"chatDock,omitempty"`
	Position   *int     `json:"position,omitempty"`
	TabIDs     []string `json:"tabIds,omitempty"`
	Name       string   `json:"name,omitempty"`
	After      string   `json:"after,omitempty"`
	SizeW      int      `json:"sizeW,omitempty"`
	SizeH      int      `json:"sizeH,omitempty"`
	HeightMode string   `json:"heightMode,omitempty"`
}

type layout struct {
	Tabs    []Tab
	Widgets []Widget
}

func cloneDeclared(d *Declared) *Declared {
	if d == nil {
		return nil
	}
	out := &Declared{}
	if len(d.NetOrigins) > 0 {
		out.NetOrigins = append([]string(nil), d.NetOrigins...)
	}
	if len(d.Tools) > 0 {
		out.Tools = append([]string(nil), d.Tools...)
	}
	return out
}

func cloneWidget(w Widget) Widget {
	out := w
	if w.Props != nil {
		props := make(map[string]any, len(w.Props))
		for k, v := range w.Props {
			props[k] = v
		}
		out.Props = props
	}
	if w.DeclaredSummary != nil {
		out.DeclaredSummary = append([]string(nil), w.DeclaredSummary...)
	}
	out.Declared = cloneDeclared(w.Declared)
	if w.PluginGrantIdentities != nil {
		out.PluginGrantIdentities = make(map[string]PluginGrantIdentity, len(w.PluginGrantIdentities))
		for capability, identity := range w.PluginGrantIdentities {
			out.PluginGrantIdentities[capability] = identity
		}
	}
	return out
}

func cloneLayout(l layout) layout {
	next := layout{Tabs: append([]Tab(nil), l.Tabs...), Widgets: make([]Widget, 0, len(l.Widgets))}
	for _, w := range l.Widgets {
		next.Widgets = append(next.Widgets, cloneWidget(w))
	}
	return next
}

// CloneSnapshot deep-copies a snapshot so callers can never mutate store state.
func CloneSnapshot(s Snapshot) Snapshot {
	out := Snapshot{SessionKey: s.SessionKey, Revision: s.Revision, Tabs: append([]Tab{}, s.Tabs...), Widgets: make([]Widget, 0, len(s.Widgets))}
	for _, w := range s.Widgets {
		out.Widgets = append(out.Widgets, cloneWidget(w))
	}
	return out
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// normalizeLayout re-sequences tab and widget positions deterministically:
// tabs by position, widgets grouped by tab order then position.
func normalizeLayout(l layout) layout {
	next := cloneLayout(l)
	sort.SliceStable(next.Tabs, func(i, j int) bool { return next.Tabs[i].Position < next.Tabs[j].Position })
	for i := range next.Tabs {
		next.Tabs[i].Position = i
	}
	tabPos := make(map[string]int, len(next.Tabs))
	for _, tab := range next.Tabs {
		tabPos[tab.TabID] = tab.Position
	}
	tabOf := func(w Widget) int {
		if p, ok := tabPos[w.TabID]; ok {
			return p
		}
		return int(^uint(0) >> 1)
	}
	sort.SliceStable(next.Widgets, func(i, j int) bool {
		ti, tj := tabOf(next.Widgets[i]), tabOf(next.Widgets[j])
		if ti != tj {
			return ti < tj
		}
		return next.Widgets[i].Position < next.Widgets[j].Position
	})
	nextPos := map[string]int{}
	for i := range next.Widgets {
		p := nextPos[next.Widgets[i].TabID]
		next.Widgets[i].Position = p
		nextPos[next.Widgets[i].TabID] = p + 1
	}
	return next
}

func findTab(l *layout, tabID string) int {
	for i := range l.Tabs {
		if l.Tabs[i].TabID == tabID {
			return i
		}
	}
	return -1
}

func findWidget(l *layout, name string) int {
	for i := range l.Widgets {
		if l.Widgets[i].Name == name {
			return i
		}
	}
	return -1
}

func moveTab(l *layout, tabID string, position int) {
	var moving Tab
	ordered := make([]Tab, 0, len(l.Tabs))
	sorted := append([]Tab(nil), l.Tabs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })
	for _, tab := range sorted {
		if tab.TabID == tabID {
			moving = tab
			continue
		}
		ordered = append(ordered, tab)
	}
	idx := clampInt(position, 0, len(ordered))
	ordered = append(ordered[:idx], append([]Tab{moving}, ordered[idx:]...)...)
	for i := range ordered {
		ordered[i].Position = i
	}
	l.Tabs = ordered
}

// moveWidget relocates a widget within/into targetTabID using either an
// explicit position or an "after" anchor (mutually exclusive).
func moveWidget(l *layout, name, targetTabID string, position *int, after string) error {
	if findTab(l, targetTabID) < 0 {
		return errNotFound("board tab not found: %s", targetTabID)
	}
	if position != nil && after != "" {
		return errInvalid("widget_move accepts either position or after, not both")
	}
	wi := findWidget(l, name)
	widget := cloneWidget(l.Widgets[wi])

	targetWidgets := make([]Widget, 0)
	otherWidgets := make([]Widget, 0)
	for _, w := range l.Widgets {
		if w.Name == name {
			continue
		}
		if w.TabID == targetTabID {
			targetWidgets = append(targetWidgets, w)
		} else {
			otherWidgets = append(otherWidgets, w)
		}
	}
	sort.SliceStable(targetWidgets, func(i, j int) bool { return targetWidgets[i].Position < targetWidgets[j].Position })

	targetPosition := len(targetWidgets)
	if after != "" {
		if after == name {
			return errInvalid("widget cannot be placed after itself")
		}
		anchor := -1
		for i, w := range targetWidgets {
			if w.Name == after {
				anchor = i
				break
			}
		}
		if anchor < 0 {
			return errNotFound("board widget anchor not found on tab %s: %s", targetTabID, after)
		}
		targetPosition = anchor + 1
	} else if position != nil {
		targetPosition = clampInt(*position, 0, len(targetWidgets))
	}
	widget.TabID = targetTabID
	targetWidgets = append(targetWidgets[:targetPosition], append([]Widget{widget}, targetWidgets[targetPosition:]...)...)
	for i := range targetWidgets {
		targetWidgets[i].Position = i
	}
	l.Widgets = append(otherWidgets, targetWidgets...)
	return nil
}

func validateOpTitle(title string) error {
	if title == "" || len(title) > maxTitleLength {
		return errInvalid("board tab title must be 1-%d characters", maxTitleLength)
	}
	return nil
}

func applyOp(l *layout, op Op) error {
	switch op.Kind {
	case "tab_create":
		if !tabIDPattern.MatchString(op.TabID) {
			return errInvalid("invalid board tab id: %s", op.TabID)
		}
		if err := validateOpTitle(op.Title); err != nil {
			return err
		}
		if op.ChatDock != "" && !chatDocks[op.ChatDock] {
			return errInvalid("invalid board chat dock: %s", op.ChatDock)
		}
		if findTab(l, op.TabID) >= 0 {
			return errConflict("board tab already exists: %s", op.TabID)
		}
		dock := op.ChatDock
		if dock == "" {
			dock = "right"
		}
		l.Tabs = append(l.Tabs, Tab{TabID: op.TabID, Title: op.Title, Position: len(l.Tabs), ChatDock: dock})
		return nil
	case "tab_update":
		ti := findTab(l, op.TabID)
		if ti < 0 {
			return errNotFound("board tab not found: %s", op.TabID)
		}
		if op.Title == "" && op.ChatDock == "" && op.Position == nil {
			return errInvalid("tab_update has no changes")
		}
		if op.Title != "" {
			if err := validateOpTitle(op.Title); err != nil {
				return err
			}
			l.Tabs[ti].Title = op.Title
		}
		if op.ChatDock != "" {
			if !chatDocks[op.ChatDock] {
				return errInvalid("invalid board chat dock: %s", op.ChatDock)
			}
			l.Tabs[ti].ChatDock = op.ChatDock
		}
		if op.Position != nil {
			moveTab(l, op.TabID, *op.Position)
		}
		return nil
	case "tab_delete":
		ti := findTab(l, op.TabID)
		if ti < 0 {
			return errNotFound("board tab not found: %s", op.TabID)
		}
		deleted := l.Tabs[ti]
		remaining := make([]Tab, 0, len(l.Tabs)-1)
		for _, tab := range l.Tabs {
			if tab.TabID != deleted.TabID {
				remaining = append(remaining, tab)
			}
		}
		sort.SliceStable(remaining, func(i, j int) bool { return remaining[i].Position < remaining[j].Position })
		orphaned := 0
		for i := range l.Widgets {
			if l.Widgets[i].TabID == deleted.TabID {
				orphaned++
			}
		}
		if len(remaining) == 0 && orphaned > 0 {
			return errInvalid("cannot delete the last board tab while it contains widgets")
		}
		l.Tabs = remaining
		if orphaned > 0 {
			fallback := remaining[0].TabID
			for i := range l.Widgets {
				if l.Widgets[i].TabID == deleted.TabID {
					l.Widgets[i].TabID = fallback
					l.Widgets[i].Position = int(^uint(0) >> 1)
				}
			}
		}
		return nil
	case "tabs_reorder":
		if len(op.TabIDs) != len(l.Tabs) {
			return errInvalid("tabs_reorder must contain every tab exactly once")
		}
		seen := make(map[string]bool, len(op.TabIDs))
		for _, id := range op.TabIDs {
			if seen[id] || findTab(l, id) < 0 {
				return errInvalid("tabs_reorder must contain every tab exactly once")
			}
			seen[id] = true
		}
		byID := make(map[string]Tab, len(l.Tabs))
		for _, tab := range l.Tabs {
			byID[tab.TabID] = tab
		}
		next := make([]Tab, 0, len(op.TabIDs))
		for i, id := range op.TabIDs {
			tab := byID[id]
			tab.Position = i
			next = append(next, tab)
		}
		l.Tabs = next
		return nil
	case "widget_move":
		wi := findWidget(l, op.Name)
		if wi < 0 {
			return errNotFound("board widget not found: %s", op.Name)
		}
		target := op.TabID
		if target == "" {
			target = l.Widgets[wi].TabID
		}
		return moveWidget(l, op.Name, target, op.Position, op.After)
	case "widget_resize":
		wi := findWidget(l, op.Name)
		if wi < 0 {
			return errNotFound("board widget not found: %s", op.Name)
		}
		if op.HeightMode != "" && !heightModes[op.HeightMode] {
			return errInvalid("invalid board height mode: %s", op.HeightMode)
		}
		l.Widgets[wi].SizeW = clampInt(op.SizeW, 1, 12)
		l.Widgets[wi].SizeH = clampInt(op.SizeH, 1, 20)
		// A resize is always explicit user intent: clients that omit heightMode
		// must still pin, or the next content report undoes their resize.
		if op.HeightMode != "" {
			l.Widgets[wi].HeightMode = op.HeightMode
		} else {
			l.Widgets[wi].HeightMode = "fixed"
		}
		return nil
	case "widget_remove":
		wi := findWidget(l, op.Name)
		if wi < 0 {
			return errNotFound("board widget not found: %s", op.Name)
		}
		next := make([]Widget, 0, len(l.Widgets)-1)
		for _, w := range l.Widgets {
			if w.Name != op.Name {
				next = append(next, w)
			}
		}
		l.Widgets = next
		return nil
	default:
		return errInvalid("unknown board op kind: %s", op.Kind)
	}
}

// applyOps applies each op in order, normalizing after every step so anchor
// and position semantics match the OpenClaw layout engine.
func applyOps(l layout, ops []Op) (layout, error) {
	next := cloneLayout(l)
	for _, op := range ops {
		if err := applyOp(&next, op); err != nil {
			return layout{}, err
		}
		next = normalizeLayout(next)
	}
	return normalizeLayout(next), nil
}

// normalizeDeclaredCapabilities trims, dedupes, and drops empty entries.
// Returns nil when nothing remains declared.
func normalizeDeclaredCapabilities(d *Declared) *Declared {
	if d == nil {
		return nil
	}
	dedupe := func(items []string) []string {
		seen := map[string]bool{}
		out := make([]string, 0, len(items))
		for _, item := range items {
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			out = append(out, item)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	out := &Declared{NetOrigins: dedupe(d.NetOrigins), Tools: dedupe(d.Tools)}
	if out.NetOrigins == nil && out.Tools == nil {
		return nil
	}
	return out
}

// declarationIsSubset reports whether next declares no capability absent from
// prior. Grants may narrow but never widen.
func declarationIsSubset(next, prior *Declared) bool {
	if next == nil {
		return true
	}
	if prior == nil {
		return len(next.NetOrigins) == 0 && len(next.Tools) == 0
	}
	contains := func(haystack []string, needle string) bool {
		for _, item := range haystack {
			if item == needle {
				return true
			}
		}
		return false
	}
	for _, origin := range next.NetOrigins {
		if !contains(prior.NetOrigins, origin) {
			return false
		}
	}
	for _, tool := range next.Tools {
		if !contains(prior.Tools, tool) {
			return false
		}
	}
	return true
}

// declaredSummaryLines renders the human-readable grant prompt lines.
func declaredSummaryLines(d *Declared) []string {
	if d == nil {
		return nil
	}
	lines := make([]string, 0, len(d.NetOrigins)+len(d.Tools))
	for _, origin := range d.NetOrigins {
		lines = append(lines, "Network access: "+origin)
	}
	for _, tool := range d.Tools {
		lines = append(lines, "Tool access: "+tool)
	}
	if len(lines) == 0 {
		return nil
	}
	return lines
}
