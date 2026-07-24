package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	ctxengine "metiq/internal/context"
	pluginhooks "metiq/internal/plugins/hooks"
	pluginregistry "metiq/internal/plugins/registry"
	"metiq/internal/skills"
)

var (
	ErrDepthLimit       = errors.New("subagent nesting depth limit reached")
	ErrConcurrencyLimit = errors.New("subagent concurrency limit reached")
	ErrBudgetExceeded   = errors.New("subagent token budget exceeded")
)

// Budget constrains one child run. Zero fields mean no limit.
type Budget struct {
	MaxInputTokens  int64 `json:"max_input_tokens,omitempty"`
	MaxOutputTokens int64 `json:"max_output_tokens,omitempty"`
	MaxTotalTokens  int64 `json:"max_total_tokens,omitempty"`
}

func (b Budget) exceeded(usage agent.TurnUsage) bool {
	return (b.MaxInputTokens > 0 && usage.InputTokens > b.MaxInputTokens) ||
		(b.MaxOutputTokens > 0 && usage.OutputTokens > b.MaxOutputTokens) ||
		(b.MaxTotalTokens > 0 && usage.InputTokens+usage.OutputTokens > b.MaxTotalTokens)
}

func (b Budget) prepareTurn(turn *agent.Turn) (int64, error) {
	if turn == nil {
		return 0, errors.New("subagent turn is nil")
	}
	inputEstimate := int64(agent.EstimateTurnTokens(*turn))
	if (b.MaxInputTokens > 0 && inputEstimate > b.MaxInputTokens) ||
		(b.MaxTotalTokens > 0 && inputEstimate >= b.MaxTotalTokens) {
		return inputEstimate, ErrBudgetExceeded
	}
	outputLimit := b.MaxOutputTokens
	if b.MaxTotalTokens > 0 {
		remaining := b.MaxTotalTokens - inputEstimate
		if outputLimit <= 0 || remaining < outputLimit {
			outputLimit = remaining
		}
	}
	if outputLimit > 0 {
		maxInt := int64(^uint(0) >> 1)
		if outputLimit > maxInt {
			outputLimit = maxInt
		}
		turn.MaxOutputTokens = int(outputLimit)
	}
	return inputEstimate, nil
}

type budgetTracker struct {
	mu             sync.Mutex
	budget         Budget
	inputEstimate  int64
	callbackOutput int64
	eventOutput    int64
	providerUsage  agent.TurnUsage
}

func newBudgetTracker(budget Budget, inputEstimate int64) *budgetTracker {
	return &budgetTracker{budget: budget, inputEstimate: inputEstimate}
}

func (t *budgetTracker) observeCallback(text string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callbackOutput += int64(ctxengine.EstimateTextTokens(text))
	return t.budget.exceeded(t.usageLocked())
}

func (t *budgetTracker) observeEvent(event agent.RuntimeEvent) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if event.Type == agent.RuntimeEventAssistantDelta || event.Type == agent.RuntimeEventThinkingDelta {
		t.eventOutput += int64(ctxengine.EstimateTextTokens(event.Delta))
	}
	if event.Usage.InputTokens > t.providerUsage.InputTokens {
		t.providerUsage.InputTokens = event.Usage.InputTokens
	}
	if event.Usage.OutputTokens > t.providerUsage.OutputTokens {
		t.providerUsage.OutputTokens = event.Usage.OutputTokens
	}
	return t.budget.exceeded(t.usageLocked())
}

func (t *budgetTracker) observeResult(usage agent.TurnUsage) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if usage.InputTokens > t.providerUsage.InputTokens {
		t.providerUsage.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens > t.providerUsage.OutputTokens {
		t.providerUsage.OutputTokens = usage.OutputTokens
	}
	return t.budget.exceeded(t.usageLocked())
}

func (t *budgetTracker) usageLocked() agent.TurnUsage {
	usage := t.providerUsage
	if usage.InputTokens < t.inputEstimate {
		usage.InputTokens = t.inputEstimate
	}
	streamedOutput := t.callbackOutput
	if t.eventOutput > streamedOutput {
		streamedOutput = t.eventOutput
	}
	if usage.OutputTokens < streamedOutput {
		usage.OutputTokens = streamedOutput
	}
	return usage
}

// AgentDefinition is a typed, spawnable agent runtime.
type AgentDefinition struct {
	ID            string
	Description   string
	Runtime       agent.Runtime
	DefaultBudget Budget
	// SkillKeys explicitly associates this typed subagent with catalog skills.
	SkillKeys []string
	Metadata  map[string]any
}

// Config controls nesting, global concurrency, per-parent fanout, and stream buffering.
type Config struct {
	MaxDepth             int
	MaxConcurrent        int
	MaxChildrenPerParent int
	EventBuffer          int
}

func DefaultConfig() Config {
	return Config{MaxDepth: 3, MaxConcurrent: 8, MaxChildrenPerParent: 4, EventBuffer: 256}
}

// SpawnRequest describes one child run.
type SpawnRequest struct {
	AgentID          string
	ParentAgentID    string
	ParentRunID      string
	ParentSessionID  string
	Task             string
	Budget           Budget
	HookInvoker      *pluginhooks.HookInvoker
	RuntimeEventSink agent.RuntimeEventSink
}

type EventType string

const (
	EventStarted   EventType = "started"
	EventRuntime   EventType = "runtime"
	EventCompleted EventType = "completed"
	EventFailed    EventType = "failed"
)

// Event is the parent-facing child stream.
type Event struct {
	Type         EventType           `json:"type"`
	RunID        string              `json:"run_id"`
	AgentID      string              `json:"agent_id"`
	SessionID    string              `json:"session_id"`
	RuntimeEvent *agent.RuntimeEvent `json:"runtime_event,omitempty"`
	Result       *agent.TurnResult   `json:"result,omitempty"`
	Error        string              `json:"error,omitempty"`
}

// Result is the terminal child outcome.
type Result struct {
	RunID     string
	AgentID   string
	SessionID string
	Turn      agent.TurnResult
	Err       error
}

// Handle controls and observes a spawned child.
type Handle struct {
	RunID  string
	Events <-chan Event

	cancel context.CancelCauseFunc
	done   <-chan struct{}
	state  *handleState
}

type handleState struct {
	mu     sync.RWMutex
	result Result
}

func (h *Handle) Cancel() {
	if h != nil && h.cancel != nil {
		h.cancel(context.Canceled)
	}
}

// Wait blocks on explicit completion or caller cancellation; it never polls.
func (h *Handle) Wait(ctx context.Context) (Result, error) {
	if h == nil || h.state == nil || h.done == nil {
		return Result{}, errors.New("invalid subagent handle")
	}
	select {
	case <-ctx.Done():
		return Result{}, context.Cause(ctx)
	case <-h.done:
		h.state.mu.RLock()
		result := h.state.result
		h.state.mu.RUnlock()
		return result, result.Err
	}
}

// Orchestrator owns typed definitions and active child runs.
type Orchestrator struct {
	mu           sync.Mutex
	config       Config
	registry     *Registry
	definitions  map[string]AgentDefinition
	active       int
	activeParent map[string]int
}

func NewOrchestrator(registry *Registry, config Config) *Orchestrator {
	defaults := DefaultConfig()
	if config.MaxDepth <= 0 {
		config.MaxDepth = defaults.MaxDepth
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = defaults.MaxConcurrent
	}
	if config.MaxChildrenPerParent <= 0 {
		config.MaxChildrenPerParent = defaults.MaxChildrenPerParent
	}
	if config.EventBuffer <= 0 {
		config.EventBuffer = defaults.EventBuffer
	}
	if registry == nil {
		registry = NewRegistry()
	}
	return &Orchestrator{config: config, registry: registry, definitions: make(map[string]AgentDefinition), activeParent: make(map[string]int)}
}

func (o *Orchestrator) RegisterDefinition(def AgentDefinition) error {
	if o == nil {
		return errors.New("orchestrator is nil")
	}
	def.ID = strings.TrimSpace(def.ID)
	def.SkillKeys = normalizedSkillKeys(def.SkillKeys)
	if def.ID == "" || def.Runtime == nil {
		return errors.New("subagent definition requires id and runtime")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, exists := o.definitions[def.ID]; exists {
		return fmt.Errorf("subagent definition %q already registered", def.ID)
	}
	o.definitions[def.ID] = def
	return nil
}

// ResolveSkillAgentDefinitions returns spawnable typed agents explicitly
// associated with an eligible catalog skill. Catalog loading remains owned by
// internal/skills; the orchestrator only consumes its resolved, read-only view.
func (o *Orchestrator) ResolveSkillAgentDefinitions(catalog *skills.SkillCatalog, skillKey string) []AgentDefinition {
	if o == nil || catalog == nil {
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(skillKey))
	if key == "" {
		return nil
	}
	eligible := false
	for _, resolved := range catalog.Skills {
		if resolved == nil || resolved.Skill == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(resolved.Skill.SkillKey), key) && (resolved.Eligible || resolved.PromptEligible) {
			eligible = true
			break
		}
	}
	if !eligible {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []AgentDefinition
	for _, def := range o.definitions {
		keys := def.SkillKeys
		if len(keys) == 0 {
			keys = skillKeysFromMetadata(def.Metadata)
		}
		for _, candidate := range keys {
			if candidate == key {
				out = append(out, def)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func normalizedSkillKeys(keys []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, key := range keys {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func skillKeysFromMetadata(metadata map[string]any) []string {
	if metadata == nil {
		return nil
	}
	switch raw := metadata["skills"].(type) {
	case []string:
		return normalizedSkillKeys(raw)
	case []any:
		keys := make([]string, 0, len(raw))
		for _, value := range raw {
			if key, ok := value.(string); ok {
				keys = append(keys, key)
			}
		}
		return normalizedSkillKeys(keys)
	case string:
		return normalizedSkillKeys([]string{raw})
	default:
		return nil
	}
}

func (o *Orchestrator) Spawn(ctx context.Context, req SpawnRequest) (*Handle, error) {
	if o == nil {
		return nil, errors.New("orchestrator is nil")
	}
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.Task = strings.TrimSpace(req.Task)
	if req.AgentID == "" || req.Task == "" {
		return nil, errors.New("subagent id and task are required")
	}

	o.mu.Lock()
	def, ok := o.definitions[req.AgentID]
	if !ok {
		o.mu.Unlock()
		return nil, fmt.Errorf("subagent definition %q not found", req.AgentID)
	}
	depth := 1
	parentKey := strings.TrimSpace(req.ParentRunID)
	if parentKey != "" {
		parent := o.registry.Get(parentKey)
		if parent == nil {
			o.mu.Unlock()
			return nil, fmt.Errorf("parent subagent run %q not found", parentKey)
		}
		depth = parent.Depth + 1
		if req.ParentAgentID == "" {
			req.ParentAgentID = parent.AgentID
		}
	}
	if depth > o.config.MaxDepth {
		o.mu.Unlock()
		return nil, ErrDepthLimit
	}
	if o.active >= o.config.MaxConcurrent || (parentKey != "" && o.activeParent[parentKey] >= o.config.MaxChildrenPerParent) {
		o.mu.Unlock()
		return nil, ErrConcurrencyLimit
	}
	o.active++
	if parentKey != "" {
		o.activeParent[parentKey]++
	}
	o.mu.Unlock()

	if err := emitSpawningHook(ctx, req, depth); err != nil {
		o.release(parentKey)
		return nil, err
	}

	runID := newRunID()
	sessionID := childSessionID(req.ParentSessionID, req.AgentID, runID)
	budget := mergeBudget(def.DefaultBudget, req.Budget)
	record := SubagentRunRecord{RunID: runID, ParentRunID: parentKey, AgentID: req.AgentID, ParentAgentID: req.ParentAgentID, Depth: depth, Budget: budget, ChildSessionKey: sessionID, RequesterSessionKey: req.ParentSessionID, Task: req.Task, Cleanup: "keep", StartedAt: time.Now().UnixMilli()}
	if err := o.registry.Register(record); err != nil {
		o.release(parentKey)
		return nil, err
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	events := make(chan Event, o.config.EventBuffer)
	done := make(chan struct{})
	state := &handleState{}
	handle := &Handle{RunID: runID, Events: events, cancel: cancel, done: done, state: state}
	emitSpawnedHook(ctx, req, runID, sessionID)

	go o.run(runCtx, cancel, def, req, record, events, done, state)
	return handle, nil
}

func (o *Orchestrator) run(ctx context.Context, cancel context.CancelCauseFunc, def AgentDefinition, req SpawnRequest, record SubagentRunRecord, events chan<- Event, done chan<- struct{}, state *handleState) {
	defer close(done)
	defer close(events)
	defer o.release(record.ParentRunID)
	defer cancel(nil)

	turn := agent.Turn{SessionID: record.ChildSessionKey, TurnID: record.RunID, UserText: record.Task, HookInvoker: req.HookInvoker, ToolPolicyAgentID: record.AgentID}
	inputEstimate, preflightErr := record.Budget.prepareTurn(&turn)
	tracker := newBudgetTracker(record.Budget, inputEstimate)
	turn.RuntimeEventSink = func(runtimeEvent agent.RuntimeEvent) {
		if req.RuntimeEventSink != nil {
			req.RuntimeEventSink(runtimeEvent)
		}
		copy := runtimeEvent
		emitChildEvent(ctx, events, Event{Type: EventRuntime, RunID: record.RunID, AgentID: record.AgentID, SessionID: record.ChildSessionKey, RuntimeEvent: &copy})
		if tracker.observeEvent(runtimeEvent) {
			cancel(ErrBudgetExceeded)
		}
	}

	var turnResult agent.TurnResult
	err := preflightErr
	if err == nil {
		emitChildEvent(ctx, events, Event{Type: EventStarted, RunID: record.RunID, AgentID: record.AgentID, SessionID: record.ChildSessionKey})
		if streaming, ok := def.Runtime.(agent.StreamingRuntime); ok {
			turnResult, err = streaming.ProcessTurnStreaming(ctx, turn, func(text string) {
				if tracker.observeCallback(text) {
					cancel(ErrBudgetExceeded)
				}
			})
		} else {
			turnResult, err = def.Runtime.ProcessTurn(ctx, turn)
		}
	}
	if err == nil && tracker.observeResult(turnResult.Usage) {
		err = ErrBudgetExceeded
	}
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	}

	status := "ok"
	eventType := EventCompleted
	if err != nil {
		status = "error"
		eventType = EventFailed
	}
	_ = o.registry.End(record.RunID, RunOutcome{Status: status, Error: errorString(err)})
	if providerRuntime, ok := def.Runtime.(*agent.ProviderRuntime); ok {
		providerRuntime.EndSession(context.WithoutCancel(ctx), record.ChildSessionKey, record.AgentID, status, req.HookInvoker)
	}
	emitEndedHook(context.WithoutCancel(ctx), req, record, status, err)

	result := Result{RunID: record.RunID, AgentID: record.AgentID, SessionID: record.ChildSessionKey, Turn: turnResult, Err: err}
	state.mu.Lock()
	state.result = result
	state.mu.Unlock()
	terminal := Event{Type: eventType, RunID: record.RunID, AgentID: record.AgentID, SessionID: record.ChildSessionKey, Result: &turnResult, Error: errorString(err)}
	select {
	case events <- terminal:
	default:
	}
}

func (o *Orchestrator) release(parentRunID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active > 0 {
		o.active--
	}
	if parentRunID != "" && o.activeParent[parentRunID] > 0 {
		o.activeParent[parentRunID]--
	}
}

func emitChildEvent(ctx context.Context, events chan<- Event, event Event) {
	select {
	case events <- event:
	case <-ctx.Done():
	}
}

func emitSpawningHook(ctx context.Context, req SpawnRequest, depth int) error {
	if req.HookInvoker == nil {
		return nil
	}
	result, err := req.HookInvoker.Emit(ctx, pluginregistry.HookSubagentSpawning, pluginhooks.SubagentSpawningEvent{BaseEvent: pluginhooks.BaseEvent{SessionID: req.ParentSessionID, AgentID: req.ParentAgentID}, ParentAgentID: req.ParentAgentID, SubagentID: req.AgentID, Instructions: req.Task, Metadata: map[string]any{"depth": depth}}, pluginhooks.EmitOptions{StopOnReject: true, HandlerTimeout: pluginhooks.DefaultHookTimeout})
	if err != nil {
		log.Printf("subagent_spawning hook error session=%s agent=%s err=%v", req.ParentSessionID, req.AgentID, err)
	}
	if result != nil && result.Rejected {
		return fmt.Errorf("subagent spawn rejected: %s", result.RejectReason)
	}
	return nil
}

func emitSpawnedHook(ctx context.Context, req SpawnRequest, runID, sessionID string) {
	if req.HookInvoker == nil {
		return
	}
	if _, err := req.HookInvoker.Emit(ctx, pluginregistry.HookSubagentSpawned, pluginhooks.SubagentSpawnedEvent{BaseEvent: pluginhooks.BaseEvent{SessionID: sessionID, AgentID: req.AgentID}, ParentAgentID: req.ParentAgentID, SubagentID: req.AgentID, RunID: runID}, pluginhooks.EmitOptions{HandlerTimeout: pluginhooks.DefaultHookTimeout}); err != nil {
		log.Printf("subagent_spawned hook error session=%s run=%s err=%v", sessionID, runID, err)
	}
}

func emitEndedHook(ctx context.Context, req SpawnRequest, record SubagentRunRecord, outcome string, err error) {
	if req.HookInvoker == nil {
		return
	}
	if _, hookErr := req.HookInvoker.Emit(ctx, pluginregistry.HookSubagentEnded, pluginhooks.SubagentEndedEvent{BaseEvent: pluginhooks.BaseEvent{SessionID: record.ChildSessionKey, AgentID: record.AgentID}, ParentAgentID: record.ParentAgentID, SubagentID: record.AgentID, Outcome: outcome, Error: errorString(err)}, pluginhooks.EmitOptions{HandlerTimeout: pluginhooks.DefaultHookTimeout}); hookErr != nil {
		log.Printf("subagent_ended hook error session=%s run=%s err=%v", record.ChildSessionKey, record.RunID, hookErr)
	}
}

func mergeBudget(defaults, override Budget) Budget {
	if override.MaxInputTokens != 0 {
		defaults.MaxInputTokens = override.MaxInputTokens
	}
	if override.MaxOutputTokens != 0 {
		defaults.MaxOutputTokens = override.MaxOutputTokens
	}
	if override.MaxTotalTokens != 0 {
		defaults.MaxTotalTokens = override.MaxTotalTokens
	}
	return defaults
}

func childSessionID(parent, agentID, runID string) string {
	parent = strings.Trim(strings.TrimSpace(parent), "/")
	if parent == "" {
		parent = "session"
	}
	return parent + "/subagent/" + agentID + "/" + runID
}

func newRunID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("subagent-%d", time.Now().UnixNano())
	}
	return "subagent-" + hex.EncodeToString(raw[:])
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
