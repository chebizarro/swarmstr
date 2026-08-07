package channels

import "strings"

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
	EchoThreshold   float64
	CommitmentGuard bool
}

// ResolveNostrRoomPolicy reads typed preflight knobs from a room's free-form
// config map. Unknown/missing keys fall back to defaults; the map is never
// mutated. The bot/loop-control knobs here never influence allow_from or command
// authorization (trust boundary).
func ResolveNostrRoomPolicy(config map[string]any) NostrRoomPolicy {
	p := NostrRoomPolicy{AllowBots: AllowBotsMentions, AckAsReaction: true}
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

	if v, ok := boolFromAny(config["ackAsReaction"]); ok {
		p.AckAsReaction = v
	}
	if v, ok := boolFromAny(config["echoSuppression"]); ok {
		p.EchoSuppression = v
	}
	if f, ok := floatFromAny(config["echoSimilarityThreshold"]); ok && f > 0 && f <= 1 {
		p.EchoThreshold = f
	}
	if v, ok := boolFromAny(config["commitmentGuard"]); ok {
		p.CommitmentGuard = v
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
