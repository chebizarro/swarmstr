package mcp

import (
	"testing"
)

func TestMCPToolDefinitions(t *testing.T) {
	defs := MCPToolDefinitions()

	expectedTools := []string{
		"mcp_list_servers",
		"mcp_list_resources",
		"mcp_read_resource",
		"mcp_list_tools",
		"mcp_list_prompts",
		"mcp_get_prompt",
	}

	if len(defs) != len(expectedTools) {
		t.Errorf("expected %d tools, got %d", len(expectedTools), len(defs))
	}

	toolNames := make(map[string]bool)
	for _, def := range defs {
		toolNames[def.Name] = true
		if def.Description == "" {
			t.Errorf("tool %s has no description", def.Name)
		}
		if def.Parameters == nil {
			t.Errorf("tool %s has no parameters", def.Name)
		}
	}

	for _, expected := range expectedTools {
		if !toolNames[expected] {
			t.Errorf("missing expected tool: %s", expected)
		}
	}
}

func TestIsMCPTool(t *testing.T) {
	cases := []struct {
		name     string
		expected bool
	}{
		{"mcp_list_servers", true},
		{"mcp_list_resources", true},
		{"mcp_read_resource", true},
		{"mcp_list_tools", true},
		{"mcp_list_prompts", true},
		{"mcp_get_prompt", true},
		{"list_servers", false},
		{"read_file", false},
		{"", false},
		{"MCP_LIST_SERVERS", false}, // case sensitive
	}

	for _, tc := range cases {
		result := IsMCPTool(tc.name)
		if result != tc.expected {
			t.Errorf("IsMCPTool(%q) = %v, want %v", tc.name, result, tc.expected)
		}
	}
}

func TestToolDefinitionStructure(t *testing.T) {
	defs := MCPToolDefinitions()

	for _, def := range defs {
		params, ok := def.Parameters["properties"].(map[string]any)
		if !ok {
			t.Errorf("tool %s: parameters.properties is not a map", def.Name)
			continue
		}

		required, hasRequired := def.Parameters["required"].([]string)

		// Check that required parameters exist in properties
		for _, req := range required {
			if _, exists := params[req]; !exists {
				t.Errorf("tool %s: required parameter %q not in properties", def.Name, req)
			}
		}

		// Check specific tools have required parameters
		switch def.Name {
		case "mcp_list_resources", "mcp_list_prompts":
			if !hasRequired || len(required) != 1 || required[0] != "server" {
				t.Errorf("tool %s should require 'server'", def.Name)
			}
		case "mcp_read_resource":
			if !hasRequired || len(required) != 2 {
				t.Errorf("tool %s should require 'server' and 'uri'", def.Name)
			}
		case "mcp_get_prompt":
			if !hasRequired || len(required) != 2 {
				t.Errorf("tool %s should require 'server' and 'name'", def.Name)
			}
		}
	}
}
