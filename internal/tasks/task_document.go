package tasks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"metiq/internal/store/state"
)

const (
	TaskStateSchemaV2        = "cascadia.task-state.v2"
	TaskSnapshotMetaKey      = "cascadia.task-state.v2.snapshot"
	ClaimOriginIDMetaKey     = "cascadia.claim.origin_id"
	ClaimOriginPubkeyMetaKey = "cascadia.claim.origin_pubkey"
)

// TaskDocument is the complete NIP-CAS-0006 wire snapshot. Optional workflow
// extension bodies are retained as raw JSON because the ledger does not act on
// them, but they must survive read-modify-write cycles.
type TaskDocument struct {
	SchemaVersion      string            `json:"schema_version,omitempty"`
	RecordType         string            `json:"_type,omitempty"`
	ID                 string            `json:"id"`
	Title              string            `json:"title"`
	Description        string            `json:"description,omitempty"`
	Status             string            `json:"status"`
	Priority           int               `json:"priority"`
	IssueType          string            `json:"issue_type,omitempty"`
	Assignee           string            `json:"assignee,omitempty"`
	Owner              string            `json:"owner,omitempty"`
	CreatedAt          string            `json:"created_at,omitempty"`
	CreatedBy          string            `json:"created_by,omitempty"`
	UpdatedAt          string            `json:"updated_at,omitempty"`
	StartedAt          string            `json:"started_at,omitempty"`
	ClaimedAt          string            `json:"claimed_at,omitempty"`
	BlockedAt          string            `json:"blocked_at,omitempty"`
	ReviewedAt         string            `json:"reviewed_at,omitempty"`
	ClosedAt           string            `json:"closed_at,omitempty"`
	CloseReason        string            `json:"close_reason,omitempty"`
	StatusReason       string            `json:"status_reason,omitempty"`
	BlockerDescription string            `json:"blocker_description,omitempty"`
	AcceptanceCriteria string            `json:"acceptance_criteria,omitempty"`
	Notes              string            `json:"notes,omitempty"`
	Labels             []string          `json:"labels,omitempty"`
	Dependencies       []TaskDependency  `json:"dependencies,omitempty"`
	Comments           []json.RawMessage `json:"comments,omitempty"`
	DependencyCount    *int              `json:"dependency_count,omitempty"`
	DependentCount     *int              `json:"dependent_count,omitempty"`
	CommentCount       *int              `json:"comment_count,omitempty"`
	Checkpoints        []json.RawMessage `json:"checkpoints,omitempty"`
	Branch             string            `json:"branch,omitempty"`
	Commits            []string          `json:"commits,omitempty"`
	Patches            []json.RawMessage `json:"patches,omitempty"`
	PullRequests       []string          `json:"pull_requests,omitempty"`
	Evidence           []json.RawMessage `json:"evidence,omitempty"`
	Review             json.RawMessage   `json:"review,omitempty"`
	QualityGate        json.RawMessage   `json:"quality_gate,omitempty"`
	Project            string            `json:"project,omitempty"`
	Epic               string            `json:"epic,omitempty"`
	Queue              string            `json:"queue,omitempty"`
	Repository         string            `json:"repository,omitempty"`
	ExecutionAttempts  []json.RawMessage `json:"execution_attempts,omitempty"`
	AgentSessions      []json.RawMessage `json:"agent_sessions,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// TaskDependency is a Beads-compatible typed dependency.
type TaskDependency struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
	CreatedAt   string `json:"created_at,omitempty"`
	CreatedBy   string `json:"created_by,omitempty"`
	Metadata    string `json:"metadata,omitempty"`
}

// ParseTaskDocument strictly decodes and normalizes one canonical v2 snapshot.
func ParseTaskDocument(data []byte) (TaskDocument, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return TaskDocument{}, err
	}
	if _, ok := raw["_type"]; ok {
		return TaskDocument{}, fmt.Errorf("canonical task content must not contain _type")
	}
	for _, required := range []string{"schema_version", "id", "title", "status", "priority"} {
		if _, ok := raw[required]; !ok {
			return TaskDocument{}, fmt.Errorf("task field %q is required", required)
		}
	}
	if p := raw["priority"]; len(p) > 0 {
		var priority int
		if err := json.Unmarshal(p, &priority); err != nil {
			return TaskDocument{}, fmt.Errorf("canonical priority must be numeric")
		}
	}
	var doc TaskDocument
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return TaskDocument{}, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return TaskDocument{}, fmt.Errorf("trailing JSON")
	}
	if doc.SchemaVersion != TaskStateSchemaV2 {
		return TaskDocument{}, fmt.Errorf("unsupported schema_version %q", doc.SchemaVersion)
	}
	doc.RecordType = ""
	return NormalizeTaskDocument(doc)
}

// EncodeTaskDocument returns the canonical complete-state JSON representation.
func EncodeTaskDocument(doc TaskDocument) ([]byte, error) {
	normalized, err := NormalizeTaskDocument(doc)
	if err != nil {
		return nil, err
	}
	normalized.SchemaVersion = TaskStateSchemaV2
	normalized.RecordType = ""
	return json.Marshal(normalized)
}

// NormalizeTaskDocument validates the interoperable core and canonicalizes it.
func NormalizeTaskDocument(doc TaskDocument) (TaskDocument, error) {
	doc.SchemaVersion = strings.TrimSpace(doc.SchemaVersion)
	doc.RecordType = strings.TrimSpace(doc.RecordType)
	doc.ID = strings.TrimSpace(doc.ID)
	doc.Title = strings.TrimSpace(doc.Title)
	doc.Status = strings.ToLower(strings.TrimSpace(doc.Status))
	doc.Assignee = strings.TrimSpace(doc.Assignee)
	doc.Repository = strings.TrimSpace(doc.Repository)
	doc.Queue = strings.TrimSpace(doc.Queue)
	doc.Epic = strings.TrimSpace(doc.Epic)
	if doc.ID == "" || strings.HasPrefix(doc.ID, "task:") {
		return TaskDocument{}, fmt.Errorf("invalid task id")
	}
	if doc.Title == "" {
		return TaskDocument{}, fmt.Errorf("task title is required")
	}
	switch doc.Status {
	case "open", "in_progress", "blocked", "closed", "deferred":
	default:
		return TaskDocument{}, fmt.Errorf("invalid task status %q", doc.Status)
	}
	if doc.Priority < 0 || doc.Priority > 4 {
		return TaskDocument{}, fmt.Errorf("priority must be between 0 and 4")
	}
	var err error
	if doc.Labels, err = cleanTaskStrings("labels", doc.Labels); err != nil {
		return TaskDocument{}, err
	}
	if doc.Commits, err = cleanTaskStrings("commits", doc.Commits); err != nil {
		return TaskDocument{}, err
	}
	if doc.PullRequests, err = cleanTaskStrings("pull_requests", doc.PullRequests); err != nil {
		return TaskDocument{}, err
	}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"created_at", &doc.CreatedAt}, {"updated_at", &doc.UpdatedAt},
		{"started_at", &doc.StartedAt}, {"claimed_at", &doc.ClaimedAt},
		{"blocked_at", &doc.BlockedAt}, {"reviewed_at", &doc.ReviewedAt},
		{"closed_at", &doc.ClosedAt},
	} {
		*field.value, err = normalizeTaskTime(*field.value)
		if err != nil {
			return TaskDocument{}, fmt.Errorf("%s: %w", field.name, err)
		}
	}
	seenDeps := map[string]struct{}{}
	for i := range doc.Dependencies {
		dep := &doc.Dependencies[i]
		dep.IssueID = strings.TrimSpace(dep.IssueID)
		dep.DependsOnID = strings.TrimSpace(dep.DependsOnID)
		dep.Type = strings.ToLower(strings.TrimSpace(dep.Type))
		if dep.IssueID == "" {
			dep.IssueID = doc.ID
		}
		if dep.IssueID != doc.ID || dep.DependsOnID == "" {
			return TaskDocument{}, fmt.Errorf("invalid dependency for task %s", doc.ID)
		}
		switch dep.Type {
		case "blocks", "blocked-by", "parent-child", "discovered-from":
		default:
			return TaskDocument{}, fmt.Errorf("invalid dependency type %q", dep.Type)
		}
		dep.CreatedAt, err = normalizeTaskTime(dep.CreatedAt)
		if err != nil {
			return TaskDocument{}, fmt.Errorf("dependency created_at: %w", err)
		}
		key := dep.DependsOnID + "|" + dep.Type
		if _, ok := seenDeps[key]; ok {
			return TaskDocument{}, fmt.Errorf("duplicate dependency %q", key)
		}
		seenDeps[key] = struct{}{}
	}
	for name, count := range map[string]*int{
		"dependency_count": doc.DependencyCount,
		"dependent_count":  doc.DependentCount,
		"comment_count":    doc.CommentCount,
	} {
		if count != nil && *count < 0 {
			return TaskDocument{}, fmt.Errorf("%s cannot be negative", name)
		}
	}
	if doc.Metadata == nil {
		doc.Metadata = map[string]string{}
	}
	if doc.Repository != "" {
		if metadataRepo := strings.TrimSpace(doc.Metadata["nip34.repo_addr"]); metadataRepo != "" && metadataRepo != doc.Repository {
			return TaskDocument{}, fmt.Errorf("repository disagrees with nip34.repo_addr")
		}
		doc.Metadata["nip34.repo_addr"] = doc.Repository
	}
	if len(doc.Metadata) == 0 {
		doc.Metadata = nil
	}
	return doc, nil
}

func normalizeTaskTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", fmt.Errorf("invalid RFC3339 timestamp %q", value)
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func cleanTaskStrings(name string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s contain an empty value", name)
		}
		if _, ok := seen[value]; ok {
			return nil, fmt.Errorf("%s contain duplicate %q", name, value)
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

// DecodeBeadsJSONL imports issues.jsonl records as canonical v2 documents.
func DecodeBeadsJSONL(r io.Reader) ([]TaskDocument, error) {
	if r == nil {
		return nil, fmt.Errorf("beads reader is nil")
	}
	reader := bufio.NewReader(r)
	var docs []TaskDocument
	seen := map[string]struct{}{}
	lineNo := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNo++
			line = bytes.TrimSpace(line)
			if len(line) > 0 {
				doc, err := decodeBeadsRecord(line)
				if err != nil {
					return nil, fmt.Errorf("beads line %d: %w", lineNo, err)
				}
				if _, ok := seen[doc.ID]; ok {
					return nil, fmt.Errorf("beads line %d: duplicate task id %q", lineNo, doc.ID)
				}
				seen[doc.ID] = struct{}{}
				docs = append(docs, doc)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, readErr
		}
	}
	return docs, nil
}

func decodeBeadsRecord(data []byte) (TaskDocument, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return TaskDocument{}, err
	}
	if value, ok := raw["_type"]; ok {
		var recordType string
		if err := json.Unmarshal(value, &recordType); err != nil || recordType != "issue" {
			return TaskDocument{}, fmt.Errorf("_type must be issue")
		}
	}
	aliasTaskField(raw, "description", "body")
	aliasTaskField(raw, "created_at", "created")
	aliasTaskField(raw, "updated_at", "updated")
	aliasTaskField(raw, "claimed_at", "claimed")
	aliasTaskField(raw, "blocked_at", "blocked")
	aliasTaskField(raw, "reviewed_at", "reviewed")
	aliasTaskField(raw, "closed_at", "closed")
	var legacyDeps []string
	for _, key := range []string{"depends_on", "dependsOn"} {
		if value, ok := raw[key]; ok {
			if err := json.Unmarshal(value, &legacyDeps); err != nil {
				return TaskDocument{}, fmt.Errorf("%s must be a string array", key)
			}
			break
		}
	}
	if _, ok := raw["dependencies"]; !ok && len(legacyDeps) > 0 {
		var id string
		_ = json.Unmarshal(raw["id"], &id)
		deps := make([]TaskDependency, 0, len(legacyDeps))
		for _, depID := range legacyDeps {
			deps = append(deps, TaskDependency{IssueID: id, DependsOnID: depID, Type: "blocks"})
		}
		encoded, _ := json.Marshal(deps)
		raw["dependencies"] = encoded
	}
	if value, ok := raw["priority"]; ok {
		var priority int
		if err := json.Unmarshal(value, &priority); err != nil {
			var named string
			if err := json.Unmarshal(value, &named); err != nil {
				return TaskDocument{}, fmt.Errorf("priority must be numeric or P0-P4")
			}
			named = strings.ToUpper(strings.TrimSpace(named))
			if !strings.HasPrefix(named, "P") {
				return TaskDocument{}, fmt.Errorf("invalid priority %q", named)
			}
			parsed, err := strconv.Atoi(strings.TrimPrefix(named, "P"))
			if err != nil {
				return TaskDocument{}, fmt.Errorf("invalid priority %q", named)
			}
			priority = parsed
		}
		if priority == 9 {
			priority = 4
		}
		encoded, _ := json.Marshal(priority)
		raw["priority"] = encoded
	}
	raw["schema_version"], _ = json.Marshal(TaskStateSchemaV2)
	for _, key := range []string{"_type", "body", "created", "updated", "claimed", "blocked", "reviewed", "closed", "depends_on", "dependsOn"} {
		delete(raw, key)
	}
	canonical, err := json.Marshal(raw)
	if err != nil {
		return TaskDocument{}, err
	}
	return ParseTaskDocument(canonical)
}

func aliasTaskField(raw map[string]json.RawMessage, canonical, legacy string) {
	if _, ok := raw[canonical]; ok {
		return
	}
	if value, ok := raw[legacy]; ok {
		raw[canonical] = value
	}
}

// EncodeBeadsJSONL writes deterministic current-format Beads records.
func EncodeBeadsJSONL(w io.Writer, docs []TaskDocument) error {
	if w == nil {
		return fmt.Errorf("beads writer is nil")
	}
	normalized := make([]TaskDocument, len(docs))
	for i, doc := range docs {
		n, err := NormalizeTaskDocument(doc)
		if err != nil {
			return fmt.Errorf("task %q: %w", doc.ID, err)
		}
		normalized[i] = n
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	for _, doc := range normalized {
		doc.SchemaVersion = ""
		doc.RecordType = "issue"
		if err := encoder.Encode(doc); err != nil {
			return err
		}
	}
	return nil
}

// TaskDocumentFromLedger projects a ledger snapshot without discarding a
// preserved v2 base document.
func TaskDocumentFromLedger(entry *LedgerEntry) (TaskDocument, error) {
	if entry == nil {
		return TaskDocument{}, fmt.Errorf("ledger entry is nil")
	}
	task := entry.Task.Normalize()
	var doc TaskDocument
	if raw, ok := task.Meta[TaskSnapshotMetaKey].(string); ok && strings.TrimSpace(raw) != "" {
		parsed, err := ParseTaskDocument([]byte(raw))
		if err != nil {
			return TaskDocument{}, fmt.Errorf("preserved task snapshot: %w", err)
		}
		doc = parsed
	}
	if doc.ID == "" {
		doc = TaskDocument{SchemaVersion: TaskStateSchemaV2, ID: task.TaskID}
	}
	doc.Title = task.Title
	if doc.Description == "" || doc.Description == doc.Title {
		doc.Description = task.Instructions
	}
	doc.Status = wireStatusFromLedger(task.Status)
	if explicit, ok := task.Meta["cascadia.priority"].(float64); ok && explicit >= 0 && explicit <= 4 {
		doc.Priority = int(explicit)
	} else if explicit, ok := task.Meta["cascadia.priority"].(int); ok && explicit >= 0 && explicit <= 4 {
		doc.Priority = explicit
	} else {
		doc.Priority = wirePriorityFromLedger(task.Priority)
	}
	doc.Assignee = task.AssignedAgent
	if doc.CreatedAt == "" && task.CreatedAt > 0 {
		doc.CreatedAt = time.Unix(task.CreatedAt, 0).UTC().Format(time.RFC3339)
	}
	updated := task.UpdatedAt
	if updated == 0 {
		updated = entry.UpdatedAt
	}
	if updated > 0 {
		doc.UpdatedAt = time.Unix(updated, 0).UTC().Format(time.RFC3339)
	}
	if len(doc.Dependencies) == 0 {
		for _, depID := range task.Dependencies {
			doc.Dependencies = append(doc.Dependencies, TaskDependency{IssueID: task.TaskID, DependsOnID: depID, Type: "blocks"})
		}
		if task.ParentTaskID != "" {
			doc.Dependencies = append(doc.Dependencies, TaskDependency{IssueID: task.TaskID, DependsOnID: task.ParentTaskID, Type: "parent-child"})
		}
	}
	return NormalizeTaskDocument(doc)
}

// TaskSpecFromDocument maps an effective wire snapshot into the existing ledger
// while retaining the complete canonical document for future publication.
func TaskSpecFromDocument(ctx context.Context, ledger *Ledger, doc TaskDocument) (state.TaskSpec, error) {
	normalized, err := NormalizeTaskDocument(doc)
	if err != nil {
		return state.TaskSpec{}, err
	}
	var task state.TaskSpec
	if ledger != nil {
		if existing, getErr := ledger.SnapshotTask(ctx, normalized.ID); getErr == nil && existing != nil {
			task = existing.Task
		}
	}
	task.TaskID = normalized.ID
	task.Title = normalized.Title
	task.Instructions = firstTaskText(normalized.Description, normalized.Notes, normalized.Title)
	task.Status = ledgerStatusFromWire(normalized.Status)
	task.Priority = ledgerPriorityFromWire(normalized.Priority)
	task.AssignedAgent = normalized.Assignee
	task.Dependencies = nil
	task.ParentTaskID = ""
	seen := map[string]struct{}{}
	for _, dep := range normalized.Dependencies {
		if _, ok := seen[dep.DependsOnID]; !ok {
			seen[dep.DependsOnID] = struct{}{}
			task.Dependencies = append(task.Dependencies, dep.DependsOnID)
		}
		if dep.Type == "parent-child" && task.ParentTaskID == "" {
			task.ParentTaskID = dep.DependsOnID
		}
	}
	if parsed, parseErr := time.Parse(time.RFC3339Nano, normalized.CreatedAt); parseErr == nil {
		task.CreatedAt = parsed.Unix()
	}
	if parsed, parseErr := time.Parse(time.RFC3339Nano, normalized.UpdatedAt); parseErr == nil {
		task.UpdatedAt = parsed.Unix()
	}
	if task.CreatedAt == 0 {
		task.CreatedAt = time.Now().Unix()
	}
	if task.UpdatedAt == 0 {
		task.UpdatedAt = task.CreatedAt
	}
	if task.Meta == nil {
		task.Meta = map[string]any{}
	}
	raw, err := EncodeTaskDocument(normalized)
	if err != nil {
		return state.TaskSpec{}, err
	}
	task.Meta[TaskSnapshotMetaKey] = string(raw)
	task.Meta["cascadia.priority"] = normalized.Priority
	return task.Normalize(), nil
}

func firstTaskText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "task"
}

func wireStatusFromLedger(status state.TaskStatus) string {
	switch status {
	case state.TaskStatusInProgress, state.TaskStatusAwaitingApproval, state.TaskStatusVerifying:
		return "in_progress"
	case state.TaskStatusBlocked, state.TaskStatusFailed:
		return "blocked"
	case state.TaskStatusCompleted:
		return "closed"
	case state.TaskStatusCancelled:
		return "deferred"
	default:
		return "open"
	}
}

func ledgerStatusFromWire(status string) state.TaskStatus {
	switch status {
	case "in_progress":
		return state.TaskStatusInProgress
	case "blocked":
		return state.TaskStatusBlocked
	case "closed":
		return state.TaskStatusCompleted
	case "deferred":
		return state.TaskStatusCancelled
	default:
		return state.TaskStatusReady
	}
}

func wirePriorityFromLedger(priority state.TaskPriority) int {
	switch priority {
	case state.TaskPriorityHigh:
		return 1
	case state.TaskPriorityLow:
		return 3
	default:
		return 2
	}
}

func ledgerPriorityFromWire(priority int) state.TaskPriority {
	switch {
	case priority <= 1:
		return state.TaskPriorityHigh
	case priority >= 3:
		return state.TaskPriorityLow
	default:
		return state.TaskPriorityMedium
	}
}
