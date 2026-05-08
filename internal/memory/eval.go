package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func DefaultSyntheticMemoryEvalCases() []MemoryEvalCase {
	base := []MemoryEvalCase{
		{ID: "pref-editor", Query: "preferred editor", ExpectedSubject: "editor", ExpectedType: MemoryRecordTypePreference, Scope: MemoryRecordScopeUser},
		{ID: "decision-deploy", Query: "deployment canary decision", ExpectedSubject: "deployment", ExpectedType: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject},
		{ID: "constraint-security", Query: "security requirement", ExpectedType: MemoryRecordTypeConstraint, Scope: MemoryRecordScopeProject},
		{ID: "feedback-style", Query: "response style feedback", ExpectedType: MemoryRecordTypeFeedback, Scope: MemoryRecordScopeUser},
		{ID: "tool-docker", Query: "docker command not found", ExpectedType: MemoryRecordTypeToolLesson, Scope: MemoryRecordScopeProject},
		{ID: "reference-dashboard", Query: "dashboard link", ExpectedType: MemoryRecordTypeReference, Scope: MemoryRecordScopeProject},
		{ID: "summary-session", Query: "session summary", ExpectedType: MemoryRecordTypeSummary, Scope: MemoryRecordScopeSession},
		{ID: "fact-provider", Query: "provider base url", ExpectedType: MemoryRecordTypeFact, Scope: MemoryRecordScopeProject},
		{ID: "artifact-spec", Query: "architecture spec artifact", ExpectedType: MemoryRecordTypeArtifactRef, Scope: MemoryRecordScopeProject},
		{ID: "team-runbook", Query: "team runbook", ExpectedType: MemoryRecordTypeReference, Scope: MemoryRecordScopeTeam},
	}
	cases := make([]MemoryEvalCase, 0, 60)
	groups := []struct {
		prefix string
		note   string
		query  string
	}{
		{prefix: "prefs", note: "preferences and feedback", query: "preferred"},
		{prefix: "decisions", note: "decision recall and supersession", query: "decision"},
		{prefix: "episodic", note: "episode and summary recall", query: "session"},
		{prefix: "stale", note: "stale/superseded avoidance", query: "latest"},
		{prefix: "scope", note: "scope filters", query: "project"},
		{prefix: "intent", note: "intent routing", query: "why"},
	}
	for gi, g := range groups {
		for bi, b := range base {
			c := b
			c.ID = fmt.Sprintf("%s-%02d", g.prefix, bi+1)
			c.Query = strings.TrimSpace(g.query + " " + b.Query)
			c.Notes = g.note
			if gi >= 3 {
				c.ExpectedIDs = nil
			}
			cases = append(cases, c)
		}
	}
	return cases
}

func RunMemoryEvals(ctx context.Context, store Store, cases []MemoryEvalCase) MemoryEvalRun {
	if len(cases) == 0 {
		cases = DefaultSyntheticMemoryEvalCases()
	}
	var run MemoryEvalRun
	run.CaseCount = len(cases)
	latencies := make([]float64, 0, len(cases))
	var hit5, hit10, noResult int
	var staleHits, supersededHits, duplicateHits int
	var tokenCost int
	for _, c := range cases {
		q := MemoryQuery{Query: c.Query, Limit: 10, IncludeSources: false, IncludeDebug: true}
		if c.Scope != "" {
			q.Scopes = []string{c.Scope}
		}
		start := time.Now()
		cards, err := QueryMemoryRecords(ctx, store, q)
		latencies = append(latencies, float64(time.Since(start).Microseconds())/1000)
		if err != nil || len(cards) == 0 {
			noResult++
			run.FailedCaseIDs = append(run.FailedCaseIDs, c.ID)
			continue
		}
		if evalHit(cards, c, 5) {
			hit5++
		}
		if evalHit(cards, c, 10) {
			hit10++
		} else {
			run.FailedCaseIDs = append(run.FailedCaseIDs, c.ID)
		}
		tokenCost += roughTokenCost(cards)
		for _, card := range cards {
			if isStaleCard(card) {
				staleHits++
			}
			if card.Why != nil && hasWhyReason(card.Why.Reasons, "superseded") {
				supersededHits++
			}
		}
		duplicateHits += duplicateCardCount(cards)
	}
	if len(cases) > 0 {
		run.RecallAt5 = float64(hit5) / float64(len(cases))
		run.RecallAt10 = float64(hit10) / float64(len(cases))
		run.NoResultRate = float64(noResult) / float64(len(cases))
	}
	totalHits := maxEvalInt(1, hit10)
	run.StaleHitRate = float64(staleHits) / float64(totalHits)
	run.SupersededHitRate = float64(supersededHits) / float64(totalHits)
	run.DuplicateRate = float64(duplicateHits) / float64(totalHits)
	run.TokenCost = tokenCost

	if stats, ok := MemoryObservabilityStats(store); ok {
		run.ReflectionPrecision = stats.ReflectionPrecision
		run.PromotionAcceptance = stats.PromotionAcceptance
	}

	sort.Float64s(latencies)
	run.P50LatencyMS = percentile(latencies, 0.50)
	run.P95LatencyMS = percentile(latencies, 0.95)
	run.P99LatencyMS = percentile(latencies, 0.99)
	return run
}

func evalHit(cards []MemoryCard, c MemoryEvalCase, k int) bool {
	if len(cards) < k {
		k = len(cards)
	}
	for i := 0; i < k; i++ {
		card := cards[i]
		if len(c.ExpectedIDs) > 0 {
			for _, id := range c.ExpectedIDs {
				if card.ID == id {
					return true
				}
			}
		}
		if c.ExpectedSubject != "" && card.Subject == c.ExpectedSubject {
			return true
		}
		if c.ExpectedType != "" && card.Type == c.ExpectedType {
			return true
		}
	}
	return false
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if p <= 0 {
		return values[0]
	}
	idx := int(float64(len(values)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return values[idx]
}

func roughTokenCost(cards []MemoryCard) int {
	return EstimateMemoryCardsTokenCost(cards)
}

func isStaleCard(card MemoryCard) bool {
	if card.Why == nil {
		return false
	}
	if hasWhyReason(card.Why.Reasons, "stale") {
		return true
	}
	if v, ok := card.Why.Components["stale_penalty"]; ok {
		return v < 0
	}
	return false
}

func hasWhyReason(reasons []string, key string) bool {
	for _, r := range reasons {
		if strings.Contains(strings.ToLower(r), key) {
			return true
		}
	}
	return false
}

func duplicateCardCount(cards []MemoryCard) int {
	seen := map[string]struct{}{}
	dups := 0
	for _, c := range cards {
		text := strings.TrimSpace(strings.ToLower(firstNonEmpty(c.Text, c.Summary)))
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			dups++
			continue
		}
		seen[text] = struct{}{}
	}
	return dups
}

func maxEvalInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
