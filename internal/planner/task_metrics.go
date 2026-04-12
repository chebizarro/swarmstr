// task_metrics.go defines task SLO, latency buckets, failure-class counters,
// and error-budget metrics.  It aggregates signals from completed TaskRuns
// and budget exhaustion events into a queryable snapshot that can be projected
// into the metrics registry.
package planner

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"metiq/internal/metrics"
	"metiq/internal/store/state"
)

// ── Latency buckets ────────────────────────────────────────────────────────────

// LatencyBucket represents one bucket in a latency histogram.
type LatencyBucket struct {
	// UpperBoundMS is the inclusive upper bound in milliseconds.
	// A value of 0 means "+Inf" (catch-all).
	UpperBoundMS int64 `json:"upper_bound_ms"`
	// Label is a human-readable name (e.g. "fast", "normal", "slow").
	Label string `json:"label"`
}

// DefaultLatencyBuckets returns the standard latency histogram for task runs.
//
//	≤  5 000 ms  → fast
//	≤ 30 000 ms  → normal
//	≤ 120 000 ms → slow
//	> 120 000 ms → very_slow
func DefaultLatencyBuckets() []LatencyBucket {
	return []LatencyBucket{
		{UpperBoundMS: 5_000, Label: "fast"},
		{UpperBoundMS: 30_000, Label: "normal"},
		{UpperBoundMS: 120_000, Label: "slow"},
		{UpperBoundMS: 0, Label: "very_slow"}, // +Inf
	}
}

// bucketLabel returns the label of the first bucket whose upper bound contains latencyMS.
func bucketLabel(buckets []LatencyBucket, latencyMS int64) string {
	for _, b := range buckets {
		if b.UpperBoundMS > 0 && latencyMS <= b.UpperBoundMS {
			return b.Label
		}
	}
	// Fall through to catch-all.
	for _, b := range buckets {
		if b.UpperBoundMS == 0 {
			return b.Label
		}
	}
	return "unknown"
}

// ── Task SLO ───────────────────────────────────────────────────────────────────

// TaskSLO defines success-latency and reliability targets for task execution.
type TaskSLO struct {
	// TargetLatencyMS is the p99 latency target in milliseconds.
	TargetLatencyMS int64 `json:"target_latency_ms"`
	// SuccessRateTarget is the target success rate (0.0–1.0).
	SuccessRateTarget float64 `json:"success_rate_target"`
	// ErrorBudgetFraction is the allowed failure fraction (1 - SuccessRateTarget).
	// Pre-computed for fast comparison.
	ErrorBudgetFraction float64 `json:"error_budget_fraction"`
}

// DefaultTaskSLO returns a production-suitable SLO:
// p99 ≤ 120s, 95% success rate, 5% error budget.
func DefaultTaskSLO() TaskSLO {
	return TaskSLO{
		TargetLatencyMS:     120_000,
		SuccessRateTarget:   0.95,
		ErrorBudgetFraction: 0.05,
	}
}

// ── Failure-class counters ─────────────────────────────────────────────────────

// FailureClassCounts tracks the number of failures per FailureClass.
type FailureClassCounts struct {
	Transient  int `json:"transient"`
	Provider   int `json:"provider"`
	Permanent  int `json:"permanent"`
	Budget     int `json:"budget"`
	SideEffect int `json:"side_effect"`
	Unclassed  int `json:"unclassed"`
}

func (f *FailureClassCounts) increment(class FailureClass) {
	switch class {
	case FailureTransient:
		f.Transient++
	case FailureProvider:
		f.Provider++
	case FailurePermanent:
		f.Permanent++
	case FailureBudget:
		f.Budget++
	case FailureSideEffect:
		f.SideEffect++
	default:
		f.Unclassed++
	}
}

// Total returns the sum of all failure counts.
func (f FailureClassCounts) Total() int {
	return f.Transient + f.Provider + f.Permanent + f.Budget + f.SideEffect + f.Unclassed
}

// ── Error-budget state ─────────────────────────────────────────────────────────

// ErrorBudgetState tracks error-budget consumption.
type ErrorBudgetState struct {
	// TotalRuns is the number of completed or failed task runs observed.
	TotalRuns int `json:"total_runs"`
	// FailedRuns is the number of failed task runs.
	FailedRuns int `json:"failed_runs"`
	// SuccessRate is the observed success rate (0.0–1.0).
	SuccessRate float64 `json:"success_rate"`
	// BudgetRemaining is the fraction of error budget remaining (can go negative).
	BudgetRemaining float64 `json:"budget_remaining"`
	// Exhausted is true when the error budget is fully consumed.
	Exhausted bool `json:"exhausted"`
	// ExhaustionEvents counts the number of budget exhaustion events.
	ExhaustionEvents int `json:"exhaustion_events"`
}

// ── Latency distribution ───────────────────────────────────────────────────────

// LatencyDistribution holds the histogram of task run latencies.
type LatencyDistribution struct {
	// BucketCounts maps bucket label → count.
	BucketCounts map[string]int `json:"bucket_counts"`
	// SumMS is the cumulative latency across all runs.
	SumMS int64 `json:"sum_ms"`
	// Count is the total number of observations.
	Count int `json:"count"`
	// P99ViolationCount is the number of runs exceeding the SLO target latency.
	P99ViolationCount int `json:"p99_violation_count"`
}

// MeanMS returns the mean latency in milliseconds, or 0 when no observations.
func (d LatencyDistribution) MeanMS() int64 {
	if d.Count == 0 {
		return 0
	}
	return d.SumMS / int64(d.Count)
}

// ── Metrics snapshot ───────────────────────────────────────────────────────────

// TaskMetricsSnapshot is the full point-in-time view of task execution metrics.
type TaskMetricsSnapshot struct {
	SLO           TaskSLO             `json:"slo"`
	Latency       LatencyDistribution `json:"latency"`
	Failures      FailureClassCounts  `json:"failures"`
	ErrorBudget   ErrorBudgetState    `json:"error_budget"`
	Completions   int                 `json:"completions"`
	Verifications int                 `json:"verifications"`
	RetryCount    int                 `json:"retry_count"`
}

// ── Collector ──────────────────────────────────────────────────────────────────

// TaskMetricsCollector aggregates task execution metrics.
// It is safe for concurrent use.
type TaskMetricsCollector struct {
	mu sync.Mutex

	slo     TaskSLO
	buckets []LatencyBucket

	completions   int
	failures      FailureClassCounts
	verifications int
	retryCount    int

	latencyBuckets map[string]int
	latencySumMS   int64
	latencyCount   int
	p99Violations  int

	exhaustionEvents int
	totalRuns        int
	failedRuns       int
}

// NewTaskMetricsCollector creates a collector with default SLO and latency buckets.
func NewTaskMetricsCollector() *TaskMetricsCollector {
	return NewTaskMetricsCollectorWith(DefaultTaskSLO(), DefaultLatencyBuckets())
}

// NewTaskMetricsCollectorWith creates a collector with custom SLO and buckets.
func NewTaskMetricsCollectorWith(slo TaskSLO, buckets []LatencyBucket) *TaskMetricsCollector {
	bc := make(map[string]int, len(buckets))
	for _, b := range buckets {
		bc[b.Label] = 0
	}
	return &TaskMetricsCollector{
		slo:            slo,
		buckets:        buckets,
		latencyBuckets: bc,
	}
}

// RecordCompletion records a completed task run.
// The run's WallClockMS is used for latency; if zero, EndedAt-StartedAt is used.
func (c *TaskMetricsCollector) RecordCompletion(run state.TaskRun) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalRuns++

	latencyMS := run.Usage.WallClockMS
	if latencyMS == 0 && run.EndedAt > 0 && run.StartedAt > 0 {
		latencyMS = (run.EndedAt - run.StartedAt) * 1000
	}

	if run.Status == state.TaskRunStatusCompleted {
		c.completions++
		c.recordLatencyLocked(latencyMS)
	}
}

// RecordFailure records a failed task run with its classified failure.
func (c *TaskMetricsCollector) RecordFailure(run state.TaskRun, class FailureClass) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalRuns++
	c.failedRuns++
	c.failures.increment(class)

	latencyMS := run.Usage.WallClockMS
	if latencyMS == 0 && run.EndedAt > 0 && run.StartedAt > 0 {
		latencyMS = (run.EndedAt - run.StartedAt) * 1000
	}
	c.recordLatencyLocked(latencyMS)
}

// RecordRetry increments the retry counter.
func (c *TaskMetricsCollector) RecordRetry() {
	c.mu.Lock()
	c.retryCount++
	c.mu.Unlock()
}

// RecordVerification increments the verification counter.
func (c *TaskMetricsCollector) RecordVerification() {
	c.mu.Lock()
	c.verifications++
	c.mu.Unlock()
}

// RecordExhaustion records a budget exhaustion event.
func (c *TaskMetricsCollector) RecordExhaustion(_ ExhaustionEvent) {
	c.mu.Lock()
	c.exhaustionEvents++
	c.mu.Unlock()
}

// recordLatencyLocked records a latency observation. Caller must hold c.mu.
func (c *TaskMetricsCollector) recordLatencyLocked(latencyMS int64) {
	if latencyMS <= 0 {
		return
	}
	label := bucketLabel(c.buckets, latencyMS)
	c.latencyBuckets[label]++
	c.latencySumMS += latencyMS
	c.latencyCount++
	if latencyMS > c.slo.TargetLatencyMS {
		c.p99Violations++
	}
}

// Snapshot returns the current metrics state.
func (c *TaskMetricsCollector) Snapshot() TaskMetricsSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	bc := make(map[string]int, len(c.latencyBuckets))
	for k, v := range c.latencyBuckets {
		bc[k] = v
	}

	eb := c.errorBudgetLocked()

	return TaskMetricsSnapshot{
		SLO: c.slo,
		Latency: LatencyDistribution{
			BucketCounts:      bc,
			SumMS:             c.latencySumMS,
			Count:             c.latencyCount,
			P99ViolationCount: c.p99Violations,
		},
		Failures:      c.failures,
		ErrorBudget:   eb,
		Completions:   c.completions,
		Verifications: c.verifications,
		RetryCount:    c.retryCount,
	}
}

// errorBudgetLocked computes error-budget state. Caller must hold c.mu.
func (c *TaskMetricsCollector) errorBudgetLocked() ErrorBudgetState {
	eb := ErrorBudgetState{
		TotalRuns:        c.totalRuns,
		FailedRuns:       c.failedRuns,
		ExhaustionEvents: c.exhaustionEvents,
	}
	if c.totalRuns == 0 {
		eb.SuccessRate = 1.0
		eb.BudgetRemaining = c.slo.ErrorBudgetFraction
		return eb
	}
	eb.SuccessRate = float64(c.totalRuns-c.failedRuns) / float64(c.totalRuns)
	failureRate := 1.0 - eb.SuccessRate
	eb.BudgetRemaining = c.slo.ErrorBudgetFraction - failureRate
	eb.Exhausted = eb.BudgetRemaining <= 0
	return eb
}

// ── Registry projection ────────────────────────────────────────────────────────

// ProjectToRegistry writes the current metrics snapshot into a metrics.Registry.
//
// All cumulative totals are exported as **gauges** (not counters) because the
// collector owns the running totals and ProjectToRegistry may be called
// repeatedly (e.g. on every scrape).  Using counters would double-count on
// each invocation since Counter.Add is additive.  The gauge values represent
// the collector's monotonically-increasing totals at the time of the call.
func (c *TaskMetricsCollector) ProjectToRegistry(reg *metrics.Registry) {
	snap := c.Snapshot()

	reg.Gauge("metiq_task_completions_total",
		"Total completed task runs").Set(float64(snap.Completions))
	reg.Gauge("metiq_task_failures_total",
		"Total failed task runs").Set(float64(snap.Failures.Total()))
	reg.Gauge("metiq_task_retries_total",
		"Total task retry attempts").Set(float64(snap.RetryCount))
	reg.Gauge("metiq_task_verifications_total",
		"Total task verification checks").Set(float64(snap.Verifications))

	// Failure-class breakdown.
	reg.Gauge("metiq_task_failures_transient",
		"Transient failures").Set(float64(snap.Failures.Transient))
	reg.Gauge("metiq_task_failures_provider",
		"Provider failures").Set(float64(snap.Failures.Provider))
	reg.Gauge("metiq_task_failures_permanent",
		"Permanent failures").Set(float64(snap.Failures.Permanent))
	reg.Gauge("metiq_task_failures_budget",
		"Budget exhaustion failures").Set(float64(snap.Failures.Budget))
	reg.Gauge("metiq_task_failures_side_effect",
		"Side-effect failures").Set(float64(snap.Failures.SideEffect))

	// Latency buckets.
	for label, count := range snap.Latency.BucketCounts {
		reg.Gauge(
			fmt.Sprintf("metiq_task_latency_%s_total", label),
			fmt.Sprintf("Task runs in %s latency bucket", label),
		).Set(float64(count))
	}
	reg.Gauge("metiq_task_latency_sum_ms",
		"Cumulative task latency in milliseconds").Set(float64(snap.Latency.SumMS))
	reg.Gauge("metiq_task_latency_p99_violations_total",
		"Task runs exceeding p99 SLO target").Set(float64(snap.Latency.P99ViolationCount))

	// Error budget.
	reg.Gauge("metiq_task_error_budget_remaining",
		"Fraction of error budget remaining").Set(snap.ErrorBudget.BudgetRemaining)
	reg.Gauge("metiq_task_success_rate",
		"Observed task success rate").Set(snap.ErrorBudget.SuccessRate)
	reg.Gauge("metiq_task_exhaustion_events_total",
		"Budget exhaustion events").Set(float64(snap.ErrorBudget.ExhaustionEvents))
	if snap.Latency.Count > 0 {
		reg.Gauge("metiq_task_latency_mean_ms",
			"Mean task latency in milliseconds").Set(float64(snap.Latency.MeanMS()))
	}
}

// ── Formatting ─────────────────────────────────────────────────────────────────

// FormatTaskMetrics returns a human-readable summary of the metrics snapshot.
func FormatTaskMetrics(snap TaskMetricsSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task Metrics\n")
	fmt.Fprintf(&b, "  Completions: %d  Failures: %d  Retries: %d  Verifications: %d\n",
		snap.Completions, snap.Failures.Total(), snap.RetryCount, snap.Verifications)

	// Latency.
	fmt.Fprintf(&b, "  Latency: mean=%dms observations=%d p99-violations=%d\n",
		snap.Latency.MeanMS(), snap.Latency.Count, snap.Latency.P99ViolationCount)
	labels := make([]string, 0, len(snap.Latency.BucketCounts))
	for l := range snap.Latency.BucketCounts {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		fmt.Fprintf(&b, "    [%s] %d\n", l, snap.Latency.BucketCounts[l])
	}

	// Failures by class.
	fmt.Fprintf(&b, "  Failures: transient=%d provider=%d permanent=%d budget=%d side_effect=%d unclassed=%d\n",
		snap.Failures.Transient, snap.Failures.Provider, snap.Failures.Permanent,
		snap.Failures.Budget, snap.Failures.SideEffect, snap.Failures.Unclassed)

	// Error budget.
	fmt.Fprintf(&b, "  SLO: target_latency=%dms success_rate=%.1f%%\n",
		snap.SLO.TargetLatencyMS, snap.SLO.SuccessRateTarget*100)
	fmt.Fprintf(&b, "  Error budget: remaining=%.2f%% exhausted=%v events=%d\n",
		snap.ErrorBudget.BudgetRemaining*100, snap.ErrorBudget.Exhausted,
		snap.ErrorBudget.ExhaustionEvents)

	return b.String()
}
