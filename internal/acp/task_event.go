package acp

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
	"metiq/internal/store/state"
)

const TaskEnvelopeVersion = 1

// TaskEnvelope is the canonical content payload for a kind:38383 task event.
type TaskEnvelope struct {
	Version         int              `json:"version"`
	Task            state.TaskSpec   `json:"task"`
	ContextMessages []map[string]any `json:"context_messages,omitempty"`
	ParentContext   *ParentContext   `json:"parent_context,omitempty"`
	TimeoutMS       int64            `json:"timeout_ms,omitempty"`
	ReplyTo         string           `json:"reply_to,omitempty"`
	SenderPubKey    string           `json:"sender_pubkey,omitempty"`
}

// TaskEventEnvelope is the JSON-safe transport form of a kind:38383 event.
type TaskEventEnvelope struct {
	ID        string     `json:"id,omitempty"`
	PubKey    string     `json:"pubkey,omitempty"`
	CreatedAt int64      `json:"created_at,omitempty"`
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags,omitempty"`
	Content   string     `json:"content"`
	Sig       string     `json:"sig,omitempty"`
}

func BuildTaskEnvelope(taskID, senderPubKey string, p TaskPayload) TaskEnvelope {
	task := buildCanonicalTaskSpec(taskID, p)
	return TaskEnvelope{
		Version:         TaskEnvelopeVersion,
		Task:            task,
		ContextMessages: cloneContextMessages(p.ContextMessages),
		ParentContext:   cloneParentContext(p.ParentContext),
		TimeoutMS:       p.TimeoutMS,
		ReplyTo:         strings.TrimSpace(p.ReplyTo),
		SenderPubKey:    strings.TrimSpace(senderPubKey),
	}
}

func BuildUnsignedTaskEvent(senderPubKey string, env TaskEnvelope) (TaskEventEnvelope, error) {
	env.Version = maxTaskEnvelopeVersion(env.Version)
	env.Task = env.Task.Normalize()
	if env.Task.Title == "" {
		env.Task.Title = deriveTaskTitle(env.Task.Instructions)
	}
	if err := env.Task.Validate(); err != nil {
		return TaskEventEnvelope{}, err
	}
	content, err := json.Marshal(env)
	if err != nil {
		return TaskEventEnvelope{}, err
	}
	return TaskEventEnvelope{
		PubKey:    strings.TrimSpace(strings.ToLower(senderPubKey)),
		CreatedAt: time.Now().Unix(),
		Kind:      int(events.KindTask),
		Tags:      BuildTaskEventTags(env.Task),
		Content:   string(content),
	}, nil
}

func BuildTaskEventTags(task state.TaskSpec) [][]string {
	task = task.Normalize()
	tags := [][]string{{"d", task.TaskID}, {"t", "task"}}
	if task.GoalID != "" {
		tags = append(tags, []string{"goal", task.GoalID})
	}
	if task.ParentTaskID != "" {
		tags = append(tags, []string{"parent", task.ParentTaskID})
	}
	if task.PlanID != "" {
		tags = append(tags, []string{"plan", task.PlanID})
	}
	if task.SessionID != "" {
		tags = append(tags, []string{"session", task.SessionID})
	}
	if task.AssignedAgent != "" {
		tags = append(tags, []string{"agent", task.AssignedAgent})
	}
	if task.MemoryScope != "" {
		tags = append(tags, []string{"scope", string(task.MemoryScope)})
	}
	if task.Status != "" {
		tags = append(tags, []string{"status", string(task.Status)})
	}
	if task.Priority != "" {
		tags = append(tags, []string{"priority", string(task.Priority)})
	}
	if task.ToolProfile != "" {
		tags = append(tags, []string{"tool_profile", task.ToolProfile})
	}
	return tags
}

func ParseTaskEvent(ev *nostr.Event) (TaskEnvelope, error) {
	if ev == nil {
		return TaskEnvelope{}, fmt.Errorf("task event is nil")
	}
	if ev.Kind != nostr.Kind(events.KindTask) {
		return TaskEnvelope{}, fmt.Errorf("unexpected task kind %d", ev.Kind)
	}
	if strings.TrimSpace(ev.Content) == "" {
		return TaskEnvelope{}, fmt.Errorf("task event content is required")
	}
	var env TaskEnvelope
	if err := json.Unmarshal([]byte(ev.Content), &env); err != nil {
		return TaskEnvelope{}, err
	}
	env.Version = maxTaskEnvelopeVersion(env.Version)
	env.SenderPubKey = firstNonEmptyTrimmed(env.SenderPubKey, ev.PubKey.Hex())
	env.Task = env.Task.Normalize()
	applyTaskTagFallbacks(&env.Task, ev.Tags)
	if env.Task.Title == "" {
		env.Task.Title = deriveTaskTitle(env.Task.Instructions)
	}
	if err := env.Task.Validate(); err != nil {
		return TaskEnvelope{}, err
	}
	return env, nil
}

func (e TaskEventEnvelope) ToNostrEvent() (*nostr.Event, error) {
	if strings.TrimSpace(e.Content) == "" {
		return nil, fmt.Errorf("task event content is required")
	}
	ev := &nostr.Event{
		Kind:      nostr.Kind(e.Kind),
		CreatedAt: nostr.Timestamp(e.CreatedAt),
		Tags:      nostr.Tags(make([]nostr.Tag, 0, len(e.Tags))),
		Content:   e.Content,
	}
	for _, tag := range e.Tags {
		ev.Tags = append(ev.Tags, nostr.Tag(tag))
	}
	if id := strings.TrimSpace(e.ID); id != "" {
		parsed, err := nostr.IDFromHex(id)
		if err != nil {
			return nil, fmt.Errorf("parse task event id: %w", err)
		}
		ev.ID = parsed
	}
	if pubkey := strings.TrimSpace(e.PubKey); pubkey != "" {
		parsed, err := nostr.PubKeyFromHex(pubkey)
		if err != nil {
			return nil, fmt.Errorf("parse task event pubkey: %w", err)
		}
		ev.PubKey = parsed
	}
	if sig := strings.TrimSpace(e.Sig); sig != "" {
		raw, err := hex.DecodeString(sig)
		if err != nil {
			return nil, fmt.Errorf("parse task event sig: %w", err)
		}
		if len(raw) != len(ev.Sig) {
			return nil, fmt.Errorf("parse task event sig: expected %d bytes, got %d", len(ev.Sig), len(raw))
		}
		copy(ev.Sig[:], raw)
	}
	return ev, nil
}

func TaskEventEnvelopeFromNostr(ev nostr.Event) TaskEventEnvelope {
	out := TaskEventEnvelope{
		CreatedAt: int64(ev.CreatedAt),
		Kind:      int(ev.Kind),
		Content:   ev.Content,
	}
	if ev.ID.String() != "0000000000000000000000000000000000000000000000000000000000000000" {
		out.ID = ev.ID.Hex()
	}
	if ev.PubKey.String() != "0000000000000000000000000000000000000000000000000000000000000000" {
		out.PubKey = ev.PubKey.Hex()
	}
	if !isZeroSig(ev.Sig) {
		out.Sig = hex.EncodeToString(ev.Sig[:])
	}
	if len(ev.Tags) > 0 {
		out.Tags = make([][]string, 0, len(ev.Tags))
		for _, tag := range ev.Tags {
			out.Tags = append(out.Tags, append([]string(nil), tag...))
		}
	}
	return out
}

func buildCanonicalTaskSpec(taskID string, p TaskPayload) state.TaskSpec {
	var task state.TaskSpec
	if p.Task != nil {
		task = p.Task.Normalize()
	}
	if task.TaskID == "" {
		task.TaskID = strings.TrimSpace(taskID)
	}
	if task.Instructions == "" {
		task.Instructions = strings.TrimSpace(p.Instructions)
	}
	if task.Title == "" {
		task.Title = deriveTaskTitle(task.Instructions)
	}
	if task.MemoryScope == "" {
		task.MemoryScope = p.MemoryScope
	}
	if task.ToolProfile == "" {
		task.ToolProfile = strings.TrimSpace(p.ToolProfile)
	}
	if len(task.EnabledTools) == 0 {
		task.EnabledTools = cloneStrings(p.EnabledTools)
	}
	if task.SessionID == "" && p.ParentContext != nil {
		task.SessionID = strings.TrimSpace(p.ParentContext.SessionID)
	}
	if task.AssignedAgent == "" && p.ParentContext != nil {
		task.AssignedAgent = strings.TrimSpace(p.ParentContext.AgentID)
	}
	return task.Normalize()
}

func applyTaskTagFallbacks(task *state.TaskSpec, tags nostr.Tags) {
	if task == nil {
		return
	}
	if task.TaskID == "" {
		task.TaskID = taskTagValue(tags, "d")
	}
	if task.GoalID == "" {
		task.GoalID = taskTagValue(tags, "goal")
	}
	if task.ParentTaskID == "" {
		task.ParentTaskID = taskTagValue(tags, "parent")
	}
	if task.PlanID == "" {
		task.PlanID = taskTagValue(tags, "plan")
	}
	if task.SessionID == "" {
		task.SessionID = taskTagValue(tags, "session")
	}
	if task.AssignedAgent == "" {
		task.AssignedAgent = taskTagValue(tags, "agent")
	}
	if task.MemoryScope == "" {
		task.MemoryScope = state.NormalizeAgentMemoryScope(taskTagValue(tags, "scope"))
	}
	if task.Status == "" {
		task.Status = state.NormalizeTaskStatus(taskTagValue(tags, "status"))
	}
	if task.Priority == "" {
		task.Priority = state.NormalizeTaskPriority(taskTagValue(tags, "priority"))
	}
	if task.ToolProfile == "" {
		task.ToolProfile = taskTagValue(tags, "tool_profile")
	}
}

func taskTagValue(tags nostr.Tags, name string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && strings.TrimSpace(tag[0]) == name {
			return strings.TrimSpace(tag[1])
		}
	}
	return ""
}

func deriveTaskTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "task"
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}
	if len(text) > 96 {
		text = strings.TrimSpace(text[:96])
	}
	if text == "" {
		return "task"
	}
	return text
}

func maxTaskEnvelopeVersion(version int) int {
	if version <= 0 {
		return TaskEnvelopeVersion
	}
	return version
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func isZeroSig(sig [64]byte) bool {
	for _, b := range sig {
		if b != 0 {
			return false
		}
	}
	return true
}
