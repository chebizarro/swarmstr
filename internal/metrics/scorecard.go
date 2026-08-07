// Package metrics — scorecard.go implements the R7 per-room group-chat
// discipline scorecard: a bounded per-room aggregation of the deterministic
// loop-control signals (ACK conversion, should-reply gate, echo / task-echo
// drops, pair-guard trips, responder election, commitment enforcement,
// progress ledger, status reactions) plus agent message-share tracking against
// the 1/n fair-share baseline ("Time to Talk" rate matching: an agent
// consistently above ~2/n of a room's traffic trips the share alarm).
//
// Cardinality is bounded by construction: at most MaxScorecardRooms distinct
// room label values (later rooms aggregate into ScorecardOverflowRoom) and a
// fixed closed set of signal label values — no per-event or per-sender labels
// are ever exported. Per-sender detail appears only in the JSON snapshot,
// capped at maxSnapshotSenders entries per room.
//
// Parity: mirrors openclaw-nostr ocn-340 (counterpart pending).
//
// Follow-up (documented, deliberately not implemented here): MAST LLM-judge
// transcript annotation — labeling room transcripts with the multi-agent
// failure taxonomy and joining those codes into this scorecard. Tracked as
// swarmstr-z4ub.
package metrics

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// RoomSignal is one discipline signal aggregated per room.
type RoomSignal string

// The closed set of per-room discipline signals (fixed label cardinality).
const (
	RoomSignalACKConversion     RoomSignal = "ack_conversion"
	RoomSignalGatePass          RoomSignal = "gate_pass"
	RoomSignalGateDrop          RoomSignal = "gate_drop"
	RoomSignalEchoDrop          RoomSignal = "echo_drop"
	RoomSignalTaskEchoDrop      RoomSignal = "task_echo_drop"
	RoomSignalPairGuardTrip     RoomSignal = "pair_guard_trip"
	RoomSignalElectionWon       RoomSignal = "election_won"
	RoomSignalElectionDeferred  RoomSignal = "election_deferred"
	RoomSignalElectionTakeover  RoomSignal = "election_takeover"
	RoomSignalCommitmentBlocked RoomSignal = "commitment_blocked"
	RoomSignalCommitmentDropped RoomSignal = "commitment_dropped"
	RoomSignalLedgerRun         RoomSignal = "ledger_run"
	RoomSignalLedgerPost        RoomSignal = "ledger_post"
	RoomSignalStatusReaction    RoomSignal = "status_reaction"
)

// Scorecard bounds and message-share window defaults.
const (
	// MaxScorecardRooms caps distinct room label values; rooms beyond the cap
	// aggregate into ScorecardOverflowRoom.
	MaxScorecardRooms = 64
	// ScorecardOverflowRoom absorbs signals for rooms past the cardinality cap.
	ScorecardOverflowRoom = "_overflow"
	// scorecardUnknownRoom absorbs signals recorded with an empty room key.
	scorecardUnknownRoom = "_unknown"
	// DefaultShareWindowSize is the sliding message window per room.
	DefaultShareWindowSize = 100
	// DefaultShareWindowAge expires window entries older than this.
	DefaultShareWindowAge = 30 * time.Minute
	// ShareAlarmFactor is the rate-matching alarm multiple of the 1/n
	// fair-share baseline: a sender above ShareAlarmFactor/n share alarms.
	ShareAlarmFactor = 2.0
	// ShareAlarmMinMessages is the minimum window fill before the alarm can
	// trip (avoids flagging the first speaker in a quiet room).
	ShareAlarmMinMessages = 10
	// maxSnapshotSenders caps per-room sender detail in the JSON snapshot.
	maxSnapshotSenders = 8
)

// Exported scorecard metric names.
const (
	roomSignalMetricName     = "metiq_room_discipline_signal_total"
	roomSignalMetricHelp     = "Per-room group-chat discipline signal counts (R7 scorecard)"
	roomShareMaxMetricName   = "metiq_room_message_share_max"
	roomShareMaxMetricHelp   = "Largest single-sender message share in the room's sliding window"
	roomShareAlarmMetricName = "metiq_room_share_alarm_senders"
	roomShareAlarmMetricHelp = "Senders above the 2/n fair-share alarm threshold in the room's sliding window"
	roomMentionAgeMetricName = "metiq_room_unanswered_mention_age_seconds"
	roomMentionAgeMetricHelp = "Age of the oldest unanswered fleet-agent mention per room (progress-ledger review)"
)

type roomMessage struct {
	sender string
	at     time.Time
}

type roomState struct {
	signals map[RoomSignal]*Counter
	msgs    []roomMessage

	shareMax   *Gauge
	shareAlarm *Gauge
	mentionAge *Gauge
}

// RoomScorecardOptions tune a RoomScorecard; zero values take defaults.
type RoomScorecardOptions struct {
	MaxRooms   int
	WindowSize int
	WindowAge  time.Duration
	Now        func() time.Time
}

// RoomScorecard is a bounded per-room aggregation of discipline signals and
// message-share tracking. Safe for concurrent use.
type RoomScorecard struct {
	registry   *Registry
	maxRooms   int
	windowSize int
	windowAge  time.Duration
	now        func() time.Time

	mu    sync.Mutex
	rooms map[string]*roomState
}

// NewRoomScorecard creates a scorecard whose counters and gauges register in reg.
func NewRoomScorecard(reg *Registry, opts RoomScorecardOptions) *RoomScorecard {
	s := &RoomScorecard{
		registry:   reg,
		maxRooms:   opts.MaxRooms,
		windowSize: opts.WindowSize,
		windowAge:  opts.WindowAge,
		now:        opts.Now,
		rooms:      map[string]*roomState{},
	}
	if s.maxRooms <= 0 {
		s.maxRooms = MaxScorecardRooms
	}
	if s.windowSize <= 0 {
		s.windowSize = DefaultShareWindowSize
	}
	if s.windowAge <= 0 {
		s.windowAge = DefaultShareWindowAge
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// roomStateLocked resolves (or creates) the bounded room bucket. Callers hold s.mu.
func (s *RoomScorecard) roomStateLocked(room string) (string, *roomState) {
	room = strings.TrimSpace(room)
	if room == "" {
		room = scorecardUnknownRoom
	}
	if state, ok := s.rooms[room]; ok {
		return room, state
	}
	if len(s.rooms) >= s.maxRooms {
		if state, ok := s.rooms[ScorecardOverflowRoom]; ok {
			return ScorecardOverflowRoom, state
		}
		room = ScorecardOverflowRoom
	}
	state := &roomState{
		signals:    map[RoomSignal]*Counter{},
		shareMax:   s.registry.GaugeWithLabels(roomShareMaxMetricName, roomShareMaxMetricHelp, map[string]string{"room": room}),
		shareAlarm: s.registry.GaugeWithLabels(roomShareAlarmMetricName, roomShareAlarmMetricHelp, map[string]string{"room": room}),
		mentionAge: s.registry.GaugeWithLabels(roomMentionAgeMetricName, roomMentionAgeMetricHelp, map[string]string{"room": room}),
	}
	s.rooms[room] = state
	return room, state
}

// RecordSignal counts one discipline signal for room. The room label is
// bounded (overflow bucket past MaxScorecardRooms); the signal label is a
// closed set, so exported cardinality stays sane.
func (s *RoomScorecard) RecordSignal(room string, signal RoomSignal) {
	if signal == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	boundedRoom, state := s.roomStateLocked(room)
	counter, ok := state.signals[signal]
	if !ok {
		counter = s.registry.CounterWithLabels(roomSignalMetricName, roomSignalMetricHelp,
			map[string]string{"room": boundedRoom, "signal": string(signal)})
		state.signals[signal] = counter
	}
	counter.Inc()
}

// RecordMessage tracks one room message by sender in the sliding window and
// refreshes the share gauges (max single-sender share + count of senders
// above the ShareAlarmFactor/n rate-matching threshold).
func (s *RoomScorecard) RecordMessage(room, sender string) {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, state := s.roomStateLocked(room)
	state.msgs = append(state.msgs, roomMessage{sender: sender, at: now})
	s.pruneLocked(state, now)
	maxShare, alarmed, _, _ := s.sharesLocked(state)
	state.shareMax.Set(maxShare)
	state.shareAlarm.Set(float64(len(alarmed)))
}

// SetUnansweredMentionAge sets the room's oldest unanswered-mention age gauge
// (zero clears it). Derived by the progress-ledger review; this scorecard only
// stores the result.
func (s *RoomScorecard) SetUnansweredMentionAge(room string, age time.Duration) {
	if age < 0 {
		age = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, state := s.roomStateLocked(room)
	state.mentionAge.Set(age.Seconds())
}

// pruneLocked evicts window entries past the size or age bound. Callers hold s.mu.
func (s *RoomScorecard) pruneLocked(state *roomState, now time.Time) {
	cutoff := now.Add(-s.windowAge)
	start := 0
	if overflow := len(state.msgs) - s.windowSize; overflow > 0 {
		start = overflow
	}
	for start < len(state.msgs) && state.msgs[start].at.Before(cutoff) {
		start++
	}
	if start > 0 {
		state.msgs = append(state.msgs[:0], state.msgs[start:]...)
	}
}

// sharesLocked computes per-sender counts and the alarm set. Callers hold s.mu.
func (s *RoomScorecard) sharesLocked(state *roomState) (maxShare float64, alarmed []string, counts map[string]int, total int) {
	total = len(state.msgs)
	counts = make(map[string]int, 8)
	for _, m := range state.msgs {
		counts[m.sender]++
	}
	if total == 0 || len(counts) == 0 {
		return 0, nil, counts, total
	}
	n := len(counts)
	threshold := ShareAlarmFactor / float64(n)
	for sender, c := range counts {
		share := float64(c) / float64(total)
		if share > maxShare {
			maxShare = share
		}
		// The alarm needs a meaningful sample and >1 participant; with n<=2
		// the 2/n threshold is >=1 and can never trip (by design: rate
		// matching is a many-agent room concern).
		if total >= ShareAlarmMinMessages && n >= 2 && share > threshold {
			alarmed = append(alarmed, sender)
		}
	}
	sort.Strings(alarmed)
	return maxShare, alarmed, counts, total
}

// RoomSenderShare is one sender's slice of a room's sliding window.
type RoomSenderShare struct {
	Sender   string  `json:"sender"`
	Messages int     `json:"messages"`
	Share    float64 `json:"share"`
}

// RoomScorecardSnapshot is the JSON view of one room's discipline scorecard.
type RoomScorecardSnapshot struct {
	Room                        string            `json:"room"`
	Signals                     map[string]uint64 `json:"signals,omitempty"`
	WindowMessages              int               `json:"window_messages"`
	DistinctSenders             int               `json:"distinct_senders"`
	BaselineShare               float64           `json:"baseline_share,omitempty"`
	AlarmThreshold              float64           `json:"alarm_threshold,omitempty"`
	TopSenders                  []RoomSenderShare `json:"top_senders,omitempty"`
	ShareAlarms                 []string          `json:"share_alarms,omitempty"`
	UnansweredMentionAgeSeconds float64           `json:"unanswered_mention_age_seconds"`
}

// Snapshots returns the per-room scorecard, sorted by room key. The message
// window is pruned as of now, so shares reflect current traffic.
func (s *RoomScorecard) Snapshots() []RoomScorecardSnapshot {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RoomScorecardSnapshot, 0, len(s.rooms))
	for room, state := range s.rooms {
		s.pruneLocked(state, now)
		maxShare, alarmed, counts, total := s.sharesLocked(state)
		state.shareMax.Set(maxShare)
		state.shareAlarm.Set(float64(len(alarmed)))
		snap := RoomScorecardSnapshot{
			Room:                        room,
			WindowMessages:              total,
			DistinctSenders:             len(counts),
			ShareAlarms:                 alarmed,
			UnansweredMentionAgeSeconds: state.mentionAge.Value(),
		}
		if len(state.signals) > 0 {
			snap.Signals = make(map[string]uint64, len(state.signals))
			for signal, counter := range state.signals {
				snap.Signals[string(signal)] = counter.Value()
			}
		}
		if n := len(counts); n > 0 {
			snap.BaselineShare = 1 / float64(n)
			snap.AlarmThreshold = ShareAlarmFactor / float64(n)
			senders := make([]RoomSenderShare, 0, n)
			for sender, c := range counts {
				senders = append(senders, RoomSenderShare{Sender: sender, Messages: c, Share: float64(c) / float64(total)})
			}
			sort.Slice(senders, func(i, j int) bool {
				if senders[i].Messages != senders[j].Messages {
					return senders[i].Messages > senders[j].Messages
				}
				return senders[i].Sender < senders[j].Sender
			})
			if len(senders) > maxSnapshotSenders {
				senders = senders[:maxSnapshotSenders]
			}
			snap.TopSenders = senders
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Room < out[j].Room })
	return out
}

// SnapshotJSON returns the scorecard as a JSON document: {"rooms": [...]}.
func (s *RoomScorecard) SnapshotJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"rooms": s.Snapshots()})
}

// DefaultScorecard is the process-wide scorecard, registered in Default.
var DefaultScorecard = NewRoomScorecard(Default, RoomScorecardOptions{})

// RecordRoomSignal records one discipline signal on the process-wide scorecard.
func RecordRoomSignal(room string, signal RoomSignal) {
	DefaultScorecard.RecordSignal(room, signal)
}

// RecordRoomMessage tracks one room message on the process-wide scorecard.
func RecordRoomMessage(room, sender string) {
	DefaultScorecard.RecordMessage(room, sender)
}

// SetRoomUnansweredMentionAge sets a room's oldest unanswered-mention age on
// the process-wide scorecard.
func SetRoomUnansweredMentionAge(room string, age time.Duration) {
	DefaultScorecard.SetUnansweredMentionAge(room, age)
}

// RoomScorecardJSON returns the process-wide scorecard as JSON.
func RoomScorecardJSON() ([]byte, error) {
	return DefaultScorecard.SnapshotJSON()
}
