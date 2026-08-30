package channels

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"
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
	ShouldReplyReasonModelTimeout         ShouldReplyGateReason = "model_timeout"
	ShouldReplyReasonModelError           ShouldReplyGateReason = "model_error"
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

// ShouldReplyModelHook is the optional asynchronous Tier-2 classifier for
// ambiguous traffic. Implementations must honor context cancellation.
type ShouldReplyModelHook interface {
	Classify(context.Context, ShouldReplyModelInput) (ShouldReplyModelVerdict, error)
}

// ShouldReplyModelHookFunc adapts a function to ShouldReplyModelHook.
type ShouldReplyModelHookFunc func(context.Context, ShouldReplyModelInput) (ShouldReplyModelVerdict, error)

func (f ShouldReplyModelHookFunc) Classify(ctx context.Context, in ShouldReplyModelInput) (ShouldReplyModelVerdict, error) {
	return f(ctx, in)
}

// ShouldReplyModelHookContext identifies the configured cheap-model deployment
// to use for one room without coupling channel code to a provider.
type ShouldReplyModelHookContext struct {
	AccountID   string
	ChannelName string
	RoomKey     string
	Model       string
}

// ShouldReplyModelHookResolver is registered by the daemon deployment.
type ShouldReplyModelHookResolver func(ShouldReplyModelHookContext) ShouldReplyModelHook

var shouldReplyModelResolverRegistry struct {
	sync.RWMutex
	nextID   uint64
	activeID uint64
	resolver ShouldReplyModelHookResolver
}

// RegisterShouldReplyModelHookResolver installs the deployment resolver. The
// returned closure unregisters only this exact registration.
func RegisterShouldReplyModelHookResolver(resolver ShouldReplyModelHookResolver) func() {
	shouldReplyModelResolverRegistry.Lock()
	shouldReplyModelResolverRegistry.nextID++
	id := shouldReplyModelResolverRegistry.nextID
	shouldReplyModelResolverRegistry.activeID = id
	shouldReplyModelResolverRegistry.resolver = resolver
	shouldReplyModelResolverRegistry.Unlock()
	return func() {
		shouldReplyModelResolverRegistry.Lock()
		if shouldReplyModelResolverRegistry.activeID == id {
			shouldReplyModelResolverRegistry.activeID = 0
			shouldReplyModelResolverRegistry.resolver = nil
		}
		shouldReplyModelResolverRegistry.Unlock()
	}
}

// GetShouldReplyModelHook resolves the currently registered deployment hook.
func GetShouldReplyModelHook(in ShouldReplyModelHookContext) ShouldReplyModelHook {
	shouldReplyModelResolverRegistry.RLock()
	resolver := shouldReplyModelResolverRegistry.resolver
	shouldReplyModelResolverRegistry.RUnlock()
	if resolver == nil {
		return nil
	}
	return resolver(in)
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
	return ResolveShouldReplyGateBounded(context.Background(), input, hook, 1500*time.Millisecond)
}

// ResolveShouldReplyGateBounded invokes Tier 2 under a hard wall-clock bound.
// Provider errors, invalid verdicts, and timeouts fail quiet.
func ResolveShouldReplyGateBounded(ctx context.Context, input ShouldReplyGateInput, hook ShouldReplyModelHook, timeout time.Duration) ShouldReplyGateDecision {
	decision := EvaluateShouldReplyHeuristics(input)
	if decision.Outcome != ShouldReplyAmbiguous {
		return decision
	}
	if hook == nil {
		decision.Outcome = ShouldReplyDrop
		decision.Reason = ShouldReplyReasonAmbiguousNoModel
		return decision
	}
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	boundedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	verdict, err := hook.Classify(boundedCtx, ShouldReplyModelInput{Input: input, Heuristic: decision})
	if err != nil {
		decision.Outcome = ShouldReplyDrop
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(boundedCtx.Err(), context.DeadlineExceeded) {
			decision.Reason = ShouldReplyReasonModelTimeout
		} else {
			decision.Reason = ShouldReplyReasonModelError
		}
		return decision
	}
	if boundedCtx.Err() != nil {
		decision.Outcome = ShouldReplyDrop
		decision.Reason = ShouldReplyReasonModelTimeout
		return decision
	}
	switch verdict {
	case ShouldReplyModelRespond:
		decision.Outcome = ShouldReplyPass
		decision.Reason = ShouldReplyReasonModelRespond
	case ShouldReplyModelIgnore:
		decision.Outcome = ShouldReplyDrop
		decision.Reason = ShouldReplyReasonModelIgnore
	case ShouldReplyModelStop:
		decision.Outcome = ShouldReplyDrop
		decision.Reason = ShouldReplyReasonModelStop
	default:
		decision.Outcome = ShouldReplyDrop
		decision.Reason = ShouldReplyReasonModelError
	}
	return decision
}
