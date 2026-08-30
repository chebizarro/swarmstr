package commitments

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status is the lifecycle state of a commitment extracted from an agent turn.
type Status string

const (
	StatusPending   Status = "pending"
	StatusFulfilled Status = "fulfilled"
	StatusBroken    Status = "broken"
	StatusExpired   Status = "expired"
)

// Kind classifies the kind of promise made by the agent.
type Kind string

const (
	KindReminder Kind = "reminder"
	KindFollowUp Kind = "follow_up"
	KindDeadline Kind = "deadline"
	KindOpenLoop Kind = "open_loop"
)

// Commitment is a tracked promise made in an agent response.
type Commitment struct {
	ID                string    `json:"id"`
	SessionID         string    `json:"session_id"`
	TurnID            string    `json:"turn_id,omitempty"`
	Kind              Kind      `json:"kind"`
	Text              string    `json:"text"`
	Source            string    `json:"source"` // regex, llm, or merged
	Status            Status    `json:"status"`
	DueAt             time.Time `json:"due_at,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	FulfilledBy       string    `json:"fulfilled_by,omitempty"`
	BrokenReason      string    `json:"broken_reason,omitempty"`
	ExtractedMatch    string    `json:"extracted_match,omitempty"`
	Confidence        float64   `json:"confidence,omitempty"`
	Channel           string    `json:"channel,omitempty"`
	To                string    `json:"to,omitempty"`
	DeliverySessionID string    `json:"delivery_session_id,omitempty"`
	Attempts          int       `json:"attempts,omitempty"`
	LastAttemptAt     time.Time `json:"last_attempt_at,omitempty"`
	NextAttemptAt     time.Time `json:"next_attempt_at,omitempty"`
	SentAt            time.Time `json:"sent_at,omitempty"`
	DroppedNoticeAt   time.Time `json:"dropped_notice_at,omitempty"`
	BackingReferences []string  `json:"backing_references,omitempty"`
	LifecycleRecorded bool      `json:"lifecycle_recorded,omitempty"`
}

// SessionMessage is a minimal representation of a conversation turn used by
// lifecycle checks. Tool messages should set Role="tool" and ToolName.
type SessionMessage struct {
	ID        string
	Role      string
	Content   string
	ToolName  string
	IsError   bool
	CreatedAt time.Time
}

// ExtractedCommitment is returned by model-backed extractors before tracking.
type ExtractedCommitment struct {
	Kind       Kind
	Text       string
	DueAt      time.Time
	Confidence float64
}

// LLMExtractor optionally provides model-backed structured extraction. The
// package remains deterministic and usable without an LLM when this is nil.
type LLMExtractor interface {
	ExtractCommitments(text string) ([]ExtractedCommitment, error)
}

// Extractor combines deterministic regex extraction with an optional model
// extractor. Model results are de-duplicated with regex results by normalized
// text and due time.
type Extractor struct {
	LLM    LLMExtractor
	Now    func() time.Time
	Config Config
}

var regexPatterns = []struct {
	kind Kind
	re   *regexp.Regexp
}{
	{KindReminder, regexp.MustCompile(`(?is)\b(?:i(?:'ll| will)|let me)\s+(?:set|create|schedule|send)?\s*(?:a\s+)?(?:reminder|remind|ping)\b[^.!?\n]*`)},
	{KindFollowUp, regexp.MustCompile(`(?is)\b(?:i(?:'ll| will)|let me)\s+(?:follow up|follow-up|check back|circle back)\b[^.!?\n]*`)},
	{KindDeadline, regexp.MustCompile(`(?is)\b(?:i(?:'ll| will)|let me)\s+[^.!?\n]*\b(?:by|before|no later than)\s+(?:tomorrow|tonight|monday|tuesday|wednesday|thursday|friday|saturday|sunday|\d{4}-\d{2}-\d{2})\b[^.!?\n]*`)},
	{KindOpenLoop, regexp.MustCompile(`(?is)\b(?:i(?:'ll| will)|let me)\s+(?:look into|investigate|check|verify|research)\b[^.!?\n]*(?:later|next|soon|afterwards|tomorrow|tonight)\b[^.!?\n]*`)},
}

var (
	dueDateRE        = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2}|tomorrow|tonight)\b`)
	taskCommitmentRE = regexp.MustCompile(`(?i)\b(?:i\s*['’]?ll|i will|let me|i(?:'m| am)\s+going to|i can)\s+(?:handle|take care of|work on|look into|investigate|check|verify|research|fix|update|implement|follow up|follow-up|check back|circle back|remind|ping|schedule|create|open)\b`)
)

// HasTaskCommitment detects an affirmative promise to take ownership of future
// work. It intentionally requires an action verb to avoid completion summaries.
func HasTaskCommitment(text string) bool {
	return taskCommitmentRE.MatchString(text)
}

// Extract detects commitments in an agent response using regexes and, when
// configured, LLM-extracted structured candidates.
func (e Extractor) Extract(sessionID, turnID, text string) ([]Commitment, error) {
	now := e.now()
	cfg := e.Config.withDefaults()
	var out []Commitment
	seen := map[string]int{}
	add := func(src string, kind Kind, body string, due time.Time, confidence float64) {
		if confidence == 0 {
			confidence = 1
		}
		if confidence < cfg.ConfidenceThreshold {
			return
		}
		body = normalizeWhitespace(body)
		if body == "" {
			return
		}
		key := strings.ToLower(string(kind) + "|" + body + "|" + due.UTC().Format(time.RFC3339))
		if idx, ok := seen[key]; ok {
			if out[idx].Source != src {
				out[idx].Source = "merged"
			}
			return
		}
		c := Commitment{
			ID:             makeID(sessionID, turnID, kind, body, due),
			SessionID:      sessionID,
			TurnID:         turnID,
			Kind:           kind,
			Text:           body,
			Source:         src,
			Status:         StatusPending,
			DueAt:          due,
			CreatedAt:      now,
			UpdatedAt:      now,
			ExtractedMatch: body,
			Confidence:     confidence,
		}
		seen[key] = len(out)
		out = append(out, c)
	}

	for _, p := range regexPatterns {
		for _, match := range p.re.FindAllString(text, -1) {
			add("regex", p.kind, match, parseDue(match, now), 1)
		}
	}
	if e.LLM != nil {
		items, err := e.LLM.ExtractCommitments(text)
		if err != nil {
			return out, err
		}
		for _, item := range items {
			kind := item.Kind
			if kind == "" {
				kind = KindOpenLoop
			}
			add("llm", kind, item.Text, item.DueAt, item.Confidence)
		}
	}
	return out, nil
}

func (e Extractor) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

// Store tracks commitments and their lifecycle. It is safe for concurrent use.
type Store struct {
	mu          sync.Mutex
	commitments map[string]Commitment
	path        string
}

func NewStore() *Store { return &Store{commitments: map[string]Commitment{}} }

func NewFileStore(path string) (*Store, error) {
	s := NewStore()
	s.path = path
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Add(items ...Commitment) {
	_ = s.AddE(items...)
}

// AddE persists commitments and reports durable-store failures. Existing IDs
// are replaced, allowing deterministic turn retries without duplicate records.
func (s *Store) AddE(items ...Commitment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitments == nil {
		s.commitments = map[string]Commitment{}
	}
	previous := make(map[string]*Commitment, len(items))
	for _, c := range items {
		if c.ID == "" {
			c.ID = makeID(c.SessionID, c.TurnID, c.Kind, c.Text, c.DueAt)
		}
		if old, ok := s.commitments[c.ID]; ok {
			copy := old
			previous[c.ID] = &copy
		} else {
			previous[c.ID] = nil
		}
		if c.Status == "" {
			c.Status = StatusPending
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = time.Now().UTC()
		}
		c.UpdatedAt = nonZeroTime(c.UpdatedAt, c.CreatedAt)
		c.BackingReferences = normalizeReferences(c.BackingReferences)
		s.commitments[c.ID] = c
	}
	if err := s.saveLocked(); err != nil {
		for id, old := range previous {
			if old == nil {
				delete(s.commitments, id)
			} else {
				s.commitments[id] = *old
			}
		}
		return err
	}
	return nil
}

func (s *Store) Get(id string) (Commitment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.commitments[id]
	return c, ok
}

func (s *Store) List(sessionID string, statuses ...Status) []Commitment {
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := map[Status]bool{}
	for _, st := range statuses {
		wanted[st] = true
	}
	out := make([]Commitment, 0, len(s.commitments))
	for _, c := range s.commitments {
		if sessionID != "" && c.SessionID != sessionID {
			continue
		}
		if len(wanted) > 0 && !wanted[c.Status] {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// PendingCount reports the number of open commitments for one room/session.
func (s *Store) PendingCount(sessionID string) int {
	return len(s.List(sessionID, StatusPending))
}

// ResolveBacking transitions every pending commitment correlated to reference.
// The returned records are the commitments whose lifecycle changed.
func (s *Store) ResolveBacking(reference string, status Status, reason string, at time.Time) ([]Commitment, error) {
	reference = normalizeReference(reference)
	if reference == "" {
		return nil, nil
	}
	if status != StatusFulfilled && status != StatusBroken && status != StatusExpired {
		return nil, fmt.Errorf("invalid terminal commitment status %q", status)
	}
	var changed []Commitment
	for _, commitment := range s.List("", StatusPending) {
		if !containsReference(commitment.BackingReferences, reference) {
			continue
		}
		fulfilledBy := ""
		if status == StatusFulfilled {
			fulfilledBy = reference
		}
		if err := s.UpdateStatus(commitment.ID, status, reason, fulfilledBy, at); err != nil {
			return changed, err
		}
		updated, _ := s.Get(commitment.ID)
		changed = append(changed, updated)
	}
	return changed, nil
}

func (s *Store) UpdateStatus(id string, status Status, reason, fulfilledBy string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.commitments[id]
	if !ok {
		return fmt.Errorf("commitment %q not found", id)
	}
	c.Status = status
	c.BrokenReason = reason
	c.FulfilledBy = fulfilledBy
	if at.IsZero() {
		at = time.Now().UTC()
	}
	c.UpdatedAt = at.UTC()
	s.commitments[id] = c
	return s.saveLocked()
}

// CheckSessionHistory evaluates pending commitments against session history.
// Successful cron_add/reminder tool calls fulfill reminder/follow-up commitments;
// explicit refusal/failure content breaks them; due commitments become expired.
func (s *Store) CheckSessionHistory(sessionID string, history []SessionMessage, now time.Time) []Commitment {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tools := successfulTools(history)
	broken := brokenEvidence(history)
	var changed []Commitment
	for _, c := range s.List(sessionID, StatusPending) {
		status := c.Status
		reason := ""
		fulfilledBy := ""
		if canFulfillWithTools(c, tools) {
			status = StatusFulfilled
			fulfilledBy = tools.firstMatch("cron_add", "reminder.create", "task.complete", "social_plan_add")
		} else if broken != "" {
			status = StatusBroken
			reason = broken
		} else if !c.DueAt.IsZero() && now.After(c.DueAt) {
			status = StatusExpired
			reason = "due time elapsed without fulfillment evidence"
		}
		if status != c.Status {
			_ = s.UpdateStatus(c.ID, status, reason, fulfilledBy, now)
			updated, _ := s.Get(c.ID)
			changed = append(changed, updated)
		}
	}
	return changed
}

type toolSet map[string]string

func successfulTools(history []SessionMessage) toolSet {
	out := toolSet{}
	for _, m := range history {
		if strings.EqualFold(m.Role, "tool") && !m.IsError && strings.TrimSpace(m.ToolName) != "" {
			out[m.ToolName] = m.ID
		}
	}
	return out
}

func (t toolSet) firstMatch(names ...string) string {
	for _, name := range names {
		if id, ok := t[name]; ok {
			if id != "" {
				return id
			}
			return name
		}
	}
	return ""
}

func canFulfillWithTools(c Commitment, tools toolSet) bool {
	switch c.Kind {
	case KindReminder, KindFollowUp, KindDeadline:
		return tools.firstMatch("cron_add", "reminder.create", "social_plan_add") != ""
	case KindOpenLoop:
		return tools.firstMatch("task.complete", "file_edit", "bash_exec") != ""
	default:
		return false
	}
}

func brokenEvidence(history []SessionMessage) string {
	for _, m := range history {
		lower := strings.ToLower(m.Content)
		if strings.Contains(lower, "cannot follow up") || strings.Contains(lower, "won't follow up") || strings.Contains(lower, "unable to schedule") || (strings.EqualFold(m.Role, "tool") && m.IsError && strings.Contains(lower, "cron")) {
			if m.ID != "" {
				return "broken by " + m.ID
			}
			return "broken by session history"
		}
	}
	return ""
}

func parseDue(text string, now time.Time) time.Time {
	m := dueDateRE.FindString(strings.ToLower(text))
	switch m {
	case "":
		return time.Time{}
	case "tomorrow":
		return time.Date(now.Year(), now.Month(), now.Day()+1, 9, 0, 0, 0, time.UTC)
	case "tonight":
		return time.Date(now.Year(), now.Month(), now.Day(), 20, 0, 0, 0, time.UTC)
	default:
		if t, err := time.Parse("2006-01-02", m); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func makeID(sessionID, turnID string, kind Kind, text string, due time.Time) string {
	h := sha1.Sum([]byte(sessionID + "\x00" + turnID + "\x00" + string(kind) + "\x00" + normalizeWhitespace(text) + "\x00" + due.UTC().Format(time.RFC3339)))
	return "cmt_" + hex.EncodeToString(h[:])[:16]
}

func normalizeReference(reference string) string {
	return strings.ToLower(strings.TrimSpace(reference))
}

func normalizeReferences(references []string) []string {
	seen := make(map[string]struct{}, len(references))
	out := make([]string, 0, len(references))
	for _, reference := range references {
		reference = normalizeReference(reference)
		if reference == "" {
			continue
		}
		if _, ok := seen[reference]; ok {
			continue
		}
		seen[reference] = struct{}{}
		out = append(out, reference)
	}
	sort.Strings(out)
	return out
}

func containsReference(references []string, wanted string) bool {
	for _, reference := range references {
		if normalizeReference(reference) == wanted {
			return true
		}
	}
	return false
}

func nonZeroTime(a, b time.Time) time.Time {
	if !a.IsZero() {
		return a
	}
	return b
}
