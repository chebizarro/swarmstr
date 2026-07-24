package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"metiq/internal/store/state"
)

// AgentRuntimeFingerprint returns a stable fingerprint of everything that can
// affect a provisioned agent runtime: the agent's own config plus the
// providers table (credentials, base URLs, and per-provider model overrides
// are resolved into the runtime at build time).
//
// Hot-reload uses this to decide which agents need their runtime rebuilt
// (swarmstr-v2uq): identical fingerprints mean the existing runtime is still
// valid; any difference requires reprovisioning.
func AgentRuntimeFingerprint(cfg state.ConfigDoc, ag state.AgentConfig) string {
	hasher := sha256.New()
	if raw, err := json.Marshal(ag); err == nil {
		_, _ = hasher.Write(raw)
	}
	hasher.Write([]byte{0})
	// json.Marshal sorts map keys, so the providers table serializes
	// deterministically.
	if raw, err := json.Marshal(cfg.Providers); err == nil {
		_, _ = hasher.Write(raw)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// ChangedAgentRuntimes compares two config snapshots and reports which agents
// need their runtime rebuilt after a hot-reload. Changed agents (new or with
// a differing runtime fingerprint) are returned in new-config order; removed
// holds the IDs of agents that disappeared from the config.
func ChangedAgentRuntimes(oldCfg, newCfg state.ConfigDoc) (changed []state.AgentConfig, removed []string) {
	oldPrints := make(map[string]string, len(oldCfg.Agents))
	for _, ag := range oldCfg.Agents {
		id := strings.TrimSpace(ag.ID)
		if id == "" {
			continue
		}
		oldPrints[id] = AgentRuntimeFingerprint(oldCfg, ag)
	}
	seen := make(map[string]struct{}, len(newCfg.Agents))
	for _, ag := range newCfg.Agents {
		id := strings.TrimSpace(ag.ID)
		if id == "" {
			continue
		}
		seen[id] = struct{}{}
		if oldPrints[id] != AgentRuntimeFingerprint(newCfg, ag) {
			changed = append(changed, ag)
		}
	}
	for _, ag := range oldCfg.Agents {
		id := strings.TrimSpace(ag.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; !ok {
			removed = append(removed, id)
		}
	}
	return changed, removed
}
