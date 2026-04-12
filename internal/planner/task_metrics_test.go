package planner

import (
	"encoding/json"
	"math"
	"sync"
	"testing"

	"metiq/internal/metrics"
	"metiq/internal/store/state"
)

// ── Latency bucket tests ───────────────────────────────────────────────────────

func TestDefaultLatencyBuckets_Order(t *testing.T) {
	buckets := DefaultLatencyBuckets()
	if len(buckets) != 4 {
		t.Fatalf("expected 4 buckets, got %d", len(buckets))
	}
	// All except the last should have positive upper bounds.
	for i, b := range buckets[:len(buckets)-1] {
		if b.UpperBoundMS <= 0 {
			t.Errorf("bucket[%d] %q should have positive upper bound", i, b.Label)
		}
	}
	// Last bucket should be the catch-all.
	if last := buckets[len(buckets)-1]; last.UpperBoundMS != 0 {
		t.Errorf("last bucket should be catch-all (0), got %d", last.UpperBoundMS)
	}
}

func TestBucketLabel_Fast(t *testing.T) {
	buckets := DefaultLatencyBuckets()
	if got := bucketLabel(buckets, 3000); got != "fast" {
		t.Errorf("3000ms → %q, want fast", got)
	}
}

func TestBucketLabel_Normal(t *testing.T) {
	buckets := DefaultLatencyBuckets()
	if got := bucketLabel(buckets, 15000); got != "normal" {
		t.Errorf("15000ms → %q, want normal", got)
	}
}

func TestBucketLabel_Slow(t *testing.T) {
	buckets := DefaultLatencyBuckets()
	if got := bucketLabel(buckets, 60000); got != "slow" {
		t.Errorf("60000ms → %q, want slow", got)
	}
}

func TestBucketLabel_VerySlow(t *testing.T) {
	buckets := DefaultLatencyBuckets()
	if got := bucketLabel(buckets, 300000); got != "very_slow" {
		t.Errorf("300000ms → %q, want very_slow", got)
	}
}

func TestBucketLabel_ExactBoundary(t *testing.T) {
	buckets := DefaultLatencyBuckets()
	// Exactly on boundary should be inclusive.
	if got := bucketLabel(buckets, 5000); got != "fast" {
		t.Errorf("5000ms → %q, want fast", got)
	}
	if got := bucketLabel(buckets, 30000); got != "normal" {
		t.Errorf("30000ms → %q, want normal", got)
	}
	if got := bucketLabel(buckets, 120000); got != "slow" {
		t.Errorf("120000ms → %q, want slow", got)
	}
}

func TestBucketLabel_EmptyBuckets(t *testing.T) {
	if got := bucketLabel(nil, 1000); got != "unknown" {
		t.Errorf("empty buckets → %q, want unknown", got)
	}
}

// ── SLO tests ──────────────────────────────────────────────────────────────────

func TestDefaultTaskSLO(t *testing.T) {
	slo := DefaultTaskSLO()
	if slo.TargetLatencyMS != 120_000 {
		t.Errorf("target latency = %d, want 120000", slo.TargetLatencyMS)
	}
	if slo.SuccessRateTarget != 0.95 {
		t.Errorf("success rate target = %f, want 0.95", slo.SuccessRateTarget)
	}
	if slo.ErrorBudgetFraction != 0.05 {
		t.Errorf("error budget fraction = %f, want 0.05", slo.ErrorBudgetFraction)
	}
}

// ── FailureClassCounts tests ───────────────────────────────────────────────────

func TestFailureClassCounts_Increment(t *testing.T) {
	var fc FailureClassCounts
	fc.increment(FailureTransient)
	fc.increment(FailureTransient)
	fc.increment(FailureProvider)
	fc.increment(FailurePermanent)
	fc.increment(FailureBudget)
	fc.increment(FailureSideEffect)
	fc.increment("unknown_class")

	if fc.Transient != 2 {
		t.Errorf("transient = %d, want 2", fc.Transient)
	}
	if fc.Provider != 1 || fc.Permanent != 1 || fc.Budget != 1 || fc.SideEffect != 1 {
		t.Error("unexpected class counts")
	}
	if fc.Unclassed != 1 {
		t.Errorf("unclassed = %d, want 1", fc.Unclassed)
	}
	if fc.Total() != 7 {
		t.Errorf("total = %d, want 7", fc.Total())
	}
}

// ── Collector: completion ──────────────────────────────────────────────────────

func TestCollector_RecordCompletion(t *testing.T) {
	c := NewTaskMetricsCollector()
	c.RecordCompletion(state.TaskRun{
		Status:    state.TaskRunStatusCompleted,
		Usage:     state.TaskUsage{WallClockMS: 4000},
		StartedAt: 100,
		EndedAt:   104,
	})
	snap := c.Snapshot()
	if snap.Completions != 1 {
		t.Errorf("completions = %d, want 1", snap.Completions)
	}
	if snap.Latency.Count != 1 {
		t.Errorf("latency count = %d, want 1", snap.Latency.Count)
	}
	if snap.Latency.BucketCounts["fast"] != 1 {
		t.Errorf("fast bucket = %d, want 1", snap.Latency.BucketCounts["fast"])
	}
	if snap.Latency.SumMS != 4000 {
		t.Errorf("sum = %d, want 4000", snap.Latency.SumMS)
	}
}

func TestCollector_RecordCompletion_FallbackLatency(t *testing.T) {
	c := NewTaskMetricsCollector()
	// WallClockMS is zero — should fall back to EndedAt-StartedAt.
	c.RecordCompletion(state.TaskRun{
		Status:    state.TaskRunStatusCompleted,
		StartedAt: 100,
		EndedAt:   130, // 30 seconds → 30000ms
	})
	snap := c.Snapshot()
	if snap.Latency.SumMS != 30000 {
		t.Errorf("sum = %d, want 30000 (fallback from timestamps)", snap.Latency.SumMS)
	}
	if snap.Latency.BucketCounts["normal"] != 1 {
		t.Errorf("normal bucket = %d, want 1", snap.Latency.BucketCounts["normal"])
	}
}

func TestCollector_RecordCompletion_NonCompleted(t *testing.T) {
	c := NewTaskMetricsCollector()
	// A "running" status shouldn't count as a completion.
	c.RecordCompletion(state.TaskRun{
		Status:    state.TaskRunStatusRunning,
		Usage:     state.TaskUsage{WallClockMS: 5000},
		StartedAt: 100,
	})
	snap := c.Snapshot()
	if snap.Completions != 0 {
		t.Errorf("completions = %d, want 0 (non-completed status)", snap.Completions)
	}
	if snap.ErrorBudget.TotalRuns != 1 {
		t.Errorf("total runs = %d, want 1", snap.ErrorBudget.TotalRuns)
	}
}

// ── Collector: failure ─────────────────────────────────────────────────────────

func TestCollector_RecordFailure(t *testing.T) {
	c := NewTaskMetricsCollector()
	c.RecordFailure(state.TaskRun{
		Status:    state.TaskRunStatusFailed,
		Usage:     state.TaskUsage{WallClockMS: 15000},
		StartedAt: 100,
		EndedAt:   115,
	}, FailureTransient)
	snap := c.Snapshot()
	if snap.Failures.Transient != 1 {
		t.Errorf("transient = %d, want 1", snap.Failures.Transient)
	}
	if snap.ErrorBudget.FailedRuns != 1 {
		t.Errorf("failed = %d, want 1", snap.ErrorBudget.FailedRuns)
	}
	if snap.Latency.BucketCounts["normal"] != 1 {
		t.Errorf("normal bucket = %d, want 1 (failures still record latency)", snap.Latency.BucketCounts["normal"])
	}
}

// ── Collector: retry / verification / exhaustion ───────────────────────────────

func TestCollector_RecordRetry(t *testing.T) {
	c := NewTaskMetricsCollector()
	c.RecordRetry()
	c.RecordRetry()
	if snap := c.Snapshot(); snap.RetryCount != 2 {
		t.Errorf("retries = %d, want 2", snap.RetryCount)
	}
}

func TestCollector_RecordVerification(t *testing.T) {
	c := NewTaskMetricsCollector()
	c.RecordVerification()
	if snap := c.Snapshot(); snap.Verifications != 1 {
		t.Errorf("verifications = %d, want 1", snap.Verifications)
	}
}

func TestCollector_RecordExhaustion(t *testing.T) {
	c := NewTaskMetricsCollector()
	c.RecordExhaustion(ExhaustionEvent{EventID: "e1"})
	c.RecordExhaustion(ExhaustionEvent{EventID: "e2"})
	snap := c.Snapshot()
	if snap.ErrorBudget.ExhaustionEvents != 2 {
		t.Errorf("exhaustion events = %d, want 2", snap.ErrorBudget.ExhaustionEvents)
	}
}

// ── Error budget computation ───────────────────────────────────────────────────

func TestErrorBudget_NoRuns(t *testing.T) {
	c := NewTaskMetricsCollector()
	snap := c.Snapshot()
	if snap.ErrorBudget.SuccessRate != 1.0 {
		t.Errorf("success rate = %f, want 1.0", snap.ErrorBudget.SuccessRate)
	}
	if snap.ErrorBudget.BudgetRemaining != 0.05 {
		t.Errorf("remaining = %f, want 0.05", snap.ErrorBudget.BudgetRemaining)
	}
	if snap.ErrorBudget.Exhausted {
		t.Error("should not be exhausted with no runs")
	}
}

func TestErrorBudget_WithinBudget(t *testing.T) {
	c := NewTaskMetricsCollector()
	// 19 successes, 1 failure → 95% success → 5% failure → budget remaining = 0
	for i := 0; i < 19; i++ {
		c.RecordCompletion(state.TaskRun{
			Status: state.TaskRunStatusCompleted,
			Usage:  state.TaskUsage{WallClockMS: 1000},
		})
	}
	c.RecordFailure(state.TaskRun{
		Status: state.TaskRunStatusFailed,
		Usage:  state.TaskUsage{WallClockMS: 1000},
	}, FailureTransient)

	snap := c.Snapshot()
	if math.Abs(snap.ErrorBudget.SuccessRate-0.95) > 0.001 {
		t.Errorf("success rate = %f, want ~0.95", snap.ErrorBudget.SuccessRate)
	}
	// 5% failure, 5% budget → remaining = 0
	if math.Abs(snap.ErrorBudget.BudgetRemaining) > 0.001 {
		t.Errorf("remaining = %f, want ~0", snap.ErrorBudget.BudgetRemaining)
	}
}

func TestErrorBudget_Exhausted(t *testing.T) {
	c := NewTaskMetricsCollector()
	// 8 successes, 2 failures → 80% success → 20% failure → exhausted
	for i := 0; i < 8; i++ {
		c.RecordCompletion(state.TaskRun{
			Status: state.TaskRunStatusCompleted,
			Usage:  state.TaskUsage{WallClockMS: 1000},
		})
	}
	c.RecordFailure(state.TaskRun{Status: state.TaskRunStatusFailed, Usage: state.TaskUsage{WallClockMS: 1000}}, FailurePermanent)
	c.RecordFailure(state.TaskRun{Status: state.TaskRunStatusFailed, Usage: state.TaskUsage{WallClockMS: 1000}}, FailureProvider)

	snap := c.Snapshot()
	if !snap.ErrorBudget.Exhausted {
		t.Error("should be exhausted with 20% failure rate")
	}
	if snap.ErrorBudget.BudgetRemaining >= 0 {
		t.Errorf("remaining = %f, should be negative", snap.ErrorBudget.BudgetRemaining)
	}
}

// ── P99 violations ─────────────────────────────────────────────────────────────

func TestP99Violations(t *testing.T) {
	c := NewTaskMetricsCollector()
	// Default SLO target = 120000ms.
	c.RecordCompletion(state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Usage:  state.TaskUsage{WallClockMS: 60000}, // within
	})
	c.RecordCompletion(state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Usage:  state.TaskUsage{WallClockMS: 200000}, // exceeds
	})
	snap := c.Snapshot()
	if snap.Latency.P99ViolationCount != 1 {
		t.Errorf("p99 violations = %d, want 1", snap.Latency.P99ViolationCount)
	}
}

// ── Mean latency ───────────────────────────────────────────────────────────────

func TestLatencyDistribution_MeanMS(t *testing.T) {
	d := LatencyDistribution{SumMS: 30000, Count: 3}
	if d.MeanMS() != 10000 {
		t.Errorf("mean = %d, want 10000", d.MeanMS())
	}
}

func TestLatencyDistribution_MeanMS_Empty(t *testing.T) {
	d := LatencyDistribution{}
	if d.MeanMS() != 0 {
		t.Errorf("mean = %d, want 0 for empty", d.MeanMS())
	}
}

// ── Registry projection ────────────────────────────────────────────────────────

func TestProjectToRegistry(t *testing.T) {
	c := NewTaskMetricsCollector()
	c.RecordCompletion(state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Usage:  state.TaskUsage{WallClockMS: 3000},
	})
	c.RecordFailure(state.TaskRun{
		Status: state.TaskRunStatusFailed,
		Usage:  state.TaskUsage{WallClockMS: 50000},
	}, FailureTransient)
	c.RecordRetry()
	c.RecordVerification()
	c.RecordExhaustion(ExhaustionEvent{})

	reg := metrics.NewRegistry()
	c.ProjectToRegistry(reg)

	expo := reg.Exposition()
	for _, want := range []string{
		"metiq_task_completions_total 1",
		"metiq_task_failures_total 1",
		"metiq_task_retries_total 1",
		"metiq_task_verifications_total 1",
		"metiq_task_failures_transient 1",
		"metiq_task_latency_fast_total 1",
		"metiq_task_latency_slow_total 1", // 50000ms > 30000ms boundary → slow
		"metiq_task_error_budget_remaining",
		"metiq_task_success_rate",
		"metiq_task_exhaustion_events_total 1",
		"metiq_task_latency_mean_ms",
	} {
		if !containsLine(expo, want) {
			t.Errorf("exposition missing %q\ngot:\n%s", want, expo)
		}
	}
}

func TestProjectToRegistry_IdempotentOnRepeatedCalls(t *testing.T) {
	c := NewTaskMetricsCollector()
	c.RecordCompletion(state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Usage:  state.TaskUsage{WallClockMS: 5000},
	})

	reg := metrics.NewRegistry()
	// Call twice — gauges should show the same value, not double.
	c.ProjectToRegistry(reg)
	c.ProjectToRegistry(reg)

	g := reg.Gauge("metiq_task_completions_total", "")
	if v := g.Value(); v != 1.0 {
		t.Errorf("after two projections, completions = %f, want 1 (gauges should be idempotent)", v)
	}
}

// ── Concurrency ────────────────────────────────────────────────────────────────

func TestCollector_ConcurrentAccess(t *testing.T) {
	c := NewTaskMetricsCollector()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			c.RecordCompletion(state.TaskRun{
				Status: state.TaskRunStatusCompleted,
				Usage:  state.TaskUsage{WallClockMS: 2000},
			})
		}()
		go func() {
			defer wg.Done()
			c.RecordFailure(state.TaskRun{
				Status: state.TaskRunStatusFailed,
				Usage:  state.TaskUsage{WallClockMS: 5000},
			}, FailureTransient)
		}()
		go func() {
			defer wg.Done()
			c.RecordRetry()
			c.RecordVerification()
		}()
		go func() {
			defer wg.Done()
			_ = c.Snapshot()
		}()
	}
	wg.Wait()

	snap := c.Snapshot()
	if snap.Completions+snap.Failures.Total() == 0 {
		t.Error("no data recorded during concurrent access")
	}
	if snap.ErrorBudget.TotalRuns != snap.Completions+snap.Failures.Total() {
		// totalRuns includes non-completed RecordCompletion calls too,
		// but all our completions have status=completed so this should match.
		t.Errorf("total runs = %d, completions+failures = %d",
			snap.ErrorBudget.TotalRuns, snap.Completions+snap.Failures.Total())
	}
}

// ── JSON round-trip ────────────────────────────────────────────────────────────

func TestTaskMetricsSnapshot_JSON(t *testing.T) {
	c := NewTaskMetricsCollector()
	c.RecordCompletion(state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Usage:  state.TaskUsage{WallClockMS: 10000},
	})
	snap := c.Snapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded TaskMetricsSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Completions != 1 {
		t.Errorf("decoded completions = %d, want 1", decoded.Completions)
	}
	if decoded.SLO.TargetLatencyMS != 120_000 {
		t.Errorf("decoded SLO target = %d, want 120000", decoded.SLO.TargetLatencyMS)
	}
}

// ── Format ─────────────────────────────────────────────────────────────────────

func TestFormatTaskMetrics(t *testing.T) {
	c := NewTaskMetricsCollector()
	c.RecordCompletion(state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Usage:  state.TaskUsage{WallClockMS: 2000},
	})
	c.RecordFailure(state.TaskRun{
		Status: state.TaskRunStatusFailed,
		Usage:  state.TaskUsage{WallClockMS: 10000},
	}, FailurePermanent)

	out := FormatTaskMetrics(c.Snapshot())
	for _, want := range []string{"Task Metrics", "Completions: 1", "Failures: 1", "permanent=1", "SLO:", "Error budget:"} {
		if !containsLine(out, want) {
			t.Errorf("format missing %q in:\n%s", want, out)
		}
	}
}

// ── Custom SLO ─────────────────────────────────────────────────────────────────

func TestCustomSLO(t *testing.T) {
	slo := TaskSLO{
		TargetLatencyMS:     5000,
		SuccessRateTarget:   0.99,
		ErrorBudgetFraction: 0.01,
	}
	buckets := []LatencyBucket{
		{UpperBoundMS: 1000, Label: "instant"},
		{UpperBoundMS: 5000, Label: "ok"},
		{UpperBoundMS: 0, Label: "too_slow"},
	}
	c := NewTaskMetricsCollectorWith(slo, buckets)
	c.RecordCompletion(state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Usage:  state.TaskUsage{WallClockMS: 800},
	})
	c.RecordCompletion(state.TaskRun{
		Status: state.TaskRunStatusCompleted,
		Usage:  state.TaskUsage{WallClockMS: 6000}, // exceeds 5000ms target
	})
	snap := c.Snapshot()
	if snap.Latency.BucketCounts["instant"] != 1 {
		t.Errorf("instant = %d, want 1", snap.Latency.BucketCounts["instant"])
	}
	if snap.Latency.BucketCounts["too_slow"] != 1 {
		t.Errorf("too_slow = %d, want 1", snap.Latency.BucketCounts["too_slow"])
	}
	if snap.Latency.P99ViolationCount != 1 {
		t.Errorf("p99 violations = %d, want 1", snap.Latency.P99ViolationCount)
	}
}

// ── Helper ─────────────────────────────────────────────────────────────────────

func containsLine(haystack, needle string) bool {
	return len(haystack) > 0 && len(needle) > 0 &&
		// Use contains rather than line-exact match for flexibility.
		(func() bool {
			for _, line := range splitLines(haystack) {
				if len(line) >= len(needle) {
					for i := 0; i <= len(line)-len(needle); i++ {
						if line[i:i+len(needle)] == needle {
							return true
						}
					}
				}
			}
			return false
		})()
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
