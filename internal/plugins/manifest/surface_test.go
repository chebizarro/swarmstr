package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func hasFieldError(err error, field string) bool {
	errs, ok := err.(ValidationErrors)
	if !ok {
		return false
	}
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}

// TestSchemaVersionIsV4 pins the current schema version so the v4 bump is
// explicit and back-compat lower bound is unchanged.
func TestSchemaVersionIsV4(t *testing.T) {
	if SchemaVersion != 4 {
		t.Fatalf("SchemaVersion = %d, want 4", SchemaVersion)
	}
	if MinSupportedVersion != 1 {
		t.Fatalf("MinSupportedVersion = %d, want 1", MinSupportedVersion)
	}
}

// TestV3ManifestBackCompat proves a v3 manifest with no Surfaces block still
// parses and validates unchanged under the v4 host.
func TestV3ManifestBackCompat(t *testing.T) {
	raw := []byte(`{
		"schema_version": 3,
		"id": "legacy-plugin",
		"version": "1.0.0",
		"runtime": "goja",
		"capabilities": {"tools": [{"name": "do_thing", "description": "x"}]}
	}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("v3 manifest rejected under v4 host: %v", err)
	}
	if m.Surfaces != nil {
		t.Fatalf("expected nil Surfaces for v3 manifest, got %+v", m.Surfaces)
	}
	if !m.Surfaces.IsEmpty() {
		t.Fatalf("nil Surfaces must read as empty")
	}
}

// TestV4ManifestParsesSurfaces proves the v4 UI-surface contribution block
// round-trips through Parse and validates.
func TestV4ManifestParsesSurfaces(t *testing.T) {
	raw := []byte(`{
		"schema_version": 4,
		"id": "dash-plugin",
		"version": "2.1.0",
		"runtime": "goja",
		"surfaces": {
			"board_widgets": [
				{"id": "dash-plugin.overview", "title": "Overview", "presentation": "card",
				 "data_bindings": ["dash-plugin.stats"], "action_verbs": ["dash-plugin.refresh"]}
			],
			"data_bindings": [{"id": "dash-plugin.stats", "description": "usage stats"}],
			"action_verbs": [{"id": "dash-plugin.refresh"}],
			"session_actions": [{"id": "dash-plugin.pin", "mutates_session": true}]
		}
	}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("valid v4 manifest rejected: %v", err)
	}
	if m.Surfaces == nil || len(m.Surfaces.BoardWidgets) != 1 {
		t.Fatalf("surfaces not decoded: %+v", m.Surfaces)
	}
	if m.Surfaces.BoardWidgets[0].DataBindings[0] != "dash-plugin.stats" {
		t.Fatalf("widget binding ref not decoded: %+v", m.Surfaces.BoardWidgets[0])
	}
	if !m.Surfaces.SessionActions[0].MutatesSession {
		t.Fatalf("session action mutates_session flag lost")
	}
}

func TestValidateSurfaceContributions_Nil(t *testing.T) {
	if errs := ValidateSurfaceContributions("p", nil); errs != nil {
		t.Fatalf("nil surfaces must validate: %v", errs)
	}
}

func TestValidateSurfaceContributions_IDShape(t *testing.T) {
	bad := []string{"", "Dash.Plugin", "dash plugin", "-dash", "dash.", strings.Repeat("a", 65)}
	for _, id := range bad {
		s := &SurfaceContributions{DataBindings: []DataBindingSurface{{ID: id}}}
		if errs := ValidateSurfaceContributions("dash-plugin", s); len(errs) == 0 {
			t.Errorf("expected rejection for binding id %q", id)
		}
	}
	good := &SurfaceContributions{DataBindings: []DataBindingSurface{{ID: "dash-plugin.stats"}}}
	if errs := ValidateSurfaceContributions("dash-plugin", good); len(errs) != 0 {
		t.Fatalf("valid binding id rejected: %v", errs)
	}
}

// TestValidateSurfaceContributions_OwnershipRequired proves a contribution id
// must be namespaced under the declaring plugin: a plugin can neither claim
// another plugin's namespace nor alias a core board binding id.
func TestValidateSurfaceContributions_OwnershipRequired(t *testing.T) {
	for _, id := range []string{"other.stats", "sessions.list", "stats", "dash-pluginX.stats"} {
		s := &SurfaceContributions{DataBindings: []DataBindingSurface{{ID: id}}}
		errs := ValidateSurfaceContributions("dash-plugin", s)
		if len(errs) == 0 {
			t.Errorf("expected ownership rejection for id %q under plugin dash-plugin", id)
		}
	}
	ok := &SurfaceContributions{DataBindings: []DataBindingSurface{{ID: "dash-plugin.stats"}}}
	if errs := ValidateSurfaceContributions("dash-plugin", ok); len(errs) != 0 {
		t.Fatalf("namespaced id rejected: %v", errs)
	}
}

// TestValidateSurfaceContributions_DottedPluginIDRejected proves a plugin
// whose id contains a dot cannot declare UI surfaces (this is what makes
// namespace ownership unambiguous — no nested namespaces).
func TestValidateSurfaceContributions_DottedPluginIDRejected(t *testing.T) {
	s := &SurfaceContributions{DataBindings: []DataBindingSurface{{ID: "finance.reports.export"}}}
	if errs := ValidateSurfaceContributions("finance.reports", s); len(errs) == 0 {
		t.Fatal("expected rejection for a dotted plugin id declaring surfaces")
	}
	// The same contribution under the canonical single-segment owner is fine.
	ok := &SurfaceContributions{DataBindings: []DataBindingSurface{{ID: "finance.export"}}}
	if errs := ValidateSurfaceContributions("finance", ok); len(errs) != 0 {
		t.Fatalf("canonical owner rejected: %v", errs)
	}
}

func TestValidateSurfaceContributions_CollisionAcrossKinds(t *testing.T) {
	s := &SurfaceContributions{
		DataBindings: []DataBindingSurface{{ID: "p.thing"}},
		ActionVerbs:  []ActionVerbSurface{{ID: "p.thing"}},
	}
	errs := ValidateSurfaceContributions("p", s)
	if len(errs) == 0 {
		t.Fatal("expected collision error when a binding and verb share an id")
	}
	if !strings.Contains(errs.Error(), "collides") {
		t.Fatalf("expected collision message, got %v", errs)
	}
}

func TestValidateSurfaceContributions_WidgetRefMustResolve(t *testing.T) {
	s := &SurfaceContributions{
		BoardWidgets: []BoardWidgetSurface{{ID: "p.w", DataBindings: []string{"p.missing"}}},
	}
	if errs := ValidateSurfaceContributions("p", s); len(errs) == 0 {
		t.Fatal("expected error for widget referencing undeclared binding")
	}
	s2 := &SurfaceContributions{
		BoardWidgets: []BoardWidgetSurface{{ID: "p.w", ActionVerbs: []string{"p.missingverb"}}},
	}
	if errs := ValidateSurfaceContributions("p", s2); len(errs) == 0 {
		t.Fatal("expected error for widget referencing undeclared action verb")
	}
}

func TestValidateSurfaceContributions_BadPresentation(t *testing.T) {
	s := &SurfaceContributions{BoardWidgets: []BoardWidgetSurface{{ID: "p.w", Presentation: "bogus"}}}
	if errs := ValidateSurfaceContributions("p", s); !hasFieldError(errs, "surfaces.board_widgets[0].presentation") {
		t.Fatalf("expected presentation error, got %v", errs)
	}
}

// TestValidateViaManifestSurfaces proves Manifest.Validate wires the surface
// validator (a bad surface fails the whole manifest).
func TestValidateViaManifestSurfaces(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 4,
		ID:            "p",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Surfaces:      &SurfaceContributions{ActionVerbs: []ActionVerbSurface{{ID: "BAD ID"}}},
	}
	if err := Validate(m); err == nil {
		t.Fatal("expected manifest validation to fail on bad surface id")
	}
	// And a JSON round-trip of a good manifest validates.
	raw, _ := json.Marshal(m)
	var decoded Manifest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}
}
