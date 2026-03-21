// Package registry implements Nostr-native plugin discovery and distribution.
//
// Plugins are published as kind 30617 parameterized-replaceable events.
// The event's "d" tag is the plugin ID; the content is a JSON-encoded
// PluginManifest.  Authors self-sign their plugin releases; clients verify
// the signature and optional content checksum before installing.
//
// Publishing is done from the swarmstr CLI (plugin-publish).
// Searching/fetching is done at install time.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/plugins/installer"
)

// KindPluginManifest is the Nostr event kind used for plugin manifests.
// We use kind 30617 (parameterized replaceable, in the 30000–39999 range).
const KindPluginManifest = nostr.Kind(30617)

// ─── Types ────────────────────────────────────────────────────────────────────

// ToolSpec describes a single tool exported by a plugin.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// PluginManifest is the JSON content of a kind 30617 event.
type PluginManifest struct {
	ID          string     `json:"id"`
	Version     string     `json:"version"`
	Description string     `json:"description,omitempty"`
	Runtime     string     `json:"runtime"` // "goja" | "npm"
	Main        string     `json:"main,omitempty"`
	Tools       []ToolSpec `json:"tools,omitempty"`
	// DownloadURL is an optional HTTPS URL for the plugin archive (.tar.gz or .zip).
	DownloadURL string `json:"download_url,omitempty"`
	// Checksum is an optional "sha256:<hex>" checksum of the archive content.
	Checksum string `json:"checksum,omitempty"`
	// License is an SPDX license identifier.
	License string `json:"license,omitempty"`
	// Homepage is an optional human-readable URL.
	Homepage string `json:"homepage,omitempty"`
}

// PluginEntry is a plugin manifest together with its Nostr provenance.
type PluginEntry struct {
	Manifest     PluginManifest
	AuthorPubKey string    // hex pubkey of the signer
	EventID      string    // hex event ID
	PublishedAt  time.Time // event timestamp
	Relays       []string  // relays this was fetched from
}

// ─── Registry ─────────────────────────────────────────────────────────────────

// Registry provides search, fetch, verify, and install operations for
// Nostr-native plugins.
type Registry struct {
	pool   *nostr.Pool
	relays []string
}

// NewRegistry creates a Registry using a fresh pool pointed at relays.
func NewRegistry(relays []string) *Registry {
	pool := nostr.NewPool(nostr.PoolOptions{PenaltyBox: true})
	return &Registry{pool: pool, relays: relays}
}

// Close releases pool connections.
func (r *Registry) Close() {
	r.pool.Close("registry closed")
}

// Search queries relays for plugins whose "d" tag contains query (case-insensitive
// substring match on plugin ID or description).  limit defaults to 20.
func (r *Registry) Search(ctx context.Context, query string, limit int) ([]PluginEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	filter := nostr.Filter{
		Kinds: []nostr.Kind{KindPluginManifest},
		Limit: limit * 3, // fetch extra to allow client-side filtering
	}
	// If the query looks like a specific plugin ID, use the d-tag filter.
	if query != "" && !strings.ContainsAny(query, " \t") {
		filter.Tags = nostr.TagMap{"d": {query}}
	}

	seen := map[string]struct{}{}
	var results []PluginEntry
	for relayEvent := range r.pool.FetchMany(ctx, r.relays, filter, nostr.SubscriptionOptions{}) {
		evt := relayEvent.Event
		if !evt.CheckID() || !evt.VerifySignature() {
			continue
		}
		if evt.Kind != KindPluginManifest {
			continue
		}
		id := evt.ID.Hex()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		entry, err := parsePluginEvent(evt)
		if err != nil {
			continue
		}
		// Client-side substring filter when we didn't use d-tag.
		if query != "" && strings.ContainsAny(query, " \t") {
			q := strings.ToLower(query)
			haystack := strings.ToLower(entry.Manifest.ID + " " + entry.Manifest.Description)
			if !strings.Contains(haystack, q) {
				continue
			}
		}
		results = append(results, entry)
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

// Fetch retrieves a specific plugin by author pubkey and plugin ID.
// Returns ErrNotFound if no matching event is found.
func (r *Registry) Fetch(ctx context.Context, authorPubKey, pluginID string) (*PluginEntry, error) {
	pk, err := nostruntime.ParsePubKey(authorPubKey)
	if err != nil {
		return nil, fmt.Errorf("invalid author pubkey: %w", err)
	}
	filter := nostr.Filter{
		Kinds:   []nostr.Kind{KindPluginManifest},
		Authors: []nostr.PubKey{pk},
		Tags:    nostr.TagMap{"d": {pluginID}},
		Limit:   1,
	}
	for relayEvent := range r.pool.FetchMany(ctx, r.relays, filter, nostr.SubscriptionOptions{}) {
		evt := relayEvent.Event
		if !evt.CheckID() || !evt.VerifySignature() {
			continue
		}
		if evt.Kind != KindPluginManifest {
			continue
		}
		entry, err := parsePluginEvent(evt)
		if err != nil {
			return nil, err
		}
		return &entry, nil
	}
	return nil, ErrNotFound
}

// Publish signs and publishes a PluginManifest to relays using privateKey.
// Returns the published event ID.
func (r *Registry) Publish(ctx context.Context, privateKey string, m PluginManifest) (string, error) {
	sk, err := nostruntime.ParseSecretKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("invalid private key: %w", err)
	}
	content, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}
	evt := nostr.Event{
		Kind:      KindPluginManifest,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"d", m.ID},
			{"t", "plugin"},
			{"version", m.Version},
			{"runtime", m.Runtime},
		},
		Content: string(content),
	}
	if err := evt.Sign([32]byte(sk)); err != nil {
		return "", fmt.Errorf("sign event: %w", err)
	}

	published := false
	var lastErr error
	for result := range r.pool.PublishMany(ctx, r.relays, evt) {
		if result.Error == nil {
			published = true
		} else {
			lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
		}
	}
	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relay accepted the publish")
		}
		return "", lastErr
	}
	return evt.ID.Hex(), nil
}

// ─── Verification ─────────────────────────────────────────────────────────────

// VerifyChecksum verifies data against a "sha256:<hex>" checksum string.
// Returns nil if the checksum matches or if checksum is empty.
func VerifyChecksum(data []byte, checksum string) error {
	if checksum == "" {
		return nil
	}
	checksum = strings.TrimSpace(checksum)
	algo, expected, ok := strings.Cut(checksum, ":")
	if !ok || strings.ToLower(algo) != "sha256" {
		return fmt.Errorf("unsupported checksum format %q (expected sha256:<hex>)", checksum)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, strings.TrimSpace(expected)) {
		return fmt.Errorf("checksum mismatch: got sha256:%s, expected %s", got, expected)
	}
	return nil
}

// ComputeChecksum returns a "sha256:<hex>" checksum string for data.
func ComputeChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ─── Installation ──────────────────────────────────────────────────────────────

// Install downloads and installs a plugin from its manifest into destDir.
// destDir/<pluginID>/ will contain the plugin files.
// The download URL and checksum in the manifest are used; if DownloadURL is
// empty, Install returns ErrNoDownloadURL.
func Install(ctx context.Context, entry PluginEntry, destDir string) (string, error) {
	m := entry.Manifest
	if m.DownloadURL == "" {
		return "", ErrNoDownloadURL
	}
	pluginDir := filepath.Join(destDir, sanitizePath(m.ID))
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return "", fmt.Errorf("create plugin dir: %w", err)
	}

	// Download archive.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.DownloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download plugin: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download plugin: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64 MiB cap
	if err != nil {
		return "", fmt.Errorf("read plugin archive: %w", err)
	}

	// Verify checksum.
	if err := VerifyChecksum(data, m.Checksum); err != nil {
		return "", fmt.Errorf("verify checksum: %w", err)
	}

	// Write archive to temp file, then extract.
	ext := archiveExt(m.DownloadURL)
	tmp, err := os.CreateTemp("", "swarmstr-plugin-"+sanitizePath(m.ID)+"-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp archive: %w", err)
	}
	tmpFile := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temp archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write temp archive: %w", err)
	}
	defer os.Remove(tmpFile)

	if _, err := installer.New().ExtractArchive(ctx, tmpFile, pluginDir); err != nil {
		return "", fmt.Errorf("extract plugin: %w", err)
	}
	return pluginDir, nil
}

// ─── Sentinel errors ──────────────────────────────────────────────────────────

// ErrNotFound is returned when a plugin is not found on any relay.
var ErrNotFound = fmt.Errorf("plugin not found on relays")

// ErrNoDownloadURL is returned when a plugin manifest has no download URL.
var ErrNoDownloadURL = fmt.Errorf("plugin manifest has no download_url")

// ─── helpers ──────────────────────────────────────────────────────────────────

func parsePluginEvent(evt nostr.Event) (PluginEntry, error) {
	var m PluginManifest
	if err := json.Unmarshal([]byte(evt.Content), &m); err != nil {
		return PluginEntry{}, fmt.Errorf("parse plugin manifest: %w", err)
	}
	if strings.TrimSpace(m.ID) == "" {
		return PluginEntry{}, fmt.Errorf("plugin manifest missing id")
	}
	return PluginEntry{
		Manifest:     m,
		AuthorPubKey: evt.PubKey.Hex(),
		EventID:      evt.ID.Hex(),
		PublishedAt:  time.Unix(int64(evt.CreatedAt), 0),
	}, nil
}

func sanitizePath(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func archiveExt(url string) string {
	url = strings.ToLower(url)
	switch {
	case strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz"):
		return ".tar.gz"
	case strings.HasSuffix(url, ".zip"):
		return ".zip"
	default:
		return ".tar.gz"
	}
}
