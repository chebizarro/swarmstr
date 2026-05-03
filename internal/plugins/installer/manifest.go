package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenClawPluginManifest represents an openclaw.plugin.json file.
type OpenClawPluginManifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Kind        []string `json:"kind"`
	Entry       string   `json:"entry"`

	Capabilities struct {
		Tools     bool `json:"tools"`
		Providers bool `json:"providers"`
		Channels  bool `json:"channels"`
		Hooks     bool `json:"hooks"`
		Services  bool `json:"services"`
	} `json:"capabilities"`

	ConfigSchema map[string]any `json:"configSchema"`
}

// LoadOpenClawManifest reads and parses an OpenClaw plugin manifest.
// It first tries openclaw.plugin.json and falls back to package.json openclaw block.
func LoadOpenClawManifest(pluginPath string) (*OpenClawPluginManifest, error) {
	pluginPath = strings.TrimSpace(pluginPath)
	if pluginPath == "" {
		return nil, fmt.Errorf("pluginPath is required")
	}

	manifestPath := filepath.Join(pluginPath, "openclaw.plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read openclaw manifest: %w", err)
		}
		return loadFromPackageJSON(pluginPath)
	}

	var mf OpenClawPluginManifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("parse openclaw manifest: %w", err)
	}
	if strings.TrimSpace(mf.ID) == "" {
		return nil, fmt.Errorf("openclaw manifest missing id")
	}
	return &mf, nil
}

func loadFromPackageJSON(pluginPath string) (*OpenClawPluginManifest, error) {
	pkgPath := filepath.Join(pluginPath, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return nil, fmt.Errorf("read package.json: %w", err)
	}

	var pkg struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
		Main        string `json:"main"`
		OpenClaw    struct {
			ID    string   `json:"id"`
			Kind  []string `json:"kind"`
			Entry string   `json:"entry"`
		} `json:"openclaw"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("parse package.json: %w", err)
	}

	id := strings.TrimSpace(pkg.OpenClaw.ID)
	if id == "" {
		id = strings.TrimSpace(pkg.Name)
	}
	if id == "" {
		return nil, fmt.Errorf("package.json missing openclaw.id/name")
	}

	entry := strings.TrimSpace(pkg.OpenClaw.Entry)
	if entry == "" {
		entry = strings.TrimSpace(pkg.Main)
	}

	mf := &OpenClawPluginManifest{
		ID:          id,
		Name:        strings.TrimSpace(pkg.Name),
		Version:     strings.TrimSpace(pkg.Version),
		Description: strings.TrimSpace(pkg.Description),
		Kind:        pkg.OpenClaw.Kind,
		Entry:       entry,
	}
	if mf.Name == "" {
		mf.Name = mf.ID
	}
	return mf, nil
}
