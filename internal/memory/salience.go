package memory

import "strings"

const (
	SalienceSearchableThreshold = 0.70
	SalienceDurableThreshold    = 0.85
	SalienceDiscardThreshold    = 0.40
)

type SalienceDecision struct {
	Score        float64 `json:"score"`
	ProposedType string  `json:"proposed_type"`
	Reason       string  `json:"reason"`
	Promote      bool    `json:"promote"`
	Durable      bool    `json:"durable,omitempty"`
}

func ClassifyMemorySalience(text, role string, tags []string) SalienceDecision {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	if trimmed == "" {
		return SalienceDecision{Score: 0, ProposedType: MemoryRecordTypeEpisode, Reason: "empty", Promote: false}
	}
	if len([]rune(trimmed)) < minTurnLen {
		return SalienceDecision{Score: 0.15, ProposedType: MemoryRecordTypeEpisode, Reason: "too short / low information", Promote: false}
	}
	if isLowInformationChatter(lower) {
		return SalienceDecision{Score: 0.20, ProposedType: MemoryRecordTypeEpisode, Reason: "low-information chatter", Promote: false}
	}
	if explicitMemoryPayload(trimmed) != "" || containsAny(lower, "remember this", "save this", "don't forget", "do not forget", "store this") {
		return SalienceDecision{Score: 0.95, ProposedType: MemoryRecordTypeFact, Reason: "explicit memory request", Promote: true, Durable: true}
	}
	if containsAny(lower, "i prefer", "my preference", "always", "never", "please default", "i like", "i don't like", "i do not like") {
		return SalienceDecision{Score: 0.90, ProposedType: MemoryRecordTypePreference, Reason: "durable user preference", Promote: true, Durable: true}
	}
	if containsAny(lower, "we decided", "decision:", "decided to", "the decision is", "agreed to", "we will use", "we chose") {
		return SalienceDecision{Score: 0.90, ProposedType: MemoryRecordTypeDecision, Reason: "project decision", Promote: true, Durable: true}
	}
	if containsAny(lower, "constraint", "must", "must not", "cannot", "can't", "required", "requirement", "blocked by", "security", "compliance") {
		return SalienceDecision{Score: 0.82, ProposedType: MemoryRecordTypeConstraint, Reason: "constraint or requirement", Promote: true}
	}
	if containsAny(lower, "correction", "actually", "wrong", "instead", "prefer that you", "when you", "next time") && role == "user" {
		return SalienceDecision{Score: 0.86, ProposedType: MemoryRecordTypeFeedback, Reason: "user correction / feedback", Promote: true, Durable: true}
	}
	if containsAny(lower, "error", "failed", "fix was", "root cause", "diagnostic", "workaround", "command not found", "unauthorized", "permission denied") {
		return SalienceDecision{Score: 0.76, ProposedType: MemoryRecordTypeToolLesson, Reason: "reusable diagnostic/tool lesson", Promote: true}
	}
	if containsAny(lower, "deploy", "deployment", "config", "configuration", "api key", "model", "provider", "database", "sqlite", "docker", "container") {
		return SalienceDecision{Score: 0.72, ProposedType: MemoryRecordTypeFact, Reason: "project/config fact", Promote: true}
	}
	if len(tags) > 0 {
		return SalienceDecision{Score: 0.68, ProposedType: MemoryRecordTypeFact, Reason: "tagged memory candidate", Promote: false}
	}
	return SalienceDecision{Score: 0.35, ProposedType: MemoryRecordTypeEpisode, Reason: "not salient enough for searchable memory", Promote: false}
}

func ShouldIndexAutomaticTurn(text, role string) SalienceDecision {
	decision := ClassifyMemorySalience(text, role, nil)
	if decision.Score < SalienceSearchableThreshold {
		decision.Promote = false
	}
	return decision
}

func isLowInformationChatter(lower string) bool {
	lower = strings.TrimSpace(lower)
	if lower == "" {
		return true
	}
	chatter := []string{
		"ok", "okay", "yes", "no", "thanks", "thank you", "great", "sounds good", "got it", "cool", "nice", "yep", "nope", "sure", "please do", "go ahead",
	}
	for _, c := range chatter {
		if lower == c || lower == c+"." || lower == c+"!" {
			return true
		}
	}
	return false
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
