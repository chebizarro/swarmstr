package toolbuiltin

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/agent"
)

func TestRegisterContextVMToolsIncludesExpandedTypedSurface(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterContextVMTools(tools, ContextVMToolOpts{})

	defs := tools.Definitions()
	seen := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		seen[def.Name] = struct{}{}
	}
	for _, name := range []string{
		"contextvm_discover",
		"contextvm_tools_list",
		"contextvm_call",
		"contextvm_resources_list",
		"contextvm_resources_read",
		"contextvm_prompts_list",
		"contextvm_prompts_get",
		"contextvm_raw",
	} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing tool definition for %q", name)
		}
	}
}

func TestContextVMResourcesReadRejectsBlankURI(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterContextVMTools(tools, ContextVMToolOpts{})

	_, err := tools.Execute(context.Background(), agent.ToolCall{
		Name: "contextvm_resources_read",
		Args: map[string]any{
			"server_pubkey": "peer-pubkey",
			"uri":           "   ",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "contextvm_resources_read: uri is required") {
		t.Fatalf("err = %v, want blank-uri failure", err)
	}
}

func TestContextVMPromptsGetRejectsInvalidArgumentsJSON(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterContextVMTools(tools, ContextVMToolOpts{})

	_, err := tools.Execute(context.Background(), agent.ToolCall{
		Name: "contextvm_prompts_get",
		Args: map[string]any{
			"server_pubkey": "peer-pubkey",
			"name":          "review",
			"arguments":     "{not-json}",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "contextvm_prompts_get: parse arguments JSON") {
		t.Fatalf("err = %v, want invalid-arguments failure", err)
	}
}
