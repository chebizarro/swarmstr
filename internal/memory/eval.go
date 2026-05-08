package memory

import (
	"context"
	"sort"
	"time"
)

func DefaultSyntheticMemoryEvalCases() []MemoryEvalCase {
	return []MemoryEvalCase{
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
}

func RunMemoryEvals(ctx context.Context, store Store, cases []MemoryEvalCase) MemoryEvalRun {
	if len(cases) == 0 {
		cases = DefaultSyntheticMemoryEvalCases()
	}
	var run MemoryEvalRun
	run.CaseCount = len(cases)
	latencies := make([]float64, 0, len(cases))
	var hit5, hit10, noResult int
	for _, c := range cases {
		q := MemoryQuery{Query: c.Query, Limit: 10, IncludeSources: false}
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
	}
	if len(cases) > 0 {
		run.RecallAt5 = float64(hit5) / float64(len(cases))
		run.RecallAt10 = float64(hit10) / float64(len(cases))
		run.NoResultRate = float64(noResult) / float64(len(cases))
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
