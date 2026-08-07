package channels

import "testing"

var shouldReplyBase = ShouldReplyGateInput{
	Aliases:      []string{"Ada", "deploy-bot"},
	Capabilities: []string{"code-review", "task/run", "deployment"},
	Enabled:      true,
}

func TestShouldReplyGateNamedVersusTalkedAbout(t *testing.T) {
	direct := shouldReplyBase
	direct.Text = "Ada, can you review this patch?"
	got := EvaluateShouldReplyHeuristics(direct)
	if got.Outcome != ShouldReplyPass || got.Reason != ShouldReplyReasonDirectlyAddressed {
		t.Fatalf("direct request = %+v, want directly_addressed pass", got)
	}
	if !got.Facts.DirectlyAddressed || got.Facts.TalkedAbout {
		t.Fatalf("direct request facts = %+v", got.Facts)
	}

	about := shouldReplyBase
	about.Text = "Is Ada reviewing the release patch?"
	got = EvaluateShouldReplyHeuristics(about)
	if got.Outcome != ShouldReplyDrop || got.Reason != ShouldReplyReasonTalkedAbout {
		t.Fatalf("third-person question = %+v, want talked_about drop", got)
	}
	if got.Facts.DirectlyAddressed || !got.Facts.TalkedAbout {
		t.Fatalf("third-person facts = %+v", got.Facts)
	}
}

func TestShouldReplyGateCapabilityMatchAndNarration(t *testing.T) {
	request := shouldReplyBase
	request.Text = "Could someone review this code?"
	got := EvaluateShouldReplyHeuristics(request)
	if got.Outcome != ShouldReplyPass || got.Reason != ShouldReplyReasonCapabilityMatch {
		t.Fatalf("capability request = %+v, want capability_match pass", got)
	}
	if !got.Facts.CapabilityMatch || !got.Facts.Question {
		t.Fatalf("capability request facts = %+v", got.Facts)
	}

	narration := shouldReplyBase
	narration.Text = "The deployment completed after the code review."
	got = EvaluateShouldReplyHeuristics(narration)
	if got.Outcome != ShouldReplyDrop || got.Reason != ShouldReplyReasonNotQuestionOrRequest {
		t.Fatalf("capability narration = %+v, want not_question_or_request drop", got)
	}
	if got.Facts.Question || got.Facts.Request || !got.Facts.CapabilityMatch {
		t.Fatalf("narration facts = %+v", got.Facts)
	}
}

func TestShouldReplyGateQuestionAmbiguityAndTier2Hook(t *testing.T) {
	input := shouldReplyBase
	input.Text = "What do people think?"

	heuristic := EvaluateShouldReplyHeuristics(input)
	if heuristic.Outcome != ShouldReplyAmbiguous || !heuristic.Facts.Question {
		t.Fatalf("generic question heuristic = %+v, want question ambiguity", heuristic)
	}
	got := ResolveShouldReplyGate(input, nil)
	if got.Outcome != ShouldReplyDrop || got.Reason != ShouldReplyReasonAmbiguousNoModel {
		t.Fatalf("generic question without hook = %+v, want fail-quiet drop", got)
	}

	called := false
	got = ResolveShouldReplyGate(input, ShouldReplyModelHookFunc(func(modelInput ShouldReplyModelInput) ShouldReplyModelVerdict {
		called = true
		if modelInput.Heuristic.Outcome != ShouldReplyAmbiguous {
			t.Fatalf("hook heuristic = %+v", modelInput.Heuristic)
		}
		return ShouldReplyModelRespond
	}))
	if !called || got.Outcome != ShouldReplyPass || got.Reason != ShouldReplyReasonModelRespond {
		t.Fatalf("Tier-2 RESPOND = %+v called=%v", got, called)
	}
}

func TestShouldReplyGateKnownBotPTagAndOffSwitch(t *testing.T) {
	knownBot := shouldReplyBase
	knownBot.Text = "Can anyone help?"
	knownBot.SenderIsKnownBot = true
	got := EvaluateShouldReplyHeuristics(knownBot)
	if got.Outcome != ShouldReplyAmbiguous || got.Reason != ShouldReplyReasonKnownBotAmbiguous {
		t.Fatalf("known-bot ambiguity = %+v", got)
	}

	pTagged := shouldReplyBase
	pTagged.Text = "status update"
	pTagged.PTagged = true
	got = EvaluateShouldReplyHeuristics(pTagged)
	if got.Outcome != ShouldReplyPass || got.Reason != ShouldReplyReasonPTagged {
		t.Fatalf("p-tagged = %+v, want bypass pass", got)
	}

	disabled := shouldReplyBase
	disabled.Text = "ambient narration"
	disabled.Enabled = false
	got = EvaluateShouldReplyHeuristics(disabled)
	if got.Outcome != ShouldReplyPass || got.Reason != ShouldReplyReasonDisabled {
		t.Fatalf("disabled = %+v, want disabled pass", got)
	}
}

func TestShouldReplyGatePreflightIntegrationAndBypasses(t *testing.T) {
	bot, sender := hexKey(), hexKey()
	base := basePreflight(bot, sender)
	base.RequireMention = boolp(false)
	base.ShouldReplyGate = true
	base.AgentID = "Ada"
	base.ShouldReplyCapabilities = []string{"code-review"}

	named := base
	named.Text = "Ada, can you review this patch?"
	got := ResolveNostrGroupPreflight(named)
	if got.ShouldDrop || got.ShouldReplyGateDecision == nil || got.ShouldReplyGateDecision.Reason != ShouldReplyReasonDirectlyAddressed {
		t.Fatalf("named ambient request = %+v", got)
	}

	about := base
	about.Text = "Is Ada reviewing the release patch?"
	got = ResolveNostrGroupPreflight(about)
	if !got.ShouldDrop || got.DropReason != DropShouldReplyGate || got.ShouldReplyGateDecision == nil || got.ShouldReplyGateDecision.Reason != ShouldReplyReasonTalkedAbout {
		t.Fatalf("talked-about preflight = %+v", got)
	}

	capability := base
	capability.Text = "Could someone review this code?"
	got = ResolveNostrGroupPreflight(capability)
	if got.ShouldDrop || got.ShouldReplyGateDecision == nil || got.ShouldReplyGateDecision.Reason != ShouldReplyReasonCapabilityMatch {
		t.Fatalf("capability preflight = %+v", got)
	}

	disabled := base
	disabled.ShouldReplyGate = false
	disabled.Text = "ambient narration"
	got = ResolveNostrGroupPreflight(disabled)
	if got.ShouldDrop || got.ShouldReplyGateDecision == nil || got.ShouldReplyGateDecision.Reason != ShouldReplyReasonDisabled {
		t.Fatalf("off-switch preflight = %+v", got)
	}

	mentioned := base
	mentioned.Text = "ambient narration"
	mentioned.Meta.MentionedPubkeys = []string{bot}
	got = ResolveNostrGroupPreflight(mentioned)
	if got.ShouldDrop || got.ShouldReplyGateDecision != nil {
		t.Fatalf("explicit mention must bypass relevance gate: %+v", got)
	}

	reply := base
	reply.Text = "ambient narration"
	reply.Meta.ReplyToSenderPubkey = bot
	got = ResolveNostrGroupPreflight(reply)
	if got.ShouldDrop || got.ShouldReplyGateDecision != nil {
		t.Fatalf("reply-to-bot must bypass relevance gate: %+v", got)
	}

	command := base
	command.Text = "/status"
	command.RoomAllowFrom = []string{sender}
	got = ResolveNostrGroupPreflight(command)
	if got.ShouldDrop || !got.CommandAuthorized || got.ShouldReplyGateDecision != nil {
		t.Fatalf("authorized command must bypass relevance gate: %+v", got)
	}
}
