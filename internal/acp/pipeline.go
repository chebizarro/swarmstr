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
	// Task carries the canonical machine-readable task contract for this step.
	Task *state.TaskSpec `json:"task,omitempty"`
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
	// Artifacts are typed inputs made available to this step.
	Artifacts []ArtifactPayload `json:"artifacts,omitempty"`
	// Label is an optional human-readable step name for task records.
	Label string `json:"label,omitempty"`
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
	// Artifacts are typed outputs produced by this step.
	Artifacts []ArtifactPayload `json:"artifacts,omitempty"`
}

// SendFunc is the callback that actually sends an ACP task DM.
// Callers inject this from the main daemon so the pipeline stays importable
// without direct dependencies on the Nostr runtime.
type SendFunc func(ctx context.Context, peerPubKey, taskID string, payload TaskPayload) error

// Pipeline orchestrates a sequence of ACP sub-tasks.
type Pipeline struct {
	Steps []Step `json:"steps"`
	// FlowRegistry, when set, records pipeline orchestration state with
	// wait/resume/block-capable FlowRecord semantics.
	FlowRegistry *FlowRegistry `json:"-"`
	// FlowID optionally reuses an existing flow record; otherwise one is created.
	FlowID string `json:"flow_id,omitempty"`
	// OwnerSessionKey identifies the parent/supervisor session for flow queries.
	OwnerSessionKey string `json:"owner_session_key,omitempty"`
	// Goal describes the pipeline objective in the flow record.
	Goal string `json:"goal,omitempty"`
	// MaxConcurrency bounds simultaneously dispatched remote steps. Values <= 0
	// use the safe default of four.
	MaxConcurrency int `json:"max_concurrency,omitempty"`
	// RemoteCancel publishes an authenticated cancellation event to a worker.
	RemoteCancel RemoteCancelFunc `json:"-"`
}

// stepTimeout returns the effective per-step deadline.
const defaultPipelineMaxConcurrency = 4

func (p *Pipeline) maxConcurrency() int {
	if p != nil && p.MaxConcurrency > 0 {
		return p.MaxConcurrency
	}
	return defaultPipelineMaxConcurrency
}

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
	flowID, _ := p.ensureFlow(ctx)
	var prevResult string
	var prevArtifacts []ArtifactPayload
	var allArtifacts []ArtifactPayload

	for i, step := range p.Steps {
		taskID := GenerateTaskID()
		p.markFlowRunning(ctx, flowID, i)

		// Optionally prepend previous result as legacy text context while also
		// passing it as a typed artifact for ACP-aware workers.
		instructions := step.Instructions
		if prevResult != "" {
			instructions = "[Previous result]\n" + prevResult + "\n\n[New task]\n" + instructions
		}
		stepArtifacts := append(cloneArtifacts(prevArtifacts), cloneArtifacts(step.Artifacts)...)

		if step.Task != nil && strings.TrimSpace(step.Task.TaskID) != "" {
			taskID = strings.TrimSpace(step.Task.TaskID)
		}
		ch, err := d.RegisterTaskWithError(ctx, TaskRecord{TaskID: taskID, FlowID: flowID, StepIndex: i, RequesterSessionKey: p.OwnerSessionKey, Instructions: instructions, Label: step.Label, Worker: &WorkerTaskMetadata{PubKey: step.PeerPubKey}, Artifacts: stepArtifacts})
		if err != nil {
			p.blockFlow(ctx, flowID, taskID, err.Error())
			return results, fmt.Errorf("pipeline step %d register: %w", i, err)
		}
		p.appendFlowTask(ctx, flowID, taskID)
		if err := send(ctx, step.PeerPubKey, taskID, TaskPayload{
			Instructions:    instructions,
			Task:            step.Task,
			ContextMessages: cloneContextMessages(step.ContextMessages),
			MemoryScope:     step.MemoryScope,
			ToolProfile:     strings.TrimSpace(step.ToolProfile),
			EnabledTools:    cloneStrings(step.EnabledTools),
			ParentContext:   cloneParentContext(step.ParentContext),
			TimeoutMS:       step.TimeoutMS,
			Artifacts:       stepArtifacts,
		}); err != nil {
			d.Cancel(taskID)
			p.blockFlow(ctx, flowID, taskID, err.Error())
			return results, fmt.Errorf("pipeline step %d send: %w", i, err)
		}
		d.MarkRunning(ctx, taskID)

		res, err := d.WaitWithRemoteCancel(ctx, taskID, stepTimeout(step.TimeoutMS), p.RemoteCancel)
		_ = ch // ch was consumed by Wait
		if err != nil {
			results = append(results, PipelineResult{
				StepIndex: i, TaskID: taskID, Error: err.Error(),
			})
			p.blockFlow(ctx, flowID, taskID, err.Error())
			return results, fmt.Errorf("pipeline step %d: %w", i, err)
		}

		artifacts := resultArtifacts(res)
		results = append(results, PipelineResult{
			StepIndex: i, TaskID: taskID, Text: res.Text, Error: res.Error, SenderPubKey: res.SenderPubKey, Worker: cloneWorkerMetadata(res.Worker), TokensUsed: res.TokensUsed, CompletedAt: res.CompletedAt, Artifacts: artifacts,
		})
		if res.Error != "" {
			p.blockFlow(ctx, flowID, taskID, res.Error)
			return results, fmt.Errorf("pipeline step %d worker error: %s", i, res.Error)
		}
		prevResult = res.Text
		prevArtifacts = artifacts
		allArtifacts = append(allArtifacts, cloneArtifacts(artifacts)...)
	}
	p.finishFlow(ctx, flowID, allArtifacts)
	return results, nil
}

// RunParallel dispatches all steps concurrently and collects results.
// Steps do not share context between them in parallel mode.
// The returned slice has the same length as p.Steps; results are in order.
func (p *Pipeline) RunParallel(ctx context.Context, d *Dispatcher, send SendFunc) ([]PipelineResult, error) {
	results := make([]PipelineResult, len(p.Steps))
	taskIDs := make([]string, len(p.Steps))
	for i, step := range p.Steps {
		taskIDs[i] = GenerateTaskID()
		if step.Task != nil && strings.TrimSpace(step.Task.TaskID) != "" {
			taskIDs[i] = strings.TrimSpace(step.Task.TaskID)
		}
	}
	flowID, _ := p.ensureFlow(ctx)
	p.markFlowRunning(ctx, flowID, 0)

	parallelCtx, cancelPipeline := context.WithCancelCause(ctx)
	defer cancelPipeline(nil)
	semaphore := make(chan struct{}, p.maxConcurrency())
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	setFirstError := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		first := firstErr == nil
		if first {
			firstErr = err
		}
		errMu.Unlock()
		if first {
			cancelPipeline(err)
		}
	}

	for i, step := range p.Steps {
		i, step, taskID := i, step, taskIDs[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-parallelCtx.Done():
				err := context.Cause(parallelCtx)
				results[i] = PipelineResult{StepIndex: i, TaskID: taskID, Error: err.Error()}
				setFirstError(err)
				return
			}
			if err := context.Cause(parallelCtx); err != nil {
				results[i] = PipelineResult{StepIndex: i, TaskID: taskID, Error: err.Error()}
				setFirstError(err)
				return
			}

			stepArtifacts := cloneArtifacts(step.Artifacts)
			if _, err := d.RegisterTaskWithError(parallelCtx, TaskRecord{TaskID: taskID, FlowID: flowID, StepIndex: i, RequesterSessionKey: p.OwnerSessionKey, Instructions: step.Instructions, Label: step.Label, Worker: &WorkerTaskMetadata{PubKey: step.PeerPubKey}, Artifacts: stepArtifacts}); err != nil {
				err = fmt.Errorf("pipeline step %d register: %w", i, err)
				results[i] = PipelineResult{StepIndex: i, TaskID: taskID, Error: err.Error()}
				setFirstError(err)
				return
			}
			p.appendFlowTask(parallelCtx, flowID, taskID)
			if err := send(parallelCtx, step.PeerPubKey, taskID, TaskPayload{
				Instructions:    step.Instructions,
				Task:            step.Task,
				ContextMessages: cloneContextMessages(step.ContextMessages),
				MemoryScope:     step.MemoryScope,
				ToolProfile:     strings.TrimSpace(step.ToolProfile),
				EnabledTools:    cloneStrings(step.EnabledTools),
				ParentContext:   cloneParentContext(step.ParentContext),
				TimeoutMS:       step.TimeoutMS,
				Artifacts:       stepArtifacts,
			}); err != nil {
				d.Cancel(taskID)
				err = fmt.Errorf("pipeline step %d send: %w", i, err)
				results[i] = PipelineResult{StepIndex: i, TaskID: taskID, Error: err.Error()}
				setFirstError(err)
				return
			}
			d.MarkRunning(parallelCtx, taskID)

			res, err := d.WaitWithRemoteCancel(parallelCtx, taskID, stepTimeout(step.TimeoutMS), p.RemoteCancel)
			if err != nil {
				results[i] = PipelineResult{StepIndex: i, TaskID: taskID, Error: err.Error()}
				setFirstError(err)
				return
			}
			artifacts := resultArtifacts(res)
			results[i] = PipelineResult{StepIndex: i, TaskID: taskID, Text: res.Text, Error: res.Error, SenderPubKey: res.SenderPubKey, Worker: cloneWorkerMetadata(res.Worker), TokensUsed: res.TokensUsed, CompletedAt: res.CompletedAt, Artifacts: artifacts}
			if res.Error != "" {
				setFirstError(fmt.Errorf("pipeline step %d worker error: %s", i, res.Error))
			}
		}()
	}
	wg.Wait()
	errMu.Lock()
	err := firstErr
	errMu.Unlock()
	if err != nil {
		p.failFlow(ctx, flowID, err.Error())
	} else {
		var allArtifacts []ArtifactPayload
		for _, result := range results {
			allArtifacts = append(allArtifacts, cloneArtifacts(result.Artifacts)...)
		}
		p.finishFlow(ctx, flowID, allArtifacts)
	}
	return results, err
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

func (p *Pipeline) ensureFlow(ctx context.Context) (string, error) {
	if p == nil || p.FlowRegistry == nil {
		return "", nil
	}
	flowID := strings.TrimSpace(p.FlowID)
	if flowID != "" {
		if rec, err := p.FlowRegistry.Get(ctx, flowID); err != nil {
			return "", err
		} else if rec != nil {
			return flowID, nil
		}
	} else {
		flowID = GenerateFlowID()
		p.FlowID = flowID
	}
	_, err := p.FlowRegistry.Create(ctx, FlowRecord{FlowID: flowID, OwnerSessionKey: p.OwnerSessionKey, Goal: p.Goal, Status: FlowStatusQueued})
	return flowID, err
}

func (p *Pipeline) markFlowRunning(ctx context.Context, flowID string, step int) {
	if p != nil && p.FlowRegistry != nil && strings.TrimSpace(flowID) != "" {
		_, _ = p.FlowRegistry.Start(ctx, flowID, step)
	}
}

func (p *Pipeline) appendFlowTask(ctx context.Context, flowID, taskID string) {
	if p != nil && p.FlowRegistry != nil && strings.TrimSpace(flowID) != "" {
		_, _ = p.FlowRegistry.AppendTask(ctx, flowID, taskID)
	}
}

func (p *Pipeline) blockFlow(ctx context.Context, flowID, taskID, summary string) {
	if p != nil && p.FlowRegistry != nil && strings.TrimSpace(flowID) != "" {
		_, _ = p.FlowRegistry.Block(ctx, flowID, taskID, summary)
	}
}

func (p *Pipeline) failFlow(ctx context.Context, flowID, summary string) {
	if p != nil && p.FlowRegistry != nil && strings.TrimSpace(flowID) != "" {
		_, _ = p.FlowRegistry.Fail(ctx, flowID, summary)
	}
}

func (p *Pipeline) finishFlow(ctx context.Context, flowID string, artifacts []ArtifactPayload) {
	if p != nil && p.FlowRegistry != nil && strings.TrimSpace(flowID) != "" {
		_, _ = p.FlowRegistry.Finish(ctx, flowID, artifacts)
	}
}

func resultArtifacts(res TaskResult) []ArtifactPayload {
	if len(res.Artifacts) > 0 {
		return cloneArtifacts(res.Artifacts)
	}
	if strings.TrimSpace(res.Text) == "" {
		return nil
	}
	artifact := ArtifactPayload{Type: "text", Text: res.Text}.Normalize()
	return []ArtifactPayload{artifact}
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
		TaskID:          worker.TaskID,
		RunID:           worker.RunID,
		SessionID:       worker.SessionID,
		AgentID:         worker.AgentID,
		ParentTaskID:    worker.ParentTaskID,
		ParentRunID:     worker.ParentRunID,
		ParentContext:   cloneParentContext(worker.ParentContext),
		HistoryEntryIDs: cloneStrings(worker.HistoryEntryIDs),
		Result:          worker.Result,
		TransportUsed:   worker.TransportUsed,
	}
	if worker.TurnResult != nil {
		turnResult := *worker.TurnResult
		cp.TurnResult = &turnResult
	}
	return cp
}
