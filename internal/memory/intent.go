package memory

import "strings"

const (
	QueryIntentPreference = "preference"
	QueryIntentDecision   = "decision"
	QueryIntentConstraint = "constraint"
	QueryIntentToolLesson = "tool_lesson"
	QueryIntentEpisodic   = "episodic"
	QueryIntentSummary    = "summary"
	QueryIntentAudit      = "audit"
	QueryIntentRecent     = "recent"
	QueryIntentReference  = "reference"
	QueryIntentGeneral    = "general"
)

type QueryIntent struct {
	Name        string   `json:"name"`
	Types       []string `json:"types,omitempty"`
	Scopes      []string `json:"scopes,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	SearchQuery string   `json:"search_query,omitempty"`
	Confidence  float64  `json:"confidence"`
	Reasons     []string `json:"reasons,omitempty"`
}

// ClassifyQueryIntent uses conservative lexical cues to route memory queries to
// likely typed records. Explicit memory_query filters still take precedence in
// applyQueryIntentRouting.
func ClassifyQueryIntent(query string) QueryIntent {
	original := strings.TrimSpace(query)
	q := strings.ToLower(original)
	intent := QueryIntent{Name: QueryIntentGeneral, SearchQuery: cleanIntentSearchQuery(original), Confidence: 0.35}
	if original == "" {
		intent.SearchQuery = ""
		return intent
	}
	if hasAnyPhrase(q, "audit", "deleted", "superseded", "forgotten", "memory history", "why was", "debug memory", "diagnose memory") {
		return intentWith(original, QueryIntentAudit, nil, nil, "audit", 0.82, "audit/history cue")
	}
	if hasAnyPhrase(q, "recent", "latest", "last time", "last session", "recently", "what just", "newest") {
		return intentWith(original, QueryIntentRecent, nil, nil, "recent", 0.78, "recency cue")
	}
	if hasAnyPhrase(q, "preference", "prefer", "likes", "dislikes", "style", "how do i like", "my default", "user prefers") {
		return intentWith(original, QueryIntentPreference, []string{MemoryRecordTypePreference, MemoryRecordTypeFeedback}, []string{MemoryRecordScopeUser}, "", 0.86, "preference cue")
	}
	if hasAnyPhrase(q, "decision", "decide", "decided", "we decided", "chosen", "choice", "approved", "agreed", "rationale") {
		return intentWith(original, QueryIntentDecision, []string{MemoryRecordTypeDecision}, []string{MemoryRecordScopeProject, MemoryRecordScopeTeam}, "", 0.84, "decision cue")
	}
	if hasAnyPhrase(q, "constraint", "rule", "requirement", "must", "must not", "should not", "guardrail", "policy", "non-negotiable") {
		return intentWith(original, QueryIntentConstraint, []string{MemoryRecordTypeConstraint}, []string{MemoryRecordScopeProject, MemoryRecordScopeUser, MemoryRecordScopeAgent}, "", 0.82, "constraint cue")
	}
	if hasAnyPhrase(q, "tool lesson", "tool_lesson", "tool failure", "command failed", "learned about", "next time use", "avoid using", "workflow lesson") {
		return intentWith(original, QueryIntentToolLesson, []string{MemoryRecordTypeToolLesson}, []string{MemoryRecordScopeAgent, MemoryRecordScopeProject, MemoryRecordScopeLocal}, "", 0.83, "tool lesson cue")
	}
	if hasAnyPhrase(q, "summary", "summarize", "recap", "overview", "session summary") {
		return intentWith(original, QueryIntentSummary, []string{MemoryRecordTypeSummary}, []string{MemoryRecordScopeSession, MemoryRecordScopeProject}, "", 0.76, "summary cue")
	}
	if hasAnyPhrase(q, "episode", "conversation", "session", "what happened", "when did", "previously", "earlier") {
		return intentWith(original, QueryIntentEpisodic, []string{MemoryRecordTypeEpisode}, []string{MemoryRecordScopeSession, MemoryRecordScopeLocal}, "", 0.70, "episodic cue")
	}
	if hasAnyPhrase(q, "reference", "link", "url", "doc", "docs", "documentation", "artifact", "file", "source") {
		return intentWith(original, QueryIntentReference, []string{MemoryRecordTypeReference, MemoryRecordTypeArtifactRef, MemoryRecordTypeFact}, []string{MemoryRecordScopeProject, MemoryRecordScopeLocal, MemoryRecordScopeTeam}, "", 0.74, "reference cue")
	}
	intent.Types = []string{}
	intent.Scopes = []string{}
	intent.Reasons = []string{"no strong intent cue"}
	return intent
}

func intentWith(query, name string, types, scopes []string, mode string, confidence float64, reason string) QueryIntent {
	return QueryIntent{Name: name, Types: normalizeIntentTypes(types), Scopes: normalizeIntentScopes(scopes), Mode: mode, SearchQuery: cleanIntentSearchQuery(query), Confidence: confidence, Reasons: []string{reason}}
}

func applyQueryIntentRouting(q MemoryQuery) (MemoryQuery, QueryIntent) {
	intent := ClassifyQueryIntent(q.Query)
	if !q.ExplicitTypes && len(q.Types) == 0 && len(intent.Types) > 0 {
		q.Types = append([]string(nil), intent.Types...)
	}
	if !q.ExplicitScopes && !q.ExplicitTypes && len(intent.Scopes) > 0 {
		q.Scopes = mergeStrings(q.Scopes, intent.Scopes)
	}
	if !q.ExplicitMode && strings.TrimSpace(q.Mode) == "" && intent.Mode != "" {
		q.Mode = intent.Mode
	}
	return q, intent
}

func cleanIntentSearchQuery(query string) string {
	tokens := tokenizeFTSQuery(query)
	if len(tokens) == 0 {
		return strings.TrimSpace(query)
	}
	cueStop := map[string]bool{
		"what": true, "whats": true, "which": true, "when": true, "where": true, "who": true, "how": true,
		"did": true, "does": true, "do": true, "was": true, "were": true, "is": true, "are": true, "about": true,
		"remember": true, "memory": true, "memories": true, "tell": true, "show": true, "find": true, "recall": true,
		"stored": true, "known": true, "our": true, "my": true, "we": true, "you": true, "me": true, "us": true, "i": true,
		"recent": true, "latest": true, "previous": true, "previously": true, "earlier": true,
		"preference": true, "preferences": true, "prefer": true, "decision": true, "decide": true, "decided": true,
		"constraint": true, "constraints": true, "rule": true, "rules": true, "summary": true, "audit": true,
		"reference": true, "references": true, "tool": true, "lesson": true, "lessons": true,
	}
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !cueStop[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return strings.Join(tokens, " ")
	}
	return strings.Join(out, " ")
}

func hasAnyPhrase(q string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(q, phrase) {
			return true
		}
	}
	return false
}

func normalizeIntentTypes(types []string) []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		out = append(out, NormalizeMemoryRecordType(t))
	}
	return normalizeStringSlice(out)
}

func normalizeIntentScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, NormalizeMemoryRecordScope(s))
	}
	return normalizeStringSlice(out)
}

func mergeStrings(base, extra []string) []string {
	return normalizeStringSlice(append(append([]string(nil), base...), extra...))
}
