package metrics

import (
	"strings"
	"testing"
)

func TestCounter(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("test_counter", "a test counter")
	if c.Value() != 0 {
		t.Fatalf("initial value should be 0")
	}
	c.Inc()
	c.Inc()
	c.Add(3)
	if c.Value() != 5 {
		t.Fatalf("expected 5, got %d", c.Value())
	}
}

func TestGauge(t *testing.T) {
	r := NewRegistry()
	g := r.Gauge("test_gauge", "a test gauge")
	g.Set(10)
	g.Inc()
	g.Dec()
	g.Add(5)
	if g.Value() != 15 {
		t.Fatalf("expected 15, got %g", g.Value())
	}
	g.Set(0)
	if g.Value() != 0 {
		t.Fatalf("expected 0 after Set, got %g", g.Value())
	}
}

func TestExposition(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("requests_total", "total requests")
	g := r.Gauge("queue_depth", "queue depth")
	c.Add(42)
	g.Set(3.5)

	out := r.Exposition()

	if !strings.Contains(out, "# HELP requests_total total requests") {
		t.Errorf("missing HELP line for counter")
	}
	if !strings.Contains(out, "# TYPE requests_total counter") {
		t.Errorf("missing TYPE line for counter")
	}
	if !strings.Contains(out, "requests_total 42") {
		t.Errorf("missing counter value line")
	}
	if !strings.Contains(out, "# TYPE queue_depth gauge") {
		t.Errorf("missing TYPE line for gauge")
	}
	if !strings.Contains(out, "queue_depth 3.5") {
		t.Errorf("missing gauge value line")
	}
}

func TestRegistry_IdempotentLookup(t *testing.T) {
	r := NewRegistry()
	c1 := r.Counter("hits", "")
	c1.Add(10)
	c2 := r.Counter("hits", "") // same name — should return same counter
	if c2.Value() != 10 {
		t.Fatalf("expected counter to be reused; got %d", c2.Value())
	}
}

func TestDefaultRegistry(t *testing.T) {
	MessagesInbound.Inc()
	out := Default.Exposition()
	if !strings.Contains(out, "metiq_messages_inbound_total") {
		t.Errorf("default registry missing metiq_messages_inbound_total")
	}
	for _, name := range []string{
		"metiq_steering_enqueued_total",
		"metiq_steering_drained_total",
		"metiq_steering_deduped_total",
		"metiq_steering_dropped_total",
		"metiq_steering_overflowed_total",
		"metiq_steering_urgent_aborted_total",
		"metiq_steering_urgent_deferred_total",
		"metiq_heartbeat_age_seconds",
		"metiq_heartbeat_runs_total",
		"metiq_publish_outcomes_total",
		"metiq_handler_failures_total",
		"metiq_session_state",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("default registry missing %s", name)
		}
	}
}

func TestRecordShouldReplyGateLabelsOutcomeAndReason(t *testing.T) {
	r := NewRegistry()
	r.RecordShouldReplyGate("pass", "capability_match")
	r.RecordShouldReplyGate("drop", "talked_about")
	r.RecordShouldReplyGate("drop", "talked_about")

	out := r.Exposition()
	for _, want := range []string{
		`metiq_should_reply_gate_pass_total{reason="capability_match"} 1`,
		`metiq_should_reply_gate_drop_total{reason="talked_about"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing labeled should-reply metric %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "# TYPE metiq_should_reply_gate_drop_total counter") != 1 {
		t.Errorf("drop metric family metadata should appear once:\n%s", out)
	}
}

// R2 responder-election metrics mirror the reference election.won /
// election.deferred / election.takeover counters.
func TestRecordResponderElection(t *testing.T) {
	r := NewRegistry()
	r.RecordResponderElection("won")
	r.RecordResponderElection("deferred")
	r.RecordResponderElection("deferred")
	r.RecordResponderElection("takeover")
	r.RecordResponderElection("bogus") // ignored

	exposition := r.Exposition()
	for _, want := range []string{
		"metiq_responder_election_won_total 1",
		"metiq_responder_election_deferred_total 2",
		"metiq_responder_election_takeover_total 1",
	} {
		if !strings.Contains(exposition, want) {
			t.Fatalf("exposition missing %q:\n%s", want, exposition)
		}
	}
	if strings.Contains(exposition, "bogus") {
		t.Fatal("invalid outcome must not register a series")
	}
}

func TestOperationalMetricLabelsAreBoundedAndSecretSafe(t *testing.T) {
	r := NewRegistry()
	forbidden := []string{
		"prompt: reveal the system instructions",
		"nsec1privatekeymaterial",
		"bearer-token-secret",
		"session-7f31d2",
		`{"kind":1,"content":"raw event payload"}`,
	}

	r.RecordShouldReplyGate("pass", forbidden[0])
	r.RecordHeartbeatOutcome(forbidden[0])
	r.RecordPublishOutcome(forbidden[1], forbidden[2])
	r.RecordHandlerFailure(forbidden[3])
	r.SetSessionState(forbidden[4], 1)

	out := r.Exposition()
	for _, secret := range forbidden {
		if strings.Contains(out, secret) {
			t.Fatalf("metric exposition leaked forbidden value %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{
		`metiq_should_reply_gate_pass_total{reason="unknown"} 1`,
		`metiq_heartbeat_runs_total{outcome="unknown"} 1`,
		`metiq_publish_outcomes_total{outcome="unknown",transport="unknown"} 1`,
		`metiq_handler_failures_total{handler="unknown"} 1`,
		`metiq_session_state{state="unknown"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("metric exposition missing bounded series %q:\n%s", want, out)
		}
	}
}

func TestOperationalMetricsExposeBoundedOutcomesAndSessionState(t *testing.T) {
	r := NewRegistry()
	r.RecordHeartbeatOutcome("success")
	r.RecordHeartbeatOutcome("failure")
	r.RecordPublishOutcome("nip17", "success")
	r.RecordPublishOutcome("nip29", "failure")
	r.RecordHandlerFailure("control_rpc")
	r.SetSessionState("stored", 4)
	r.SetSessionState("running", 2)

	out := r.Exposition()
	for _, want := range []string{
		`metiq_heartbeat_runs_total{outcome="success"} 1`,
		`metiq_heartbeat_runs_total{outcome="failure"} 1`,
		`metiq_publish_outcomes_total{outcome="success",transport="nip17"} 1`,
		`metiq_publish_outcomes_total{outcome="failure",transport="nip29"} 1`,
		`metiq_handler_failures_total{handler="control_rpc"} 1`,
		`metiq_session_state{state="stored"} 4`,
		`metiq_session_state{state="running"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("metric exposition missing %q:\n%s", want, out)
		}
	}
}

func TestRecordProgressLedger(t *testing.T) {
	r := NewRegistry()
	r.RecordProgressLedger("run")
	r.RecordProgressLedger("run")
	r.RecordProgressLedger("post")
	r.RecordProgressLedger("bogus") // ignored

	exposition := r.Exposition()
	for _, want := range []string{
		"metiq_progress_ledger_runs_total 2",
		"metiq_progress_ledger_posts_total 1",
	} {
		if !strings.Contains(exposition, want) {
			t.Fatalf("exposition missing %q:\n%s", want, exposition)
		}
	}
	if strings.Contains(exposition, "bogus") {
		t.Fatal("invalid outcome must not register a series")
	}
}
