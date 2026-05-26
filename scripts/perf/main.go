package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"metiq/internal/agent/toolloop"
	policyrules "metiq/internal/policy/rules"
	"metiq/internal/trajectory"
)

type BenchResult struct {
	Name       string `json:"name"`
	Iterations int    `json:"iterations"`
	DurationNS int64  `json:"duration_ns"`
	PerOpNS    int64  `json:"per_op_ns"`
	Bytes      int    `json:"bytes,omitempty"`
}
type Report struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Results     []BenchResult `json:"results"`
}

func main() {
	jsonOut := flag.Bool("json", true, "emit JSON")
	iters := flag.Int("n", 1000, "iterations")
	flag.Parse()
	if *iters <= 0 {
		fmt.Fprintln(os.Stderr, "-n must be greater than zero")
		os.Exit(2)
	}
	report := Report{GeneratedAt: time.Now().UTC()}
	report.Results = append(report.Results, benchRuleEval(*iters), benchTrajectoryWrite(*iters), benchCompaction(*iters), benchRelayDispatch(*iters))
	if *jsonOut {
		raw, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(raw))
		return
	}
	for _, r := range report.Results {
		fmt.Printf("%s: %d ns/op (%d iterations)\n", r.Name, r.PerOpNS, r.Iterations)
	}
}

func benchRuleEval(n int) BenchResult {
	e, _ := policyrules.New([]policyrules.Rule{{ID: "r", Action: policyrules.ActionWarn, EventTypes: []policyrules.EventType{policyrules.EventBash}, Conditions: []policyrules.Condition{{Field: "command", Contains: "relay"}}}})
	start := time.Now()
	for i := 0; i < n; i++ {
		_ = e.Evaluate(policyrules.Event{Type: policyrules.EventBash, Command: "nostr relay publish"})
	}
	d := time.Since(start)
	return BenchResult{Name: "policy_rule_eval", Iterations: n, DurationNS: d.Nanoseconds(), PerOpNS: d.Nanoseconds() / int64(n)}
}

func benchTrajectoryWrite(n int) BenchResult {
	dir, _ := os.MkdirTemp("", "metiq-perf-traj-")
	defer os.RemoveAll(dir)
	r, _ := trajectory.NewRecorder(dir, "perf", trajectory.Metadata{Version: "perf"})
	defer r.Close()
	start := time.Now()
	for i := 0; i < n; i++ {
		_ = r.Record(trajectory.Event{Type: trajectory.EventToolResult, Payload: map[string]any{"i": i, "text": "ok"}})
	}
	d := time.Since(start)
	return BenchResult{Name: "trajectory_write", Iterations: n, DurationNS: d.Nanoseconds(), PerOpNS: d.Nanoseconds() / int64(n)}
}

func benchCompaction(n int) BenchResult {
	out := strings.Repeat("line noise\n", 2000) + "ERROR: sample failure\n" + strings.Repeat("tail\n", 1000)
	start := time.Now()
	var bytes int
	for i := 0; i < n; i++ {
		r := toolloop.CompactToolOutput("bash", out, toolloop.DefaultCompactionConfig())
		bytes = r.CompactedBytes
	}
	d := time.Since(start)
	return BenchResult{Name: "tokenjuice_compaction", Iterations: n, DurationNS: d.Nanoseconds(), PerOpNS: d.Nanoseconds() / int64(n), Bytes: bytes}
}

func benchRelayDispatch(n int) BenchResult {
	ch := make(chan int, 1024)
	done := make(chan struct{})
	start := time.Now()
	go func() {
		for i := 0; i < n; i++ {
			<-ch
		}
		close(done)
	}()
	for i := 0; i < n; i++ {
		ch <- i
	}
	<-done
	d := time.Since(start)
	return BenchResult{Name: "relay_subscription_dispatch_sim", Iterations: n, DurationNS: d.Nanoseconds(), PerOpNS: d.Nanoseconds() / int64(n)}
}
