package main

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"metiq/internal/agent"
	mcppkg "metiq/internal/mcp"
	"metiq/internal/store/state"
)

type mcpToolRegistryReconcileResult struct {
	Added     int
	Updated   int
	Removed   int
	Unchanged int
	Desired   int
	Conflicts int
}

func (r mcpToolRegistryReconcileResult) Changed() bool {
	return r.Added+r.Updated+r.Removed > 0
}

func reconcileMCPToolRegistry(reg *agent.ToolRegistry, mgr *mcppkg.Manager) mcpToolRegistryReconcileResult {
	result := mcpToolRegistryReconcileResult{}
	if reg == nil {
		return result
	}

	desired := desiredMCPToolRegistrations(mgr)
	result.Desired = len(desired)

	existingMCP := map[string]agent.ToolDescriptor{}
	for _, desc := range reg.Descriptors() {
		if desc.Origin.Kind == agent.ToolOriginKindMCP {
			existingMCP[desc.Name] = desc
		}
	}

	existingNames := make([]string, 0, len(existingMCP))
	for name := range existingMCP {
		existingNames = append(existingNames, name)
	}
	sort.Strings(existingNames)
	for _, name := range existingNames {
		if _, ok := desired[name]; ok {
			continue
		}
		if reg.Remove(name) {
			result.Removed++
			log.Printf("[mcp] unregistered tool: %s", name)
		}
	}

	desiredNames := make([]string, 0, len(desired))
	for name := range desired {
		desiredNames = append(desiredNames, name)
	}
	sort.Strings(desiredNames)
	for _, name := range desiredNames {
		registration := desired[name]
		if current, ok := reg.Descriptor(name); ok && current.Origin.Kind != agent.ToolOriginKindMCP {
			result.Conflicts++
			log.Printf("[mcp] skipped tool reconcile for %s: already owned by %s", name, current.Origin.Kind)
			continue
		}
		existing, ok := existingMCP[name]
		switch {
		case !ok:
			reg.RegisterTool(name, registration)
			result.Added++
			log.Printf("[mcp] registered tool: %s", name)
		case reflect.DeepEqual(existing, registration.Descriptor):
			result.Unchanged++
		default:
			reg.RegisterTool(name, registration)
			result.Updated++
			log.Printf("[mcp] updated tool: %s", name)
		}
	}

	return result
}

func desiredMCPToolRegistrations(mgr *mcppkg.Manager) map[string]agent.ToolRegistration {
	if mgr == nil {
		return nil
	}
	allTools := mgr.GetAllTools()
	if len(allTools) == 0 {
		return nil
	}

	serverNames := make([]string, 0, len(allTools))
	for serverName := range allTools {
		serverNames = append(serverNames, serverName)
	}
	sort.Strings(serverNames)

	registrations := make(map[string]agent.ToolRegistration)
	for _, serverName := range serverNames {
		serverTools := append([]*sdkmcp.Tool(nil), allTools[serverName]...)
		sort.SliceStable(serverTools, func(i, j int) bool {
			return strings.TrimSpace(serverTools[i].Name) < strings.TrimSpace(serverTools[j].Name)
		})
		for _, tool := range serverTools {
			name, registration := buildMCPToolRegistration(mgr, serverName, tool)
			registrations[name] = registration
		}
	}
	return registrations
}

func buildMCPToolRegistration(mgr *mcppkg.Manager, serverName string, mcpTool *sdkmcp.Tool) (string, agent.ToolRegistration) {
	name, fn, params := mcppkg.MCPToolToToolDef(mgr, serverName, mcpTool)
	description := strings.TrimSpace(mcpTool.Description)
	if description == "" {
		description = fmt.Sprintf("MCP tool from %s server", serverName)
	}
	return name, agent.ToolRegistration{
		Func:            fn,
		ProviderVisible: true,
		Descriptor: agent.ToolDescriptor{
			Name:            name,
			Description:     fmt.Sprintf("[MCP:%s] %s", serverName, description),
			Parameters:      toolParametersFromJSONSchema(params),
			InputJSONSchema: params,
			Origin: agent.ToolOrigin{
				Kind:          agent.ToolOriginKindMCP,
				ServerName:    serverName,
				CanonicalName: mcpTool.Name,
			},
		},
	}
}

func toolParametersFromJSONSchema(schema map[string]any) agent.ToolParameters {
	params := agent.ToolParameters{Type: "object"}
	if len(schema) == 0 {
		return params
	}
	if t, ok := schema["type"].(string); ok && strings.TrimSpace(t) != "" {
		params.Type = strings.TrimSpace(t)
	}
	if props, ok := schema["properties"].(map[string]any); ok && len(props) > 0 {
		params.Properties = make(map[string]agent.ToolParamProp, len(props))
		for name, value := range props {
			propMap, ok := value.(map[string]any)
			if !ok {
				continue
			}
			params.Properties[name] = toolParamPropFromJSONSchema(propMap)
		}
		if len(params.Properties) == 0 {
			params.Properties = nil
		}
	}
	params.Required = requiredStringsFromJSONSchema(schema["required"])
	return params
}

func toolParamPropFromJSONSchema(schema map[string]any) agent.ToolParamProp {
	prop := agent.ToolParamProp{}
	if t, ok := schema["type"].(string); ok {
		prop.Type = strings.TrimSpace(t)
	}
	if description, ok := schema["description"].(string); ok {
		prop.Description = description
	}
	switch raw := schema["enum"].(type) {
	case []string:
		prop.Enum = append([]string(nil), raw...)
	case []any:
		for _, value := range raw {
			if text, ok := value.(string); ok {
				prop.Enum = append(prop.Enum, text)
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		item := toolParamPropFromJSONSchema(items)
		prop.Items = &item
	}
	if value, ok := schema["default"]; ok {
		prop.Default = value
	}
	return prop
}

func requiredStringsFromJSONSchema(value any) []string {
	switch raw := value.(type) {
	case []string:
		return append([]string(nil), raw...)
	case []any:
		required := make([]string, 0, len(raw))
		for _, item := range raw {
			if text, ok := item.(string); ok {
				required = append(required, text)
			}
		}
		if len(required) == 0 {
			return nil
		}
		return required
	default:
		return nil
	}
}

func pruneMCPToolsForPendingConfig(reg *agent.ToolRegistry, snapshot mcppkg.ManagerSnapshot, resolved mcppkg.Config) int {
	if reg == nil {
		return 0
	}
	currentServers := make(map[string]mcppkg.ServerStateSnapshot, len(snapshot.Servers))
	for _, server := range snapshot.Servers {
		currentServers[server.Name] = server
	}
	removed := 0
	for _, desc := range reg.Descriptors() {
		if desc.Origin.Kind != agent.ToolOriginKindMCP {
			continue
		}
		serverName := strings.TrimSpace(desc.Origin.ServerName)
		desired, keep := resolved.Servers[serverName]
		prune := !resolved.Enabled || !keep
		if !prune {
			current, ok := currentServers[serverName]
			prune = !ok || current.State != mcppkg.ConnectionStateConnected || current.Signature != desired.Signature
		}
		if prune && reg.Remove(desc.Name) {
			removed++
			log.Printf("[mcp] pre-pruned tool: %s", desc.Name)
		}
	}
	return removed
}

func applyMCPConfigAndReconcile(ctx context.Context, mgr **mcppkg.Manager, reg *agent.ToolRegistry, resolved mcppkg.Config, logContext string) mcpToolRegistryReconcileResult {
	if mgr == nil {
		return mcpToolRegistryReconcileResult{}
	}
	if *mgr != nil {
		prePruned := pruneMCPToolsForPendingConfig(reg, (*mgr).Snapshot(), resolved)
		if prePruned > 0 {
			log.Printf("[mcp] %s pre-pruned tools=%d", logContext, prePruned)
		}
	}
	if *mgr == nil && (len(resolved.Servers) > 0 || len(resolved.DisabledServers) > 0) {
		*mgr = mcppkg.NewManager()
	}
	if *mgr == nil {
		return reconcileMCPToolRegistry(reg, nil)
	}
	if err := (*mgr).ApplyConfig(ctx, resolved); err != nil {
		log.Printf("[mcp] %s error (non-fatal): %v", logContext, err)
	}
	result := reconcileMCPToolRegistry(reg, *mgr)
	if result.Changed() || result.Conflicts > 0 {
		log.Printf("[mcp] %s tool reconcile: added=%d updated=%d removed=%d unchanged=%d desired=%d conflicts=%d", logContext, result.Added, result.Updated, result.Removed, result.Unchanged, result.Desired, result.Conflicts)
	}
	return result
}

// resolveMCPConfigWithDefaults resolves the MCP config from a config doc,
// merging in auto-detected default coding servers (GitHub, PostgreSQL, SQLite,
// filesystem) based on environment variables.
func resolveMCPConfigWithDefaults(doc state.ConfigDoc, workspaceDir string) mcppkg.Config {
	defaults := mcppkg.DefaultCodingServers(mcppkg.DefaultServerOpts{
		WorkspaceDir: workspaceDir,
	})
	return mcppkg.ResolveConfigDocWithDefaults(doc, defaults)
}
