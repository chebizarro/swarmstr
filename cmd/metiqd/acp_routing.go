package main

import (
	"context"
	"fmt"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const (
	acpDMCompatibilityIncompatible = iota
	acpDMCompatibilityUnknown
	acpDMCompatibilityCompatible
)

const (
	acpCapabilityCompatibilityIncompatible = iota
	acpCapabilityCompatibilityUnknown
	acpCapabilityCompatibilityCompatible
)

type acpTargetRequirements struct {
	ToolNames         []string
	ContextVMFeatures []string
}

func normalizeACPAdvertisedScheme(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "nip04", "nip-04":
		return "nip04"
	case "giftwrap", "nip17", "nip-17", "nip44", "nip-44", "nip59", "nip-59":
		return "nip17"
	case "fips":
		return "fips"
	default:
		return ""
	}
}

func normalizeACPAdvertisedSchemes(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := map[string]struct{}{}
	for _, value := range values {
		if mode := normalizeACPAdvertisedScheme(value); mode != "" {
			out[mode] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func currentACPTransportBus(mode string) nostruntime.DMTransport {
	if controlServices != nil {
		return controlServices.currentACPTransportBus(mode)
	}
	// Fallback: use package-level globals (test compatibility).
	mode, ok := state.ParseACPTransportMode(mode)
	if !ok || mode == "auto" {
		return nil
	}
	switch mode {
	case "nip17":
		if controlNIP17Bus != nil {
			return controlNIP17Bus
		}
	case "nip04":
		if controlNIP04Bus != nil {
			return controlNIP04Bus
		}
	case "fips":
		if controlTransportSelector != nil {
			return controlTransportSelector
		}
	}
	return nil
}

func (s *daemonServices) currentACPTransportBus(mode string) nostruntime.DMTransport {
	mode, ok := state.ParseACPTransportMode(mode)
	if !ok || mode == "auto" {
		return nil
	}
	s.relay.dmBusMu.RLock()
	defer s.relay.dmBusMu.RUnlock()
	switch mode {
	case "nip17":
		if s.relay.nip17Bus != nil {
			return s.relay.nip17Bus
		}
		if _, ok := (*s.relay.dmBus).(*nostruntime.NIP17Bus); ok {
			return *s.relay.dmBus
		}
	case "nip04":
		if s.relay.nip04Bus != nil {
			return s.relay.nip04Bus
		}
		if _, ok := (*s.relay.dmBus).(*nostruntime.DMBus); ok {
			return *s.relay.dmBus
		}
	case "fips":
		if s.relay.transportSelector != nil {
			return s.relay.transportSelector
		}
	}
	return nil
}

func availableACPTransportModes(cfg state.ConfigDoc) map[string]struct{} {
	out := map[string]struct{}{}
	switch cfg.ACPTransportMode() {
	case "nip17":
		if currentACPTransportBus("nip17") != nil {
			out["nip17"] = struct{}{}
		}
	case "nip04":
		if currentACPTransportBus("nip04") != nil {
			out["nip04"] = struct{}{}
		}
	case "fips":
		if currentACPTransportBus("fips") != nil {
			out["fips"] = struct{}{}
		}
	default:
		if currentACPTransportBus("nip17") != nil {
			out["nip17"] = struct{}{}
		}
		if currentACPTransportBus("nip04") != nil {
			out["nip04"] = struct{}{}
		}
		if currentACPTransportBus("fips") != nil {
			out["fips"] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func fleetEntryDMCompatibility(entry toolbuiltin.FleetEntry) int {
	return fleetEntryDMCompatibilityForConfig(entry, state.ConfigDoc{})
}

func fleetEntryDMCompatibilityForConfig(entry toolbuiltin.FleetEntry, cfg state.ConfigDoc) int {
	remote := normalizeACPAdvertisedSchemes(entry.DMSchemes)
	if len(remote) == 0 {
		return acpDMCompatibilityUnknown
	}
	local := availableACPTransportModes(cfg)
	if len(local) == 0 {
		return acpDMCompatibilityIncompatible
	}
	for mode := range remote {
		if _, ok := local[mode]; ok {
			return acpDMCompatibilityCompatible
		}
	}
	return acpDMCompatibilityIncompatible
}

func fleetDisplayNameForACP(entry toolbuiltin.FleetEntry) string {
	if name := strings.TrimSpace(entry.Name); name != "" {
		return name
	}
	if len(entry.Pubkey) >= 12 {
		return entry.Pubkey[:12] + "..."
	}
	return entry.Pubkey
}

func acpTargetDisplayName(pubkey string) string {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return ""
	}
	if pk, err := nostruntime.ParsePubKey(pubkey); err == nil {
		pubkey = pk.Hex()
	}
	for _, entry := range fleetDirectory() {
		if strings.EqualFold(strings.TrimSpace(entry.Pubkey), pubkey) {
			return fleetDisplayNameForACP(entry)
		}
	}
	if len(pubkey) >= 12 {
		return pubkey[:12] + "..."
	}
	return pubkey
}

func contextVMFeaturesFromToolNames(toolNames []string) []string {
	if len(toolNames) == 0 {
		return nil
	}
	defs := make([]agent.ToolDefinition, 0, len(toolNames))
	for _, name := range toolNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		defs = append(defs, agent.ToolDefinition{Name: trimmed})
	}
	surface := capabilityToolSurfaceFromDefinitions(defs)
	return surface.ContextVMFeatures
}

func advertisedContextVMFeatureSet(entry toolbuiltin.FleetEntry) map[string]struct{} {
	values := append([]string{}, entry.ContextVMFeatures...)
	values = append(values, contextVMFeaturesFromToolNames(entry.Tools)...)
	if len(values) == 0 {
		return nil
	}
	out := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildACPTargetRequirements(cfg state.ConfigDoc, constraints turnToolConstraints) acpTargetRequirements {
	if controlServices != nil {
		return controlServices.buildACPTargetRequirements(cfg, constraints)
	}
	// Fallback: use package-level tool registry (test compatibility).
	if controlToolRegistry == nil {
		return acpTargetRequirements{}
	}
	allowed := intersectTurnToolConstraints(nil, cfg, constraints)
	if allowed == nil {
		return acpTargetRequirements{}
	}
	exec := agent.FilteredToolExecutor(controlToolRegistry, allowed)
	surface := capabilityToolSurfaceFromDefinitions(agent.ToolDefinitions(exec))
	return acpTargetRequirements{ToolNames: surface.ToolNames, ContextVMFeatures: surface.ContextVMFeatures}
}

func (s *daemonServices) buildACPTargetRequirements(cfg state.ConfigDoc, constraints turnToolConstraints) acpTargetRequirements {
	if s.session.toolRegistry == nil {
		return acpTargetRequirements{}
	}
	allowed := intersectTurnToolConstraints(nil, cfg, constraints)
	if allowed == nil {
		return acpTargetRequirements{}
	}
	exec := agent.FilteredToolExecutor(s.session.toolRegistry, allowed)
	surface := capabilityToolSurfaceFromDefinitions(agent.ToolDefinitions(exec))
	return acpTargetRequirements{ToolNames: surface.ToolNames, ContextVMFeatures: surface.ContextVMFeatures}
}

func fleetEntryCapabilityCompatibility(entry toolbuiltin.FleetEntry, req acpTargetRequirements) int {
	if len(req.ToolNames) == 0 && len(req.ContextVMFeatures) == 0 {
		return acpCapabilityCompatibilityCompatible
	}
	toolSet := map[string]struct{}{}
	for _, name := range entry.Tools {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		toolSet[trimmed] = struct{}{}
	}
	featureSet := advertisedContextVMFeatureSet(entry)
	knownTools := len(toolSet) > 0
	knownFeatures := len(featureSet) > 0
	if !knownTools && !knownFeatures {
		return acpCapabilityCompatibilityUnknown
	}
	for _, name := range req.ToolNames {
		if _, ok := toolSet[name]; !ok {
			return acpCapabilityCompatibilityIncompatible
		}
	}
	for _, feature := range req.ContextVMFeatures {
		if !knownFeatures {
			return acpCapabilityCompatibilityUnknown
		}
		if _, ok := featureSet[strings.ToLower(strings.TrimSpace(feature))]; !ok {
			return acpCapabilityCompatibilityIncompatible
		}
	}
	return acpCapabilityCompatibilityCompatible
}

func describeACPTargetRequirements(req acpTargetRequirements) string {
	parts := make([]string, 0, len(req.ToolNames)+len(req.ContextVMFeatures))
	if len(req.ToolNames) > 0 {
		parts = append(parts, "tools: "+strings.Join(req.ToolNames, ", "))
	}
	if len(req.ContextVMFeatures) > 0 {
		parts = append(parts, "contextvm_features: "+strings.Join(req.ContextVMFeatures, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}

func betterACPTargetCandidate(a, b toolbuiltin.FleetEntry) bool {
	return betterACPTargetCandidateForConfigAndRequirements(a, b, state.ConfigDoc{}, acpTargetRequirements{})
}

func betterACPTargetCandidateForConfig(a, b toolbuiltin.FleetEntry, cfg state.ConfigDoc) bool {
	return betterACPTargetCandidateForConfigAndRequirements(a, b, cfg, acpTargetRequirements{})
}

func betterACPTargetCandidateForConfigAndRequirements(a, b toolbuiltin.FleetEntry, cfg state.ConfigDoc, req acpTargetRequirements) bool {
	peers := controlACPPeers
	aRegistered := peers != nil && peers.IsPeer(a.Pubkey)
	bRegistered := peers != nil && peers.IsPeer(b.Pubkey)
	if aRegistered != bRegistered {
		return aRegistered
	}
	aDM := fleetEntryDMCompatibilityForConfig(a, cfg)
	bDM := fleetEntryDMCompatibilityForConfig(b, cfg)
	if aDM != bDM {
		return aDM > bDM
	}
	aCaps := fleetEntryCapabilityCompatibility(a, req)
	bCaps := fleetEntryCapabilityCompatibility(b, req)
	if aCaps != bCaps {
		return aCaps > bCaps
	}
	aACP := a.ACPVersion > 0
	bACP := b.ACPVersion > 0
	if aACP != bACP {
		return aACP
	}
	if strings.TrimSpace(a.Name) != strings.TrimSpace(b.Name) {
		return strings.TrimSpace(a.Name) < strings.TrimSpace(b.Name)
	}
	return strings.ToLower(strings.TrimSpace(a.Pubkey)) < strings.ToLower(strings.TrimSpace(b.Pubkey))
}

func resolveACPFleetTarget(raw string) (string, string, error) {
	return resolveACPFleetTargetForConfigAndRequirements(raw, state.ConfigDoc{}, acpTargetRequirements{})
}

func resolveACPFleetTargetForConfig(raw string, cfg state.ConfigDoc) (string, string, error) {
	return resolveACPFleetTargetForConfigAndRequirements(raw, cfg, acpTargetRequirements{})
}

func resolveACPFleetTargetForConfigAndRequirements(raw string, cfg state.ConfigDoc, req acpTargetRequirements) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("ACP peer target is required")
	}
	entries := fleetDirectory()
	candidates := make([]toolbuiltin.FleetEntry, 0, len(entries))
	if pk, err := nostruntime.ParsePubKey(raw); err == nil {
		hex := pk.Hex()
		for _, entry := range entries {
			if strings.EqualFold(entry.Pubkey, hex) {
				candidates = append(candidates, entry)
			}
		}
		if len(candidates) == 0 {
			if controlACPPeers != nil && controlACPPeers.IsPeer(hex) {
				if len(hex) >= 12 {
					return hex, hex[:12] + "...", nil
				}
				return hex, hex, nil
			}
			return "", "", fmt.Errorf("ACP peer %q is not registered; register via acp.register first", raw)
		}
	} else {
		lowerRaw := strings.ToLower(raw)
		for _, entry := range entries {
			if strings.ToLower(strings.TrimSpace(entry.Name)) == lowerRaw {
				candidates = append(candidates, entry)
			}
		}
		if len(candidates) == 0 {
			return "", "", fmt.Errorf("ACP peer %q is not in the discovered fleet directory", raw)
		}
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if betterACPTargetCandidateForConfigAndRequirements(candidate, best, cfg, req) {
			best = candidate
		}
	}
	registeredSeen := false
	bestIncompatibleDM := toolbuiltin.FleetEntry{}
	bestIncompatibleDMSet := false
	bestIncompatibleCaps := toolbuiltin.FleetEntry{}
	bestIncompatibleCapsSet := false
	ordered := append([]toolbuiltin.FleetEntry{}, candidates...)
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if betterACPTargetCandidateForConfigAndRequirements(ordered[j], ordered[i], cfg, req) {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}
	for _, candidate := range ordered {
		if controlACPPeers == nil || !controlACPPeers.IsPeer(candidate.Pubkey) {
			continue
		}
		registeredSeen = true
		if fleetEntryDMCompatibilityForConfig(candidate, cfg) == acpDMCompatibilityIncompatible {
			if !bestIncompatibleDMSet {
				bestIncompatibleDM = candidate
				bestIncompatibleDMSet = true
			}
			continue
		}
		if fleetEntryCapabilityCompatibility(candidate, req) == acpCapabilityCompatibilityIncompatible {
			if !bestIncompatibleCapsSet {
				bestIncompatibleCaps = candidate
				bestIncompatibleCapsSet = true
			}
			continue
		}
		return candidate.Pubkey, fleetDisplayNameForACP(candidate), nil
	}
	if registeredSeen && bestIncompatibleDMSet {
		return "", "", fmt.Errorf("ACP peer %q does not advertise a compatible DM scheme", fleetDisplayNameForACP(bestIncompatibleDM))
	}
	if registeredSeen && bestIncompatibleCapsSet {
		details := describeACPTargetRequirements(req)
		if details == "" {
			details = "required tool surface"
		}
		return "", "", fmt.Errorf("ACP peer %q does not advertise %s", fleetDisplayNameForACP(bestIncompatibleCaps), details)
	}
	return "", "", fmt.Errorf("ACP peer %q is known in fleet discovery but not registered; register via acp.register first", fleetDisplayNameForACP(best))
}

func peerAdvertisedACPTransportModes(pubkey string) map[string]struct{} {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return nil
	}
	if pk, err := nostruntime.ParsePubKey(pubkey); err == nil {
		pubkey = pk.Hex()
	}
	if capabilityRegistry != nil {
		if cap, ok := capabilityRegistry.Get(pubkey); ok {
			if modes := normalizeACPAdvertisedSchemes(cap.DMSchemes); len(modes) > 0 {
				return modes
			}
		}
	}
	for _, entry := range fleetDirectory() {
		if strings.EqualFold(strings.TrimSpace(entry.Pubkey), pubkey) {
			return normalizeACPAdvertisedSchemes(entry.DMSchemes)
		}
	}
	return nil
}

func resolveACPDMTransport(cfg state.ConfigDoc, targetPubKey string) (nostruntime.DMTransport, string, error) {
	targetPubKey = strings.TrimSpace(targetPubKey)
	if targetPubKey == "" {
		return nil, "", fmt.Errorf("ACP peer target is required")
	}
	configured := cfg.ACPTransportMode()
	remote := peerAdvertisedACPTransportModes(targetPubKey)
	if configured != "auto" {
		if len(remote) > 0 {
			if _, ok := remote[configured]; !ok {
				return nil, "", fmt.Errorf("ACP peer %q does not advertise DM transport %s", acpTargetDisplayName(targetPubKey), configured)
			}
		}
		bus := currentACPTransportBus(configured)
		if bus == nil {
			return nil, "", fmt.Errorf("ACP transport %s is configured but no local %s DM transport is available", configured, configured)
		}
		return bus, configured, nil
	}
	// Auto mode: try FIPS first if the TransportSelector is available and
	// the peer advertises FIPS (or no capability info is available).
	if bus := currentACPTransportBus("fips"); bus != nil {
		if len(remote) == 0 {
			// No advertised schemes — TransportSelector handles fallback.
			return bus, "auto", nil
		}
		if _, ok := remote["fips"]; ok {
			return bus, "auto", nil
		}
	}
	if len(remote) > 0 {
		if _, ok := remote["nip17"]; ok {
			if bus := currentACPTransportBus("nip17"); bus != nil {
				return bus, "nip17", nil
			}
		}
		if _, ok := remote["nip04"]; ok {
			if bus := currentACPTransportBus("nip04"); bus != nil {
				return bus, "nip04", nil
			}
		}
		return nil, "", fmt.Errorf("ACP peer %q does not advertise a compatible DM scheme", acpTargetDisplayName(targetPubKey))
	}
	if bus := currentACPTransportBus("nip17"); bus != nil {
		return bus, "nip17", nil
	}
	return nil, "", fmt.Errorf("ACP peer %q does not advertise a compatible DM scheme", acpTargetDisplayName(targetPubKey))
}

func sendACPDMWithTransport(ctx context.Context, bus nostruntime.DMTransport, scheme string, toPubKey string, text string) error {
	if bus == nil {
		return fmt.Errorf("ACP DM transport not available")
	}
	if schemeBus, ok := bus.(nostruntime.DMSchemeTransport); ok && strings.TrimSpace(scheme) != "" {
		return schemeBus.SendDMWithScheme(ctx, toPubKey, text, scheme)
	}
	return bus.SendDM(ctx, toPubKey, text)
}
