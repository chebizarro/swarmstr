package acp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

// Step describes one task in a multi-agent pipeline.
type Step struct {
	// PeerPubKey is the Nostr pubkey of the target worker agent (hex, no-prefix).
	PeerPubKey string `json:"peer_pubkey"`
	// Instructions is the natural-language task text sent to the worker.
	Instructions string `json:"instructions"`
	// ContextMessages seeds the worker with prior parent history/context.
	ContextMessages []map[string]any `json:"context_messages,omitempty"`
	// MemoryScope carries the explicit worker memory scope contract.
	MemoryScope state.AgentMemoryScope `json:"memory_scope,omitempty"`
	// ToolProfile carries the inherited worker tool profile contract.
	ToolProfile string `json:"tool_profile,omitempty"`
	// EnabledTools carries an explicit inherited tool allowlist.
	EnabledTools []string `json:"enabled_tools,omitempty"`
	// ParentContext carries optional metadata about the originating runtime.
	ParentContext *ParentContext `json:"parent_context,omitempty"`
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
	// SenderPubKey is the worker pubkey that returned the result.
	SenderPubKey string `json:"sender_pubkey,omitempty"`
	// Worker carries structured worker-side completion/history metadata.
	Worker *WorkerMetadata `json:"worker,omitempty"`
	// TokensUsed is the top-level completion usage hint from the worker result.
	TokensUsed int `json:"tokens_used,omitempty"`
	// CompletedAt is the worker-reported completion timestamp.
	CompletedAt int64 `json:"completed_at,omitempty"`
}

// SendFunc is the callback that actually sends an ACP task DM.
// Callers inject this from the main daemon so the pipeline stays importable
// without direct dependencies on the Nostr runtime.
type SendFunc func(ctx context.Context, peerPubKey, taskID string, payload TaskPayload) error

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
		if err := send(ctx, step.PeerPubKey, taskID, TaskPayload{
			Instructions:    instructions,
			ContextMessages: cloneContextMessages(step.ContextMessages),
			MemoryScope:     step.MemoryScope,
			ToolProfile:     strings.TrimSpace(step.ToolProfile),
			EnabledTools:    cloneStrings(step.EnabledTools),
			ParentContext:   cloneParentContext(step.ParentContext),
			TimeoutMS:       step.TimeoutMS,
		}); err != nil {
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
			StepIndex: i, TaskID: taskID, Text: res.Text, Error: res.Error, SenderPubKey: res.SenderPubKey, Worker: cloneWorkerMetadata(res.Worker), TokensUsed: res.TokensUsed, CompletedAt: res.CompletedAt,
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
		if err := send(ctx, step.PeerPubKey, taskID, TaskPayload{
			Instructions:    step.Instructions,
			ContextMessages: cloneContextMessages(step.ContextMessages),
			MemoryScope:     step.MemoryScope,
			ToolProfile:     strings.TrimSpace(step.ToolProfile),
			EnabledTools:    cloneStrings(step.EnabledTools),
			ParentContext:   cloneParentContext(step.ParentContext),
			TimeoutMS:       step.TimeoutMS,
		}); err != nil {
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
				results[i] = PipelineResult{StepIndex: i, TaskID: taskID, Text: res.Text, Error: res.Error, SenderPubKey: res.SenderPubKey, Worker: cloneWorkerMetadata(res.Worker), TokensUsed: res.TokensUsed, CompletedAt: res.CompletedAt}
				if res.Error != "" && firstErr == nil {
					firstErr = fmt.Errorf("pipeline step %d worker error: %s", i, res.Error)
				}
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

func cloneStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	copy(out, items)
	return out
}

func cloneContextMessages(items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		cp := make(map[string]any, len(item))
		for k, v := range item {
			cp[k] = v
		}
		out = append(out, cp)
	}
	return out
}

func cloneParentContext(parent *ParentContext) *ParentContext {
	if parent == nil {
		return nil
	}
	cp := *parent
	return &cp
}

func cloneWorkerMetadata(worker *WorkerMetadata) *WorkerMetadata {
	if worker == nil {
		return nil
	}
	cp := &WorkerMetadata{
		SessionID:       worker.SessionID,
		AgentID:         worker.AgentID,
		ParentContext:   cloneParentContext(worker.ParentContext),
		HistoryEntryIDs: cloneStrings(worker.HistoryEntryIDs),
	}
	if worker.TurnResult != nil {
		turnResult := *worker.TurnResult
		cp.TurnResult = &turnResult
	}
	return cp
}
