package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pluginmanifest "metiq/internal/plugins/manifest"
)

const ClaudePluginMetadataDir = ".claude-plugin"

type ClaudeAuthor struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type ClaudePluginJSON struct {
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	Description string       `json:"description,omitempty"`
	Author      ClaudeAuthor `json:"author,omitempty"`
	Homepage    string       `json:"homepage,omitempty"`
}

type ClaudeMarketplace struct {
	Name        string                    `json:"name"`
	Version     string                    `json:"version,omitempty"`
	Description string                    `json:"description,omitempty"`
	Owner       ClaudeAuthor              `json:"owner,omitempty"`
	Plugins     []ClaudeMarketplacePlugin `json:"plugins,omitempty"`
}

type ClaudeMarketplacePlugin struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Source      string       `json:"source"`
	Category    string       `json:"category,omitempty"`
	Version     string       `json:"version,omitempty"`
	Author      ClaudeAuthor `json:"author,omitempty"`
}

type ClaudeComponents struct {
	Commands bool `json:"commands,omitempty"`
	Agents   bool `json:"agents,omitempty"`
	Skills   bool `json:"skills,omitempty"`
	Hooks    bool `json:"hooks,omitempty"`
}

type ClaudePlugin struct {
	Metadata   ClaudePluginJSON `json:"metadata"`
	Components ClaudeComponents `json:"components"`
	Hooks      *ClaudeHooksFile `json:"hooks,omitempty"`
}

type ClaudeHooksFile struct {
	Description string                         `json:"description,omitempty"`
	Hooks       map[string][]ClaudeHookMatcher `json:"hooks"`
}

type ClaudeHookMatcher struct {
	Matcher string              `json:"matcher,omitempty"`
	Hooks   []ClaudeHookCommand `json:"hooks"`
}

type ClaudeHookCommand struct {
	Type           string `json:"type"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout,omitempty"`
	If             string `json:"if,omitempty"`
	AsyncRewake    bool   `json:"asyncRewake,omitempty"`
	RewakeMessage  string `json:"rewakeMessage,omitempty"`
	RewakeSummary  string `json:"rewakeSummary,omitempty"`
}

func LoadClaudePlugin(pluginPath string) (*ClaudePlugin, error) {
	pluginPath = strings.TrimSpace(pluginPath)
	if pluginPath == "" {
		return nil, fmt.Errorf("pluginPath is required")
	}
	meta, err := LoadClaudePluginJSON(pluginPath)
	if err != nil {
		return nil, err
	}
	components, err := DiscoverClaudeComponents(pluginPath)
	if err != nil {
		return nil, err
	}
	out := &ClaudePlugin{Metadata: *meta, Components: components}
	if components.Hooks {
		hooks, err := LoadClaudeHooks(pluginPath)
		if err != nil {
			return nil, err
		}
		out.Hooks = hooks
	}
	return out, nil
}

func LoadClaudePluginJSON(pluginPath string) (*ClaudePluginJSON, error) {
	data, err := os.ReadFile(filepath.Join(pluginPath, ClaudePluginMetadataDir, "plugin.json"))
	if err != nil {
		return nil, fmt.Errorf("read Claude plugin.json: %w", err)
	}
	var meta ClaudePluginJSON
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse Claude plugin.json: %w", err)
	}
	if strings.TrimSpace(meta.Name) == "" {
		return nil, fmt.Errorf("Claude plugin.json missing name")
	}
	if strings.TrimSpace(meta.Version) == "" {
		return nil, fmt.Errorf("Claude plugin.json missing version")
	}
	return &meta, nil
}

func LoadClaudeMarketplace(path string) (*ClaudeMarketplace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Claude marketplace: %w", err)
	}
	var marketplace ClaudeMarketplace
	if err := json.Unmarshal(data, &marketplace); err != nil {
		return nil, fmt.Errorf("parse Claude marketplace: %w", err)
	}
	if strings.TrimSpace(marketplace.Name) == "" {
		return nil, fmt.Errorf("Claude marketplace missing name")
	}
	if strings.TrimSpace(marketplace.Owner.Name) == "" {
		return nil, fmt.Errorf("Claude marketplace missing owner.name")
	}
	for i, plugin := range marketplace.Plugins {
		if strings.TrimSpace(plugin.Name) == "" {
			return nil, fmt.Errorf("Claude marketplace plugins[%d] missing name", i)
		}
		if strings.TrimSpace(plugin.Source) == "" {
			return nil, fmt.Errorf("Claude marketplace plugins[%d] missing source", i)
		}
	}
	return &marketplace, nil
}

func DiscoverClaudeComponents(pluginPath string) (ClaudeComponents, error) {
	var out ClaudeComponents
	for _, item := range []struct {
		name string
		set  func()
	}{
		{"commands", func() { out.Commands = true }},
		{"agents", func() { out.Agents = true }},
		{"skills", func() { out.Skills = true }},
		{"hooks", func() { out.Hooks = true }},
	} {
		info, err := os.Stat(filepath.Join(pluginPath, item.name))
		if err == nil && info.IsDir() {
			item.set()
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return out, fmt.Errorf("stat Claude component %s: %w", item.name, err)
		}
	}
	return out, nil
}

func LoadClaudeHooks(pluginPath string) (*ClaudeHooksFile, error) {
	data, err := os.ReadFile(filepath.Join(pluginPath, "hooks", "hooks.json"))
	if err != nil {
		return nil, fmt.Errorf("read Claude hooks.json: %w", err)
	}
	var hooks ClaudeHooksFile
	if err := json.Unmarshal(data, &hooks); err != nil {
		return nil, fmt.Errorf("parse Claude hooks.json: %w", err)
	}
	return &hooks, nil
}

func ClaudeHookEvent(event string) (string, bool) {
	switch strings.TrimSpace(event) {
	case "PreToolUse":
		return "before_tool_call", true
	case "PostToolUse":
		return "after_tool_call", true
	case "Stop":
		return "agent_end", true
	case "UserPromptSubmit":
		return "agent_turn_prepare", true
	case "SessionStart":
		return "session_start", true
	default:
		return "", false
	}
}

func NormalizeClaudePlugin(pluginPath string) (*pluginmanifest.Manifest, error) {
	plugin, err := LoadClaudePlugin(pluginPath)
	if err != nil {
		return nil, err
	}
	m := &pluginmanifest.Manifest{
		SchemaVersion: pluginmanifest.SchemaVersion,
		ID:            sanitizePluginID(plugin.Metadata.Name),
		Name:          plugin.Metadata.Name,
		Version:       plugin.Metadata.Version,
		Description:   plugin.Metadata.Description,
		Author:        authorInfoFromClaude(plugin.Metadata.Author),
		Homepage:      plugin.Metadata.Homepage,
		Runtime:       pluginmanifest.RuntimeNative,
		Compat: pluginmanifest.Compatibility{
			PluginAPI: "^" + pluginmanifest.HostPluginAPIVersion,
		},
		Build: pluginmanifest.BuildInfo{
			HostVersion:      "0.0.0-dev",
			PluginSDKVersion: pluginmanifest.HostPluginSDKVersion,
		},
		Trust: "external",
	}
	if plugin.Components.Commands {
		m.Capabilities.Tools = append(m.Capabilities.Tools, pluginmanifest.ToolCapability{Name: "claude.commands", Description: "Claude Code slash commands"})
	}
	if plugin.Components.Agents {
		m.Capabilities.Skills = append(m.Capabilities.Skills, pluginmanifest.SkillCapability{ID: "claude.agents", Name: "Claude Code agents"})
	}
	if plugin.Components.Skills {
		m.Capabilities.Skills = append(m.Capabilities.Skills, pluginmanifest.SkillCapability{ID: "claude.skills", Name: "Claude Code skills"})
	}
	if plugin.Hooks != nil {
		seen := map[string]bool{}
		for event := range plugin.Hooks.Hooks {
			if mapped, ok := ClaudeHookEvent(event); ok && !seen[mapped] {
				seen[mapped] = true
				m.Capabilities.Hooks = append(m.Capabilities.Hooks, pluginmanifest.HookCapability{Event: mapped, Description: "Claude Code hook: " + event})
			}
		}
		sort.Slice(m.Capabilities.Hooks, func(i, j int) bool { return m.Capabilities.Hooks[i].Event < m.Capabilities.Hooks[j].Event })
	}
	if err := pluginmanifest.Validate(m); err != nil {
		return nil, err
	}
	return m, nil
}

func sanitizePluginID(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "claude-plugin"
	}
	return out
}
