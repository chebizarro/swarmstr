package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	acppkg "metiq/internal/acp"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleACPRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string) (nostruntime.ControlRPCResult, bool, error) {
	configState := h.deps.configState

	switch method {
	case methods.MethodACPRegister:
		var req methods.ACPRegisterRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.register: invalid params: %w", err)
		}
		pk := strings.TrimSpace(req.PubKey)
		if pk == "" {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.register: pubkey required")
		}
		if h.deps.acpPeers == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.register: ACP not configured")
		}
		if err := h.deps.acpPeers.Register(acppkg.PeerEntry{
			PubKey: pk,
			Alias:  req.Alias,
			Tags:   req.Tags,
		}); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "pubkey": pk}}, true, nil

	case methods.MethodACPUnregister:
		var req methods.ACPUnregisterRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.unregister: invalid params: %w", err)
		}
		if h.deps.acpPeers != nil {
			h.deps.acpPeers.Remove(req.PubKey)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, true, nil

	case methods.MethodACPPeers:
		var peers []acppkg.PeerEntry
		if h.deps.acpPeers != nil {
			peers = h.deps.acpPeers.List()
		}
		out := make([]map[string]any, 0, len(peers))
		for _, p := range peers {
			out = append(out, map[string]any{
				"pubkey": p.PubKey,
				"alias":  p.Alias,
				"tags":   p.Tags,
			})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"peers": out}}, true, nil

	case methods.MethodACPDispatch:
		if h.deps.acpDispatcher == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: ACP not configured")
		}
		req, err := methods.DecodeACPDispatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: invalid params: %w", err)
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: %w", err)
		}
		cfg := state.ConfigDoc{}
		if configState != nil {
			cfg = configState.Get()
		}
		targetReqs := buildACPTargetRequirements(cfg, turnToolConstraints{ToolProfile: req.ToolProfile, EnabledTools: req.EnabledTools})
		target, _, err := resolveACPFleetTargetForConfigAndRequirements(req.TargetPubKey, cfg, targetReqs)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: %w", err)
		}
		dmBus, dmScheme, err := resolveACPDMTransport(cfg, target)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: %w", err)
		}
		taskID := fmt.Sprintf("acp-%d-%x", time.Now().UnixNano(), func() []byte {
			b := make([]byte, 4)
			_, _ = rand.Read(b)
			return b
		}())
		if req.Task != nil && strings.TrimSpace(req.Task.TaskID) != "" {
			taskID = strings.TrimSpace(req.Task.TaskID)
		}
		senderPubKey := dmBus.PublicKey()
		req.ToolProfile = strings.TrimSpace(req.ToolProfile)
		req.EnabledTools = normalizeACPEnabledTools(req.EnabledTools)
		var parentContext *acppkg.ParentContext
		if req.ParentContext != nil {
			parentContext = &acppkg.ParentContext{
				SessionID: strings.TrimSpace(req.ParentContext.SessionID),
				AgentID:   strings.TrimSpace(req.ParentContext.AgentID),
			}
		}
		taskPayload := acppkg.TaskPayload{
			Instructions:    req.Instructions,
			Task:            req.Task,
			ContextMessages: cloneACPContextMessages(req.ContextMessages),
			MemoryScope:     req.MemoryScope,
			ToolProfile:     req.ToolProfile,
			EnabledTools:    req.EnabledTools,
			ParentContext:   parentContext,
			TimeoutMS:       req.TimeoutMS,
			ReplyTo:         senderPubKey,
		}
		bindACPTaskID(&taskPayload, taskID)
		recordACPDelegatedChild(h.deps.sessionStore, taskPayload, taskID)
		acpMsg := acppkg.NewTask(taskID, senderPubKey, taskPayload)
		payload, err := json.Marshal(acpMsg)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: marshal: %w", err)
		}
		waitRegistered := false
		if req.Wait || parentContext != nil {
			if _, err := h.deps.acpDispatcher.RegisterTaskWithError(ctx, acppkg.TaskRecord{
				TaskID:              taskID,
				RequesterSessionKey: requesterSessionKeyFromParent(parentContext, in.FromPubKey),
				Instructions:        req.Instructions,
				Worker:              &acppkg.WorkerTaskMetadata{PubKey: target},
			}); err != nil {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: register task: %w", err)
			}
			waitRegistered = true
		}
		if err := sendACPDMWithTransport(ctx, dmBus, dmScheme, target, string(payload)); err != nil {
			if waitRegistered {
				h.deps.acpDispatcher.Cancel(taskID)
			}
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: send DM: %w", err)
		}

		if parentContext != nil {
			h.deps.acpDispatcher.MarkRunning(ctx, taskID)
			h.deps.acpDispatcher.RecordProgress(ctx, taskID, "dispatched to child agent")
		}

		// If wait==true, block until result arrives.
		if req.Wait {
			timeout := time.Duration(req.TimeoutMS) * time.Millisecond
			if timeout == 0 {
				timeout = 60 * time.Second
			}
			remoteCancel := func(cancelCtx context.Context, peerPubKey, cancelTaskID, reason string) error {
				cancelMsg := acppkg.NewCancel(cancelTaskID, senderPubKey, acppkg.CancelPayload{Reason: reason})
				encoded, marshalErr := json.Marshal(cancelMsg)
				if marshalErr != nil {
					return fmt.Errorf("marshal cancel: %w", marshalErr)
				}
				return sendACPDMWithTransport(cancelCtx, dmBus, dmScheme, peerPubKey, string(encoded))
			}
			result, waitErr := h.deps.acpDispatcher.WaitWithRemoteCancel(ctx, taskID, timeout, remoteCancel)
			if waitErr != nil {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: wait: %w", waitErr)
			}
			if result.Error != "" {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.dispatch: worker error: %s", result.Error)
			}
			out := map[string]any{
				"ok": true, "task_id": taskID, "target": target,
				"text": result.Text,
			}
			if result.SenderPubKey != "" {
				out["sender_pubkey"] = result.SenderPubKey
			}
			if result.Worker != nil {
				out["worker"] = result.Worker
			}
			if result.TokensUsed > 0 {
				out["tokens_used"] = result.TokensUsed
			}
			if result.CompletedAt > 0 {
				out["completed_at"] = result.CompletedAt
			}
			return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
		}

		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "task_id": taskID, "target": target}}, true, nil

	case methods.MethodACPPipeline:
		req, err := methods.DecodeACPPipelineParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.pipeline: invalid params: %w", err)
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.pipeline: %w", err)
		}
		ctx = acppkg.ContextWithFlowAnnouncement(ctx, req.Announce)
		cfg := state.ConfigDoc{}
		if configState != nil {
			cfg = configState.Get()
		}

		sendFn := func(ctx context.Context, peerPubKey, taskID string, payload acppkg.TaskPayload) error {
			dmBus, dmScheme, err := resolveACPDMTransport(cfg, peerPubKey)
			if err != nil {
				return err
			}
			senderPubKey := dmBus.PublicKey()
			payload.ReplyTo = senderPubKey
			if payload.Task != nil && strings.TrimSpace(payload.Task.TaskID) != "" {
				taskID = strings.TrimSpace(payload.Task.TaskID)
			}
			bindACPTaskID(&payload, taskID)
			recordACPDelegatedChild(h.deps.sessionStore, payload, taskID)
			acpMsg := acppkg.NewTask(taskID, senderPubKey, payload)
			encoded, marshalErr := json.Marshal(acpMsg)
			if marshalErr != nil {
				return fmt.Errorf("marshal task: %w", marshalErr)
			}
			return sendACPDMWithTransport(ctx, dmBus, dmScheme, peerPubKey, string(encoded))
		}
		remoteCancel := func(cancelCtx context.Context, peerPubKey, taskID, reason string) error {
			dmBus, dmScheme, err := resolveACPDMTransport(cfg, peerPubKey)
			if err != nil {
				return err
			}
			cancelMsg := acppkg.NewCancel(taskID, dmBus.PublicKey(), acppkg.CancelPayload{Reason: reason})
			encoded, marshalErr := json.Marshal(cancelMsg)
			if marshalErr != nil {
				return fmt.Errorf("marshal cancel: %w", marshalErr)
			}
			return sendACPDMWithTransport(cancelCtx, dmBus, dmScheme, peerPubKey, string(encoded))
		}

		steps := make([]acppkg.Step, 0, len(req.Steps))
		for i, s := range req.Steps {
			stepReqs := buildACPTargetRequirements(cfg, turnToolConstraints{ToolProfile: s.ToolProfile, EnabledTools: s.EnabledTools})
			resolvedPeer, _, routeErr := resolveACPFleetTargetForConfigAndRequirements(s.PeerPubKey, cfg, stepReqs)
			if routeErr != nil {
				return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.pipeline: %w at steps[%d]", routeErr, i)
			}
			s.PeerPubKey = resolvedPeer
			s.ToolProfile = strings.TrimSpace(s.ToolProfile)
			s.EnabledTools = normalizeACPEnabledTools(s.EnabledTools)
			var parentContext *acppkg.ParentContext
			if s.ParentContext != nil {
				parentContext = &acppkg.ParentContext{
					SessionID: strings.TrimSpace(s.ParentContext.SessionID),
					AgentID:   strings.TrimSpace(s.ParentContext.AgentID),
				}
			}
			steps = append(steps, acppkg.Step{
				PeerPubKey:      s.PeerPubKey,
				Instructions:    s.Instructions,
				Task:            s.Task,
				ContextMessages: cloneACPContextMessages(s.ContextMessages),
				MemoryScope:     s.MemoryScope,
				ToolProfile:     s.ToolProfile,
				EnabledTools:    s.EnabledTools,
				ParentContext:   parentContext,
				TimeoutMS:       s.TimeoutMS,
			})
		}
		ownerSessionKey := strings.TrimSpace(in.FromPubKey)
		// Prefer the invoking turn's room-scoped parent session so commitment
		// validation cannot reuse a live flow from another room.
		for _, step := range steps {
			if step.ParentContext != nil && strings.TrimSpace(step.ParentContext.SessionID) != "" {
				ownerSessionKey = strings.TrimSpace(step.ParentContext.SessionID)
				break
			}
		}
		goal := "ACP pipeline"
		if len(steps) > 0 {
			goal = strings.TrimSpace(steps[0].Instructions)
			if goal == "" {
				goal = "ACP pipeline"
			}
		}
		pipeline := &acppkg.Pipeline{Steps: steps, FlowRegistry: h.deps.acpFlowRegistry, OwnerSessionKey: ownerSessionKey, Goal: goal, MaxConcurrency: req.MaxConcurrency, RemoteCancel: remoteCancel}

		var pipelineResults []acppkg.PipelineResult
		var pipelineErr error
		if req.Parallel {
			pipelineResults, pipelineErr = pipeline.RunParallel(ctx, h.deps.acpDispatcher, sendFn)
		} else {
			pipelineResults, pipelineErr = pipeline.RunSequential(ctx, h.deps.acpDispatcher, sendFn)
		}

		out := make([]map[string]any, 0, len(pipelineResults))
		for _, r := range pipelineResults {
			item := map[string]any{
				"step_index": r.StepIndex,
				"task_id":    r.TaskID,
				"text":       r.Text,
				"error":      r.Error,
			}
			if r.SenderPubKey != "" {
				item["sender_pubkey"] = r.SenderPubKey
			}
			if r.Worker != nil {
				item["worker"] = r.Worker
			}
			if r.TokensUsed > 0 {
				item["tokens_used"] = r.TokensUsed
			}
			if r.CompletedAt > 0 {
				item["completed_at"] = r.CompletedAt
			}
			out = append(out, item)
		}
		aggregate := acppkg.AggregateResults(pipelineResults)

		if pipelineErr != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.pipeline: %w", pipelineErr)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":      true,
			"flow_id": pipeline.FlowID,
			"results": out,
			"text":    aggregate,
		}}, true, nil

	case methods.MethodACPSessionInit:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.init: ACP manager not configured")
		}
		var req acppkg.InitializeSessionInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.init: invalid params: %w", err)
		}
		handle, err := h.deps.acpManager.InitializeSession(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.init: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "handle": handle}}, true, nil

	case methods.MethodACPSessionRun:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.run: ACP manager not configured")
		}
		var req acppkg.RunSessionTurnInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.run: invalid params: %w", err)
		}
		events, err := h.deps.acpManager.RunTurn(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.run: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "events": events}}, true, nil

	case methods.MethodACPSessionSpawn:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.spawn: ACP manager not configured")
		}
		var req acppkg.SpawnSessionInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.spawn: invalid params: %w", err)
		}
		spawn, err := h.deps.acpManager.SpawnSession(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.spawn: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "spawn": spawn}}, true, nil

	case methods.MethodACPSessionCancel:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.cancel: ACP manager not configured")
		}
		var req acppkg.CancelSessionInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.cancel: invalid params: %w", err)
		}
		if err := h.deps.acpManager.CancelSession(ctx, req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.cancel: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, true, nil

	case methods.MethodACPSessionClose:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.close: ACP manager not configured")
		}
		var req acppkg.CloseSessionInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.close: invalid params: %w", err)
		}
		if err := h.deps.acpManager.CloseSession(ctx, req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.close: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, true, nil

	case methods.MethodACPSessionStatus:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.status: ACP manager not configured")
		}
		var req struct {
			SessionKey string `json:"session_key"`
		}
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.status: invalid params: %w", err)
		}
		status, err := h.deps.acpManager.GetSessionStatus(ctx, req.SessionKey)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.session.status: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session": status}}, true, nil

	case methods.MethodACPManagerStatus:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("acp.manager.status: ACP manager not configured")
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "manager": h.deps.acpManager.Status(ctx)}}, true, nil
	}
	return nostruntime.ControlRPCResult{}, false, nil
}
