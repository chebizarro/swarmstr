package manifest

import (
	"fmt"
	"regexp"
	"strings"
)

// ─── Plugin UI-Surface Contributions (schema v4) ─────────────────────────────
//
// Schema v4 lets a plugin contribute board-widget UI descriptors, data
// bindings, action verbs, and session-action verbs. Every contribution is
// OPTIONAL: a v3 manifest omits the whole block and keeps loading unchanged.
//
// SECURITY: a contribution declaration is inert on its own. A plugin data
// binding / action verb / session action only becomes reachable when a board
// widget declares its id in the sandbox capability set AND an operator grants
// that widget (board.widget.grant). The host then dispatches the id into the
// plugin's own sandboxed runtime — plugins never gain host-method access
// through a binding. Everything fails closed for untrusted plugins.

// surfaceIDPattern bounds contribution ids to a stable, collision-resistant
// shape. Ids are the wire tokens a board widget declares and the operator
// grants, so they must be predictable and non-spoofable.
var surfaceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

const maxSurfaceID = 64

// SurfaceContributions bundles the optional plugin UI-surface contribution
// declarations added in manifest schema v4.
type SurfaceContributions struct {
	// BoardWidgets are plugin-contributed board widget UI descriptors. They are
	// surfaced through plugins.uiDescriptors and always render through the
	// sandboxed board frame-host + view-ticket path.
	BoardWidgets []BoardWidgetSurface `json:"board_widgets,omitempty"`

	// DataBindings are read-only data sources a granted board widget may call
	// via board.data.read; each dispatches into the plugin runtime.
	DataBindings []DataBindingSurface `json:"data_bindings,omitempty"`

	// ActionVerbs are action verbs a granted board widget may execute via
	// board.action; each dispatches into the plugin runtime and may have side
	// effects scoped to the plugin.
	ActionVerbs []ActionVerbSurface `json:"action_verbs,omitempty"`

	// SessionActions are session-scoped verbs dispatched via plugins.sessionAction.
	// They may mutate session state (through the plugin's SessionHost surface).
	SessionActions []SessionActionSurface `json:"session_actions,omitempty"`
}

// IsEmpty reports whether no contributions are declared.
func (s *SurfaceContributions) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.BoardWidgets) == 0 && len(s.DataBindings) == 0 &&
		len(s.ActionVerbs) == 0 && len(s.SessionActions) == 0
}

// BoardWidgetSurface is a plugin-contributed board widget UI descriptor.
type BoardWidgetSurface struct {
	// ID is the descriptor identifier (required, namespaced).
	ID string `json:"id"`

	// Title is the human-readable widget title (optional).
	Title string `json:"title,omitempty"`

	// Description explains the widget (optional).
	Description string `json:"description,omitempty"`

	// Presentation is the board presentation hint (optional):
	// "card" | "full-bleed" | "frameless".
	Presentation string `json:"presentation,omitempty"`

	// DataBindings lists data-binding ids this widget reads (optional). Each
	// must be declared in SurfaceContributions.DataBindings.
	DataBindings []string `json:"data_bindings,omitempty"`

	// ActionVerbs lists action-verb ids this widget executes (optional). Each
	// must be declared in SurfaceContributions.ActionVerbs.
	ActionVerbs []string `json:"action_verbs,omitempty"`
}

// DataBindingSurface declares a read-only plugin data binding.
type DataBindingSurface struct {
	// ID is the binding identifier (required, namespaced). It is the token a
	// board widget declares and board.data.read presents.
	ID string `json:"id"`

	// Description explains the binding (optional).
	Description string `json:"description,omitempty"`
}

// ActionVerbSurface declares a plugin action verb executable from a board widget.
type ActionVerbSurface struct {
	// ID is the verb identifier (required, namespaced).
	ID string `json:"id"`

	// Description explains the verb (optional).
	Description string `json:"description,omitempty"`
}

// SessionActionSurface declares a session-scoped plugin verb.
type SessionActionSurface struct {
	// ID is the verb identifier (required, namespaced).
	ID string `json:"id"`

	// Description explains the verb (optional).
	Description string `json:"description,omitempty"`

	// MutatesSession documents that dispatching this verb may mutate session
	// state (informational; enforcement lives in the plugin runtime + grant).
	MutatesSession bool `json:"mutates_session,omitempty"`
}

var boardSurfacePresentations = map[string]bool{"card": true, "full-bleed": true, "frameless": true}

// validateSurfaceID checks a single contribution id. When pluginID is set the
// id MUST be namespaced under it ("<pluginID>.*"): this binds every
// contribution to its declaring plugin so a granted id can never be
// impersonated by another plugin, and can never alias a core board binding
// (core binding ids are not namespaced under any plugin id).
func validateSurfaceID(field, pluginID, id string) *ValidationError {
	id = strings.TrimSpace(id)
	if id == "" {
		return &ValidationError{Field: field, Message: "is required"}
	}
	if len(id) > maxSurfaceID {
		return &ValidationError{Field: field, Message: fmt.Sprintf("must be at most %d characters", maxSurfaceID)}
	}
	if !surfaceIDPattern.MatchString(id) {
		return &ValidationError{Field: field, Message: "must be a lowercase namespaced id (e.g. my-plugin.refresh)"}
	}
	if pluginID != "" && !strings.HasPrefix(id, pluginID+".") {
		return &ValidationError{Field: field, Message: fmt.Sprintf("must be namespaced under the plugin id (%s.*)", pluginID)}
	}
	return nil
}

// ValidateSurfaceContributions validates the schema v4 UI-surface contribution
// block. Ids must be namespaced under pluginID ("<pluginID>.*"), unique across
// ALL contribution kinds (a board widget, data binding, action verb, and
// session action can never share an id), and every widget reference must
// resolve to a declared binding/verb. Returns nil when s is nil (v3
// back-compat) or fully valid.
func ValidateSurfaceContributions(pluginID string, s *SurfaceContributions) ValidationErrors {
	if s == nil {
		return nil
	}
	var errs ValidationErrors

	// A plugin may only declare UI surfaces under a canonical (dot-free) plugin
	// id. This makes contribution-id ownership unambiguous: the segment before
	// the first dot is exactly the declaring plugin id, so no plugin can nest
	// its namespace inside another plugin's (e.g. "finance.reports" cannot claim
	// "finance.*").
	if !IsValidPluginID(pluginID) {
		return append(errs, ValidationError{
			Field:   "id",
			Message: "must be a canonical plugin id (lowercase, hyphens, no dots) to declare UI surfaces",
		})
	}

	// Global id namespace across all contribution kinds within this manifest.
	ids := map[string]string{} // id -> kind that first claimed it
	claim := func(kind, field, id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if owner, ok := ids[id]; ok {
			errs = append(errs, ValidationError{
				Field:   field,
				Message: fmt.Sprintf("id %q collides with a %s contribution of the same id", id, owner),
			})
			return
		}
		ids[id] = kind
	}

	bindings := map[string]bool{}
	verbs := map[string]bool{}

	for i := range s.DataBindings {
		field := fmt.Sprintf("surfaces.data_bindings[%d].id", i)
		if verr := validateSurfaceID(field, pluginID, s.DataBindings[i].ID); verr != nil {
			errs = append(errs, *verr)
			continue
		}
		claim("data binding", field, s.DataBindings[i].ID)
		bindings[strings.TrimSpace(s.DataBindings[i].ID)] = true
	}
	for i := range s.ActionVerbs {
		field := fmt.Sprintf("surfaces.action_verbs[%d].id", i)
		if verr := validateSurfaceID(field, pluginID, s.ActionVerbs[i].ID); verr != nil {
			errs = append(errs, *verr)
			continue
		}
		claim("action verb", field, s.ActionVerbs[i].ID)
		verbs[strings.TrimSpace(s.ActionVerbs[i].ID)] = true
	}
	for i := range s.SessionActions {
		field := fmt.Sprintf("surfaces.session_actions[%d].id", i)
		if verr := validateSurfaceID(field, pluginID, s.SessionActions[i].ID); verr != nil {
			errs = append(errs, *verr)
			continue
		}
		claim("session action", field, s.SessionActions[i].ID)
	}
	for i := range s.BoardWidgets {
		w := s.BoardWidgets[i]
		field := fmt.Sprintf("surfaces.board_widgets[%d].id", i)
		if verr := validateSurfaceID(field, pluginID, w.ID); verr != nil {
			errs = append(errs, *verr)
		} else {
			claim("board widget", field, w.ID)
		}
		if w.Presentation != "" && !boardSurfacePresentations[w.Presentation] {
			errs = append(errs, ValidationError{
				Field:         fmt.Sprintf("surfaces.board_widgets[%d].presentation", i),
				Message:       fmt.Sprintf("unknown presentation %q", w.Presentation),
				AllowedValues: []string{"card", "full-bleed", "frameless"},
			})
		}
		for j, ref := range w.DataBindings {
			if !bindings[strings.TrimSpace(ref)] {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("surfaces.board_widgets[%d].data_bindings[%d]", i, j),
					Message: fmt.Sprintf("references undeclared data binding %q", ref),
				})
			}
		}
		for j, ref := range w.ActionVerbs {
			if !verbs[strings.TrimSpace(ref)] {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("surfaces.board_widgets[%d].action_verbs[%d]", i, j),
					Message: fmt.Sprintf("references undeclared action verb %q", ref),
				})
			}
		}
	}

	return errs
}
