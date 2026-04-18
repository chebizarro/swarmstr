package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"metiq/internal/store/state"
	"strings"
	"unicode/utf8"
)

func normalizeACPParentContext(parent *ACPParentContextHint) *ACPParentContextHint {
	if parent == nil {
		return nil
	}
	out := &ACPParentContextHint{
		SessionID: strings.TrimSpace(parent.SessionID),
		AgentID:   strings.TrimSpace(parent.AgentID),
	}
	if out.SessionID == "" && out.AgentID == "" {
		return nil
	}
	return out
}

func normalizeACPEnabledToolList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
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
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeACPTaskSpec(task *state.TaskSpec, instructions string, memoryScope state.AgentMemoryScope, toolProfile string, enabledTools []string) (*state.TaskSpec, error) {
	if task == nil {
		return nil, nil
	}
	norm := task.Normalize()
	if norm.Instructions == "" {
		norm.Instructions = strings.TrimSpace(instructions)
	}
	if norm.Title == "" {
		norm.Title = deriveACPTaskTitle(norm.Instructions)
	}
	if norm.MemoryScope == "" {
		norm.MemoryScope = memoryScope
	}
	if norm.ToolProfile == "" {
		norm.ToolProfile = strings.TrimSpace(toolProfile)
	}
	if len(norm.EnabledTools) == 0 {
		norm.EnabledTools = append([]string(nil), enabledTools...)
	}
	if norm.MemoryScope != "" && !norm.MemoryScope.Valid() {
		return nil, fmt.Errorf("task.memory_scope must be one of: user, project, local")
	}
	if strings.TrimSpace(norm.Instructions) == "" {
		return nil, fmt.Errorf("task.instructions required")
	}
	if strings.TrimSpace(norm.Title) == "" {
		return nil, fmt.Errorf("task.title required")
	}
	if raw := strings.TrimSpace(string(norm.Status)); raw != "" && !norm.Status.Valid() {
		return nil, fmt.Errorf("task.status is invalid")
	}
	if raw := strings.TrimSpace(string(norm.Priority)); raw != "" && !norm.Priority.Valid() {
		return nil, fmt.Errorf("task.priority is invalid")
	}
	for i, output := range norm.ExpectedOutputs {
		if strings.TrimSpace(output.Name) == "" {
			return nil, fmt.Errorf("task.expected_outputs[%d].name required", i)
		}
	}
	for i, criterion := range norm.AcceptanceCriteria {
		if strings.TrimSpace(criterion.Description) == "" {
			return nil, fmt.Errorf("task.acceptance_criteria[%d].description required", i)
		}
	}
	return &norm, nil
}

func deriveACPTaskTitle(instructions string) string {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return "task"
	}
	if idx := strings.IndexByte(instructions, '\n'); idx >= 0 {
		instructions = strings.TrimSpace(instructions[:idx])
	}
	if len(instructions) > 96 {
		instructions = strings.TrimSpace(instructions[:96])
	}
	if instructions == "" {
		return "task"
	}
	return instructions
}

func decodeMethodParams[T any](params json.RawMessage) (T, error) {
	var out T
	if len(bytes.TrimSpace(params)) == 0 {
		return out, nil
	}
	params = normalizeObjectParamAliases(params)
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("invalid params")
	}
	return out, nil
}

var objectParamAliases = map[string]string{
	"sessionId":        "session_id",
	"session_key":      "sessionKey",
	"runId":            "run_id",
	"timeoutMs":        "timeout_ms",
	"targetPubKey":     "target_pubkey",
	"peerPubKey":       "peer_pubkey",
	"contextMessages":  "context_messages",
	"memoryScope":      "memory_scope",
	"toolProfile":      "tool_profile",
	"enabledTools":     "enabled_tools",
	"parentContext":    "parent_context",
	"requestId":        "request_id",
	"expectedVersion":  "expected_version",
	"expectedEvent":    "expected_event",
	"agentId":          "agent_id",
	"taskId":           "task_id",
	"goalId":           "goal_id",
	"parentTaskId":     "parent_task_id",
	"planId":           "plan_id",
	"assignedAgent":    "assigned_agent",
	"currentRunId":     "current_run_id",
	"lastRunId":        "last_run_id",
	"createdAt":        "created_at",
	"updatedAt":        "updated_at",
	"runsLimit":        "runs_limit",
	"installId":        "install_id",
	"skillKey":         "skill_key",
	"apiKey":           "api_key",
	"includePlugins":   "include_plugins",
	"includeEvents":    "include_events",
	"includeLogs":      "include_logs",
	"eventCursor":      "event_cursor",
	"logCursor":        "log_cursor",
	"eventLimit":       "event_limit",
	"logLimit":         "log_limit",
	"maxBytes":         "max_bytes",
	"waitTimeoutMs":    "wait_timeout_ms",
	"channelId":        "channel_id",
	"nodeId":           "node_id",
	"maxItems":         "max_items",
	"ttlMs":            "ttl_ms",
	"deviceId":         "device_id",
	"instanceId":       "instance_id",
	"lastInputSeconds": "last_input_seconds",
	"displayName":      "display_name",
	"coreVersion":      "core_version",
	"uiVersion":        "ui_version",
	"deviceFamily":     "device_family",
	"modelIdentifier":  "model_identifier",
	"remoteIp":         "remote_ip",
	"start_date":       "startDate",
	"end_date":         "endDate",
	"utc_offset":       "utcOffset",
	"base_hash":            "baseHash",
	"restart_delay_ms":     "restartDelayMs",
	"replyTo":              "reply_to",
	"replyChannel":         "reply_channel",
	"accountId":            "account_id",
	"replyAccountId":       "reply_account_id",
	"threadId":             "thread_id",
	"groupId":              "group_id",
	"groupChannel":         "group_channel",
	"groupSpace":           "group_space",
	"bestEffortDeliver":    "best_effort_deliver",
	"extraSystemPrompt":    "extra_system_prompt",
	"internalEvents":       "internal_events",
	"inputProvenance":      "input_provenance",
	"deleteFiles":          "delete_files",
	"gifPlayback":          "gif_playback",
	"commandArgv":          "command_argv",
	"systemRunPlan":        "system_run_plan",
	"resolvedPath":         "resolved_path",
	"turnSourceChannel":    "turn_source_channel",
	"turnSourceTo":         "turn_source_to",
	"turnSourceAccountId":  "turn_source_account_id",
	"turnSourceThreadId":   "turn_source_thread_id",
	"twoPhase":             "two_phase",
	"childSessionKey":      "child_session_key",
	"childSessionId":       "child_session_id",
	"announceType":         "announce_type",
	"taskLabel":            "task_label",
	"statusLabel":          "status_label",
	"statsLine":            "stats_line",
	"replyInstruction":     "reply_instruction",
	"originSessionId":      "origin_session_id",
	"sourceSessionKey":     "source_session_key",
	"sourceChannel":        "source_channel",
	"sourceTool":           "source_tool",

	"parentSessionId":      "parent_session_id",
}

func normalizeObjectParamAliases(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return raw
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return raw
	}
	changed := false
	for alias, canonical := range objectParamAliases {
		value, ok := payload[alias]
		if !ok {
			continue
		}
		if _, exists := payload[canonical]; !exists {
			payload[canonical] = value
		}
		delete(payload, alias)
		changed = true
	}
	if !changed {
		return raw
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return normalized
}

func isJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func normalizeLimit(value, def, max int) int {
	if value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}

func normalizeListName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if utf8.RuneCountInString(name) > 64 {
		name = truncateRunes(name, 64)
	}
	return name
}

func normalizeAgentID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if utf8.RuneCountInString(id) > 64 {
		id = truncateRunes(id, 64)
	}
	return id
}

func isSafeAgentFileName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	if strings.ContainsAny(name, "\\/") {
		return false
	}
	return true
}

func isSafePluginID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if len(id) > 100 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '@' || r == '.') {
			return false
		}
	}
	if strings.Contains(id, "..") {
		return false
	}
	if strings.HasPrefix(id, ".") || strings.HasPrefix(id, "-") {
		return false
	}
	return true
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boolPtr(v bool) *bool {
	return &v
}

func compactStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compactObjectSlice(values []map[string]any) []map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		cp := make(map[string]any, len(value))
		for k, v := range value {
			cp[k] = v
		}
		out = append(out, cp)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isValidNostrIdentifier(id string) bool {
	if strings.HasPrefix(id, "npub1") && len(id) == 63 {
		return true
	}
	if len(id) == 64 {
		for _, c := range id {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return false
			}
		}
		return true
	}
	return false
}

func decodeWritePrecondition(raw json.RawMessage, expectedVersion *int, expectedEvent *string, baseHash *string) (bool, error) {
	raw = normalizeObjectParamAliases(raw)
	var pre struct {
		ExpectedVersion *int   `json:"expected_version"`
		ExpectedEvent   string `json:"expected_event"`
		BaseHash        string `json:"baseHash"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pre); err != nil {
		return false, err
	}
	expectedVersionSet := false
	if pre.ExpectedVersion != nil {
		expectedVersionSet = true
		*expectedVersion = *pre.ExpectedVersion
	}
	*expectedEvent = pre.ExpectedEvent
	if baseHash != nil {
		*baseHash = pre.BaseHash
	}
	return expectedVersionSet, nil
}
