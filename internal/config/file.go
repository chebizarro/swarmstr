// Package config provides config file I/O for Metiq.
// It supports reading OpenClaw-compatible JSON5/YAML config files
// and mapping them to the Metiq ConfigDoc format.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
	"metiq/internal/store/state"
)

// LoadConfigFile reads a JSON5 (or plain JSON) or YAML config file
// and returns a ConfigDoc. OpenClaw config fields are mapped automatically.
func LoadConfigFile(path string) (state.ConfigDoc, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return state.ConfigDoc{}, fmt.Errorf("config file path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return state.ConfigDoc{}, fmt.Errorf("read config file: %w", err)
	}
	var obj map[string]any
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &obj); err != nil {
			return state.ConfigDoc{}, fmt.Errorf("parse YAML config: %w", err)
		}
	default:
		// JSON5 / HuJSON: standardise to plain JSON first.
		standardised, err := hujson.Standardize(raw)
		if err != nil {
			return state.ConfigDoc{}, fmt.Errorf("parse JSON5 config: %w", err)
		}
		if err := json.Unmarshal(standardised, &obj); err != nil {
			return state.ConfigDoc{}, fmt.Errorf("parse JSON config: %w", err)
		}
	}
	if obj == nil {
		return state.ConfigDoc{}, fmt.Errorf("config file is empty or null")
	}
	return mapRawToConfigDoc(obj), nil
}

// WriteConfigFile serialises a ConfigDoc to a JSON file at path.
// Parent directories are created as needed.
func WriteConfigFile(path string, doc state.ConfigDoc) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("config file path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir for config file: %w", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create config temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync config temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename config temp file: %w", err)
	}
	cleanup = false
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync config directory: %w", err)
	}
	return nil
}

// ParseConfigBytes parses raw bytes (JSON5, plain JSON, or YAML if ext hint
// contains ".yaml"/".yml") and returns a ConfigDoc.  ext is optional; an
// empty string defaults to JSON5 parsing.
func ParseConfigBytes(raw []byte, extHint string) (state.ConfigDoc, error) {
	extHint = strings.ToLower(strings.TrimSpace(extHint))
	ext := filepath.Ext(extHint)
	var obj map[string]any
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &obj); err != nil {
			return state.ConfigDoc{}, fmt.Errorf("parse YAML config: %w", err)
		}
	default:
		standardised, err := hujson.Standardize(raw)
		if err != nil {
			return state.ConfigDoc{}, fmt.Errorf("parse JSON5 config: %w", err)
		}
		if err := json.Unmarshal(standardised, &obj); err != nil {
			return state.ConfigDoc{}, fmt.Errorf("parse JSON config: %w", err)
		}
	}
	if obj == nil {
		return state.ConfigDoc{}, fmt.Errorf("config is empty or null")
	}
	return mapRawToConfigDoc(obj), nil
}

// DefaultConfigPath returns ~/.metiq/config.json.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".metiq", "config.json"), nil
}

// ConfigFileExists returns true when a readable config file exists at path.
func ConfigFileExists(path string) bool {
	info, err := os.Stat(strings.TrimSpace(path))
	return err == nil && !info.IsDir()
}

// ConfigFileModTime returns the modification time of path, or zero on error.
func ConfigFileModTime(path string) time.Time {
	info, err := os.Stat(strings.TrimSpace(path))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// ──────────────────────────────────────────────────────────────────────────────
// OpenClaw → ConfigDoc field mapping
// ──────────────────────────────────────────────────────────────────────────────

// mapRawToConfigDoc converts a raw parsed map (from JSON5 or YAML) to a
// ConfigDoc, mapping known OpenClaw config fields to their Metiq equivalents.
// Unknown top-level sections are preserved in ConfigDoc.Extra.
func mapRawToConfigDoc(raw map[string]any) state.ConfigDoc {
	doc := state.ConfigDoc{Version: 1}
	if doc.Extra == nil {
		doc.Extra = map[string]any{}
	}

	// ── relays ────────────────────────────────────────────────────────────────
	if relaysRaw, ok := raw["relays"]; ok {
		if rm, ok := relaysRaw.(map[string]any); ok {
			doc.Relays.Read = toStringSlice(rm["read"])
			doc.Relays.Write = toStringSlice(rm["write"])
		}
	}

	// ── native Metiq DM policy (written by WriteConfigFile) ───────────────
	// When the file was produced by WriteConfigFile it has a top-level "dm"
	// key that maps directly onto DMPolicy's JSON fields.
	if dmRaw, ok := raw["dm"].(map[string]any); ok {
		if policy, ok := dmRaw["policy"].(string); ok && strings.TrimSpace(policy) != "" {
			doc.DM.Policy = strings.TrimSpace(policy)
		}
		if doc.DM.AllowFrom == nil {
			doc.DM.AllowFrom = toStringSlice(dmRaw["allow_from"])
		}
		// allow_from_lists: NIP-51 kind:30000 list references for dynamic allowlist.
		if listsRaw, ok := dmRaw["allow_from_lists"].([]any); ok {
			for _, item := range listsRaw {
				if m, ok := item.(map[string]any); ok {
					ref := state.AllowFromListRef{}
					if v, ok := m["pubkey"].(string); ok {
						ref.Pubkey = strings.TrimSpace(v)
					}
					if v, ok := m["d"].(string); ok {
						ref.D = strings.TrimSpace(v)
					}
					if v, ok := m["relay"].(string); ok {
						ref.Relay = strings.TrimSpace(v)
					}
					if ref.Pubkey != "" && ref.D != "" {
						doc.DM.AllowFromLists = append(doc.DM.AllowFromLists, ref)
					}
				}
			}
		}
	}

	// ── agent_list (NIP-51 kind:30000 agent list sync) ────────────────────────
	if alRaw, ok := raw["agent_list"].(map[string]any); ok {
		al := &state.AgentListConfig{}
		if v, ok := alRaw["d"].(string); ok {
			al.DTag = strings.TrimSpace(v)
		}
		if v, ok := alRaw["relay"].(string); ok {
			al.Relay = strings.TrimSpace(v)
		}
		if v, ok := alRaw["auto_sync"].(bool); ok {
			al.AutoSync = v
		}
		if al.DTag != "" {
			doc.AgentList = al
		}
	}

	// ── native Metiq agent policy ──────────────────────────────────────────
	if agentRaw, ok := raw["agent"].(map[string]any); ok {
		if model, ok := agentRaw["default_model"].(string); ok && model != "" {
			if doc.Agent.DefaultModel == "" {
				doc.Agent.DefaultModel = strings.TrimSpace(model)
			}
		}
	}

	// ── native extra pass-through ─────────────────────────────────────────────
	if extraRaw, ok := raw["extra"].(map[string]any); ok {
		for k, v := range extraRaw {
			if _, exists := doc.Extra[k]; !exists {
				doc.Extra[k] = v
			}
		}
	}

	// ── DM policy (OpenClaw: channels.web.dm) ────────────────────────────────
	if channelsRaw, ok := raw["channels"].(map[string]any); ok {
		if webRaw, ok := channelsRaw["web"].(map[string]any); ok {
			if dmRaw, ok := webRaw["dm"].(map[string]any); ok {
				if policy, ok := dmRaw["policy"].(string); ok {
					doc.DM.Policy = strings.TrimSpace(policy)
				}
				doc.DM.AllowFrom = toStringSlice(dmRaw["allow_from"])
				if doc.DM.AllowFrom == nil {
					doc.DM.AllowFrom = toStringSlice(dmRaw["allowFrom"])
				}
			}
		}
		// Preserve full channels map for Nostr channel configs (NIP-17/NIP-29 etc).
		doc.Extra["channels"] = channelsRaw
	}

	// ── agent / provider defaults ─────────────────────────────────────────────
	switch agentsRaw := raw["agents"].(type) {
	case map[string]any:
		// OpenClaw schema: {"agents": {"defaults": {"model": "..."}, "list": [...]}}
		if defaults, ok := agentsRaw["defaults"].(map[string]any); ok {
			if model, ok := defaults["model"].(string); ok {
				doc.Agent.DefaultModel = strings.TrimSpace(model)
			}
		}
		// Parse typed agent list if present under "list" key.
		if list, ok := agentsRaw["list"].([]any); ok {
			doc.Agents = parseAgentConfigList(list)
		}
		doc.Extra["agents"] = agentsRaw
	case []any:
		// Metiq-native typed format: agents is directly an array.
		doc.Agents = parseAgentConfigList(agentsRaw)
	}

	// ── plugins (map to extensions in extra, matching existing Metiq key) ──
	if pluginsRaw, ok := raw["plugins"]; ok {
		doc.Extra["extensions"] = pluginsRaw
	}

	// ── providers (typed) ────────────────────────────────────────────────────
	if providersRaw, ok := raw["providers"].(map[string]any); ok {
		doc.Providers = make(state.ProvidersConfig, len(providersRaw))
		for name, val := range providersRaw {
			if pm, ok := val.(map[string]any); ok {
				entry := state.ProviderEntry{}
				if v, ok := pm["enabled"].(bool); ok {
					entry.Enabled = v
				}
				if v, ok := pm["api_key"].(string); ok {
					entry.APIKey = strings.TrimSpace(v)
				} else if v, ok := pm["apiKey"].(string); ok {
					entry.APIKey = strings.TrimSpace(v)
				}
				if v, ok := pm["base_url"].(string); ok {
					entry.BaseURL = strings.TrimSpace(v)
				} else if v, ok := pm["baseUrl"].(string); ok {
					entry.BaseURL = strings.TrimSpace(v)
				}
				if v, ok := pm["model"].(string); ok {
					entry.Model = strings.TrimSpace(v)
				}
				if extraRaw, ok := pm["extra"].(map[string]any); ok {
					entry.Extra = make(map[string]any, len(extraRaw))
					for key, value := range extraRaw {
						entry.Extra[key] = value
					}
				}
				for key, value := range pm {
					switch key {
					case "enabled", "api_key", "apiKey", "base_url", "baseUrl", "model", "extra":
						continue
					}
					if entry.Extra == nil {
						entry.Extra = map[string]any{}
					}
					entry.Extra[key] = value
				}
				doc.Providers[name] = entry
			}
		}
	}

	// ── nostr_channels (typed) ───────────────────────────────────────────────
	if ncRaw, ok := raw["nostr_channels"].(map[string]any); ok {
		doc.NostrChannels = make(state.NostrChannelsConfig, len(ncRaw))
		for name, val := range ncRaw {
			if cm, ok := val.(map[string]any); ok {
				ch := state.NostrChannelConfig{}
				if v, ok := cm["kind"].(string); ok {
					ch.Kind = strings.TrimSpace(v)
				}
				if v, ok := cm["enabled"].(bool); ok {
					ch.Enabled = v
				}
				if v, ok := cm["group_address"].(string); ok {
					ch.GroupAddress = strings.TrimSpace(v)
				}
				if v, ok := cm["channel_id"].(string); ok {
					ch.ChannelID = strings.TrimSpace(v)
				}
				if v, ok := cm["relays"]; ok {
					ch.Relays = toStringSlice(v)
				}
				if v, ok := cm["agent_id"].(string); ok {
					ch.AgentID = strings.TrimSpace(v)
				}
				if tags, ok := cm["tags"].(map[string]any); ok {
					ch.Tags = make(map[string][]string, len(tags))
					for k, tv := range tags {
						ch.Tags[k] = toStringSlice(tv)
					}
				}
				doc.NostrChannels[name] = ch
			}
		}
	}

	// ── session (typed) ───────────────────────────────────────────────────────
	if sessionRaw, ok := raw["session"].(map[string]any); ok {
		sess := state.SessionConfig{}
		if v, ok := sessionRaw["ttl_seconds"].(float64); ok {
			sess.TTLSeconds = int(v)
		}
		if v, ok := sessionRaw["max_sessions"].(float64); ok {
			sess.MaxSessions = int(v)
		}
		if v, ok := sessionRaw["history_limit"].(float64); ok {
			sess.HistoryLimit = int(v)
		}
		doc.Session = sess
	}

	// ── heartbeat (typed) ─────────────────────────────────────────────────────
	if hbRaw, ok := raw["heartbeat"].(map[string]any); ok {
		hb := state.HeartbeatConfig{}
		if v, ok := hbRaw["enabled"].(bool); ok {
			hb.Enabled = v
		}
		if v, ok := hbRaw["interval_ms"].(float64); ok {
			hb.IntervalMS = int(v)
		}
		doc.Heartbeat = hb
	}

	// ── tts (typed) ───────────────────────────────────────────────────────────
	if ttsRaw, ok := raw["tts"].(map[string]any); ok {
		tts := state.TTSConfig{}
		if v, ok := ttsRaw["enabled"].(bool); ok {
			tts.Enabled = v
		}
		if v, ok := ttsRaw["provider"].(string); ok {
			tts.Provider = strings.TrimSpace(v)
		}
		if v, ok := ttsRaw["voice"].(string); ok {
			tts.Voice = strings.TrimSpace(v)
		}
		doc.TTS = tts
	}

	// ── secrets (typed) ───────────────────────────────────────────────────────
	if secretsRaw, ok := raw["secrets"].(map[string]any); ok {
		doc.Secrets = make(state.SecretsConfig, len(secretsRaw))
		for k, v := range secretsRaw {
			if s, ok := v.(string); ok {
				doc.Secrets[k] = s
			}
		}
	}

	// ── cron (typed) ──────────────────────────────────────────────────────────
	if cronRaw, ok := raw["cron"].(map[string]any); ok {
		cron := state.CronConfig{}
		if v, ok := cronRaw["enabled"].(bool); ok {
			cron.Enabled = v
		}
		doc.CronCfg = cron
	}

	// ── pass-through sections (unknown / rarely accessed as typed) ────────────
	passThrough := []string{
		"skills", "memory", "update", "wizard", "pairing",
	}
	for _, key := range passThrough {
		if v, ok := raw[key]; ok {
			doc.Extra[key] = v
		}
	}

	// Clean up empty extras.
	if len(doc.Extra) == 0 {
		doc.Extra = nil
	}
	return doc
}

// ──────────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────────

// parseAgentConfigList converts a []any (from JSON unmarshalling) into a typed
// AgentsConfig slice.  Unknown fields are silently ignored to stay forward-compatible.
func parseAgentConfigList(list []any) state.AgentsConfig {
	out := make(state.AgentsConfig, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ac := state.AgentConfig{}
		if v, ok := m["id"].(string); ok {
			ac.ID = strings.TrimSpace(v)
		}
		if v, ok := m["name"].(string); ok {
			ac.Name = strings.TrimSpace(v)
		}
		if v, ok := m["model"].(string); ok {
			ac.Model = strings.TrimSpace(v)
		}
		if v, ok := m["workspace_dir"].(string); ok {
			ac.WorkspaceDir = strings.TrimSpace(v)
		} else if v, ok := m["workspaceDir"].(string); ok {
			ac.WorkspaceDir = strings.TrimSpace(v)
		}
		if v, ok := m["tool_profile"].(string); ok {
			ac.ToolProfile = strings.TrimSpace(v)
		} else if v, ok := m["toolProfile"].(string); ok {
			ac.ToolProfile = strings.TrimSpace(v)
		}
		if v, ok := toInt(m["heartbeat_ms"]); ok {
			ac.HeartbeatMS = v
		}
		if v, ok := toInt(m["history_limit"]); ok {
			ac.HistoryLimit = v
		}
		if v, ok := m["provider"].(string); ok {
			ac.Provider = strings.TrimSpace(v)
		}
		if v, ok := m["system_prompt"].(string); ok {
			ac.SystemPrompt = strings.TrimSpace(v)
		} else if v, ok := m["systemPrompt"].(string); ok {
			ac.SystemPrompt = strings.TrimSpace(v)
		}
		if v, ok := m["memory_scope"].(string); ok {
			ac.MemoryScope = state.AgentMemoryScope(strings.TrimSpace(v))
		} else if v, ok := m["memoryScope"].(string); ok {
			ac.MemoryScope = state.AgentMemoryScope(strings.TrimSpace(v))
		}
		if v, ok := m["enabled_tools"].([]any); ok {
			for _, t := range v {
				if s, ok := t.(string); ok && strings.TrimSpace(s) != "" {
					ac.EnabledTools = append(ac.EnabledTools, strings.TrimSpace(s))
				}
			}
		}
		// fallback_models: ordered list of fallback model identifiers.
		if v, ok := m["fallback_models"].([]any); ok {
			for _, fm := range v {
				if s, ok := fm.(string); ok && strings.TrimSpace(s) != "" {
					ac.FallbackModels = append(ac.FallbackModels, strings.TrimSpace(s))
				}
			}
		} else if v, ok := m["fallbackModels"].([]any); ok {
			for _, fm := range v {
				if s, ok := fm.(string); ok && strings.TrimSpace(s) != "" {
					ac.FallbackModels = append(ac.FallbackModels, strings.TrimSpace(s))
				}
			}
		}
		// light_model: cheaper model for simple messages.
		if v, ok := m["light_model"].(string); ok {
			ac.LightModel = strings.TrimSpace(v)
		} else if v, ok := m["lightModel"].(string); ok {
			ac.LightModel = strings.TrimSpace(v)
		}
		// light_model_threshold: complexity score threshold for light model routing.
		if v, ok := toFloat64(m["light_model_threshold"]); ok {
			ac.LightModelThreshold = v
		} else if v, ok := toFloat64(m["lightModelThreshold"]); ok {
			ac.LightModelThreshold = v
		}
		if v, ok := toInt(m["max_context_tokens"]); ok {
			ac.MaxContextTokens = v
		} else if v, ok := toInt(m["maxContextTokens"]); ok {
			ac.MaxContextTokens = v
		}
		if v, ok := m["thinking_level"].(string); ok {
			ac.ThinkingLevel = strings.TrimSpace(v)
		} else if v, ok := m["thinkingLevel"].(string); ok {
			ac.ThinkingLevel = strings.TrimSpace(v)
		}
		if v, ok := toInt(m["turn_timeout_secs"]); ok {
			ac.TurnTimeoutSecs = v
		} else if v, ok := toInt(m["turnTimeoutSecs"]); ok {
			ac.TurnTimeoutSecs = v
		}
		// dm_peers: list of Nostr pubkeys routed to this agent for DMs.
		if v, ok := m["dm_peers"].([]any); ok {
			for _, peer := range v {
				if s, ok := peer.(string); ok && strings.TrimSpace(s) != "" {
					ac.DmPeers = append(ac.DmPeers, strings.TrimSpace(s))
				}
			}
		} else if v, ok := m["dmPeers"].([]any); ok {
			for _, peer := range v {
				if s, ok := peer.(string); ok && strings.TrimSpace(s) != "" {
					ac.DmPeers = append(ac.DmPeers, strings.TrimSpace(s))
				}
			}
		}
		out = append(out, ac)
	}
	return out
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	case float32:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func toStringSlice(v any) []string {
	switch typed := v.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					out = append(out, t)
				}
			}
		}
		return out
	}
	return nil
}
