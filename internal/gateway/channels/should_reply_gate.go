package channels

import (
	"regexp"
	"strings"
)

// ShouldReplyGateOutcome is the Tier-1/Tier-2 admission result.
type ShouldReplyGateOutcome string

const (
	ShouldReplyPass      ShouldReplyGateOutcome = "pass"
	ShouldReplyDrop      ShouldReplyGateOutcome = "drop"
	ShouldReplyAmbiguous ShouldReplyGateOutcome = "ambiguous"
)

// ShouldReplyGateReason explains a deterministic or model-assisted decision.
type ShouldReplyGateReason string

const (
	ShouldReplyReasonDisabled             ShouldReplyGateReason = "disabled"
	ShouldReplyReasonPTagged              ShouldReplyGateReason = "p_tagged"
	ShouldReplyReasonDirectlyAddressed    ShouldReplyGateReason = "directly_addressed"
	ShouldReplyReasonTalkedAbout          ShouldReplyGateReason = "talked_about"
	ShouldReplyReasonNotQuestionOrRequest ShouldReplyGateReason = "not_question_or_request"
	ShouldReplyReasonCapabilityMatch      ShouldReplyGateReason = "capability_match"
	ShouldReplyReasonKnownBotAmbiguous    ShouldReplyGateReason = "known_bot_ambiguous"
	ShouldReplyReasonAmbiguous            ShouldReplyGateReason = "ambiguous"
	ShouldReplyReasonAmbiguousNoModel     ShouldReplyGateReason = "ambiguous_no_model"
	ShouldReplyReasonModelRespond         ShouldReplyGateReason = "model_respond"
	ShouldReplyReasonModelIgnore          ShouldReplyGateReason = "model_ignore"
	ShouldReplyReasonModelStop            ShouldReplyGateReason = "model_stop"
)

// ShouldReplyGateInput contains only cheap, local facts. Bot status is loop
// control information and must never be used as an authorization signal.
type ShouldReplyGateInput struct {
	Text             string
	Aliases          []string
	Capabilities     []string
	PTagged          bool
	SenderIsKnownBot bool
	Enabled          bool
}

// ShouldReplyGateFacts exposes the facts used to reach a decision.
type ShouldReplyGateFacts struct {
	DirectlyAddressed bool
	TalkedAbout       bool
	Question          bool
	Request           bool
	CapabilityMatch   bool
	SenderIsKnownBot  bool
}

// ShouldReplyGateDecision is the complete gate result.
type ShouldReplyGateDecision struct {
	Outcome ShouldReplyGateOutcome
	Reason  ShouldReplyGateReason
	Score   int
	Facts   ShouldReplyGateFacts
}

// ShouldReplyModelVerdict mirrors the reference Tier-2 RESPOND/IGNORE/STOP
// contract. Runtime wiring is intentionally deferred.
type ShouldReplyModelVerdict string

const (
	ShouldReplyModelRespond ShouldReplyModelVerdict = "RESPOND"
	ShouldReplyModelIgnore  ShouldReplyModelVerdict = "IGNORE"
	ShouldReplyModelStop    ShouldReplyModelVerdict = "STOP"
)

// ShouldReplyModelInput gives a cheap secondary classifier both the original
// input and the deterministic result.
type ShouldReplyModelInput struct {
	Input     ShouldReplyGateInput
	Heuristic ShouldReplyGateDecision
}

// ShouldReplyModelHook is the optional Tier-2 classifier for ambiguous traffic.
type ShouldReplyModelHook interface {
	Classify(ShouldReplyModelInput) ShouldReplyModelVerdict
}

// ShouldReplyModelHookFunc adapts a function to ShouldReplyModelHook.
type ShouldReplyModelHookFunc func(ShouldReplyModelInput) ShouldReplyModelVerdict

func (f ShouldReplyModelHookFunc) Classify(in ShouldReplyModelInput) ShouldReplyModelVerdict {
	return f(in)
}

var (
	shouldReplyRequestPhrase    = regexp.MustCompile(`(?i)\b(?:please|help\s+me|can\s+someone|could\s+someone|would\s+someone|take\s+a\s+look)\b`)
	shouldReplyImperativeStart  = regexp.MustCompile(`(?i)^\s*(?:review|check|fix|build|create|send|run|explain|summarize|find|update|implement|test|deploy|investigate|look\s+into|help)\b`)
	shouldReplyQuestionStart    = regexp.MustCompile(`(?i)^\s*(?:who|what|when|where|why|how|can|could|would|will|should|is|are|do|does|did|has|have)\b`)
	shouldReplyPhraseSeparators = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

const shouldReplyThirdPersonAfterName = `\s+(?:is|was|has|had|will|can|could|should|would|did|does|said|says|thinks|thought|works|worked|handles|handled|owns|owned|needs|needed)\b`

var shouldReplyStopWords = map[string]struct{}{
	"agent": {}, "channel": {}, "method": {}, "model": {}, "scope": {},
	"tool": {}, "tools": {}, "main": {}, "default": {},
}

func normalizeShouldReplyPhrase(value string) string {
	value = strings.ToLower(value)
	value = shouldReplyPhraseSeparators.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func normalizedShouldReplyAliases(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		alias := normalizeShouldReplyPhrase(value)
		if len(alias) < 2 {
			continue
		}
		if _, stop := shouldReplyStopWords[alias]; stop {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		out = append(out, alias)
	}
	return out
}

func detectShouldReplyAddressing(text string, aliases []string) (directlyAddressed, talkedAbout bool) {
	normalizedText := strings.ToLower(text)
	for _, alias := range aliases {
		escaped := strings.ReplaceAll(regexp.QuoteMeta(alias), " ", `\s+`)
		named := regexp.MustCompile(`(?i)\b` + escaped + `\b`)
		if !named.MatchString(normalizedText) {
			continue
		}

		atMention := regexp.MustCompile(`(?i)@` + escaped + `\b`).MatchString(normalizedText)
		openingVocative := regexp.MustCompile(`(?i)^\s*(?:(?:hey|hi|hello)\s+)?@?` + escaped + `\s*[,!:—-]`).MatchString(normalizedText)
		nameThenYouRequest := regexp.MustCompile(`(?i)\b` + escaped + `\b\s*,?\s*(?:can|could|would|will)\s+you\b`).MatchString(normalizedText)
		pleaseName := regexp.MustCompile(`(?i)\bplease\s+@?` + escaped + `\b`).MatchString(normalizedText)
		directlyAddressed = directlyAddressed || atMention || openingVocative || nameThenYouRequest || pleaseName

		thirdPerson := regexp.MustCompile(`(?i)\b` + escaped + `\b` + shouldReplyThirdPersonAfterName).MatchString(normalizedText)
		interrogativeAbout := regexp.MustCompile(
			`(?i)(?:\b(?:what|when|where|why|how|whether|if)\s+(?:can\s+|could\s+|does\s+|did\s+|is\s+|was\s+)?` +
				escaped +
				`\b|^\s*(?:is|was|has|did|does|can|could|will|would|should)\s+` +
				escaped +
				`\b)`,
		).MatchString(normalizedText)
		talkedAbout = talkedAbout || thirdPerson || interrogativeAbout
	}
	// Direct syntax wins over a coarse third-person pattern ("Ada, is this ready?").
	return directlyAddressed, talkedAbout && !directlyAddressed
}

func detectShouldReplyCapabilityMatch(text string, capabilities []string) bool {
	normalizedText := " " + normalizeShouldReplyPhrase(text) + " "
	for _, capability := range capabilities {
		phrase := normalizeShouldReplyPhrase(capability)
		if phrase == "" {
			continue
		}
		if len(phrase) >= 4 && strings.Contains(normalizedText, " "+phrase+" ") {
			return true
		}
		for _, token := range strings.Fields(phrase) {
			if len(token) < 4 {
				continue
			}
			if _, stop := shouldReplyStopWords[token]; stop {
				continue
			}
			if strings.Contains(normalizedText, " "+token+" ") {
				return true
			}
		}
	}
	return false
}

// EvaluateShouldReplyHeuristics applies the reference Tier-1 decision semantics.
func EvaluateShouldReplyHeuristics(input ShouldReplyGateInput) ShouldReplyGateDecision {
	aliases := normalizedShouldReplyAliases(input.Aliases)
	directlyAddressed, talkedAbout := detectShouldReplyAddressing(input.Text, aliases)
	question := strings.Contains(input.Text, "?") || shouldReplyQuestionStart.MatchString(input.Text)
	request := shouldReplyRequestPhrase.MatchString(input.Text) || shouldReplyImperativeStart.MatchString(input.Text)
	capabilityMatch := detectShouldReplyCapabilityMatch(input.Text, input.Capabilities)
	facts := ShouldReplyGateFacts{
		DirectlyAddressed: directlyAddressed,
		TalkedAbout:       talkedAbout,
		Question:          question,
		Request:           request,
		CapabilityMatch:   capabilityMatch,
		SenderIsKnownBot:  input.SenderIsKnownBot,
	}

	if !input.Enabled {
		return ShouldReplyGateDecision{Outcome: ShouldReplyPass, Reason: ShouldReplyReasonDisabled, Facts: facts}
	}
	if input.PTagged {
		return ShouldReplyGateDecision{Outcome: ShouldReplyPass, Reason: ShouldReplyReasonPTagged, Score: 100, Facts: facts}
	}
	if talkedAbout {
		// Pragmatic R1 rule: being talked about is not being talked to.
		return ShouldReplyGateDecision{Outcome: ShouldReplyDrop, Reason: ShouldReplyReasonTalkedAbout, Score: -6, Facts: facts}
	}
	if !question && !request {
		return ShouldReplyGateDecision{Outcome: ShouldReplyDrop, Reason: ShouldReplyReasonNotQuestionOrRequest, Facts: facts}
	}

	score := 0
	if directlyAddressed {
		score += 6
	}
	if question {
		score += 2
	}
	if request {
		score += 2
	}
	if capabilityMatch {
		score += 3
	}
	if input.SenderIsKnownBot {
		score -= 3
	}

	if directlyAddressed {
		return ShouldReplyGateDecision{Outcome: ShouldReplyPass, Reason: ShouldReplyReasonDirectlyAddressed, Score: score, Facts: facts}
	}
	if capabilityMatch && score >= 4 {
		return ShouldReplyGateDecision{Outcome: ShouldReplyPass, Reason: ShouldReplyReasonCapabilityMatch, Score: score, Facts: facts}
	}
	reason := ShouldReplyReasonAmbiguous
	if input.SenderIsKnownBot {
		reason = ShouldReplyReasonKnownBotAmbiguous
	}
	return ShouldReplyGateDecision{Outcome: ShouldReplyAmbiguous, Reason: reason, Score: score, Facts: facts}
}

// ResolveShouldReplyGate resolves ambiguity with an optional cheap-model hook.
// Without one it fails quiet rather than spending a full agent turn.
func ResolveShouldReplyGate(input ShouldReplyGateInput, hook ShouldReplyModelHook) ShouldReplyGateDecision {
	decision := EvaluateShouldReplyHeuristics(input)
	if decision.Outcome != ShouldReplyAmbiguous {
		return decision
	}
	if hook == nil {
		decision.Outcome = ShouldReplyDrop
		decision.Reason = ShouldReplyReasonAmbiguousNoModel
		return decision
	}
	switch hook.Classify(ShouldReplyModelInput{Input: input, Heuristic: decision}) {
	case ShouldReplyModelRespond:
		decision.Outcome = ShouldReplyPass
		decision.Reason = ShouldReplyReasonModelRespond
	case ShouldReplyModelStop:
		decision.Outcome = ShouldReplyDrop
		decision.Reason = ShouldReplyReasonModelStop
	default:
		decision.Outcome = ShouldReplyDrop
		decision.Reason = ShouldReplyReasonModelIgnore
	}
	return decision
}
