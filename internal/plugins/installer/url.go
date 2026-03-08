package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RegistryPlugin describes a single plugin entry in a remote registry index.
type RegistryPlugin struct {
	// ID is the plugin's unique identifier (e.g. "weather").
	ID string `json:"id"`
	// Name is a human-readable display name.
	Name string `json:"name"`
	// Description is a short one-line description of what the plugin does.
	Description string `json:"description"`
	// Version is the latest available version string.
	Version string `json:"version,omitempty"`
	// URL is the download URL for the plugin (single .js file or archive).
	URL string `json:"url"`
	// Type is the plugin runtime type: "goja" (default) or "node".
	Type string `json:"type,omitempty"`
	// Author is the author/organization.
	Author string `json:"author,omitempty"`
	// License is the SPDX license identifier.
	License string `json:"license,omitempty"`
	// Tags is an optional list of category tags.
	Tags []string `json:"tags,omitempty"`
}

// RegistryIndex is the top-level format of a remote plugin registry JSON file.
type RegistryIndex struct {
	// Version is the schema version; currently "1".
	Version string `json:"version"`
	// Plugins is the list of available plugins.
	Plugins []RegistryPlugin `json:"plugins"`
}

// DownloadURL downloads a URL to a local temp file and returns the file path.
// The caller is responsible for removing the temp file when done.
// Only https:// URLs are accepted (http:// is rejected for security).
func DownloadURL(ctx context.Context, rawURL string) (string, error) {
	if err := validatePluginURL(rawURL); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "swarmstr-plugin-installer/1.0")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}

	// Determine extension from URL path.
	ext := ""
	parsedURL, _ := url.Parse(rawURL)
	if parsedURL != nil {
		base := filepath.Base(parsedURL.Path)
		switch {
		case strings.HasSuffix(base, ".tar.gz"), strings.HasSuffix(base, ".tgz"):
			ext = ".tar.gz"
		case strings.HasSuffix(base, ".zip"):
			ext = ".zip"
		case strings.HasSuffix(base, ".js"):
			ext = ".js"
		}
	}

	tmp, err := os.CreateTemp("", "swarmstr-plugin-*"+ext)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer tmp.Close()

	// Limit download to 50 MB.
	const maxBytes = 50 << 20
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, maxBytes+1)); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("write download: %w", err)
	}

	// Check we didn't hit the limit.
	info, _ := tmp.Stat()
	if info != nil && info.Size() > maxBytes {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("download exceeded maximum size of %d bytes", maxBytes)
	}

	return tmp.Name(), nil
}

// FetchRegistry downloads and parses a remote plugin registry index.
// The URL must be https://.
func FetchRegistry(ctx context.Context, registryURL string) (*RegistryIndex, error) {
	if err := validatePluginURL(registryURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create registry request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "swarmstr-plugin-installer/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch registry %s: %w", registryURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch registry %s: HTTP %d", registryURL, resp.StatusCode)
	}

	var index RegistryIndex
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&index); err != nil {
		return nil, fmt.Errorf("parse registry index: %w", err)
	}
	if len(index.Plugins) == 0 && index.Version == "" {
		return nil, fmt.Errorf("registry response does not look like a valid index")
	}
	return &index, nil
}

// validatePluginURL ensures rawURL is a well-formed https:// URL.
func validatePluginURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("URL is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("only https:// URLs are supported for plugin downloads (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("URL %q has no host", rawURL)
	}
	return nil
}
