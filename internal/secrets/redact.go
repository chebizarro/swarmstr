package secrets

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Redactor scrubs exact secret values and common credential patterns from text
// before logs, errors, admin responses, or serialized data leave a trust boundary.
type Redactor struct {
	values map[string]string
	keys   []string
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|passwd|bearer)\s*[:=]\s*['"]?[^'"\s,}]+`),
	regexp.MustCompile(`(?i)Authorization:\s*Bearer\s+[A-Za-z0-9._~+/-]+=*`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`nsec1[023456789acdefghjklmnpqrstuvwxyz]+`),
}

// NewRedactor creates a redactor from a key/value map. Empty or very short
// values are ignored to avoid over-redacting ordinary words.
func NewRedactor(values map[string]string) *Redactor {
	r := &Redactor{values: map[string]string{}}
	for key, value := range values {
		r.Add(key, value)
	}
	return r
}

// Add registers a known secret value.
func (r *Redactor) Add(key, value string) {
	if r == nil || len(strings.TrimSpace(value)) < 4 {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "secret"
	}
	if r.values == nil {
		r.values = map[string]string{}
	}
	if _, exists := r.values[value]; !exists {
		r.keys = append(r.keys, value)
	}
	r.values[value] = key
	sort.SliceStable(r.keys, func(i, j int) bool { return len(r.keys[i]) > len(r.keys[j]) })
}

// RedactString returns text with registered secrets and common credential
// patterns replaced by redaction markers.
func (r *Redactor) RedactString(text string) string {
	out := text
	if r != nil {
		for _, value := range r.keys {
			out = strings.ReplaceAll(out, value, fmt.Sprintf("[REDACTED:%s]", r.values[value]))
		}
	}
	for _, pattern := range sensitivePatterns {
		out = pattern.ReplaceAllStringFunc(out, func(match string) string {
			if idx := strings.IndexAny(match, ":="); idx > 0 {
				return strings.TrimSpace(match[:idx+1]) + " [REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return out
}

// RedactAny recursively redacts strings in maps/slices without mutating input.
func (r *Redactor) RedactAny(v any) any {
	switch x := v.(type) {
	case string:
		return r.RedactString(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = r.RedactAny(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = r.RedactAny(item)
		}
		return out
	default:
		return v
	}
}
