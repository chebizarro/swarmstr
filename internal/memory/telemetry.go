package memory

import (
	"context"
	"strings"
	"sync"
	"time"
)

type MemoryTelemetryEvent struct {
	Type      string         `json:"type"`
	At        string         `json:"at"`
	LatencyMS float64        `json:"latency_ms,omitempty"`
	Attrs     map[string]any `json:"attrs,omitempty"`
}

type MemoryRetrievalTrace struct {
	AtMS         int64                     `json:"at_ms"`
	Query        string                    `json:"query"`
	Mode         string                    `json:"mode"`
	Intent       QueryIntent               `json:"intent"`
	LatencyMS    float64                   `json:"latency_ms"`
	SelectedIDs  []string                  `json:"selected_ids"`
	RejectedIDs  []string                  `json:"rejected_ids,omitempty"`
	CandidateSet MemoryCandidateSetSummary `json:"candidate_set,omitempty"`
}

type ObservabilityStats struct {
	ReflectionPrecision float64 `json:"reflection_precision"`
	PromotionAcceptance float64 `json:"promotion_acceptance"`
}

type MemoryTokenTelemetry struct {
	TokenCostP50                    int `json:"token_cost_p50"`
	TokenCostP95                    int `json:"token_cost_p95"`
	TokenCostP99                    int `json:"token_cost_p99"`
	SessionTokenBudgetExceededCount int `json:"session_token_budget_exceeded_count"`
}

var memoryTelemetry = newTelemetryBuffer(512, 128)

type telemetryBuffer struct {
	mu                              sync.Mutex
	events                          []MemoryTelemetryEvent
	eventCap                        int
	traces                          []MemoryRetrievalTrace
	traceCap                        int
	captureTraces                   bool
	tokenCosts                      []int
	tokenCostCap                    int
	sessionTokenTotals              map[string]int
	sessionTokenBudgetExceededCount int
}

func newTelemetryBuffer(eventCap, traceCap int) *telemetryBuffer {
	return &telemetryBuffer{eventCap: eventCap, traceCap: traceCap, tokenCostCap: 1024, sessionTokenTotals: map[string]int{}}
}

func ConfigureMemoryTraceCapture(enabled bool, maxTraces int) {
	memoryTelemetry.mu.Lock()
	defer memoryTelemetry.mu.Unlock()
	memoryTelemetry.captureTraces = enabled
	if maxTraces > 0 {
		memoryTelemetry.traceCap = maxTraces
	}
	if !enabled {
		memoryTelemetry.traces = nil
	}
}

func recordMemoryTelemetry(eventType string, start time.Time, attrs map[string]any) {
	memoryTelemetry.mu.Lock()
	defer memoryTelemetry.mu.Unlock()
	evt := MemoryTelemetryEvent{Type: eventType, At: time.Now().UTC().Format(time.RFC3339Nano), Attrs: attrs}
	if !start.IsZero() {
		evt.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
	}
	memoryTelemetry.events = append(memoryTelemetry.events, evt)
	if len(memoryTelemetry.events) > memoryTelemetry.eventCap {
		drop := len(memoryTelemetry.events) - memoryTelemetry.eventCap
		memoryTelemetry.events = append([]MemoryTelemetryEvent(nil), memoryTelemetry.events[drop:]...)
	}
}

func recordRetrievalTrace(trace MemoryRetrievalTrace) {
	memoryTelemetry.mu.Lock()
	defer memoryTelemetry.mu.Unlock()
	if !memoryTelemetry.captureTraces {
		return
	}
	memoryTelemetry.traces = append(memoryTelemetry.traces, trace)
	if len(memoryTelemetry.traces) > memoryTelemetry.traceCap {
		drop := len(memoryTelemetry.traces) - memoryTelemetry.traceCap
		memoryTelemetry.traces = append([]MemoryRetrievalTrace(nil), memoryTelemetry.traces[drop:]...)
	}
}

func MemoryTelemetrySnapshot() (events []MemoryTelemetryEvent, traces []MemoryRetrievalTrace) {
	memoryTelemetry.mu.Lock()
	defer memoryTelemetry.mu.Unlock()
	events = append([]MemoryTelemetryEvent(nil), memoryTelemetry.events...)
	traces = append([]MemoryRetrievalTrace(nil), memoryTelemetry.traces...)
	return events, traces
}

func recordMemoryTokenCost(sessionID string, tokenCost int, sessionBudget int) MemoryTokenTelemetry {
	memoryTelemetry.mu.Lock()
	defer memoryTelemetry.mu.Unlock()
	if tokenCost > 0 {
		memoryTelemetry.tokenCosts = append(memoryTelemetry.tokenCosts, tokenCost)
		if len(memoryTelemetry.tokenCosts) > memoryTelemetry.tokenCostCap {
			drop := len(memoryTelemetry.tokenCosts) - memoryTelemetry.tokenCostCap
			memoryTelemetry.tokenCosts = append([]int(nil), memoryTelemetry.tokenCosts[drop:]...)
		}
	}
	if strings.TrimSpace(sessionID) != "" && tokenCost > 0 {
		memoryTelemetry.sessionTokenTotals[sessionID] += tokenCost
		if sessionBudget > 0 && memoryTelemetry.sessionTokenTotals[sessionID] > sessionBudget {
			memoryTelemetry.sessionTokenBudgetExceededCount++
		}
	}
	return memoryTokenTelemetryLocked()
}

func MemoryTokenTelemetrySnapshot() MemoryTokenTelemetry {
	memoryTelemetry.mu.Lock()
	defer memoryTelemetry.mu.Unlock()
	return memoryTokenTelemetryLocked()
}

func memoryTokenTelemetryLocked() MemoryTokenTelemetry {
	return MemoryTokenTelemetry{
		TokenCostP50:                    percentileInts(memoryTelemetry.tokenCosts, 0.50),
		TokenCostP95:                    percentileInts(memoryTelemetry.tokenCosts, 0.95),
		TokenCostP99:                    percentileInts(memoryTelemetry.tokenCosts, 0.99),
		SessionTokenBudgetExceededCount: memoryTelemetry.sessionTokenBudgetExceededCount,
	}
}

func MemoryObservabilityStats(store Store) (ObservabilityStats, bool) {
	typed, ok := any(store).(interface {
		computeObservabilityStats(context.Context) (ObservabilityStats, error)
	})
	if !ok {
		return ObservabilityStats{}, false
	}
	stats, err := typed.computeObservabilityStats(context.Background())
	if err != nil {
		return ObservabilityStats{}, false
	}
	return stats, true
}
