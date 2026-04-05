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

func TestRegisterMCPResourceToolsIncludesDefinitions(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterMCPResourceTools(tools, MCPResourceToolOpts{})

	defs := tools.Definitions()
	seen := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		seen[def.Name] = struct{}{}
	}
	for _, name := range []string{"mcp_resources_list", "mcp_resources_read"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing tool definition for %q", name)
		}
	}
}

func TestMCPResourcesReadRejectsBlankURI(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterMCPResourceTools(tools, MCPResourceToolOpts{Manager: func() *mcppkg.Manager { return mcppkg.NewManager() }})

	_, err := tools.Execute(context.Background(), agent.ToolCall{
		Name: "mcp_resources_read",
		Args: map[string]any{
			"server": "demo",
			"uri":    "   ",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "mcp_resources_read: uri is required") {
		t.Fatalf("err = %v, want blank-uri failure", err)
	}
}

func TestMCPResourcesListAggregatesPartialFailures(t *testing.T) {
	mgr := mcppkg.NewManager()
	mgr.SetConnectFunc(func(_ context.Context, name string, _ mcppkg.ServerConfig) (*mcppkg.ServerConnection, error) {
		switch name {
		case "alpha":
			return &mcppkg.ServerConnection{
				Name:         name,
				Capabilities: mcppkg.CapabilitySnapshot{Resources: true},
				ListResourcesFunc: func(context.Context, *sdkmcp.ListResourcesParams) (*sdkmcp.ListResourcesResult, error) {
					return &sdkmcp.ListResourcesResult{Resources: []*sdkmcp.Resource{{URI: "file:///alpha.txt", Name: "alpha", MIMEType: "text/plain", Description: "alpha resource"}}}, nil
				},
			}, nil
		case "beta":
			return &mcppkg.ServerConnection{
				Name:         name,
				Capabilities: mcppkg.CapabilitySnapshot{Resources: true},
				ListResourcesFunc: func(context.Context, *sdkmcp.ListResourcesParams) (*sdkmcp.ListResourcesResult, error) {
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
	RegisterMCPResourceTools(tools, MCPResourceToolOpts{Manager: func() *mcppkg.Manager { return mgr }})
	result, err := tools.Execute(context.Background(), agent.ToolCall{Name: "mcp_resources_list"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	var payload struct {
		Resources []struct {
			Server string `json:"server"`
			URI    string `json:"uri"`
			Name   string `json:"name"`
		} `json:"resources"`
		Errors []struct {
			Server string `json:"server"`
			Error  string `json:"error"`
		} `json:"errors"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if payload.Count != 1 || len(payload.Resources) != 1 {
		t.Fatalf("unexpected resource payload: %+v", payload)
	}
	if payload.Resources[0].Server != "alpha" || payload.Resources[0].URI != "file:///alpha.txt" {
		t.Fatalf("unexpected aggregated resource: %+v", payload.Resources[0])
	}
	if len(payload.Errors) != 1 || payload.Errors[0].Server != "beta" {
		t.Fatalf("expected partial failure for beta, got %+v", payload.Errors)
	}
}
