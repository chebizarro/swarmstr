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
}
