// Package channels — echo_suppressor.go ports the openclaw-nostr echo suppressor
// (Layer-3 loop control, opt-in per room). It keeps a small per-room ring of
// recent normalized message contents (peers' AND own) and, before a reply is
// delivered, drops it if it substantially restates recent traffic — damping
// bot-to-bot "me too" / re-acknowledgement loops. The check is cheap: normalize,
// then compare by exact normalized equality OR word-set Jaccard >= threshold
// (with a min-token guard so short-but-distinct lines are not collapsed).
package channels

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	DefaultEchoWindow    = 12
	DefaultEchoThreshold = 0.85
	DefaultEchoMinTokens = 4
	DefaultEchoTTL       = 10 * time.Minute

	// DefaultTaskEchoWindow bounds the fleet-wide ring of recent kind-30900
	// task-transition summaries (a busy fleet emits more transitions than one
	// room emits chat lines, so it is larger than DefaultEchoWindow).
	DefaultTaskEchoWindow = 32
	// DefaultTaskEchoThreshold is the task-transition restatement bar: the
	// fraction of a transition summary's tokens that must reappear in a chat
	// message for it to count as the transition's "chat shadow" (R6). It uses
	// coverage (intersection / summary size), not Jaccard, because a shadow
	// message usually restates the transition PLUS conversational filler.
	DefaultTaskEchoThreshold = 0.75
	// DefaultTaskEchoAnnounceWindow is the per (room, author, task) throttle:
	// one compact announcement is allowed through per window; further chat
	// restatements inside it are suppressed (the typed event is the source of
	// truth).
	DefaultTaskEchoAnnounceWindow = 10 * time.Minute
)

var echoURLRe = regexp.MustCompile(`https?://\S+`)

// NormalizeEchoText lowercases, strips URLs and punctuation, and collapses
// whitespace so cosmetic differences do not defeat echo detection.
func NormalizeEchoText(text string) string {
	s := echoURLRe.ReplaceAllString(strings.ToLower(text), " ")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func echoTokenize(normalized string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tok := range strings.Fields(normalized) {
		out[tok] = struct{}{}
	}
	return out
}

func echoJaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for tok := range a {
		if _, ok := b[tok]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

type echoEntry struct {
	normalized string
	tokens     map[string]struct{}
	at         time.Time
}

// EchoSuppressorOptions configure an EchoSuppressor.
type EchoSuppressorOptions struct {
	WindowSize             int
	SimilarityThreshold    float64
	MinTokensForSimilarity int
	TTL                    time.Duration
	// TaskWindowSize bounds the task-transition corpus (0 = default).
	TaskWindowSize int
	// TaskSimilarityThreshold is the default task-shadow coverage bar in (0,1]
	// (0 = DefaultTaskEchoThreshold).
	TaskSimilarityThreshold float64
	// TaskAnnounceWindow throttles allowed announcements per (room, author,
	// task) (0 = DefaultTaskEchoAnnounceWindow).
	TaskAnnounceWindow time.Duration
	Now                func() time.Time
}

// EchoSuppressor is a per-room recent-text ring for echo detection. Safe for
// concurrent use.
type EchoSuppressor struct {
	mu         sync.Mutex
	rooms      map[string][]echoEntry
	windowSize int
	threshold  float64
	minTokens  int
	ttl        time.Duration
	now        func() time.Time

	// Task-transition corpus (source tag "task", R6): fleet-wide, not
	// per-room, because kind-30900 transitions are not room traffic — their
	// chat shadow can surface in any room.
	taskEntries    []taskEchoEntry
	taskWindowSize int
	taskThreshold  float64
	taskAnnounce   time.Duration
	// announced tracks the last allowed announcement per room|author|task.
	announced map[string]time.Time
}

// NewEchoSuppressor constructs an EchoSuppressor.
func NewEchoSuppressor(opts EchoSuppressorOptions) (*EchoSuppressor, error) {
	ws := opts.WindowSize
	if ws == 0 {
		ws = DefaultEchoWindow
	}
	if ws < 1 {
		return nil, fmt.Errorf("echo suppressor windowSize must be a positive integer")
	}
	th := opts.SimilarityThreshold
	if th == 0 {
		th = DefaultEchoThreshold
	}
	if th <= 0 || th > 1 {
		return nil, fmt.Errorf("echo suppressor similarityThreshold must be in (0, 1]")
	}
	mt := opts.MinTokensForSimilarity
	if mt <= 0 {
		mt = DefaultEchoMinTokens
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultEchoTTL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	tws := opts.TaskWindowSize
	if tws == 0 {
		tws = DefaultTaskEchoWindow
	}
	if tws < 1 {
		return nil, fmt.Errorf("echo suppressor taskWindowSize must be a positive integer")
	}
	tth := opts.TaskSimilarityThreshold
	if tth == 0 {
		tth = DefaultTaskEchoThreshold
	}
	if tth <= 0 || tth > 1 {
		return nil, fmt.Errorf("echo suppressor taskSimilarityThreshold must be in (0, 1]")
	}
	taw := opts.TaskAnnounceWindow
	if taw <= 0 {
		taw = DefaultTaskEchoAnnounceWindow
	}
	return &EchoSuppressor{
		rooms:          map[string][]echoEntry{},
		windowSize:     ws,
		threshold:      th,
		minTokens:      mt,
		ttl:            ttl,
		now:            now,
		taskWindowSize: tws,
		taskThreshold:  tth,
		taskAnnounce:   taw,
		announced:      map[string]time.Time{},
	}, nil
}

// liveEntriesLocked returns the non-expired entries for roomKey, compacting the
// stored slice when entries have aged out. Caller holds s.mu.
func (s *EchoSuppressor) liveEntriesLocked(roomKey string) []echoEntry {
	entries := s.rooms[roomKey]
	if len(entries) == 0 {
		return nil
	}
	cutoff := s.now().Add(-s.ttl)
	live := entries[:0:0]
	for _, e := range entries {
		if !e.at.Before(cutoff) {
			live = append(live, e)
		}
	}
	if len(live) != len(entries) {
		if len(live) == 0 {
			delete(s.rooms, roomKey)
		} else {
			s.rooms[roomKey] = live
		}
	}
	return live
}

func (s *EchoSuppressor) toEntry(text string) echoEntry {
	normalized := NormalizeEchoText(text)
	return echoEntry{normalized: normalized, tokens: echoTokenize(normalized), at: s.now()}
}

func (s *EchoSuppressor) matches(candidate, entry echoEntry, threshold float64) bool {
	if candidate.normalized == "" {
		return false
	}
	if candidate.normalized == entry.normalized {
		return true
	}
	if len(candidate.tokens) < s.minTokens || len(entry.tokens) < s.minTokens {
		return false
	}
	return echoJaccard(candidate.tokens, entry.tokens) >= threshold
}

// Observe records text (peer or own) into the room ring.
func (s *EchoSuppressor) Observe(roomKey, text string) {
	entry := s.toEntry(text)
	if entry.normalized == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.liveEntriesLocked(roomKey)
	entries = append(entries, entry)
	for len(entries) > s.windowSize {
		entries = entries[1:]
	}
	s.rooms[roomKey] = entries
}

// IsEcho reports whether text substantially restates recent room traffic. A
// thresholdOverride in (0,1] applies a per-room similarity bar; anything else
// uses the configured default.
func (s *EchoSuppressor) IsEcho(roomKey, text string, thresholdOverride float64) bool {
	candidate := s.toEntry(text)
	if candidate.normalized == "" {
		return false
	}
	threshold := s.threshold
	if thresholdOverride > 0 && thresholdOverride <= 1 {
		threshold = thresholdOverride
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.liveEntriesLocked(roomKey) {
		if s.matches(candidate, e, threshold) {
			return true
		}
	}
	return false
}

// Reset clears one room, or all rooms when roomKey is empty. The full reset
// also drops the task-transition corpus and announcement throttles; a per-room
// reset drops only that room's chat ring and announcement throttles (task
// transitions are fleet-wide, not room state).
func (s *EchoSuppressor) Reset(roomKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if roomKey == "" {
		s.rooms = map[string][]echoEntry{}
		s.taskEntries = nil
		s.announced = map[string]time.Time{}
		return
	}
	delete(s.rooms, roomKey)
	prefix := roomKey + "\x00"
	for key := range s.announced {
		if strings.HasPrefix(key, prefix) {
			delete(s.announced, key)
		}
	}
}

// ── Task-transition echo (R6, mirror of openclaw-nostr ocn-rb3) ─────────────
//
// A kind-30900 fleet-task state transition is typed truth; a chat message by
// the SAME author that substantially restates it is a "chat shadow". Validated
// transitions are fed here as compact summaries (a distinct, source-tagged
// corpus separate from the per-room chat ring) and outbound replies are
// checked against them: the first restatement per (room, author, task) inside
// the announce window passes as the one allowed compact announcement; further
// restatements are suppressed.

// TaskTransitionSummary is one validated kind-30900 fleet-task transition.
type TaskTransitionSummary struct {
	// Author is the hex pubkey that signed the transition event.
	Author string
	TaskID string
	Status string
	Title  string
}

type taskEchoEntry struct {
	author     string
	taskID     string
	status     string
	normalized string
	tokens     map[string]struct{}
	at         time.Time
}

// TaskEchoVerdict is the outcome of CheckTaskEcho.
type TaskEchoVerdict struct {
	// Suppress is true when the text restates a recent same-author transition
	// AND the compact announcement for it was already spent.
	Suppress bool
	// Announce is true when the text restates a recent same-author transition
	// and IS the one allowed compact announcement (throttle recorded).
	Announce bool
	TaskID   string
	Status   string
}

// taskTransitionText renders the summary fed into normalization. Underscored
// statuses (in_progress) and hyphenated ids (swarmstr-31jn) normalize to the
// same tokens a chat restatement produces.
func taskTransitionText(t TaskTransitionSummary) string {
	return strings.TrimSpace(strings.Join([]string{"task", t.TaskID, t.Status, t.Title}, " "))
}

// ObserveTaskTransition records one validated transition into the task corpus.
func (s *EchoSuppressor) ObserveTaskTransition(t TaskTransitionSummary) {
	author := strings.ToLower(strings.TrimSpace(t.Author))
	if author == "" || strings.TrimSpace(t.TaskID) == "" {
		return
	}
	normalized := NormalizeEchoText(taskTransitionText(t))
	if normalized == "" {
		return
	}
	entry := taskEchoEntry{
		author:     author,
		taskID:     strings.TrimSpace(t.TaskID),
		status:     strings.TrimSpace(t.Status),
		normalized: normalized,
		tokens:     echoTokenize(normalized),
		at:         s.now(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.liveTaskEntriesLocked()
	entries = append(entries, entry)
	for len(entries) > s.taskWindowSize {
		entries = entries[1:]
	}
	s.taskEntries = entries
}

// liveTaskEntriesLocked drops expired task entries (same TTL as the chat
// ring). Caller holds s.mu.
func (s *EchoSuppressor) liveTaskEntriesLocked() []taskEchoEntry {
	if len(s.taskEntries) == 0 {
		return nil
	}
	cutoff := s.now().Add(-s.ttl)
	live := s.taskEntries[:0:0]
	for _, e := range s.taskEntries {
		if !e.at.Before(cutoff) {
			live = append(live, e)
		}
	}
	s.taskEntries = live
	return live
}

// taskCoverage is the fraction of the transition summary's tokens present in
// the candidate (echoCoverage of entry in candidate).
func taskCoverage(candidate, entry map[string]struct{}) float64 {
	if len(entry) == 0 {
		return 0
	}
	intersection := 0
	for tok := range entry {
		if _, ok := candidate[tok]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(len(entry))
}

// CheckTaskEcho reports whether text (about to be posted by author into
// roomKey) is the chat shadow of a recent same-author task transition. A
// thresholdOverride in (0,1] applies a per-room coverage bar; anything else
// uses the configured task default. The first shadow per (room, author, task)
// inside the announce window is returned as Announce (allowed through, and
// the throttle is recorded); later shadows return Suppress.
func (s *EchoSuppressor) CheckTaskEcho(roomKey, author, text string, thresholdOverride float64) TaskEchoVerdict {
	author = strings.ToLower(strings.TrimSpace(author))
	if author == "" {
		return TaskEchoVerdict{}
	}
	normalized := NormalizeEchoText(text)
	if normalized == "" {
		return TaskEchoVerdict{}
	}
	candidate := echoTokenize(normalized)
	threshold := s.taskThreshold
	if thresholdOverride > 0 && thresholdOverride <= 1 {
		threshold = thresholdOverride
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, at := range s.announced {
		if now.Sub(at) > s.taskAnnounce {
			delete(s.announced, key)
		}
	}
	for i := len(s.taskEntries) - 1; i >= 0; i-- { // newest transition first
		e := s.taskEntries[i]
		if e.at.Before(now.Add(-s.ttl)) || e.author != author {
			continue
		}
		if normalized != e.normalized {
			if len(e.tokens) < s.minTokens || taskCoverage(candidate, e.tokens) < threshold {
				continue
			}
		}
		announceKey := roomKey + "\x00" + author + "\x00" + e.taskID
		if _, spent := s.announced[announceKey]; spent {
			return TaskEchoVerdict{Suppress: true, TaskID: e.taskID, Status: e.status}
		}
		s.announced[announceKey] = now
		return TaskEchoVerdict{Announce: true, TaskID: e.taskID, Status: e.status}
	}
	return TaskEchoVerdict{}
}
