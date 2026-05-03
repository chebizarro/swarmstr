package hooks

import "strings"

func ExtractMutation(result any) map[string]any {
	m, ok := asMap(result)
	if !ok || len(m) == 0 {
		return nil
	}
	for _, key := range []string{"mutation", "mutations", "patch"} {
		if nested, ok := asMap(m[key]); ok {
			return cloneMap(nested)
		}
	}
	out := map[string]any{}
	for _, key := range []string{"args", "mutated_args", "payload", "message", "text", "reply_text", "model", "provider", "target", "contribution", "result"} {
		if v, ok := m[key]; ok {
			out[key] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ExtractRejection(result any) (bool, string) {
	m, ok := asMap(result)
	if !ok || len(m) == 0 {
		return false, ""
	}
	rejected := boolValue(m["reject"]) || boolValue(m["rejected"])
	if approved, ok := m["approved"]; ok && !boolValue(approved) {
		rejected = true
	}
	if !rejected {
		return false, ""
	}
	for _, key := range []string{"reason", "reject_reason", "rejection_reason", "message", "error"} {
		if s, ok := stringValue(m[key]); ok && strings.TrimSpace(s) != "" {
			return true, s
		}
	}
	return true, "rejected by hook"
}

func MergeMap(base, patch map[string]any) map[string]any {
	if len(base) == 0 {
		base = map[string]any{}
	}
	for k, v := range patch {
		if nestedPatch, ok := asMap(v); ok {
			if nestedBase, ok := asMap(base[k]); ok {
				base[k] = MergeMap(cloneMap(nestedBase), nestedPatch)
				continue
			}
			base[k] = cloneMap(nestedPatch)
			continue
		}
		base[k] = v
	}
	return base
}

func asMap(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if nested, ok := asMap(v); ok {
			out[k] = cloneMap(nested)
		} else {
			out[k] = v
		}
	}
	return out
}

func boolValue(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		s := strings.TrimSpace(strings.ToLower(b))
		return s == "true" || s == "1" || s == "yes"
	default:
		return false
	}
}
func stringValue(v any) (string, bool) { s, ok := v.(string); return s, ok }
