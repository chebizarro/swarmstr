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
	mu       sync.RWMutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
	help     map[string]string // optional HELP lines
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		counters: map[string]*Counter{},
		gauges:   map[string]*Gauge{},
		help:     map[string]string{},
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
	r.mu.RUnlock()

	sort.Strings(counterNames)
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

// Default is the process-wide default registry.
var Default = NewRegistry()

// Standard metric names registered in Default.
var (
	MessagesInbound  = Default.Counter("metiq_messages_inbound_total", "Total inbound messages processed")
	MessagesOutbound = Default.Counter("metiq_messages_outbound_total", "Total outbound messages sent")
	ToolCalls        = Default.Counter("metiq_tool_calls_total", "Total agent tool calls executed")
	ToolDenied       = Default.Counter("metiq_tool_denied_total", "Total agent tool calls denied by approval gate")
	TokensIn         = Default.Counter("metiq_tokens_in_total", "Total input tokens processed")
	TokensOut        = Default.Counter("metiq_tokens_out_total", "Total output tokens generated")

	ActiveSessions    = Default.Gauge("metiq_active_sessions", "Currently active chat sessions")
	ApprovalQueueSize = Default.Gauge("metiq_approval_queue_size", "Number of pending exec approval requests")
	RelayConnected    = Default.Gauge("metiq_relays_connected", "Number of currently connected relays")
	UptimeSeconds     = Default.Gauge("metiq_uptime_seconds", "Daemon uptime in seconds")
)
