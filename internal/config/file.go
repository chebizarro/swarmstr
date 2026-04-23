// Package config provides config file I/O for Metiq.
// It supports reading OpenClaw-compatible JSON5/YAML config files
// and mapping them to the Metiq ConfigDoc format.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
	"metiq/internal/store/state"
)

var supportedConfigExtensions = map[string]struct{}{
	".json":  {},
	".json5": {},
	".yaml":  {},
	".yml":   {},
}

// ValidateConfigFilePath normalizes and validates a config file path used by
// import/load/reload/sync flows.
func ValidateConfigFilePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("config file path is required")
	}
	path = filepath.Clean(path)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return "", fmt.Errorf("config file path must be a file")
	}
	return path, nil
}

// ValidateConfigWritePath validates a config file path intended as a write
// target. New config files must use a supported extension so the parser mode is
// explicit on subsequent reload/sync operations.
func ValidateConfigWritePath(path string) (string, error) {
	path, err := ValidateConfigFilePath(path)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if _, ok := supportedConfigExtensions[ext]; !ok {
		return "", fmt.Errorf("config file path must end in .json, .json5, .yaml, or .yml")
	}
	return path, nil
}

// LoadConfigFile reads a JSON5 (or plain JSON) or YAML config file
// and returns a ConfigDoc. OpenClaw config fields are mapped automatically.
func LoadConfigFile(path string) (state.ConfigDoc, error) {
	var err error
	path, err = ValidateConfigFilePath(path)
	if err != nil {
		return state.ConfigDoc{}, err
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
	return parseRawConfigMap(obj)
}

// WriteConfigFile serialises a ConfigDoc to a JSON file at path.
// Parent directories are created as needed.
func WriteConfigFile(path string, doc state.ConfigDoc) error {
	var err error
	path, err = ValidateConfigWritePath(path)
	if err != nil {
		return err
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
	return parseRawConfigMap(obj)
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

func parseConfigDurationMS(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, false
		}
		return int(d / time.Millisecond), true
	default:
		return 0, false
	}
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
	if v, ok := toInt(raw["version"]); ok && v > 0 {
		doc.Version = v
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
		if replyScheme, ok := dmRaw["reply_scheme"].(string); ok && strings.TrimSpace(replyScheme) != "" {
			doc.DM.ReplyScheme = strings.TrimSpace(replyScheme)
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
		if v, ok := agentRaw["thinking"].(string); ok {
			doc.Agent.Thinking = strings.TrimSpace(v)
		}
		if v, ok := agentRaw["verbose"].(string); ok {
			doc.Agent.Verbose = strings.TrimSpace(v)
		}
		if v, ok := agentRaw["default_autonomy"].(string); ok {
			doc.Agent.DefaultAutonomy = state.AutonomyMode(strings.TrimSpace(v))
		}
		if authRaw, ok := agentRaw["default_authority"].(map[string]any); ok {
			var auth state.TaskAuthority
			if decodeMapIntoStruct(authRaw, &auth) {
				doc.Agent.DefaultAuthority = &auth
			}
		}
	}

	// ── control (typed) ───────────────────────────────────────────────────────
	if controlRaw, ok := raw["control"].(map[string]any); ok {
		control := state.ControlPolicy{}
		if v, ok := controlRaw["require_auth"].(bool); ok {
			control.RequireAuth = v
		}
		if v, ok := controlRaw["legacy_token_fallback"].(bool); ok {
			control.LegacyTokenFallback = v
		}
		control.AllowUnauthMethods = toStringSlice(controlRaw["allow_unauth_methods"])
		if adminsRaw, ok := controlRaw["admins"].([]any); ok {
			control.Admins = make([]state.ControlAdmin, 0, len(adminsRaw))
			for _, item := range adminsRaw {
				adminMap, ok := item.(map[string]any)
				if !ok {
					continue
				}
				admin := state.ControlAdmin{}
				if v, ok := adminMap["pubkey"].(string); ok {
					admin.PubKey = strings.TrimSpace(v)
				}
				admin.Methods = toStringSlice(adminMap["methods"])
				control.Admins = append(control.Admins, admin)
			}
		}
		doc.Control = control
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
				if replyScheme, ok := dmRaw["reply_scheme"].(string); ok && strings.TrimSpace(replyScheme) != "" {
					doc.DM.ReplyScheme = strings.TrimSpace(replyScheme)
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
			if hbDefaults, ok := defaults["heartbeat"].(map[string]any); ok {
				if intervalMS, ok := parseConfigDurationMS(hbDefaults["every"]); ok {
					doc.Heartbeat.IntervalMS = intervalMS
					doc.Heartbeat.Enabled = intervalMS > 0
				}
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
				if keys := toStringSlice(pm["api_keys"]); len(keys) > 0 {
					entry.APIKeys = keys
				} else if keys := toStringSlice(pm["apiKeys"]); len(keys) > 0 {
					entry.APIKeys = keys
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
					case "enabled", "api_key", "apiKey", "api_keys", "apiKeys", "base_url", "baseUrl", "model", "extra":
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
				if cfgMap, ok := cm["config"].(map[string]any); ok {
					ch.Config = cloneAnyMap(cfgMap)
				}
				if v := toStringSlice(cm["allow_from"]); len(v) > 0 {
					ch.AllowFrom = v
				}
				doc.NostrChannels[name] = ch
			}
		}
	}

	// ── acp (typed) ───────────────────────────────────────────────────────────
	if acpRaw, ok := raw["acp"].(map[string]any); ok {
		acp := state.ACPConfig{}
		if v, ok := acpRaw["transport"].(string); ok {
			acp.Transport = strings.TrimSpace(v)
		}
		doc.ACP = acp
	}

	// ── fips (typed) ──────────────────────────────────────────────────────────
	if fipsRaw, ok := raw["fips"].(map[string]any); ok {
		fips := state.FIPSConfig{}
		if v, ok := fipsRaw["enabled"].(bool); ok {
			fips.Enabled = v
		}
		if v, ok := fipsRaw["control_socket"].(string); ok {
			fips.ControlSocket = strings.TrimSpace(v)
		}
		if v, ok := fipsRaw["agent_port"].(float64); ok {
			fips.AgentPort = int(v)
		}
		if v, ok := fipsRaw["control_port"].(float64); ok {
			fips.ControlPort = int(v)
		}
		if v, ok := fipsRaw["transport_pref"].(string); ok {
			fips.TransportPref = strings.TrimSpace(v)
		}
		if v, ok := fipsRaw["peers"].([]any); ok {
			for _, p := range v {
				if s, ok := p.(string); ok {
					if trimmed := strings.TrimSpace(s); trimmed != "" {
						fips.Peers = append(fips.Peers, trimmed)
					}
				}
			}
		}
		if v, ok := fipsRaw["conn_timeout"].(string); ok {
			fips.ConnTimeout = strings.TrimSpace(v)
		}
		if v, ok := fipsRaw["reach_cache_ttl"].(string); ok {
			fips.ReachCacheTTL = strings.TrimSpace(v)
		}
		doc.FIPS = fips
	}

	// ── session (typed) ───────────────────────────────────────────────────────
	if sessionRaw, ok := raw["session"].(map[string]any); ok {
		sess := state.SessionConfig{}
		if v, ok := sessionRaw["ttl_seconds"].(float64); ok {
			sess.TTLSeconds = int(v)
		}
		if v, ok := toInt(sessionRaw["prune_after_days"]); ok {
			sess.PruneAfterDays = v
		}
		if v, ok := toInt(sessionRaw["prune_idle_after_days"]); ok {
			sess.PruneIdleAfterDays = v
		}
		if v, ok := sessionRaw["prune_on_boot"].(bool); ok {
			sess.PruneOnBoot = v
		}
		doc.Session = sess
	}

	// ── storage (typed) ───────────────────────────────────────────────────────
	if storageRaw, ok := raw["storage"].(map[string]any); ok {
		storage := state.StorageConfig{}
		if v, ok := storageRaw["encrypt"].(bool); ok {
			storage.Encrypt = state.BoolPtr(v)
		}
		doc.Storage = storage
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
		if v, ok := toInt(cronRaw["job_timeout_secs"]); ok {
			cron.JobTimeoutSecs = v
		}
		doc.CronCfg = cron
	}

	// ── hooks (typed) ─────────────────────────────────────────────────────────
	if hooksRaw, ok := raw["hooks"].(map[string]any); ok {
		var hooks state.HooksConfig
		if decodeMapIntoStruct(hooksRaw, &hooks) {
			doc.Hooks = hooks
		}
	}

	// ── timeouts (typed) ──────────────────────────────────────────────────────
	if timeoutsRaw, ok := raw["timeouts"].(map[string]any); ok {
		var cfg state.TimeoutsConfig
		if decodeMapIntoStruct(timeoutsRaw, &cfg) {
			doc.Timeouts = cfg
		}
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
		if hb, ok := m["heartbeat"].(map[string]any); ok {
			if v, ok := hb["model"].(string); ok {
				ac.Heartbeat.Model = strings.TrimSpace(v)
			}
		}
		if v, ok := toInt(m["context_window"]); ok {
			ac.ContextWindow = v
		} else if v, ok := toInt(m["contextWindow"]); ok {
			ac.ContextWindow = v
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
		if v, ok := toInt(m["max_agentic_iterations"]); ok {
			ac.MaxAgenticIterations = v
		} else if v, ok := toInt(m["maxAgenticIterations"]); ok {
			ac.MaxAgenticIterations = v
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

func parseRawConfigMap(raw map[string]any) (state.ConfigDoc, error) {
	if errs := detectUnknownConfigKeys(raw); len(errs) > 0 {
		return state.ConfigDoc{}, fmt.Errorf("unsupported config fields:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return mapRawToConfigDoc(raw), nil
}

func decodeMapIntoStruct(raw map[string]any, out any) bool {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	return json.Unmarshal(encoded, out) == nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func detectUnknownConfigKeys(raw map[string]any) []string {
	var errs []string
	allowedTop := []string{
		"version", "dm", "relays", "agent", "control", "acp", "agents", "nostr_channels",
		"providers", "session", "storage", "heartbeat", "tts", "secrets", "cron",
		"hooks", "timeouts", "agent_list", "fips", "extra", "channels", "plugins",
		"skills", "memory", "update", "wizard", "pairing",
	}
	for key, value := range raw {
		if !slices.Contains(allowedTop, key) {
			errs = append(errs, key)
			continue
		}
		switch key {
		case "dm":
			errs = append(errs, detectUnknownMapKeys("dm", value, []string{"policy", "reply_scheme", "allow_from", "allowFrom", "allow_from_lists"})...)
		case "relays":
			errs = append(errs, detectUnknownMapKeys("relays", value, []string{"read", "write"})...)
		case "agent":
			errs = append(errs, detectUnknownMapKeys("agent", value, []string{"default_model", "thinking", "verbose", "default_autonomy", "default_authority"})...)
		case "control":
			errs = append(errs, detectUnknownMapKeys("control", value, []string{"require_auth", "allow_unauth_methods", "admins", "legacy_token_fallback"})...)
		case "acp":
			errs = append(errs, detectUnknownMapKeys("acp", value, []string{"transport"})...)
		case "session":
			errs = append(errs, detectUnknownMapKeys("session", value, []string{"ttl_seconds", "prune_after_days", "prune_idle_after_days", "prune_on_boot"})...)
		case "storage":
			errs = append(errs, detectUnknownMapKeys("storage", value, []string{"encrypt"})...)
		case "heartbeat":
			errs = append(errs, detectUnknownMapKeys("heartbeat", value, []string{"enabled", "interval_ms"})...)
		case "tts":
			errs = append(errs, detectUnknownMapKeys("tts", value, []string{"enabled", "provider", "voice"})...)
		case "cron":
			errs = append(errs, detectUnknownMapKeys("cron", value, []string{"enabled", "job_timeout_secs"})...)
		case "agent_list":
			errs = append(errs, detectUnknownMapKeys("agent_list", value, []string{"d", "relay", "auto_sync"})...)
		case "fips":
			errs = append(errs, detectUnknownMapKeys("fips", value, []string{"enabled", "control_socket", "agent_port", "control_port", "transport_pref", "peers", "conn_timeout", "reach_cache_ttl"})...)
		case "hooks":
			errs = append(errs, detectUnknownMapKeys("hooks", value, []string{"enabled", "token", "allowed_agent_ids", "default_session_key", "allow_request_session_key", "mappings"})...)
		case "timeouts":
			errs = append(errs, detectUnknownMapKeys("timeouts", value, []string{
				"session_memory_extraction_secs", "session_compact_summary_secs", "grep_search_secs",
				"image_fetch_secs", "tool_chain_exec_secs", "git_ops_secs", "llm_provider_http_secs",
				"webhook_wake_secs", "webhook_agent_start_secs", "signer_connect_secs",
				"memory_persist_secs", "subagent_default_secs",
			})...)
		case "agents":
			errs = append(errs, detectUnknownAgentsKeys(value)...)
		case "nostr_channels":
			errs = append(errs, detectUnknownNostrChannelKeys(value)...)
		}
	}
	slices.Sort(errs)
	return errs
}

func detectUnknownMapKeys(path string, raw any, allowed []string) []string {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	var errs []string
	for key := range m {
		if !slices.Contains(allowed, key) {
			errs = append(errs, path+"."+key)
		}
	}
	return errs
}

func detectUnknownAgentsKeys(raw any) []string {
	var errs []string
	switch typed := raw.(type) {
	case []any:
		allowed := []string{
			"id", "name", "model", "workspace_dir", "workspaceDir", "tool_profile", "toolProfile",
			"provider", "system_prompt", "systemPrompt", "memory_scope", "memoryScope",
			"enabled_tools", "fallback_models", "fallbackModels", "light_model", "lightModel",
			"light_model_threshold", "lightModelThreshold", "heartbeat", "context_window",
			"contextWindow", "max_context_tokens", "maxContextTokens", "thinking_level",
			"thinkingLevel", "turn_timeout_secs", "turnTimeoutSecs", "dm_peers", "dmPeers",
			"max_agentic_iterations", "maxAgenticIterations",
		}
		for i, item := range typed {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			for key := range m {
				if !slices.Contains(allowed, key) {
					errs = append(errs, fmt.Sprintf("agents[%d].%s", i, key))
				}
			}
		}
	case map[string]any:
		for key := range typed {
			if !slices.Contains([]string{"defaults", "list"}, key) {
				errs = append(errs, "agents."+key)
			}
		}
		if defaults, ok := typed["defaults"].(map[string]any); ok {
			for key := range defaults {
				if !slices.Contains([]string{"model", "heartbeat"}, key) {
					errs = append(errs, "agents.defaults."+key)
				}
			}
			if hb, ok := defaults["heartbeat"].(map[string]any); ok {
				for key := range hb {
					if key != "every" {
						errs = append(errs, "agents.defaults.heartbeat."+key)
					}
				}
			}
		}
		if list, ok := typed["list"].([]any); ok {
			errs = append(errs, detectUnknownAgentsKeys(list)...)
		}
	}
	return errs
}

func detectUnknownNostrChannelKeys(raw any) []string {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	allowed := []string{"kind", "enabled", "group_address", "channel_id", "relays", "agent_id", "tags", "config", "allow_from"}
	var errs []string
	for name, item := range m {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for key := range cm {
			if !slices.Contains(allowed, key) {
				errs = append(errs, "nostr_channels."+name+"."+key)
			}
		}
	}
	return errs
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
