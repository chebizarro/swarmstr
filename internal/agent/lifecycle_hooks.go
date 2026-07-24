package agent

import (
	"context"
	"fmt"
	"log"
	"sync"

	ctxengine "metiq/internal/context"
	pluginhooks "metiq/internal/plugins/hooks"
	pluginregistry "metiq/internal/plugins/registry"
)

type runtimeLifecycleState struct {
	mu       sync.Mutex
	sessions map[string]bool
}

func newRuntimeLifecycleState() *runtimeLifecycleState {
	return &runtimeLifecycleState{sessions: make(map[string]bool)}
}

func lifecycleBaseEvent(turn Turn) pluginhooks.BaseEvent {
	trace := map[string]any{}
	if turn.Trace.GoalID != "" {
		trace["goal_id"] = turn.Trace.GoalID
	}
	if turn.Trace.TaskID != "" {
		trace["task_id"] = turn.Trace.TaskID
	}
	if turn.Trace.RunID != "" {
		trace["run_id"] = turn.Trace.RunID
	}
	if turn.Trace.StepID != "" {
		trace["step_id"] = turn.Trace.StepID
	}
	if turn.Trace.ParentTaskID != "" {
		trace["parent_task_id"] = turn.Trace.ParentTaskID
	}
	if turn.Trace.ParentRunID != "" {
		trace["parent_run_id"] = turn.Trace.ParentRunID
	}
	if len(trace) == 0 {
		trace = nil
	}
	return pluginhooks.BaseEvent{SessionID: turn.SessionID, TurnID: turn.TurnID, AgentID: turn.ToolPolicyAgentID, Trace: trace}
}

func emitBeforeAgentHook(ctx context.Context, turn Turn) error {
	if turn.HookInvoker == nil {
		return nil
	}
	result, err := turn.HookInvoker.Emit(ctx, pluginregistry.HookBeforeAgentStart, pluginhooks.BeforeAgentStartEvent{
		BaseEvent: lifecycleBaseEvent(turn),
		UserText:  turn.UserText,
	}, pluginhooks.EmitOptions{StopOnReject: true, HandlerTimeout: pluginhooks.DefaultHookTimeout})
	if err != nil {
		log.Printf("before_agent_start hook error session=%s turn=%s err=%v", turn.SessionID, turn.TurnID, err)
	}
	if result != nil && result.Rejected {
		reason := result.RejectReason
		if reason == "" {
			reason = "rejected by hook"
		}
		return fmt.Errorf("agent turn rejected: %s", reason)
	}
	return nil
}

func emitAgentEndHook(ctx context.Context, turn Turn, result TurnResult, turnErr error) {
	if turn.HookInvoker == nil {
		return
	}
	event := pluginhooks.AgentEndEvent{
		BaseEvent:  lifecycleBaseEvent(turn),
		Outcome:    string(result.Outcome),
		StopReason: string(result.StopReason),
		Text:       result.Text,
		Usage: map[string]any{
			"input_tokens": result.Usage.InputTokens, "output_tokens": result.Usage.OutputTokens,
			"cache_read_tokens": result.Usage.CacheReadTokens, "cache_creation_tokens": result.Usage.CacheCreationTokens,
		},
	}
	if turnErr != nil {
		event.Error = turnErr.Error()
		if event.Outcome == "" {
			event.Outcome = "error"
		}
	}
	if _, err := turn.HookInvoker.Emit(ctx, pluginregistry.HookAgentEnd, event, pluginhooks.EmitOptions{HandlerTimeout: pluginhooks.DefaultHookTimeout}); err != nil {
		log.Printf("agent_end hook error session=%s turn=%s err=%v", turn.SessionID, turn.TurnID, err)
	}
}

func (r *ProviderRuntime) ensureSessionStarted(ctx context.Context, turn Turn) {
	if r == nil || turn.HookInvoker == nil || turn.SessionID == "" {
		return
	}
	if r.lifecycle == nil {
		r.lifecycle = newRuntimeLifecycleState()
	}
	r.lifecycle.mu.Lock()
	defer r.lifecycle.mu.Unlock()
	if r.lifecycle.sessions[turn.SessionID] {
		return
	}
	_, err := turn.HookInvoker.Emit(ctx, pluginregistry.HookSessionStart, pluginhooks.SessionStartEvent{
		SessionID: turn.SessionID,
		AgentID:   turn.ToolPolicyAgentID,
	}, pluginhooks.EmitOptions{HandlerTimeout: pluginhooks.DefaultHookTimeout})
	if err != nil {
		log.Printf("session_start hook error session=%s err=%v", turn.SessionID, err)
	}
	r.lifecycle.sessions[turn.SessionID] = true
	if registrar, ok := turn.ContextEngine.(ctxengine.SessionEndObserverRegistrar); ok {
		sessionID := turn.SessionID
		agentID := turn.ToolPolicyAgentID
		invoker := turn.HookInvoker
		registrar.RegisterSessionEndObserver(sessionID, func(endCtx context.Context, reason string) {
			r.EndSession(endCtx, sessionID, agentID, reason, invoker)
		})
	}
}

// EndSession emits session_end once for a session previously observed by this
// runtime. Session owners call this at their natural close/reset boundary.
func (r *ProviderRuntime) EndSession(ctx context.Context, sessionID, agentID, reason string, invoker *pluginhooks.HookInvoker) {
	if r == nil || invoker == nil || sessionID == "" || r.lifecycle == nil {
		return
	}
	r.lifecycle.mu.Lock()
	if !r.lifecycle.sessions[sessionID] {
		r.lifecycle.mu.Unlock()
		return
	}
	delete(r.lifecycle.sessions, sessionID)
	r.lifecycle.mu.Unlock()
	if _, err := invoker.Emit(ctx, pluginregistry.HookSessionEnd, pluginhooks.SessionEndEvent{SessionID: sessionID, AgentID: agentID, Reason: reason}, pluginhooks.EmitOptions{HandlerTimeout: pluginhooks.DefaultHookTimeout}); err != nil {
		log.Printf("session_end hook error session=%s err=%v", sessionID, err)
	}
}
