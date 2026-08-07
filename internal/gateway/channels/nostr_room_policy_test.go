package channels

import (
	"testing"
	"time"
)

func TestResolveNostrRoomPolicy_Defaults(t *testing.T) {
	p := ResolveNostrRoomPolicy(nil)
	if p.RequireMention != nil {
		t.Error("requireMention should be unset (nil) by default")
	}
	if p.AllowBots != AllowBotsMentions {
		t.Errorf("allowBots default = %q, want mentions", p.AllowBots)
	}
	if !p.AckAsReaction {
		t.Error("ackAsReaction should default true")
	}
	if p.AmbientRespond || p.UnmentionedRoomEvent || p.EchoSuppression || p.CommitmentGuard {
		t.Error("opt-in boolean knobs should default false")
	}
	if p.PairLoop != nil {
		t.Error("PairLoop should be nil when unset")
	}
}

func TestResolveNostrRoomPolicy_Typed(t *testing.T) {
	cfg := map[string]any{
		"requireMention":     false,
		"allowBots":          "off",
		"ambientPolicy":      "respond",
		"unmentionedInbound": "room_event",
		"ackAsReaction":      false,
		"echoSuppression":    true,
		"commitmentGuard":    true,
	}
	p := ResolveNostrRoomPolicy(cfg)
	if p.RequireMention == nil || *p.RequireMention != false {
		t.Errorf("requireMention = %v, want ptr(false)", p.RequireMention)
	}
	if p.AllowBots != AllowBotsOff {
		t.Errorf("allowBots = %q, want off", p.AllowBots)
	}
	if !p.AmbientRespond {
		t.Error("ambientPolicy respond -> AmbientRespond true")
	}
	if !p.UnmentionedRoomEvent {
		t.Error("unmentionedInbound room_event -> true")
	}
	if p.AckAsReaction {
		t.Error("ackAsReaction false should opt out")
	}
	if !p.EchoSuppression || !p.CommitmentGuard {
		t.Error("echoSuppression/commitmentGuard should be true")
	}
}

func TestResolveNostrRoomPolicy_PairLoopOverride(t *testing.T) {
	// JSON decodes numbers as float64.
	cfg := map[string]any{
		"botLoopProtection": map[string]any{
			"maxEventsPerWindow": float64(5),
			"windowSeconds":      float64(30),
			"enabled":            true,
		},
	}
	p := ResolveNostrRoomPolicy(cfg)
	if p.PairLoop == nil {
		t.Fatal("expected PairLoop override")
	}
	// Feed it through the settings resolver to confirm it takes effect.
	s := ResolvePairLoopGuardSettings(p.PairLoop, nil, true)
	if s.MaxEventsPerWindow != 5 {
		t.Errorf("maxEvents = %d, want 5", s.MaxEventsPerWindow)
	}
	if s.Window != 30*time.Second {
		t.Errorf("window = %v, want 30s", s.Window)
	}
	// cooldown falls back to builtin default (60s).
	if s.Cooldown != 60*time.Second {
		t.Errorf("cooldown = %v, want 60s (builtin fallback)", s.Cooldown)
	}
}

func TestResolveNostrRoomPolicy_EchoThreshold(t *testing.T) {
	p := ResolveNostrRoomPolicy(map[string]any{"echoSuppression": true, "echoSimilarityThreshold": float64(0.7)})
	if !p.EchoSuppression {
		t.Error("echoSuppression should be true")
	}
	if p.EchoThreshold != 0.7 {
		t.Errorf("EchoThreshold = %v, want 0.7", p.EchoThreshold)
	}
	// Out-of-range threshold is ignored (stays 0 -> suppressor default).
	p = ResolveNostrRoomPolicy(map[string]any{"echoSimilarityThreshold": float64(2)})
	if p.EchoThreshold != 0 {
		t.Errorf("out-of-range threshold must be ignored, got %v", p.EchoThreshold)
	}
}

func TestResolveNostrRoomPolicy_AllowBotsBoolCoercion(t *testing.T) {
	if ResolveNostrRoomPolicy(map[string]any{"allowBots": true}).AllowBots != AllowBotsAll {
		t.Error("allowBots true -> all")
	}
	if ResolveNostrRoomPolicy(map[string]any{"allowBots": false}).AllowBots != AllowBotsOff {
		t.Error("allowBots false -> off")
	}
}

func TestResolveNostrRoomPolicy_IgnoresGarbageTypes(t *testing.T) {
	// Wrong types must not panic and should fall back to defaults.
	cfg := map[string]any{
		"requireMention":    "yes",  // not a bool
		"ackAsReaction":     "yes",  // not a bool
		"ambientPolicy":     123,    // not a string
		"botLoopProtection": "nope", // not a map
	}
	p := ResolveNostrRoomPolicy(cfg)
	if p.RequireMention != nil {
		t.Error("non-bool requireMention must stay unset")
	}
	if p.AmbientRespond {
		t.Error("non-string ambientPolicy must stay default")
	}
	if !p.AckAsReaction {
		t.Error("non-bool ackAsReaction must retain the enabled default")
	}
	if p.PairLoop != nil {
		t.Error("non-map botLoopProtection must stay nil")
	}
}
