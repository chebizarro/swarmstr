package surface

import (
	"fmt"
	"testing"

	pluginmanifest "metiq/internal/plugins/manifest"
	"metiq/internal/plugins/sdk"
)

// fakeSource is a test ManifestSource backed by an in-memory manifest map.
type fakeSource struct {
	manifests map[string]sdk.Manifest
	digests   map[string]string
}

func (f *fakeSource) PluginIDs() []string {
	ids := make([]string, 0, len(f.manifests))
	for id := range f.manifests {
		ids = append(ids, id)
	}
	return ids
}

func (f *fakeSource) PluginManifest(id string) (sdk.Manifest, error) {
	mf, ok := f.manifests[id]
	if !ok {
		return sdk.Manifest{}, fmt.Errorf("not found: %s", id)
	}
	return mf, nil
}

func (f *fakeSource) PluginPackageDigest(id string) (string, bool) {
	if digest, ok := f.digests[id]; ok {
		return digest, true
	}
	if _, ok := f.manifests[id]; ok {
		return "sha256:test-" + id, true
	}
	return "", false
}

func surfaceManifest(id string, s *pluginmanifest.SurfaceContributions) sdk.Manifest {
	return sdk.Manifest{ID: id, Version: "1.0.0", Surfaces: s}
}

func TestRegistry_AggregatesContributions(t *testing.T) {
	src := &fakeSource{manifests: map[string]sdk.Manifest{
		"dash": surfaceManifest("dash", &pluginmanifest.SurfaceContributions{
			BoardWidgets: []pluginmanifest.BoardWidgetSurface{
				{ID: "dash.overview", Title: "Overview", Presentation: "card",
					DataBindings: []string{"dash.stats"}, ActionVerbs: []string{"dash.refresh"}},
			},
			DataBindings:   []pluginmanifest.DataBindingSurface{{ID: "dash.stats", Description: "usage"}},
			ActionVerbs:    []pluginmanifest.ActionVerbSurface{{ID: "dash.refresh"}},
			SessionActions: []pluginmanifest.SessionActionSurface{{ID: "dash.pin", MutatesSession: true}},
		}),
		// A plugin with no surfaces must be skipped cleanly.
		"plain": surfaceManifest("plain", nil),
	}}
	r := New(src)

	if b, ok := r.LookupBinding("dash.stats"); !ok || b.PluginID != "dash" || b.PackageDigest != "sha256:test-dash" {
		t.Fatalf("binding not aggregated with package identity: %+v ok=%v", b, ok)
	}
	if identity, ok := r.ResolveGrantIdentity("dash.stats"); !ok || identity.PluginID != "dash" || identity.PackageDigest != "sha256:test-dash" {
		t.Fatalf("grant identity not resolved: %+v ok=%v", identity, ok)
	}
	if v, ok := r.LookupActionVerb("dash.refresh"); !ok || v.PluginID != "dash" {
		t.Fatalf("action verb not aggregated: %+v ok=%v", v, ok)
	}
	sv, ok := r.LookupSessionAction("dash.pin")
	if !ok || !sv.MutatesSession {
		t.Fatalf("session action not aggregated: %+v ok=%v", sv, ok)
	}
	descs := r.Descriptors()
	if len(descs) != 1 || descs[0]["pluginId"] != "dash" || descs[0]["id"] != "dash.overview" {
		t.Fatalf("descriptors wrong: %+v", descs)
	}
	w, b, vb, s := r.Counts()
	if w != 1 || b != 1 || vb != 1 || s != 1 {
		t.Fatalf("counts wrong: w=%d b=%d v=%d s=%d", w, b, vb, s)
	}
}

// TestRegistry_UnknownIDFailsClosed proves an id no plugin declares never
// resolves.
func TestRegistry_UnknownIDFailsClosed(t *testing.T) {
	r := New(&fakeSource{manifests: map[string]sdk.Manifest{
		"dash": surfaceManifest("dash", &pluginmanifest.SurfaceContributions{
			DataBindings: []pluginmanifest.DataBindingSurface{{ID: "dash.stats"}},
		}),
	}})
	if _, ok := r.LookupBinding("dash.unknown"); ok {
		t.Fatal("unknown binding must not resolve")
	}
	if _, ok := r.LookupActionVerb("evil.verb"); ok {
		t.Fatal("unknown action verb must not resolve")
	}
	if _, ok := r.LookupSessionAction("evil.session"); ok {
		t.Fatal("unknown session action must not resolve")
	}
}

// TestRegistry_CrossKindCollisionDropped proves an id re-used across kinds
// within one plugin resolves to NEITHER (fail-closed).
func TestRegistry_CrossKindCollisionDropped(t *testing.T) {
	r := New(&fakeSource{manifests: map[string]sdk.Manifest{
		"dash": surfaceManifest("dash", &pluginmanifest.SurfaceContributions{
			DataBindings: []pluginmanifest.DataBindingSurface{{ID: "dash.x"}},
			ActionVerbs:  []pluginmanifest.ActionVerbSurface{{ID: "dash.x"}},
		}),
	}})
	if _, ok := r.LookupBinding("dash.x"); ok {
		t.Fatal("cross-kind colliding binding must drop fail-closed")
	}
	if _, ok := r.LookupActionVerb("dash.x"); ok {
		t.Fatal("cross-kind colliding verb must drop fail-closed")
	}
}

// TestRegistry_UnownedIDDropped proves the registry drops any contribution id
// not namespaced under its declaring plugin — defense-in-depth against a
// plugin (that skipped manifest validation) claiming another plugin's id or
// aliasing a core board binding.
func TestRegistry_UnownedIDDropped(t *testing.T) {
	r := New(&fakeSource{manifests: map[string]sdk.Manifest{
		"dash": surfaceManifest("dash", &pluginmanifest.SurfaceContributions{
			// Not namespaced under "dash": a core-binding alias and another
			// plugin's namespace. Both must be dropped.
			DataBindings: []pluginmanifest.DataBindingSurface{{ID: "sessions.list"}, {ID: "finance.export"}},
			ActionVerbs:  []pluginmanifest.ActionVerbSurface{{ID: "dash.ok"}},
		}),
	}})
	if _, ok := r.LookupBinding("sessions.list"); ok {
		t.Fatal("core-binding alias must not resolve as a plugin binding")
	}
	if _, ok := r.LookupBinding("finance.export"); ok {
		t.Fatal("cross-plugin namespace must not resolve")
	}
	// The correctly-namespaced verb still resolves.
	if _, ok := r.LookupActionVerb("dash.ok"); !ok {
		t.Fatal("namespaced verb should resolve")
	}
}

// TestRegistry_ReservedIDDropped proves a plugin whose id is a prefix of a
// core board binding cannot alias it: the reserved id is dropped even though it
// is correctly namespaced under the plugin (plugin "sessions" → "sessions.list").
func TestRegistry_ReservedIDDropped(t *testing.T) {
	r := New(&fakeSource{manifests: map[string]sdk.Manifest{
		"sessions": surfaceManifest("sessions", &pluginmanifest.SurfaceContributions{
			DataBindings: []pluginmanifest.DataBindingSurface{{ID: "sessions.list"}, {ID: "sessions.mine"}},
		}),
	}})
	r.SetReservedIDs([]string{"sessions.list", "usage.status", "health"})
	if _, ok := r.LookupBinding("sessions.list"); ok {
		t.Fatal("reserved core binding id must not resolve as a plugin binding")
	}
	// A non-reserved, correctly-namespaced binding still resolves.
	if _, ok := r.LookupBinding("sessions.mine"); !ok {
		t.Fatal("non-reserved namespaced binding should resolve")
	}
}

// TestRegistry_RefreshPicksUpChanges proves Refresh re-aggregates from live
// source state.
func TestRegistry_RefreshPicksUpChanges(t *testing.T) {
	src := &fakeSource{manifests: map[string]sdk.Manifest{}}
	r := New(src)
	if _, ok := r.LookupBinding("late.stats"); ok {
		t.Fatal("binding should not exist before plugin loads")
	}
	src.manifests["late"] = surfaceManifest("late", &pluginmanifest.SurfaceContributions{
		DataBindings: []pluginmanifest.DataBindingSurface{{ID: "late.stats"}},
	})
	r.Refresh()
	if _, ok := r.LookupBinding("late.stats"); !ok {
		t.Fatal("binding should resolve after refresh")
	}
}

// TestRegistry_RefreshScope reports whether the scoped plugin exists.
func TestRegistry_RefreshScope(t *testing.T) {
	r := New(&fakeSource{manifests: map[string]sdk.Manifest{
		"dash": surfaceManifest("dash", &pluginmanifest.SurfaceContributions{
			DataBindings: []pluginmanifest.DataBindingSurface{{ID: "dash.stats"}},
		}),
	}})
	if !r.RefreshScope("dash") {
		t.Fatal("expected scope refresh to find loaded plugin")
	}
	if r.RefreshScope("ghost") {
		t.Fatal("expected scope refresh to report missing plugin")
	}
}
