package main

import (
	"context"
	"fmt"
	"strings"

	"metiq/internal/agent/toolbuiltin"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const (
	acpDMCompatibilityIncompatible = iota
	acpDMCompatibilityUnknown
	acpDMCompatibilityCompatible
)

func normalizeACPAdvertisedScheme(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "nip04", "nip-04":
		return "nip04"
	case "giftwrap", "nip17", "nip-17", "nip44", "nip-44", "nip59", "nip-59":
		return "nip17"
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
	mode, ok := state.ParseACPTransportMode(mode)
	if !ok || mode == "auto" {
		return nil
	}
	controlDMBusMu.RLock()
	defer controlDMBusMu.RUnlock()
	switch mode {
	case "nip17":
		if controlNIP17Bus != nil {
			return controlNIP17Bus
		}
		if _, ok := controlDMBus.(*nostruntime.NIP17Bus); ok {
			return controlDMBus
		}
	case "nip04":
		if controlNIP04Bus != nil {
			return controlNIP04Bus
		}
		if _, ok := controlDMBus.(*nostruntime.DMBus); ok {
			return controlDMBus
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
	default:
		if currentACPTransportBus("nip17") != nil {
			out["nip17"] = struct{}{}
		}
		if currentACPTransportBus("nip04") != nil {
			out["nip04"] = struct{}{}
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

func betterACPTargetCandidate(a, b toolbuiltin.FleetEntry) bool {
	return betterACPTargetCandidateForConfig(a, b, state.ConfigDoc{})
}

func betterACPTargetCandidateForConfig(a, b toolbuiltin.FleetEntry, cfg state.ConfigDoc) bool {
	aRegistered := controlACPPeers != nil && controlACPPeers.IsPeer(a.Pubkey)
	bRegistered := controlACPPeers != nil && controlACPPeers.IsPeer(b.Pubkey)
	if aRegistered != bRegistered {
		return aRegistered
	}
	aDM := fleetEntryDMCompatibilityForConfig(a, cfg)
	bDM := fleetEntryDMCompatibilityForConfig(b, cfg)
	if aDM != bDM {
		return aDM > bDM
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
	return resolveACPFleetTargetForConfig(raw, state.ConfigDoc{})
}

func resolveACPFleetTargetForConfig(raw string, cfg state.ConfigDoc) (string, string, error) {
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
		if betterACPTargetCandidateForConfig(candidate, best, cfg) {
			best = candidate
		}
	}
	registeredSeen := false
	bestIncompatible := toolbuiltin.FleetEntry{}
	bestIncompatibleSet := false
	ordered := append([]toolbuiltin.FleetEntry{}, candidates...)
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if betterACPTargetCandidateForConfig(ordered[j], ordered[i], cfg) {
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
			if !bestIncompatibleSet {
				bestIncompatible = candidate
				bestIncompatibleSet = true
			}
			continue
		}
		return candidate.Pubkey, fleetDisplayNameForACP(candidate), nil
	}
	if registeredSeen && bestIncompatibleSet {
		return "", "", fmt.Errorf("ACP peer %q does not advertise a compatible DM scheme", fleetDisplayNameForACP(bestIncompatible))
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
	if bus := currentACPTransportBus("nip04"); bus != nil {
		return bus, "nip04", nil
	}
	if bus := currentACPTransportBus("nip17"); bus != nil {
		return bus, "nip17", nil
	}
	return nil, "", fmt.Errorf("ACP DM transport not available")
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
