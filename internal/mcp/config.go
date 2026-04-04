package mcp

import (
	"encoding/json"
	"sort"
	"strings"

	"metiq/internal/store/state"
)

// ConfigSource identifies where an MCP server definition came from.
// The current implementation only resolves ConfigDoc.Extra["mcp"], but the
// resolver is source-aware so future project/dynamic/plugin layers can plug in
// without changing the manager runtime contract.
type ConfigSource string

const (
	ConfigSourceExtraMCP ConfigSource = "extra.mcp"
)

const extraMCPPrecedence = 100

// SuppressionReason explains why a server candidate lost during inventory
// resolution.
type SuppressionReason string

const (
	SuppressionReasonNameConflict       SuppressionReason = "name_conflict"
	SuppressionReasonDuplicateSignature SuppressionReason = "duplicate_signature"
)

// SourceConfig is a source-local MCP configuration block before precedence and
// deduplication are applied.
type SourceConfig struct {
	Source     ConfigSource            `json:"source"`
	Enabled    bool                    `json:"enabled"`
	Precedence int                     `json:"precedence"`
	Servers    map[string]ServerConfig `json:"servers,omitempty"`
}

// ResolvedServerConfig is the canonical MCP inventory entry consumed by the
// runtime. It embeds the executable ServerConfig and adds provenance metadata
// needed for future policy, lifecycle, and CLI work.
type ResolvedServerConfig struct {
	Name string `json:"name"`
	ServerConfig
	Source     ConfigSource `json:"source"`
	Precedence int          `json:"precedence"`
	Signature  string       `json:"signature,omitempty"`
}

// SuppressedServer records a server definition that lost during inventory
// resolution due to precedence/name conflict or duplicate launch signature.
type SuppressedServer struct {
	Name        string            `json:"name"`
	Source      ConfigSource      `json:"source"`
	Precedence  int               `json:"precedence"`
	Signature   string            `json:"signature,omitempty"`
	DuplicateOf string            `json:"duplicate_of,omitempty"`
	Reason      SuppressionReason `json:"reason"`
}

type serverCandidate struct {
	name       string
	config     ServerConfig
	source     ConfigSource
	precedence int
	signature  string
}

// ResolveConfigDoc returns the canonical resolved MCP inventory for a runtime
// configuration document.
func ResolveConfigDoc(doc state.ConfigDoc) Config {
	return ResolveSourceConfigs(parseExtraMCPSource(doc.Extra))
}

// ParseMCPConfig preserves the historical manager API but now returns the
// resolved inventory rather than a direct parse of extra["mcp"].
func ParseMCPConfig(extra map[string]any) Config {
	return ResolveSourceConfigs(parseExtraMCPSource(extra))
}

// ResolveSourceConfigs merges source-local MCP definitions into one resolved
// inventory. Higher-precedence sources win. Ties are broken deterministically
// by source name then server name. Disabled servers are preserved separately so
// the lifecycle manager can surface explicit disabled state without letting
// disabled definitions suppress enabled candidates.
func ResolveSourceConfigs(sources ...SourceConfig) Config {
	resolved := Config{
		Servers:         make(map[string]ResolvedServerConfig),
		DisabledServers: make(map[string]ResolvedServerConfig),
	}
	if len(sources) == 0 {
		return resolved
	}

	activeCandidates := make([]serverCandidate, 0)
	disabledCandidates := make([]serverCandidate, 0)
	for _, src := range sources {
		if src.Enabled {
			resolved.Enabled = true
		}
		if len(src.Servers) == 0 {
			continue
		}
		names := make([]string, 0, len(src.Servers))
		for name := range src.Servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			trimmedName := strings.TrimSpace(name)
			if trimmedName == "" {
				continue
			}
			cfg := normalizeServerConfig(src.Servers[name])
			candidate := serverCandidate{
				name:       trimmedName,
				config:     cfg,
				source:     src.Source,
				precedence: src.Precedence,
				signature:  getServerSignature(cfg),
			}
			if src.Enabled && cfg.Enabled {
				activeCandidates = append(activeCandidates, candidate)
			} else {
				candidate.config.Enabled = false
				disabledCandidates = append(disabledCandidates, candidate)
			}
		}
	}

	sortCandidates(activeCandidates)
	sortCandidates(disabledCandidates)

	seenNames := make(map[string]ResolvedServerConfig, len(activeCandidates)+len(disabledCandidates))
	seenSigs := make(map[string]string, len(activeCandidates)+len(disabledCandidates))
	resolveCandidates(resolved.Servers, activeCandidates, seenNames, seenSigs, &resolved.Suppressed)
	resolveCandidates(resolved.DisabledServers, disabledCandidates, seenNames, seenSigs, &resolved.Suppressed)

	if len(resolved.Servers) == 0 {
		resolved.Servers = nil
	}
	if len(resolved.DisabledServers) == 0 {
		resolved.DisabledServers = nil
	}
	return resolved
}

func sortCandidates(candidates []serverCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].precedence != candidates[j].precedence {
			return candidates[i].precedence > candidates[j].precedence
		}
		if candidates[i].source != candidates[j].source {
			return candidates[i].source < candidates[j].source
		}
		return candidates[i].name < candidates[j].name
	})
}

func resolveCandidates(dest map[string]ResolvedServerConfig, candidates []serverCandidate, seenNames map[string]ResolvedServerConfig, seenSigs map[string]string, suppressed *[]SuppressedServer) {
	for _, candidate := range candidates {
		if existing, ok := seenNames[candidate.name]; ok {
			*suppressed = append(*suppressed, SuppressedServer{
				Name:        candidate.name,
				Source:      candidate.source,
				Precedence:  candidate.precedence,
				Signature:   candidate.signature,
				DuplicateOf: existing.Name,
				Reason:      SuppressionReasonNameConflict,
			})
			continue
		}
		if candidate.signature != "" {
			if duplicateOf, ok := seenSigs[candidate.signature]; ok && duplicateOf != candidate.name {
				*suppressed = append(*suppressed, SuppressedServer{
					Name:        candidate.name,
					Source:      candidate.source,
					Precedence:  candidate.precedence,
					Signature:   candidate.signature,
					DuplicateOf: duplicateOf,
					Reason:      SuppressionReasonDuplicateSignature,
				})
				continue
			}
		}
		entry := ResolvedServerConfig{
			Name:         candidate.name,
			ServerConfig: candidate.config,
			Source:       candidate.source,
			Precedence:   candidate.precedence,
			Signature:    candidate.signature,
		}
		dest[candidate.name] = entry
		seenNames[candidate.name] = entry
		if candidate.signature != "" {
			seenSigs[candidate.signature] = candidate.name
		}
	}
}

func parseExtraMCPSource(extra map[string]any) SourceConfig {
	source := SourceConfig{
		Source:     ConfigSourceExtraMCP,
		Precedence: extraMCPPrecedence,
	}
	if extra == nil {
		return source
	}
	mcpRaw, ok := extra["mcp"]
	if !ok {
		return source
	}
	mcpMap, ok := mcpRaw.(map[string]any)
	if !ok {
		return source
	}
	if enabled, ok := mcpMap["enabled"].(bool); ok {
		source.Enabled = enabled
	}
	serversRaw, ok := mcpMap["servers"]
	if !ok {
		return source
	}
	serversMap, ok := serversRaw.(map[string]any)
	if !ok {
		return source
	}
	source.Servers = make(map[string]ServerConfig, len(serversMap))
	for name, raw := range serversMap {
		serverMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		source.Servers[name] = parseServerConfigMap(serverMap)
	}
	return source
}

func parseServerConfigMap(raw map[string]any) ServerConfig {
	var sc ServerConfig
	if enabled, ok := raw["enabled"].(bool); ok {
		sc.Enabled = enabled
	}
	if command, ok := raw["command"].(string); ok {
		sc.Command = strings.TrimSpace(command)
	}
	if transportType, ok := raw["type"].(string); ok {
		sc.Type = strings.ToLower(strings.TrimSpace(transportType))
	}
	if url, ok := raw["url"].(string); ok {
		sc.URL = strings.TrimSpace(url)
	}
	if argsRaw, ok := raw["args"]; ok {
		sc.Args = parseStringArray(argsRaw)
	}
	if envRaw, ok := raw["env"]; ok {
		sc.Env = parseStringMap(envRaw)
	}
	if headersRaw, ok := raw["headers"]; ok {
		sc.Headers = parseStringMap(headersRaw)
	}
	return normalizeServerConfig(sc)
}

func normalizeServerConfig(cfg ServerConfig) ServerConfig {
	cfg.Command = strings.TrimSpace(cfg.Command)
	cfg.Type = strings.ToLower(strings.TrimSpace(cfg.Type))
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Args = trimStringArray(cfg.Args)
	cfg.Env = trimStringMap(cfg.Env)
	cfg.Headers = trimStringMap(cfg.Headers)
	return cfg
}

func trimStringArray(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func trimStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseStringArray(raw any) []string {
	switch values := raw.(type) {
	case []string:
		return trimStringArray(values)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			s, ok := value.(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return trimStringArray(out)
	default:
		return nil
	}
}

func parseStringMap(raw any) map[string]string {
	switch values := raw.(type) {
	case map[string]string:
		return trimStringMap(values)
	case map[string]any:
		out := make(map[string]string, len(values))
		for key, value := range values {
			s, ok := value.(string)
			if !ok {
				continue
			}
			out[key] = s
		}
		return trimStringMap(out)
	default:
		return nil
	}
}

func getServerSignature(cfg ServerConfig) string {
	cfg = normalizeServerConfig(cfg)
	transportType := transportTypeForSignature(cfg)
	switch transportType {
	case "stdio":
		if cfg.Command == "" {
			return ""
		}
		data, err := json.Marshal(struct {
			Transport string            `json:"transport"`
			Command   string            `json:"command"`
			Args      []string          `json:"args,omitempty"`
			Env       map[string]string `json:"env,omitempty"`
		}{
			Transport: transportType,
			Command:   cfg.Command,
			Args:      cfg.Args,
			Env:       cfg.Env,
		})
		if err != nil {
			return ""
		}
		return transportType + ":" + string(data)
	case "sse", "http":
		if cfg.URL == "" {
			return ""
		}
		data, err := json.Marshal(struct {
			Transport string            `json:"transport"`
			URL       string            `json:"url"`
			Headers   map[string]string `json:"headers,omitempty"`
		}{
			Transport: transportType,
			URL:       cfg.URL,
			Headers:   cfg.Headers,
		})
		if err != nil {
			return ""
		}
		return transportType + ":" + string(data)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return "unknown:" + string(data)
}

func transportTypeForSignature(cfg ServerConfig) string {
	switch cfg.Type {
	case "stdio", "sse", "http":
		return cfg.Type
	case "":
		if cfg.URL != "" {
			return "sse"
		}
		if cfg.Command != "" {
			return "stdio"
		}
	}
	return "unknown"
}
