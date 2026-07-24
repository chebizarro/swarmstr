package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pluginmanifest "metiq/internal/plugins/manifest"
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

// OpenClawCompatibility is normalized compatibility metadata from the external
// OpenClaw package contract.
type OpenClawCompatibility struct {
	PluginAPIRange           string `json:"pluginApiRange,omitempty"`
	BuiltWithOpenClawVersion string `json:"builtWithOpenClawVersion,omitempty"`
	PluginSDKVersion         string `json:"pluginSdkVersion,omitempty"`
	MinGatewayVersion        string `json:"minGatewayVersion,omitempty"`
}

// OpenClawValidationIssue describes a package-contract validation problem.
type OpenClawValidationIssue struct {
	FieldPath string `json:"fieldPath"`
	Message   string `json:"message"`
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

// ValidateOpenClawPackageContract validates and negotiates an external package
// against the plugin API implemented by this host.
func ValidateOpenClawPackageContract(pluginPath string) (OpenClawCompatibility, error) {
	pluginPath = strings.TrimSpace(pluginPath)
	if info, err := os.Stat(pluginPath); err == nil && !info.IsDir() {
		pluginPath = filepath.Dir(pluginPath)
	}
	pkgPath := filepath.Join(pluginPath, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return OpenClawCompatibility{}, nil
		}
		return OpenClawCompatibility{}, fmt.Errorf("read package.json for OpenClaw contract: %w", err)
	}
	var pkg map[string]any
	if err := json.Unmarshal(data, &pkg); err != nil {
		return OpenClawCompatibility{}, fmt.Errorf("parse package.json for OpenClaw contract: %w", err)
	}
	if _, ok := pkg["openclaw"]; !ok {
		return OpenClawCompatibility{}, nil
	}
	compat, issues := normalizeOpenClawCompatibility(pkg)
	if len(issues) > 0 {
		parts := make([]string, len(issues))
		for i, issue := range issues {
			parts[i] = issue.Message
		}
		return compat, fmt.Errorf("incompatible OpenClaw plugin package: %s", strings.Join(parts, "; "))
	}
	if err := compat.CheckCompatibility(""); err != nil {
		return compat, fmt.Errorf("incompatible OpenClaw plugin package: %w", err)
	}
	return compat, nil
}

// CheckCompatibility negotiates package metadata against this host. hostVersion
// may be empty when only the independently-versioned plugin API is known.
func (c OpenClawCompatibility) CheckCompatibility(hostVersion string) error {
	apiRange := strings.TrimSpace(c.PluginAPIRange)
	if apiRange != "" {
		ok, err := pluginmanifest.CheckVersionRange(pluginmanifest.HostPluginAPIVersion, apiRange)
		if err != nil {
			return fmt.Errorf("invalid openclaw.compat.pluginApi range %q: %w", apiRange, err)
		}
		if !ok {
			return fmt.Errorf("openclaw.compat.pluginApi %q does not include host API %s", apiRange, pluginmanifest.HostPluginAPIVersion)
		}
	}
	minHost := strings.TrimSpace(c.MinGatewayVersion)
	if minHost != "" && strings.TrimSpace(hostVersion) != "" {
		constraint := minHost
		if !strings.ContainsAny(constraint, "<>=~^*xX| ") {
			constraint = ">=" + constraint
		}
		ok, err := pluginmanifest.CheckVersionRange(hostVersion, constraint)
		if err != nil {
			return fmt.Errorf("invalid minimum host version %q: %w", minHost, err)
		}
		if !ok {
			return fmt.Errorf("plugin requires host %q but host is %s", minHost, hostVersion)
		}
	}
	return nil
}

func validateOpenClawPackageContract(pluginPath string) (OpenClawCompatibility, error) {
	return ValidateOpenClawPackageContract(pluginPath)
}

func normalizeOpenClawCompatibility(pkg map[string]any) (OpenClawCompatibility, []OpenClawValidationIssue) {
	openclaw := mapValue(pkg["openclaw"])
	compatBlock := mapValue(openclaw["compat"])
	build := mapValue(openclaw["build"])
	install := mapValue(openclaw["install"])

	out := OpenClawCompatibility{
		PluginAPIRange:           normalizedString(compatBlock["pluginApi"]),
		BuiltWithOpenClawVersion: normalizedString(build["openclawVersion"]),
		PluginSDKVersion:         normalizedString(build["pluginSdkVersion"]),
		MinGatewayVersion:        normalizedString(compatBlock["minGatewayVersion"]),
	}
	if out.MinGatewayVersion == "" {
		out.MinGatewayVersion = normalizedString(install["minHostVersion"])
	}
	if out.BuiltWithOpenClawVersion == "" {
		out.BuiltWithOpenClawVersion = normalizedString(pkg["version"])
	}

	var issues []OpenClawValidationIssue
	if out.PluginAPIRange == "" {
		issues = append(issues, OpenClawValidationIssue{FieldPath: "openclaw.compat.pluginApi", Message: "openclaw.compat.pluginApi is required for external code plugin packages"})
	}
	if normalizedString(build["openclawVersion"]) == "" {
		issues = append(issues, OpenClawValidationIssue{FieldPath: "openclaw.build.openclawVersion", Message: "openclaw.build.openclawVersion is required for external code plugin packages"})
	}
	return out, issues
}

func mapValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func normalizedString(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func authorInfoFromClaude(author ClaudeAuthor) *pluginmanifest.AuthorInfo {
	if strings.TrimSpace(author.Name) == "" && strings.TrimSpace(author.Email) == "" {
		return nil
	}
	return &pluginmanifest.AuthorInfo{Name: strings.TrimSpace(author.Name), Email: strings.TrimSpace(author.Email)}
}
