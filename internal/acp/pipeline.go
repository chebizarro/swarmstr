package acp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Step describes one task in a multi-agent pipeline.
type Step struct {
	// PeerPubKey is the Nostr pubkey of the target worker agent (hex, no-prefix).
	PeerPubKey string `json:"peer_pubkey"`
	// Instructions is the natural-language task text sent to the worker.
	Instructions string `json:"instructions"`
	// TimeoutMS is the per-step timeout in milliseconds.  0 = 60 s default.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

// PipelineResult captures the outcome of a single pipeline step.
type PipelineResult struct {
	// StepIndex is the 0-based index of the step.
	StepIndex int `json:"step_index"`
	// TaskID is the ACP task identifier.
	TaskID string `json:"task_id"`
	// Text is the worker's response text (empty on error).
	Text string `json:"text,omitempty"`
	// Error is set when the worker reported an error or the step timed out.
	Error string `json:"error,omitempty"`
}

// SendFunc is the callback that actually sends an ACP task DM.
// Callers inject this from the main daemon so the pipeline stays importable
// without direct dependencies on the Nostr runtime.
type SendFunc func(ctx context.Context, peerPubKey, instructions, taskID string) error

// Pipeline orchestrates a sequence of ACP sub-tasks.
type Pipeline struct {
	Steps []Step
}

// stepTimeout returns the effective per-step deadline.
func stepTimeout(ms int64) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return 60 * time.Second
}

// RunSequential dispatches each step in order, feeding the previous step's
// result as context to the next step's instructions.
// It blocks until all steps complete or the context is cancelled.
func (p *Pipeline) RunSequential(ctx context.Context, d *Dispatcher, send SendFunc) ([]PipelineResult, error) {
	results := make([]PipelineResult, 0, len(p.Steps))
	var prevResult string

	for i, step := range p.Steps {
		taskID := GenerateTaskID()

		// Optionally prepend previous result as context.
		instructions := step.Instructions
		if prevResult != "" {
			instructions = "[Previous result]\n" + prevResult + "\n\n[New task]\n" + instructions
		}

		ch := d.Register(taskID)
		if err := send(ctx, step.PeerPubKey, instructions, taskID); err != nil {
			d.Cancel(taskID)
			return results, fmt.Errorf("pipeline step %d send: %w", i, err)
		}

		res, err := d.Wait(ctx, taskID, stepTimeout(step.TimeoutMS))
		_ = ch // ch was consumed by Wait
		if err != nil {
			results = append(results, PipelineResult{
				StepIndex: i, TaskID: taskID, Error: err.Error(),
			})
			return results, fmt.Errorf("pipeline step %d: %w", i, err)
		}

		results = append(results, PipelineResult{
			StepIndex: i, TaskID: taskID, Text: res.Text, Error: res.Error,
		})
		if res.Error != "" {
			return results, fmt.Errorf("pipeline step %d worker error: %s", i, res.Error)
		}
		prevResult = res.Text
	}
	return results, nil
}

// RunParallel dispatches all steps concurrently and collects results.
// Steps do not share context between them in parallel mode.
// The returned slice has the same length as p.Steps; results are in order.
func (p *Pipeline) RunParallel(ctx context.Context, d *Dispatcher, send SendFunc) ([]PipelineResult, error) {
	results := make([]PipelineResult, len(p.Steps))
	taskIDs := make([]string, len(p.Steps))

	// Register and dispatch all tasks.
	for i, step := range p.Steps {
		taskID := GenerateTaskID()
		taskIDs[i] = taskID
		d.Register(taskID)
		if err := send(ctx, step.PeerPubKey, step.Instructions, taskID); err != nil {
			// Cancel all already-registered sibling tasks on send failure.
			for j := 0; j <= i; j++ {
				if taskIDs[j] != "" {
					d.Cancel(taskIDs[j])
				}
			}
			return nil, fmt.Errorf("pipeline step %d send: %w", i, err)
		}
	}

	// Wait for all results concurrently.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, step := range p.Steps {
		i, step, taskID := i, step, taskIDs[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := d.Wait(ctx, taskID, stepTimeout(step.TimeoutMS))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[i] = PipelineResult{StepIndex: i, TaskID: taskID, Error: err.Error()}
				if firstErr == nil {
					firstErr = err
				}
			} else {
				results[i] = PipelineResult{StepIndex: i, TaskID: taskID, Text: res.Text, Error: res.Error}
			}
		}()
	}
	wg.Wait()
	return results, firstErr
}

// AggregateResults joins all step texts into a single string, separated by a
// double newline, skipping steps with errors.
func AggregateResults(results []PipelineResult) string {
	var parts []string
	for _, r := range results {
		if r.Error == "" && strings.TrimSpace(r.Text) != "" {
			parts = append(parts, strings.TrimSpace(r.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}
