// feedback.go provides structured feedback capture linked to goals, tasks,
// and runs.  Feedback is distinct from generic memory notes — it carries
// source, severity, category, and durable linkage to the workflow objects
// that triggered it.
package planner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// ── Feedback collector ─────────────────────────────────────────────────────────

// FeedbackCollector accumulates feedback records in memory before they are
// persisted.  It is safe for concurrent use.
type FeedbackCollector struct {
	mu      sync.Mutex
	records []state.FeedbackRecord
	nextID  int
	prefix  string // ID prefix, e.g. "fb"
}

// NewFeedbackCollector creates a collector with the given ID prefix.
func NewFeedbackCollector(prefix string) *FeedbackCollector {
	if prefix == "" {
		prefix = "fb"
	}
	return &FeedbackCollector{prefix: prefix}
}

// generateID creates a unique feedback ID.
func (c *FeedbackCollector) generateID() string {
	c.nextID++
	return fmt.Sprintf("%s-%d", c.prefix, c.nextID)
}

// Capture records a new feedback entry. If FeedbackID is empty, one is
// auto-generated. CreatedAt defaults to now.
// Capture normalizes but does not validate — use CaptureValidated for
// early rejection of invalid records.
func (c *FeedbackCollector) Capture(rec state.FeedbackRecord) state.FeedbackRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.captureLocked(rec)
}

// CaptureValidated is like Capture but returns an error if the record
// fails validation after defaults are applied.
func (c *FeedbackCollector) CaptureValidated(rec state.FeedbackRecord) (state.FeedbackRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec = c.captureLocked(rec)
	if err := rec.Validate(); err != nil {
		// Remove the just-appended invalid record.
		c.records = c.records[:len(c.records)-1]
		return state.FeedbackRecord{}, fmt.Errorf("feedback validation: %w", err)
	}
	return rec, nil
}

func (c *FeedbackCollector) captureLocked(rec state.FeedbackRecord) state.FeedbackRecord {
	if rec.FeedbackID == "" {
		rec.FeedbackID = c.generateID()
	}
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	rec = rec.Normalize()
	c.records = append(c.records, rec)
	return rec
}

// Records returns a snapshot of all captured feedback.
func (c *FeedbackCollector) Records() []state.FeedbackRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]state.FeedbackRecord, len(c.records))
	copy(out, c.records)
	return out
}

// Count returns the number of captured records.
func (c *FeedbackCollector) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}

// FilterByTask returns feedback linked to a specific task.
func (c *FeedbackCollector) FilterByTask(taskID string) []state.FeedbackRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []state.FeedbackRecord
	for _, r := range c.records {
		if r.TaskID == taskID {
			out = append(out, r)
		}
	}
	return out
}

// FilterByRun returns feedback linked to a specific run.
func (c *FeedbackCollector) FilterByRun(runID string) []state.FeedbackRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []state.FeedbackRecord
	for _, r := range c.records {
		if r.RunID == runID {
			out = append(out, r)
		}
	}
	return out
}

// FilterByGoal returns feedback linked to a specific goal.
func (c *FeedbackCollector) FilterByGoal(goalID string) []state.FeedbackRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []state.FeedbackRecord
	for _, r := range c.records {
		if r.GoalID == goalID {
			out = append(out, r)
		}
	}
	return out
}

// FilterBySeverity returns feedback at or above the given severity.
func (c *FeedbackCollector) FilterBySeverity(minSeverity state.FeedbackSeverity) []state.FeedbackRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	minRank := severityRank(minSeverity)
	var out []state.FeedbackRecord
	for _, r := range c.records {
		if severityRank(r.Severity) >= minRank {
			out = append(out, r)
		}
	}
	return out
}

var severityOrder = map[state.FeedbackSeverity]int{
	state.FeedbackSeverityInfo:     0,
	state.FeedbackSeverityWarning:  1,
	state.FeedbackSeverityError:    2,
	state.FeedbackSeverityCritical: 3,
}

func severityRank(s state.FeedbackSeverity) int {
	if r, ok := severityOrder[s]; ok {
		return r
	}
	return 0
}

// ── Convenience capture helpers ────────────────────────────────────────────────

// CaptureOperatorFeedback creates feedback from an operator message.
func CaptureOperatorFeedback(
	collector *FeedbackCollector,
	taskID, runID, goalID string,
	summary string,
	severity state.FeedbackSeverity,
	author string,
) state.FeedbackRecord {
	// Convenience helpers always produce valid records, so ignore the error.
	rec, _ := collector.CaptureValidated(state.FeedbackRecord{
		GoalID:   goalID,
		TaskID:   taskID,
		RunID:    runID,
		Source:   state.FeedbackSourceOperator,
		Severity: severity,
		Category: state.FeedbackCategoryGeneral,
		Summary:  summary,
		Author:   author,
	})
	return rec
}

// CaptureVerificationFailure creates feedback from a failed verification check.
func CaptureVerificationFailure(
	collector *FeedbackCollector,
	taskID, runID, goalID string,
	checkID, reason string,
) state.FeedbackRecord {
	rec, _ := collector.CaptureValidated(state.FeedbackRecord{
		GoalID:   goalID,
		TaskID:   taskID,
		RunID:    runID,
		Source:   state.FeedbackSourceVerification,
		Severity: state.FeedbackSeverityError,
		Category: state.FeedbackCategoryCorrectness,
		Summary:  fmt.Sprintf("verification check %s failed: %s", checkID, reason),
		Meta:     map[string]any{"check_id": checkID},
	})
	return rec
}

// CaptureReviewFeedback creates feedback from a post-run review.
func CaptureReviewFeedback(
	collector *FeedbackCollector,
	taskID, runID, goalID string,
	summary, detail string,
	category state.FeedbackCategory,
	severity state.FeedbackSeverity,
	reviewer string,
) state.FeedbackRecord {
	rec, _ := collector.CaptureValidated(state.FeedbackRecord{
		GoalID:   goalID,
		TaskID:   taskID,
		RunID:    runID,
		Source:   state.FeedbackSourceReview,
		Severity: severity,
		Category: category,
		Summary:  summary,
		Detail:   detail,
		Author:   reviewer,
	})
	return rec
}

// ── Formatting ─────────────────────────────────────────────────────────────────

// FormatFeedbackRecord returns a human-readable summary of a feedback record.
func FormatFeedbackRecord(rec state.FeedbackRecord) string {
	var b strings.Builder
	icon := severityIcon(rec.Severity)
	fmt.Fprintf(&b, "%s [%s] %s\n", icon, rec.Source, rec.Summary)
	if rec.Detail != "" {
		fmt.Fprintf(&b, "  Detail: %s\n", rec.Detail)
	}
	fmt.Fprintf(&b, "  Severity: %s  Category: %s\n", rec.Severity, rec.Category)
	parts := make([]string, 0, 3)
	if rec.GoalID != "" {
		parts = append(parts, "goal="+rec.GoalID)
	}
	if rec.TaskID != "" {
		parts = append(parts, "task="+rec.TaskID)
	}
	if rec.RunID != "" {
		parts = append(parts, "run="+rec.RunID)
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "  Linked: %s\n", strings.Join(parts, " "))
	}
	return b.String()
}

func severityIcon(s state.FeedbackSeverity) string {
	switch s {
	case state.FeedbackSeverityInfo:
		return "ℹ️"
	case state.FeedbackSeverityWarning:
		return "⚠️"
	case state.FeedbackSeverityError:
		return "❌"
	case state.FeedbackSeverityCritical:
		return "🔥"
	default:
		return "•"
	}
}

// FormatFeedbackSummary returns a brief summary of collected feedback.
func FormatFeedbackSummary(records []state.FeedbackRecord) string {
	if len(records) == 0 {
		return "No feedback captured."
	}
	counts := make(map[state.FeedbackSeverity]int)
	for _, r := range records {
		counts[r.Severity]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Feedback: %d records", len(records))
	for _, s := range []state.FeedbackSeverity{
		state.FeedbackSeverityCritical,
		state.FeedbackSeverityError,
		state.FeedbackSeverityWarning,
		state.FeedbackSeverityInfo,
	} {
		if c := counts[s]; c > 0 {
			fmt.Fprintf(&b, " %s=%d", s, c)
		}
	}
	return b.String()
}
