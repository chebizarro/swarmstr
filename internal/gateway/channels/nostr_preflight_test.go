package channels

import (
	"testing"

	nostr "fiatjaf.com/nostr"
)

func hexKey() string { return nostr.Generate().Public().Hex() }

func boolp(b bool) *bool { return &b }

// base builds a minimal live-message input; tests tweak fields.
func basePreflight(bot, sender string) NostrPreflightInput {
	return NostrPreflightInput{
		BotPubkey:         bot,
		ChannelName:       "fleet",
		GroupID:           "room1",
		GroupAddress:      "relay.example'room1",
		SenderPubkey:      sender,
		Text:              "hello everyone",
		AllowTextCommands: true,
		Meta:              NostrInboundMeta{EventID: "evt1", DeliveryPhase: "live"},
	}
}

func TestPreflight_RoomKeyNormalization(t *testing.T) {
	got := NormalizeNostrRoomSessionKey("  Relay.Example ' Room1 ")
	want := "nostr:room:relay.example'room1"
	if got != want {
		t.Errorf("room key = %q, want %q", got, want)
	}
}

// Row 1: human @mentions agent in a requireMention room -> answer.
func TestPreflight_Row1_HumanMentionAnswers(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(true)
	in.Meta.MentionedPubkeys = []string{bot} // p-tag mention
	r := ResolveNostrGroupPreflight(in)
	if r.ShouldDrop {
		t.Fatalf("mentioned human should not drop, got %q", r.DropReason)
	}
	if !r.EffectiveWasMentioned || !r.ExplicitWasMentioned {
		t.Error("expected explicit mention")
	}
}

// Row 2: human ambient, requireMention=true -> drop no_mention.
func TestPreflight_Row2_AmbientDropsNoMention(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(true)
	r := ResolveNostrGroupPreflight(in)
	if !r.ShouldDrop || r.DropReason != DropNoMention {
		t.Fatalf("expected no_mention drop, got drop=%v reason=%q", r.ShouldDrop, r.DropReason)
	}
}

// Row 3: human ambient, requireMention=false, ambientPolicy respond -> answer, room_event off.
func TestPreflight_Row3_AmbientRequireMentionFalseAnswers(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(false)
	in.UnmentionedRoomEvent = false // user_request
	r := ResolveNostrGroupPreflight(in)
	if r.ShouldDrop {
		t.Fatalf("requireMention=false ambient should not drop, got %q", r.DropReason)
	}
	if r.InboundEventKind != InboundEventUserRequest {
		t.Errorf("kind = %q, want user_request", r.InboundEventKind)
	}
}

func TestPreflight_UnmentionedRoomEventClassification(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(false)
	in.UnmentionedRoomEvent = true // observe
	r := ResolveNostrGroupPreflight(in)
	if r.ShouldDrop {
		t.Fatalf("should not drop, got %q", r.DropReason)
	}
	if r.InboundEventKind != InboundEventRoomEvent {
		t.Errorf("unmentioned ambient with room_event policy: kind = %q, want room_event", r.InboundEventKind)
	}
}

// Row 4: known bot ambient, allowBots=mentions (default) -> drop bot_requires_mention.
func TestPreflight_Row4_KnownBotAmbientRequiresMention(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(true)
	in.SenderIsKnownBot = true
	in.SenderIsPeer = true
	in.AllowBots = AllowBotsMentions
	r := ResolveNostrGroupPreflight(in)
	if !r.ShouldDrop || r.DropReason != DropBotRequiresMention {
		t.Fatalf("expected bot_requires_mention, got drop=%v reason=%q", r.ShouldDrop, r.DropReason)
	}
}

// Row 5: known bot @mentions agent, allowBots=mentions -> answer.
func TestPreflight_Row5_KnownBotMentionAnswers(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(true)
	in.SenderIsKnownBot = true
	in.SenderIsPeer = true
	in.AllowBots = AllowBotsMentions
	in.Meta.MentionedPubkeys = []string{bot}
	r := ResolveNostrGroupPreflight(in)
	if r.ShouldDrop {
		t.Fatalf("explicitly-mentioned bot should pass under allowBots=mentions, got %q", r.DropReason)
	}
}

// Row 6: known bot, allowBots=off, even @mention -> drop bot_disallowed.
func TestPreflight_Row6_AllowBotsOffDropsEvenMention(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(true)
	in.SenderIsKnownBot = true
	in.SenderIsPeer = true
	in.AllowBots = AllowBotsOff
	in.Meta.MentionedPubkeys = []string{bot}
	r := ResolveNostrGroupPreflight(in)
	if !r.ShouldDrop || r.DropReason != DropBotDisallowed {
		t.Fatalf("expected bot_disallowed, got drop=%v reason=%q", r.ShouldDrop, r.DropReason)
	}
}

// Row 7: unknown sender (not known bot), allowBots=off -> NOT gated (treated human).
func TestPreflight_Row7_UnknownSenderNotGated(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(true)
	in.SenderIsKnownBot = false // unknown/human
	in.AllowBots = AllowBotsOff
	in.Meta.MentionedPubkeys = []string{bot}
	r := ResolveNostrGroupPreflight(in)
	if r.ShouldDrop {
		t.Fatalf("unknown sender must not be gated by allowBots, got %q", r.DropReason)
	}
}

func TestPreflight_AllowBotsAllPasses(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(false)
	in.SenderIsKnownBot = true
	in.SenderIsPeer = true
	in.AllowBots = AllowBotsAll
	r := ResolveNostrGroupPreflight(in)
	if r.ShouldDrop {
		t.Fatalf("allowBots=all should pass to the pair guard, got %q", r.DropReason)
	}
}

// Row 10: backfill-phase ambient bot burst -> dropped as backfill_ambient.
func TestPreflight_Row10_BackfillAmbientDrop(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(false) // so no_mention doesn't fire first
	in.Meta.DeliveryPhase = DeliveryPhaseBackfill
	r := ResolveNostrGroupPreflight(in)
	if !r.ShouldDrop || r.DropReason != DropBackfillAmbient {
		t.Fatalf("expected backfill_ambient, got drop=%v reason=%q", r.ShouldDrop, r.DropReason)
	}
}

func TestPreflight_BackfillMentionNotDropped(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(true)
	in.Meta.DeliveryPhase = DeliveryPhaseBackfill
	in.Meta.MentionedPubkeys = []string{bot}
	r := ResolveNostrGroupPreflight(in)
	if r.ShouldDrop {
		t.Fatalf("mentioned backfill message must not drop, got %q", r.DropReason)
	}
}

// Row 11: reply-to-bot from a HUMAN -> implicit mention answers; from a PEER -> suppressed.
func TestPreflight_Row11_ReplyToBotHumanVsPeer(t *testing.T) {
	bot, sender := hexKey(), hexKey()

	human := basePreflight(bot, sender)
	human.RequireMention = boolp(true)
	human.SenderIsPeer = false
	human.Meta.ReplyToSenderPubkey = bot
	r := ResolveNostrGroupPreflight(human)
	if r.ShouldDrop {
		t.Fatalf("reply-to-bot from human should answer, got %q", r.DropReason)
	}
	found := false
	for _, k := range r.ImplicitMentionKinds {
		if k == ImplicitMentionReplyToBot {
			found = true
		}
	}
	if !found {
		t.Error("expected reply_to_bot implicit mention for human")
	}

	peer := basePreflight(bot, sender)
	peer.RequireMention = boolp(true)
	peer.SenderIsPeer = true
	peer.Meta.ReplyToSenderPubkey = bot
	rp := ResolveNostrGroupPreflight(peer)
	if len(rp.ImplicitMentionKinds) != 0 {
		t.Errorf("peer implicit mentions must be suppressed, got %v", rp.ImplicitMentionKinds)
	}
	if !rp.ShouldDrop || rp.DropReason != DropNoMention {
		t.Errorf("peer reply-to-bot with no explicit mention should drop no_mention, got drop=%v reason=%q", rp.ShouldDrop, rp.DropReason)
	}
}

// Control command authorization: explicit allow_from authorizes; wildcard does not.
func TestPreflight_ControlCommandAuthorization(t *testing.T) {
	bot, sender := hexKey(), hexKey()

	// Unauthorized command -> unauthorized_control_command (highest precedence).
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(true)
	in.Text = "/status"
	r := ResolveNostrGroupPreflight(in)
	if !r.HasControlCommand || r.CommandName != "status" {
		t.Fatalf("expected control command status, got hasCmd=%v name=%q", r.HasControlCommand, r.CommandName)
	}
	if !r.ShouldDrop || r.DropReason != DropUnauthorizedControlCommand {
		t.Fatalf("unauthorized command should drop, got drop=%v reason=%q", r.ShouldDrop, r.DropReason)
	}

	// Wildcard does NOT authorize.
	in.RoomAllowFrom = []string{"*"}
	r = ResolveNostrGroupPreflight(in)
	if r.CommandAuthorized {
		t.Error("wildcard * must not authorize control commands")
	}

	// Explicit allow_from authorizes and bypasses mention requirement.
	in.RoomAllowFrom = []string{sender}
	r = ResolveNostrGroupPreflight(in)
	if !r.CommandAuthorized {
		t.Fatal("explicit allow_from should authorize the command")
	}
	if r.ShouldDrop {
		t.Fatalf("authorized command should bypass mention, got %q", r.DropReason)
	}
}

func TestPreflight_MentionExtractionFromText(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	in := basePreflight(bot, sender)
	in.RequireMention = boolp(true)
	// bare hex mention in the body
	in.Text = "hey " + bot + " please look"
	r := ResolveNostrGroupPreflight(in)
	if !r.ExplicitWasMentioned {
		t.Error("expected explicit mention from bare-hex in text")
	}
}

func TestResolveNostrAllowBots(t *testing.T) {
	cases := map[any]NostrAllowBots{
		"off": AllowBotsOff, "all": AllowBotsAll, "mentions": AllowBotsMentions,
		false: AllowBotsOff, true: AllowBotsAll,
		"garbage": AllowBotsMentions, nil: AllowBotsMentions,
	}
	for raw, want := range cases {
		if got := ResolveNostrAllowBots(raw); got != want {
			t.Errorf("ResolveNostrAllowBots(%v) = %q, want %q", raw, got, want)
		}
	}
}
