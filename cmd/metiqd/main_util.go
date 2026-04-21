package main

import (
	"fmt"
	"sort"
	"strings"
)

// --- String normalization helpers ---

func normalizeCSVList(raw string) []string {
	items := strings.Split(raw, ",")
	return normalizeStringList(items)
}

func normalizeStringList(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// sanitizeStrings deduplicates and trims whitespace from a string slice.
func sanitizeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// --- Thinking / reasoning / verbose level normalization ---

// thinkingLevelToBudget converts a level string to an Anthropic thinking
// budget in tokens.  Returns 0 (disabled) for "off" or unknown values.
func thinkingLevelToBudget(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off", "":
		return 0
	case "minimal":
		return 1024
	case "low":
		return 5000
	case "medium":
		return 10000
	case "high":
		return 20000
	case "xhigh":
		return 40000
	default:
		return 10000
	}
}

func normalizeThinkingLevel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func normalizeReasoningLevel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func normalizeVerboseLevel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "quiet", "normal", "debug":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func normalizeResponseUsage(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "on", "tokens", "full":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

// --- Queue mode helpers ---

func normalizeQueueDrop(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "summarize":
		return "summarize"
	case "old", "oldest":
		return "oldest"
	case "new", "newest":
		return "newest"
	default:
		return ""
	}
}

func normalizeQueueMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "collect", "followup", "queue", "steer", "steer-backlog", "steer+backlog", "interrupt":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func queueModeCollect(mode string) bool {
	return mode == "" || mode == "collect"
}

func queueModeSequential(mode string) bool {
	switch mode {
	case "followup", "queue", "steer-backlog", "steer+backlog":
		return true
	default:
		return false
	}
}

// --- Simple string helpers ---

func coalesceString(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func prefixIfNeeded(value, prefix string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	return prefix + value
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

// --- Map / record helpers ---

func copyRecord(record map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range record {
		out[key] = value
	}
	return out
}

func getString(record map[string]any, key string) string {
	return strings.TrimSpace(fmt.Sprintf("%v", record[key]))
}

func getStringSlice(record map[string]any, key string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	switch values := record[key].(type) {
	case []string:
		for _, value := range values {
			v := strings.TrimSpace(value)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	case []any:
		for _, raw := range values {
			v := strings.TrimSpace(fmt.Sprintf("%v", raw))
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func getInt64(record map[string]any, key string) int64 {
	switch v := record[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// --- Merge / sort helpers ---

func mergeSessionMeta(base map[string]any, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

func mergeUniqueStrings(values ...[]string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, list := range values {
		for _, value := range list {
			v := strings.TrimSpace(value)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func sortRecordsByKeyDesc(records []map[string]any, key string) {
	sort.Slice(records, func(i, j int) bool {
		return getInt64(records[i], key) > getInt64(records[j], key)
	})
}

// --- Access control helpers ---

func scopesAllow(requested []string, allowed []string) bool {
	if len(requested) == 0 {
		return true
	}
	if len(allowed) == 0 {
		return false
	}
	allowedSet := map[string]struct{}{}
	for _, scope := range allowed {
		allowedSet[scope] = struct{}{}
	}
	for _, scope := range requested {
		if _, ok := allowedSet[scope]; !ok {
			return false
		}
	}
	return true
}
