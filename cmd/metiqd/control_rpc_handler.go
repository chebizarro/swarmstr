package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	"metiq/internal/autoreply"
	"metiq/internal/canvas"
	ctxengine "metiq/internal/context"
	"metiq/internal/gateway/channels"
	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/nodepending"
	hookspkg "metiq/internal/hooks"
	mediapkg "metiq/internal/media"
	"metiq/internal/memory"
	nostruntime "metiq/internal/nostr/runtime"
	pluginmanager "metiq/internal/plugins/manager"
	"metiq/internal/policy"
	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

type controlRPCDeps struct {
	dmBus             nostruntime.DMTransport
	controlBus        *nostruntime.ControlRPCBus
	chatCancels       *chatAbortRegistry
	steeringMailboxes *autoreply.SteeringMailboxRegistry
	usageState        *usageTracker
	logBuffer         *runtimeLogBuffer
	channelState      *channelRuntimeState
	docsRepo          *state.DocsRepository
	taskService       *taskspkg.Service
	transcriptRepo    *state.TranscriptRepository
	memoryIndex       memory.Store
	configState       *runtimeConfigStore
	tools             *agent.ToolRegistry
	pluginMgr         *pluginmanager.GojaPluginManager
	startedAt         time.Time
	bootstrapPath     string

	sessionStore     *state.SessionStore
	hooksMgr         hooksEventFirer
	hooksMgrFull     *hookspkg.Manager
	mediaTranscriber mediapkg.Transcriber
	toolRegistry     *agent.ToolRegistry
	agentJobs        *agentJobRegistry
	sessionRouter    *agent.AgentSessionRouter
	agentRegistry    *agent.AgentRuntimeRegistry
	agentRuntime     agent.Runtime

	// Fields below replace direct global access inside Handle().
	sessionMemoryRuntime *sessionMemoryRuntime
	acpPeers             *acppkg.PeerRegistry
	acpDispatcher        *acppkg.Dispatcher
	acpManager           *acppkg.Manager

	// services provides access to the consolidated daemonServices struct.
	// Extracted handler files and RPC sub-handlers can use this instead of
	// reading package-level globals.
	services *daemonServices

	// Operation registries — replace direct global reads in RPC sub-handlers.
	ops             *operationsRegistry
	cronJobs        *cronRegistry
	execApprovals   *execApprovalsRegistry
	wizards         *wizardRegistry
	contextEngine   ctxengine.Engine
	mcpOps          *mcpOpsController
	mcpAuth         *mcpAuthController
	nodeInvocations *nodeInvocationRegistry
	nodePending     *nodepending.Store
	canvasHost      *canvas.Host
	channels        *channels.Registry
	nostrHub        *nostruntime.NostrHub
	keyer           nostr.Keyer
}

type hooksEventFirer interface {
	Fire(eventName string, sessionKey string, ctx map[string]any) []error
}

type controlRPCHandler struct {
	deps controlRPCDeps
}

func newControlRPCHandler(deps controlRPCDeps) controlRPCHandler {
	return controlRPCHandler{deps: deps}
}

func (h controlRPCHandler) Handle(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, error) {
	usageState := h.deps.usageState
	memoryIndex := h.deps.memoryIndex
	configState := h.deps.configState

	method := strings.TrimSpace(in.Method)
	cfg := configState.Get()
	internal := in.Internal
	if !internal && !in.Authenticated && strings.TrimSpace(in.FromPubKey) != "" && !cfg.Control.RequireAuth && in.EventID == "" && in.RequestID == "" && in.RelayURL == "" {
		// Backward-compatible path for in-process daemon/test dispatchers that
		// predate explicit auth metadata. Real ingress paths now set either
		// Authenticated (Nostr/admin/WS principal) or Internal explicitly.
		internal = true
	}
	decision := policy.ControlDecision{Allowed: true, Authenticated: true}
	if !internal {
		decision = policy.EvaluateControlCall(in.FromPubKey, method, in.Authenticated, cfg)
	}
	if usageState != nil {
		usageState.RecordControl()
	}
	if !decision.Allowed {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("forbidden")
		}
		if !strings.HasPrefix(strings.ToLower(reason), "forbidden") {
			reason = "forbidden: " + reason
		}
		return nostruntime.ControlRPCResult{}, errors.New(reason)
	}

	if result, handled, err := h.handleAgentRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleSessionRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleTaskRPC(ctx, in, method); handled {
		return result, err
	}
	if result, handled, err := h.handleChannelRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleToolingRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleNodeRPC(ctx, in, method); handled {
		return result, err
	}
	if result, handled, err := h.handleOpsRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleConfigRPC(ctx, in, method, cfg); handled {
		return result, err
	}

	switch method {
	case methods.MethodSupportedMethods:
		return nostruntime.ControlRPCResult{Result: supportedMethods(cfg)}, nil
	case methods.MethodHealth:
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil
	case methods.MethodDoctorMemoryStatus:
		indexAvailable := memoryIndex != nil
		entryCount := 0
		sessionCount := 0
		sessionCountSupported := true
		countSource := "primary_index"
		var storeStatus *memory.StoreStatus
		if memoryIndex != nil {
			if reporter, ok := memoryIndex.(interface{ MemoryStatus() memory.StoreStatus }); ok {
				status := reporter.MemoryStatus()
				storeStatus = &status
				indexAvailable = status.Primary.Available || status.Kind == "hybrid"
				switch status.Kind {
				case "hybrid":
					countSource = "fallback_index"
				case "backend":
					countSource = "primary_backend"
					if status.Primary.Name == "qdrant" {
						sessionCountSupported = false
					}
				}
			}
			if storeStatus == nil || storeStatus.Kind == "index" || storeStatus.Kind == "hybrid" || storeStatus.Primary.Available {
				entryCount = memoryIndex.Count()
				if sessionCountSupported {
					sessionCount = memoryIndex.SessionCount()
				}
			}
		}
		indexStatus := map[string]any{
			"available":    indexAvailable,
			"entry_count":  entryCount,
			"count_source": countSource,
		}
		if sessionCountSupported {
			indexStatus["session_count"] = sessionCount
		} else {
			indexStatus["session_count_supported"] = false
		}
		result := map[string]any{
			"ok":             true,
			"index":          indexStatus,
			"file_memory":    fileMemoryStatusPayload(h.deps.sessionStore),
			"session_memory": sessionMemoryStatusPayload(cfg, h.deps.sessionStore, h.deps.sessionMemoryRuntime),
			"maintenance":    memoryMaintenanceStatusPayload(h.deps.sessionStore),
		}
		if storeStatus != nil {
			result["store"] = memoryStoreStatusPayload(*storeStatus)
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodACPRegister:
		var req methods.ACPRegisterRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.register: invalid params: %w", err)
		}
		pk := strings.TrimSpace(req.PubKey)
		if pk == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.register: pubkey required")
		}
		if h.deps.acpPeers == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.register: ACP not configured")
		}
		if err := h.deps.acpPeers.Register(acppkg.PeerEntry{
			PubKey: pk,
			Alias:  req.Alias,
			Tags:   req.Tags,
		}); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "pubkey": pk}}, nil

	case methods.MethodACPUnregister:
		var req methods.ACPUnregisterRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.unregister: invalid params: %w", err)
		}
		if h.deps.acpPeers != nil {
			h.deps.acpPeers.Remove(req.PubKey)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil

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
		return nostruntime.ControlRPCResult{Result: map[string]any{"peers": out}}, nil

	case methods.MethodACPDispatch:
		if h.deps.acpDispatcher == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: ACP not configured")
		}
		req, err := methods.DecodeACPDispatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: invalid params: %w", err)
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: %w", err)
		}
		cfg := state.ConfigDoc{}
		if configState != nil {
			cfg = configState.Get()
		}
		targetReqs := buildACPTargetRequirements(cfg, turnToolConstraints{ToolProfile: req.ToolProfile, EnabledTools: req.EnabledTools})
		target, _, err := resolveACPFleetTargetForConfigAndRequirements(req.TargetPubKey, cfg, targetReqs)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: %w", err)
		}
		dmBus, dmScheme, err := resolveACPDMTransport(cfg, target)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: %w", err)
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
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: marshal: %w", err)
		}
		waitRegistered := false
		if req.Wait {
			h.deps.acpDispatcher.Register(taskID)
			waitRegistered = true
		}
		if err := sendACPDMWithTransport(ctx, dmBus, dmScheme, target, string(payload)); err != nil {
			if waitRegistered {
				h.deps.acpDispatcher.Cancel(taskID)
			}
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: send DM: %w", err)
		}

		// If wait==true, block until result arrives.
		if req.Wait {
			timeout := time.Duration(req.TimeoutMS) * time.Millisecond
			if timeout == 0 {
				timeout = 60 * time.Second
			}
			result, waitErr := h.deps.acpDispatcher.Wait(ctx, taskID, timeout)
			if waitErr != nil {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: wait: %w", waitErr)
			}
			if result.Error != "" {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: worker error: %s", result.Error)
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
			return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
		}

		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "task_id": taskID, "target": target}}, nil

	case methods.MethodACPPipeline:
		req, err := methods.DecodeACPPipelineParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.pipeline: invalid params: %w", err)
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.pipeline: %w", err)
		}
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
			encoded, _ := json.Marshal(acpMsg)
			return sendACPDMWithTransport(ctx, dmBus, dmScheme, peerPubKey, string(encoded))
		}

		steps := make([]acppkg.Step, 0, len(req.Steps))
		for i, s := range req.Steps {
			stepReqs := buildACPTargetRequirements(cfg, turnToolConstraints{ToolProfile: s.ToolProfile, EnabledTools: s.EnabledTools})
			resolvedPeer, _, routeErr := resolveACPFleetTargetForConfigAndRequirements(s.PeerPubKey, cfg, stepReqs)
			if routeErr != nil {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.pipeline: %w at steps[%d]", routeErr, i)
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
		pipeline := &acppkg.Pipeline{Steps: steps}

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
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.pipeline: %w", pipelineErr)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":      true,
			"results": out,
			"text":    aggregate,
		}}, nil

	case methods.MethodACPSessionInit:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.init: ACP manager not configured")
		}
		var req acppkg.InitializeSessionInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.init: invalid params: %w", err)
		}
		handle, err := h.deps.acpManager.InitializeSession(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.init: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "handle": handle}}, nil

	case methods.MethodACPSessionRun:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.run: ACP manager not configured")
		}
		var req acppkg.RunSessionTurnInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.run: invalid params: %w", err)
		}
		events, err := h.deps.acpManager.RunTurn(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.run: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "events": events}}, nil

	case methods.MethodACPSessionSpawn:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.spawn: ACP manager not configured")
		}
		var req acppkg.SpawnSessionInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.spawn: invalid params: %w", err)
		}
		spawn, err := h.deps.acpManager.SpawnSession(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.spawn: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "spawn": spawn}}, nil

	case methods.MethodACPSessionCancel:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.cancel: ACP manager not configured")
		}
		var req acppkg.CancelSessionInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.cancel: invalid params: %w", err)
		}
		if err := h.deps.acpManager.CancelSession(ctx, req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.cancel: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil

	case methods.MethodACPSessionClose:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.close: ACP manager not configured")
		}
		var req acppkg.CloseSessionInput
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.close: invalid params: %w", err)
		}
		if err := h.deps.acpManager.CloseSession(ctx, req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.close: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil

	case methods.MethodACPSessionStatus:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.status: ACP manager not configured")
		}
		var req struct {
			SessionKey string `json:"session_key"`
		}
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.status: invalid params: %w", err)
		}
		status, err := h.deps.acpManager.GetSessionStatus(ctx, req.SessionKey)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.session.status: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session": status}}, nil

	case methods.MethodACPManagerStatus:
		if h.deps.acpManager == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.manager.status: ACP manager not configured")
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "manager": h.deps.acpManager.Status(ctx)}}, nil

	default:
		return nostruntime.ControlRPCResult{}, fmt.Errorf("unknown method %q", method)
	}

}
