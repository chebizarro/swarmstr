package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

// ─── Tool Mutation Detection ──────────────────────────────────────────────────
//
// Detects mutating tool calls (those with side effects) and creates stable
// fingerprints for idempotency checking. This prevents duplicate actions
// when agents retry or resume.

// mutatingToolNames is the set of tool names that are known to have side effects
var mutatingToolNames = map[string]bool{
	"write":          true,
	"edit":           true,
	"apply_patch":    true,
	"exec":           true,
	"bash":           true,
	"bash_exec":      true,
	"process":        true,
	"message":        true,
	"sessions_spawn": true,
	"sessions_send":  true,
	"cron":           true,
	"cron_add":       true,
	"gateway":        true,
	"canvas":         true,
	"nodes":          true,
	"session_status": true,
	"nostr_publish":  true,
	"nostr_dm":       true,
	"file_write":     true,
	"create_file":    true,
	"delete_file":    true,
	"move_file":      true,
}

// readOnlyActions are actions that don't cause mutations
var readOnlyActions = map[string]bool{
	"get":     true,
	"list":    true,
	"read":    true,
	"status":  true,
	"show":    true,
	"fetch":   true,
	"search":  true,
	"query":   true,
	"view":    true,
	"poll":    true,
	"log":     true,
	"inspect": true,
	"check":   true,
	"probe":   true,
}

// processMutatingActions are mutating actions for process tool
var processMutatingActions = map[string]bool{
	"write":     true,
	"send_keys": true,
	"submit":    true,
	"paste":     true,
	"kill":      true,
}

// messageMutatingActions are mutating actions for message tool
var messageMutatingActions = map[string]bool{
	"send":         true,
	"reply":        true,
	"thread_reply": true,
	"threadreply":  true,
	"edit":         true,
	"delete":       true,
	"react":        true,
	"pin":          true,
	"unpin":        true,
}

// ToolMutationState represents the mutation analysis of a tool call
type ToolMutationState struct {
	IsMutating        bool
	ActionFingerprint string
}

// ToolActionRef identifies a tool action for comparison
type ToolActionRef struct {
	ToolName          string
	Meta              string
	ActionFingerprint string
}

// whitespaceRE normalizes whitespace in strings
var whitespaceRE = regexp.MustCompile(`[\s-]+`)

// normalizeActionName normalizes an action name for comparison
func normalizeActionName(value interface{}) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	s = whitespaceRE.ReplaceAllString(s, "_")
	return s
}

// normalizeFingerprintValue normalizes a value for fingerprinting
func normalizeFingerprintValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return ""
		}
		return strings.ToLower(s)
	case float64:
		return strings.ToLower(strings.TrimSpace(string(rune(int(v)))))
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int, int64, int32:
		return strings.ToLower(strings.TrimSpace(string(rune(v.(int)))))
	default:
		return ""
	}
}

// asRecord converts interface{} to map[string]interface{}
func asRecord(args interface{}) map[string]interface{} {
	if args == nil {
		return nil
	}

	// If already a map
	if m, ok := args.(map[string]interface{}); ok {
		return m
	}

	// If it's a string, try to parse as JSON
	if s, ok := args.(string); ok {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			return m
		}
	}

	return nil
}

// appendFingerprintAlias appends a fingerprint part if any of the keys exist
func appendFingerprintAlias(parts []string, record map[string]interface{}, label string, keys []string) ([]string, bool) {
	if record == nil {
		return parts, false
	}

	for _, key := range keys {
		if val, ok := record[key]; ok {
			normalized := normalizeFingerprintValue(val)
			if normalized != "" {
				return append(parts, label+"="+normalized), true
			}
		}
	}
	return parts, false
}

// IsLikelyMutatingToolName checks if a tool name suggests mutation
func IsLikelyMutatingToolName(toolName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	if normalized == "" {
		return false
	}

	if mutatingToolNames[normalized] {
		return true
	}

	// Check patterns
	if strings.HasSuffix(normalized, "_actions") {
		return true
	}
	if strings.HasPrefix(normalized, "message_") {
		return true
	}
	if strings.Contains(normalized, "send") {
		return true
	}
	if strings.Contains(normalized, "publish") {
		return true
	}
	if strings.Contains(normalized, "write") {
		return true
	}
	if strings.Contains(normalized, "create") {
		return true
	}
	if strings.Contains(normalized, "delete") {
		return true
	}
	if strings.Contains(normalized, "exec") {
		return true
	}

	return false
}

// IsMutatingToolCall checks if a specific tool call is mutating
func IsMutatingToolCall(toolName string, args interface{}) bool {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	record := asRecord(args)
	action := normalizeActionName(record["action"])

	switch normalized {
	case "write", "edit", "apply_patch", "exec", "bash", "bash_exec",
		"sessions_send", "nostr_publish", "nostr_dm", "file_write",
		"create_file", "delete_file", "move_file":
		return true

	case "process":
		return action != "" && processMutatingActions[action]

	case "message":
		if action != "" && messageMutatingActions[action] {
			return true
		}
		// Also mutating if content or message is provided
		if _, ok := record["content"].(string); ok {
			return true
		}
		if _, ok := record["message"].(string); ok {
			return true
		}
		return false

	case "subagents":
		return action == "kill" || action == "steer"

	case "session_status":
		if model, ok := record["model"].(string); ok {
			return strings.TrimSpace(model) != ""
		}
		return false

	case "cron", "cron_add", "gateway", "canvas":
		return action == "" || !readOnlyActions[action]

	case "nodes":
		return action == "" || action != "list"

	default:
		// Patterns
		if strings.HasSuffix(normalized, "_actions") {
			return action == "" || !readOnlyActions[action]
		}
		if strings.HasPrefix(normalized, "message_") || strings.Contains(normalized, "send") {
			return true
		}
		return false
	}
}

// BuildToolActionFingerprint creates a stable fingerprint for a mutating tool call
func BuildToolActionFingerprint(toolName string, args interface{}, meta string) string {
	if !IsMutatingToolCall(toolName, args) {
		return ""
	}

	normalized := strings.ToLower(strings.TrimSpace(toolName))
	record := asRecord(args)
	action := normalizeActionName(record["action"])

	parts := []string{"tool=" + normalized}
	if action != "" {
		parts = append(parts, "action="+action)
	}

	hasStableTarget := false

	// Path variants
	parts, found := appendFingerprintAlias(parts, record, "path", []string{
		"path", "file_path", "filePath", "filepath", "file",
	})
	hasStableTarget = hasStableTarget || found

	// Old/new path for moves/renames
	parts, found = appendFingerprintAlias(parts, record, "oldpath", []string{"oldPath", "old_path"})
	hasStableTarget = hasStableTarget || found

	parts, found = appendFingerprintAlias(parts, record, "newpath", []string{"newPath", "new_path"})
	hasStableTarget = hasStableTarget || found

	// Target
	parts, found = appendFingerprintAlias(parts, record, "to", []string{"to", "target"})
	hasStableTarget = hasStableTarget || found

	// Message ID
	parts, found = appendFingerprintAlias(parts, record, "messageid", []string{"messageId", "message_id"})
	hasStableTarget = hasStableTarget || found

	// Session key
	parts, found = appendFingerprintAlias(parts, record, "sessionkey", []string{"sessionKey", "session_key"})
	hasStableTarget = hasStableTarget || found

	// Job ID
	parts, found = appendFingerprintAlias(parts, record, "jobid", []string{"jobId", "job_id"})
	hasStableTarget = hasStableTarget || found

	// Generic ID
	parts, found = appendFingerprintAlias(parts, record, "id", []string{"id"})
	hasStableTarget = hasStableTarget || found

	// Model
	parts, found = appendFingerprintAlias(parts, record, "model", []string{"model"})
	hasStableTarget = hasStableTarget || found

	// Pubkey for nostr
	parts, found = appendFingerprintAlias(parts, record, "pubkey", []string{"pubkey", "npub", "recipient"})
	hasStableTarget = hasStableTarget || found

	// Only include meta if we don't have stable target keys
	if !hasStableTarget && meta != "" {
		normalizedMeta := strings.ToLower(strings.TrimSpace(meta))
		normalizedMeta = whitespaceRE.ReplaceAllString(normalizedMeta, " ")
		if normalizedMeta != "" {
			parts = append(parts, "meta="+normalizedMeta)
		}
	}

	return strings.Join(parts, "|")
}

// BuildToolMutationState analyzes a tool call for mutation state
func BuildToolMutationState(toolName string, args interface{}, meta string) ToolMutationState {
	fingerprint := BuildToolActionFingerprint(toolName, args, meta)
	return ToolMutationState{
		IsMutating:        fingerprint != "",
		ActionFingerprint: fingerprint,
	}
}

// IsSameToolMutationAction checks if two tool actions are the same mutation
func IsSameToolMutationAction(existing, next ToolActionRef) bool {
	// If either has a fingerprint, compare fingerprints
	if existing.ActionFingerprint != "" || next.ActionFingerprint != "" {
		// Fail closed: only match when both fingerprints exist and match
		return existing.ActionFingerprint != "" &&
			next.ActionFingerprint != "" &&
			existing.ActionFingerprint == next.ActionFingerprint
	}

	// Fallback to name + meta comparison
	return existing.ToolName == next.ToolName &&
		existing.Meta == next.Meta
}

// ─── Fingerprint Tracking ─────────────────────────────────────────────────────

// MutationTracker tracks tool mutation fingerprints within a session
type MutationTracker struct {
	seen map[string]int // fingerprint -> count
}

// NewMutationTracker creates a new mutation tracker
func NewMutationTracker() *MutationTracker {
	return &MutationTracker{
		seen: make(map[string]int),
	}
}

// Track records a mutation and returns true if it's a duplicate
func (t *MutationTracker) Track(fingerprint string) bool {
	if fingerprint == "" {
		return false
	}

	count := t.seen[fingerprint]
	t.seen[fingerprint] = count + 1
	return count > 0
}

// TrackToolCall tracks a tool call and returns true if it's a duplicate mutation
func (t *MutationTracker) TrackToolCall(toolName string, args interface{}, meta string) bool {
	fingerprint := BuildToolActionFingerprint(toolName, args, meta)
	return t.Track(fingerprint)
}

// Count returns how many times a fingerprint has been seen
func (t *MutationTracker) Count(fingerprint string) int {
	return t.seen[fingerprint]
}

// Reset clears all tracked fingerprints
func (t *MutationTracker) Reset() {
	t.seen = make(map[string]int)
}

// All returns all tracked fingerprints
func (t *MutationTracker) All() map[string]int {
	result := make(map[string]int, len(t.seen))
	for k, v := range t.seen {
		result[k] = v
	}
	return result
}
