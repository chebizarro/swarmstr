// Package config – config redaction for safe API responses.
//
// Redact returns a deep-copied ConfigDoc with sensitive values masked.
// It is called before any config.get response is sent over the wire.
//
// Redaction rules (in priority order):
//  1. Whole top-level Extra sections that are always secret: secrets, pairing.
//  2. Recursive field-name scan: any key whose name matches a sensitive pattern
//     (api_key, token, password, secret, private_key, …) is replaced with
//     the constant RedactedValue.
package config

import (
	"encoding/json"
	"strings"
	"unicode"

	"metiq/internal/store/state"
)

// RedactedValue is the placeholder used in place of sensitive values.
const RedactedValue = "[REDACTED]"

// alwaysRedactSections are Extra map keys whose entire value is replaced with
// RedactedValue without inspecting contents.
var alwaysRedactSections = map[string]bool{
	"secrets": true,
	"pairing": true,
}

// sensitiveSuffixes are lower-cased key suffixes that trigger per-value
// redaction during recursive traversal.
var sensitiveSuffixes = []string{
	"api_key", "apikey", "api_secret", "secret", "password", "passwd",
	"token", "authtoken", "bearertoken", "access_token", "refresh_token", "private_key", "privatekey",
	"credential", "auth_key", "authkey",
}

// Redact returns a deep copy of doc with sensitive fields masked.
// The original doc is never modified.
func Redact(doc state.ConfigDoc) state.ConfigDoc {
	// Deep-copy via JSON round-trip; keeps things simple and correct.
	raw, err := json.Marshal(doc)
	if err != nil {
		// Defensive: return empty doc if somehow unmarshalable.
		return state.ConfigDoc{Version: doc.Version}
	}
	var out state.ConfigDoc
	if err := json.Unmarshal(raw, &out); err != nil {
		return state.ConfigDoc{Version: doc.Version}
	}

	// Redact provider API keys in the typed Providers section.
	if out.Providers != nil {
		redacted := make(state.ProvidersConfig, len(out.Providers))
		for name, entry := range out.Providers {
			if entry.APIKey != "" {
				entry.APIKey = RedactedValue
			}
			if len(entry.APIKeys) > 0 {
				entry.APIKeys = make([]string, len(entry.APIKeys))
				for i := range entry.APIKeys {
					entry.APIKeys[i] = RedactedValue
				}
			}
			redacted[name] = entry
		}
		out.Providers = redacted
	}

	// Redact the entire Secrets section (values are opaque references).
	if len(out.Secrets) > 0 {
		redactedSecrets := make(state.SecretsConfig, len(out.Secrets))
		for k := range out.Secrets {
			redactedSecrets[k] = RedactedValue
		}
		out.Secrets = redactedSecrets
	}

	if out.Hooks.Token != "" {
		out.Hooks.Token = RedactedValue
	}

	if out.Extra == nil {
		return out
	}

	// Walk extra map.
	out.Extra = redactMap(out.Extra)
	if len(out.Extra) == 0 {
		out.Extra = nil
	}
	return out
}

// RedactMap applies redaction rules to an arbitrary map, returning a new map.
// It is exported so tests can exercise it directly.
func RedactMap(m map[string]any) map[string]any {
	return redactMap(m)
}

// ──────────────────────────────────────────────────────────────────────────────
// internal helpers
// ──────────────────────────────────────────────────────────────────────────────

func redactMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		lower := strings.ToLower(k)
		// Whole-section redaction takes highest priority.
		if alwaysRedactSections[lower] {
			out[k] = RedactedValue
			continue
		}
		// Key-name sensitive pattern check.
		if isSensitiveKey(k) {
			out[k] = RedactedValue
			continue
		}
		// Recurse into nested structures.
		out[k] = redactValue(v)
	}
	return out
}

func redactValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return redactMap(typed)
	case []any:
		return redactSlice(typed)
	default:
		return v
	}
}

func redactSlice(s []any) []any {
	if s == nil {
		return nil
	}
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = redactValue(v)
	}
	return out
}

func isSensitiveKey(key string) bool {
	canonical := canonicalSensitiveKeyName(key)
	for _, suffix := range sensitiveSuffixes {
		if canonical == suffix || strings.HasSuffix(canonical, "_"+suffix) {
			return true
		}
	}
	return false
}

func canonicalSensitiveKeyName(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	runes := []rune(key)
	var b strings.Builder
	lastSep := true
	for i, r := range runes {
		if r == '_' || r == '.' || r == '-' || unicode.IsSpace(r) {
			if !lastSep {
				b.WriteByte('_')
				lastSep = true
			}
			continue
		}
		if unicode.IsUpper(r) {
			if !lastSep && i > 0 {
				prev := runes[i-1]
				var next rune
				if i+1 < len(runes) {
					next = runes[i+1]
				}
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && next != 0 && unicode.IsLower(next)) {
					b.WriteByte('_')
				}
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(unicode.ToLower(r))
		lastSep = false
	}
	return strings.Trim(b.String(), "_")
}
