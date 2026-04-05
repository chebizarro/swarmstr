package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	mcppkg "metiq/internal/mcp"
	"metiq/internal/policy"
	secretspkg "metiq/internal/secrets"
	"metiq/internal/store/state"
)

type mcpOpsController struct {
	manager           **mcppkg.Manager
	tools             *agent.ToolRegistry
	auth              *mcpAuthController
	configState       *runtimeConfigStore
	docsRepo          *state.DocsRepository
	managerFactory    func() *mcppkg.Manager
	persistConfigFile func(state.ConfigDoc) error
	scheduleRestart   func(old, next state.ConfigDoc, delayMS int) bool
}

func newMCPOpsController(manager **mcppkg.Manager, tools *agent.ToolRegistry, auth *mcpAuthController, configState *runtimeConfigStore, docsRepo *state.DocsRepository) *mcpOpsController {
	return &mcpOpsController{
		manager:           manager,
		tools:             tools,
		auth:              auth,
		configState:       configState,
		docsRepo:          docsRepo,
		managerFactory:    mcppkg.NewManager,
		persistConfigFile: persistRuntimeConfigFile,
		scheduleRestart:   scheduleRestartIfNeeded,
	}
}

func (c *mcpOpsController) applyList(_ context.Context, _ methods.MCPListRequest) (map[string]any, error) {
	doc, resolved, snapshot, err := c.inventoryState()
	if err != nil {
		return nil, err
	}
	servers := c.buildInventory(resolved, snapshot)
	return map[string]any{
		"ok":         true,
		"enabled":    resolved.Enabled,
		"count":      len(servers),
		"hash":       doc.Hash(),
		"servers":    servers,
		"suppressed": append([]mcppkg.SuppressedServer(nil), resolved.Suppressed...),
	}, nil
}

func (c *mcpOpsController) applyGet(_ context.Context, req methods.MCPGetRequest) (map[string]any, error) {
	_, resolved, snapshot, err := c.inventoryState()
	if err != nil {
		return nil, err
	}
	server, ok := c.lookupInventoryServer(req.Server, resolved, snapshot)
	if !ok {
		return nil, fmt.Errorf("mcp server %s not configured", req.Server)
	}
	return map[string]any{"ok": true, "server": server}, nil
}

func (c *mcpOpsController) applyPut(ctx context.Context, req methods.MCPPutRequest) (map[string]any, error) {
	cfg, err := c.currentConfig()
	if err != nil {
		return nil, err
	}
	next, err := methods.ApplyConfigSet(cfg, "mcp.servers."+req.Server, withEnabledDefault(req.Config))
	if err != nil {
		return nil, err
	}
	next, err = methods.ApplyConfigSet(next, "mcp.enabled", true)
	if err != nil {
		return nil, err
	}
	eventID, restartPending, err := c.persistConfig(ctx, cfg, next)
	if err != nil {
		return nil, err
	}
	_, resolved, snapshot, err := c.inventoryState()
	if err != nil {
		return nil, err
	}
	server, ok := c.lookupInventoryServer(req.Server, resolved, snapshot)
	if !ok {
		return nil, fmt.Errorf("mcp server %s not configured after put", req.Server)
	}
	result := map[string]any{
		"ok":              true,
		"hash":            next.Hash(),
		"restart_pending": restartPending,
		"server":          server,
	}
	if eventID != "" {
		result["event_id"] = eventID
	}
	return result, nil
}

func (c *mcpOpsController) applyRemove(ctx context.Context, req methods.MCPRemoveRequest) (map[string]any, error) {
	cfg, err := c.currentConfig()
	if err != nil {
		return nil, err
	}
	resolved := mcppkg.ResolveConfigDoc(cfg)
	if _, ok := resolved.Servers[req.Server]; !ok {
		if _, ok := resolved.DisabledServers[req.Server]; !ok {
			if _, ok := resolved.FilteredServers[req.Server]; !ok {
				return nil, fmt.Errorf("mcp server %s not configured", req.Server)
			}
		}
	}
	next, err := methods.ApplyConfigSet(cfg, "mcp.servers."+req.Server, nil)
	if err != nil {
		return nil, err
	}
	eventID, restartPending, err := c.persistConfig(ctx, cfg, next)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"ok":              true,
		"removed":         true,
		"server":          req.Server,
		"hash":            next.Hash(),
		"restart_pending": restartPending,
		"remaining":       len(c.buildInventory(mcppkg.ResolveConfigDoc(next), c.managerSnapshot())),
	}
	if eventID != "" {
		result["event_id"] = eventID
	}
	return result, nil
}

func (c *mcpOpsController) applyTest(ctx context.Context, req methods.MCPTestRequest) (map[string]any, error) {
	resolvedServer, usingInline, err := c.resolveTestServer(req)
	if err != nil {
		return nil, err
	}
	factory := c.managerFactory
	if factory == nil {
		factory = mcppkg.NewManager
	}
	mgr := factory()
	if mgr == nil {
		mgr = mcppkg.NewManager()
	}
	defer func() { _ = mgr.Close() }()
	if c.auth != nil {
		c.auth.InstallOnManager(mgr)
	}
	timeout := 30 * time.Second
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	applyErr := mgr.ApplyConfig(testCtx, mcppkg.Config{
		Enabled: true,
		Servers: map[string]mcppkg.ResolvedServerConfig{req.Server: resolvedServer},
	})
	testResolved := mcppkg.Config{Enabled: true, Servers: map[string]mcppkg.ResolvedServerConfig{req.Server: resolvedServer}}
	server, _ := c.lookupInventoryServer(req.Server, testResolved, mgr.Snapshot())
	result := map[string]any{
		"ok":                  applyErr == nil,
		"server":              server,
		"using_inline_config": usingInline,
		"timeout_ms":          int(timeout / time.Millisecond),
	}
	if applyErr != nil {
		result["error"] = applyErr.Error()
	}
	return result, nil
}

func (c *mcpOpsController) applyReconnect(ctx context.Context, req methods.MCPReconnectRequest) (map[string]any, error) {
	_, resolved, _, err := c.inventoryState()
	if err != nil {
		return nil, err
	}
	mgr := c.liveManager()
	if mgr == nil {
		return nil, fmt.Errorf("mcp manager not configured")
	}
	reconnectErr := mgr.ReconnectServer(ctx, req.Server)
	if reconnectErr == nil && c.tools != nil {
		reconcileMCPToolRegistry(c.tools, mgr)
	}
	server, ok := c.lookupInventoryServer(req.Server, resolved, mgr.Snapshot())
	if !ok {
		return nil, fmt.Errorf("mcp server %s not configured", req.Server)
	}
	result := map[string]any{"ok": reconnectErr == nil, "server": server}
	if reconnectErr != nil {
		result["error"] = reconnectErr.Error()
	}
	return result, nil
}

func (c *mcpOpsController) currentConfig() (state.ConfigDoc, error) {
	if c == nil || c.configState == nil {
		return state.ConfigDoc{}, fmt.Errorf("config state not configured")
	}
	return c.configState.Get(), nil
}

func (c *mcpOpsController) inventoryState() (state.ConfigDoc, mcppkg.Config, mcppkg.ManagerSnapshot, error) {
	cfg, err := c.currentConfig()
	if err != nil {
		return state.ConfigDoc{}, mcppkg.Config{}, mcppkg.ManagerSnapshot{}, err
	}
	resolved := mcppkg.ResolveConfigDoc(cfg)
	snapshot := c.managerSnapshot()
	if !snapshot.Enabled {
		snapshot.Enabled = resolved.Enabled
	}
	if len(snapshot.Suppressed) == 0 && len(resolved.Suppressed) > 0 {
		snapshot.Suppressed = append([]mcppkg.SuppressedServer(nil), resolved.Suppressed...)
	}
	return cfg, resolved, snapshot, nil
}

func (c *mcpOpsController) managerSnapshot() mcppkg.ManagerSnapshot {
	if mgr := c.liveManager(); mgr != nil {
		return mgr.Snapshot()
	}
	return mcppkg.ManagerSnapshot{}
}

func (c *mcpOpsController) liveManager() *mcppkg.Manager {
	if c == nil || c.manager == nil {
		return nil
	}
	return *c.manager
}

func (c *mcpOpsController) persistConfig(ctx context.Context, oldCfg, next state.ConfigDoc) (string, bool, error) {
	if c == nil || c.docsRepo == nil || c.configState == nil {
		return "", false, fmt.Errorf("config persistence not configured")
	}
	next = policy.NormalizeConfig(next)
	if err := policy.ValidateConfig(next); err != nil {
		return "", false, err
	}
	persist := c.persistConfigFile
	if persist == nil {
		persist = persistRuntimeConfigFile
	}
	if err := persist(next); err != nil {
		return "", false, err
	}
	evt, err := c.docsRepo.PutConfig(ctx, next)
	if err != nil {
		return "", false, err
	}
	c.configState.Set(next)
	schedule := c.scheduleRestart
	if schedule == nil {
		schedule = scheduleRestartIfNeeded
	}
	return strings.TrimSpace(evt.ID), schedule(oldCfg, next, 0), nil
}

func (c *mcpOpsController) resolveTestServer(req methods.MCPTestRequest) (mcppkg.ResolvedServerConfig, bool, error) {
	cfg, err := c.currentConfig()
	if err != nil {
		return mcppkg.ResolvedServerConfig{}, false, err
	}
	working := cfg
	usingInline := len(req.Config) > 0
	var serverMap map[string]any
	if usingInline {
		serverMap = cloneAnyMap(req.Config)
	} else {
		resolved := mcppkg.ResolveConfigDoc(cfg)
		current, ok := resolved.Servers[req.Server]
		if !ok {
			current, ok = resolved.DisabledServers[req.Server]
		}
		if !ok {
			if filtered, found := resolved.FilteredServers[req.Server]; found {
				current = filtered.ResolvedServerConfig
				ok = true
			}
		}
		if !ok {
			return mcppkg.ResolvedServerConfig{}, false, fmt.Errorf("mcp server %s not configured", req.Server)
		}
		serverMap = serverConfigMap(current.ServerConfig)
	}
	serverMap["enabled"] = true
	working, err = methods.ApplyConfigSet(working, "mcp.enabled", true)
	if err != nil {
		return mcppkg.ResolvedServerConfig{}, false, err
	}
	working, err = methods.ApplyConfigSet(working, "mcp.servers."+req.Server, serverMap)
	if err != nil {
		return mcppkg.ResolvedServerConfig{}, false, err
	}
	working = policy.NormalizeConfig(working)
	if err := policy.ValidateConfig(working); err != nil {
		return mcppkg.ResolvedServerConfig{}, false, err
	}
	resolved := mcppkg.ResolveConfigDoc(working)
	server, ok := resolved.Servers[req.Server]
	if !ok {
		return mcppkg.ResolvedServerConfig{}, false, fmt.Errorf("mcp server %s test config did not resolve to enabled server", req.Server)
	}
	return server, usingInline, nil
}

func (c *mcpOpsController) buildInventory(resolved mcppkg.Config, snapshot mcppkg.ManagerSnapshot) []map[string]any {
	byName := make(map[string]map[string]any, len(snapshot.Servers)+len(resolved.Servers)+len(resolved.DisabledServers)+len(resolved.FilteredServers))
	for name, server := range resolved.Servers {
		s := server
		byName[name] = c.buildServerPayload(name, &s, nil, nil)
	}
	for name, server := range resolved.DisabledServers {
		s := server
		byName[name] = c.buildServerPayload(name, &s, nil, nil)
	}
	for name, server := range resolved.FilteredServers {
		s := server
		byName[name] = c.buildServerPayload(name, &s.ResolvedServerConfig, &s, nil)
	}
	for _, runtime := range snapshot.Servers {
		rt := runtime
		if resolvedServer, ok := resolved.Servers[runtime.Name]; ok {
			rs := resolvedServer
			byName[runtime.Name] = c.buildServerPayload(runtime.Name, &rs, nil, &rt)
			continue
		}
		if resolvedServer, ok := resolved.DisabledServers[runtime.Name]; ok {
			rs := resolvedServer
			byName[runtime.Name] = c.buildServerPayload(runtime.Name, &rs, nil, &rt)
			continue
		}
		if filteredServer, ok := resolved.FilteredServers[runtime.Name]; ok {
			fs := filteredServer
			byName[runtime.Name] = c.buildServerPayload(runtime.Name, &fs.ResolvedServerConfig, &fs, &rt)
			continue
		}
		byName[runtime.Name] = c.buildServerPayload(runtime.Name, nil, nil, &rt)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]map[string]any, 0, len(names))
	for _, name := range names {
		servers = append(servers, byName[name])
	}
	return servers
}

func (c *mcpOpsController) lookupInventoryServer(name string, resolved mcppkg.Config, snapshot mcppkg.ManagerSnapshot) (map[string]any, bool) {
	for _, server := range c.buildInventory(resolved, snapshot) {
		if strings.EqualFold(strings.TrimSpace(anyString(server["name"])), strings.TrimSpace(name)) {
			return server, true
		}
	}
	return nil, false
}

func (c *mcpOpsController) buildServerPayload(name string, resolved *mcppkg.ResolvedServerConfig, filtered *mcppkg.FilteredServer, runtime *mcppkg.ServerStateSnapshot) map[string]any {
	payload := map[string]any{"name": name}
	if resolved != nil {
		payload["configured"] = true
		payload["enabled"] = resolved.Enabled
		payload["source"] = resolved.Source
		payload["precedence"] = resolved.Precedence
		payload["signature"] = resolved.Signature
		if transport := inferServerTransport(resolved.ServerConfig); transport != "" {
			payload["transport"] = transport
		}
		if resolved.Command != "" {
			payload["command"] = resolved.Command
		}
		if resolved.URL != "" {
			payload["url"] = resolved.URL
		}
		payload["arg_count"] = len(resolved.Args)
		if keys := sortedStringKeys(resolved.Env); len(keys) > 0 {
			payload["env_keys"] = keys
		}
		if keys := sortedStringKeys(resolved.Headers); len(keys) > 0 {
			payload["header_keys"] = keys
		}
		payload["oauth_configured"] = supportsRemoteOAuth(resolved.ServerConfig)
		if resolved.OAuth != nil {
			payload["oauth"] = map[string]any{
				"enabled":               resolved.OAuth.Enabled,
				"client_id":             resolved.OAuth.ClientID,
				"authorize_url":         resolved.OAuth.AuthorizeURL,
				"token_url":             resolved.OAuth.TokenURL,
				"revoke_url":            resolved.OAuth.RevokeURL,
				"scopes":                append([]string(nil), resolved.OAuth.Scopes...),
				"use_pkce":              resolved.OAuth.UsePKCE,
				"client_secret_ref_set": strings.TrimSpace(resolved.OAuth.ClientSecretRef) != "",
			}
		}
		if credentialKey := mcppkg.CredentialKey(resolved.ServerConfig); credentialKey != "" {
			payload["credential_key"] = credentialKey
			cred, hasCred := c.authCredential(resolved.ServerConfig)
			payload["has_credentials"] = hasCred
			if hasCred {
				payload["has_refresh_token"] = strings.TrimSpace(cred.RefreshToken) != ""
				payload["token_type"] = tokenType(cred)
				if expiry := timeToMillis(cred.Expiry); expiry != 0 {
					payload["credential_expires_at_ms"] = expiry
				}
				if updatedAt := timeToMillis(cred.UpdatedAt); updatedAt != 0 {
					payload["credential_updated_at_ms"] = updatedAt
				}
			}
		}
	}
	if filtered != nil {
		payload["policy_status"] = filtered.PolicyStatus
		payload["policy_reason"] = filtered.PolicyReason
	}
	if runtime != nil {
		payload["runtime_present"] = true
		payload["state"] = runtime.State
		payload["tool_count"] = runtime.ToolCount
		if runtime.Capabilities != (mcppkg.CapabilitySnapshot{}) {
			payload["capabilities"] = map[string]any{
				"tools":     runtime.Capabilities.Tools,
				"resources": runtime.Capabilities.Resources,
				"prompts":   runtime.Capabilities.Prompts,
				"logging":   runtime.Capabilities.Logging,
			}
		}
		if runtime.ServerInfo != nil {
			payload["server_info"] = runtime.ServerInfo
		}
		if runtime.Instructions != "" {
			payload["instructions"] = runtime.Instructions
		}
		if runtime.LastError != "" {
			payload["last_error"] = runtime.LastError
		}
		payload["reconnect_attempts"] = runtime.ReconnectAttempts
		if runtime.LastAttemptAtMS != 0 {
			payload["last_attempt_at_ms"] = runtime.LastAttemptAtMS
		}
		if runtime.LastConnectedAtMS != 0 {
			payload["last_connected_at_ms"] = runtime.LastConnectedAtMS
		}
		if runtime.LastFailedAtMS != 0 {
			payload["last_failed_at_ms"] = runtime.LastFailedAtMS
		}
		if runtime.UpdatedAtMS != 0 {
			payload["updated_at_ms"] = runtime.UpdatedAtMS
		}
	} else {
		payload["runtime_present"] = false
		if filtered != nil {
			payload["state"] = string(filtered.PolicyStatus)
		} else if resolved != nil {
			if resolved.Enabled {
				payload["state"] = mcppkg.ConnectionStatePending
			} else {
				payload["state"] = mcppkg.ConnectionStateDisabled
			}
		}
	}
	return payload
}

func (c *mcpOpsController) authCredential(cfg mcppkg.ServerConfig) (secretspkg.MCPAuthCredential, bool) {
	if c == nil || c.auth == nil {
		return secretspkg.MCPAuthCredential{}, false
	}
	return c.auth.currentCredential(cfg)
}

func inferServerTransport(cfg mcppkg.ServerConfig) string {
	if transport := strings.ToLower(strings.TrimSpace(cfg.Type)); transport != "" {
		return transport
	}
	if strings.TrimSpace(cfg.URL) != "" {
		return "sse"
	}
	if strings.TrimSpace(cfg.Command) != "" {
		return "stdio"
	}
	return ""
}

func sortedStringKeys[V any](items map[string]V) []string {
	if len(items) == 0 {
		return nil
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func withEnabledDefault(in map[string]any) map[string]any {
	out := cloneAnyMap(in)
	if _, ok := out["enabled"]; !ok {
		out["enabled"] = true
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func serverConfigMap(cfg mcppkg.ServerConfig) map[string]any {
	raw, _ := json.Marshal(cfg)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func anyString(v any) string {
	text, _ := v.(string)
	return text
}
