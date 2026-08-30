package metrics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Parity note: the R7 scorecard mirrors openclaw-nostr ocn-340 (counterpart
// pending); keep signal names and thresholds aligned when that lands.

func TestGaugeWithLabels(t *testing.T) {
	r := NewRegistry()
	g := r.GaugeWithLabels("labeled_gauge", "a labeled gauge", map[string]string{"room": "a"})
	g.Set(2.5)
	if again := r.GaugeWithLabels("labeled_gauge", "", map[string]string{"room": "a"}); again != g {
		t.Fatalf("same labels must return the same gauge series")
	}
	other := r.GaugeWithLabels("labeled_gauge", "", map[string]string{"room": "b"})
	other.Set(1)

	out := r.Exposition()
	if !strings.Contains(out, "# HELP labeled_gauge a labeled gauge") {
		t.Errorf("missing HELP line: %s", out)
	}
	if !strings.Contains(out, "# TYPE labeled_gauge gauge") {
		t.Errorf("missing TYPE line: %s", out)
	}
	if !strings.Contains(out, `labeled_gauge{room="a"} 2.5`) {
		t.Errorf("missing series a: %s", out)
	}
	if !strings.Contains(out, `labeled_gauge{room="b"} 1`) {
		t.Errorf("missing series b: %s", out)
	}
	if strings.Count(out, "# TYPE labeled_gauge gauge") != 1 {
		t.Errorf("TYPE family line must be emitted once: %s", out)
	}
}

func newTestScorecard(t *testing.T, opts RoomScorecardOptions) (*RoomScorecard, *Registry, *time.Time) {
	t.Helper()
	reg := NewRegistry()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	opts.Now = func() time.Time { return now }
	s := NewRoomScorecard(reg, opts)
	return s, reg, &now
}

func snapshotFor(t *testing.T, s *RoomScorecard, room string) RoomScorecardSnapshot {
	t.Helper()
	for _, snap := range s.Snapshots() {
		if snap.Room == room {
			return snap
		}
	}
	t.Fatalf("no snapshot for room %q", room)
	return RoomScorecardSnapshot{}
}

func TestRoomScorecardSignals(t *testing.T) {
	s, reg, _ := newTestScorecard(t, RoomScorecardOptions{})
	s.RecordSignal("nostr:room:alpha", RoomSignalGatePass)
	s.RecordSignal("nostr:room:alpha", RoomSignalGatePass)
	s.RecordSignal("nostr:room:alpha", RoomSignalEchoDrop)
	s.RecordSignal("nostr:room:beta", RoomSignalACKConversion)
	s.RecordSignal("nostr:room:alpha", "") // ignored

	alpha := snapshotFor(t, s, "nostr:room:alpha")
	if alpha.Signals[string(RoomSignalGatePass)] != 2 || alpha.Signals[string(RoomSignalEchoDrop)] != 1 {
		t.Fatalf("unexpected alpha signals: %+v", alpha.Signals)
	}
	beta := snapshotFor(t, s, "nostr:room:beta")
	if beta.Signals[string(RoomSignalACKConversion)] != 1 {
		t.Fatalf("unexpected beta signals: %+v", beta.Signals)
	}

	out := reg.Exposition()
	if !strings.Contains(out, `metiq_room_discipline_signal_total{room="room_01",signal="gate_pass"} 2`) {
		t.Errorf("missing labeled counter series: %s", out)
	}
	if !strings.Contains(out, "# TYPE metiq_room_discipline_signal_total counter") {
		t.Errorf("missing counter TYPE family: %s", out)
	}
}

func TestRoomScorecardMetricLabelsDoNotExposeSessionIdentifiers(t *testing.T) {
	s, reg, _ := newTestScorecard(t, RoomScorecardOptions{})
	sessionID := "session-7f31d2-private"
	rawEvent := `{"kind":1,"content":"raw event payload"}`
	s.RecordSignal(sessionID, RoomSignalGatePass)
	s.RecordSignal(sessionID, RoomSignal(rawEvent))
	s.RecordMessage(sessionID, "sender")
	s.SetUnansweredMentionAge(sessionID, time.Minute)

	out := reg.Exposition()
	if strings.Contains(out, sessionID) {
		t.Fatalf("Prometheus exposition leaked session identifier %q:\n%s", sessionID, out)
	}
	if strings.Contains(out, rawEvent) {
		t.Fatalf("Prometheus exposition leaked raw event data %q:\n%s", rawEvent, out)
	}
	if !strings.Contains(out, `room="room_01"`) {
		t.Fatalf("Prometheus exposition missing bounded room label:\n%s", out)
	}
	if snap := snapshotFor(t, s, sessionID); snap.Room != sessionID {
		t.Fatalf("authenticated JSON snapshot should retain the room key: %+v", snap)
	}
}

func TestRoomScorecardEmptyRoomKey(t *testing.T) {
	s, _, _ := newTestScorecard(t, RoomScorecardOptions{})
	s.RecordSignal("  ", RoomSignalGateDrop)
	snap := snapshotFor(t, s, "_unknown")
	if snap.Signals[string(RoomSignalGateDrop)] != 1 {
		t.Fatalf("empty room key must aggregate under _unknown: %+v", snap)
	}
}

func TestRoomScorecardRoomBounding(t *testing.T) {
	s, _, _ := newTestScorecard(t, RoomScorecardOptions{MaxRooms: 2})
	s.RecordSignal("room-1", RoomSignalGatePass)
	s.RecordSignal("room-2", RoomSignalGatePass)
	s.RecordSignal("room-3", RoomSignalGatePass) // past the cap
	s.RecordSignal("room-4", RoomSignalGateDrop) // shares the overflow bucket
	s.RecordSignal("room-1", RoomSignalGatePass) // existing rooms unaffected

	snaps := s.Snapshots()
	if len(snaps) != 3 { // room-1, room-2, _overflow
		t.Fatalf("expected 3 rooms (2 + overflow), got %d: %+v", len(snaps), snaps)
	}
	overflow := snapshotFor(t, s, ScorecardOverflowRoom)
	if overflow.Signals[string(RoomSignalGatePass)] != 1 || overflow.Signals[string(RoomSignalGateDrop)] != 1 {
		t.Fatalf("overflow bucket must absorb past-cap rooms: %+v", overflow.Signals)
	}
	if snapshotFor(t, s, "room-1").Signals[string(RoomSignalGatePass)] != 2 {
		t.Fatalf("existing room must keep counting after the cap")
	}
}

func TestRoomScorecardMessageShareAlarm(t *testing.T) {
	s, reg, _ := newTestScorecard(t, RoomScorecardOptions{})
	room := "nostr:room:busy"
	s.SetAgentRoster(room, []string{"loud", "a", "b", "c"})
	// 4 agents, 20 agent messages: "loud" sends 14 (share 0.7 > 2/4 = 0.5).
	for i := 0; i < 14; i++ {
		s.RecordMessage(room, "loud")
	}
	for _, quiet := range []string{"a", "b", "c"} {
		s.RecordMessage(room, quiet)
		s.RecordMessage(room, quiet)
	}

	snap := snapshotFor(t, s, room)
	if snap.WindowMessages != 20 || snap.DistinctSenders != 4 {
		t.Fatalf("unexpected window: %+v", snap)
	}
	if snap.BaselineShare != 0.25 || snap.AlarmThreshold != 0.5 {
		t.Fatalf("unexpected thresholds: %+v", snap)
	}
	if len(snap.ShareAlarms) != 1 || snap.ShareAlarms[0] != "loud" {
		t.Fatalf("expected loud to trip the 2/n alarm: %+v", snap.ShareAlarms)
	}
	if len(snap.TopSenders) == 0 || snap.TopSenders[0].Sender != "loud" || snap.TopSenders[0].Share != 0.7 {
		t.Fatalf("unexpected top senders: %+v", snap.TopSenders)
	}

	out := reg.Exposition()
	if !strings.Contains(out, `metiq_room_message_share_max{room="room_01"} 0.7`) {
		t.Errorf("missing share-max gauge: %s", out)
	}
	if !strings.Contains(out, `metiq_room_share_alarm_senders{room="room_01"} 1`) {
		t.Errorf("missing share-alarm gauge: %s", out)
	}
}

func TestRoomScorecardShareAlarmNeedsSampleAndPeers(t *testing.T) {
	s, _, _ := newTestScorecard(t, RoomScorecardOptions{})

	// Below the minimum sample: a dominant sender must not alarm.
	s.SetAgentRoster("quiet-room", []string{"solo", "other"})
	for i := 0; i < ShareAlarmMinMessages-2; i++ {
		s.RecordMessage("quiet-room", "solo")
	}
	s.RecordMessage("quiet-room", "other")
	if snap := snapshotFor(t, s, "quiet-room"); len(snap.ShareAlarms) != 0 {
		t.Fatalf("below-sample window must not alarm: %+v", snap.ShareAlarms)
	}

	// Single participant: 2/1 threshold is unreachable by design.
	s.SetAgentRoster("mono-room", []string{"solo"})
	for i := 0; i < 30; i++ {
		s.RecordMessage("mono-room", "solo")
	}
	snap := snapshotFor(t, s, "mono-room")
	if len(snap.ShareAlarms) != 0 {
		t.Fatalf("single-sender room must not alarm: %+v", snap.ShareAlarms)
	}
	if snap.TopSenders[0].Share != 1 {
		t.Fatalf("expected full share for the only sender: %+v", snap.TopSenders)
	}
}

func TestRoomScorecardWindowPruning(t *testing.T) {
	s, _, now := newTestScorecard(t, RoomScorecardOptions{WindowSize: 5, WindowAge: 10 * time.Minute})
	room := "nostr:room:pruned"
	s.SetAgentRoster(room, []string{"early", "late"})
	// 7 sends with a bounded window of 5: the oldest two fall off.
	for i := 0; i < 7; i++ {
		s.RecordMessage(room, "early")
	}
	if snap := snapshotFor(t, s, room); snap.WindowMessages != 5 {
		t.Fatalf("size bound must cap the window at 5, got %d", snap.WindowMessages)
	}

	// Advance past the age bound: the window drains entirely.
	*now = now.Add(11 * time.Minute)
	snap := snapshotFor(t, s, room)
	if snap.WindowMessages != 0 || snap.DistinctSenders != 0 || len(snap.TopSenders) != 0 {
		t.Fatalf("aged-out window must be empty: %+v", snap)
	}

	// Fresh traffic after the drain re-populates cleanly.
	s.RecordMessage(room, "late")
	if snap := snapshotFor(t, s, room); snap.WindowMessages != 1 || snap.TopSenders[0].Sender != "late" {
		t.Fatalf("post-drain window must only hold fresh traffic: %+v", snap)
	}
}

func TestRoomScorecardRosterExcludesHumansFromAgentShare(t *testing.T) {
	s, _, _ := newTestScorecard(t, RoomScorecardOptions{})
	room := "nostr:room:mixed"
	s.SetAgentRoster(room, []string{"agent-a", "agent-b", "silent-agent"})
	for i := 0; i < 6; i++ {
		s.RecordMessage(room, "human")
	}
	for i := 0; i < 3; i++ {
		s.RecordMessage(room, "agent-a")
	}
	s.RecordMessage(room, "agent-b")

	snap := snapshotFor(t, s, room)
	if snap.WindowMessages != 10 || snap.DistinctSenders != 3 {
		t.Fatalf("observed traffic must retain humans: %+v", snap)
	}
	if snap.AgentWindowMessages != 4 || snap.AgentRosterSize != 3 {
		t.Fatalf("agent denominator must use the fleet roster: %+v", snap)
	}
	if snap.BaselineShare != 1.0/3.0 || snap.AlarmThreshold != 2.0/3.0 {
		t.Fatalf("silent roster members must remain in n: %+v", snap)
	}
	if len(snap.TopSenders) != 2 || snap.TopSenders[0].Sender != "agent-a" || snap.TopSenders[0].Share != 0.75 {
		t.Fatalf("agent share must exclude human messages: %+v", snap.TopSenders)
	}
	if len(snap.ObservedTopSenders) == 0 || snap.ObservedTopSenders[0].Sender != "human" {
		t.Fatalf("observed sender diagnostics must retain humans: %+v", snap.ObservedTopSenders)
	}
}

func TestRoomScorecardRatesAndCommitmentLifecycle(t *testing.T) {
	s, _, _ := newTestScorecard(t, RoomScorecardOptions{})
	room := "nostr:room:rates"
	for i := 0; i < 4; i++ {
		s.RecordSignal(room, RoomSignalACKCandidate)
	}
	s.RecordSignal(room, RoomSignalACKConversion)
	for i := 0; i < 5; i++ {
		s.RecordSignal(room, RoomSignalEchoOpportunity)
	}
	s.RecordSignal(room, RoomSignalEchoDrop)
	for i := 0; i < 2; i++ {
		s.RecordSignal(room, RoomSignalTaskEchoOpportunity)
		s.RecordSignal(room, RoomSignalTaskEchoDrop)
	}
	s.RecordSignal(room, RoomSignalCommitmentOpened)
	s.RecordSignal(room, RoomSignalCommitmentOpened)
	s.RecordSignal(room, RoomSignalCommitmentCompleted)
	s.RecordSignal(room, RoomSignalCommitmentDropped)
	s.SetOpenCommitments(room, 2)

	snap := snapshotFor(t, s, room)
	if snap.ACKCandidates != 4 || snap.ACKConversions != 1 || snap.ACKConversionRate != 0.25 {
		t.Fatalf("unexpected ACK rate: %+v", snap)
	}
	if snap.EchoOpportunities != 5 || snap.EchoDrops != 1 || snap.EchoDropRate != 0.2 {
		t.Fatalf("unexpected echo rate: %+v", snap)
	}
	if snap.TaskEchoOpportunities != 2 || snap.TaskEchoDrops != 2 || snap.TaskEchoDropRate != 1 {
		t.Fatalf("unexpected task echo rate: %+v", snap)
	}
	if snap.CommitmentsOpen != 2 || snap.CommitmentsOpened != 2 || snap.CommitmentsCompleted != 1 || snap.CommitmentsDropped != 1 {
		t.Fatalf("unexpected commitment lifecycle: %+v", snap)
	}
}

func TestRoomScorecardUnansweredMentionAge(t *testing.T) {
	s, reg, _ := newTestScorecard(t, RoomScorecardOptions{})
	room := "nostr:room:mentions"
	s.SetUnansweredMentionAge(room, 90*time.Second)
	if snap := snapshotFor(t, s, room); snap.UnansweredMentionAgeSeconds != 90 {
		t.Fatalf("expected 90s mention age, got %g", snap.UnansweredMentionAgeSeconds)
	}
	if !strings.Contains(reg.Exposition(), `metiq_room_unanswered_mention_age_seconds{room="room_01"} 90`) {
		t.Errorf("missing mention-age gauge: %s", reg.Exposition())
	}
	// Zero clears; negative clamps.
	s.SetUnansweredMentionAge(room, 0)
	if snap := snapshotFor(t, s, room); snap.UnansweredMentionAgeSeconds != 0 {
		t.Fatalf("expected cleared mention age, got %g", snap.UnansweredMentionAgeSeconds)
	}
	s.SetUnansweredMentionAge(room, -time.Minute)
	if snap := snapshotFor(t, s, room); snap.UnansweredMentionAgeSeconds != 0 {
		t.Fatalf("negative age must clamp to zero, got %g", snap.UnansweredMentionAgeSeconds)
	}
}

func TestRoomScorecardSnapshotJSON(t *testing.T) {
	s, _, _ := newTestScorecard(t, RoomScorecardOptions{})
	s.RecordSignal("nostr:room:json", RoomSignalLedgerPost)
	s.SetAgentRoster("nostr:room:json", []string{"sender-1"})
	s.RecordMessage("nostr:room:json", "sender-1")
	s.SetUnansweredMentionAge("nostr:room:json", 30*time.Second)

	body, err := s.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON: %v", err)
	}
	var doc struct {
		Rooms []RoomScorecardSnapshot `json:"rooms"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Rooms) != 1 {
		t.Fatalf("expected 1 room, got %d", len(doc.Rooms))
	}
	room := doc.Rooms[0]
	if room.Room != "nostr:room:json" ||
		room.Signals[string(RoomSignalLedgerPost)] != 1 ||
		room.WindowMessages != 1 ||
		room.UnansweredMentionAgeSeconds != 30 {
		t.Fatalf("unexpected snapshot round-trip: %+v", room)
	}
}

func TestRoomScorecardSnapshotSenderCap(t *testing.T) {
	s, _, _ := newTestScorecard(t, RoomScorecardOptions{})
	room := "nostr:room:crowded"
	roster := make([]string, 0, maxSnapshotSenders+4)
	for i := 0; i < maxSnapshotSenders+4; i++ {
		roster = append(roster, strings.Repeat("s", i+1))
	}
	s.SetAgentRoster(room, roster)
	for _, sender := range roster {
		s.RecordMessage(room, sender)
	}
	snap := snapshotFor(t, s, room)
	if snap.DistinctSenders != maxSnapshotSenders+4 {
		t.Fatalf("distinct senders must count everyone: %d", snap.DistinctSenders)
	}
	if len(snap.TopSenders) != maxSnapshotSenders {
		t.Fatalf("snapshot sender detail must cap at %d, got %d", maxSnapshotSenders, len(snap.TopSenders))
	}
}

func TestDefaultScorecardHelpers(t *testing.T) {
	room := "nostr:room:default-helpers-test"
	RecordRoomSignal(room, RoomSignalPairGuardTrip)
	SetRoomAgentRoster(room, []string{"sender"})
	RecordRoomMessage(room, "sender")
	SetRoomUnansweredMentionAge(room, time.Minute)

	body, err := RoomScorecardJSON()
	if err != nil {
		t.Fatalf("RoomScorecardJSON: %v", err)
	}
	if !strings.Contains(string(body), room) {
		t.Fatalf("default scorecard JSON must include %q: %s", room, body)
	}
	found := false
	for _, snap := range DefaultScorecard.Snapshots() {
		if snap.Room == room {
			found = true
			if snap.Signals[string(RoomSignalPairGuardTrip)] != 1 || snap.UnansweredMentionAgeSeconds != 60 {
				t.Fatalf("unexpected default snapshot: %+v", snap)
			}
		}
	}
	if !found {
		t.Fatalf("room missing from default scorecard")
	}
}
