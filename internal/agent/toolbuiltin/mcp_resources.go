package toolbuiltin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"metiq/internal/agent"
	mcppkg "metiq/internal/mcp"
)

// MCPResourceToolOpts configures built-in tools for external MCP resources.
type MCPResourceToolOpts struct {
	Manager func() *mcppkg.Manager
}

var MCPResourcesListDef = agent.ToolDefinition{
	Name:        "mcp_resources_list",
	Description: "List resources exposed by connected external MCP servers. If server is omitted, aggregates resources across all connected resource-capable servers.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server": {Type: "string", Description: "Optional MCP server name to restrict the listing to one server."},
		},
	},
}

var MCPResourcesReadDef = agent.ToolDefinition{
	Name:        "mcp_resources_read",
	Description: "Read a specific resource from a connected external MCP server by server name and resource URI.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"server": {Type: "string", Description: "MCP server name."},
			"uri":    {Type: "string", Description: "Resource URI to read."},
		},
		Required: []string{"server", "uri"},
	},
}

type externalMCPResource struct {
	Server      string `json:"server"`
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	Description string `json:"description,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type externalMCPResourceContent struct {
	URI        string `json:"uri"`
	MIMEType   string `json:"mimeType,omitempty"`
	Text       string `json:"text,omitempty"`
	BlobBase64 string `json:"blobBase64,omitempty"`
	BlobBytes  int    `json:"blobBytes,omitempty"`
}

type externalMCPResourceError struct {
	Server string `json:"server"`
	Error  string `json:"error"`
}

// RegisterMCPResourceTools registers stable built-in tools for querying
// resources exposed by connected external MCP servers.
func RegisterMCPResourceTools(tools *agent.ToolRegistry, opts MCPResourceToolOpts) {
	resolveManager := func() (*mcppkg.Manager, error) {
		if opts.Manager == nil {
			return nil, fmt.Errorf("external MCP manager unavailable")
		}
		mgr := opts.Manager()
		if mgr == nil {
			return nil, fmt.Errorf("external MCP manager unavailable")
		}
		return mgr, nil
	}

	tools.RegisterWithDef("mcp_resources_list", func(ctx context.Context, args map[string]any) (string, error) {
		mgr, err := resolveManager()
		if err != nil {
			return "", fmt.Errorf("mcp_resources_list: %w", err)
		}
		serverName := strings.TrimSpace(agent.ArgString(args, "server"))
		resources := make([]externalMCPResource, 0)
		errorsOut := make([]externalMCPResourceError, 0)

		appendResources := func(server string, result *sdkmcp.ListResourcesResult) {
			if result == nil {
				return
			}
			for _, resource := range result.Resources {
				if resource == nil {
					continue
				}
				resources = append(resources, externalMCPResource{
					Server:      server,
					URI:         strings.TrimSpace(resource.URI),
					Name:        strings.TrimSpace(resource.Name),
					Title:       strings.TrimSpace(resource.Title),
					MIMEType:    strings.TrimSpace(resource.MIMEType),
					Description: strings.TrimSpace(resource.Description),
					Size:        resource.Size,
				})
			}
		}

		if serverName != "" {
			result, err := mgr.ListResources(ctx, serverName)
			if err != nil {
				return "", fmt.Errorf("mcp_resources_list: %w", err)
			}
			appendResources(serverName, result)
		} else {
			states := mgr.ListServerStates()
			sort.SliceStable(states, func(i, j int) bool { return states[i].Name < states[j].Name })
			for _, state := range states {
				if state.State != mcppkg.ConnectionStateConnected || !state.Capabilities.Resources {
					continue
				}
				result, err := mgr.ListResources(ctx, state.Name)
				if err != nil {
					errorsOut = append(errorsOut, externalMCPResourceError{Server: state.Name, Error: err.Error()})
					continue
				}
				appendResources(state.Name, result)
			}
		}

		out := map[string]any{
			"resources": resources,
			"count":     len(resources),
		}
		if serverName != "" {
			out["server"] = serverName
		}
		if len(errorsOut) > 0 {
			out["errors"] = errorsOut
		}
		payload, _ := json.Marshal(out)
		return string(payload), nil
	}, MCPResourcesListDef)

	tools.RegisterWithDef("mcp_resources_read", func(ctx context.Context, args map[string]any) (string, error) {
		mgr, err := resolveManager()
		if err != nil {
			return "", fmt.Errorf("mcp_resources_read: %w", err)
		}
		serverName := strings.TrimSpace(agent.ArgString(args, "server"))
		if serverName == "" {
			return "", fmt.Errorf("mcp_resources_read: server is required")
		}
		uri := strings.TrimSpace(agent.ArgString(args, "uri"))
		if uri == "" {
			return "", fmt.Errorf("mcp_resources_read: uri is required")
		}
		result, err := mgr.ReadResource(ctx, serverName, uri)
		if err != nil {
			return "", fmt.Errorf("mcp_resources_read: %w", err)
		}
		contents := make([]externalMCPResourceContent, 0)
		if result != nil {
			for _, content := range result.Contents {
				if content == nil {
					continue
				}
				entry := externalMCPResourceContent{
					URI:      strings.TrimSpace(content.URI),
					MIMEType: strings.TrimSpace(content.MIMEType),
					Text:     content.Text,
				}
				if len(content.Blob) > 0 {
					entry.BlobBytes = len(content.Blob)
					entry.BlobBase64 = base64.StdEncoding.EncodeToString(content.Blob)
				}
				contents = append(contents, entry)
			}
		}
		payload, _ := json.Marshal(map[string]any{
			"server":   serverName,
			"uri":      uri,
			"contents": contents,
			"count":    len(contents),
		})
		return string(payload), nil
	}, MCPResourcesReadDef)
}
