// Package surface implements the host-side plugin UI-surface registry.
//
// It aggregates the schema-v4 UI-surface contributions (board-widget
// descriptors, data bindings, action verbs, session-action verbs) declared by
// every loaded plugin into a single lookup surface, mirroring how the plugin
// manager aggregates plugin tools. Consumers are:
//
//   - plugins.uiDescriptors    → Descriptors() (metiq-native widget shape)
//   - board.data.read          → LookupBinding(id)
//   - board.action             → LookupActionVerb(id)
//   - plugins.sessionAction    → LookupSessionAction(id)
//   - plugin.surface.refresh   → Refresh() / RefreshScope(pluginID)
//
// SECURITY: aggregation is metadata only. A binding/verb/session-action id
// resolved here is still inert until a board widget declares it AND an
// operator grants that widget; only then does the caller dispatch the id into
// the owning plugin's sandboxed runtime. Global id collisions across plugins
// are dropped fail-closed (never resolve to an arbitrary plugin).
package surface

import (
	"sort"
	"strings"
	"sync"

	"metiq/internal/plugins/sdk"
)

// ManifestSource is the read side of a plugin manager: the ids currently
// loaded and each plugin's runtime manifest. *manager.GojaPluginManager
// satisfies it (and loads both goja and node plugins), so one source covers
// every runtime.
type ManifestSource interface {
	PluginIDs() []string
	PluginManifest(pluginID string) (sdk.Manifest, error)
}

// Binding is an aggregated read-only plugin data binding.
type Binding struct {
	PluginID    string
	ID          string
	Description string
}

// Verb is an aggregated plugin action verb or session-action verb.
type Verb struct {
	PluginID       string
	ID             string
	Description    string
	MutatesSession bool // session-action verbs only
}

// Widget is an aggregated board-widget UI descriptor.
type Widget struct {
	PluginID     string
	ID           string
	Title        string
	Description  string
	Presentation string
	DataBindings []string
	ActionVerbs  []string
}

// Registry aggregates plugin UI-surface contributions. Safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	sources []ManifestSource
	// reserved ids may never be claimed by a plugin contribution (they alias
	// host-dispatched core board bindings). Injected via SetReservedIDs from the
	// authoritative board core-binding allowlist so no plugin id can shadow a
	// core binding (e.g. plugin "sessions" declaring "sessions.list").
	reserved map[string]struct{}

	widgets  []Widget
	bindings map[string]Binding
	verbs    map[string]Verb
	sessions map[string]Verb
	// collisions records ids seen in more than one plugin; such ids resolve to
	// no plugin (fail-closed) and are excluded from Descriptors.
	collisions map[string]struct{}
}

// New builds a registry over the given manifest sources and performs an
// initial aggregation.
func New(sources ...ManifestSource) *Registry {
	filtered := make([]ManifestSource, 0, len(sources))
	for _, s := range sources {
		if s != nil {
			filtered = append(filtered, s)
		}
	}
	r := &Registry{sources: filtered}
	r.Refresh()
	return r
}

// SetReservedIDs replaces the set of contribution ids no plugin may claim and
// re-aggregates. Callers pass the authoritative core board-binding ids so a
// plugin can never register a contribution that aliases a host-dispatched
// core binding.
func (r *Registry) SetReservedIDs(ids []string) {
	reserved := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			reserved[id] = struct{}{}
		}
	}
	r.mu.Lock()
	r.reserved = reserved
	r.mu.Unlock()
	r.Refresh()
}

// Refresh re-scans every source and re-aggregates all contributions.
func (r *Registry) Refresh() {
	r.aggregate("")
}

// RefreshScope re-aggregates but only reports contributions changed for a
// single plugin via the caller's event scope. Aggregation is global (cheap and
// always reflects current manager state); pluginID is validated to exist so
// callers can fail a refresh for an unknown plugin.
func (r *Registry) RefreshScope(pluginID string) bool {
	return r.aggregate(pluginID)
}

// aggregate rebuilds the lookup tables from the current source manifests.
// When scope is non-empty it returns whether that plugin id was found among
// the loaded plugins.
func (r *Registry) aggregate(scope string) bool {
	widgets := []Widget{}
	bindings := map[string]Binding{}
	verbs := map[string]Verb{}
	sessions := map[string]Verb{}
	collisions := map[string]struct{}{}
	scopeFound := false

	// claimed tracks every id already claimed by ANY kind/plugin, enforcing a
	// single global namespace so a widget can never grant an id that resolves
	// ambiguously.
	claimed := map[string]string{} // id -> pluginID

	r.mu.RLock()
	sources := append([]ManifestSource(nil), r.sources...)
	reservedSet := r.reserved
	r.mu.RUnlock()

	// Deterministic ordering: sort plugin ids so collision resolution and
	// descriptor output are stable across refreshes.
	type pluginManifest struct {
		id string
		mf sdk.Manifest
	}
	var loaded []pluginManifest
	seen := map[string]struct{}{}
	for _, src := range sources {
		for _, id := range src.PluginIDs() {
			if _, dup := seen[id]; dup {
				continue
			}
			mf, err := src.PluginManifest(id)
			if err != nil {
				continue
			}
			seen[id] = struct{}{}
			loaded = append(loaded, pluginManifest{id: id, mf: mf})
		}
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].id < loaded[j].id })

	markCollision := func(id string) {
		collisions[id] = struct{}{}
		delete(bindings, id)
		delete(verbs, id)
		delete(sessions, id)
	}
	claim := func(pluginID, id string) bool {
		// Ownership guard (defense-in-depth against manifest validation): a
		// contribution id MUST be namespaced under its declaring plugin. This
		// prevents a plugin from claiming another plugin's granted id.
		if !strings.HasPrefix(id, pluginID+".") {
			return false
		}
		// Reserved-id guard: a plugin can never claim a core board binding id,
		// even if its plugin id is a prefix of one (e.g. plugin "sessions"
		// declaring "sessions.list"). Core bindings dispatch to host methods.
		if _, bad := reservedSet[id]; bad {
			return false
		}
		if _, bad := collisions[id]; bad {
			return false
		}
		// Any id already claimed — by another plugin OR the same plugin across
		// kinds — is ambiguous and drops fail-closed.
		if _, ok := claimed[id]; ok {
			markCollision(id)
			return false
		}
		claimed[id] = pluginID
		return true
	}

	for _, pm := range loaded {
		if scope != "" && pm.id == scope {
			scopeFound = true
		}
		s := pm.mf.Surfaces
		if s == nil {
			continue
		}
		for _, b := range s.DataBindings {
			if claim(pm.id, b.ID) {
				bindings[b.ID] = Binding{PluginID: pm.id, ID: b.ID, Description: b.Description}
			}
		}
		for _, v := range s.ActionVerbs {
			if claim(pm.id, v.ID) {
				verbs[v.ID] = Verb{PluginID: pm.id, ID: v.ID, Description: v.Description}
			}
		}
		for _, v := range s.SessionActions {
			if claim(pm.id, v.ID) {
				sessions[v.ID] = Verb{PluginID: pm.id, ID: v.ID, Description: v.Description, MutatesSession: v.MutatesSession}
			}
		}
		for _, w := range s.BoardWidgets {
			if !claim(pm.id, w.ID) {
				continue
			}
			widgets = append(widgets, Widget{
				PluginID:     pm.id,
				ID:           w.ID,
				Title:        w.Title,
				Description:  w.Description,
				Presentation: w.Presentation,
				DataBindings: append([]string(nil), w.DataBindings...),
				ActionVerbs:  append([]string(nil), w.ActionVerbs...),
			})
		}
	}
	sort.Slice(widgets, func(i, j int) bool { return widgets[i].ID < widgets[j].ID })

	r.mu.Lock()
	r.widgets = widgets
	r.bindings = bindings
	r.verbs = verbs
	r.sessions = sessions
	r.collisions = collisions
	r.mu.Unlock()

	return scopeFound
}

// LookupBinding returns the plugin data binding with id, if any resolves
// uniquely.
func (r *Registry) LookupBinding(id string) (Binding, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.bindings[id]
	return b, ok
}

// LookupActionVerb returns the plugin action verb with id, if any resolves
// uniquely.
func (r *Registry) LookupActionVerb(id string) (Verb, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.verbs[id]
	return v, ok
}

// LookupSessionAction returns the plugin session-action verb with id, if any
// resolves uniquely.
func (r *Registry) LookupSessionAction(id string) (Verb, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.sessions[id]
	return v, ok
}

// Widgets returns a copy of the aggregated board-widget descriptors.
func (r *Registry) Widgets() []Widget {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Widget, len(r.widgets))
	copy(out, r.widgets)
	return out
}

// Descriptors returns the aggregated board-widget UI descriptors in the
// metiq-native shape consumed by plugins.uiDescriptors.
func (r *Registry) Descriptors() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]map[string]any, 0, len(r.widgets))
	for _, w := range r.widgets {
		out = append(out, map[string]any{
			"pluginId":     w.PluginID,
			"id":           w.ID,
			"title":        w.Title,
			"description":  w.Description,
			"presentation": w.Presentation,
			"dataBindings": append([]string{}, w.DataBindings...),
			"actionVerbs":  append([]string{}, w.ActionVerbs...),
		})
	}
	return out
}

// Counts reports the number of aggregated contributions by kind (for refresh
// responses and diagnostics).
func (r *Registry) Counts() (widgets, bindings, verbs, sessions int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.widgets), len(r.bindings), len(r.verbs), len(r.sessions)
}
