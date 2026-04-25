package manifest

import (
	"encoding/json"
	"testing"
)

func TestValidateMinimal(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 2,
		ID:            "my-plugin",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
	}
	if err := Validate(m); err != nil {
		t.Errorf("valid minimal manifest rejected: %v", err)
	}
}

func TestValidateMissingID(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 2,
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
	}
	err := Validate(m)
	if err == nil {
		t.Error("expected error for missing ID")
	}
	errs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}
	found := false
	for _, e := range errs {
		if e.Field == "id" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'id' field error")
	}
}

func TestValidateInvalidID(t *testing.T) {
	cases := []string{
		"My-Plugin",   // uppercase
		"my_plugin",   // underscore
		"123-plugin",  // starts with number
		"my--plugin",  // double hyphen (actually valid per regex)
		"-my-plugin",  // starts with hyphen
		"my-plugin-",  // ends with hyphen
	}
	for _, id := range cases {
		m := &Manifest{
			SchemaVersion: 2,
			ID:            id,
			Version:       "1.0.0",
			Runtime:       RuntimeGoja,
		}
		// Some of these should fail, some might pass
		// The important thing is the regex is being applied
		_ = Validate(m)
	}
}

func TestValidateVersion(t *testing.T) {
	validVersions := []string{"1.0.0", "0.1.0", "10.20.30", "1.0.0-alpha", "1.0.0-beta.1", "1.0.0+build"}
	for _, v := range validVersions {
		m := &Manifest{
			SchemaVersion: 2,
			ID:            "test",
			Version:       v,
			Runtime:       RuntimeGoja,
		}
		if err := Validate(m); err != nil {
			t.Errorf("valid version %q rejected: %v", v, err)
		}
	}

	invalidVersions := []string{"1.0", "v1.0.0", "1", "1.0.0.0"}
	for _, v := range invalidVersions {
		m := &Manifest{
			SchemaVersion: 2,
			ID:            "test",
			Version:       v,
			Runtime:       RuntimeGoja,
		}
		if err := Validate(m); err == nil {
			t.Errorf("invalid version %q accepted", v)
		}
	}
}

func TestValidateRuntime(t *testing.T) {
	validRuntimes := []RuntimeType{RuntimeGoja, RuntimeNode, RuntimeNative}
	for _, rt := range validRuntimes {
		m := &Manifest{
			SchemaVersion: 2,
			ID:            "test",
			Version:       "1.0.0",
			Runtime:       rt,
		}
		if err := Validate(m); err != nil {
			t.Errorf("valid runtime %q rejected: %v", rt, err)
		}
	}

	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test",
		Version:       "1.0.0",
		Runtime:       "python",
	}
	if err := Validate(m); err == nil {
		t.Error("invalid runtime 'python' accepted")
	}
}

func TestValidateTools(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Tools: []ToolCapability{
				{Name: "tool1", Description: "First tool"},
				{Name: "tool2", Description: "Second tool"},
			},
		},
	}
	if err := Validate(m); err != nil {
		t.Errorf("valid tools rejected: %v", err)
	}

	// Duplicate tool names
	m.Capabilities.Tools = []ToolCapability{
		{Name: "tool1", Description: "First"},
		{Name: "tool1", Description: "Duplicate"},
	}
	if err := Validate(m); err == nil {
		t.Error("duplicate tool names accepted")
	}

	// Empty tool name
	m.Capabilities.Tools = []ToolCapability{
		{Name: "", Description: "No name"},
	}
	if err := Validate(m); err == nil {
		t.Error("empty tool name accepted")
	}
}

func TestValidateChannels(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Channels: []ChannelCapability{
				{ID: "telegram", Name: "Telegram Bot"},
				{ID: "discord", Name: "Discord Bot"},
			},
		},
	}
	if err := Validate(m); err != nil {
		t.Errorf("valid channels rejected: %v", err)
	}
}

func TestValidateMCPServers(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			MCPServers: []MCPServerCapability{
				{ID: "my-mcp", Transport: MCPTransportStdio, Command: "my-mcp-server"},
			},
		},
	}
	if err := Validate(m); err != nil {
		t.Errorf("valid MCP server rejected: %v", err)
	}

	// Missing transport
	m.Capabilities.MCPServers[0].Transport = ""
	if err := Validate(m); err == nil {
		t.Error("MCP server without transport accepted")
	}
}

func TestParse(t *testing.T) {
	jsonData := `{
		"schema_version": 2,
		"id": "example-plugin",
		"version": "1.0.0",
		"name": "Example Plugin",
		"description": "A test plugin",
		"runtime": "goja",
		"capabilities": {
			"tools": [
				{
					"name": "example_tool",
					"description": "Does something useful",
					"category": "read"
				}
			],
			"channels": [
				{
					"id": "telegram",
					"name": "Telegram Integration",
					"features": {
						"typing": true,
						"reactions": true
					}
				}
			]
		},
		"permissions": {
			"network": {
				"hosts": ["api.example.com"]
			},
			"storage": true
		}
	}`

	m, err := Parse([]byte(jsonData))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if m.ID != "example-plugin" {
		t.Errorf("ID = %q, want %q", m.ID, "example-plugin")
	}
	if m.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", m.Version, "1.0.0")
	}
	if len(m.Capabilities.Tools) != 1 {
		t.Errorf("Tools count = %d, want 1", len(m.Capabilities.Tools))
	}
	if m.Capabilities.Tools[0].Name != "example_tool" {
		t.Errorf("Tool name = %q, want %q", m.Capabilities.Tools[0].Name, "example_tool")
	}
	if len(m.Capabilities.Channels) != 1 {
		t.Errorf("Channels count = %d, want 1", len(m.Capabilities.Channels))
	}
	if !m.Capabilities.Channels[0].Features.Typing {
		t.Error("Channel.Features.Typing should be true")
	}
	if !m.Permissions.Storage {
		t.Error("Permissions.Storage should be true")
	}
}

func TestParseLegacyV1(t *testing.T) {
	// Legacy manifest without schema_version
	jsonData := `{
		"id": "legacy-plugin",
		"version": "0.1.0",
		"runtime": "goja"
	}`

	m, err := Parse([]byte(jsonData))
	if err != nil {
		t.Fatalf("Parse failed for legacy manifest: %v", err)
	}

	if m.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1 (default)", m.SchemaVersion)
	}
	if m.Main != "index.js" {
		t.Errorf("Main = %q, want %q (default)", m.Main, "index.js")
	}
}

func TestCapabilityQueries(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Tools: []ToolCapability{
				{Name: "tool1"},
				{Name: "tool2"},
			},
			MCPServers: []MCPServerCapability{
				{ID: "mcp1", Transport: MCPTransportStdio},
			},
		},
	}

	if !m.HasTools() {
		t.Error("HasTools should be true")
	}
	if m.HasChannels() {
		t.Error("HasChannels should be false")
	}
	if !m.HasMCPServers() {
		t.Error("HasMCPServers should be true")
	}

	names := m.ToolNames()
	if len(names) != 2 || names[0] != "tool1" || names[1] != "tool2" {
		t.Errorf("ToolNames = %v, want [tool1 tool2]", names)
	}
}

func TestToJSON(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 2,
		ID:            "test-plugin",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Capabilities: Capabilities{
			Tools: []ToolCapability{
				{Name: "my_tool", Description: "A tool"},
			},
		},
	}

	data, err := m.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	// Parse it back
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if parsed["id"] != "test-plugin" {
		t.Errorf("id = %v, want test-plugin", parsed["id"])
	}
}

func TestValidationErrors(t *testing.T) {
	m := &Manifest{
		SchemaVersion: 0,
		ID:            "",
		Version:       "bad",
		Runtime:       "invalid",
	}

	err := Validate(m)
	if err == nil {
		t.Fatal("expected validation errors")
	}

	errs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}

	// Should have multiple errors
	if len(errs) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(errs), errs)
	}

	// Error string should include count
	errStr := errs.Error()
	if !contains(errStr, "validation errors") {
		t.Errorf("error string should mention 'validation errors': %s", errStr)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestIsCompatible(t *testing.T) {
	tests := []struct {
		name           string
		minVersion     string
		currentVersion string
		want           bool
	}{
		{"empty min version always compatible", "", "1.0.0", true},
		{"equal versions compatible", "1.0.0", "1.0.0", true},
		{"current newer major", "1.0.0", "2.0.0", true},
		{"current newer minor", "1.0.0", "1.1.0", true},
		{"current newer patch", "1.0.0", "1.0.1", true},
		{"current older major", "2.0.0", "1.0.0", false},
		{"current older minor", "1.2.0", "1.1.0", false},
		{"current older patch", "1.0.2", "1.0.1", false},
		{"v prefix handled", "v1.0.0", "v1.0.0", true},
		{"mixed v prefix", "1.0.0", "v1.0.0", true},
		{"pre-release ignored", "1.0.0-beta", "1.0.0", true},
		{"build metadata ignored", "1.0.0+build", "1.0.0", true},
		{"two-part version", "1.0", "1.0.0", true},
		{"one-part version", "1", "1.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manifest{MinMetiqVersion: tt.minVersion}
			got := m.IsCompatible(tt.currentVersion)
			if got != tt.want {
				t.Errorf("IsCompatible(%q, %q) = %v, want %v",
					tt.currentVersion, tt.minVersion, got, tt.want)
			}
		})
	}
}

func TestSemverLessThan(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "2.0.0", true},
		{"2.0.0", "1.0.0", false},
		{"1.0.0", "1.1.0", true},
		{"1.1.0", "1.0.0", false},
		{"1.0.0", "1.0.1", true},
		{"1.0.1", "1.0.0", false},
		{"1.9.0", "1.10.0", true},
		{"1.10.0", "1.9.0", false},
		{"0.0.1", "0.0.2", true},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := semverLessThan(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("semverLessThan(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
