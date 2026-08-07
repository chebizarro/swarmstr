package channels

// responder_election_test.go ports the reference suite
// (openclaw-nostr src/responder-election.test.ts + channel.election.test.ts,
// ocn-myl): the Go election must reach the same decisions from the same
// inputs as the reference implementation.

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var (
	electionAgentA = strings.Repeat("a", 64)
	electionAgentB = strings.Repeat("b", 64)
	electionAgentC = strings.Repeat("c", 64)
	electionHuman  = strings.Repeat("d", 64)
	electionEvent  = strings.Repeat("e", 64)
)

const electionRoom = "nostr:room:groups.sharegap.net'fleet-ops"

func electionCandidates(entries ...[2]any) []ResponderCandidate {
	out := make([]ResponderCandidate, 0, len(entries))
	for _, e := range entries {
		out = append(out, ResponderCandidate{Pubkey: e[0].(string), Capabilities: e[1].([]string)})
	}
	return out
}

func TestScoreResponderCapabilityMatch(t *testing.T) {
	text := "Can someone deploy the gateway and check relay health?"
	score := ScoreResponderCapabilityMatch(text, []string{"deploy", "relay-health", "deploy"})
	if dedup := ScoreResponderCapabilityMatch(text, []string{"deploy", "relay-health"}); score != dedup {
		t.Fatalf("duplicate capability changed the score: %d != %d", score, dedup)
	}
	if score <= 0 {
		t.Fatalf("expected a positive score, got %d", score)
	}
	// "deploy" phrase (+2) + token (+1); "relay health" phrase contiguous (+2)
	// + tokens "relay" (+1) and "health" (+1).
	if want := 7; score != want {
		t.Fatalf("score = %d, want %d", score, want)
	}
	if got := ScoreResponderCapabilityMatch("good morning everyone", []string{"deploy", "review"}); got != 0 {
		t.Fatalf("no-match score = %d, want 0", got)
	}
	if got := ScoreResponderCapabilityMatch("the main agent tool", []string{"main", "agent", "tool", "ab"}); got != 0 {
		t.Fatalf("stop words / short tokens conferred weight: %d", got)
	}
}

func TestHashResponderEventID_MatchesReferenceFNV1a(t *testing.T) {
	// FNV-1a 32-bit reference vectors (offset basis / "a") plus
	// case/whitespace normalization.
	if got := HashResponderEventID(""); got != 0x811c9dc5 {
		t.Fatalf("hash(\"\") = %#x, want offset basis", got)
	}
	if got := HashResponderEventID("a"); got != 0xe40c292c {
		t.Fatalf("hash(\"a\") = %#x, want 0xe40c292c", got)
	}
	if HashResponderEventID(" ABC ") != HashResponderEventID("abc") {
		t.Fatal("hash must trim and lowercase")
	}
}

func TestComputeResponderElection_DeterministicAcrossInstances(t *testing.T) {
	fleet := electionCandidates(
		[2]any{electionAgentA, []string{"deploy", "gateway"}},
		[2]any{electionAgentB, []string{"deploy"}},
		[2]any{electionAgentC, []string{"code-review"}},
	)
	text := "please deploy the gateway"
	perspective := func(self string) *ResponderElectionResult {
		return ComputeResponderElection(ResponderElectionInput{
			EventID: electionEvent, Text: text, SelfPubkey: self, Candidates: fleet,
		})
	}
	fromA, fromB, fromC := perspective(electionAgentA), perspective(electionAgentB), perspective(electionAgentC)
	if !reflect.DeepEqual(fromA.Order, fromB.Order) || !reflect.DeepEqual(fromB.Order, fromC.Order) {
		t.Fatalf("instances disagree on order: %v / %v / %v", fromA.Order, fromB.Order, fromC.Order)
	}
	if fromA.ElectedPubkey != fromB.ElectedPubkey || fromB.ElectedPubkey != fromC.ElectedPubkey {
		t.Fatal("instances disagree on the elected responder")
	}
	elected := 0
	for _, r := range []*ResponderElectionResult{fromA, fromB, fromC} {
		if r.Role == ResponderRoleElected {
			elected++
		}
	}
	if elected != 1 {
		t.Fatalf("exactly one instance must see itself elected, got %d", elected)
	}
	if again := perspective(electionAgentA); !reflect.DeepEqual(again, fromA) {
		t.Fatal("repeated evaluation is not stable")
	}
}

func TestComputeResponderElection_RanksBetterCapabilityMatchFirst(t *testing.T) {
	fleet := electionCandidates(
		[2]any{electionAgentA, []string{"deploy", "gateway"}},
		[2]any{electionAgentB, []string{"deploy"}},
		[2]any{electionAgentC, []string{"code-review"}},
	)
	result := ComputeResponderElection(ResponderElectionInput{
		EventID: electionEvent, Text: "please deploy the gateway",
		SelfPubkey: electionAgentC, Candidates: fleet,
	})
	if result.ElectedPubkey != electionAgentA {
		t.Fatalf("elected = %s, want the multi-capability agent A", result.ElectedPubkey)
	}
	if want := []string{electionAgentA, electionAgentB}; !reflect.DeepEqual(result.Order, want) {
		t.Fatalf("order = %v, want %v", result.Order, want)
	}
	if result.EligibleCount != 2 || result.Role != ResponderRoleNotCandidate || result.SelfIndex != -1 {
		t.Fatalf("unmatched self must be not_candidate: %+v", result)
	}
}

func TestComputeResponderElection_HashTieBreakRotatesBand(t *testing.T) {
	tied := electionCandidates(
		[2]any{electionAgentA, []string{"deploy"}},
		[2]any{electionAgentB, []string{"deploy"}},
		[2]any{electionAgentC, []string{"deploy"}},
	)
	text := "please deploy this"
	result := ComputeResponderElection(ResponderElectionInput{
		EventID: electionEvent, Text: text, SelfPubkey: electionAgentA, Candidates: tied,
	})
	rotation := int(HashResponderEventID(electionEvent) % 3)
	sorted := []string{electionAgentA, electionAgentB, electionAgentC}
	want := make([]string, 3)
	for offset := 0; offset < 3; offset++ {
		want[offset] = sorted[(rotation+offset)%3]
	}
	if !reflect.DeepEqual(result.Order, want) {
		t.Fatalf("order = %v, want rotation-by-hash %v", result.Order, want)
	}
	// A different event id can elect a different member of the tie band.
	winners := map[string]struct{}{}
	for _, digit := range []string{"1", "2", "3", "4"} {
		r := ComputeResponderElection(ResponderElectionInput{
			EventID: strings.Repeat(digit, 64), Text: text,
			SelfPubkey: electionAgentA, Candidates: tied,
		})
		winners[r.ElectedPubkey] = struct{}{}
	}
	if len(winners) < 2 {
		t.Fatalf("different events should spread load across the band, winners=%v", winners)
	}
}

func TestComputeResponderElection_FallsBackToFullSetWhenNobodyMatches(t *testing.T) {
	fleet := electionCandidates(
		[2]any{electionAgentA, []string{"deploy", "gateway"}},
		[2]any{electionAgentB, []string{"deploy"}},
		[2]any{electionAgentC, []string{"code-review"}},
	)
	result := ComputeResponderElection(ResponderElectionInput{
		EventID: electionEvent, Text: "does anyone know when standup is?",
		SelfPubkey: electionAgentB, Candidates: fleet,
	})
	if result.EligibleCount != 3 || len(result.Order) != 3 {
		t.Fatalf("no-match election must include everyone: %+v", result)
	}
}

func TestComputeResponderElection_RolesDownTheOrder(t *testing.T) {
	tied := electionCandidates(
		[2]any{electionAgentA, []string{"deploy"}},
		[2]any{electionAgentB, []string{"deploy"}},
		[2]any{electionAgentC, []string{"deploy"}},
	)
	roles := map[ResponderRole]int{}
	var any *ResponderElectionResult
	for _, self := range []string{electionAgentA, electionAgentB, electionAgentC} {
		r := ComputeResponderElection(ResponderElectionInput{
			EventID: electionEvent, Text: "please deploy this", SelfPubkey: self, Candidates: tied,
		})
		roles[r.Role]++
		any = r
	}
	if roles[ResponderRoleElected] != 1 || roles[ResponderRoleSuccessor] != 1 || roles[ResponderRoleDeferred] != 1 {
		t.Fatalf("roles = %v, want one of each", roles)
	}
	if any.SuccessorPubkey != any.Order[1] {
		t.Fatalf("successor = %s, want order[1] = %s", any.SuccessorPubkey, any.Order[1])
	}
}

func TestComputeResponderElection_DedupesAndHandlesEmpty(t *testing.T) {
	if r := ComputeResponderElection(ResponderElectionInput{
		EventID: electionEvent, Text: "hi", SelfPubkey: electionAgentA,
	}); r != nil {
		t.Fatalf("no candidates must return nil, got %+v", r)
	}
	dup := ComputeResponderElection(ResponderElectionInput{
		EventID: electionEvent, Text: "please deploy", SelfPubkey: electionAgentA,
		Candidates: electionCandidates(
			[2]any{electionAgentA, []string{"deploy"}},
			[2]any{strings.ToUpper(electionAgentA), []string{}},
		),
	})
	if want := []string{electionAgentA}; !reflect.DeepEqual(dup.Order, want) {
		t.Fatalf("case-duplicated candidate not deduped: %v", dup.Order)
	}
}

// ─── EvaluateNostrResponderElection (channel-layer bypass semantics) ─────────

func electionGatePass() *ShouldReplyGateDecision {
	return &ShouldReplyGateDecision{
		Outcome: ShouldReplyPass,
		Reason:  ShouldReplyReasonCapabilityMatch,
		Score:   5,
		Facts:   ShouldReplyGateFacts{Question: true, Request: true, CapabilityMatch: true},
	}
}

type electionEvalOverrides struct {
	policy    *NostrRoomPolicy
	gate      *ShouldReplyGateDecision
	gateSet   bool
	directory []ResponderDirectoryEntry
	dirSet    bool
	self      string
	selfCaps  []string
	sender    string
	isMuted   func(string) bool
}

func evalElection(o electionEvalOverrides) *NostrResponderElectionDecision {
	policy := ResolveNostrRoomPolicy(nil)
	if o.policy != nil {
		policy = *o.policy
	}
	gate := electionGatePass()
	if o.gateSet {
		gate = o.gate
	}
	directory := []ResponderDirectoryEntry{
		{Pubkey: electionAgentA, Capabilities: []string{"deploy"}},
		{Pubkey: electionAgentB, Capabilities: []string{"deploy"}},
	}
	if o.dirSet {
		directory = o.directory
	}
	self := electionAgentA
	if o.self != "" {
		self = o.self
	}
	selfCaps := []string{"deploy"}
	if o.selfCaps != nil {
		selfCaps = o.selfCaps
	}
	sender := electionHuman
	if o.sender != "" {
		sender = o.sender
	}
	return EvaluateNostrResponderElection(NostrResponderElectionParams{
		Policy:           policy,
		SelfPubkey:       self,
		SelfCapabilities: selfCaps,
		EventID:          electionEvent,
		Text:             "can someone deploy the gateway?",
		SenderPubkey:     sender,
		Gate:             gate,
		Directory:        directory,
		IsMuted:          o.isMuted,
	})
}

func TestEvaluateNostrResponderElection_Bypasses(t *testing.T) {
	off := ResolveNostrRoomPolicy(map[string]any{"responderElection": false})
	if evalElection(electionEvalOverrides{policy: &off}) != nil {
		t.Fatal("room off-switch must bypass the election")
	}
	if evalElection(electionEvalOverrides{dirSet: true}) != nil {
		t.Fatal("no fleet directory must bypass the election")
	}
	// Explicit mentions and commands never reach the ambient gate — the gate
	// decision is nil there and the message stays fully this agent's.
	if evalElection(electionEvalOverrides{gateSet: true}) != nil {
		t.Fatal("nil gate decision (mention/command traffic) must bypass")
	}
	drop := electionGatePass()
	drop.Outcome = ShouldReplyDrop
	drop.Reason = ShouldReplyReasonNotQuestionOrRequest
	if evalElection(electionEvalOverrides{gateSet: true, gate: drop}) != nil {
		t.Fatal("gate-dropped traffic must bypass (handled upstream)")
	}
	named := electionGatePass()
	named.Reason = ShouldReplyReasonDirectlyAddressed
	named.Facts.DirectlyAddressed = true
	if evalElection(electionEvalOverrides{gateSet: true, gate: named}) != nil {
		t.Fatal("name-addressed traffic must bypass")
	}
	// Solo agent: no other capability-advertising fleet member.
	if evalElection(electionEvalOverrides{dirSet: true, directory: []ResponderDirectoryEntry{
		{Pubkey: electionAgentA, Capabilities: []string{"deploy"}},
	}}) != nil {
		t.Fatal("solo advertiser must bypass")
	}
	if evalElection(electionEvalOverrides{dirSet: true, directory: []ResponderDirectoryEntry{
		{Pubkey: electionAgentA, Capabilities: []string{"deploy"}},
		{Pubkey: electionAgentB},
	}}) != nil {
		t.Fatal("unadvertised-only peers must bypass")
	}
}

func TestEvaluateNostrResponderElection_SameElectionOnEveryInstance(t *testing.T) {
	fromA := evalElection(electionEvalOverrides{self: electionAgentA})
	// Instance B carries different LOCAL capabilities, but both agents are
	// scored from the shared directory advertisement, so the election agrees.
	fromB := evalElection(electionEvalOverrides{self: electionAgentB, selfCaps: []string{"something-local"}})
	if fromA == nil || fromB == nil {
		t.Fatal("expected an applicable election")
	}
	if fromA.ElectedPubkey != fromB.ElectedPubkey || !reflect.DeepEqual(fromA.Order, fromB.Order) {
		t.Fatalf("instances disagree: %+v vs %+v", fromA, fromB)
	}
	roles := []ResponderRole{fromA.Role, fromB.Role}
	if !((roles[0] == ResponderRoleElected && roles[1] == ResponderRoleSuccessor) ||
		(roles[0] == ResponderRoleSuccessor && roles[1] == ResponderRoleElected)) {
		t.Fatalf("roles = %v, want elected + successor", roles)
	}
	if fromA.Takeover != DefaultResponderElectionTakeover {
		t.Fatalf("takeover = %v, want default %v", fromA.Takeover, DefaultResponderElectionTakeover)
	}
	custom := ResolveNostrRoomPolicy(map[string]any{"responderElectionTakeoverSeconds": 45})
	if got := evalElection(electionEvalOverrides{policy: &custom}); got.Takeover != 45*time.Second {
		t.Fatalf("takeover = %v, want 45s", got.Takeover)
	}
}

func TestEvaluateNostrResponderElection_ExcludesMutedAndAuthor(t *testing.T) {
	directory := []ResponderDirectoryEntry{
		{Pubkey: electionAgentA, Capabilities: []string{"deploy"}},
		{Pubkey: electionAgentB, Capabilities: []string{"deploy"}},
		{Pubkey: electionAgentC, Capabilities: []string{"deploy"}},
	}
	muted := func(pk string) bool { return pk == electionAgentC }
	result := evalElection(electionEvalOverrides{dirSet: true, directory: directory, isMuted: muted})
	for _, pk := range result.Order {
		if pk == electionAgentC {
			t.Fatalf("muted member took a turn: %v", result.Order)
		}
	}
	// The author of the message never competes to answer itself; B was the
	// only other unmuted advertiser, so the election is bypassed.
	if evalElection(electionEvalOverrides{dirSet: true, directory: directory, isMuted: muted, sender: electionAgentB}) != nil {
		t.Fatal("author-only fleet must bypass")
	}
}

// ─── Takeover coordinator ────────────────────────────────────────────────────

type fakeTakeoverTimer struct {
	fn      func()
	d       time.Duration
	cleared bool
}

type takeoverHarness struct {
	coordinator *TakeoverCoordinator[string]
	timers      []*fakeTakeoverTimer
	fired       []TakeoverPending[string]
	errors      []error
}

func newTakeoverHarness(t *testing.T, maxPending int, onTakeover func(TakeoverPending[string])) *takeoverHarness {
	t.Helper()
	h := &takeoverHarness{}
	if onTakeover == nil {
		onTakeover = func(p TakeoverPending[string]) { h.fired = append(h.fired, p) }
	}
	c, err := NewTakeoverCoordinator(TakeoverCoordinatorOptions[string]{
		SelfPubkey: electionAgentB,
		OnTakeover: onTakeover,
		OnError:    func(err error) { h.errors = append(h.errors, err) },
		MaxPending: maxPending,
		SetTimer: func(fn func(), d time.Duration) any {
			timer := &fakeTakeoverTimer{fn: fn, d: d}
			h.timers = append(h.timers, timer)
			return timer
		},
		ClearTimer: func(handle any) { handle.(*fakeTakeoverTimer).cleared = true },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.coordinator = c
	return h
}

func takeoverPendingFor(eventID string) TakeoverPending[string] {
	return TakeoverPending[string]{
		RoomKey:       electionRoom,
		EventID:       eventID,
		ElectedPubkey: electionAgentA,
		Payload:       "payload",
	}
}

func TestTakeoverCoordinator_FiresAfterTimeout(t *testing.T) {
	h := newTakeoverHarness(t, 0, nil)
	if !h.coordinator.Schedule(takeoverPendingFor(electionEvent), 120*time.Second) {
		t.Fatal("schedule failed")
	}
	if h.timers[0].d != 120*time.Second {
		t.Fatalf("timer window = %v", h.timers[0].d)
	}
	h.timers[0].fn()
	if len(h.fired) != 1 || h.fired[0].EventID != electionEvent || h.fired[0].ElectedPubkey != electionAgentA {
		t.Fatalf("takeover did not fire with the pending payload: %+v", h.fired)
	}
	if h.coordinator.Size() != 0 {
		t.Fatal("fired entry must be removed")
	}
	// Deduped while armed.
	h2 := newTakeoverHarness(t, 0, nil)
	if !h2.coordinator.Schedule(takeoverPendingFor(electionEvent), time.Second) {
		t.Fatal("first schedule failed")
	}
	if h2.coordinator.Schedule(takeoverPendingFor(electionEvent), time.Second) {
		t.Fatal("already-armed event must not re-arm")
	}
}

func TestTakeoverCoordinator_CancelsOnElectedOrThreadAnswer(t *testing.T) {
	h := newTakeoverHarness(t, 0, nil)
	h.coordinator.Schedule(takeoverPendingFor(electionEvent), time.Second)
	// The elected responder posts anything in the room (case-insensitive).
	h.coordinator.ObserveRoomMessage(TakeoverRoomMessageFacts{
		RoomKey: electionRoom, SenderPubkey: strings.ToUpper(electionAgentA),
	})
	if h.coordinator.Size() != 0 || !h.timers[0].cleared {
		t.Fatal("elected responder's message must cancel and clear the timer")
	}
	h.timers[0].fn()
	if len(h.fired) != 0 {
		t.Fatal("cancelled takeover must not fire")
	}

	// Anyone else answering in the contested event's thread cancels.
	h = newTakeoverHarness(t, 0, nil)
	h.coordinator.Schedule(takeoverPendingFor(electionEvent), time.Second)
	h.coordinator.ObserveRoomMessage(TakeoverRoomMessageFacts{
		RoomKey: electionRoom, SenderPubkey: electionAgentC, ReplyToEventID: electionEvent,
	})
	if h.coordinator.Size() != 0 {
		t.Fatal("a thread reply must cancel")
	}

	// Unrelated rooms/messages do not cancel.
	h = newTakeoverHarness(t, 0, nil)
	h.coordinator.Schedule(takeoverPendingFor(electionEvent), time.Second)
	h.coordinator.ObserveRoomMessage(TakeoverRoomMessageFacts{
		RoomKey: "nostr:room:other'room", SenderPubkey: electionAgentA,
	})
	h.coordinator.ObserveRoomMessage(TakeoverRoomMessageFacts{
		RoomKey: electionRoom, SenderPubkey: electionAgentC,
		ReplyToEventID: strings.Repeat("f", 64),
	})
	if h.coordinator.Size() != 1 {
		t.Fatal("unrelated traffic must not cancel")
	}
}

func TestTakeoverCoordinator_ReactionCancelsExceptOwn(t *testing.T) {
	h := newTakeoverHarness(t, 0, nil)
	h.coordinator.Schedule(takeoverPendingFor(electionEvent), time.Second)
	// Own reactions (self = AGENT_B) never stand a takeover down.
	h.coordinator.ObserveReaction(TakeoverReactionFacts{
		RoomKey: electionRoom, SenderPubkey: electionAgentB, TargetEventID: electionEvent,
	})
	if h.coordinator.Size() != 1 {
		t.Fatal("own reaction must not cancel")
	}
	h.coordinator.ObserveReaction(TakeoverReactionFacts{
		RoomKey: electionRoom, SenderPubkey: electionAgentC, TargetEventID: electionEvent,
	})
	if h.coordinator.Size() != 0 {
		t.Fatal("another agent's reaction must cancel")
	}
}

func TestTakeoverCoordinator_EvictsOldestPastMaxPending(t *testing.T) {
	h := newTakeoverHarness(t, 2, nil)
	h.coordinator.Schedule(takeoverPendingFor(strings.Repeat("1", 64)), time.Second)
	h.coordinator.Schedule(takeoverPendingFor(strings.Repeat("2", 64)), time.Second)
	h.coordinator.Schedule(takeoverPendingFor(strings.Repeat("3", 64)), time.Second)
	if h.coordinator.Size() != 2 {
		t.Fatalf("size = %d, want 2", h.coordinator.Size())
	}
	if !h.timers[0].cleared {
		t.Fatal("oldest timer must be cleared on eviction")
	}
	if h.coordinator.Cancel(electionRoom, strings.Repeat("1", 64)) {
		t.Fatal("evicted entry must be gone")
	}
	if !h.coordinator.Cancel(electionRoom, strings.Repeat("3", 64)) {
		t.Fatal("newest entry must remain")
	}
}

func TestTakeoverCoordinator_PanicRoutedToOnError(t *testing.T) {
	var h *takeoverHarness
	h = newTakeoverHarness(t, 0, func(TakeoverPending[string]) { panic("relay down") })
	h.coordinator.Schedule(takeoverPendingFor(electionEvent), time.Second)
	h.timers[0].fn()
	if len(h.errors) != 1 || !strings.Contains(h.errors[0].Error(), "relay down") {
		t.Fatalf("panic not routed to OnError: %v", h.errors)
	}
}

func TestTakeoverCoordinator_CloseClearsEverything(t *testing.T) {
	h := newTakeoverHarness(t, 0, nil)
	h.coordinator.Schedule(takeoverPendingFor(strings.Repeat("1", 64)), time.Second)
	h.coordinator.Schedule(takeoverPendingFor(strings.Repeat("2", 64)), time.Second)
	h.coordinator.Close()
	if h.coordinator.Size() != 0 {
		t.Fatal("close must drop all entries")
	}
	for _, timer := range h.timers {
		if !timer.cleared {
			t.Fatal("close must clear every timer")
		}
	}
	if h.coordinator.Schedule(takeoverPendingFor(strings.Repeat("3", 64)), time.Second) == false {
		// Scheduling after close is allowed (coordinator is just a registry);
		// nothing to assert beyond not panicking.
		t.Log("schedule after close returned false")
	}
}

func TestResolveNostrRoomPolicy_ResponderElectionKnobs(t *testing.T) {
	p := ResolveNostrRoomPolicy(nil)
	if !p.ResponderElection || p.ResponderElectionTakeover != DefaultResponderElectionTakeover {
		t.Fatalf("defaults = %+v, want enabled with 120s takeover", p)
	}
	if ResolveNostrRoomPolicy(map[string]any{"responderElection": false}).ResponderElection {
		t.Fatal("responderElection=false must switch the election off")
	}
	if got := ResolveNostrRoomPolicy(map[string]any{"responderElectionTakeoverSeconds": 30}).ResponderElectionTakeover; got != 30*time.Second {
		t.Fatalf("takeover = %v, want 30s", got)
	}
	// Out-of-range / mistyped values keep the default (min 5 seconds).
	if got := ResolveNostrRoomPolicy(map[string]any{"responderElectionTakeoverSeconds": 0}).ResponderElectionTakeover; got != DefaultResponderElectionTakeover {
		t.Fatalf("takeover(0) = %v, want default", got)
	}
	if got := ResolveNostrRoomPolicy(map[string]any{"responderElectionTakeoverSeconds": 4.9}).ResponderElectionTakeover; got != DefaultResponderElectionTakeover {
		t.Fatalf("takeover(4.9) = %v, want default", got)
	}
	if got := ResolveNostrRoomPolicy(map[string]any{"responderElectionTakeoverSeconds": "30"}).ResponderElectionTakeover; got != DefaultResponderElectionTakeover {
		t.Fatalf("takeover(\"30\") = %v, want default", got)
	}
}
