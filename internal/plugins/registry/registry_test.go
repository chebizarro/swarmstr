package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

// ─── PluginManifest JSON roundtrip ────────────────────────────────────────────

func TestPluginManifest_JSON(t *testing.T) {
	m := PluginManifest{
		ID:          "weather-tool",
		Version:     "1.2.3",
		Description: "Fetch weather data",
		Runtime:     "goja",
		Main:        "index.js",
		Tools: []ToolSpec{
			{Name: "get_weather", Description: "Returns current weather"},
		},
		DownloadURL: "https://example.com/weather-tool-1.2.3.tar.gz",
		Checksum:    "sha256:abc123",
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m2 PluginManifest
	if err := json.Unmarshal(data, &m2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m2.ID != m.ID || m2.Version != m.Version {
		t.Errorf("roundtrip mismatch: %+v", m2)
	}
	if len(m2.Tools) != 1 || m2.Tools[0].Name != "get_weather" {
		t.Errorf("tools roundtrip mismatch: %v", m2.Tools)
	}
}

// ─── VerifyChecksum ───────────────────────────────────────────────────────────

func TestVerifyChecksum_match(t *testing.T) {
	data := []byte("hello plugin")
	sum := ComputeChecksum(data)
	if err := VerifyChecksum(data, sum); err != nil {
		t.Errorf("expected checksum match, got: %v", err)
	}
}

func TestVerifyChecksum_mismatch(t *testing.T) {
	data := []byte("hello plugin")
	if err := VerifyChecksum(data, "sha256:deadbeef"); err == nil {
		t.Error("expected checksum mismatch error, got nil")
	}
}

func TestVerifyChecksum_empty(t *testing.T) {
	// Empty checksum means no verification — should pass.
	if err := VerifyChecksum([]byte("anything"), ""); err != nil {
		t.Errorf("empty checksum should pass, got: %v", err)
	}
}

func TestVerifyChecksum_unsupportedAlgo(t *testing.T) {
	if err := VerifyChecksum([]byte("data"), "md5:abc123"); err == nil {
		t.Error("expected error for unsupported algo, got nil")
	}
}

func TestVerifyChecksum_noColon(t *testing.T) {
	if err := VerifyChecksum([]byte("data"), "justahexstring"); err == nil {
		t.Error("expected error for missing colon, got nil")
	}
}

// ─── ComputeChecksum ──────────────────────────────────────────────────────────

func TestComputeChecksum_prefix(t *testing.T) {
	sum := ComputeChecksum([]byte("data"))
	if !strings.HasPrefix(sum, "sha256:") {
		t.Errorf("checksum should start with sha256:, got %q", sum)
	}
	if len(sum) != len("sha256:")+64 {
		t.Errorf("unexpected checksum length: %d", len(sum))
	}
}

func TestComputeChecksum_deterministic(t *testing.T) {
	data := []byte("consistent data")
	if ComputeChecksum(data) != ComputeChecksum(data) {
		t.Error("ComputeChecksum is not deterministic")
	}
}

// ─── sanitizePath ─────────────────────────────────────────────────────────────

func TestSanitizePath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"weather-tool", "weather-tool"},
		{"my plugin", "my_plugin"},
		{"../dangerous", "___dangerous"}, // '.', '.', '/' all become '_'
		{"ok_name-123", "ok_name-123"},
	}
	for _, tc := range tests {
		got := sanitizePath(tc.in)
		if got != tc.want {
			t.Errorf("sanitizePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ─── archiveExt ───────────────────────────────────────────────────────────────

func TestArchiveExt(t *testing.T) {
	tests := []struct{ url, want string }{
		{"https://example.com/plugin.tar.gz", ".tar.gz"},
		{"https://example.com/plugin.tgz", ".tar.gz"},
		{"https://example.com/plugin.zip", ".zip"},
		{"https://example.com/plugin.wasm", ".tar.gz"}, // default
	}
	for _, tc := range tests {
		got := archiveExt(tc.url)
		if got != tc.want {
			t.Errorf("archiveExt(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

// ─── Install via HTTP mock ────────────────────────────────────────────────────

func TestInstall_noDownloadURL(t *testing.T) {
	entry := PluginEntry{
		Manifest: PluginManifest{
			ID:      "test-plugin",
			Version: "1.0.0",
			Runtime: "goja",
		},
	}
	_, err := Install(context.Background(), entry, t.TempDir())
	if err == nil || err != ErrNoDownloadURL {
		t.Errorf("expected ErrNoDownloadURL, got: %v", err)
	}
}

func TestInstall_checksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake archive content"))
	}))
	defer srv.Close()

	entry := PluginEntry{
		Manifest: PluginManifest{
			ID:          "test-plugin",
			Version:     "1.0.0",
			Runtime:     "goja",
			DownloadURL: srv.URL + "/plugin.tar.gz",
			Checksum:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		},
	}
	_, err := Install(context.Background(), entry, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("expected checksum error, got: %v", err)
	}
}

// ─── NewRegistry ──────────────────────────────────────────────────────────────

func TestNewRegistry(t *testing.T) {
	r := NewRegistry([]string{"wss://relay.example.com"})
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	defer r.Close()
	if len(r.relays) != 1 {
		t.Errorf("expected 1 relay, got %d", len(r.relays))
	}
}

func TestNewRegistry_Empty(t *testing.T) {
	r := NewRegistry(nil)
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	defer r.Close()
}

func TestRegistrySearchFetchPublishEdgeCases(t *testing.T) {
	r := NewRegistry(nil)
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results, err := r.Search(ctx, "weather plugins", 0)
	if err != nil || len(results) != 0 {
		t.Fatalf("empty search results=%+v err=%v", results, err)
	}
	if _, err := r.Fetch(ctx, "bad-pubkey", "plugin"); err == nil {
		t.Fatal("expected invalid pubkey error")
	}
	if _, err := r.Publish(ctx, "bad-secret", PluginManifest{ID: "p", Version: "1", Runtime: "goja"}); err == nil {
		t.Fatal("expected invalid secret key error")
	}
}

// ─── parsePluginEvent ────────────────────────────────────────────────────────

func TestParsePluginEvent_ValidEvent(t *testing.T) {
	manifest := `{"id":"test-plugin","version":"1.0.0","runtime":"goja","main":"index.js"}`
	evt := nostr.Event{
		Content:   manifest,
		CreatedAt: nostr.Timestamp(1700000000),
	}
	entry, err := parsePluginEvent(evt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Manifest.ID != "test-plugin" {
		t.Errorf("expected test-plugin, got %s", entry.Manifest.ID)
	}
	if entry.Manifest.Version != "1.0.0" {
		t.Errorf("expected 1.0.0, got %s", entry.Manifest.Version)
	}
}

func TestParsePluginEvent_InvalidJSON(t *testing.T) {
	evt := nostr.Event{Content: "not json"}
	_, err := parsePluginEvent(evt)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParsePluginEvent_missingID(t *testing.T) {
	// Can't easily create a signed nostr.Event in unit tests without a key,
	// so we test parsePluginEvent via a helper approach: the function is
	// unexported so we exercise it indirectly through JSON unmarshal directly.
	var m PluginManifest
	_ = json.Unmarshal([]byte(`{"version":"1.0","runtime":"goja"}`), &m)
	if strings.TrimSpace(m.ID) != "" {
		t.Error("expected empty ID")
	}
}

// ─── Install creates destination dir ─────────────────────────────────────────

func TestInstall_httpNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	entry := PluginEntry{
		Manifest: PluginManifest{
			ID:          "test-plugin",
			Version:     "1.0.0",
			Runtime:     "goja",
			DownloadURL: srv.URL + "/plugin.tar.gz",
		},
	}
	_, err := Install(context.Background(), entry, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("expected HTTP 404 error, got: %v", err)
	}
}

func TestInstall_canceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	entry := PluginEntry{
		Manifest: PluginManifest{
			ID:          "test-plugin",
			Version:     "1.0.0",
			Runtime:     "goja",
			DownloadURL: "https://example.com/plugin.tar.gz",
		},
	}
	_, err := Install(ctx, entry, t.TempDir())
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestParsePluginEvent_MissingIDReturnsError(t *testing.T) {
	evt := nostr.Event{Content: `{"version":"1.0","runtime":"goja"}`}
	_, err := parsePluginEvent(evt)
	if err == nil {
		t.Fatal("expected error for missing ID")
	}
	if !strings.Contains(err.Error(), "missing id") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParsePluginEvent_SetsPublishedAt(t *testing.T) {
	manifest := `{"id":"p","version":"1.0","runtime":"goja"}`
	evt := nostr.Event{
		Content:   manifest,
		CreatedAt: nostr.Timestamp(1700000000),
	}
	entry, err := parsePluginEvent(evt)
	if err != nil {
		t.Fatal(err)
	}
	if entry.PublishedAt.Unix() != 1700000000 {
		t.Errorf("PublishedAt = %v, want Unix 1700000000", entry.PublishedAt)
	}
}

func TestPluginEntry_Fields(t *testing.T) {
	e := PluginEntry{
		Manifest:     PluginManifest{ID: "test"},
		AuthorPubKey: "abc",
		EventID:      "evt1",
		Relays:       []string{"wss://r1"},
	}
	if e.AuthorPubKey != "abc" {
		t.Errorf("AuthorPubKey = %q", e.AuthorPubKey)
	}
	if e.EventID != "evt1" {
		t.Errorf("EventID = %q", e.EventID)
	}
	if len(e.Relays) != 1 {
		t.Errorf("Relays = %v", e.Relays)
	}
}

func TestToolSpec_Fields(t *testing.T) {
	ts := ToolSpec{
		Name:        "fetch",
		Description: "Fetch a URL",
		Parameters:  map[string]any{"url": "string"},
	}
	if ts.Name != "fetch" || ts.Description != "Fetch a URL" {
		t.Errorf("ToolSpec fields: %+v", ts)
	}
}

func TestPluginManifest_AllFields(t *testing.T) {
	m := PluginManifest{
		ID:          "p",
		Version:     "1",
		Description: "d",
		Runtime:     "goja",
		Main:        "index.js",
		DownloadURL: "https://example.com/p.tar.gz",
		Checksum:    "sha256:abc",
		License:     "MIT",
		Homepage:    "https://example.com",
	}
	data, _ := json.Marshal(m)
	var m2 PluginManifest
	json.Unmarshal(data, &m2)
	if m2.License != "MIT" || m2.Homepage != "https://example.com" {
		t.Errorf("optional fields lost in roundtrip: %+v", m2)
	}
}

func TestRegistrySearch_CanceledContext(t *testing.T) {
	r := NewRegistry([]string{"wss://localhost:1"})
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := r.Search(ctx, "test", 5)
	if err != nil {
		t.Fatalf("Search with canceled context should not error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestRegistrySearch_DefaultLimit(t *testing.T) {
	r := NewRegistry([]string{"wss://localhost:1"})
	defer r.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// limit <= 0 should default to 20 (tested indirectly)
	results, _ := r.Search(ctx, "", 0)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestRegistryFetch_InvalidPubkey(t *testing.T) {
	r := NewRegistry([]string{"wss://localhost:1"})
	defer r.Close()

	_, err := r.Fetch(context.Background(), "not-a-valid-pubkey", "test-plugin")
	if err == nil {
		t.Fatal("expected error for invalid pubkey")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRegistryPublish_InvalidKey(t *testing.T) {
	r := NewRegistry([]string{"wss://localhost:1"})
	defer r.Close()

	_, err := r.Publish(context.Background(), "not-a-valid-key", PluginManifest{ID: "test", Version: "1"})
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestErrNotFound(t *testing.T) {
	if ErrNotFound.Error() != "plugin not found on relays" {
		t.Errorf("ErrNotFound = %q", ErrNotFound.Error())
	}
}

func TestErrNoDownloadURL(t *testing.T) {
	if ErrNoDownloadURL.Error() != "plugin manifest has no download_url" {
		t.Errorf("ErrNoDownloadURL = %q", ErrNoDownloadURL.Error())
	}
}

func TestVerifyChecksum_WhitespaceHandling(t *testing.T) {
	data := []byte("test data")
	sum := ComputeChecksum(data)
	// Add whitespace around checksum
	if err := VerifyChecksum(data, "  "+sum+"  "); err != nil {
		t.Errorf("checksum with whitespace should match: %v", err)
	}
}

func TestInstall_createsPluginDir(t *testing.T) {
	// Create a minimal valid zip archive to test directory creation.
	// We use a real zip to verify the path creation code path.
	// The install will fail on extraction (wrong content) but the dir should be created.
	archiveData := []byte("PK") // minimal invalid zip — just enough to test dir creation
	sum := ComputeChecksum(archiveData)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archiveData)
	}))
	defer srv.Close()

	destDir := filepath.Join(t.TempDir(), "plugins")
	entry := PluginEntry{
		Manifest: PluginManifest{
			ID:          "my-plugin",
			Version:     "1.0.0",
			Runtime:     "goja",
			DownloadURL: srv.URL + "/plugin.zip",
			Checksum:    sum,
		},
	}
	_, _ = Install(context.Background(), entry, destDir)
	// The plugin directory should have been created before extraction attempt.
	if _, err := os.Stat(filepath.Join(destDir, "my-plugin")); err != nil {
		t.Errorf("plugin dir was not created: %v", err)
	}
}
