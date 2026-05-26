package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// DreamingPhase identifies a consolidation pass. Light sleep handles recent,
// high-confidence promotion candidates; REM can perform broader consolidation
// and narrative synthesis.
type DreamingPhase string

const (
	DreamingPhaseLight DreamingPhase = "light"
	DreamingPhaseREM   DreamingPhase = "rem"
)

// DreamingConfig configures phased memory promotion.
type DreamingConfig struct {
	Enabled           bool `json:"enabled"`
	LightLimit        int  `json:"light_limit,omitempty"`
	REMLimit          int  `json:"rem_limit,omitempty"`
	Narratives        bool `json:"narratives,omitempty"`
	NarrativeMaxChars int  `json:"narrative_max_chars,omitempty"`
}

// DreamingPhaseResult reports one phase of a dreaming run.
type DreamingPhaseResult struct {
	Phase       DreamingPhase `json:"phase"`
	Candidates  int           `json:"candidates"`
	Promoted    int           `json:"promoted"`
	Narrative   string        `json:"narrative,omitempty"`
	StartedUnix int64         `json:"started_unix"`
	EndedUnix   int64         `json:"ended_unix"`
}

// DreamingResult reports a complete phased dreaming run.
type DreamingResult struct {
	Phases     []DreamingPhaseResult `json:"phases"`
	Promoted   int                   `json:"promoted"`
	Narrative  string                `json:"narrative,omitempty"`
	DurationMS int64                 `json:"duration_ms"`
}

// DreamingNarrativeBuilder can replace the deterministic built-in narrative.
type DreamingNarrativeBuilder func(phase DreamingPhase, candidates []PromotionCandidate, promoted int) (string, error)

// RunDreamingPhases executes light and REM promotion phases using the existing
// PromotionManager. It is deterministic and event-free; schedulers can call it
// from cron without changing hot search paths.
func RunDreamingPhases(manager *PromotionManager, cfg DreamingConfig, builder DreamingNarrativeBuilder) (*DreamingResult, error) {
	start := time.Now()
	result := &DreamingResult{}
	if manager == nil || !cfg.Enabled {
		return result, nil
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.tracker != nil {
		if err := manager.tracker.Flush(); err != nil {
			return result, err
		}
	}
	if cfg.LightLimit <= 0 {
		cfg.LightLimit = 25
	}
	if cfg.REMLimit <= 0 {
		cfg.REMLimit = 100
	}
	if cfg.NarrativeMaxChars <= 0 {
		cfg.NarrativeMaxChars = 1200
	}
	phases := []struct {
		phase DreamingPhase
		limit int
	}{
		{DreamingPhaseLight, cfg.LightLimit},
		{DreamingPhaseREM, cfg.REMLimit},
	}
	for _, phaseCfg := range phases {
		phaseResult, err := runDreamingPhase(manager, phaseCfg.phase, phaseCfg.limit, cfg, builder)
		if err != nil {
			return result, err
		}
		result.Phases = append(result.Phases, phaseResult)
		result.Promoted += phaseResult.Promoted
	}
	if cfg.Narratives {
		parts := make([]string, 0, len(result.Phases))
		for _, phase := range result.Phases {
			if strings.TrimSpace(phase.Narrative) != "" {
				parts = append(parts, phase.Narrative)
			}
		}
		result.Narrative = truncateDreamingNarrative(strings.Join(parts, "\n\n"), cfg.NarrativeMaxChars)
	}
	result.DurationMS = time.Since(start).Milliseconds()
	return result, nil
}

func runDreamingPhase(manager *PromotionManager, phase DreamingPhase, limit int, cfg DreamingConfig, builder DreamingNarrativeBuilder) (DreamingPhaseResult, error) {
	started := time.Now().UTC().Unix()
	out := DreamingPhaseResult{Phase: phase, StartedUnix: started}
	if manager == nil {
		out.EndedUnix = time.Now().UTC().Unix()
		return out, nil
	}
	originalMax := manager.cfg.MaxBatchSize
	if limit > 0 && (manager.cfg.MaxBatchSize <= 0 || limit < manager.cfg.MaxBatchSize) {
		manager.cfg.MaxBatchSize = limit
	}
	candidates, err := manager.FindCandidates()
	manager.cfg.MaxBatchSize = originalMax
	if err != nil {
		out.EndedUnix = time.Now().UTC().Unix()
		return out, err
	}
	candidates = filterDreamingCandidates(phase, candidates, limit)
	out.Candidates = len(candidates)
	if len(candidates) > 0 {
		byTopic := map[string][]PromotionCandidate{}
		now := time.Now().UTC().Unix()
		for _, candidate := range candidates {
			topic := candidate.Memory.Topic
			if topic == "" {
				topic = manager.cfg.PromotedTopic
			}
			byTopic[topic] = append(byTopic[topic], candidate)
		}
		for topic, group := range byTopic {
			promoted, err := manager.promoteGroup(topic, group, now)
			if err != nil {
				continue
			}
			out.Promoted += promoted
		}
	}
	if cfg.Narratives {
		narrative := ""
		if builder != nil {
			narrative, err = builder(phase, candidates, out.Promoted)
			if err != nil {
				return out, err
			}
		}
		if strings.TrimSpace(narrative) == "" {
			narrative = BuildDreamingNarrative(phase, candidates, out.Promoted, cfg.NarrativeMaxChars)
		}
		out.Narrative = narrative
	}
	out.EndedUnix = time.Now().UTC().Unix()
	return out, nil
}

func filterDreamingCandidates(phase DreamingPhase, candidates []PromotionCandidate, limit int) []PromotionCandidate {
	if len(candidates) == 0 {
		return nil
	}
	out := make([]PromotionCandidate, len(candidates))
	copy(out, candidates)
	sort.SliceStable(out, func(i, j int) bool {
		if phase == DreamingPhaseLight {
			return out[i].RecallRecord.LastRecallUnix > out[j].RecallRecord.LastRecallUnix
		}
		return out[i].Score > out[j].Score
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// BuildDreamingNarrative creates a compact transparent report of what a phase
// considered and promoted. It avoids LLM calls while still giving users a
// human-readable sleep report.
func BuildDreamingNarrative(phase DreamingPhase, candidates []PromotionCandidate, promoted int, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 1200
	}
	topics := map[string]int{}
	for _, candidate := range candidates {
		topic := strings.TrimSpace(candidate.Memory.Topic)
		if topic == "" {
			topic = "uncategorized"
		}
		topics[topic]++
	}
	topicPairs := make([]string, 0, len(topics))
	for topic, count := range topics {
		topicPairs = append(topicPairs, fmt.Sprintf("%s=%d", topic, count))
	}
	sort.Strings(topicPairs)
	text := fmt.Sprintf("Dreaming phase %s reviewed %d candidates and promoted %d memories.", phase, len(candidates), promoted)
	if len(topicPairs) > 0 {
		text += " Topics: " + strings.Join(topicPairs, ", ") + "."
	}
	return truncateDreamingNarrative(text, maxChars)
}

func truncateDreamingNarrative(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	if maxChars <= 1 {
		return text[:maxChars]
	}
	return strings.TrimSpace(text[:maxChars-1]) + "…"
}
