package channels

import (
	"math"
	"strings"
	"time"
)

// Responder-election (R2) defaults: the takeover window the successor waits
// before claiming a contested event, and the minimum a room may configure.
const (
	DefaultResponderElectionTakeover    = 120 * time.Second
	MinResponderElectionTakeoverSeconds = 5
)

// NostrRoomPolicy holds the per-room knobs the preflight and context builder
// need, resolved from a room's free-form NostrChannelConfig.Config map. Mirrors
// the reference resolvers (resolveNostrRequireMention / resolveNostrRoomAllowBots
// / resolveNostrRoomAmbientPolicy / unmentionedInbound).
type NostrRoomPolicy struct {
	// RequireMention is the room config value; nil means "unset" so the preflight
	// applies its default of true.
	RequireMention *bool
	// AllowBots gates known-bot senders (default "mentions").
	AllowBots NostrAllowBots
	// AmbientRespond is true when ambientPolicy == "respond" (raw body); false is
	// the default "scan" (cautionary wrapper).
	AmbientRespond bool
	// UnmentionedRoomEvent is true when unmentionedInbound == "room_event".
	UnmentionedRoomEvent bool
	// ShouldReplyGate runs the cheap deterministic relevance gate for ambient,
	// non-mention room traffic. It defaults on; false preserves legacy dispatch.
	ShouldReplyGate bool
	// ResponderElection runs the deterministic single-responder election (R2)
	// for ambient traffic the should-reply gate admits. It defaults on; false
	// lets every capable agent answer (legacy behavior).
	ResponderElection bool
	// ResponderElectionTakeover is how long the successor waits for the
	// elected responder before claiming the event with a 🙋 reaction
	// (config key responderElectionTakeoverSeconds, ≥ 5, default 120).
	ResponderElectionTakeover time.Duration
	// PairLoop is the room-level bot-loop-protection override (nil when unset).
	PairLoop *PairLoopGuardConfig
	// AckAsReaction converts pure acknowledgements into NIP-25 reactions on
	// the inbound event instead of posting a new room message. It defaults to
	// true; rooms can explicitly opt out with ackAsReaction=false.
	AckAsReaction bool
	// EchoSuppression / CommitmentGuard opt-ins (secondary defenses).
	EchoSuppression bool
	// EchoThreshold is the per-room echo similarity bar (0..1); 0 means use the
	// suppressor default.
	EchoThreshold float64
	// TaskEchoSuppression suppresses the "chat shadow" of kind-30900 fleet-task
	// transitions (R6): a chat message by the same author substantially
	// restating a recent transition is dropped (one compact throttled
	// announcement is allowed). Defaults ON when echoSuppression is on;
	// taskEchoSuppression=false opts out (or true forces it on alone).
	TaskEchoSuppression bool
	// TaskEchoThreshold is the per-room task-shadow coverage bar (0..1); 0
	// means use the suppressor's task default.
	TaskEchoThreshold float64
	CommitmentGuard   bool
	// CommitmentEnforcement blocks/rephrases unbacked outbound work promises.
	// It is explicit opt-in because rooms do not expose a separate taskflow flag.
	CommitmentEnforcement bool
}

// ResolveNostrRoomPolicy reads typed preflight knobs from a room's free-form
// config map. Unknown/missing keys fall back to defaults; the map is never
// mutated. The bot/loop-control knobs here never influence allow_from or command
// authorization (trust boundary).
func ResolveNostrRoomPolicy(config map[string]any) NostrRoomPolicy {
	p := NostrRoomPolicy{
		AllowBots:                 AllowBotsMentions,
		ShouldReplyGate:           true,
		AckAsReaction:             true,
		ResponderElection:         true,
		ResponderElectionTakeover: DefaultResponderElectionTakeover,
	}
	if config == nil {
		return p
	}

	if v, ok := boolFromAny(config["requireMention"]); ok {
		p.RequireMention = &v
	}

	if raw, ok := config["allowBots"]; ok {
		p.AllowBots = ResolveNostrAllowBots(raw)
	}

	if s, ok := config["ambientPolicy"].(string); ok {
		p.AmbientRespond = strings.EqualFold(strings.TrimSpace(s), "respond")
	}

	if s, ok := config["unmentionedInbound"].(string); ok {
		p.UnmentionedRoomEvent = strings.EqualFold(strings.TrimSpace(s), "room_event")
	}
	if v, ok := boolFromAny(config["shouldReplyGate"]); ok {
		p.ShouldReplyGate = v
	}
	if v, ok := boolFromAny(config["responderElection"]); ok {
		p.ResponderElection = v
	}
	// Out-of-range or non-numeric takeover windows keep the default (mirrors
	// the reference resolver: finite number ≥ 5 seconds only).
	if f, ok := floatFromAny(config["responderElectionTakeoverSeconds"]); ok &&
		!math.IsNaN(f) && !math.IsInf(f, 0) && f >= MinResponderElectionTakeoverSeconds {
		p.ResponderElectionTakeover = time.Duration(math.Round(f*1000)) * time.Millisecond
	}

	if v, ok := boolFromAny(config["ackAsReaction"]); ok {
		p.AckAsReaction = v
	}
	if v, ok := boolFromAny(config["echoSuppression"]); ok {
		p.EchoSuppression = v
	}
	if f, ok := floatFromAny(config["echoSimilarityThreshold"]); ok && f > 0 && f <= 1 {
		p.EchoThreshold = f
	}
	// taskEchoSuppression follows echoSuppression unless set explicitly.
	p.TaskEchoSuppression = p.EchoSuppression
	if v, ok := boolFromAny(config["taskEchoSuppression"]); ok {
		p.TaskEchoSuppression = v
	}
	if f, ok := floatFromAny(config["taskEchoSimilarityThreshold"]); ok && f > 0 && f <= 1 {
		p.TaskEchoThreshold = f
	}
	if v, ok := boolFromAny(config["commitmentGuard"]); ok {
		p.CommitmentGuard = v
	}
	if v, ok := boolFromAny(config["commitmentEnforcement"]); ok {
		p.CommitmentEnforcement = v
	}

	p.PairLoop = resolveRoomPairLoopConfig(config["botLoopProtection"])

	return p
}

// boolFromAny coerces a JSON-decoded value to bool (true only for a real bool).
func boolFromAny(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

// floatFromAny coerces a JSON-decoded numeric to a float64.
func floatFromAny(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// intFromAny coerces a JSON-decoded numeric (float64/int) to a positive int.
func intFromAny(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

func resolveRoomPairLoopConfig(raw any) *PairLoopGuardConfig {
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	cfg := &PairLoopGuardConfig{}
	set := false
	if v, ok := boolFromAny(m["enabled"]); ok {
		cfg.Enabled = &v
		set = true
	}
	if v, ok := intFromAny(m["maxEventsPerWindow"]); ok {
		cfg.MaxEventsPerWindow = &v
		set = true
	}
	if v, ok := intFromAny(m["windowSeconds"]); ok {
		cfg.WindowSeconds = &v
		set = true
	}
	if v, ok := intFromAny(m["cooldownSeconds"]); ok {
		cfg.CooldownSeconds = &v
		set = true
	}
	if !set {
		return nil
	}
	return cfg
}
