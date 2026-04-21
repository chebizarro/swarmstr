package main

// main_catalog.go — Model catalog, tool catalog, extension helpers, and
// config resolution functions.
//
// Extracted from main.go to reduce god-file size. All functions remain in
// package main and reference the same globals/helpers as before.

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"metiq/internal/agent"
	mediapkg "metiq/internal/media"
	"metiq/internal/gateway/methods"
	pluginmanager "metiq/internal/plugins/manager"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

// ---------------------------------------------------------------------------
// Model catalog
// ---------------------------------------------------------------------------

func defaultModelsCatalog(configProviders map[string]state.ProviderEntry) []map[string]any {
	catalog := []map[string]any{
		{"id": "echo", "name": "Echo (built-in)", "provider": "echo", "context_window": 8192, "reasoning": false},
	}

	// HTTP provider — available when METIQ_AGENT_HTTP_URL is set.
	if strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_URL")) != "" {
		catalog = append(catalog, map[string]any{"id": "http-default", "name": "HTTP Provider", "provider": "http", "context_window": 16384, "reasoning": true, "configured": true})
	}

	// Well-known LLM providers — listed when their API key env var is set.
	type providerEntry struct {
		id, name, envKey string
		contextWindow    int
		reasoning        bool
	}
	knownProviders := []providerEntry{
		{"claude-sonnet-4-20250514", "Anthropic Claude", "ANTHROPIC_API_KEY", 200000, true},
		{"gpt-4o", "OpenAI GPT-4o", "OPENAI_API_KEY", 128000, true},
		{"gemini-2.5-pro", "Google Gemini", "GEMINI_API_KEY", 1000000, true},
		{"grok-3", "xAI Grok", "XAI_API_KEY", 131072, true},
		{"command-r-plus", "Cohere Command", "COHERE_API_KEY", 128000, false},
		{"groq/llama-4-scout-17b-16e-instruct", "Groq", "GROQ_API_KEY", 131072, false},
		{"mistral-large-latest", "Mistral AI", "MISTRAL_API_KEY", 128000, true},
		{"together/meta-llama/Llama-4-Scout-17B-16E-Instruct", "Together AI", "TOGETHER_API_KEY", 131072, false},
		{"openrouter/anthropic/claude-sonnet-4", "OpenRouter", "OPENROUTER_API_KEY", 200000, true},
	}
	for _, p := range knownProviders {
		configured := strings.TrimSpace(os.Getenv(p.envKey)) != ""
		catalog = append(catalog, map[string]any{
			"id": p.id, "name": p.name, "provider": strings.SplitN(p.id, "/", 2)[0],
			"context_window": p.contextWindow, "reasoning": p.reasoning, "configured": configured,
		})
	}

	// Providers from runtime config (extra entries from providers[] config section).
	if configProviders != nil {
		for providerID := range configProviders {
			// Skip if already covered by known entries.
			found := false
			for _, c := range catalog {
				id, _ := c["id"].(string)
				provider, _ := c["provider"].(string)
				if id == providerID || provider == providerID || strings.HasPrefix(id, providerID+"/") {
					found = true
					break
				}
			}
			if !found {
				catalog = append(catalog, map[string]any{
					"id": providerID, "name": providerID + " (config)", "provider": providerID,
					"context_window": 128000, "reasoning": true, "configured": true,
				})
			}
		}
	}
	return catalog
}

func defaultToolProfiles() []map[string]any {
	return agent.ProfilesAsResponse()
}

// configuredTranscriber returns a Transcriber based on the live config's
// extra.media_understanding.transcriber field, or nil if not specified there.
func configuredTranscriber(cfg state.ConfigDoc) mediapkg.Transcriber {
	mu, ok := cfg.Extra["media_understanding"].(map[string]any)
	if !ok {
		return nil
	}
	name, _ := mu["transcriber"].(string)
	if strings.TrimSpace(name) == "" {
		return nil
	}
	t, err := mediapkg.ResolveTranscriber(name)
	if err != nil {
		log.Printf("media transcriber config: %v", err)
		return nil
	}
	return t
}

func configuredPDFAllowedRoots(cfg state.ConfigDoc) []string {
	if toolsExtra, ok := cfg.Extra["tools"].(map[string]any); ok {
		if pdfExtra, ok := toolsExtra["pdf"].(map[string]any); ok {
			if roots, ok := extensionPolicyList(pdfExtra, "allowed_roots"); ok && len(roots) > 0 {
				return roots
			}
		}
	}
	return []string{workspace.ResolveWorkspaceDir(cfg, "")}
}

func supportedMethods(cfg state.ConfigDoc) []string {
	base := append([]string{}, methods.SupportedMethods()...)
	seen := map[string]struct{}{}
	for _, method := range base {
		seen[method] = struct{}{}
	}
	for _, method := range extensionGatewayMethods(cfg) {
		if _, ok := seen[method]; ok {
			continue
		}
		seen[method] = struct{}{}
		base = append(base, method)
	}
	sort.Strings(base)
	return base
}

func extensionGatewayMethods(cfg state.ConfigDoc) []string {
	if cfg.Extra == nil {
		return nil
	}
	rawExt, ok := cfg.Extra["extensions"].(map[string]any)
	if !ok {
		return nil
	}
	rawEntries, ok := rawExt["entries"].(map[string]any)
	if !ok {
		return nil
	}
	methodsOut := make([]string, 0)
	seen := map[string]struct{}{}
	push := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		methodsOut = append(methodsOut, v)
	}
	for pluginID, rawEntry := range rawEntries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		if !extensionEntryAllowed(rawExt, pluginID, entry) {
			continue
		}
		for _, key := range []string{"gateway_methods", "gatewayMethods"} {
			switch vals := entry[key].(type) {
			case []string:
				for _, method := range vals {
					push(method)
				}
			case []any:
				for _, raw := range vals {
					if method, ok := raw.(string); ok {
						push(method)
					}
				}
			}
		}
	}
	sort.Strings(methodsOut)
	return methodsOut
}

func extensionEntryAllowed(rawExt map[string]any, pluginID string, entry map[string]any) bool {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return false
	}
	if enabled, ok := rawExt["enabled"].(bool); ok && !enabled {
		return false
	}
	if load, ok := rawExt["load"].(bool); ok && !load {
		return false
	}
	denyList, denyValid := extensionPolicyList(rawExt, "deny")
	if !denyValid {
		log.Printf("WARNING: invalid plugins.deny list type, blocking all plugins (fail-closed)")
		return false
	}
	deny := map[string]struct{}{}
	for _, item := range denyList {
		deny[item] = struct{}{}
	}
	if _, blocked := deny[pluginID]; blocked {
		return false
	}
	allow, allowValid := extensionPolicyList(rawExt, "allow")
	if !allowValid {
		log.Printf("WARNING: invalid plugins.allow list type, blocking all plugins (fail-closed)")
		return false
	}
	if len(allow) > 0 {
		allowed := false
		for _, candidate := range allow {
			if candidate == pluginID {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	if enabled, ok := entry["enabled"].(bool); ok && !enabled {
		return false
	}
	return true
}

func extensionPolicyList(rawExt map[string]any, key string) ([]string, bool) {
	raw, exists := rawExt[key]
	if !exists {
		return nil, true
	}
	switch values := raw.(type) {
	case []string:
		return sanitizeStrings(values), true
	case []any:
		out := make([]string, 0, len(values))
		for _, item := range values {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return sanitizeStrings(out), true
	default:
		return nil, false
	}
}


type coreToolSection struct {
	ID    string
	Label string
}

type coreToolDef struct {
	ID          string
	Label       string
	Description string
	SectionID   string
	Profiles    []string
}

var coreToolSections = []coreToolSection{
	{ID: "fs", Label: "Files"},
	{ID: "runtime", Label: "Runtime"},
	{ID: "web", Label: "Web"},
	{ID: "memory", Label: "Memory"},
	{ID: "sessions", Label: "Sessions"},
	{ID: "ui", Label: "UI"},
	{ID: "messaging", Label: "Messaging"},
	{ID: "automation", Label: "Automation"},
	{ID: "nodes", Label: "Nodes"},
	{ID: "agents", Label: "Agents"},
	{ID: "media", Label: "Media"},
}

var coreToolCatalog = []coreToolDef{
	{ID: "apply_patch", Label: "apply_patch", Description: "Patch files", SectionID: "fs", Profiles: []string{"coding"}},
	{ID: "read_pdf", Label: "read_pdf", Description: "Read local PDF files", SectionID: "fs", Profiles: []string{"coding"}},
	{ID: "memory_search", Label: "memory_search", Description: "Search memory index", SectionID: "memory", Profiles: []string{"coding"}},
	{ID: "memory_store", Label: "memory_store", Description: "Store memory entries", SectionID: "memory", Profiles: []string{"coding"}},
	{ID: "memory_delete", Label: "memory_delete", Description: "Delete memory entries", SectionID: "memory", Profiles: []string{"coding"}},
	{ID: "sessions_list", Label: "sessions_list", Description: "List sessions", SectionID: "sessions", Profiles: []string{"coding", "messaging"}},
	{ID: "session_spawn", Label: "session_spawn", Description: "Spawn sub-agent session", SectionID: "sessions", Profiles: []string{"coding"}},
	{ID: "session_send", Label: "session_send", Description: "Send to session", SectionID: "sessions", Profiles: []string{"coding", "messaging"}},
	{ID: "canvas_update", Label: "canvas_update", Description: "Update shared canvas", SectionID: "ui", Profiles: []string{}},
	{ID: "add_reaction", Label: "add_reaction", Description: "Add emoji reaction", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "remove_reaction", Label: "remove_reaction", Description: "Remove emoji reaction", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "send_typing", Label: "send_typing", Description: "Send typing indicator", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "send_in_thread", Label: "send_in_thread", Description: "Send message in thread", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "edit_message", Label: "edit_message", Description: "Edit channel message", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "cron_add", Label: "cron_add", Description: "Schedule recurring task", SectionID: "automation", Profiles: []string{"coding"}},
	{ID: "cron_list", Label: "cron_list", Description: "List scheduled tasks", SectionID: "automation", Profiles: []string{"coding"}},
	{ID: "cron_remove", Label: "cron_remove", Description: "Remove scheduled task", SectionID: "automation", Profiles: []string{"coding"}},
	{ID: "social_plan_add", Label: "social_plan_add", Description: "Add social action plan", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "social_plan_list", Label: "social_plan_list", Description: "List social plans & usage", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "social_plan_remove", Label: "social_plan_remove", Description: "Remove social plan", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "social_history", Label: "social_history", Description: "Query social action history", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "social_record", Label: "social_record", Description: "Record social action", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "fleet_agents", Label: "fleet_agents", Description: "List fleet agents", SectionID: "fleet", Profiles: []string{"coding"}},
	{ID: "nostr_agent_rpc", Label: "nostr_agent_rpc", Description: "Sync RPC to fleet agent", SectionID: "fleet", Profiles: []string{"coding"}},
	{ID: "nostr_agent_send", Label: "nostr_agent_send", Description: "Async send to fleet agent", SectionID: "fleet", Profiles: []string{"coding"}},
	{ID: "nostr_agent_inbox", Label: "nostr_agent_inbox", Description: "Poll fleet agent replies", SectionID: "fleet", Profiles: []string{"coding"}},
	{ID: "node_invoke", Label: "node_invoke", Description: "Invoke a remote node", SectionID: "nodes", Profiles: []string{}},
	{ID: "node_list", Label: "node_list", Description: "List known nodes", SectionID: "nodes", Profiles: []string{}},
	{ID: "acp_delegate", Label: "acp_delegate", Description: "Delegate ACP task to peer", SectionID: "nodes", Profiles: []string{}},
	{ID: "web_search", Label: "web_search", Description: "Search the web", SectionID: "web", Profiles: []string{}},
	{ID: "web_fetch", Label: "web_fetch", Description: "Fetch web content", SectionID: "web", Profiles: []string{}},
	{ID: "image", Label: "image", Description: "Image understanding", SectionID: "media", Profiles: []string{"coding"}},
	{ID: "tts", Label: "tts", Description: "Text-to-speech conversion", SectionID: "media", Profiles: []string{}},
}

func buildToolCatalogGroups(cfg state.ConfigDoc, registry *agent.ToolRegistry, includePlugins *bool, pm *pluginmanager.GojaPluginManager) []map[string]any {
	sectionTools := map[string][]map[string]any{}
	seen := map[string]struct{}{}
	addCore := func(sectionID, id, label, description string, profiles []string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		entry := map[string]any{
			"id":              id,
			"label":           label,
			"description":     description,
			"source":          "core",
			"defaultProfiles": profiles,
		}
		sectionTools[sectionID] = append(sectionTools[sectionID], entry)
	}
	for _, tool := range coreToolCatalog {
		addCore(tool.SectionID, tool.ID, tool.Label, tool.Description, tool.Profiles)
	}
	for _, tool := range coreToolCatalog {
		if tool.ID != "" {
			seen[tool.ID] = struct{}{}
		}
	}
	groups := make([]map[string]any, 0, len(coreToolSections)+4)
	for _, section := range coreToolSections {
		tools := sectionTools[section.ID]
		if len(tools) == 0 {
			continue
		}
		sort.Slice(tools, func(i, j int) bool {
			return fmt.Sprintf("%v", tools[i]["id"]) < fmt.Sprintf("%v", tools[j]["id"])
		})
		groups = append(groups, map[string]any{
			"id":     section.ID,
			"label":  section.Label,
			"source": "core",
			"tools":  tools,
		})
	}
	if includePlugins != nil && !*includePlugins {
		return groups
	}
	for _, group := range buildPluginToolGroups(cfg, seen) {
		groups = append(groups, group)
	}
	// Append live Goja plugin tools (real manifests from loaded VMs).
	if pm != nil {
		for _, group := range pm.CatalogGroups(seen) {
			groups = append(groups, group)
		}
	}
	return groups
}

func buildPluginToolGroups(cfg state.ConfigDoc, seen map[string]struct{}) []map[string]any {
	if cfg.Extra == nil {
		return nil
	}
	rawExt, ok := cfg.Extra["extensions"].(map[string]any)
	if !ok {
		return nil
	}
	rawEntries, ok := rawExt["entries"].(map[string]any)
	if !ok {
		return nil
	}
	pluginIDs := make([]string, 0, len(rawEntries))
	for pluginID := range rawEntries {
		pluginIDs = append(pluginIDs, pluginID)
	}
	sort.Strings(pluginIDs)
	groups := make([]map[string]any, 0, len(pluginIDs))
	for _, pluginID := range pluginIDs {
		rawEntry, ok := rawEntries[pluginID].(map[string]any)
		if !ok {
			continue
		}
		if !extensionEntryAllowed(rawExt, pluginID, rawEntry) {
			continue
		}
		tools := make([]map[string]any, 0)
		switch rawTools := rawEntry["tools"].(type) {
		case []string:
			for _, t := range rawTools {
				trimmed := strings.TrimSpace(t)
				if trimmed == "" {
					continue
				}
				if _, exists := seen[trimmed]; exists {
					continue
				}
				seen[trimmed] = struct{}{}
				tools = append(tools, map[string]any{
					"id":              trimmed,
					"label":           trimmed,
					"description":     "Plugin tool",
					"source":          "plugin",
					"pluginId":        pluginID,
					"defaultProfiles": []string{},
				})
			}
		case []any:
			for _, rawTool := range rawTools {
				switch t := rawTool.(type) {
				case string:
					trimmed := strings.TrimSpace(t)
					if trimmed == "" {
						continue
					}
					if _, exists := seen[trimmed]; exists {
						continue
					}
					seen[trimmed] = struct{}{}
					tools = append(tools, map[string]any{
						"id":              trimmed,
						"label":           trimmed,
						"description":     "Plugin tool",
						"source":          "plugin",
						"pluginId":        pluginID,
						"defaultProfiles": []string{},
					})
				case map[string]any:
					idRaw, ok := t["id"].(string)
					if !ok {
						continue
					}
					id := strings.TrimSpace(idRaw)
					if id == "" {
						continue
					}
					if _, exists := seen[id]; exists {
						continue
					}
					seen[id] = struct{}{}
					label, _ := t["label"].(string)
					label = strings.TrimSpace(label)
					if label == "" {
						label = id
					}
					description, _ := t["description"].(string)
					description = strings.TrimSpace(description)
					if description == "" {
						description = "Plugin tool"
					}
					optional, hasOptional := t["optional"].(bool)
					profiles := getStringSlice(t, "default_profiles")
					if len(profiles) == 0 {
						profiles = getStringSlice(t, "defaultProfiles")
					}
					toolEntry := map[string]any{
						"id":              id,
						"label":           label,
						"description":     description,
						"source":          "plugin",
						"pluginId":        pluginID,
						"defaultProfiles": profiles,
					}
					// Only include optional field when explicitly true to reduce payload size.
					// Omitting the field is semantically equivalent to optional=false.
					if hasOptional && optional {
						toolEntry["optional"] = true
					}
					tools = append(tools, toolEntry)
				}
			}
		}
		if len(tools) == 0 {
			continue
		}
		sort.Slice(tools, func(i, j int) bool {
			return fmt.Sprintf("%v", tools[i]["id"]) < fmt.Sprintf("%v", tools[j]["id"])
		})
		groups = append(groups, map[string]any{
			"id":       "plugin:" + pluginID,
			"label":    pluginID,
			"source":   "plugin",
			"pluginId": pluginID,
			"tools":    tools,
		})
	}
	return groups
}
