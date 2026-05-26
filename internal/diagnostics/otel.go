package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// OTelConfig configures optional OpenTelemetry-style diagnostics. When Endpoint
// is empty, all operations are no-ops.
type OTelConfig struct {
	Endpoint    string
	ServiceName string
	HTTPClient  *http.Client
	Now         func() time.Time
}

// Diagnostics records spans and counters for agent turns, tool calls, and
// provider requests. It intentionally avoids hard dependency on the OTel SDK so
// the feature remains optional for minimal builds.
type Diagnostics struct {
	cfg      OTelConfig
	enabled  bool
	mu       sync.Mutex
	spans    []SpanSnapshot
	counters map[string]int64
}

// Span is an active diagnostic span.
type Span struct {
	d     *Diagnostics
	name  string
	attrs map[string]string
	start time.Time
}

// SpanSnapshot is the serializable form exported to an OTLP-compatible HTTP
// collector endpoint for lightweight diagnostics.
type SpanSnapshot struct {
	Name       string            `json:"name"`
	StartUnix  int64             `json:"start_unix_nano"`
	EndUnix    int64             `json:"end_unix_nano"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// SetupOTLPTraceExporter initializes the optional OTLP trace exporter facade.
// It returns disabled no-op diagnostics when cfg.Endpoint and OTEL env vars are empty.
func SetupOTLPTraceExporter(cfg OTelConfig) *Diagnostics { return NewOTelDiagnostics(cfg) }

// NewOTelDiagnostics returns diagnostics configured from METIQ_OTEL_ENDPOINT or
// OTEL_EXPORTER_OTLP_ENDPOINT if cfg.Endpoint is empty.
func NewOTelDiagnostics(cfg OTelConfig) *Diagnostics {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		cfg.Endpoint = firstEnv("METIQ_OTEL_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if strings.TrimSpace(cfg.ServiceName) == "" {
		cfg.ServiceName = "metiq"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &Diagnostics{cfg: cfg, enabled: strings.TrimSpace(cfg.Endpoint) != "", counters: map[string]int64{}}
}

func (d *Diagnostics) Enabled() bool { return d != nil && d.enabled }

func (d *Diagnostics) StartAgentTurn(ctx context.Context, sessionID, turnID string) (context.Context, *Span) {
	return d.start(ctx, "agent.turn", map[string]string{"session_id": sessionID, "turn_id": turnID})
}

func (d *Diagnostics) StartToolCall(ctx context.Context, sessionID, turnID, toolName string) (context.Context, *Span) {
	d.IncCounter("tool.calls", 1)
	return d.start(ctx, "tool.call", map[string]string{"session_id": sessionID, "turn_id": turnID, "tool_name": toolName})
}

func (d *Diagnostics) StartProviderRequest(ctx context.Context, provider, model string) (context.Context, *Span) {
	d.IncCounter("provider.requests", 1)
	return d.start(ctx, "provider.request", map[string]string{"provider": provider, "model": model})
}

func (d *Diagnostics) start(ctx context.Context, name string, attrs map[string]string) (context.Context, *Span) {
	if d == nil || !d.enabled {
		return ctx, nil
	}
	attrs = cloneAttrs(attrs)
	attrs["service.name"] = d.cfg.ServiceName
	return ctx, &Span{d: d, name: name, attrs: attrs, start: d.now()}
}

// End closes a span and queues it for export. Passing a non-nil error annotates
// the span and increments diagnostics.errors.
func (s *Span) End(err error) {
	if s == nil || s.d == nil || !s.d.enabled {
		return
	}
	if err != nil {
		s.d.IncCounter("diagnostics.errors", 1)
	}
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	snap := SpanSnapshot{Name: s.name, StartUnix: s.start.UnixNano(), EndUnix: s.d.now().UnixNano(), Attributes: cloneAttrs(s.attrs)}
	if err != nil {
		snap.Error = err.Error()
	}
	s.d.spans = append(s.d.spans, snap)
}

func (d *Diagnostics) IncCounter(name string, delta int64) {
	if d == nil || !d.enabled || name == "" || delta == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.counters == nil {
		d.counters = map[string]int64{}
	}
	d.counters[name] += delta
}

func (d *Diagnostics) Counter(name string) int64 {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counters[name]
}

// Flush exports queued spans and metric counters to Endpoint. The payload is a
// small JSON envelope; collectors can bridge this into full OTLP pipelines. No-op
// diagnostics return nil without network activity.
func (d *Diagnostics) Flush(ctx context.Context) error {
	if d == nil || !d.enabled {
		return nil
	}
	d.mu.Lock()
	spans := append([]SpanSnapshot(nil), d.spans...)
	counters := map[string]int64{}
	for k, v := range d.counters {
		counters[k] = v
	}
	d.mu.Unlock()
	payload := map[string]any{"resource": map[string]string{"service.name": d.cfg.ServiceName}, "spans": spans, "metrics": counters}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, normalizeEndpoint(d.cfg.Endpoint), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("otel exporter returned non-2xx status")
	}
	d.mu.Lock()
	d.spans = nil
	d.mu.Unlock()
	return nil
}

func (d *Diagnostics) now() time.Time {
	if d.cfg.Now != nil {
		return d.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func cloneAttrs(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizeEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if strings.HasSuffix(endpoint, "/v1/traces") {
		return endpoint
	}
	return strings.TrimRight(endpoint, "/") + "/v1/traces"
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}
