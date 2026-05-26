package trajectory

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"metiq/internal/nostr/adoption"
	"time"
)

type EventType string

const (
	EventModelRequest  EventType = "model_request"
	EventModelResponse EventType = "model_response"
	EventToolCall      EventType = "tool_call"
	EventToolResult    EventType = "tool_result"
	EventApproval      EventType = "approval"
	EventError         EventType = "error"
	EventWarning       EventType = "warning"
	EventCompaction    EventType = "compaction"
)

type Event struct {
	Time      time.Time         `json:"time"`
	SessionID string            `json:"session_id"`
	TurnID    string            `json:"turn_id,omitempty"`
	Type      EventType         `json:"type"`
	Name      string            `json:"name,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Payload   map[string]any    `json:"payload,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type Metadata struct {
	Version  string            `json:"version,omitempty"`
	Config   map[string]string `json:"config,omitempty"`
	Plugins  []string          `json:"plugins,omitempty"`
	Skills   []string          `json:"skills,omitempty"`
	Provider string            `json:"provider,omitempty"`
}

type Recorder struct {
	mu        sync.Mutex
	sessionID string
	file      *os.File
	enc       *json.Encoder
	redactor  Redactor
	metadata  Metadata
}

func NewRecorder(root, sessionID string, md Metadata) (*Recorder, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id required")
	}
	if root == "" {
		root = DefaultRoot()
	}
	dir := filepath.Join(root, safeName(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "trajectory.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	r := &Recorder{sessionID: sessionID, file: f, enc: json.NewEncoder(f), redactor: DefaultRedactor(), metadata: md}
	_ = r.Record(Event{Type: "metadata", Summary: "session metadata", Payload: map[string]any{"metadata": md}})
	return r, nil
}

func (r *Recorder) Record(ev Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	if ev.SessionID == "" {
		ev.SessionID = r.sessionID
	}
	ev = r.redactor.RedactEvent(ev)
	return r.enc.Encode(ev)
}

func (r *Recorder) Close() error { r.mu.Lock(); defer r.mu.Unlock(); return r.file.Close() }

func DefaultRoot() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".metiq", "trajectories")
	}
	return filepath.Join(os.TempDir(), "metiq-trajectories")
}

func SessionPath(root, sessionID string) string {
	if root == "" {
		root = DefaultRoot()
	}
	return filepath.Join(root, safeName(sessionID), "trajectory.jsonl")
}

type Redactor struct {
	MaxString int
	patterns  []*regexp.Regexp
}

func DefaultRedactor() Redactor {
	pats := []string{`(?i)(api[_-]?key|token|secret|password|private[_-]?key)\s*[:=]\s*[^\s,;]+`, `sk-[A-Za-z0-9_-]{16,}`, `nsec1[023456789acdefghjklmnpqrstuvwxyz]+`}
	out := Redactor{MaxString: 4096}
	for _, p := range pats {
		out.patterns = append(out.patterns, regexp.MustCompile(p))
	}
	return out
}

func (r Redactor) RedactEvent(ev Event) Event {
	ev.Summary = r.RedactString(ev.Summary)
	for k, v := range ev.Metadata {
		ev.Metadata[k] = r.RedactString(v)
	}
	ev.Payload = r.redactMap(ev.Payload)
	return ev
}

func (r Redactor) redactMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = r.redactValue(v)
	}
	return out
}

func (r Redactor) redactValue(v any) any {
	switch x := v.(type) {
	case string:
		return r.RedactString(x)
	case map[string]any:
		return r.redactMap(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = r.redactValue(x[i])
		}
		return out
	default:
		return v
	}
}

func (r Redactor) RedactString(s string) string {
	for _, p := range r.patterns {
		s = p.ReplaceAllString(s, "$1=[REDACTED]")
	}
	if r.MaxString > 0 && len(s) > r.MaxString {
		s = s[:r.MaxString] + fmt.Sprintf("...[truncated %d bytes]", len(s)-r.MaxString)
	}
	return s
}

type BundleManifest struct {
	SessionID  string       `json:"session_id"`
	ExportedAt time.Time    `json:"exported_at"`
	Files      []string     `json:"files"`
	Summary    AuditSummary `json:"summary"`
}

type AuditSummary struct {
	SessionID   string            `json:"session_id"`
	StartedAt   time.Time         `json:"started_at,omitempty"`
	EndedAt     time.Time         `json:"ended_at,omitempty"`
	EventCounts map[EventType]int `json:"event_counts"`
	Warnings    int               `json:"warnings"`
	Errors      int               `json:"errors"`
	ToolCalls   int               `json:"tool_calls"`
	Compactions int               `json:"compactions"`
	Provider    string            `json:"provider,omitempty"`
}

func ExportBundle(root, sessionID, outPath string) (BundleManifest, error) {
	path := SessionPath(root, sessionID)
	events, err := ReadEvents(path)
	if err != nil {
		return BundleManifest{}, err
	}
	summary := Summarize(sessionID, events)
	if outPath == "" {
		outPath = filepath.Join(filepath.Dir(path), "support-bundle.zip")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return BundleManifest{}, err
	}
	manifest := BundleManifest{SessionID: sessionID, ExportedAt: time.Now().UTC(), Files: []string{"manifest.json", "trajectory.jsonl", "summary.json"}, Summary: summary}
	if err := writeZip(outPath, manifest, events); err != nil {
		return BundleManifest{}, err
	}
	return manifest, nil
}

func ReadEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	red := DefaultRedactor()
	var events []Event
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			return nil, err
		}
		events = append(events, red.RedactEvent(ev))
	}
	return events, scanner.Err()
}

func Summarize(sessionID string, events []Event) AuditSummary {
	s := AuditSummary{SessionID: sessionID, EventCounts: map[EventType]int{}}
	for _, ev := range events {
		if s.StartedAt.IsZero() || ev.Time.Before(s.StartedAt) {
			s.StartedAt = ev.Time
		}
		if ev.Time.After(s.EndedAt) {
			s.EndedAt = ev.Time
		}
		s.EventCounts[ev.Type]++
		switch ev.Type {
		case EventWarning:
			s.Warnings++
		case EventError:
			s.Errors++
		case EventToolCall:
			s.ToolCalls++
		case EventCompaction:
			s.Compactions++
		}
		if p, _ := ev.Payload["metadata"].(map[string]any); p != nil {
			if provider, _ := p["provider"].(string); provider != "" {
				s.Provider = provider
			} else if provider, _ := p["Provider"].(string); provider != "" {
				s.Provider = provider
			}
		}
	}
	return s
}

func writeZip(path string, manifest BundleManifest, events []Event) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	addJSON := func(name string, v any) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	if err := addJSON("manifest.json", manifest); err != nil {
		return err
	}
	if err := addJSON("summary.json", manifest.Summary); err != nil {
		return err
	}
	w, err := zw.Create("trajectory.jsonl")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}

const AuditSummaryKind = adoption.KindTrajectoryAudit

type DraftEvent = adoption.DraftEvent

func BuildNostrAuditSummary(summary AuditSummary) (DraftEvent, error) {
	content, err := json.Marshal(summary)
	if err != nil {
		return DraftEvent{}, err
	}
	tags := [][]string{{"d", summary.SessionID}, {"t", "metiq-trajectory"}, {"t", "audit-summary"}}
	return DraftEvent{Kind: AuditSummaryKind, CreatedAt: time.Now().Unix(), Tags: tags, Content: string(content)}, nil
}

func Cleanup(root, sessionID string) error {
	if root == "" {
		root = DefaultRoot()
	}
	return os.RemoveAll(filepath.Join(root, safeName(sessionID)))
}

func safeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	return regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(s, "_")
}

func CopySanitizedJSONL(dst io.Writer, events []Event) error {
	enc := json.NewEncoder(dst)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}

func ListSessions(root string) ([]string, error) {
	if root == "" {
		root = DefaultRoot()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
