package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"metiq/internal/store/state"
)

const (
	MemoryRecordTypePreference  = "preference"
	MemoryRecordTypeDecision    = "decision"
	MemoryRecordTypeConstraint  = "constraint"
	MemoryRecordTypeFact        = "fact"
	MemoryRecordTypeEpisode     = "episode"
	MemoryRecordTypeToolLesson  = "tool_lesson"
	MemoryRecordTypeSummary     = "summary"
	MemoryRecordTypeArtifactRef = "artifact_ref"
	MemoryRecordTypeFeedback    = "feedback"
	MemoryRecordTypeReference   = "reference"
)

const (
	MemoryRecordScopeUser    = "user"
	MemoryRecordScopeProject = "project"
	MemoryRecordScopeLocal   = "local"
	MemoryRecordScopeSession = "session"
	MemoryRecordScopeAgent   = "agent"
	MemoryRecordScopeTeam    = "team"
)

const (
	MemorySourceKindTurn           = "turn"
	MemorySourceKindTool           = "tool"
	MemorySourceKindFile           = "file"
	MemorySourceKindSessionSummary = "session_summary"
	MemorySourceKindNostr          = "nostr"
	MemorySourceKindManual         = "manual"
)

// MemoryRecord is the typed, lifecycle-aware memory model used by the unified
// memory layer. Existing state.MemoryDoc values are adapted into this shape so
// older persistence paths can coexist during migration.
type MemoryRecord struct {
	ID               string         `json:"id"`
	Type             string         `json:"type"`
	Scope            string         `json:"scope"`
	Subject          string         `json:"subject"`
	Text             string         `json:"text"`
	Summary          string         `json:"summary,omitempty"`
	Keywords         []string       `json:"keywords,omitempty"`
	Tags             []string       `json:"tags,omitempty"`
	Confidence       float64        `json:"confidence"`
	Salience         float64        `json:"salience"`
	Source           MemorySource   `json:"source"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	ValidFrom        time.Time      `json:"valid_from,omitempty"`
	ValidUntil       *time.Time     `json:"valid_until,omitempty"`
	Pinned           bool           `json:"pinned,omitempty"`
	Supersedes       []string       `json:"supersedes,omitempty"`
	SupersededBy     string         `json:"superseded_by,omitempty"`
	DeletedAt        *time.Time     `json:"deleted_at,omitempty"`
	EmbeddingModel   string         `json:"embedding_model,omitempty"`
	EmbeddingVersion string         `json:"embedding_version,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type MemorySource struct {
	Kind         string `json:"kind"`
	Ref          string `json:"ref,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	EventID      string `json:"event_id,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
	NostrEventID string `json:"nostr_event_id,omitempty"`
}

type MemoryQuery struct {
	Query          string                `json:"query"`
	Scopes         []string              `json:"scope,omitempty"`
	Types          []string              `json:"types,omitempty"`
	Tags           []string              `json:"tags,omitempty"`
	Mode           string                `json:"mode,omitempty"`
	Limit          int                   `json:"limit,omitempty"`
	IncludeSources bool                  `json:"include_sources"`
	IncludeDebug   bool                  `json:"include_debug,omitempty"`
	RankingWeights *MemoryRankingWeights `json:"ranking_weights,omitempty"`
	TokenBudget    int                   `json:"token_budget,omitempty"`
	SessionID      string                `json:"-"`
	ExplicitScopes bool                  `json:"-"`
	ExplicitTypes  bool                  `json:"-"`
	ExplicitMode   bool                  `json:"-"`
}

type MemoryCard struct {
	ID         string              `json:"id"`
	Type       string              `json:"type"`
	Scope      string              `json:"scope"`
	Subject    string              `json:"subject,omitempty"`
	Summary    string              `json:"summary"`
	Text       string              `json:"text,omitempty"`
	Score      float64             `json:"score"`
	Confidence float64             `json:"confidence"`
	Salience   float64             `json:"salience,omitempty"`
	Source     MemorySource        `json:"source,omitempty"`
	UpdatedAt  string              `json:"updated_at"`
	Tags       []string            `json:"tags,omitempty"`
	Why        *MemoryRetrievalWhy `json:"why,omitempty"`
}

type MemoryWriteRequest struct {
	Text       string         `json:"text"`
	Type       string         `json:"type,omitempty"`
	Scope      string         `json:"scope,omitempty"`
	Subject    string         `json:"subject,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Confidence *float64       `json:"confidence,omitempty"`
	Pinned     bool           `json:"pinned,omitempty"`
	Durable    bool           `json:"durable,omitempty"`
	Source     *MemorySource  `json:"source,omitempty"`
	Supersedes []string       `json:"supersedes,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type MemoryWriteResult struct {
	ID       string           `json:"id"`
	Stored   bool             `json:"stored"`
	Durable  bool             `json:"durable,omitempty"`
	Pinned   bool             `json:"pinned,omitempty"`
	Salience SalienceDecision `json:"salience,omitempty"`
	Skipped  bool             `json:"skipped,omitempty"`
	Reason   string           `json:"reason,omitempty"`
}

type MemoryRecordStore interface {
	WriteMemoryRecord(context.Context, MemoryRecord) error
	QueryMemoryRecords(context.Context, MemoryQuery) ([]MemoryCard, error)
	GetMemoryRecord(context.Context, string) (MemoryRecord, bool, error)
	UpdateMemoryRecord(context.Context, string, map[string]any) (MemoryRecord, error)
	ForgetMemoryRecord(context.Context, string, string) (bool, error)
	CompactMemoryRecords(context.Context, CompactionConfig) (CompactionResult, error)
}

func NewMemoryRecordID() string { return uuid.New().String() }

func NormalizeMemoryRecord(r MemoryRecord) (MemoryRecord, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		r.ID = NewMemoryRecordID()
	}
	r.Type = NormalizeMemoryRecordType(r.Type)
	r.Scope = NormalizeMemoryRecordScope(r.Scope)
	r.Subject = normalizeSubject(r.Subject)
	r.Text = strings.TrimSpace(r.Text)
	if r.Text == "" {
		return MemoryRecord{}, fmt.Errorf("memory record text is required")
	}
	if r.Subject == "" {
		r.Subject = deriveSubject(r.Text, r.Tags)
	}
	r.Summary = strings.TrimSpace(r.Summary)
	if r.Summary == "" {
		r.Summary = summarizeMemoryText(r.Text, 220)
	}
	r.Keywords = normalizeStringSlice(r.Keywords)
	r.Tags = normalizeStringSlice(r.Tags)
	if len(r.Keywords) == 0 {
		r.Keywords = extractKeywords(strings.Join([]string{r.Subject, r.Summary, r.Text}, " "))
	}
	if r.Confidence <= 0 || r.Confidence > 1 {
		r.Confidence = state.DefaultConfidence
	}
	if r.Salience < 0 {
		r.Salience = 0
	}
	if r.Salience > 1 {
		r.Salience = 1
	}
	if r.Source.Kind == "" {
		r.Source.Kind = MemorySourceKindManual
	}
	r.Source.Kind = strings.TrimSpace(strings.ToLower(r.Source.Kind))
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	} else {
		r.CreatedAt = r.CreatedAt.UTC()
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	} else {
		r.UpdatedAt = r.UpdatedAt.UTC()
	}
	if r.ValidFrom.IsZero() {
		r.ValidFrom = r.CreatedAt
	} else {
		r.ValidFrom = r.ValidFrom.UTC()
	}
	if r.ValidUntil != nil {
		v := r.ValidUntil.UTC()
		r.ValidUntil = &v
	}
	if r.DeletedAt != nil {
		d := r.DeletedAt.UTC()
		r.DeletedAt = &d
	}
	r.Supersedes = normalizeStringSlice(r.Supersedes)
	if r.Metadata == nil {
		r.Metadata = map[string]any{}
	}
	return r, nil
}

func NormalizeMemoryRecordType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case MemoryRecordTypePreference, "profile", "user":
		return MemoryRecordTypePreference
	case MemoryRecordTypeDecision:
		return MemoryRecordTypeDecision
	case MemoryRecordTypeConstraint:
		return MemoryRecordTypeConstraint
	case MemoryRecordTypeEpisode, "episodic", "task":
		return MemoryRecordTypeEpisode
	case MemoryRecordTypeToolLesson, "tool-lesson":
		return MemoryRecordTypeToolLesson
	case MemoryRecordTypeSummary:
		return MemoryRecordTypeSummary
	case MemoryRecordTypeArtifactRef, "artifact-ref", "artifact":
		return MemoryRecordTypeArtifactRef
	case MemoryRecordTypeFeedback:
		return MemoryRecordTypeFeedback
	case MemoryRecordTypeReference, "migrated":
		return MemoryRecordTypeReference
	case MemoryRecordTypeFact, "":
		return MemoryRecordTypeFact
	default:
		return MemoryRecordTypeFact
	}
}

func NormalizeMemoryRecordScope(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case MemoryRecordScopeUser, MemoryRecordScopeProject, MemoryRecordScopeLocal, MemoryRecordScopeSession, MemoryRecordScopeAgent, MemoryRecordScopeTeam:
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return MemoryRecordScopeLocal
	}
}

func MemoryRecordFromDoc(doc state.MemoryDoc) MemoryRecord {
	created := time.Unix(doc.Unix, 0).UTC()
	if doc.Unix <= 0 {
		created = time.Now().UTC()
	}
	scope := MemoryRecordScopeLocal
	if doc.SessionID != "" {
		scope = MemoryRecordScopeSession
	}
	mtype := NormalizeMemoryRecordType(doc.Type)
	if mtype == MemoryRecordTypeFact && doc.Type == state.MemoryTypeEpisodic {
		mtype = MemoryRecordTypeEpisode
	}
	sourceKind := MemorySourceKindTurn
	if doc.Source != "" {
		sourceKind = doc.Source
	}
	rec := MemoryRecord{
		ID:         doc.MemoryID,
		Type:       mtype,
		Scope:      scope,
		Subject:    doc.Topic,
		Text:       doc.Text,
		Keywords:   append([]string(nil), doc.Keywords...),
		Tags:       tagsFromDoc(doc),
		Confidence: doc.Confidence,
		Salience:   salienceFromDoc(doc),
		Source: MemorySource{
			Kind:      sourceKind,
			Ref:       doc.SourceRef,
			SessionID: doc.SessionID,
			EventID:   doc.SourceRef,
		},
		CreatedAt:    created,
		UpdatedAt:    created,
		ValidFrom:    created,
		SupersededBy: doc.SupersededBy,
		Metadata:     cloneMetadata(doc.Meta),
	}
	if doc.ExpiresAt > 0 {
		v := time.Unix(doc.ExpiresAt, 0).UTC()
		rec.ValidUntil = &v
	}
	if doc.InvalidatedAt > 0 || doc.MemStatus == state.MemStatusContradicted {
		d := time.Unix(maxInt64(doc.InvalidatedAt, created.Unix()), 0).UTC()
		rec.DeletedAt = &d
	}
	return rec
}

func (r MemoryRecord) ToDoc() state.MemoryDoc {
	unix := r.CreatedAt.Unix()
	if unix <= 0 {
		unix = time.Now().Unix()
	}
	doc := state.MemoryDoc{
		Version:      1,
		MemoryID:     r.ID,
		Type:         legacyDocType(r.Type),
		SessionID:    r.Source.SessionID,
		SourceRef:    firstNonEmpty(r.Source.Ref, r.Source.EventID, r.Source.FilePath, r.Source.NostrEventID),
		Text:         r.Text,
		Keywords:     append(append([]string(nil), r.Keywords...), r.Tags...),
		Topic:        r.Subject,
		Unix:         unix,
		Meta:         cloneMetadata(r.Metadata),
		Confidence:   r.Confidence,
		Source:       r.Source.Kind,
		SupersededBy: r.SupersededBy,
	}
	if r.ValidUntil != nil {
		doc.ExpiresAt = r.ValidUntil.Unix()
	}
	if r.DeletedAt != nil {
		doc.MemStatus = state.MemStatusContradicted
		doc.InvalidatedAt = r.DeletedAt.Unix()
	} else if r.SupersededBy != "" {
		doc.MemStatus = state.MemStatusSuperseded
	} else {
		doc.MemStatus = state.MemStatusActive
	}
	if r.Type == MemoryRecordTypeEpisode || r.Type == MemoryRecordTypeToolLesson {
		doc.Type = state.MemoryTypeEpisodic
		doc.EpisodeKind = state.EpisodeKindInsight
	}
	return doc
}

func StableMemoryRecordID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "mem_" + hex.EncodeToString(h[:16])
}

func MemoryCardFromRecord(r MemoryRecord, score float64, includeSource bool) MemoryCard {
	card := MemoryCard{
		ID:         r.ID,
		Type:       r.Type,
		Scope:      r.Scope,
		Subject:    r.Subject,
		Summary:    firstNonEmpty(r.Summary, summarizeMemoryText(r.Text, 220)),
		Text:       r.Text,
		Score:      score,
		Confidence: r.Confidence,
		Salience:   r.Salience,
		UpdatedAt:  r.UpdatedAt.UTC().Format(time.RFC3339),
		Tags:       append([]string(nil), r.Tags...),
	}
	if includeSource {
		card.Source = r.Source
	}
	return card
}

func recordJSON(v any) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func normalizeStringSlice(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(strings.ToLower(v))
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

func normalizeSubject(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-')
	}), "-")
	s = strings.Trim(s, "-")
	if len(s) > 80 {
		s = s[:80]
		s = strings.Trim(s, "-")
	}
	return s
}

func deriveSubject(text string, tags []string) string {
	if len(tags) > 0 && strings.TrimSpace(tags[0]) != "" {
		return normalizeSubject(tags[0])
	}
	kw := extractKeywords(text)
	if len(kw) > 0 {
		return normalizeSubject(kw[0])
	}
	return "general"
}

func summarizeMemoryText(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxRunes <= 0 {
		return text
	}
	r := []rune(text)
	if len(r) <= maxRunes {
		return text
	}
	return string(r[:maxRunes-1]) + "…"
}

func tagsFromDoc(doc state.MemoryDoc) []string {
	out := make([]string, 0, len(doc.Keywords)+2)
	out = append(out, doc.Keywords...)
	if doc.Topic != "" {
		out = append(out, doc.Topic)
	}
	if doc.EpisodeKind != "" {
		out = append(out, doc.EpisodeKind)
	}
	return normalizeStringSlice(out)
}

func salienceFromDoc(doc state.MemoryDoc) float64 {
	if v, ok := doc.Meta["salience"].(float64); ok {
		return v
	}
	switch NormalizeMemoryRecordType(doc.Type) {
	case MemoryRecordTypeEpisode:
		return 0.45
	case MemoryRecordTypePreference, MemoryRecordTypeDecision, MemoryRecordTypeConstraint, MemoryRecordTypeFeedback:
		return 0.9
	default:
		return 0.7
	}
}

func legacyDocType(t string) string {
	switch t {
	case MemoryRecordTypePreference:
		return state.MemoryTypePreference
	case MemoryRecordTypeEpisode, MemoryRecordTypeToolLesson:
		return state.MemoryTypeEpisodic
	default:
		return state.MemoryTypeFact
	}
}

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
