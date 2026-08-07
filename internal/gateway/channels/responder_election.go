// Package channels — responder_election.go ports openclaw-nostr
// responder-election.ts (ocn-myl, group-chat discipline report R2) into Go.
//
// Problem: several fleet agents each correctly decide (via the R1 should-reply
// gate) that an ambient room message deserves an answer — and the room gets N
// redundant replies. Fix: every agent computes the SAME election locally, with
// no coordinator, from shared inputs (the event id, the message text, and the
// NIP-51 fleet directory's capability advertisements):
//
//  1. Rank capability-matched members by match score.
//  2. Rotate equal-score bands by hash(event_id) so ties break identically on
//     every instance while different events spread load across the band.
//  3. order[0] answers; order[1] arms a takeover timer and claims the event
//     (visible reaction) only if the elected responder stays silent past the
//     window; everyone else stays silent for that event.
//
// This module is pure (election) plus a small timer registry (takeover). It is
// loop/noise control ONLY — it never gates authorization, DMs, mentions, or
// commands; the gateway layer enforces that scoping.
package channels

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// NostrResponderClaimEmoji is the claim reaction a takeover successor posts
// before answering (R2).
const NostrResponderClaimEmoji = "🙋"

// ResponderCandidate is one fleet member competing in an election.
type ResponderCandidate struct {
	Pubkey string
	// Capabilities are the declared capability names, methods, tools, models,
	// and scope terms.
	Capabilities []string
}

// ResponderElectionInput is the pure input to ComputeResponderElection.
type ResponderElectionInput struct {
	// EventID is the Nostr event id of the contested message (shared by all
	// instances).
	EventID string
	// Text is the message text (shared by all instances).
	Text string
	// SelfPubkey is this agent's pubkey (hex).
	SelfPubkey string
	// Candidates are all fleet candidates, including self.
	Candidates []ResponderCandidate
}

// ResponderRole is this agent's position in the responder order.
type ResponderRole string

const (
	ResponderRoleElected      ResponderRole = "elected"
	ResponderRoleSuccessor    ResponderRole = "successor"
	ResponderRoleDeferred     ResponderRole = "deferred"
	ResponderRoleNotCandidate ResponderRole = "not_candidate"
)

// ResponderElectionResult is the deterministic election outcome.
type ResponderElectionResult struct {
	Role ResponderRole
	// Order is the full deterministic responder order over the eligible set.
	Order         []string
	ElectedPubkey string
	// SuccessorPubkey is order[1], or "" when the eligible set has one member.
	SuccessorPubkey string
	// SelfIndex is the index of self in Order, -1 when self is not eligible.
	SelfIndex     int
	EligibleCount int
	// Scores is the capability-match score per candidate (all candidates, not
	// just eligible).
	Scores map[string]int
}

func normalizeResponderKey(pubkey string) string {
	return strings.ToLower(strings.TrimSpace(pubkey))
}

// ScoreResponderCapabilityMatch is the deterministic capability-match score,
// mirroring the should-reply gate's matching semantics but COUNTING distinct
// hits so multi-capable agents can be ranked: +2 per distinct matched whole
// phrase, +1 per distinct matched token. Same inputs → same score on every
// instance.
func ScoreResponderCapabilityMatch(text string, capabilities []string) int {
	normalizedText := " " + normalizeShouldReplyPhrase(text) + " "
	matchedPhrases := map[string]struct{}{}
	matchedTokens := map[string]struct{}{}
	for _, capability := range capabilities {
		phrase := normalizeShouldReplyPhrase(capability)
		if phrase == "" {
			continue
		}
		// Unlike the boolean gate, a generic single stop word ("main", "tool")
		// must not confer election weight — it would skew every election toward
		// the most generically-named manifest.
		if len(phrase) >= 4 {
			if _, stop := shouldReplyStopWords[phrase]; !stop &&
				strings.Contains(normalizedText, " "+phrase+" ") {
				matchedPhrases[phrase] = struct{}{}
			}
		}
		for _, token := range strings.Fields(phrase) {
			if len(token) < 4 {
				continue
			}
			if _, stop := shouldReplyStopWords[token]; stop {
				continue
			}
			if strings.Contains(normalizedText, " "+token+" ") {
				matchedTokens[token] = struct{}{}
			}
		}
	}
	return len(matchedPhrases)*2 + len(matchedTokens)
}

// HashResponderEventID is FNV-1a 32-bit over the (lowercased) event id —
// stable across runtimes and instances.
func HashResponderEventID(eventID string) uint32 {
	value := strings.ToLower(strings.TrimSpace(eventID))
	var hash uint32 = 0x811c9dc5
	for i := 0; i < len(value); i++ {
		hash ^= uint32(value[i])
		hash *= 0x01000193
	}
	return hash
}

// ComputeResponderElection computes the deterministic responder order and this
// agent's role in it. Returns nil when there are no candidates.
func ComputeResponderElection(input ResponderElectionInput) *ResponderElectionResult {
	self := normalizeResponderKey(input.SelfPubkey)
	type scoredCandidate struct {
		pubkey string
		score  int
	}
	seen := map[string]struct{}{}
	scores := map[string]int{}
	scored := make([]scoredCandidate, 0, len(input.Candidates))
	for _, candidate := range input.Candidates {
		key := normalizeResponderKey(candidate.Pubkey)
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		score := ScoreResponderCapabilityMatch(input.Text, candidate.Capabilities)
		scores[key] = score
		scored = append(scored, scoredCandidate{pubkey: key, score: score})
	}
	if len(scored) == 0 {
		return nil
	}

	// Eligible set: capability-matched members; when nobody matches, every
	// candidate is equally (un)qualified and the hash alone decides.
	matched := make([]scoredCandidate, 0, len(scored))
	for _, candidate := range scored {
		if candidate.score > 0 {
			matched = append(matched, candidate)
		}
	}
	eligible := scored
	if len(matched) > 0 {
		eligible = matched
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].score != eligible[j].score {
			return eligible[i].score > eligible[j].score
		}
		return eligible[i].pubkey < eligible[j].pubkey
	})

	// Rotate each equal-score band by hash(event_id) mod band size.
	hash := HashResponderEventID(input.EventID)
	order := make([]string, 0, len(eligible))
	start := 0
	for start < len(eligible) {
		end := start
		for end < len(eligible) && eligible[end].score == eligible[start].score {
			end++
		}
		bandSize := end - start
		rotation := int(hash % uint32(bandSize))
		for offset := 0; offset < bandSize; offset++ {
			order = append(order, eligible[start+((rotation+offset)%bandSize)].pubkey)
		}
		start = end
	}

	selfIndex := -1
	for i, pubkey := range order {
		if pubkey == self {
			selfIndex = i
			break
		}
	}
	role := ResponderRoleNotCandidate
	switch {
	case selfIndex == 0:
		role = ResponderRoleElected
	case selfIndex == 1:
		role = ResponderRoleSuccessor
	case selfIndex > 1:
		role = ResponderRoleDeferred
	}
	successor := ""
	if len(order) > 1 {
		successor = order[1]
	}
	return &ResponderElectionResult{
		Role:            role,
		Order:           order,
		ElectedPubkey:   order[0],
		SuccessorPubkey: successor,
		SelfIndex:       selfIndex,
		EligibleCount:   len(order),
		Scores:          scores,
	}
}

// ---------------------------------------------------------------------------
// R2 election evaluation over the NIP-51 fleet directory
// ---------------------------------------------------------------------------

// ResponderDirectoryEntry is one NIP-51 fleet-directory member with its
// advertised capability terms (kind:30317 tool names in Metiq).
type ResponderDirectoryEntry struct {
	Pubkey       string
	Capabilities []string
}

// NostrResponderElectionParams is the input to EvaluateNostrResponderElection.
type NostrResponderElectionParams struct {
	// Policy carries the room's responderElection knobs.
	Policy NostrRoomPolicy
	// SelfPubkey is this agent's pubkey (hex).
	SelfPubkey string
	// SelfCapabilities are the locally declared capability terms; the
	// fleet-visible advertisement for self wins when present so every instance
	// scores this agent from the same manifest.
	SelfCapabilities []string
	EventID          string
	Text             string
	SenderPubkey     string
	// Gate is the R1 should-reply decision; nil for mention/command traffic
	// (the message is fully this agent's — election bypassed).
	Gate *ShouldReplyGateDecision
	// Directory is the NIP-51 fleet directory snapshot; empty = no directory
	// (election inert).
	Directory []ResponderDirectoryEntry
	// IsMuted, when set, excludes muted members from taking turns.
	IsMuted func(pubkey string) bool
}

// NostrResponderElectionDecision is an applicable election with the room's
// takeover window attached.
type NostrResponderElectionDecision struct {
	ResponderElectionResult
	// Takeover is how long the successor waits for the elected responder
	// before claiming the event.
	Takeover time.Duration
}

func safeLowercaseResponderPubkey(value string) string {
	if pk := normalizeNostrPubkey(value); pk != "" {
		return pk
	}
	return strings.ToLower(strings.TrimSpace(value))
}

// EvaluateNostrResponderElection decides whether the deterministic
// single-responder election applies to an admitted ambient room message and,
// if so, computes this agent's role in it. Mirrors the reference
// evaluateNostrResponderElection (openclaw-nostr channel.ts, ocn-myl).
//
// Returns nil when the message is fully this agent's to answer (election
// bypassed): explicit mentions, replies-to-bot, and command traffic (the
// should-reply gate decision is nil there — explicit mention = always mine),
// name-addressed messages, gate-dropped turns (handled upstream), rooms with
// the election switched off, and deployments without a NIP-51 fleet directory
// or without another capability-advertising fleet member. Never consulted for
// DMs and never a substitute for authorization.
func EvaluateNostrResponderElection(params NostrResponderElectionParams) *NostrResponderElectionDecision {
	if !params.Policy.ResponderElection {
		return nil
	}
	// Composes with R1: the election runs ONLY on ambient traffic the
	// should-reply gate admitted.
	gate := params.Gate
	if gate == nil || gate.Outcome != ShouldReplyPass {
		return nil
	}
	// Being addressed by name targets THIS agent, like an @mention.
	if gate.Reason == ShouldReplyReasonPTagged ||
		gate.Reason == ShouldReplyReasonDirectlyAddressed ||
		gate.Facts.DirectlyAddressed {
		return nil
	}
	if len(params.Directory) == 0 {
		return nil
	}

	self := safeLowercaseResponderPubkey(params.SelfPubkey)
	sender := safeLowercaseResponderPubkey(params.SenderPubkey)
	selfCapabilities := params.SelfCapabilities
	candidates := make([]ResponderCandidate, 0, len(params.Directory)+1)
	for _, entry := range params.Directory {
		pubkey := safeLowercaseResponderPubkey(entry.Pubkey)
		if pubkey == self {
			// Prefer the fleet-visible capability advertisement for self so
			// every instance scores this agent from the same manifest.
			if len(entry.Capabilities) > 0 {
				selfCapabilities = entry.Capabilities
			}
			continue
		}
		// The author never answers itself; unadvertised members cannot be
		// scored consistently across instances; muted members never take turns.
		if pubkey == sender {
			continue
		}
		if len(entry.Capabilities) == 0 {
			continue
		}
		if params.IsMuted != nil && params.IsMuted(pubkey) {
			continue
		}
		candidates = append(candidates, ResponderCandidate{
			Pubkey:       pubkey,
			Capabilities: entry.Capabilities,
		})
	}
	// Solo agent (no other capability-advertising fleet member): nothing to elect.
	if len(candidates) == 0 {
		return nil
	}
	candidates = append(candidates, ResponderCandidate{Pubkey: self, Capabilities: selfCapabilities})

	election := ComputeResponderElection(ResponderElectionInput{
		EventID:    params.EventID,
		Text:       params.Text,
		SelfPubkey: self,
		Candidates: candidates,
	})
	if election == nil {
		return nil
	}
	return &NostrResponderElectionDecision{
		ResponderElectionResult: *election,
		Takeover:                params.Policy.ResponderElectionTakeover,
	}
}

// ---------------------------------------------------------------------------
// Takeover coordinator
// ---------------------------------------------------------------------------

// TakeoverPending is one armed takeover: the successor's claim on an event the
// elected responder has not answered yet.
type TakeoverPending[T any] struct {
	RoomKey       string
	EventID       string
	ElectedPubkey string
	Payload       T
}

// TakeoverRoomMessageFacts are the facts of one live room message fed into the
// coordinator; a message that settles a contested event cancels its takeover.
type TakeoverRoomMessageFacts struct {
	RoomKey           string
	SenderPubkey      string
	ReplyToEventID    string
	ThreadRootEventID string
}

// TakeoverReactionFacts are the facts of one observed room reaction; a
// claim/ack on the event by anyone else cancels the pending takeover.
type TakeoverReactionFacts struct {
	RoomKey       string
	SenderPubkey  string
	TargetEventID string
}

// DefaultTakeoverMaxPending bounds concurrently-armed takeovers; the oldest is
// evicted past the bound.
const DefaultTakeoverMaxPending = 64

// TakeoverCoordinatorOptions configure NewTakeoverCoordinator.
type TakeoverCoordinatorOptions[T any] struct {
	// SelfPubkey is this agent's pubkey; its own outbound reactions (e.g. its
	// own claim) never cancel its pending takeovers.
	SelfPubkey string
	// OnTakeover fires when the elected responder stays silent past the window.
	OnTakeover func(TakeoverPending[T])
	// OnError receives a panic recovered from OnTakeover.
	OnError func(error)
	// MaxPending bounds concurrently-armed takeovers (0 = default 64).
	MaxPending int
	// SetTimer / ClearTimer are injectable timers for tests. Nil uses
	// time.AfterFunc / (*time.Timer).Stop.
	SetTimer   func(fn func(), d time.Duration) any
	ClearTimer func(handle any)
}

// TakeoverCoordinator arms, observes, and fires successor takeover timers.
// Safe for concurrent use.
type TakeoverCoordinator[T any] struct {
	mu         sync.Mutex
	self       string
	onTakeover func(TakeoverPending[T])
	onError    func(error)
	maxPending int
	setTimer   func(fn func(), d time.Duration) any
	clearTimer func(handle any)
	entries    map[string]*takeoverEntry[T]
	// order tracks insertion order for oldest-first eviction (lazily compacted).
	order []string
}

type takeoverEntry[T any] struct {
	pending TakeoverPending[T]
	timer   any
}

// NewTakeoverCoordinator creates a takeover coordinator.
func NewTakeoverCoordinator[T any](opts TakeoverCoordinatorOptions[T]) (*TakeoverCoordinator[T], error) {
	maxPending := opts.MaxPending
	if maxPending == 0 {
		maxPending = DefaultTakeoverMaxPending
	}
	if maxPending < 1 {
		return nil, fmt.Errorf("takeover coordinator MaxPending must be a positive integer")
	}
	setTimer := opts.SetTimer
	clearTimer := opts.ClearTimer
	if setTimer == nil {
		setTimer = func(fn func(), d time.Duration) any { return time.AfterFunc(d, fn) }
	}
	if clearTimer == nil {
		clearTimer = func(handle any) {
			if t, ok := handle.(*time.Timer); ok {
				t.Stop()
			}
		}
	}
	return &TakeoverCoordinator[T]{
		self:       normalizeResponderKey(opts.SelfPubkey),
		onTakeover: opts.OnTakeover,
		onError:    opts.OnError,
		maxPending: maxPending,
		setTimer:   setTimer,
		clearTimer: clearTimer,
		entries:    map[string]*takeoverEntry[T]{},
	}, nil
}

// keyOf: roomKey is a normalized session key and eventID is hex, so a pipe
// join cannot collide.
func takeoverKeyOf(roomKey, eventID string) string { return roomKey + "|" + eventID }

// removeLocked deletes an entry and stops its timer. Caller holds c.mu.
func (c *TakeoverCoordinator[T]) removeLocked(key string) bool {
	entry, ok := c.entries[key]
	if !ok {
		return false
	}
	delete(c.entries, key)
	c.clearTimer(entry.timer)
	return true
}

// Schedule arms a takeover timer; returns false when this event is already
// pending.
func (c *TakeoverCoordinator[T]) Schedule(pending TakeoverPending[T], timeout time.Duration) bool {
	key := takeoverKeyOf(pending.RoomKey, pending.EventID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; exists {
		return false
	}
	// Evict the oldest still-armed takeover past the bound.
	for len(c.entries) >= c.maxPending {
		evicted := false
		for len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			if c.removeLocked(oldest) {
				evicted = true
				break
			}
		}
		if !evicted {
			break
		}
	}
	pending.ElectedPubkey = normalizeResponderKey(pending.ElectedPubkey)
	timer := c.setTimer(func() { c.fire(key) }, timeout)
	c.entries[key] = &takeoverEntry[T]{pending: pending, timer: timer}
	c.order = append(c.order, key)
	return true
}

// fire runs the takeover for key unless it was cancelled in the meantime.
func (c *TakeoverCoordinator[T]) fire(key string) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	if ok {
		delete(c.entries, key)
	}
	c.mu.Unlock()
	if !ok || c.onTakeover == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && c.onError != nil {
			if err, isErr := r.(error); isErr {
				c.onError(err)
			} else {
				c.onError(fmt.Errorf("takeover panic: %v", r))
			}
		}
	}()
	c.onTakeover(entry.pending)
}

// ObserveRoomMessage feeds EVERY live room message; it cancels takeovers the
// message settles (the elected responder answering, or anyone but self
// replying in the contested event's thread).
func (c *TakeoverCoordinator[T]) ObserveRoomMessage(facts TakeoverRoomMessageFacts) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) == 0 {
		return
	}
	sender := normalizeResponderKey(facts.SenderPubkey)
	for key, entry := range c.entries {
		if entry.pending.RoomKey != facts.RoomKey {
			continue
		}
		electedAnswered := sender == entry.pending.ElectedPubkey
		threadAnswered := sender != c.self &&
			(facts.ReplyToEventID == entry.pending.EventID ||
				facts.ThreadRootEventID == entry.pending.EventID)
		if electedAnswered || threadAnswered {
			c.removeLocked(key)
		}
	}
}

// ObserveReaction feeds room reactions; a claim/ack on the event by anyone
// else cancels the pending takeover. The agent's own outbound reactions (e.g.
// its own claim) never cancel.
func (c *TakeoverCoordinator[T]) ObserveReaction(facts TakeoverReactionFacts) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) == 0 {
		return
	}
	if normalizeResponderKey(facts.SenderPubkey) == c.self {
		return
	}
	for key, entry := range c.entries {
		if entry.pending.RoomKey == facts.RoomKey &&
			entry.pending.EventID == facts.TargetEventID {
			c.removeLocked(key)
		}
	}
}

// Cancel drops one armed takeover; reports whether it existed.
func (c *TakeoverCoordinator[T]) Cancel(roomKey, eventID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removeLocked(takeoverKeyOf(roomKey, eventID))
}

// Size reports the number of armed takeovers.
func (c *TakeoverCoordinator[T]) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Close cancels everything (channel shutdown).
func (c *TakeoverCoordinator[T]) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		c.removeLocked(key)
	}
	c.order = nil
}
