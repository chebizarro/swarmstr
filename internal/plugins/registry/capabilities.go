package registry

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/plugins/runtime"
)

// Registration is the normalized OpenClaw capability payload captured by the
// Node.js host. Alias it here so registry callers do not need to import the
// runtime package directly.
type Registration = runtime.CapabilityRegistration

// CapabilityType identifies a registered capability namespace.
type CapabilityType string

const (
	CapabilityTypeTool                  CapabilityType = "tool"
	CapabilityTypeProvider              CapabilityType = "provider"
	CapabilityTypeChannel               CapabilityType = "channel"
	CapabilityTypeHook                  CapabilityType = "hook"
	CapabilityTypeService               CapabilityType = "service"
	CapabilityTypeCommand               CapabilityType = "command"
	CapabilityTypeGatewayMethod         CapabilityType = "gateway_method"
	CapabilityTypeSpeechProvider        CapabilityType = "speech_provider"
	CapabilityTypeTranscriptionProvider CapabilityType = "transcription_provider"
	CapabilityTypeImageGenProvider      CapabilityType = "image_gen_provider"
	CapabilityTypeVideoGenProvider      CapabilityType = "video_gen_provider"
	CapabilityTypeMusicGenProvider      CapabilityType = "music_gen_provider"
	CapabilityTypeWebSearchProvider     CapabilityType = "web_search_provider"
	CapabilityTypeWebFetchProvider      CapabilityType = "web_fetch_provider"
	CapabilityTypeMemoryProvider        CapabilityType = "memory_embedding_provider"
)

// PluginSource records the implementation/runtime that contributed a capability.
type PluginSource string

const (
	PluginSourceNative   PluginSource = "native"
	PluginSourceOpenClaw PluginSource = "openclaw"
	PluginSourceGoja     PluginSource = "goja"
)

// PluginRecord tracks a loaded plugin and every capability it contributed.
type PluginRecord struct {
	ID           string
	Name         string
	Version      string
	Source       PluginSource
	LoadedAt     time.Time
	Capabilities []CapabilityRef
}

// CapabilityRef references a capability by type and O(1) registry key.
type CapabilityRef struct {
	Type string
	ID   string
}

// CapabilityMetadata is the common metadata exposed by all registries.
type CapabilityMetadata struct {
	ID           string
	PluginID     string
	Name         string
	Label        string
	Description  string
	Source       PluginSource
	Raw          map[string]any
	RegisteredAt time.Time
}

func normalizeCapabilityType(t string) CapabilityType {
	switch strings.TrimSpace(t) {
	case "image_generation_provider":
		return CapabilityTypeImageGenProvider
	case "video_generation_provider":
		return CapabilityTypeVideoGenProvider
	case "music_generation_provider":
		return CapabilityTypeMusicGenProvider
	case "memory_provider":
		return CapabilityTypeMemoryProvider
	default:
		return CapabilityType(t)
	}
}

func capabilityTypeString(t CapabilityType) string { return string(t) }

func cloneRaw(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneAny(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return cloneRaw(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = cloneAny(item)
		}
		return out
	case []string:
		return append([]string(nil), val...)
	case []int:
		return append([]int(nil), val...)
	case []float64:
		return append([]float64(nil), val...)
	case []bool:
		return append([]bool(nil), val...)
	default:
		return val
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func stringFromRaw(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	v, _ := raw[key].(string)
	return v
}

func boolFromRaw(raw map[string]any, key string) bool {
	if raw == nil {
		return false
	}
	v, _ := raw[key].(bool)
	return v
}

func intFromRaw(raw map[string]any, key string, fallback int) int {
	if raw == nil {
		return fallback
	}
	switch v := raw[key].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return fallback
	}
}

func stringSlice(values []string, raw map[string]any, key string) []string {
	if len(values) > 0 {
		out := make([]string, 0, len(values))
		for _, v := range values {
			if v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	if raw == nil {
		return nil
	}
	switch v := raw[key].(type) {
	case []string:
		out := append([]string(nil), v...)
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func sortedCapabilityRefs(refs []CapabilityRef) []CapabilityRef {
	out := append([]CapabilityRef(nil), refs...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type == out[j].Type {
			return out[i].ID < out[j].ID
		}
		return out[i].Type < out[j].Type
	})
	return out
}

func metadataFromRegistration(pluginID string, source PluginSource, reg Registration, id string) CapabilityMetadata {
	if reg.PluginID != "" {
		pluginID = reg.PluginID
	}
	return CapabilityMetadata{
		ID:           id,
		PluginID:     pluginID,
		Name:         firstNonEmpty(reg.Name, stringFromRaw(reg.Raw, "name")),
		Label:        firstNonEmpty(reg.Label, stringFromRaw(reg.Raw, "label")),
		Description:  firstNonEmpty(reg.Description, stringFromRaw(reg.Raw, "description")),
		Source:       source,
		Raw:          cloneRaw(reg.Raw),
		RegisteredAt: time.Now(),
	}
}

func requireID(kind, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%s registration missing id", kind)
	}
	return nil
}

func addPluginIndex(index map[string]map[string]struct{}, pluginID, id string) {
	if pluginID == "" || id == "" {
		return
	}
	if index[pluginID] == nil {
		index[pluginID] = map[string]struct{}{}
	}
	index[pluginID][id] = struct{}{}
}

func removePluginIndex(index map[string]map[string]struct{}, pluginID, id string) {
	if pluginID == "" || id == "" {
		return
	}
	ids := index[pluginID]
	if ids == nil {
		return
	}
	delete(ids, id)
	if len(ids) == 0 {
		delete(index, pluginID)
	}
}

func idsForPlugin(index map[string]map[string]struct{}, pluginID string) []string {
	ids := index[pluginID]
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
