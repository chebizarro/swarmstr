package talk

import (
	"sort"
	"strings"
)

// Persona is one configured TTS persona (a named provider+voice preset).
type Persona struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Voice       string `json:"voice,omitempty"`
	Description string `json:"description,omitempty"`
}

// clearPersonaAliases are the sentinel ids that clear the active persona back
// to null (mirrors OpenClaw tts.setPersona off/none/default semantics).
var clearPersonaAliases = map[string]struct{}{
	"off":     {},
	"none":    {},
	"default": {},
	"":        {},
}

// IsClearPersona reports whether id requests clearing the active persona.
func IsClearPersona(id string) bool {
	_, ok := clearPersonaAliases[strings.ToLower(strings.TrimSpace(id))]
	return ok
}

// ListPersonas reads the configured personas from cfg.Extra["talk"]["personas"]
// and returns them sorted by id, plus the resolved active persona id (empty
// when unset or when the active id is no longer configured).
func ListPersonas(extra map[string]any, active string) ([]Persona, string) {
	talk := talkConfig(extra)
	personas := parsePersonas(talk["personas"])

	sort.Slice(personas, func(i, j int) bool { return personas[i].ID < personas[j].ID })

	active = strings.TrimSpace(active)
	if active != "" {
		found := false
		for _, p := range personas {
			if strings.EqualFold(p.ID, active) {
				active = p.ID
				found = true
				break
			}
		}
		if !found {
			active = ""
		}
	}
	return personas, active
}

// ValidatePersona resolves a requested persona id against the configured set.
// A clear-alias returns ("", true, nil). An unknown id returns ("", false, err).
func ValidatePersona(extra map[string]any, id string) (resolved string, cleared bool, err error) {
	if IsClearPersona(id) {
		return "", true, nil
	}
	personas := parsePersonas(talkConfig(extra)["personas"])
	for _, p := range personas {
		if strings.EqualFold(p.ID, id) {
			return p.ID, false, nil
		}
	}
	return "", false, newReasonedError(ReasonTalkUnconfigured, "unknown persona %q", strings.TrimSpace(id))
}

// PersonaByID returns the configured persona with the given id (case-insensitive).
func PersonaByID(extra map[string]any, id string) (Persona, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Persona{}, false
	}
	for _, p := range parsePersonas(talkConfig(extra)["personas"]) {
		if strings.EqualFold(p.ID, id) {
			return p, true
		}
	}
	return Persona{}, false
}

func parsePersonas(raw any) []Persona {
	items, ok := raw.([]any)
	if !ok {
		return []Persona{}
	}
	out := make([]Persona, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := firstString(m, "id", "personaId", "persona_id", "key", "name")
		if id == "" {
			continue
		}
		out = append(out, Persona{
			ID:          id,
			Name:        firstString(m, "name", "displayName", "label"),
			Provider:    firstString(m, "provider", "ttsProvider", "tts_provider"),
			Voice:       firstString(m, "voice", "voiceAlias", "voice_alias", "voiceModel", "voice_model"),
			Description: firstString(m, "description", "summary"),
		})
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s := asString(v); s != "" {
				return s
			}
		}
	}
	return ""
}
