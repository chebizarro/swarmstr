package installer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestClawHubClient_Search(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/plugins" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plugins": []map[string]any{{"id": "p1", "name": "Plugin 1"}},
		})
	}))
	defer s.Close()

	c := NewClawHubClient(s.URL, s.Client())
	plugins, err := c.Search(context.Background(), "plugin")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(plugins) != 1 || plugins[0].ID != "p1" {
		t.Fatalf("unexpected plugins: %+v", plugins)
	}
}

func TestClawHubClient_GetPluginInfo(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/plugins/p2" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(ClawHubPlugin{ID: "p2", Package: "left-pad@1.3.0"})
	}))
	defer s.Close()

	c := NewClawHubClient(s.URL, s.Client())
	p, err := c.GetPluginInfo(context.Background(), "p2")
	if err != nil {
		t.Fatalf("GetPluginInfo failed: %v", err)
	}
	if p.ID != "p2" {
		t.Fatalf("unexpected plugin: %+v", p)
	}
}

func TestClawHubInstall_MissingPackageAndDistURL(t *testing.T) {
	err := installClawHubPlugin(context.Background(), &ClawHubPlugin{ID: "x"}, t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClawHubUpdate_MissingPackageAndDistURL(t *testing.T) {
	err := updateClawHubPlugin(context.Background(), &ClawHubPlugin{ID: "x"}, t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClawHubInstallUpdateMissingDistribution(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ClawHubPlugin{ID: "plug", Name: "Plug", Version: "1.0.0"})
	}))
	defer srv.Close()
	client := NewClawHubClient(srv.URL, srv.Client())
	if err := client.Install(context.Background(), "plug", "1.0.0", t.TempDir()); err == nil || !strings.Contains(err.Error(), "missing install package") {
		t.Fatalf("expected missing install package error, got %v", err)
	}
	if err := client.Update(context.Background(), "plug", "", t.TempDir()); err == nil || !strings.Contains(err.Error(), "missing install package") {
		t.Fatalf("expected missing install package update error, got %v", err)
	}
	if err := client.Install(context.Background(), "", "", t.TempDir()); err == nil {
		t.Fatal("expected plugin id required")
	}
}

func TestRemoveFileNoopsForMissingPath(t *testing.T) {
	removeFile(filepath.Join(t.TempDir(), "missing"))
}
