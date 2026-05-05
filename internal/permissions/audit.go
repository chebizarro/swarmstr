package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ─── Audit Event Types ───────────────────────────────────────────────────────

// AuditEventType identifies the type of audit event.
type AuditEventType string

const (
	// AuditEventDecision records a permission decision.
	AuditEventDecision AuditEventType = "decision"
	// AuditEventRuleAdded records a rule addition.
	AuditEventRuleAdded AuditEventType = "rule_added"
	// AuditEventRuleRemoved records a rule removal.
	AuditEventRuleRemoved AuditEventType = "rule_removed"
	// AuditEventRuleModified records a rule modification.
	AuditEventRuleModified AuditEventType = "rule_modified"
	// AuditEventOverride records a manual permission override.
	AuditEventOverride AuditEventType = "override"
	// AuditEventEscalation records a permission escalation.
	AuditEventEscalation AuditEventType = "escalation"
)

// ─── Audit Event ─────────────────────────────────────────────────────────────

// AuditEvent represents a single audit log entry.
type AuditEvent struct {
	// ID is a unique identifier for this event.
	ID string `json:"id"`

	// Type classifies the event.
	Type AuditEventType `json:"type"`

	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`

	// ToolName is the tool involved (for decision events).
	ToolName string `json:"tool_name,omitempty"`

	// Category is the tool capability category (for decision events).
	Category ToolCategory `json:"category,omitempty"`

	// Origin is the tool provenance kind (for decision events).
	Origin ToolOrigin `json:"origin,omitempty"`

	// OriginName is the provenance-specific source name (for decision events).
	OriginName string `json:"origin_name,omitempty"`

	// Behavior is the decision made (for decision events).
	Behavior Behavior `json:"behavior,omitempty"`

	// Reason explains the decision or action.
	Reason string `json:"reason,omitempty"`

	// RuleID is the rule involved (for rule events).
	RuleID string `json:"rule_id,omitempty"`

	// UserID identifies the user involved.
	UserID string `json:"user_id,omitempty"`

	// ProjectID identifies the project involved.
	ProjectID string `json:"project_id,omitempty"`

	// AgentID identifies the agent involved.
	AgentID string `json:"agent_id,omitempty"`

	// SessionID identifies the session involved.
	SessionID string `json:"session_id,omitempty"`

	// Details contains additional event-specific data.
	Details map[string]any `json:"details,omitempty"`
}

// ─── Auditor ─────────────────────────────────────────────────────────────────

// Auditor manages audit logging for the permission system.
type Auditor struct {
	mu        sync.Mutex
	baseDir   string
	counter   int64
	file      *os.File
	buffer    []AuditEvent
	flushSize int
}

// NewAuditor creates a new auditor.
func NewAuditor(baseDir string) *Auditor {
	return &Auditor{
		baseDir:   baseDir,
		buffer:    make([]AuditEvent, 0, 100),
		flushSize: 100,
	}
}

// LogEvent records an audit event.
func (a *Auditor) LogEvent(event AuditEvent) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Generate ID
	id := atomic.AddInt64(&a.counter, 1)
	event.ID = fmt.Sprintf("audit-%d-%d", event.Timestamp.UnixNano(), id)

	// Add to buffer
	a.buffer = append(a.buffer, event)

	// Flush if buffer is full
	if len(a.buffer) >= a.flushSize {
		a.flush()
	}

	return event.ID
}

// LogDecision records a permission decision.
func (a *Auditor) LogDecision(req *ToolRequest, decision *Decision) string {
	return a.LogEvent(AuditEvent{
		Type:       AuditEventDecision,
		Timestamp:  time.Now(),
		ToolName:   req.ToolName,
		Category:   req.Category,
		Origin:     req.Origin,
		OriginName: req.OriginName,
		Behavior:   decision.Behavior,
		Reason:     decision.Reason,
		UserID:     req.UserID,
		ProjectID:  req.ProjectID,
		AgentID:    req.AgentID,
		SessionID:  req.SessionID,
		Details: map[string]any{
			"content":       truncate(req.Content, 200),
			"matched_rules": len(decision.MatchedRules),
			"scope":         decision.Scope,
		},
	})
}

// LogOverride records a manual permission override.
func (a *Auditor) LogOverride(req *ToolRequest, originalBehavior, newBehavior Behavior, overrideBy, reason string) string {
	return a.LogEvent(AuditEvent{
		Type:       AuditEventOverride,
		Timestamp:  time.Now(),
		ToolName:   req.ToolName,
		Category:   req.Category,
		Origin:     req.Origin,
		OriginName: req.OriginName,
		Behavior:   newBehavior,
		Reason:     reason,
		UserID:     req.UserID,
		ProjectID:  req.ProjectID,
		AgentID:    req.AgentID,
		SessionID:  req.SessionID,
		Details: map[string]any{
			"original_behavior": originalBehavior,
			"override_by":       overrideBy,
		},
	})
}

// LogEscalation records a permission escalation.
func (a *Auditor) LogEscalation(req *ToolRequest, fromScope, toScope Scope, reason string) string {
	return a.LogEvent(AuditEvent{
		Type:       AuditEventEscalation,
		Timestamp:  time.Now(),
		ToolName:   req.ToolName,
		Category:   req.Category,
		Origin:     req.Origin,
		OriginName: req.OriginName,
		Reason:     reason,
		UserID:     req.UserID,
		ProjectID:  req.ProjectID,
		AgentID:    req.AgentID,
		SessionID:  req.SessionID,
		Details: map[string]any{
			"from_scope": fromScope,
			"to_scope":   toScope,
		},
	})
}

// Flush writes buffered events to disk.
func (a *Auditor) Flush() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.flush()
}

func (a *Auditor) flush() error {
	if len(a.buffer) == 0 {
		return nil
	}

	// Ensure directory exists
	if err := os.MkdirAll(a.baseDir, 0755); err != nil {
		return fmt.Errorf("create audit dir: %w", err)
	}

	// Open or create log file for today
	today := time.Now().Format("2006-01-02")
	path := filepath.Join(a.baseDir, fmt.Sprintf("audit-%s.jsonl", today))

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	defer file.Close()

	// Write buffered events
	encoder := json.NewEncoder(file)
	for _, event := range a.buffer {
		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("write audit event: %w", err)
		}
	}

	// Clear buffer
	a.buffer = a.buffer[:0]

	return nil
}

// EntryCount returns the total number of audit entries.
func (a *Auditor) EntryCount() int64 {
	return atomic.LoadInt64(&a.counter)
}

// Query returns audit events matching the given criteria.
func (a *Auditor) Query(opts AuditQueryOptions) ([]AuditEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Flush pending events first
	if err := a.flush(); err != nil {
		return nil, err
	}

	var results []AuditEvent

	// Determine which files to read
	entries, err := os.ReadDir(a.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return results, nil
		}
		return nil, fmt.Errorf("read audit dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}

		// Check date range
		if opts.Since != nil || opts.Until != nil {
			// Parse date from filename (audit-YYYY-MM-DD.jsonl)
			name := entry.Name()
			if len(name) >= 21 {
				dateStr := name[6:16]
				fileDate, err := time.Parse("2006-01-02", dateStr)
				if err == nil {
					if opts.Since != nil && fileDate.Before(*opts.Since) {
						continue
					}
					if opts.Until != nil && fileDate.After(*opts.Until) {
						continue
					}
				}
			}
		}

		// Read file
		path := filepath.Join(a.baseDir, entry.Name())
		events, err := a.readAuditFile(path, opts)
		if err != nil {
			continue // Skip corrupt files
		}
		results = append(results, events...)

		// Check limit
		if opts.Limit > 0 && len(results) >= opts.Limit {
			results = results[:opts.Limit]
			break
		}
	}

	return results, nil
}

func (a *Auditor) readAuditFile(path string, opts AuditQueryOptions) ([]AuditEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var events []AuditEvent
	decoder := json.NewDecoder(file)

	for decoder.More() {
		var event AuditEvent
		if err := decoder.Decode(&event); err != nil {
			continue // Skip malformed lines
		}

		// Apply filters
		if opts.Type != "" && event.Type != opts.Type {
			continue
		}
		if opts.ToolName != "" && event.ToolName != opts.ToolName {
			continue
		}
		if opts.UserID != "" && event.UserID != opts.UserID {
			continue
		}
		if opts.Behavior != "" && event.Behavior != opts.Behavior {
			continue
		}
		if opts.Since != nil && event.Timestamp.Before(*opts.Since) {
			continue
		}
		if opts.Until != nil && event.Timestamp.After(*opts.Until) {
			continue
		}

		events = append(events, event)
	}

	return events, nil
}

// AuditQueryOptions specifies criteria for querying audit events.
type AuditQueryOptions struct {
	// Type filters by event type.
	Type AuditEventType `json:"type,omitempty"`

	// ToolName filters by tool name.
	ToolName string `json:"tool_name,omitempty"`

	// UserID filters by user.
	UserID string `json:"user_id,omitempty"`

	// Behavior filters by decision behavior.
	Behavior Behavior `json:"behavior,omitempty"`

	// Since filters events after this time.
	Since *time.Time `json:"since,omitempty"`

	// Until filters events before this time.
	Until *time.Time `json:"until,omitempty"`

	// Limit caps the number of results.
	Limit int `json:"limit,omitempty"`
}

// ─── Audit Statistics ────────────────────────────────────────────────────────

// AuditStats provides statistics about audit events.
type AuditStats struct {
	TotalEvents         int64          `json:"total_events"`
	EventsByType        map[string]int `json:"events_by_type"`
	EventsByTool        map[string]int `json:"events_by_tool"`
	DecisionsByBehavior map[string]int `json:"decisions_by_behavior"`
	UniqueUsers         int            `json:"unique_users"`
	UniqueAgents        int            `json:"unique_agents"`
}

// Stats computes statistics from recent audit events.
func (a *Auditor) Stats(since time.Time) (*AuditStats, error) {
	events, err := a.Query(AuditQueryOptions{Since: &since})
	if err != nil {
		return nil, err
	}

	stats := &AuditStats{
		TotalEvents:         int64(len(events)),
		EventsByType:        make(map[string]int),
		EventsByTool:        make(map[string]int),
		DecisionsByBehavior: make(map[string]int),
	}

	users := make(map[string]bool)
	agents := make(map[string]bool)

	for _, e := range events {
		stats.EventsByType[string(e.Type)]++
		if e.ToolName != "" {
			stats.EventsByTool[e.ToolName]++
		}
		if e.Type == AuditEventDecision && e.Behavior != "" {
			stats.DecisionsByBehavior[string(e.Behavior)]++
		}
		if e.UserID != "" {
			users[e.UserID] = true
		}
		if e.AgentID != "" {
			agents[e.AgentID] = true
		}
	}

	stats.UniqueUsers = len(users)
	stats.UniqueAgents = len(agents)

	return stats, nil
}

// Close flushes pending events and closes the auditor.
func (a *Auditor) Close() error {
	return a.Flush()
}

// ─── Helper Functions ────────────────────────────────────────────────────────

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
