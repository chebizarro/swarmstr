package channels

// progress_ledger.go implements R5 — the Magentic-One-style Progress Ledger:
// a scheduled per-room moderator review turn (reference: openclaw-nostr
// ocn-d3u, pending). ONE designated agent per room (nostr_room_policy.go:
// progressLedgerModerator = true on the moderator instance, or a moderator
// pubkey every instance can compare against) periodically reviews the last N
// room events plus the current kind-30900 fleet-task state and answers the
// ledger questions deterministically:
//
//   1. Are there @mentions of fleet agents older than X with no response?
//   2. Are there open commitments with no backing task?
//   3. Is any task claimed by more than one agent?
//   4. Is a bot pair looping (pair-guard cooldown active)?
//
// Silence is the default: the moderator posts ONE compact summary message only
// when something is actionable, throttled to at most one post per configurable
// interval. Everything is explicit opt-in (progressLedger defaults off).

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/commitments"
	metricspkg "metiq/internal/metrics"
)

// R5 progress-ledger defaults and floors.
const (
	// DefaultProgressLedgerInterval is the review cadence when the room does
	// not configure progressLedgerIntervalSeconds.
	DefaultProgressLedgerInterval = 15 * time.Minute
	// MinProgressLedgerIntervalSeconds floors the review/post intervals.
	MinProgressLedgerIntervalSeconds = 60
	// DefaultProgressLedgerStaleMentionAge is how old an unanswered agent
	// @mention must be before it is actionable.
	DefaultProgressLedgerStaleMentionAge = 10 * time.Minute
	// MinProgressLedgerStaleMentionSeconds floors the staleness knob.
	MinProgressLedgerStaleMentionSeconds = 60
	// DefaultProgressLedgerMaxEvents bounds the per-room review window
	// ("the last N room events").
	DefaultProgressLedgerMaxEvents = 50
	// progressLedgerTitleCoverage is the token-coverage bar above which a
	// commitment's text counts as restating an open task's title (backed).
	progressLedgerTitleCoverage = 0.6
	// progressLedgerMaxListedIDs caps IDs listed inline in the summary.
	progressLedgerMaxListedIDs = 3
)

// ProgressLedgerEvent is one observed room chat event (typed facts only).
type ProgressLedgerEvent struct {
	EventID          string
	Author           string
	CreatedAt        time.Time
	Content          string
	MentionedPubkeys []string
	ReplyToEventID   string
}

// ProgressLedgerTask is the current head state of one kind-30900 fleet task.
// Claimants carries every agent currently claiming/assigned the task; entries
// for the same TaskID are merged (claimant union) before evaluation.
type ProgressLedgerTask struct {
	TaskID    string
	Status    string
	Title     string
	Claimants []string
}

// ProgressLedgerCommitment is one OPEN commitment from the commitments Store.
// TaskID, when known, is checked against the current task state; otherwise the
// commitment text is matched against task IDs and titles deterministically.
type ProgressLedgerCommitment struct {
	ID     string
	Text   string
	TaskID string
}

// ProgressLedgerStaleMention is an agent @mention older than the staleness
// threshold with no later room event authored by the mentioned agent.
type ProgressLedgerStaleMention struct {
	EventID   string
	Mentioned string
	Age       time.Duration
}

// ProgressLedgerLoopSignal is a bot pair currently in loop-guard cooldown.
type ProgressLedgerLoopSignal struct {
	PairKey     string
	RecentCount int
}

// ProgressLedgerFindings are the deterministic answers to the ledger questions.
type ProgressLedgerFindings struct {
	StaleMentions       []ProgressLedgerStaleMention
	UnbackedCommitments []string // commitment IDs
	DuplicateClaims     []string // task IDs claimed by >1 distinct agent
	LoopSignals         []ProgressLedgerLoopSignal
}

// Actionable reports whether anything warrants a summary post. False keeps
// the moderator silent (the default).
func (f ProgressLedgerFindings) Actionable() bool {
	return len(f.StaleMentions) > 0 || len(f.UnbackedCommitments) > 0 ||
		len(f.DuplicateClaims) > 0 || len(f.LoopSignals) > 0
}

// ProgressLedgerInput carries the typed facts one review turn evaluates.
type ProgressLedgerInput struct {
	// Now anchors staleness/cooldown math (zero = time.Now().UTC()).
	Now time.Time
	// Events is the room's recent chat window (the last N events, any order).
	Events []ProgressLedgerEvent
	// AgentPubkeys optionally restricts which mentioned pubkeys are checked
	// for staleness (the fleet roster). Empty = every mentioned pubkey.
	AgentPubkeys []string
	// Tasks is the CURRENT open kind-30900 fleet-task state.
	Tasks []ProgressLedgerTask
	// Commitments are the OPEN commitments to check for task backing.
	Commitments []ProgressLedgerCommitment
	// PairGuard is the loop-guard snapshot (PairLoopGuard.Snapshot()).
	PairGuard []PairLoopGuardSnapshotEntry
	// StaleMentionAge overrides the staleness threshold (<=0 = default).
	StaleMentionAge time.Duration
}

// EvaluateProgressLedger answers the Progress Ledger questions from typed
// facts only. Tier 1 (v1) is fully deterministic — no model call. A future
// LLM-review tier hooks in HERE: after the deterministic findings are
// computed, an opt-in model pass could triage/expand them (e.g. judge whether
// an unanswered mention was implicitly resolved), but the posting contract —
// one compact message, silence by default — must not change.
func EvaluateProgressLedger(in ProgressLedgerInput) ProgressLedgerFindings {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	staleAge := in.StaleMentionAge
	if staleAge <= 0 {
		staleAge = DefaultProgressLedgerStaleMentionAge
	}

	var f ProgressLedgerFindings
	tasks := mergeProgressLedgerTasks(in.Tasks)
	f.StaleMentions = staleMentionFindings(in.Events, in.AgentPubkeys, now, staleAge)
	f.UnbackedCommitments = unbackedCommitmentFindings(in.Commitments, tasks)
	f.DuplicateClaims = duplicateClaimFindings(tasks)
	f.LoopSignals = loopSignalFindings(in.PairGuard, now)
	return f
}

// staleMentionFindings: a mention of pubkey P at time T is unanswered when the
// event is older than staleAge and NO later room event is authored by P (any
// later message from P counts as engagement — deliberately conservative so the
// deterministic tier stays low-false-positive).
func staleMentionFindings(events []ProgressLedgerEvent, roster []string, now time.Time, staleAge time.Duration) []ProgressLedgerStaleMention {
	agentSet := map[string]struct{}{}
	for _, pk := range roster {
		if k := normalizeResponderKey(pk); k != "" {
			agentSet[k] = struct{}{}
		}
	}
	var out []ProgressLedgerStaleMention
	for _, ev := range events {
		if ev.CreatedAt.IsZero() || now.Sub(ev.CreatedAt) < staleAge {
			continue
		}
		author := normalizeResponderKey(ev.Author)
		seen := map[string]struct{}{}
		for _, raw := range ev.MentionedPubkeys {
			p := normalizeResponderKey(raw)
			if p == "" || p == author {
				continue
			}
			if len(agentSet) > 0 {
				if _, ok := agentSet[p]; !ok {
					continue
				}
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			answered := false
			for _, later := range events {
				if normalizeResponderKey(later.Author) == p && later.CreatedAt.After(ev.CreatedAt) {
					answered = true
					break
				}
			}
			if !answered {
				out = append(out, ProgressLedgerStaleMention{EventID: ev.EventID, Mentioned: p, Age: now.Sub(ev.CreatedAt)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EventID != out[j].EventID {
			return out[i].EventID < out[j].EventID
		}
		return out[i].Mentioned < out[j].Mentioned
	})
	return out
}

// mergeProgressLedgerTasks merges entries by TaskID with a claimant union, so
// two claim events for one task surface as one multi-claimant task.
func mergeProgressLedgerTasks(tasks []ProgressLedgerTask) []ProgressLedgerTask {
	byID := map[string]*ProgressLedgerTask{}
	order := []string{}
	for _, t := range tasks {
		id := strings.TrimSpace(t.TaskID)
		if id == "" {
			continue
		}
		existing, ok := byID[id]
		if !ok {
			copied := t
			copied.TaskID = id
			copied.Claimants = append([]string(nil), t.Claimants...)
			byID[id] = &copied
			order = append(order, id)
			continue
		}
		existing.Claimants = append(existing.Claimants, t.Claimants...)
		if existing.Title == "" {
			existing.Title = t.Title
		}
		if t.Status != "" {
			existing.Status = t.Status
		}
	}
	out := make([]ProgressLedgerTask, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// unbackedCommitmentFindings: a commitment is backed when its TaskID matches a
// current task, its text names a current task's ID, or its text covers a
// current task's title tokens past the coverage bar.
func unbackedCommitmentFindings(open []ProgressLedgerCommitment, tasks []ProgressLedgerTask) []string {
	var out []string
	for _, c := range open {
		if strings.TrimSpace(c.ID) == "" {
			continue
		}
		if !commitmentBackedByTask(c, tasks) {
			out = append(out, c.ID)
		}
	}
	sort.Strings(out)
	return out
}

func commitmentBackedByTask(c ProgressLedgerCommitment, tasks []ProgressLedgerTask) bool {
	wantID := strings.ToLower(strings.TrimSpace(c.TaskID))
	lowerText := strings.ToLower(c.Text)
	textTokens := echoTokenize(NormalizeEchoText(c.Text))
	for _, t := range tasks {
		id := strings.ToLower(t.TaskID)
		if wantID != "" && wantID == id {
			return true
		}
		if id != "" && strings.Contains(lowerText, id) {
			return true
		}
		titleTokens := echoTokenize(NormalizeEchoText(t.Title))
		if len(titleTokens) > 0 && taskCoverage(textTokens, titleTokens) >= progressLedgerTitleCoverage {
			return true
		}
	}
	return false
}

// progressLedgerTerminalStatuses are task states where a historical duplicate
// claim is no longer actionable.
var progressLedgerTerminalStatuses = map[string]struct{}{
	"completed": {}, "done": {}, "failed": {}, "cancelled": {}, "canceled": {},
}

func duplicateClaimFindings(tasks []ProgressLedgerTask) []string {
	var out []string
	for _, t := range tasks {
		if _, terminal := progressLedgerTerminalStatuses[strings.ToLower(strings.TrimSpace(t.Status))]; terminal {
			continue
		}
		distinct := map[string]struct{}{}
		for _, c := range t.Claimants {
			if k := normalizeResponderKey(c); k != "" {
				distinct[k] = struct{}{}
			}
		}
		if len(distinct) > 1 {
			out = append(out, t.TaskID)
		}
	}
	sort.Strings(out)
	return out
}

// loopSignalFindings: a pair with an active cooldown means the pair guard
// tripped (looping); RecentCount is informational.
func loopSignalFindings(snapshot []PairLoopGuardSnapshotEntry, now time.Time) []ProgressLedgerLoopSignal {
	var out []ProgressLedgerLoopSignal
	for _, e := range snapshot {
		if e.CooldownUntil.After(now) {
			out = append(out, ProgressLedgerLoopSignal{PairKey: e.Key, RecentCount: e.RecentCount})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PairKey < out[j].PairKey })
	return out
}

// Summary formats the ONE compact room message for actionable findings.
// Empty when nothing is actionable (silence is the default).
func (f ProgressLedgerFindings) Summary() string {
	var segments []string
	if n := len(f.StaleMentions); n > 0 {
		oldest := time.Duration(0)
		for _, m := range f.StaleMentions {
			if m.Age > oldest {
				oldest = m.Age
			}
		}
		segments = append(segments, fmt.Sprintf("%d unanswered mention(s), oldest %s", n, formatProgressLedgerAge(oldest)))
	}
	if n := len(f.UnbackedCommitments); n > 0 {
		segments = append(segments, fmt.Sprintf("%d open commitment(s) with no backing task", n))
	}
	if len(f.DuplicateClaims) > 0 {
		segments = append(segments, "duplicate claims on "+joinCappedIDs(f.DuplicateClaims))
	}
	if n := len(f.LoopSignals); n > 0 {
		segments = append(segments, fmt.Sprintf("%d bot pair(s) in loop cooldown", n))
	}
	if len(segments) == 0 {
		return ""
	}
	return "📋 Progress ledger: " + strings.Join(segments, "; ") + "."
}

func formatProgressLedgerAge(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	mins := int(d / time.Minute)
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dh%02dm", mins/60, mins%60)
}

func joinCappedIDs(ids []string) string {
	if len(ids) <= progressLedgerMaxListedIDs {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:progressLedgerMaxListedIDs], ", ") +
		fmt.Sprintf(" +%d more", len(ids)-progressLedgerMaxListedIDs)
}

// ProgressLedgerCommitmentsFromStore maps a session's OPEN (pending)
// commitments from the commitments Store into review inputs.
func ProgressLedgerCommitmentsFromStore(store *commitments.Store, sessionID string) []ProgressLedgerCommitment {
	if store == nil {
		return nil
	}
	list := store.List(sessionID, commitments.StatusPending)
	out := make([]ProgressLedgerCommitment, 0, len(list))
	for _, c := range list {
		out = append(out, ProgressLedgerCommitment{ID: c.ID, Text: c.Text})
	}
	return out
}

// ─── Recorder ────────────────────────────────────────────────────────────────

// ProgressLedgerRecorder keeps a bounded per-room ring of recent chat events
// (the review's "last N room events"). Redelivered events (same EventID) are
// recorded once. Safe for concurrent use.
type ProgressLedgerRecorder struct {
	mu        sync.Mutex
	maxEvents int
	rooms     map[string][]ProgressLedgerEvent
}

// NewProgressLedgerRecorder creates a recorder; maxEvents <= 0 uses the
// default window (DefaultProgressLedgerMaxEvents).
func NewProgressLedgerRecorder(maxEvents int) *ProgressLedgerRecorder {
	if maxEvents <= 0 {
		maxEvents = DefaultProgressLedgerMaxEvents
	}
	return &ProgressLedgerRecorder{maxEvents: maxEvents, rooms: map[string][]ProgressLedgerEvent{}}
}

// Record appends one room event, dropping the oldest past the window.
func (r *ProgressLedgerRecorder) Record(roomKey string, ev ProgressLedgerEvent) {
	if r == nil || roomKey == "" || ev.EventID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ring := r.rooms[roomKey]
	for _, existing := range ring {
		if existing.EventID == ev.EventID {
			return // redelivery: count once
		}
	}
	ring = append(ring, ev)
	if len(ring) > r.maxEvents {
		ring = append([]ProgressLedgerEvent(nil), ring[len(ring)-r.maxEvents:]...)
	}
	r.rooms[roomKey] = ring
}

// Events returns a copy of the room's recorded window.
func (r *ProgressLedgerRecorder) Events(roomKey string) []ProgressLedgerEvent {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ProgressLedgerEvent(nil), r.rooms[roomKey]...)
}

// ─── Scheduler ───────────────────────────────────────────────────────────────

// ProgressLedgerScheduler drives the periodic moderator review, following the
// commitments HeartbeatScheduler pattern: the caller ticks it (any cadence at
// or under the room interval); the scheduler enforces the per-room review
// interval and the at-most-one-post-per-interval throttle. Safe for
// concurrent use across rooms.
type ProgressLedgerScheduler struct {
	// Now is an injectable clock for tests (nil = time.Now().UTC()).
	Now func() time.Time

	mu    sync.Mutex
	rooms map[string]*progressLedgerRoomState
}

type progressLedgerRoomState struct {
	lastRun  time.Time
	lastPost time.Time
}

// Stable skip reasons reported by Run.
const (
	ProgressLedgerSkipDisabled     = "disabled"
	ProgressLedgerSkipNotModerator = "not_moderator"
	ProgressLedgerSkipInterval     = "interval"
)

// ProgressLedgerRunParams parameterizes one scheduler tick for one room.
type ProgressLedgerRunParams struct {
	RoomKey    string
	Policy     NostrRoomPolicy
	SelfPubkey string
	// Collect gathers the review input (recent events, task state, open
	// commitments, pair-guard snapshot). Called only when a review is due.
	Collect func() ProgressLedgerInput
	// Post publishes the compact summary to the room. Called at most once per
	// post interval, and only for actionable findings.
	Post func(summary string) error
}

// ProgressLedgerRunResult reports what one tick did.
type ProgressLedgerRunResult struct {
	Ran       bool
	Skipped   string
	Findings  ProgressLedgerFindings
	Summary   string
	Posted    bool
	Throttled bool
	PostErr   error
}

// Run executes one scheduler tick: due-check → collect → deterministic review
// → throttled post (metrics: metiq_progress_ledger_runs_total /
// metiq_progress_ledger_posts_total).
func (s *ProgressLedgerScheduler) Run(p ProgressLedgerRunParams) ProgressLedgerRunResult {
	if !p.Policy.ProgressLedger {
		return ProgressLedgerRunResult{Skipped: ProgressLedgerSkipDisabled}
	}
	if !p.Policy.ProgressLedgerModeratorFor(p.SelfPubkey) {
		return ProgressLedgerRunResult{Skipped: ProgressLedgerSkipNotModerator}
	}
	interval := p.Policy.ProgressLedgerInterval
	if interval <= 0 {
		interval = DefaultProgressLedgerInterval
	}
	postInterval := p.Policy.ProgressLedgerPostInterval
	if postInterval <= 0 {
		postInterval = interval
	}
	now := s.now()

	s.mu.Lock()
	if s.rooms == nil {
		s.rooms = map[string]*progressLedgerRoomState{}
	}
	state, ok := s.rooms[p.RoomKey]
	if !ok {
		state = &progressLedgerRoomState{}
		s.rooms[p.RoomKey] = state
	}
	if !state.lastRun.IsZero() && now.Before(state.lastRun.Add(interval)) {
		s.mu.Unlock()
		return ProgressLedgerRunResult{Skipped: ProgressLedgerSkipInterval}
	}
	state.lastRun = now
	s.mu.Unlock()

	metricspkg.RecordProgressLedger("run")
	var input ProgressLedgerInput
	if p.Collect != nil {
		input = p.Collect()
	}
	if input.Now.IsZero() {
		input.Now = now
	}
	if input.StaleMentionAge <= 0 {
		input.StaleMentionAge = p.Policy.ProgressLedgerStaleMentionAge
	}
	result := ProgressLedgerRunResult{Ran: true, Findings: EvaluateProgressLedger(input)}
	if !result.Findings.Actionable() {
		return result // silence is the default
	}
	result.Summary = result.Findings.Summary()

	s.mu.Lock()
	throttled := !state.lastPost.IsZero() && now.Before(state.lastPost.Add(postInterval))
	s.mu.Unlock()
	if throttled {
		result.Throttled = true
		return result
	}
	if p.Post == nil {
		return result
	}
	if err := p.Post(result.Summary); err != nil {
		result.PostErr = err
		return result
	}
	result.Posted = true
	metricspkg.RecordProgressLedger("post")
	s.mu.Lock()
	state.lastPost = now
	s.mu.Unlock()
	return result
}

func (s *ProgressLedgerScheduler) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
