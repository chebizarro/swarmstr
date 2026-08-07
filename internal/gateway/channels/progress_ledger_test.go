package channels

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var ledgerBase = time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

func ledgerEvent(id, author string, at time.Time, mentions ...string) ProgressLedgerEvent {
	return ProgressLedgerEvent{EventID: id, Author: author, CreatedAt: at, MentionedPubkeys: mentions}
}

func TestResolveNostrRoomPolicy_ProgressLedgerDefaultsOff(t *testing.T) {
	p := ResolveNostrRoomPolicy(nil)
	if p.ProgressLedger {
		t.Fatal("progress ledger must default off (explicit opt-in)")
	}
	if p.ProgressLedgerInterval != DefaultProgressLedgerInterval ||
		p.ProgressLedgerPostInterval != DefaultProgressLedgerInterval ||
		p.ProgressLedgerStaleMentionAge != DefaultProgressLedgerStaleMentionAge {
		t.Fatalf("interval knobs must carry defaults, got %+v", p)
	}
	if p.ProgressLedgerModeratorFor("anyone") {
		t.Fatal("disabled ledger must never designate a moderator")
	}
}

func TestResolveNostrRoomPolicy_ProgressLedgerKnobs(t *testing.T) {
	p := ResolveNostrRoomPolicy(map[string]any{
		"progressLedger":                    true,
		"progressLedgerIntervalSeconds":     300,
		"progressLedgerPostIntervalSeconds": 900,
		"progressLedgerStaleMentionSeconds": 120,
		"progressLedgerModerator":           "ABCDEF",
	})
	if !p.ProgressLedger || p.ProgressLedgerInterval != 5*time.Minute ||
		p.ProgressLedgerPostInterval != 15*time.Minute || p.ProgressLedgerStaleMentionAge != 2*time.Minute {
		t.Fatalf("knobs not resolved: %+v", p)
	}
	if !p.ProgressLedgerModeratorFor("abcdef") || p.ProgressLedgerModeratorFor("other") {
		t.Fatal("moderator pubkey must match case-insensitively and exclude others")
	}

	// Sub-floor and junk values keep defaults; post interval follows the
	// review interval when unset.
	p = ResolveNostrRoomPolicy(map[string]any{
		"progressLedger":                    true,
		"progressLedgerIntervalSeconds":     5,
		"progressLedgerStaleMentionSeconds": "soon",
	})
	if p.ProgressLedgerInterval != DefaultProgressLedgerInterval ||
		p.ProgressLedgerPostInterval != DefaultProgressLedgerInterval ||
		p.ProgressLedgerStaleMentionAge != DefaultProgressLedgerStaleMentionAge {
		t.Fatalf("out-of-range knobs must keep defaults: %+v", p)
	}

	// Boolean moderator form: the moderator's own instance config says true.
	p = ResolveNostrRoomPolicy(map[string]any{"progressLedger": true, "progressLedgerModerator": true})
	if !p.ProgressLedgerModeratorFor("me") {
		t.Fatal("progressLedgerModerator=true must designate this instance")
	}
	// Enabled but undesignated: nobody moderates (a shared room config must
	// not turn every fleet member into a ledger poster).
	p = ResolveNostrRoomPolicy(map[string]any{"progressLedger": true})
	if p.ProgressLedgerModeratorFor("me") {
		t.Fatal("enabled-but-undesignated room must have no moderator")
	}
}

func TestEvaluateProgressLedger_StaleMentions(t *testing.T) {
	agent := hexKey()
	human := hexKey()
	other := hexKey()
	now := ledgerBase.Add(30 * time.Minute)
	in := ProgressLedgerInput{
		Now:          now,
		AgentPubkeys: []string{agent, other},
		Events: []ProgressLedgerEvent{
			// Unanswered stale mention of agent.
			ledgerEvent("evt-old", human, ledgerBase, agent),
			// Answered: other spoke after being mentioned.
			ledgerEvent("evt-answered", human, ledgerBase, other),
			ledgerEvent("evt-reply", other, ledgerBase.Add(2*time.Minute)),
			// Self-mention never counts.
			ledgerEvent("evt-self", agent, ledgerBase, agent),
			// Fresh mention is not yet actionable.
			ledgerEvent("evt-fresh", human, now.Add(-time.Minute), agent),
			// Mention outside the roster is ignored.
			ledgerEvent("evt-outside", human, ledgerBase, human+"ff"),
		},
	}
	f := EvaluateProgressLedger(in)
	if len(f.StaleMentions) != 1 {
		t.Fatalf("want exactly one stale mention, got %+v", f.StaleMentions)
	}
	m := f.StaleMentions[0]
	if m.EventID != "evt-old" || m.Mentioned != strings.ToLower(agent) || m.Age != 30*time.Minute {
		t.Fatalf("wrong stale mention: %+v", m)
	}
	if !f.Actionable() {
		t.Fatal("stale mention must be actionable")
	}
}

func TestEvaluateProgressLedger_UnbackedCommitments(t *testing.T) {
	tasks := []ProgressLedgerTask{
		{TaskID: "fleet-042", Status: "in_progress", Title: "Rotate the relay credentials"},
	}
	f := EvaluateProgressLedger(ProgressLedgerInput{
		Now:   ledgerBase,
		Tasks: tasks,
		Commitments: []ProgressLedgerCommitment{
			{ID: "c-linked", Text: "I'll do it", TaskID: "fleet-042"},
			{ID: "c-by-id", Text: "working on fleet-042 now"},
			{ID: "c-by-title", Text: "I will rotate the relay credentials tonight"},
			{ID: "c-unbacked", Text: "I'll migrate the database next week"},
		},
	})
	if len(f.UnbackedCommitments) != 1 || f.UnbackedCommitments[0] != "c-unbacked" {
		t.Fatalf("want only c-unbacked flagged, got %v", f.UnbackedCommitments)
	}
	// No tasks at all: every open commitment is unbacked.
	f = EvaluateProgressLedger(ProgressLedgerInput{
		Now:         ledgerBase,
		Commitments: []ProgressLedgerCommitment{{ID: "c1", Text: "I'll handle it"}},
	})
	if len(f.UnbackedCommitments) != 1 {
		t.Fatalf("commitment with no task state must be unbacked, got %v", f.UnbackedCommitments)
	}
}

func TestEvaluateProgressLedger_DuplicateClaims(t *testing.T) {
	a, b := hexKey(), hexKey()
	f := EvaluateProgressLedger(ProgressLedgerInput{
		Now: ledgerBase,
		Tasks: []ProgressLedgerTask{
			// Two claim heads for one task merge into a multi-claimant task.
			{TaskID: "fleet-007", Status: "in_progress", Claimants: []string{a}},
			{TaskID: "fleet-007", Status: "in_progress", Claimants: []string{b}},
			// Same claimant twice is not a duplicate.
			{TaskID: "fleet-008", Status: "in_progress", Claimants: []string{a, strings.ToUpper(a)}},
			// Terminal tasks are not actionable.
			{TaskID: "fleet-009", Status: "completed", Claimants: []string{a, b}},
		},
	})
	if len(f.DuplicateClaims) != 1 || f.DuplicateClaims[0] != "fleet-007" {
		t.Fatalf("want only fleet-007 flagged, got %v", f.DuplicateClaims)
	}
}

func TestEvaluateProgressLedger_LoopSignals(t *testing.T) {
	f := EvaluateProgressLedger(ProgressLedgerInput{
		Now: ledgerBase,
		PairGuard: []PairLoopGuardSnapshotEntry{
			{Key: "acct|room|botA|botB", RecentCount: 21, CooldownUntil: ledgerBase.Add(5 * time.Minute)},
			{Key: "acct|room|botC|botD", RecentCount: 3, CooldownUntil: ledgerBase.Add(-time.Minute)},
		},
	})
	if len(f.LoopSignals) != 1 || f.LoopSignals[0].PairKey != "acct|room|botA|botB" {
		t.Fatalf("only the pair in active cooldown is a loop signal, got %+v", f.LoopSignals)
	}
}

func TestProgressLedgerFindings_SummaryAndSilence(t *testing.T) {
	// Silence is the default: nothing actionable -> empty summary.
	var none ProgressLedgerFindings
	if none.Actionable() || none.Summary() != "" {
		t.Fatal("no findings must stay silent")
	}

	f := ProgressLedgerFindings{
		StaleMentions:       []ProgressLedgerStaleMention{{EventID: "e", Mentioned: "p", Age: 95 * time.Minute}},
		UnbackedCommitments: []string{"c1", "c2"},
		DuplicateClaims:     []string{"t1", "t2", "t3", "t4", "t5"},
		LoopSignals:         []ProgressLedgerLoopSignal{{PairKey: "k", RecentCount: 21}},
	}
	got := f.Summary()
	for _, want := range []string{
		"📋 Progress ledger: ",
		"1 unanswered mention(s), oldest 1h35m",
		"2 open commitment(s) with no backing task",
		"duplicate claims on t1, t2, t3 +2 more",
		"1 bot pair(s) in loop cooldown",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n") != 0 {
		t.Fatalf("summary must be one compact line: %q", got)
	}
}

func TestProgressLedgerRecorder_WindowAndRedelivery(t *testing.T) {
	r := NewProgressLedgerRecorder(3)
	for i, id := range []string{"e1", "e2", "e1", "e3", "e4"} { // e1 redelivered
		r.Record("room", ledgerEvent(id, "author", ledgerBase.Add(time.Duration(i)*time.Minute)))
	}
	events := r.Events("room")
	if len(events) != 3 || events[0].EventID != "e2" || events[2].EventID != "e4" {
		t.Fatalf("want window [e2 e3 e4], got %+v", events)
	}
	if r.Events("other") != nil {
		t.Fatal("unknown room must be empty")
	}
}

func TestProgressLedgerScheduler_RunGatesAndThrottle(t *testing.T) {
	self := hexKey()
	now := ledgerBase
	s := &ProgressLedgerScheduler{Now: func() time.Time { return now }}
	agent := hexKey()
	actionable := func() ProgressLedgerInput {
		return ProgressLedgerInput{
			AgentPubkeys: []string{agent},
			Events:       []ProgressLedgerEvent{ledgerEvent("evt", hexKey(), ledgerBase.Add(-time.Hour), agent)},
		}
	}
	var posts []string
	post := func(text string) error { posts = append(posts, text); return nil }
	policy := ResolveNostrRoomPolicy(map[string]any{
		"progressLedger":                    true,
		"progressLedgerModerator":           self,
		"progressLedgerIntervalSeconds":     300,
		"progressLedgerPostIntervalSeconds": 900,
	})
	params := func(p NostrRoomPolicy) ProgressLedgerRunParams {
		return ProgressLedgerRunParams{RoomKey: "room", Policy: p, SelfPubkey: self, Collect: actionable, Post: post}
	}

	// Disabled room: never runs.
	if r := s.Run(params(ResolveNostrRoomPolicy(nil))); r.Ran || r.Skipped != ProgressLedgerSkipDisabled {
		t.Fatalf("disabled room must skip, got %+v", r)
	}
	// Not the moderator: never runs.
	notMe := params(policy)
	notMe.SelfPubkey = hexKey()
	if r := s.Run(notMe); r.Ran || r.Skipped != ProgressLedgerSkipNotModerator {
		t.Fatalf("non-moderator must skip, got %+v", r)
	}

	// First due run posts ONE compact summary.
	r := s.Run(params(policy))
	if !r.Ran || !r.Posted || len(posts) != 1 || !strings.HasPrefix(posts[0], "📋 Progress ledger: ") {
		t.Fatalf("first actionable run must post once, got %+v posts=%v", r, posts)
	}

	// Within the review interval: no review at all.
	now = now.Add(2 * time.Minute)
	if r := s.Run(params(policy)); r.Ran || r.Skipped != ProgressLedgerSkipInterval {
		t.Fatalf("within interval must skip, got %+v", r)
	}

	// Next review is due but the post throttle (15m) still holds: the review
	// runs, findings remain actionable, nothing is posted.
	now = now.Add(4 * time.Minute)
	r = s.Run(params(policy))
	if !r.Ran || r.Posted || !r.Throttled || len(posts) != 1 {
		t.Fatalf("post throttle must withhold the summary, got %+v posts=%v", r, posts)
	}

	// Past the post throttle: posts again.
	now = now.Add(10 * time.Minute)
	if r := s.Run(params(policy)); !r.Posted || len(posts) != 2 {
		t.Fatalf("post past throttle expected, got %+v posts=%v", r, posts)
	}

	// Nothing actionable: silence (no post, no throttle bookkeeping).
	now = now.Add(time.Hour)
	quiet := params(policy)
	quiet.Collect = func() ProgressLedgerInput { return ProgressLedgerInput{} }
	if r := s.Run(quiet); !r.Ran || r.Posted || r.Summary != "" || len(posts) != 2 {
		t.Fatalf("quiet run must stay silent, got %+v posts=%v", r, posts)
	}

	// A failed post does not consume the throttle: the next due run retries.
	now = now.Add(time.Hour)
	failing := params(policy)
	failing.Post = func(string) error { return errors.New("relay down") }
	if r := s.Run(failing); r.Posted || r.PostErr == nil {
		t.Fatalf("failed post must surface the error, got %+v", r)
	}
	now = now.Add(6 * time.Minute)
	if r := s.Run(params(policy)); !r.Posted || len(posts) != 3 {
		t.Fatalf("retry after failed post expected, got %+v posts=%v", r, posts)
	}
}
