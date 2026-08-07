// Package metrics provides a minimal thread-safe Prometheus-compatible metrics
// registry for metiqd.  It deliberately avoids the official prometheus/client_golang
// library to keep the dependency footprint small.
//
// Supported metric types: Counter (monotonically increasing) and Gauge (arbitrary value).
// The registry exports text/plain Prometheus exposition format via Exposition().
package metrics

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Registry holds a set of named counters and gauges.
type Registry struct {
	mu              sync.RWMutex
	counters        map[string]*Counter
	labeledCounters map[string]*labeledCounterSeries
	gauges          map[string]*Gauge
	help            map[string]string // optional HELP lines
}

type metricLabel struct {
	name  string
	value string
}

type labeledCounterSeries struct {
	name    string
	labels  []metricLabel
	counter *Counter
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:        map[string]*Counter{},
		labeledCounters: map[string]*labeledCounterSeries{},
		gauges:          map[string]*Gauge{},
		help:            map[string]string{},
	}
}

// Counter is a monotonically increasing uint64 metric.
type Counter struct {
	val uint64
}

// Inc increments the counter by 1.
func (c *Counter) Inc() { atomic.AddUint64(&c.val, 1) }

// Add increments the counter by n (must be ≥ 0).
func (c *Counter) Add(n uint64) { atomic.AddUint64(&c.val, n) }

// Value returns the current counter value.
func (c *Counter) Value() uint64 { return atomic.LoadUint64(&c.val) }

// Gauge is a metric whose value can go up or down.
type Gauge struct {
	mu  sync.Mutex
	val float64
}

// Set sets the gauge to v.
func (g *Gauge) Set(v float64) {
	g.mu.Lock()
	g.val = v
	g.mu.Unlock()
}

// Inc increments the gauge by 1.
func (g *Gauge) Inc() { g.Add(1) }

// Dec decrements the gauge by 1.
func (g *Gauge) Dec() { g.Add(-1) }

// Add adds delta to the gauge.
func (g *Gauge) Add(delta float64) {
	g.mu.Lock()
	g.val += delta
	g.mu.Unlock()
}

// Value returns the current gauge value.
func (g *Gauge) Value() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.val
}

// Counter registers (or retrieves) a counter with the given name and optional help string.
func (r *Registry) Counter(name, help string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	c := &Counter{}
	r.counters[name] = c
	if help != "" {
		r.help[name] = help
	}
	return c
}

// CounterWithLabels registers (or retrieves) one labeled counter series.
func (r *Registry) CounterWithLabels(name, help string, labels map[string]string) *Counter {
	labelNames := make([]string, 0, len(labels))
	for label := range labels {
		labelNames = append(labelNames, label)
	}
	sort.Strings(labelNames)
	seriesLabels := make([]metricLabel, 0, len(labelNames))
	var key strings.Builder
	key.WriteString(name)
	for _, label := range labelNames {
		value := labels[label]
		seriesLabels = append(seriesLabels, metricLabel{name: label, value: value})
		key.WriteByte(0)
		key.WriteString(label)
		key.WriteByte('=')
		key.WriteString(value)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if series, ok := r.labeledCounters[key.String()]; ok {
		return series.counter
	}
	counter := &Counter{}
	r.labeledCounters[key.String()] = &labeledCounterSeries{name: name, labels: seriesLabels, counter: counter}
	if help != "" {
		r.help[name] = help
	}
	return counter
}

// Gauge registers (or retrieves) a gauge with the given name and optional help string.
func (r *Registry) Gauge(name, help string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gauges[name]; ok {
		return g
	}
	g := &Gauge{}
	r.gauges[name] = g
	if help != "" {
		r.help[name] = help
	}
	return g
}

// Exposition returns a Prometheus text exposition format string.
// https://prometheus.io/docs/instrumenting/exposition_formats/
func (r *Registry) Exposition() string {
	r.mu.RLock()
	counterNames := make([]string, 0, len(r.counters))
	for n := range r.counters {
		counterNames = append(counterNames, n)
	}
	gaugeNames := make([]string, 0, len(r.gauges))
	for n := range r.gauges {
		gaugeNames = append(gaugeNames, n)
	}
	labeledCounters := make([]*labeledCounterSeries, 0, len(r.labeledCounters))
	for _, series := range r.labeledCounters {
		labeledCounters = append(labeledCounters, series)
	}
	r.mu.RUnlock()

	sort.Strings(counterNames)
	sort.Slice(labeledCounters, func(i, j int) bool {
		if labeledCounters[i].name != labeledCounters[j].name {
			return labeledCounters[i].name < labeledCounters[j].name
		}
		return formatMetricLabels(labeledCounters[i].labels) < formatMetricLabels(labeledCounters[j].labels)
	})
	sort.Strings(gaugeNames)

	var sb strings.Builder
	for _, name := range counterNames {
		r.mu.RLock()
		c := r.counters[name]
		h := r.help[name]
		r.mu.RUnlock()

		if h != "" {
			fmt.Fprintf(&sb, "# HELP %s %s\n", name, h)
		}
		fmt.Fprintf(&sb, "# TYPE %s counter\n", name)
		fmt.Fprintf(&sb, "%s %d\n", name, c.Value())
	}
	emittedCounterFamily := make(map[string]struct{}, len(counterNames)+len(labeledCounters))
	for _, name := range counterNames {
		emittedCounterFamily[name] = struct{}{}
	}
	for _, series := range labeledCounters {
		if _, emitted := emittedCounterFamily[series.name]; !emitted {
			r.mu.RLock()
			h := r.help[series.name]
			r.mu.RUnlock()
			if h != "" {
				fmt.Fprintf(&sb, "# HELP %s %s\n", series.name, h)
			}
			fmt.Fprintf(&sb, "# TYPE %s counter\n", series.name)
			emittedCounterFamily[series.name] = struct{}{}
		}
		fmt.Fprintf(&sb, "%s%s %d\n", series.name, formatMetricLabels(series.labels), series.counter.Value())
	}
	for _, name := range gaugeNames {
		r.mu.RLock()
		g := r.gauges[name]
		h := r.help[name]
		r.mu.RUnlock()

		if h != "" {
			fmt.Fprintf(&sb, "# HELP %s %s\n", name, h)
		}
		fmt.Fprintf(&sb, "# TYPE %s gauge\n", name)
		v := g.Value()
		if math.IsNaN(v) || math.IsInf(v, 0) {
			fmt.Fprintf(&sb, "%s 0\n", name)
		} else {
			fmt.Fprintf(&sb, "%s %g\n", name, v)
		}
	}
	return sb.String()
}

func formatMetricLabels(labels []metricLabel) string {
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, label := range labels {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(label.name)
		b.WriteString("=\"")
		value := strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(label.value)
		b.WriteString(value)
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

const shouldReplyGateMetricHelp = "Total ambient should-reply gate decisions by reason"

// RecordShouldReplyGate records one pass/drop decision with its stable reason.
func (r *Registry) RecordShouldReplyGate(outcome, reason string) {
	var name string
	switch outcome {
	case "pass":
		name = "metiq_should_reply_gate_pass_total"
	case "drop":
		name = "metiq_should_reply_gate_drop_total"
	default:
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = "unknown"
	}
	r.CounterWithLabels(name, shouldReplyGateMetricHelp, map[string]string{"reason": reason}).Inc()
}

// RecordShouldReplyGate records on the process-wide registry.
func RecordShouldReplyGate(outcome, reason string) {
	Default.RecordShouldReplyGate(outcome, reason)
}

const responderElectionMetricHelp = "Total deterministic single-responder election outcomes (R2)"

// RecordResponderElection records one responder-election outcome: "won" (this
// agent was elected and takes the turn), "deferred" (another agent answers this
// event), or "takeover" (the elected responder stayed silent past the window
// and this successor claims the event). Mirrors the reference election.won /
// election.deferred / election.takeover counters.
func (r *Registry) RecordResponderElection(outcome string) {
	var name string
	switch outcome {
	case "won":
		name = "metiq_responder_election_won_total"
	case "deferred":
		name = "metiq_responder_election_deferred_total"
	case "takeover":
		name = "metiq_responder_election_takeover_total"
	default:
		return
	}
	r.Counter(name, responderElectionMetricHelp).Inc()
}

// RecordResponderElection records on the process-wide registry.
func RecordResponderElection(outcome string) {
	Default.RecordResponderElection(outcome)
}

// Default is the process-wide default registry.
var Default = NewRegistry()

// Standard metric names registered in Default.
var (
	MessagesInbound      = Default.Counter("metiq_messages_inbound_total", "Total inbound messages processed")
	MessagesOutbound     = Default.Counter("metiq_messages_outbound_total", "Total outbound messages sent")
	OutboundACKReactions = Default.Counter("metiq_outbound_ack_reactions_total", "Total pure-ACK outbound room messages converted to reactions")
	TaskEchoSuppressed   = Default.Counter("metiq_task_echo_suppressed_total", "Total outbound room replies dropped as chat shadows of kind-30900 task transitions")
	ToolCalls            = Default.Counter("metiq_tool_calls_total", "Total agent tool calls executed")
	ToolDenied           = Default.Counter("metiq_tool_denied_total", "Total agent tool calls denied by approval gate")
	TokensIn             = Default.Counter("metiq_tokens_in_total", "Total input tokens processed")
	TokensOut            = Default.Counter("metiq_tokens_out_total", "Total output tokens generated")

	SteeringEnqueued       = Default.Counter("metiq_steering_enqueued_total", "Total active-run steering inputs accepted into a mailbox")
	SteeringDrained        = Default.Counter("metiq_steering_drained_total", "Total active-run steering inputs drained for model injection or residual fallback")
	SteeringDeduped        = Default.Counter("metiq_steering_deduped_total", "Total duplicate active-run steering inputs rejected by event ID")
	SteeringDropped        = Default.Counter("metiq_steering_dropped_total", "Total active-run steering inputs dropped by mailbox capacity policy")
	SteeringOverflowed     = Default.Counter("metiq_steering_overflowed_total", "Total active-run steering mailbox capacity overflows")
	SteeringUrgentAborted  = Default.Counter("metiq_steering_urgent_aborted_total", "Total busy interrupt inputs that aborted the active turn immediately")
	SteeringUrgentDeferred = Default.Counter("metiq_steering_urgent_deferred_total", "Total busy interrupt inputs deferred as urgent steering because a blocking tool was active")

	ActiveSessions    = Default.Gauge("metiq_active_sessions", "Currently active chat sessions")
	ApprovalQueueSize = Default.Gauge("metiq_approval_queue_size", "Number of pending exec approval requests")
	RelayConnected    = Default.Gauge("metiq_relays_connected", "Number of currently connected relays")
	UptimeSeconds     = Default.Gauge("metiq_uptime_seconds", "Daemon uptime in seconds")
)
