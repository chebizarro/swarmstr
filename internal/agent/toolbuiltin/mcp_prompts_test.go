package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"metiq/internal/agent"
	mcppkg "metiq/internal/mcp"
)

func TestRegisterMCPPromptToolsIncludesDefinitions(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterMCPPromptTools(tools, MCPPromptToolOpts{})

	defs := tools.Definitions()
	seen := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		seen[def.Name] = struct{}{}
	}
	for _, name := range []string{"mcp_prompts_list", "mcp_prompts_get"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing tool definition for %q", name)
		}
	}
}

func TestParseMCPPromptArgumentsRejectsUnsupportedType(t *testing.T) {
	_, err := parseMCPPromptArguments([]string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "expected object or JSON string") {
		t.Fatalf("err = %v, want invalid-arguments failure", err)
	}
}

func TestMCPPromptsListAggregatesPartialFailures(t *testing.T) {
	mgr := mcppkg.NewManager()
	mgr.SetConnectFunc(func(_ context.Context, name string, _ mcppkg.ServerConfig) (*mcppkg.ServerConnection, error) {
		switch name {
		case "alpha":
			return &mcppkg.ServerConnection{
				Name:         name,
				Capabilities: mcppkg.CapabilitySnapshot{Prompts: true},
				ListPromptsFunc: func(context.Context, *sdkmcp.ListPromptsParams) (*sdkmcp.ListPromptsResult, error) {
					return &sdkmcp.ListPromptsResult{Prompts: []*sdkmcp.Prompt{{
						Name:        "review",
						Title:       "Review",
						Description: "Review prompt",
						Arguments:   []*sdkmcp.PromptArgument{{Name: "topic", Required: true}},
					}}}, nil
				},
			}, nil
		case "beta":
			return &mcppkg.ServerConnection{
				Name:         name,
				Capabilities: mcppkg.CapabilitySnapshot{Prompts: true},
				ListPromptsFunc: func(context.Context, *sdkmcp.ListPromptsParams) (*sdkmcp.ListPromptsResult, error) {
					return nil, context.DeadlineExceeded
				},
			}, nil
		default:
			return &mcppkg.ServerConnection{Name: name}, nil
		}
	})
	if err := mgr.ApplyConfig(context.Background(), mcppkg.Config{
		Enabled: true,
		Servers: map[string]mcppkg.ResolvedServerConfig{
			"alpha": {Name: "alpha", Signature: "sig-alpha", ServerConfig: mcppkg.ServerConfig{Enabled: true, Command: "alpha"}},
			"beta":  {Name: "beta", Signature: "sig-beta", ServerConfig: mcppkg.ServerConfig{Enabled: true, Command: "beta"}},
		},
	}); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}

	tools := agent.NewToolRegistry()
	RegisterMCPPromptTools(tools, MCPPromptToolOpts{Manager: func() *mcppkg.Manager { return mgr }})
	result, err := tools.Execute(context.Background(), agent.ToolCall{Name: "mcp_prompts_list"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	var payload struct {
		Prompts []struct {
			Server    string `json:"server"`
			Name      string `json:"name"`
			Title     string `json:"title"`
			Arguments []struct {
				Name     string `json:"name"`
				Required bool   `json:"required"`
			} `json:"arguments"`
		} `json:"prompts"`
		Errors []struct {
			Server string `json:"server"`
			Error  string `json:"error"`
		} `json:"errors"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload.Count != 1 || len(payload.Prompts) != 1 {
		t.Fatalf("unexpected prompt payload: %+v", payload)
	}
	if payload.Prompts[0].Server != "alpha" || payload.Prompts[0].Name != "review" || len(payload.Prompts[0].Arguments) != 1 {
		t.Fatalf("unexpected aggregated prompt: %+v", payload.Prompts[0])
	}
	if len(payload.Errors) != 1 || payload.Errors[0].Server != "beta" {
		t.Fatalf("expected partial failure for beta, got %+v", payload.Errors)
	}
}

func TestMCPPromptsGetPassesArgumentsAndReturnsMessages(t *testing.T) {
	mgr := mcppkg.NewManager()
	mgr.SetConnectFunc(func(_ context.Context, name string, _ mcppkg.ServerConfig) (*mcppkg.ServerConnection, error) {
		return &mcppkg.ServerConnection{
			Name:         name,
			Capabilities: mcppkg.CapabilitySnapshot{Prompts: true},
			GetPromptFunc: func(_ context.Context, params *sdkmcp.GetPromptParams) (*sdkmcp.GetPromptResult, error) {
				if params == nil || params.Name != "review" {
					t.Fatalf("unexpected get prompt params: %#v", params)
				}
				if params.Arguments["topic"] != "mcp" || params.Arguments["count"] != "3" {
					t.Fatalf("unexpected arguments: %#v", params.Arguments)
				}
				return &sdkmcp.GetPromptResult{
					Description: "Review prompt",
					Messages: []*sdkmcp.PromptMessage{{
						Role:    sdkmcp.Role("user"),
						Content: &sdkmcp.TextContent{Text: "Review MCP parity"},
					}},
				}, nil
			},
		}, nil
	})
	if err := mgr.ApplyConfig(context.Background(), mcppkg.Config{
		Enabled: true,
		Servers: map[string]mcppkg.ResolvedServerConfig{
			"alpha": {Name: "alpha", Signature: "sig-alpha", ServerConfig: mcppkg.ServerConfig{Enabled: true, Command: "alpha"}},
		},
	}); err != nil {
		t.Fatalf("ApplyConfig error: %v", err)
	}

	tools := agent.NewToolRegistry()
	RegisterMCPPromptTools(tools, MCPPromptToolOpts{Manager: func() *mcppkg.Manager { return mgr }})
	result, err := tools.Execute(context.Background(), agent.ToolCall{
		Name: "mcp_prompts_get",
		Args: map[string]any{
			"server": "alpha",
			"name":   "review",
			"arguments": map[string]any{
				"topic": "mcp",
				"count": 3,
			},
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	var payload struct {
		Server      string            `json:"server"`
		Name        string            `json:"name"`
		Arguments   map[string]string `json:"arguments"`
		Description string            `json:"description"`
		Messages    []map[string]any  `json:"messages"`
		Count       int               `json:"count"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload.Server != "alpha" || payload.Name != "review" || payload.Description != "Review prompt" {
		t.Fatalf("unexpected get payload: %+v", payload)
	}
	if payload.Arguments["topic"] != "mcp" || payload.Arguments["count"] != "3" {
		t.Fatalf("arguments not preserved: %+v", payload.Arguments)
	}
	if payload.Count != 1 || len(payload.Messages) != 1 || payload.Messages[0]["role"] != "user" {
		t.Fatalf("unexpected prompt messages: %+v", payload.Messages)
	}
}
