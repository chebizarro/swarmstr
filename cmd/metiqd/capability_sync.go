package main

import (
	"context"
	"sort"
	"strings"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

type capabilityToolSurface struct {
	ToolNames         []string
	ContextVMFeatures []string
}

func buildLocalCapabilityAnnouncement(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository) nostruntime.CapabilityAnnouncement {
	if ctx == nil {
		ctx = context.Background()
	}
	surface := currentCapabilityToolSurface(ctx, cfg, docsRepo)
	return nostruntime.CapabilityAnnouncement{
		Runtime:           "metiq",
		RuntimeVersion:    version,
		DMSchemes:         currentCapabilityDMSchemes(),
		ACPVersion:        acppkg.Version,
		Tools:             surface.ToolNames,
		ContextVMFeatures: surface.ContextVMFeatures,
		Relays:            currentCapabilityPublishRelays(cfg),
	}
}

func capabilityToolSurfaceFromDefinitions(defs []agent.ToolDefinition) capabilityToolSurface {
	if len(defs) == 0 {
		return capabilityToolSurface{}
	}
	names := make([]string, 0, len(defs))
	ctxvm := make([]string, 0, 8)
	for _, def := range defs {
		if def.Name == "" {
			continue
		}
		names = append(names, def.Name)
		switch def.Name {
		case "contextvm_discover":
			ctxvm = append(ctxvm, "discover")
		case "contextvm_tools_list":
			ctxvm = append(ctxvm, "tools_list")
		case "contextvm_call":
			ctxvm = append(ctxvm, "tools_call")
		case "contextvm_resources_list":
			ctxvm = append(ctxvm, "resources_list")
		case "contextvm_resources_read":
			ctxvm = append(ctxvm, "resources_read")
		case "contextvm_prompts_list":
			ctxvm = append(ctxvm, "prompts_list")
		case "contextvm_prompts_get":
			ctxvm = append(ctxvm, "prompts_get")
		case "contextvm_raw":
			ctxvm = append(ctxvm, "raw")
		}
	}
	sort.Strings(names)
	return capabilityToolSurface{
		ToolNames:         names,
		ContextVMFeatures: nostruntime.NormalizeCapabilityValues(ctxvm),
	}
}

func currentCapabilityToolSurface(ctx context.Context, cfg state.ConfigDoc, docsRepo *state.DocsRepository) capabilityToolSurface {
	if controlServices.session.toolRegistry == nil {
		return capabilityToolSurface{}
	}
	allowed := resolvedAgentRuntimeToolAllowlist(ctx, cfg, docsRepo, "")
	exec := agent.FilteredToolExecutor(controlServices.session.toolRegistry, allowed)
	return capabilityToolSurfaceFromDefinitions(agent.ToolDefinitions(exec))
}

func currentCapabilityDMSchemes() []string {
	seen := map[string]struct{}{}
	add := func(values ...string) {
		for _, value := range values {
			if value == "" {
				continue
			}
			seen[value] = struct{}{}
		}
	}
	controlServices.relay.dmBusMu.RLock()
	defer controlServices.relay.dmBusMu.RUnlock()
	if controlServices.relay.nip17Bus != nil {
		add("giftwrap", "nip17", "nip44")
	}
	if controlServices.relay.nip04Bus != nil {
		add("nip04")
	}
	if len(seen) == 0 {
		switch (*controlServices.relay.dmBus).(type) {
		case *nostruntime.NIP17Bus:
			add("giftwrap", "nip17", "nip44")
		case *nostruntime.DMBus:
			add("nip04")
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizeCapabilityRelayURLs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = trimmed
	}
	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}

func currentCapabilityPublishRelays(cfg state.ConfigDoc) []string {
	relays := nostruntime.MergeRelayLists(cfg.Relays.Read, cfg.Relays.Write)
	if len(relays) > 0 {
		return relays
	}
	controlServices.relay.dmBusMu.RLock()
	if *controlServices.relay.dmBus != nil {
		relays = append(relays, (*controlServices.relay.dmBus).Relays()...)
	}
	controlServices.relay.dmBusMu.RUnlock()
	if controlServices.relay.controlBus != nil {
		relays = append(relays, controlServices.relay.controlBus.Relays()...)
	}
	return normalizeCapabilityRelayURLs(relays)
}

func currentCapabilitySubscriptionRelays(cfg state.ConfigDoc) []string {
	relays := append([]string{}, currentCapabilityPublishRelays(cfg)...)
	nip51AllowlistMu.RLock()
	for _, entry := range nip51FleetEntries {
		if entry.Relay != "" {
			relays = append(relays, entry.Relay)
		}
	}
	nip51AllowlistMu.RUnlock()
	if capabilityRegistry != nil {
		for _, cap := range capabilityRegistry.All() {
			relays = append(relays, cap.Relays...)
		}
	}
	return normalizeCapabilityRelayURLs(relays)
}

func applyCapabilityRuntimeState(cfg state.ConfigDoc) {
	if capabilityMonitor == nil {
		return
	}
	capabilityMonitor.UpdatePublishRelays(currentCapabilityPublishRelays(cfg))
	capabilityMonitor.UpdateSubscribeRelays(currentCapabilitySubscriptionRelays(cfg))
	var dr *state.DocsRepository; if controlServices != nil { dr = controlServices.docsRepo }
	capabilityMonitor.UpdateLocal(buildLocalCapabilityAnnouncement(context.Background(), cfg, dr))
	capabilityMonitor.TriggerPublish()
}
