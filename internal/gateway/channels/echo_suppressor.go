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
	Now                    func() time.Time
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
	return &EchoSuppressor{
		rooms:      map[string][]echoEntry{},
		windowSize: ws,
		threshold:  th,
		minTokens:  mt,
		ttl:        ttl,
		now:        now,
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

// Reset clears one room, or all rooms when roomKey is empty.
func (s *EchoSuppressor) Reset(roomKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if roomKey == "" {
		s.rooms = map[string][]echoEntry{}
		return
	}
	delete(s.rooms, roomKey)
}
