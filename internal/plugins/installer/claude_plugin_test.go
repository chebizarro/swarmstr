package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenClawPackageContractValidation(t *testing.T) {
	dir := t.TempDir()
	pkg := `{
		"name":"pkg-plugin",
		"version":"1.2.3",
		"openclaw":{
			"compat":{"pluginApi":"^1.0.0","minGatewayVersion":"0.9.0"},
			"build":{"openclawVersion":"1.5.0","pluginSdkVersion":"2.0.0"}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	compat, err := validateOpenClawPackageContract(dir)
	if err != nil {
		t.Fatalf("valid contract rejected: %v", err)
	}
	if compat.PluginAPIRange != "^1.0.0" || compat.BuiltWithOpenClawVersion != "1.5.0" || compat.PluginSDKVersion != "2.0.0" || compat.MinGatewayVersion != "0.9.0" {
		t.Fatalf("unexpected compatibility: %+v", compat)
	}
}

func TestOpenClawPackageContractRejectsUnsupportedAPIRange(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"name":"future","version":"1.0.0","openclaw":{"compat":{"pluginApi":"^2.0.0"},"build":{"openclawVersion":"2.0.0"}}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateOpenClawPackageContract(dir)
	if err == nil || !strings.Contains(err.Error(), "does not include host API") {
		t.Fatalf("expected API negotiation failure, got %v", err)
	}
}

func TestOpenClawPackageContractValidationMissingAndInvalid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"pkg","version":"1.0.0","openclaw":{"build":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := validateOpenClawPackageContract(dir)
	if err == nil {
		t.Fatal("expected missing fields error")
	}
	if !strings.Contains(err.Error(), "openclaw.compat.pluginApi") || !strings.Contains(err.Error(), "openclaw.build.openclawVersion") {
		t.Fatalf("error did not mention missing required fields: %v", err)
	}

	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "package.json"), []byte(`{"openclaw":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateOpenClawPackageContract(bad); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestClaudePluginParsingMarketplaceDiscoveryAndNormalize(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	pluginJSON := `{"name":"hookify","version":"0.1.0","description":"hooks","author":{"name":"Daisy","email":"daisy@example.com"}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(pluginJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{"commands", "agents", "skills", "hooks"} {
		if err := os.MkdirAll(filepath.Join(dir, component), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hooksJSON := `{"description":"test","hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"python3 ${CLAUDE_PLUGIN_ROOT}/hooks/pre.py","timeout":10}]}],"PostToolUse":[{"hooks":[{"type":"command","command":"python3 ${CLAUDE_PLUGIN_ROOT}/hooks/post.py"}]}],"Stop":[{"hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/hooks/stop.sh"}]}],"UserPromptSubmit":[{"hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/hooks/prompt.sh"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "hooks", "hooks.json"), []byte(hooksJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	plugin, err := LoadClaudePlugin(dir)
	if err != nil {
		t.Fatalf("LoadClaudePlugin failed: %v", err)
	}
	if plugin.Metadata.Name != "hookify" || !plugin.Components.Commands || !plugin.Components.Agents || !plugin.Components.Skills || !plugin.Components.Hooks {
		t.Fatalf("unexpected Claude plugin: %+v", plugin)
	}
	if got, ok := ClaudeHookEvent("PreToolUse"); !ok || got != "before_tool_call" {
		t.Fatalf("PreToolUse mapped to %q ok=%v", got, ok)
	}
	mf, err := NormalizeClaudePlugin(dir)
	if err != nil {
		t.Fatalf("NormalizeClaudePlugin failed: %v", err)
	}
	if mf.ID != "hookify" || len(mf.Capabilities.Hooks) != 4 || len(mf.Capabilities.Skills) != 2 || len(mf.Capabilities.Tools) != 1 {
		t.Fatalf("unexpected normalized manifest: %+v", mf)
	}

	marketplacePath := filepath.Join(dir, ".claude-plugin", "marketplace.json")
	marketplaceJSON := `{"name":"claude-code-plugins","owner":{"name":"Anthropic","email":"support@example.com"},"plugins":[{"name":"hookify","source":"./plugins/hookify","category":"productivity","version":"0.1.0","author":{"name":"Daisy"}}]}`
	if err := os.WriteFile(marketplacePath, []byte(marketplaceJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	marketplace, err := LoadClaudeMarketplace(marketplacePath)
	if err != nil {
		t.Fatalf("LoadClaudeMarketplace failed: %v", err)
	}
	if marketplace.Name != "claude-code-plugins" || marketplace.Owner.Name != "Anthropic" || len(marketplace.Plugins) != 1 || marketplace.Plugins[0].Source != "./plugins/hookify" {
		t.Fatalf("unexpected marketplace: %+v", marketplace)
	}
}
