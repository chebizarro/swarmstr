package skillproposal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ledgerSchema = 1
	maxEvents    = 10_000
)

var ledgerMu sync.Mutex

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

type Finding struct {
	RuleID   string `json:"ruleId"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}

type EvaluationResult struct {
	Summary        string         `json:"summary,omitempty"`
	Findings       []Finding      `json:"findings,omitempty"`
	Metrics        map[string]any `json:"metrics,omitempty"`
	EvaluatorVer   string         `json:"evaluatorVersion,omitempty"`
	Mode           string         `json:"mode,omitempty"`
	Decision       string         `json:"decision,omitempty"`
	DecisionReason string         `json:"decisionReason,omitempty"`
}

type EvaluationOutcome struct {
	PluginID      string           `json:"pluginId"`
	PluginVersion string           `json:"pluginVersion,omitempty"`
	EvaluatorID   string           `json:"evaluatorId"`
	Status        string           `json:"status"`
	Result        EvaluationResult `json:"result,omitempty"`
	Error         string           `json:"error,omitempty"`
}

type Evaluation struct {
	ID              string              `json:"id"`
	ProposedVersion string              `json:"proposedVersion"`
	RevisionHash    string              `json:"revisionHash"`
	Trigger         string              `json:"trigger"`
	StartedAt       string              `json:"startedAt"`
	CompletedAt     string              `json:"completedAt"`
	CorrelationID   string              `json:"correlationId,omitempty"`
	Outcomes        []EvaluationOutcome `json:"outcomes"`
}

type Event struct {
	Sequence        int64          `json:"sequence"`
	EventID         string         `json:"eventId"`
	ProposalID      string         `json:"proposalId"`
	ProposedVersion string         `json:"proposedVersion"`
	RevisionHash    string         `json:"revisionHash"`
	Type            string         `json:"type"`
	OccurredAt      string         `json:"occurredAt"`
	Actor           Actor          `json:"actor"`
	CorrelationID   string         `json:"correlationId,omitempty"`
	Payload         map[string]any `json:"payload,omitempty"`
	Evaluation      *Evaluation    `json:"evaluation,omitempty"`
}

type ledger struct {
	Schema int     `json:"schema"`
	Events []Event `json:"events"`
}

type Store struct {
	path string
}

func NewStore(workspaceDir string) *Store {
	return &Store{path: filepath.Join(workspaceDir, ".metiq", "skill-proposals", "events.json")}
}

func randomID(prefix string) (string, error) {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf[:]), nil
}

func (s *Store) load() (ledger, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ledger{Schema: ledgerSchema, Events: []Event{}}, nil
		}
		return ledger{}, err
	}
	var out ledger
	if err := json.Unmarshal(raw, &out); err != nil {
		return ledger{}, fmt.Errorf("decode skill proposal event ledger: %w", err)
	}
	if out.Schema != ledgerSchema {
		return ledger{}, fmt.Errorf("unsupported skill proposal event ledger schema %d", out.Schema)
	}
	if out.Events == nil {
		out.Events = []Event{}
	}
	return out, nil
}

func (s *Store) write(value ledger) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".events-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func (s *Store) Append(event Event) (Event, error) {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	value, err := s.load()
	if err != nil {
		return Event{}, err
	}
	if len(value.Events) >= maxEvents {
		return Event{}, fmt.Errorf("skill proposal event ledger reached %d events", maxEvents)
	}
	if strings.TrimSpace(event.ProposalID) == "" || strings.TrimSpace(event.RevisionHash) == "" || strings.TrimSpace(event.Type) == "" {
		return Event{}, fmt.Errorf("proposal id, revision hash, and event type are required")
	}
	id, err := randomID("spevt_")
	if err != nil {
		return Event{}, err
	}
	event.Sequence = int64(len(value.Events) + 1)
	event.EventID = id
	if event.OccurredAt == "" {
		event.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if event.Actor.Type == "" {
		event.Actor.Type = "gateway"
	}
	value.Events = append(value.Events, event)
	if err := s.write(value); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (s *Store) List(proposalID string, after int64, limit int) ([]Event, *int64, error) {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	value, err := s.load()
	if err != nil {
		return nil, nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	out := make([]Event, 0, limit)
	for _, event := range value.Events {
		if event.Sequence <= after || (proposalID != "" && event.ProposalID != proposalID) {
			continue
		}
		out = append(out, event)
		if len(out) == limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	var next *int64
	if len(out) == limit {
		cursor := out[len(out)-1].Sequence
		for _, event := range value.Events {
			if event.Sequence > cursor && (proposalID == "" || event.ProposalID == proposalID) {
				next = &cursor
				break
			}
		}
	}
	return out, next, nil
}

func NewEvaluation(proposedVersion, revisionHash, correlationID string, outcomes []EvaluationOutcome) (Evaluation, error) {
	id, err := randomID("speval_")
	if err != nil {
		return Evaluation{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return Evaluation{ID: id, ProposedVersion: proposedVersion, RevisionHash: revisionHash, Trigger: "manual", StartedAt: now, CompletedAt: now, CorrelationID: correlationID, Outcomes: outcomes}, nil
}
