package main

// nip51_allowlist.go — NIP-51 kind:30000 list-based DM allowlist + agent list sync.
//
// Features:
//   1. allow_from_lists: fetch + subscribe to kind:30000 lists; merge "p" tags
//      into dynamicAllowlist so those pubkeys can DM the agent.
//   2. agent_list: publish Strand's own kind:30000 list of known peers so other
//      agents can discover and trust it.
//   3. FLEET.md: written to the agent workspace after EOSE and on live updates
//      so the LLM can read it directly without a tool call.

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/nostr/nip51"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

// ── Dynamic allowlist ──────────────────────────────────────────────────────────

// nip51AllowlistMu guards nip51PerListPubkeys and nip51FleetEntries.
var nip51AllowlistMu sync.RWMutex

// nip51PerListPubkeys maps "ownerHex:dtag" → set of allowed pubkeys.
// When a list is updated the entire inner set is replaced atomically.
var nip51PerListPubkeys = make(map[string]map[string]struct{})

// nip51PerListEntries keeps the full fleet-entry view for each watched list.
// The merged fleet directory is rebuilt from this map so removals do not leave
// stale peers behind.
var nip51PerListEntries = make(map[string]map[string]toolbuiltin.FleetEntry)

// nip51FleetEntries holds the merged fleet directory entries (pubkey, name,
// relay) from all watched NIP-51 lists, keyed by hex pubkey.
var nip51FleetEntries = make(map[string]toolbuiltin.FleetEntry)

// fleetWorkspaceDir is the current workspace location where FLEET.md is kept.
var (
	fleetWorkspaceDirMu sync.RWMutex
	fleetWorkspaceDir   string
)

// fleetMarkdownMu serializes FLEET.md rewrites across NIP-51 and capability updates.
var fleetMarkdownMu sync.Mutex

func setFleetWorkspaceDir(wsDir string) {
	fleetWorkspaceDirMu.Lock()
	fleetWorkspaceDir = wsDir
	fleetWorkspaceDirMu.Unlock()
}

func getFleetWorkspaceDir() string {
	fleetWorkspaceDirMu.RLock()
	defer fleetWorkspaceDirMu.RUnlock()
	return fleetWorkspaceDir
}

// setNIP51ListEntries atomically replaces the pubkey set and fleet entries
// for a single list entry. entries must be the full ListEntry slice from the list.
func setNIP51ListEntries(ownerHex, dtag string, entries []nip51.ListEntry) {
	key := ownerHex + ":" + dtag
	pubkeys := make(map[string]struct{}, len(entries))
	fleetEntries := make(map[string]toolbuiltin.FleetEntry, len(entries))

	// Collect list-level FIPS metadata from non-p tags.
	// Tags like ["fips", "true"] and ["fips_transport", "udp:2121"] apply to
	// the preceding "p" entry, or to all entries if they appear before any "p".
	listFIPSEnabled := false
	listFIPSTransport := ""
	for _, e := range entries {
		switch e.Tag {
		case "fips":
			if strings.EqualFold(strings.TrimSpace(e.Value), "true") {
				listFIPSEnabled = true
			}
		case "fips_transport":
			if v := strings.TrimSpace(e.Value); v != "" {
				listFIPSTransport = v
			}
		}
	}

	nip51AllowlistMu.Lock()
	for _, e := range entries {
		if e.Tag != "p" || e.Value == "" {
			continue
		}
		pubkeys[e.Value] = struct{}{}
		entry := toolbuiltin.FleetEntry{
			Pubkey:        e.Value,
			Relay:         e.Relay,
			Name:          e.Petname,
			FIPSEnabled:   listFIPSEnabled,
			FIPSTransport: listFIPSTransport,
		}
		fleetEntries[e.Value] = entry
	}
	nip51PerListPubkeys[key] = pubkeys
	nip51PerListEntries[key] = fleetEntries
	rebuildFleetEntriesLocked()
	nip51AllowlistMu.Unlock()
	refreshCapabilityPeerWatch()
}

// setNIP51ListPubkeys atomically replaces the pubkey set for a single list entry.
// Kept for backward compatibility; new callers should use setNIP51ListEntries.
func setNIP51ListPubkeys(ownerHex, dtag string, pubkeys []string) {
	key := ownerHex + ":" + dtag
	pubkeySet := make(map[string]struct{}, len(pubkeys))
	fleetEntries := make(map[string]toolbuiltin.FleetEntry, len(pubkeys))
	for _, pk := range pubkeys {
		if pk == "" {
			continue
		}
		pubkeySet[pk] = struct{}{}
		fleetEntries[pk] = toolbuiltin.FleetEntry{Pubkey: pk}
	}
	nip51AllowlistMu.Lock()
	nip51PerListPubkeys[key] = pubkeySet
	nip51PerListEntries[key] = fleetEntries
	rebuildFleetEntriesLocked()
	nip51AllowlistMu.Unlock()
	refreshCapabilityPeerWatch()
}

// fleetDirectory returns a snapshot of all known fleet agents.
// Called by the fleet_agents and nostr_agent_rpc tools.
func fleetDirectory() []toolbuiltin.FleetEntry {
	nip51AllowlistMu.RLock()
	defer nip51AllowlistMu.RUnlock()
	out := make([]toolbuiltin.FleetEntry, 0, len(nip51FleetEntries))
	for _, e := range nip51FleetEntries {
		merged := e
		if capabilityRegistry != nil {
			if cap, ok := capabilityRegistry.Get(e.Pubkey); ok {
				merged.Runtime = cap.Runtime
				merged.RuntimeVersion = cap.RuntimeVersion
				merged.DMSchemes = append([]string{}, cap.DMSchemes...)
				merged.ACPVersion = cap.ACPVersion
				merged.Tools = append([]string{}, cap.Tools...)
				merged.ContextVMFeatures = append([]string{}, cap.ContextVMFeatures...)
				merged.Relays = append([]string{}, cap.Relays...)
				if merged.Relay == "" && len(cap.Relays) > 0 {
					merged.Relay = cap.Relays[0]
				}
				// Merge FIPS capability if advertised via kind:30317.
				if cap.FIPSEnabled {
					merged.FIPSEnabled = true
					if merged.FIPSTransport == "" && cap.FIPSTransport != "" {
						merged.FIPSTransport = cap.FIPSTransport
					}
				}
			}
		}
		// Derive FIPS IPv6 address for FIPS-enabled agents.
		if merged.FIPSEnabled && merged.FIPSIPv6Addr == "" {
			if addr, err := nostruntime.FIPSIPv6FromPubkey(merged.Pubkey); err == nil {
				merged.FIPSIPv6Addr = addr.String()
			}
		}
		// Ensure "fips" appears in DM schemes for FIPS-enabled agents.
		if merged.FIPSEnabled {
			hasFIPS := false
			for _, s := range merged.DMSchemes {
				if s == "fips" {
					hasFIPS = true
					break
				}
			}
			if !hasFIPS {
				merged.DMSchemes = append(merged.DMSchemes, "fips")
			}
		}
		out = append(out, merged)
	}
	return out
}

func rebuildFleetEntriesLocked() {
	merged := make(map[string]toolbuiltin.FleetEntry)
	keys := make([]string, 0, len(nip51PerListEntries))
	for key := range nip51PerListEntries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entries := nip51PerListEntries[key]
		for pubkey, entry := range entries {
			current, ok := merged[pubkey]
			if !ok {
				merged[pubkey] = entry
				continue
			}
			if current.Name == "" && entry.Name != "" {
				current.Name = entry.Name
			}
			if current.Relay == "" && entry.Relay != "" {
				current.Relay = entry.Relay
			}
			// Merge FIPS: any list advertising FIPS for this peer wins.
			if entry.FIPSEnabled {
				current.FIPSEnabled = true
			}
			if current.FIPSTransport == "" && entry.FIPSTransport != "" {
				current.FIPSTransport = entry.FIPSTransport
			}
			merged[pubkey] = current
		}
	}
	nip51FleetEntries = merged
}

func fleetPeerPubkeys() []string {
	nip51AllowlistMu.RLock()
	defer nip51AllowlistMu.RUnlock()
	out := make([]string, 0, len(nip51FleetEntries))
	for pubkey := range nip51FleetEntries {
		out = append(out, pubkey)
	}
	sort.Strings(out)
	return out
}

func refreshCapabilityPeerWatch() {
	if capabilityMonitor == nil {
		return
	}
	capabilityMonitor.UpdatePeers(fleetPeerPubkeys())
	cfg := state.ConfigDoc{}
	if controlServices != nil && controlServices.runtimeConfig != nil {
		cfg = controlServices.runtimeConfig.Get()
	}
	capabilityMonitor.UpdateSubscribeRelays(currentCapabilitySubscriptionRelays(cfg))
}

// isInDynamicAllowlist returns true if rawPubkey (hex or npub) appears in any
// of the fetched NIP-51 lists.
func isInDynamicAllowlist(rawPubkey string) bool {
	pk, err := nostruntime.ParsePubKey(rawPubkey)
	if err != nil {
		return false
	}
	hexPK := pk.Hex()
	nip51AllowlistMu.RLock()
	defer nip51AllowlistMu.RUnlock()
	for _, m := range nip51PerListPubkeys {
		if _, ok := m[hexPK]; ok {
			return true
		}
	}
	return false
}

// pubkeysFromList extracts "p" tag values from a decoded NIP-51 list.
func pubkeysFromList(list *nip51.List) []string {
	var out []string
	for _, e := range list.Entries {
		if e.Tag == "p" && e.Value != "" {
			out = append(out, e.Value)
		}
	}
	return out
}

// ── Fleet markdown writer ──────────────────────────────────────────────────────

// writeFleetMD writes a FLEET.md snapshot to the agent workspace directory.
// Called after EOSE and on every live list update so the LLM can read it
// directly without needing a fleet_agents tool call.
func writeFleetMD(wsDir string) {
	fleetMarkdownMu.Lock()
	defer fleetMarkdownMu.Unlock()
	if wsDir == "" {
		log.Printf("nip51: writeFleetMD: empty workspace dir, skipping")
		return
	}

	entries := fleetDirectory()

	// Sort by name for stable output.
	sort.Slice(entries, func(i, j int) bool {
		ni := strings.ToLower(entries[i].Name)
		nj := strings.ToLower(entries[j].Name)
		if ni == nj {
			return entries[i].Pubkey < entries[j].Pubkey
		}
		return ni < nj
	})

	var sb strings.Builder
	sb.WriteString("# FLEET.md — Cascadia Agent Roster\n")
	sb.WriteString(fmt.Sprintf("_Synced from NIP-51 fleet lists + kind:30317 capability events · %s_\n\n", time.Now().UTC().Format("2006-01-02 15:04 UTC")))
	sb.WriteString(fmt.Sprintf("Fleet size: %d agents\n\n", len(entries)))

	for _, e := range entries {
		name := e.Name
		if name == "" {
			name = e.Pubkey
		}
		sb.WriteString(fmt.Sprintf("## %s\n", name))
		sb.WriteString(fmt.Sprintf("- **pubkey:** `%s`\n", e.Pubkey))
		if strings.TrimSpace(e.Relay) != "" {
			sb.WriteString(fmt.Sprintf("- **relay:** %s\n", e.Relay))
		}
		if e.Runtime != "" {
			if e.RuntimeVersion != "" {
				sb.WriteString(fmt.Sprintf("- **runtime:** %s %s\n", e.Runtime, e.RuntimeVersion))
			} else {
				sb.WriteString(fmt.Sprintf("- **runtime:** %s\n", e.Runtime))
			}
		}
		if e.ACPVersion > 0 {
			sb.WriteString(fmt.Sprintf("- **acp_version:** %d\n", e.ACPVersion))
		}
		if len(e.DMSchemes) > 0 {
			sb.WriteString(fmt.Sprintf("- **dm_schemes:** %s\n", strings.Join(e.DMSchemes, ", ")))
		}
		if len(e.Relays) > 0 {
			sb.WriteString(fmt.Sprintf("- **relays:** %s\n", strings.Join(e.Relays, ", ")))
		}
		if len(e.Tools) > 0 {
			sb.WriteString(fmt.Sprintf("- **tools (%d):** %s\n", len(e.Tools), strings.Join(e.Tools, ", ")))
		}
		if len(e.ContextVMFeatures) > 0 {
			sb.WriteString(fmt.Sprintf("- **contextvm_features:** %s\n", strings.Join(e.ContextVMFeatures, ", ")))
		}
		if e.FIPSEnabled {
			sb.WriteString("- **fips:** ✅ enabled\n")
			if e.FIPSIPv6Addr != "" {
				sb.WriteString(fmt.Sprintf("- **fips_addr:** `%s`\n", e.FIPSIPv6Addr))
			}
			if e.FIPSTransport != "" {
				sb.WriteString(fmt.Sprintf("- **fips_transport:** %s\n", e.FIPSTransport))
			}
		}
		sb.WriteString("\n")
	}

	dest := filepath.Join(wsDir, "FLEET.md")
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0644); err != nil {
		log.Printf("nip51: writeFleetMD: write %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, dest); err != nil {
		log.Printf("nip51: writeFleetMD: rename to %s: %v", dest, err)
		return
	}
	log.Printf("nip51: wrote FLEET.md (%d agents) → %s", len(entries), dest)
}

// ── Watcher / subscription ─────────────────────────────────────────────────────

// startNIP51AllowlistWatcher starts goroutines for each allow_from_lists entry:
//   - Fetch the current list contents immediately.
//   - Subscribe to real-time replaceable updates.
//
// The function returns quickly; goroutines run until ctx is cancelled.
func fleetWorkspaceDirFromConfig(cfg state.ConfigDoc) string {
	return workspace.ResolveWorkspaceDir(cfg, "")
}

func startNIP51AllowlistWatcher(ctx context.Context, pool *nostr.Pool, cfg state.ConfigDoc) {
	wsDir := fleetWorkspaceDirFromConfig(cfg)
	setFleetWorkspaceDir(wsDir)
	if len(cfg.DM.AllowFromLists) == 0 {
		return
	}

	for _, ref := range cfg.DM.AllowFromLists {
		ref := ref // capture loop var
		go watchNIP51List(ctx, pool, ref, cfg.Relays.Read, wsDir)
	}
}

// nip51EOSEReady is closed once the initial NIP-51 EOSE has been received,
// signalling that the fleet directory has been populated from stored events.
// Callers that need the directory to be ready can select on this channel.
var nip51EOSEReady = make(chan struct{})
var nip51EOSEOnce sync.Once

func watchNIP51List(ctx context.Context, pool *nostr.Pool, ref state.AllowFromListRef, fallbackRelays []string, wsDir string) {
	ownerPK, err := nostruntime.ParsePubKey(ref.Pubkey)
	if err != nil {
		log.Printf("nip51: invalid pubkey for list %q: %v", ref.D, err)
		return
	}
	ownerHex := ownerPK.Hex()
	logPrefix := ownerHex
	if len(logPrefix) > 12 {
		logPrefix = logPrefix[:12]
	}

	relays := buildRelayList(ref.Relay, fallbackRelays)

	filter := nostr.Filter{
		Kinds:   []nostr.Kind{nostr.Kind(nip51.KindPeopleList)},
		Authors: []nostr.PubKey{ownerPK},
		Tags:    nostr.TagMap{"d": []string{ref.D}},
	}

	// SubscribeManyNotifyEOSE: single open subscription; eoseChan is closed
	// when all relays have sent EOSE (stored events complete). The event channel
	// stays open for live replaceable-event updates — no timeout needed.
	events, eoseChan := pool.SubscribeManyNotifyEOSE(ctx, relays, filter, nostr.SubscriptionOptions{})

	eoseSignalled := false
	for {
		select {
		case re, ok := <-events:
			if !ok {
				return // context cancelled, subscription done
			}
			decoded := nip51.DecodeEvent(re.Event)
			if decoded.DTag != ref.D {
				continue
			}
			setNIP51ListEntries(ownerHex, ref.D, decoded.Entries)
			if eoseSignalled {
				log.Printf("nip51: live update list %q: %d pubkeys (owner=%s)", ref.D, len(pubkeysFromList(decoded)), logPrefix)
				writeFleetMD(wsDir)
			} else {
				log.Printf("nip51: loaded %d pubkeys from %q (owner=%s)", len(pubkeysFromList(decoded)), ref.D, logPrefix)
			}

		case <-eoseChan:
			eoseChan = nil // prevent busy-loop: closed channel returns immediately
			eoseSignalled = true
			log.Printf("nip51: EOSE received for list %q (owner=%s) — fleet directory ready", ref.D, logPrefix)
			nip51EOSEOnce.Do(func() { close(nip51EOSEReady) })
			writeFleetMD(wsDir)
		case <-ctx.Done():
			return
		}
	}
}

// buildRelayList builds a relay slice with hintRelay first, falling back to defaults.
func buildRelayList(hintRelay string, defaults []string) []string {
	if hintRelay == "" {
		return defaults
	}
	// Prepend hint without duplicating.
	out := []string{hintRelay}
	for _, r := range defaults {
		if r != hintRelay {
			out = append(out, r)
		}
	}
	return out
}

// ── Agent list sync ────────────────────────────────────────────────────────────

// syncAgentList fetches Strand's own kind:30000 list (identified by cfg.AgentList.DTag),
// merges it with the current static + dynamic allowlist, and publishes an updated
// event if anything changed.  Called once at startup when auto_sync is true.
func syncAgentList(ctx context.Context, pool *nostr.Pool, cfg state.ConfigDoc) {
	alCfg := cfg.AgentList
	if alCfg == nil || !alCfg.AutoSync || alCfg.DTag == "" {
		return
	}

	// Get Strand's own pubkey.
	pkCtx, pkCancel := context.WithTimeout(ctx, 10*time.Second)
	strandPK, err := controlServices.relay.keyer.GetPublicKey(pkCtx)
	pkCancel()
	if err != nil {
		log.Printf("nip51: agent_list sync: get public key: %v", err)
		return
	}
	strandHex := strandPK.Hex()
	logPrefix := strandHex
	if len(logPrefix) > 12 {
		logPrefix = logPrefix[:12]
	}

	relays := buildRelayList(alCfg.Relay, cfg.Relays.Read)
	writeRelays := buildRelayList(alCfg.Relay, cfg.Relays.Write)
	if len(writeRelays) == 0 {
		writeRelays = relays
	}

	// Fetch existing list (may not exist).
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	existing, fetchErr := nip51.Fetch(fetchCtx, pool, relays, strandHex, nip51.KindPeopleList, alCfg.DTag)
	cancel()

	var existingEntries []nip51.ListEntry
	if fetchErr == nil && existing != nil {
		existingEntries = existing.Entries
	}

	// Build desired pubkey set: static allow_from + dynamic (NIP-51 lists).
	desired := make(map[string]nip51.ListEntry)

	// Add static allow_from entries.
	for _, raw := range cfg.DM.AllowFrom {
		pk, pkErr := nostruntime.ParsePubKey(raw)
		if pkErr != nil {
			continue
		}
		hex := pk.Hex()
		if _, exists := desired[hex]; !exists {
			desired[hex] = nip51.ListEntry{Tag: "p", Value: hex}
		}
	}

	// Add pubkeys from dynamic allowlist.
	nip51AllowlistMu.RLock()
	for _, m := range nip51PerListPubkeys {
		for pk := range m {
			if _, exists := desired[pk]; !exists {
				desired[pk] = nip51.ListEntry{Tag: "p", Value: pk}
			}
		}
	}
	nip51AllowlistMu.RUnlock()

	// Build existing pubkey set (preserving relay hints and petnames).
	existingPKMap := make(map[string]nip51.ListEntry)
	for _, e := range existingEntries {
		if e.Tag == "p" && e.Value != "" {
			existingPKMap[e.Value] = e
		}
	}

	// Merge: keep existing entries (to preserve hints/petnames), add new ones.
	merged := make(map[string]nip51.ListEntry)
	for pk, entry := range existingPKMap {
		merged[pk] = entry
	}
	for pk, entry := range desired {
		if _, exists := merged[pk]; !exists {
			merged[pk] = entry
		}
	}

	// Check if an update is needed.
	if len(merged) == len(existingPKMap) {
		allPresent := true
		for pk := range desired {
			if _, ok := existingPKMap[pk]; !ok {
				allPresent = false
				break
			}
		}
		if allPresent {
			log.Printf("nip51: agent_list %q already up-to-date (%d entries)", alCfg.DTag, len(merged))
			return
		}
	}

	// Build the list to publish.
	newList := &nip51.List{
		Kind:   nip51.KindPeopleList,
		DTag:   alCfg.DTag,
		PubKey: strandHex,
	}
	// Preserve non-p tags from existing list (e.g. "alt", "title").
	for _, e := range existingEntries {
		if e.Tag != "p" {
			newList.Entries = append(newList.Entries, e)
		}
	}
	// Add merged "p" entries.
	for _, entry := range merged {
		newList.Entries = append(newList.Entries, entry)
	}

	publishCtx, publishCancel := context.WithTimeout(ctx, 20*time.Second)
	eventID, publishErr := nip51.Publish(publishCtx, pool, controlServices.relay.keyer, writeRelays, newList)
	publishCancel()

	if publishErr != nil {
		log.Printf("nip51: agent_list publish %q: %v", alCfg.DTag, publishErr)
		return
	}
	log.Printf("nip51: agent_list %q published event=%s (%d p-entries)", alCfg.DTag, eventID, len(merged))
}
