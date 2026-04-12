package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"metiq/internal/admin"
	"metiq/internal/agent"
	"metiq/internal/config"
	"metiq/internal/gateway/channels"
	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/nodepending"
	gatewayws "metiq/internal/gateway/ws"
	hookspkg "metiq/internal/hooks"
	mcppkg "metiq/internal/mcp"
	"metiq/internal/memory"
	nostruntime "metiq/internal/nostr/runtime"
	pluginmanager "metiq/internal/plugins/manager"
	"metiq/internal/policy"
	"metiq/internal/store/state"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent/toolbuiltin"
	exportpkg "metiq/internal/export"
	securitypkg "metiq/internal/security"
)

type controlRPCDeps struct {
	dmBus          nostruntime.DMTransport
	controlBus     *nostruntime.ControlRPCBus
	chatCancels    *chatAbortRegistry
	usageState     *usageTracker
	logBuffer      *runtimeLogBuffer
	channelState   *channelRuntimeState
	docsRepo       *state.DocsRepository
	transcriptRepo *state.TranscriptRepository
	memoryIndex    memory.Store
	configState    *runtimeConfigStore
	tools          *agent.ToolRegistry
	pluginMgr      *pluginmanager.GojaPluginManager
	startedAt      time.Time
}

type controlRPCHandler struct {
	deps controlRPCDeps
}

func newControlRPCHandler(deps controlRPCDeps) controlRPCHandler {
	return controlRPCHandler{deps: deps}
}

func (h controlRPCHandler) Handle(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, error) {
	dmBus := h.deps.dmBus
	controlBus := h.deps.controlBus
	chatCancels := h.deps.chatCancels
	usageState := h.deps.usageState
	logBuffer := h.deps.logBuffer
	channelState := h.deps.channelState
	docsRepo := h.deps.docsRepo
	transcriptRepo := h.deps.transcriptRepo
	memoryIndex := h.deps.memoryIndex
	configState := h.deps.configState
	tools := h.deps.tools
	pluginMgr := h.deps.pluginMgr
	startedAt := h.deps.startedAt

	method := strings.TrimSpace(in.Method)
	cfg := configState.Get()
	decision := policy.EvaluateControlCall(in.FromPubKey, method, true, cfg)
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
			"file_memory":    fileMemoryStatusPayload(controlSessionStore),
			"session_memory": sessionMemoryStatusPayload(cfg, controlSessionStore, controlSessionMemoryRuntime),
			"maintenance":    memoryMaintenanceStatusPayload(controlSessionStore),
		}
		if storeStatus != nil {
			result["store"] = memoryStoreStatusPayload(*storeStatus)
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodLogsTail:
		req, err := methods.DecodeLogsTailParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if logBuffer == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"cursor": req.Cursor, "lines": []string{}, "truncated": false, "reset": false}}, nil
		}
		return nostruntime.ControlRPCResult{Result: logBuffer.Tail(req.Cursor, req.Limit, req.MaxBytes)}, nil
	case methods.MethodRuntimeObserve:
		req, err := methods.DecodeRuntimeObserveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := toolbuiltin.ObserveRuntime(ctx, runtimeObserveToolRequest(req))
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: out}, nil
	case methods.MethodChannelsStatus:
		req, err := methods.DecodeChannelsStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if channelState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []map[string]any{buildNostrChannelStatusRow(map[string]any{}, "channel_state_unavailable")}}}, nil
		}
		status := channelState.Status(dmBus, controlBus, cfg)
		return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []map[string]any{buildNostrChannelStatusRow(status, "")}}}, nil
	case methods.MethodChannelsLogout:
		req, err := methods.DecodeChannelsLogoutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if channelState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel": req.Channel}}, nil
		}
		res, err := channelState.Logout(req.Channel)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: res}, nil
	case methods.MethodChannelsJoin:
		req, err := methods.DecodeChannelsJoinParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("channel runtime not configured")
		}
		ch, chErr := channels.NewNIP29GroupChannel(ctx, channels.NIP29GroupChannelOptions{
			GroupAddress: req.GroupAddress,
			Hub:          controlHub,
			Keyer:        controlKeyer,
			OnMessage: func(msg channels.InboundMessage) {
				emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
					TS:        time.Now().UnixMilli(),
					ChannelID: msg.ChannelID,
					GroupID:   msg.GroupID,
					Relay:     msg.Relay,
					Direction: "inbound",
					From:      msg.FromPubKey,
					Text:      msg.Text,
					EventID:   msg.EventID,
				})
				activeAgentID, rt := resolveInboundChannelRuntime("", msg.ChannelID)
				turnCtx, release := chatCancels.Begin(msg.ChannelID, ctx)
				go func() {
					defer release()
					filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, msg.ChannelID, activeAgentID, rt, tools, turnToolConstraints{})
					result, turnErr := filteredRuntime.ProcessTurn(turnCtx, agent.Turn{
						SessionID:           msg.ChannelID,
						UserText:            msg.Text,
						Tools:               turnTools,
						Executor:            turnExecutor,
						ContextWindowTokens: maxContextTokensForAgent(configState.Get(), activeAgentID),
					})
					if turnErr != nil {
						log.Printf("channel agent turn error channel=%s err=%v", msg.ChannelID, turnErr)
						return
					}
					if err := msg.Reply(turnCtx, result.Text); err != nil {
						log.Printf("channel reply error channel=%s err=%v", msg.ChannelID, err)
						return
					}
					emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
						TS:        time.Now().UnixMilli(),
						ChannelID: msg.ChannelID,
						GroupID:   msg.GroupID,
						Relay:     msg.Relay,
						Direction: "outbound",
						Text:      result.Text,
					})
				}()
			},
			OnError: func(err error) {
				log.Printf("nip29 channel error channel=%s err=%v", req.GroupAddress, err)
			},
		})
		if chErr != nil {
			return nostruntime.ControlRPCResult{}, chErr
		}
		if err := controlChannels.Add(ch); err != nil {
			ch.Close()
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":         true,
			"channel_id": ch.ID(),
			"type":       ch.Type(),
		}}, nil
	case methods.MethodChannelsLeave:
		req, err := methods.DecodeChannelsLeaveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("channel runtime not configured")
		}
		if err := controlChannels.Remove(req.ChannelID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel_id": req.ChannelID}}, nil
	case methods.MethodChannelsList:
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []any{}}}, nil
		}
		list := controlChannels.List()
		return nostruntime.ControlRPCResult{Result: map[string]any{"channels": list, "count": len(list)}}, nil
	case methods.MethodChannelsSend:
		req, err := methods.DecodeChannelsSendParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("channel runtime not configured")
		}
		ch, ok := controlChannels.Get(req.ChannelID)
		if !ok {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("channel %q not found; join it first with channels.join", req.ChannelID)
		}
		if err := ch.Send(ctx, req.Text); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
			TS:        time.Now().UnixMilli(),
			ChannelID: req.ChannelID,
			Direction: "outbound",
			Text:      req.Text,
		})
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel_id": req.ChannelID}}, nil
	case methods.MethodStatus:
		pubkey := ""
		if dmBus != nil {
			pubkey = dmBus.PublicKey()
		}
		var subs []methods.SubHealthInfo
		if controlBus != nil {
			subs = append(subs, subHealthToInfo(controlBus.HealthSnapshot()))
		}
		if dmBus != nil {
			if reporter, ok := dmBus.(nostruntime.SubHealthReporter); ok {
				subs = append(subs, subHealthToInfo(reporter.HealthSnapshot()))
			}
		}
		if dvmHandler != nil {
			subs = append(subs, subHealthToInfo(dvmHandler.HealthSnapshot()))
		}
		// Collect relay sets for status response.
		var relaySets map[string][]string
		if relaySetRegistry != nil {
			all := relaySetRegistry.All()
			if len(all) > 0 {
				relaySets = make(map[string][]string, len(all))
				for dtag, entry := range all {
					relaySets[dtag] = entry.Relays
				}
			}
		}
		var mcpSnapshot *mcppkg.TelemetrySnapshot
		if controlMCPOps != nil {
			mcpSnapshot = controlMCPOps.telemetrySnapshotPtr()
		}
		return nostruntime.ControlRPCResult{Result: methods.StatusResponse{
			PubKey:        pubkey,
			Relays:        cfg.Relays.Read,
			DMPolicy:      cfg.DM.Policy,
			UptimeSeconds: int(time.Since(startedAt).Seconds()),
			UptimeMS:      time.Since(startedAt).Milliseconds(),
			Version:       "metiqd",
			Subscriptions: subs,
			RelaySets:     relaySets,
			MCP:           mcpSnapshot,
		}}, nil
	case methods.MethodUsageStatus:
		if usageState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "totals": map[string]any{"requests": 0, "tokens": 0}}}, nil
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "totals": usageState.Status()}}, nil
	case methods.MethodUsageCost:
		req, err := methods.DecodeUsageCostParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if usageState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "total_usd": 0, "filtered": false}}, nil
		}
		if req.StartDate != "" || req.EndDate != "" || req.Days > 0 {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("usage.cost: date filtering is not supported")
		}
		cost := usageState.Cost()
		result := map[string]any{"ok": true, "total_usd": cost["total_usd"], "estimate": cost, "filtered": false}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodMemorySearch:
		req, err := methods.DecodeMemorySearchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.MemorySearchResponse{Results: memoryIndex.Search(req.Query, req.Limit)}}, nil

	case methods.MethodMemoryCompact:
		var compactReq methods.MemoryCompactRequest
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &compactReq)
		}
		if controlContextEngine == nil {
			return nostruntime.ControlRPCResult{Result: methods.MemoryCompactResponse{OK: false, Summary: "no context engine active"}}, nil
		}
		sessionToCompact := compactReq.SessionID
		flushOutcome, err := ensureSessionMemoryCurrent(ctx, configState.Get(), sessionToCompact, controlSessionStore)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("memory.compact session memory flush: %w", err)
		}
		cr, cErr := controlContextEngine.Compact(ctx, sessionToCompact)
		if cErr != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("memory.compact: %w", cErr)
		}
		recordSessionCompaction(controlSessionStore, sessionToCompact, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
		return nostruntime.ControlRPCResult{Result: methods.MemoryCompactResponse{
			OK:           cr.OK,
			SessionsRun:  1,
			TokensBefore: cr.TokensBefore,
			TokensAfter:  cr.TokensAfter,
			Summary:      cr.Summary,
		}}, nil
	case methods.MethodAgent:
		req, err := methods.DecodeAgentParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if req.SessionID == "" {
			req.SessionID = in.FromPubKey
		}
		if controlAgentJobs == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agent runtime not configured")
		}
		// Route to the agent assigned to this session, falling back to the default runtime.
		var rt agent.Runtime
		if controlSessionRouter != nil && controlAgentRegistry != nil {
			activeAgentID := controlSessionRouter.Get(req.SessionID)
			rt = controlAgentRegistry.Get(activeAgentID)
		}
		if rt == nil {
			rt = controlAgentRuntime
		}
		if rt == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agent runtime not configured")
		}
		activeAgentID := ""
		if controlSessionRouter != nil {
			activeAgentID = controlSessionRouter.Get(req.SessionID)
		}
		// Apply profile-based tool filtering when the agent has a non-full profile.
		rt = applyAgentProfileFilter(ctx, rt, req.SessionID, cfg, docsRepo)
		// Build fallback runtimes from the active agent's FallbackModels list.
		var fallbackRuntimes []agent.Runtime
		primaryLabel := strings.TrimSpace(cfg.Agent.DefaultModel)
		if primaryLabel == "" {
			primaryLabel = "primary"
		}
		runtimeLabels := []string{primaryLabel}
		if agCfg, ok := resolveAgentConfigByID(cfg, activeAgentID); ok {
			if strings.TrimSpace(agCfg.Model) != "" {
				primaryLabel = strings.TrimSpace(agCfg.Model)
				runtimeLabels[0] = primaryLabel
			}
			for _, fbModel := range agCfg.FallbackModels {
				fbModel = strings.TrimSpace(fbModel)
				if fbModel == "" {
					continue
				}
				fbRt, fbErr := buildRuntimeForAgentModel(cfg, agCfg, fbModel, controlToolRegistry)
				if fbErr == nil && fbRt != nil {
					fbRt = applyAgentProfileFilterForAgent(ctx, fbRt, activeAgentID, cfg, docsRepo)
					fallbackRuntimes = append(fallbackRuntimes, fbRt)
					runtimeLabels = append(runtimeLabels, fbModel)
				}
			}
		}
		runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
		snapshot := controlAgentJobs.Begin(runID, req.SessionID)
		go executeAgentRunWithFallbacks(runID, req, rt, fallbackRuntimes, runtimeLabels, memoryIndex, controlAgentJobs)
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"run_id": runID, "status": "accepted", "accepted_at": snapshot.StartedAt})}, nil
	case methods.MethodAgentWait:
		req, err := methods.DecodeAgentWaitParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlAgentJobs == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agent runtime not configured")
		}
		snap, ok := controlAgentJobs.Wait(ctx, req.RunID, time.Duration(req.TimeoutMS)*time.Millisecond)
		if !ok {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("run not found")
		}
		if snap.Status == "pending" {
			return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"run_id": req.RunID, "status": "timeout"})}, nil
		}
		out := map[string]any{"run_id": req.RunID, "status": snap.Status, "started_at": snap.StartedAt, "ended_at": snap.EndedAt}
		if snap.Err != "" {
			out["error"] = snap.Err
		}
		if snap.Result != "" {
			out["result"] = snap.Result
		}
		if snap.FallbackUsed {
			out["fallback_used"] = true
			out["fallback_from"] = snap.FallbackFrom
			out["fallback_to"] = snap.FallbackTo
			if snap.FallbackReason != "" {
				out["fallback_reason"] = truncateRunes(snap.FallbackReason, 200)
			}
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodAgentIdentityGet:
		req, err := methods.DecodeAgentIdentityParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agentID := strings.TrimSpace(req.AgentID)
		sessionID := strings.TrimSpace(req.SessionID)
		if sessionID == "" {
			sessionID = in.FromPubKey
		}
		if agentID == "" && controlSessionRouter != nil {
			agentID = controlSessionRouter.Get(sessionID)
		}
		if agentID == "" {
			agentID = "main"
		}
		displayName := "Metiq Agent"
		if docsRepo != nil {
			if ag, err2 := docsRepo.GetAgent(ctx, agentID); err2 == nil && ag.Name != "" {
				displayName = ag.Name
			}
		}
		pubkey := strings.TrimSpace(in.FromPubKey)
		if dmBus != nil {
			pubkey = dmBus.PublicKey()
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agent_id": agentID, "display_name": displayName, "session_id": sessionID, "pubkey": pubkey})}, nil
	case methods.MethodGatewayIdentityGet:
		pubkey := strings.TrimSpace(in.FromPubKey)
		if dmBus != nil {
			pubkey = dmBus.PublicKey()
		}
		deviceID := pubkey
		if len(deviceID) > 24 {
			deviceID = deviceID[:24]
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"deviceId": deviceID, "publicKey": pubkey, "pubkey": pubkey}}, nil
	case methods.MethodChatSend:
		req, err := methods.DecodeChatSendParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Preprocess any media attachments: transcribe audio, extract PDF text,
		// resolve image references. The augmented text is sent via DM; image
		// refs are currently logged (vision would require direct ProcessTurn access
		// which the RPC handler does not have without threading the runtime through).
		msgText := req.Text
		if len(req.Attachments) > 0 {
			var preprocessErr error
			msgText, _, preprocessErr = preprocessAttachments(ctx, req.Text, req.Attachments, controlMediaTranscriber)
			if preprocessErr != nil {
				log.Printf("chat.send: attachment preprocess error: %v", preprocessErr)
			}
		}
		if msgText == "" {
			msgText = req.Text
		}
		sendCtx := ctx
		release := func() {}
		if chatCancels != nil {
			sendCtx, release = chatCancels.Begin(req.To, ctx)
			defer release()
		}
		if err := dmBus.SendDM(sendCtx, req.To, msgText); err != nil {
			if errors.Is(err, context.Canceled) {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("chat aborted")
			}
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil
	case methods.MethodChatHistory:
		req, err := methods.DecodeChatHistoryParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.GetSession(ctx, req.SessionID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		transcript, err := transcriptRepo.ListSession(ctx, req.SessionID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"session_id": req.SessionID, "entries": transcript})}, nil
	case methods.MethodChatAbort:
		req, err := methods.DecodeChatAbortParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		aborted := 0
		if chatCancels != nil {
			if strings.TrimSpace(req.SessionID) == "" {
				aborted = chatCancels.AbortAll()
			} else if chatCancels.Abort(req.SessionID) {
				aborted = 1
			}
		}
		if usageState != nil {
			usageState.RecordAbort(aborted)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "aborted": aborted > 0, "aborted_count": aborted})}, nil
	case methods.MethodSessionGet:
		req, err := methods.DecodeSessionGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		session, err := docsRepo.GetSession(ctx, req.SessionID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		transcript, err := transcriptRepo.ListSession(ctx, req.SessionID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.SessionGetResponse{Session: session, Transcript: transcript}}, nil
	case methods.MethodSessionsList:
		req, err := methods.DecodeSessionsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		result, err := admin.BuildSessionsListResponse(ctx, req, admin.SessionsListResponseOptions{
			Config:         cfg,
			SessionStore:   controlSessionStore,
			ListSessions:   docsRepo.ListSessions,
			ListTranscript: transcriptRepo.ListSession,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodSessionsPreview:
		req, err := methods.DecodeSessionsPreviewParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		session, err := docsRepo.GetSession(ctx, req.SessionID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		transcript, err := transcriptRepo.ListSession(ctx, req.SessionID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"session": session, "preview": transcript}}, nil
	case methods.MethodSessionsPatch:
		req, err := methods.DecodeSessionsPatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		session, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, req.Meta)
			return nil
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session": session}}, nil
	case methods.MethodSessionsReset:
		req, err := methods.DecodeSessionsResetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		var session state.SessionDoc
		if chatCancels != nil {
			chatCancels.Abort(req.SessionID)
		}
		err = withExclusiveSessionTurn(ctx, req.SessionID, 15*time.Second, func() error {
			var innerErr error
			session, innerErr = updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.LastInboundAt = 0
				session.LastReplyAt = 0
				session.Meta = map[string]any{}
				return nil
			})
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Fire hook event.
		if controlHooksMgr != nil {
			go controlHooksMgr.Fire("command:reset", req.SessionID, map[string]any{})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session": session}}, nil
	case methods.MethodSessionsDelete:
		req, err := methods.DecodeSessionsDeleteParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if chatCancels != nil {
			chatCancels.Abort(req.SessionID)
		}
		err = withExclusiveSessionTurn(ctx, req.SessionID, 15*time.Second, func() error {
			_, innerErr := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{"deleted": true, "deleted_at": time.Now().Unix()})
				return nil
			})
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "deleted": true})}, nil
	case methods.MethodSessionsCompact:
		req, err := methods.DecodeSessionsCompactParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		var compactResult map[string]any
		if chatCancels != nil {
			chatCancels.Abort(req.SessionID)
		}
		err = withExclusiveSessionTurn(ctx, req.SessionID, 15*time.Second, func() error {
			if _, err := docsRepo.GetSession(ctx, req.SessionID); err != nil {
				return err
			}
			flushOutcome, err := ensureSessionMemoryCurrent(ctx, configState.Get(), req.SessionID, controlSessionStore)
			if err != nil {
				return fmt.Errorf("sessions.compact session memory flush: %w", err)
			}
			entries, err := transcriptRepo.ListSessionAll(ctx, req.SessionID)
			if err != nil {
				return err
			}
			dropped := len(entries) - req.Keep
			if dropped < 0 {
				dropped = 0
			}

			// ── LLM summary generation ─────────────────────────────────────────
			// Before tombstoning, generate a compact summary of the entries that
			// are about to be removed and inject it as a system-role entry.
			summaryGenerated := false
			activeAgentID, summaryRuntime := resolveInboundChannelRuntime("", req.SessionID)
			if dropped > 0 && summaryRuntime != nil {
				compactedEntries := entries[:dropped]
				var sb strings.Builder
				for _, e := range compactedEntries {
					if e.Role == "deleted" {
						continue
					}
					sb.WriteString(e.Role)
					sb.WriteString(": ")
					text := e.Text
					if len(text) > 400 {
						text = text[:400] + "…"
					}
					sb.WriteString(text)
					sb.WriteString("\n")
				}
				snippet := sb.String()
				if len(snippet) > 6000 {
					snippet = snippet[:6000] + "…"
				}
				if snippet != "" {
					summaryPrompt := "You are a session-memory assistant. Summarize the following conversation history concisely in 2-4 sentences, capturing the key topics, decisions, and context needed to continue the conversation later. Do NOT include greetings or meta-commentary; only output the summary.\n\n" + snippet
					selectedRuntime := summaryRuntime
					usedAuxiliaryRuntime := false
					if agCfg, ok := resolveAgentConfigByID(cfg, activeAgentID); ok {
						if auxiliaryModel := resolveAuxiliaryModelForAgent(agCfg, auxiliaryModelUseCaseCompaction); auxiliaryModel != "" {
							lightRuntime, lightErr := buildRuntimeForAgentModel(cfg, agCfg, auxiliaryModel, controlToolRegistry)
							if lightErr != nil {
								log.Printf("sessions.compact: light summary runtime unavailable agent=%s model=%q err=%v", activeAgentID, auxiliaryModel, lightErr)
							} else if lightRuntime != nil {
								selectedRuntime = lightRuntime
								usedAuxiliaryRuntime = true
							}
						}
					}
					runSummary := func(rt agent.Runtime) (agent.TurnResult, error) {
						summaryCtx, summaryCancel := context.WithTimeout(ctx, 30*time.Second)
						defer summaryCancel()
						return rt.ProcessTurn(summaryCtx, agent.Turn{
							SessionID:           req.SessionID + ":compact",
							UserText:            summaryPrompt,
							ContextWindowTokens: maxContextTokensForAgent(configState.Get(), activeAgentID),
						})
					}
					result, summaryErr := runSummary(selectedRuntime)
					if summaryErr != nil && usedAuxiliaryRuntime && summaryRuntime != nil {
						log.Printf("sessions.compact: light summary failed agent=%s err=%v — retrying primary runtime", activeAgentID, summaryErr)
						result, summaryErr = runSummary(summaryRuntime)
					}
					if summaryErr == nil && strings.TrimSpace(result.Text) != "" {
						summaryEntryID := fmt.Sprintf("compact-summary-%d", time.Now().UnixMilli())
						summaryEntry := state.TranscriptEntryDoc{
							Version:   1,
							SessionID: req.SessionID,
							EntryID:   summaryEntryID,
							Role:      "system",
							Text:      "[Compact summary of " + strconv.Itoa(dropped) + " earlier messages]\n\n" + strings.TrimSpace(result.Text),
							Unix:      time.Now().Unix(),
							Meta:      map[string]any{"compact": true, "compact_from": dropped},
						}
						if _, putErr := transcriptRepo.PutEntry(ctx, summaryEntry); putErr != nil {
							log.Printf("sessions.compact: insert summary entry: %v", putErr)
						} else {
							summaryGenerated = true
						}
					} else if summaryErr != nil {
						log.Printf("sessions.compact: LLM summary skipped: %v", summaryErr)
					}
				}
			}

			deleteErrors := 0
			for i := 0; i < dropped; i++ {
				if delErr := transcriptRepo.DeleteEntry(ctx, req.SessionID, entries[i].EntryID); delErr != nil {
					log.Printf("sessions.compact: delete entry %s: %v", entries[i].EntryID, delErr)
					deleteErrors++
				}
			}
			if _, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{
					"compacted_at":              time.Now().Unix(),
					"compacted_keep":            req.Keep,
					"compacted_from_entries":    len(entries),
					"compacted_dropped_entries": dropped - deleteErrors,
					"compacted_summary":         summaryGenerated,
				})
				return nil
			}); err != nil {
				return err
			}
			compactResult = methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "kept": req.Keep, "from_entries": len(entries), "dropped": dropped - deleteErrors, "summary_generated": summaryGenerated})
			recordSessionCompaction(controlSessionStore, req.SessionID, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
			return nil
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: compactResult}, nil
	case methods.MethodSessionsExport:
		var exportReq methods.SessionsExportRequest
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &exportReq)
		}
		if exportReq.SessionID == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("sessions.export: session_id is required")
		}
		exportFormat := strings.ToLower(strings.TrimSpace(exportReq.Format))
		if exportFormat == "" {
			exportFormat = "html"
		}
		if exportFormat != "html" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("sessions.export: unsupported format %q (only 'html' is supported)", exportFormat)
		}
		// Load transcript entries for the session.
		entries, err := transcriptRepo.ListSessionAll(ctx, exportReq.SessionID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("sessions.export: load transcript: %w", err)
		}
		msgs := make([]exportpkg.Message, 0, len(entries))
		for _, e := range entries {
			if e.Role == "deleted" || e.Role == "" {
				continue
			}
			msgs = append(msgs, exportpkg.Message{
				Role:      e.Role,
				Content:   e.Text,
				Timestamp: e.Unix,
				ID:        e.EntryID,
			})
		}
		// Resolve agent name from registry.
		agentName := ""
		if agDoc, err2 := docsRepo.GetAgent(ctx, "main"); err2 == nil {
			agentName = agDoc.Name
		}
		htmlOut, err := exportpkg.SessionToHTML(exportpkg.SessionHTMLOptions{
			SessionID:  exportReq.SessionID,
			AgentID:    "main",
			AgentName:  agentName,
			PubKey:     in.FromPubKey,
			Messages:   msgs,
			ExportedAt: time.Now(),
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("sessions.export: render: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: methods.SessionsExportResponse{HTML: htmlOut, Format: "html"}}, nil

	case methods.MethodSessionsPrune:
		var pruneReq methods.SessionsPruneRequest
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &pruneReq)
		}
		result, err := runSessionsPrune(ctx, docsRepo, transcriptRepo, pruneReq, "manual")
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: result}, nil

	case methods.MethodSessionsSpawn:
		req, err := methods.DecodeSessionsSpawnParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySessionsSpawn(ctx, req, cfg, docsRepo, memoryIndex)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTasksCreate:
		req, err := methods.DecodeTasksCreateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		result, err := createControlTask(ctx, docsRepo, req, in.FromPubKey, time.Now())
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodTasksGet:
		req, err := methods.DecodeTasksGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		result, err := buildControlTaskResponse(ctx, docsRepo, req.TaskID, req.RunsLimit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodTasksList:
		req, err := methods.DecodeTasksListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		result, err := listControlTasks(ctx, docsRepo, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodTasksCancel:
		req, err := methods.DecodeTasksCancelParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		result, err := cancelControlTask(ctx, docsRepo, req, in.FromPubKey, time.Now())
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodTasksResume:
		req, err := methods.DecodeTasksResumeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		result, err := resumeControlTask(ctx, docsRepo, req, in.FromPubKey, time.Now())
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: result}, nil

	case methods.MethodTasksDoctor:
		req, err := methods.DecodeTasksDoctorParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if docsRepo == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("docs repository is nil")
		}
		task, err := docsRepo.GetTask(ctx, req.TaskID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		runs, err := docsRepo.ListTaskRuns(ctx, task.TaskID, req.RunsLimit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		diag := methods.BuildTaskDiagnostic(task, runs, time.Now())
		return nostruntime.ControlRPCResult{Result: methods.TasksDoctorResponse{Task: task, Runs: runs, Doctor: diag}}, nil
	case methods.MethodTasksTrace:
		req, err := methods.DecodeTasksTraceParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if docsRepo == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("docs repository is nil")
		}
		task, err := docsRepo.GetTask(ctx, req.TaskID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		runs, err := docsRepo.ListTaskRuns(ctx, task.TaskID, 100)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Collect memory recall and turn telemetry from session store,
		// filtering each record by TaskID to avoid mixing data from
		// sessions that served multiple tasks.
		var turnTelemetry []state.TurnTelemetry
		var memoryRecall []state.MemoryRecallSample
		if controlSessionStore != nil {
			for sessID, entry := range controlSessionStore.List() {
				if strings.TrimSpace(entry.ActiveTaskID) != req.TaskID && strings.TrimSpace(entry.ParentTaskID) != req.TaskID && sessID != strings.TrimSpace(task.SessionID) {
					continue
				}
				if entry.LastTurn != nil && strings.TrimSpace(entry.LastTurn.TaskID) == req.TaskID {
					turnTelemetry = append(turnTelemetry, *entry.LastTurn)
				}
				for _, sample := range entry.RecentMemoryRecall {
					if strings.TrimSpace(sample.TaskID) == req.TaskID {
						memoryRecall = append(memoryRecall, sample)
					}
				}
			}
		}
		traceInput := methods.TraceInput{
			Task:          task,
			Runs:          runs,
			TurnTelemetry: turnTelemetry,
			MemoryRecall:  memoryRecall,
		}
		traceResp := methods.AssembleTaskTrace(traceInput, req.RunID, req.Limit)
		return nostruntime.ControlRPCResult{Result: traceResp}, nil
	case methods.MethodTasksAuditExport:
		req, err := methods.DecodeAuditExportParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if docsRepo == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("docs repository is nil")
		}
		var scopeTasks []state.TaskSpec
		if req.TaskID != "" {
			allTasks, err := docsRepo.ListTasks(ctx, 5000)
			if err != nil {
				return nostruntime.ControlRPCResult{}, err
			}
			scopeTasks = methods.CollectDescendants(req.TaskID, allTasks)
			if len(scopeTasks) == 0 {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("task %q not found", req.TaskID)
			}
		} else {
			allTasks, err := docsRepo.ListTasks(ctx, 5000)
			if err != nil {
				return nostruntime.ControlRPCResult{}, err
			}
			for _, t := range allTasks {
				if strings.TrimSpace(t.GoalID) == req.GoalID {
					scopeTasks = append(scopeTasks, t)
				}
			}
		}
		runsByTask := make(map[string][]state.TaskRun, len(scopeTasks))
		for _, t := range scopeTasks {
			runs, err := docsRepo.ListTaskRuns(ctx, t.TaskID, req.RunsLimit)
			if err != nil {
				return nostruntime.ControlRPCResult{}, err
			}
			if len(runs) > 0 {
				runsByTask[t.TaskID] = runs
			}
		}
		bundle := methods.BuildAuditBundle(scopeTasks, runsByTask, req, in.FromPubKey, time.Now())
		return nostruntime.ControlRPCResult{Result: bundle}, nil
	case methods.MethodTasksSummary:
		req, err := methods.DecodeTasksSummaryParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if docsRepo == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("docs repository is nil")
		}
		fetchLimit := 5000
		tasks, err := docsRepo.ListTasks(ctx, fetchLimit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if req.GoalID != "" {
			var filtered []state.TaskSpec
			for _, t := range tasks {
				if strings.TrimSpace(t.GoalID) == req.GoalID {
					filtered = append(filtered, t)
				}
			}
			tasks = filtered
		}
		summary := methods.BuildTasksSummary(tasks)
		return nostruntime.ControlRPCResult{Result: summary}, nil

	case methods.MethodAgentsList:
		req, err := methods.DecodeAgentsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agents, err := docsRepo.ListAgents(ctx, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"agents": agents}}, nil
	case methods.MethodAgentsCreate:
		req, err := methods.DecodeAgentsCreateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.GetAgent(ctx, req.AgentID); err == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agent %q already exists", req.AgentID)
		} else if !errors.Is(err, state.ErrNotFound) {
			return nostruntime.ControlRPCResult{}, err
		}
		doc := state.AgentDoc{Version: 1, AgentID: req.AgentID, Name: req.Name, Workspace: req.Workspace, Model: req.Model, Meta: req.Meta}
		if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Register a runtime for the new agent (best-effort; falls back to default on failure).
		if controlAgentRegistry != nil {
			if rt, rtErr := agent.BuildRuntimeForModel(req.Model, tools); rtErr == nil {
				controlAgentRegistry.Set(req.AgentID, rt)
			} else {
				log.Printf("agents.create: runtime build warning id=%s model=%q err=%v", req.AgentID, req.Model, rtErr)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent": doc}}, nil
	case methods.MethodAgentsUpdate:
		req, err := methods.DecodeAgentsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		doc, err := docsRepo.GetAgent(ctx, req.AgentID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if req.Name != "" {
			doc.Name = req.Name
		}
		if req.Workspace != "" {
			doc.Workspace = req.Workspace
		}
		if req.Model != "" {
			doc.Model = req.Model
		}
		doc.Meta = mergeSessionMeta(doc.Meta, req.Meta)
		if doc.Version == 0 {
			doc.Version = 1
		}
		if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Rebuild the runtime if the model changed.
		if controlAgentRegistry != nil && req.Model != "" {
			if rt, rtErr := agent.BuildRuntimeForModel(doc.Model, tools); rtErr == nil {
				controlAgentRegistry.Set(req.AgentID, rt)
			} else {
				log.Printf("agents.update: runtime rebuild warning id=%s model=%q err=%v", req.AgentID, doc.Model, rtErr)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent": doc}}, nil
	case methods.MethodAgentsDelete:
		req, err := methods.DecodeAgentsDeleteParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		doc, err := docsRepo.GetAgent(ctx, req.AgentID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		doc.Deleted = true
		doc.Meta = mergeSessionMeta(doc.Meta, map[string]any{"deleted_at": time.Now().Unix()})
		if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Remove the runtime and any session assignments for the deleted agent.
		if controlAgentRegistry != nil {
			controlAgentRegistry.Remove(req.AgentID)
		}
		if controlSessionRouter != nil {
			// Un-assign all sessions that were using this agent.
			for sessionID, aid := range controlSessionRouter.List() {
				if aid == req.AgentID {
					controlSessionRouter.Unassign(sessionID)
				}
			}
		}
		// Hard cleanup: remove persisted session.meta["agent_id"] references to
		// this agent so routes do not reappear after restarts.
		sessions, sessErr := docsRepo.ListSessions(ctx, 5000)
		if sessErr != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agents.delete: list sessions for cleanup: %w", sessErr)
		}
		for _, sess := range sessions {
			if sess.Meta == nil {
				continue
			}
			aid, _ := sess.Meta["agent_id"].(string)
			if aid != req.AgentID {
				continue
			}
			sessionID := strings.TrimSpace(sess.SessionID)
			if sessionID == "" {
				continue
			}
			if _, err := updateExistingSessionDoc(ctx, docsRepo, sessionID, sess.PeerPubKey, func(session *state.SessionDoc) error {
				if session.Meta != nil {
					delete(session.Meta, "agent_id")
				}
				return nil
			}); err != nil {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("agents.delete: cleanup session %q: %w", sessionID, err)
			}
			if controlSessionRouter != nil {
				controlSessionRouter.Unassign(sessionID)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent_id": req.AgentID, "deleted": true}}, nil
	case methods.MethodAgentsAssign:
		req, err := methods.DecodeAgentsAssignParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Validate that the target agent exists and is not deleted.
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlSessionRouter != nil {
			controlSessionRouter.Assign(req.SessionID, req.AgentID)
		}
		// Persist assignment in session meta so it survives restarts.
		persisted := true
		if err := updateSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, map[string]any{"agent_id": req.AgentID})
			return nil
		}); err != nil {
			persisted = false
			log.Printf("agents.assign: persist session meta warning session=%s err=%v", req.SessionID, err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":         true,
			"agent_id":   req.AgentID,
			"persisted":  persisted,
			"durability": "best_effort",
		}}, nil
	case methods.MethodAgentsUnassign:
		req, err := methods.DecodeAgentsUnassignParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlSessionRouter != nil {
			controlSessionRouter.Unassign(req.SessionID)
		}
		// Remove the persisted agent_id from session meta.
		persisted := true
		if _, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			if session.Meta != nil {
				delete(session.Meta, "agent_id")
			}
			return nil
		}); err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				persisted = false
				log.Printf("agents.unassign: load session warning session=%s err=%v", req.SessionID, err)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":         true,
			"session_id": req.SessionID,
			"persisted":  persisted,
			"durability": "best_effort",
		}}, nil
	case methods.MethodAgentsActive:
		req, err := methods.DecodeAgentsActiveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		var registered []string
		if controlAgentRegistry != nil {
			registered = controlAgentRegistry.Registered()
			sort.Strings(registered)
		}
		var assignments []map[string]any
		if controlSessionRouter != nil {
			for sessionID, agentID := range controlSessionRouter.List() {
				assignments = append(assignments, map[string]any{
					"session_id": sessionID,
					"agent_id":   agentID,
				})
			}
			sortRecordsByKeyDesc(assignments, "session_id")
		}
		if req.Limit > 0 && len(assignments) > req.Limit {
			assignments = assignments[:req.Limit]
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"registered":  registered,
			"assignments": assignments,
		}}, nil
	case methods.MethodAgentsFilesList:
		req, err := methods.DecodeAgentsFilesListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		files, err := docsRepo.ListAgentFiles(ctx, req.AgentID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out := make([]map[string]any, 0, len(files))
		for _, file := range files {
			out = append(out, map[string]any{"name": file.Name, "size": len(file.Content)})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"agent_id": req.AgentID, "files": out}}, nil
	case methods.MethodAgentsFilesGet:
		req, err := methods.DecodeAgentsFilesGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		file, err := docsRepo.GetAgentFile(ctx, req.AgentID, req.Name)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{Result: map[string]any{"agent_id": req.AgentID, "file": map[string]any{"name": req.Name, "missing": true}}}, nil
			}
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"agent_id": req.AgentID, "file": map[string]any{"name": file.Name, "missing": false, "content": file.Content}}}, nil
	case methods.MethodAgentsFilesSet:
		req, err := methods.DecodeAgentsFilesSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		doc := state.AgentFileDoc{Version: 1, AgentID: req.AgentID, Name: req.Name, Content: req.Content}
		if _, err := docsRepo.PutAgentFile(ctx, req.AgentID, req.Name, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent_id": req.AgentID, "file": map[string]any{"name": req.Name, "missing": false, "content": req.Content}}}, nil
	case methods.MethodModelsList:
		req, err := methods.DecodeModelsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"models": defaultModelsCatalog(cfg.Providers)}}, nil
	case methods.MethodToolsCatalog:
		req, err := methods.DecodeToolsCatalogParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agentID := defaultAgentID(req.AgentID)
		groups := buildToolCatalogGroups(cfg, tools, req.IncludePlugins, pluginMgr)
		if req.Profile != nil && *req.Profile != "" {
			profileID := strings.TrimSpace(strings.ToLower(*req.Profile))
			if agent.LookupProfile(profileID) == nil {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("unknown profile %q; valid: %s", profileID, strings.Join(agent.ProfileListSorted(), ", "))
			}
			groups = agent.FilterCatalogByProfile(groups, profileID)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profiles": defaultToolProfiles(), "groups": groups})}, nil
	case methods.MethodToolsProfileGet:
		req, err := methods.DecodeToolsProfileGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agentID := defaultAgentID(req.AgentID)
		doc, _ := docsRepo.GetAgent(ctx, agentID)
		profileID := agent.DefaultProfile
		if p, ok := doc.Meta[agent.AgentProfileKey].(string); ok && p != "" {
			profileID = p
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profile": profileID})}, nil
	case methods.MethodToolsProfileSet:
		req, err := methods.DecodeToolsProfileSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if agent.LookupProfile(req.Profile) == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("unknown profile %q; valid: %s", req.Profile, strings.Join(agent.ProfileListSorted(), ", "))
		}
		agentID := defaultAgentID(req.AgentID)
		doc, _ := docsRepo.GetAgent(ctx, agentID)
		if doc.AgentID == "" {
			doc = state.AgentDoc{Version: 1, AgentID: agentID}
		}
		if doc.Meta == nil {
			doc.Meta = map[string]any{}
		}
		doc.Meta[agent.AgentProfileKey] = req.Profile
		if _, err := docsRepo.PutAgent(ctx, agentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profile": req.Profile})}, nil
	case methods.MethodSkillsStatus:
		req, err := methods.DecodeSkillsStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agentID := defaultAgentID(req.AgentID)
		return nostruntime.ControlRPCResult{Result: buildSkillsStatusReport(cfg, agentID)}, nil
	case methods.MethodSkillsBins:
		req, err := methods.DecodeSkillsBinsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: applySkillsBins(cfg)}, nil
	case methods.MethodSkillsInstall:
		req, err := methods.DecodeSkillsInstallParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		_, installResult, err := applySkillInstall(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: installResult}, nil
	case methods.MethodSkillsUpdate:
		req, err := methods.DecodeSkillsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		_, entry, err := applySkillUpdate(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "skillKey": strings.ToLower(strings.TrimSpace(req.SkillKey)), "config": entry}}, nil
	case methods.MethodPluginsInstall:
		req, err := methods.DecodePluginsInstallParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyPluginInstallRuntime(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsUninstall:
		req, err := methods.DecodePluginsUninstallParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyPluginUninstallRuntime(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsUpdate:
		req, err := methods.DecodePluginsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyPluginUpdateRuntime(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsRegistryList:
		req, err := methods.DecodePluginsRegistryListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := handlePluginsRegistryList(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsRegistryGet:
		req, err := methods.DecodePluginsRegistryGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := handlePluginsRegistryGet(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsRegistrySearch:
		req, err := methods.DecodePluginsRegistrySearchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := handlePluginsRegistrySearch(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairRequest:
		req, err := methods.DecodeNodePairRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairRequest(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		requestID := ""
		if id, ok := out["request_id"].(string); ok {
			requestID = id
		}
		emitControlWSEvent(gatewayws.EventNodePairRequested, gatewayws.NodePairRequestedPayload{
			TS:        time.Now().UnixMilli(),
			RequestID: requestID,
			Label:     req.DisplayName,
		})
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairList:
		req, err := methods.DecodeNodePairListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairList(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairApprove:
		req, err := methods.DecodeNodePairApproveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairApprove(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		nodeID := ""
		approvalToken := ""
		if node, ok := out["node"].(map[string]any); ok {
			if id, ok := node["node_id"].(string); ok {
				nodeID = id
			}
			if tok, ok := node["token"].(string); ok {
				approvalToken = tok
			}
		}
		emitControlWSEvent(gatewayws.EventNodePairResolved, gatewayws.NodePairResolvedPayload{
			TS:        time.Now().UnixMilli(),
			RequestID: req.RequestID,
			NodeID:    nodeID,
			Decision:  "approved",
		})
		// Notify the node via NIP-17 DM if node_id looks like a Nostr pubkey.
		if nodeID != "" && approvalToken != "" {
			go sendControlDM(ctx, nodeID, fmt.Sprintf(`{"type":"pair.approved","request_id":%q,"token":%q}`, req.RequestID, approvalToken))
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairReject:
		req, err := methods.DecodeNodePairRejectParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairReject(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		nodeID := ""
		if id, ok := out["node_id"].(string); ok {
			nodeID = id
		}
		emitControlWSEvent(gatewayws.EventNodePairResolved, gatewayws.NodePairResolvedPayload{
			TS:        time.Now().UnixMilli(),
			RequestID: req.RequestID,
			NodeID:    nodeID,
			Decision:  "rejected",
		})
		// Notify the node via NIP-17 DM if node_id looks like a Nostr pubkey.
		if nodeID != "" {
			go sendControlDM(ctx, nodeID, fmt.Sprintf(`{"type":"pair.rejected","request_id":%q}`, req.RequestID))
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairVerify:
		req, err := methods.DecodeNodePairVerifyParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairVerify(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDevicePairList:
		req, err := methods.DecodeDevicePairListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDevicePairList(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDevicePairApprove:
		req, err := methods.DecodeDevicePairApproveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDevicePairApprove(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		deviceID := ""
		label := ""
		if device, ok := out["device"].(map[string]any); ok {
			if id, ok := device["id"].(string); ok {
				deviceID = id
			}
			if l, ok := device["label"].(string); ok {
				label = l
			}
		}
		emitControlWSEvent(gatewayws.EventDevicePairResolved, gatewayws.DevicePairResolvedPayload{
			TS:       time.Now().UnixMilli(),
			DeviceID: deviceID,
			Label:    label,
			Decision: "approved",
		})
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDevicePairReject:
		req, err := methods.DecodeDevicePairRejectParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDevicePairReject(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		deviceID := ""
		if device, ok := out["device"].(map[string]any); ok {
			if id, ok := device["id"].(string); ok {
				deviceID = id
			}
		}
		emitControlWSEvent(gatewayws.EventDevicePairResolved, gatewayws.DevicePairResolvedPayload{
			TS:       time.Now().UnixMilli(),
			DeviceID: deviceID,
			Decision: "rejected",
		})
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDevicePairRemove:
		req, err := methods.DecodeDevicePairRemoveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDevicePairRemove(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDeviceTokenRotate:
		req, err := methods.DecodeDeviceTokenRotateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDeviceTokenRotate(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDeviceTokenRevoke:
		req, err := methods.DecodeDeviceTokenRevokeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDeviceTokenRevoke(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeList:
		req, err := methods.DecodeNodeListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeList(configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeDescribe:
		req, err := methods.DecodeNodeDescribeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeDescribe(configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeRename:
		req, err := methods.DecodeNodeRenameParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeRename(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeCanvasCapabilityRefresh:
		req, err := methods.DecodeNodeCanvasCapabilityRefreshParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeCanvasCapabilityRefresh(configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeInvoke:
		req, err := methods.DecodeNodeInvokeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeInvoke(controlNodeInvocations, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Dispatch the invocation to the target node via NIP-17 DM if its
		// node_id looks like a Nostr pubkey (hex or npub).
		if req.NodeID != "" {
			runID, _ := out["run_id"].(string)
			payload, _ := json.Marshal(map[string]any{
				"type":    "node.invoke",
				"run_id":  runID,
				"command": req.Command,
				"args":    req.Args,
			})
			go sendControlDM(ctx, req.NodeID, string(payload))
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeEvent:
		req, err := methods.DecodeNodeEventParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeEvent(controlNodeInvocations, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeResult, methods.MethodNodeInvokeResult:
		req, err := methods.DecodeNodeResultParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeResult(controlNodeInvocations, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePendingEnqueue:
		req, err := methods.DecodeNodePendingEnqueueParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := controlNodePending.Enqueue(nodepending.EnqueueRequest{NodeID: req.NodeID, Command: req.Command, Args: req.Args, IdempotencyKey: req.IdempotencyKey, TTLMS: req.TTLMS})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePendingPull:
		req, err := methods.DecodeNodePendingPullParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := controlNodePending.Pull(req.NodeID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePendingAck:
		req, err := methods.DecodeNodePendingAckParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := controlNodePending.Ack(nodepending.AckRequest{NodeID: req.NodeID, IDs: req.IDs})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePendingDrain:
		req, err := methods.DecodeNodePendingDrainParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := controlNodePending.Drain(nodepending.DrainRequest{NodeID: req.NodeID, MaxItems: req.MaxItems})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCanvasGet:
		var req methods.CanvasGetRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("invalid params: %w", err)
		}
		c := controlCanvas.GetCanvas(req.ID)
		if c == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("canvas %q not found", req.ID)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"canvas": c}}, nil
	case methods.MethodCanvasList:
		canvases := controlCanvas.ListCanvases()
		return nostruntime.ControlRPCResult{Result: map[string]any{"canvases": canvases, "count": len(canvases)}}, nil
	case methods.MethodCanvasUpdate:
		var req methods.CanvasUpdateRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("invalid params: %w", err)
		}
		if err := controlCanvas.UpdateCanvas(req.ID, req.ContentType, req.Data); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "canvas_id": req.ID}}, nil
	case methods.MethodCanvasDelete:
		var req methods.CanvasDeleteRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("invalid params: %w", err)
		}
		removed := controlCanvas.DeleteCanvas(req.ID)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "removed": removed, "canvas_id": req.ID}}, nil
	case methods.MethodCronList:
		req, err := methods.DecodeCronListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronList(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronStatus:
		req, err := methods.DecodeCronStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronStatus(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronAdd:
		req, err := methods.DecodeCronAddParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronAdd(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron jobs save warning (add): %v", saveErr)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronUpdate:
		req, err := methods.DecodeCronUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronUpdate(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron jobs save warning (update): %v", saveErr)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronRemove:
		req, err := methods.DecodeCronRemoveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronRemove(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron jobs save warning (remove): %v", saveErr)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronRun:
		req, err := methods.DecodeCronRunParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronRun(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronRuns:
		req, err := methods.DecodeCronRunsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronRuns(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalsGet:
		req, err := methods.DecodeExecApprovalsGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalsGet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalsSet:
		req, err := methods.DecodeExecApprovalsSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalsSet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalsNodeGet:
		req, err := methods.DecodeExecApprovalsNodeGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalsNodeGet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalsNodeSet:
		req, err := methods.DecodeExecApprovalsNodeSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalsNodeSet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalRequest:
		req, err := methods.DecodeExecApprovalRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalRequest(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalWaitDecision:
		req, err := methods.DecodeExecApprovalWaitDecisionParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalWaitDecision(ctx, controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalResolve:
		req, err := methods.DecodeExecApprovalResolveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalResolve(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSandboxRun:
		req, err := methods.DecodeSandboxRunParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySandboxRun(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPList:
		req, err := methods.DecodeMCPListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyList(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPGet:
		req, err := methods.DecodeMCPGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyGet(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPPut:
		req, err := methods.DecodeMCPPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyPut(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPRemove:
		req, err := methods.DecodeMCPRemoveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyRemove(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPTest:
		req, err := methods.DecodeMCPTestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyTest(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPReconnect:
		req, err := methods.DecodeMCPReconnectParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyReconnect(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPAuthStart:
		req, err := methods.DecodeMCPAuthStartParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPAuth == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp auth not configured")
		}
		out, err := controlMCPAuth.applyStart(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPAuthRefresh:
		req, err := methods.DecodeMCPAuthRefreshParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPAuth == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp auth not configured")
		}
		out, err := controlMCPAuth.applyRefresh(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPAuthClear:
		req, err := methods.DecodeMCPAuthClearParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPAuth == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp auth not configured")
		}
		out, err := controlMCPAuth.applyClear(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSecretsReload:
		req, err := methods.DecodeSecretsReloadParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySecretsReload(req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSecretsResolve:
		req, err := methods.DecodeSecretsResolveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySecretsResolve(req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWizardStart:
		req, err := methods.DecodeWizardStartParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWizardStart(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWizardNext:
		req, err := methods.DecodeWizardNextParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWizardNext(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWizardCancel:
		req, err := methods.DecodeWizardCancelParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWizardCancel(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWizardStatus:
		req, err := methods.DecodeWizardStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWizardStatus(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodUpdateRun:
		req, err := methods.DecodeUpdateRunParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyUpdateRun(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTalkConfig:
		req, err := methods.DecodeTalkConfigParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTalkConfig(cfg, controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTalkMode:
		req, err := methods.DecodeTalkModeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTalkMode(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodLastHeartbeat:
		req, err := methods.DecodeLastHeartbeatParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyLastHeartbeat(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSetHeartbeats:
		req, err := methods.DecodeSetHeartbeatsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySetHeartbeats(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWake:
		req, err := methods.DecodeWakeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWake(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSystemPresence:
		req, err := methods.DecodeSystemPresenceParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySystemPresence(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSystemEvent:
		req, err := methods.DecodeSystemEventParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySystemEvent(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSend:
		req, err := methods.DecodeSendParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySend(ctx, dmBus, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodBrowserRequest:
		req, err := methods.DecodeBrowserRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyBrowserRequest(req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodVoicewakeGet:
		req, err := methods.DecodeVoicewakeGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyVoicewakeGet(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodVoicewakeSet:
		req, err := methods.DecodeVoicewakeSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyVoicewakeSet(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSStatus:
		req, err := methods.DecodeTTSStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSStatus(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSProviders:
		req, err := methods.DecodeTTSProvidersParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSProviders(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSSetProvider:
		req, err := methods.DecodeTTSSetProviderParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSSetProvider(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSEnable:
		req, err := methods.DecodeTTSEnableParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSEnable(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSDisable:
		req, err := methods.DecodeTTSDisableParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSDisable(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSConvert:
		req, err := methods.DecodeTTSConvertParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSConvert(ctx, controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil

	// ── Hooks ────────────────────────────────────────────────────────────────
	case methods.MethodHooksList:
		var statuses []map[string]any
		if controlHooksMgr != nil {
			for _, s := range controlHooksMgr.List() {
				statuses = append(statuses, hookspkg.StatusToMap(s))
			}
		}
		if statuses == nil {
			statuses = []map[string]any{}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"hooks": statuses}}, nil

	case methods.MethodHooksEnable:
		var req struct {
			HookKey string `json:"hookKey"`
			Key     string `json:"key"`
		}
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &req)
		}
		key := req.HookKey
		if key == "" {
			key = req.Key
		}
		if key == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hookKey is required")
		}
		if controlHooksMgr != nil {
			controlHooksMgr.SetEnabled(key, true)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hookKey": key, "enabled": true}}, nil

	case methods.MethodHooksDisable:
		var req struct {
			HookKey string `json:"hookKey"`
			Key     string `json:"key"`
		}
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &req)
		}
		key := req.HookKey
		if key == "" {
			key = req.Key
		}
		if key == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hookKey is required")
		}
		if controlHooksMgr != nil {
			controlHooksMgr.SetEnabled(key, false)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hookKey": key, "enabled": false}}, nil

	case methods.MethodHooksInfo:
		var req struct {
			HookKey string `json:"hookKey"`
			Key     string `json:"key"`
		}
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &req)
		}
		key := req.HookKey
		if key == "" {
			key = req.Key
		}
		if key == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hookKey is required")
		}
		if controlHooksMgr == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hook %q not found", key)
		}
		info := controlHooksMgr.Info(key)
		if info == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hook %q not found", key)
		}
		return nostruntime.ControlRPCResult{Result: hookspkg.StatusToMap(*info)}, nil

	case methods.MethodHooksCheck:
		var statuses []map[string]any
		if controlHooksMgr != nil {
			for _, s := range controlHooksMgr.List() {
				statuses = append(statuses, hookspkg.StatusToMap(s))
			}
		}
		if statuses == nil {
			statuses = []map[string]any{}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"hooks":      statuses,
			"totalHooks": len(statuses),
			"eligible":   countEligible(statuses),
		}}, nil

	case methods.MethodConfigGet:
		redacted := config.Redact(cfg)
		// Include base_hash so OpenClaw clients can use optimistic-lock semantics on mutations.
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"config":    redacted,
			"hash":      cfg.Hash(),
			"base_hash": cfg.Hash(),
		}}, nil
	case methods.MethodRelayPolicyGet:
		dmRelays := []string{}
		controlRelays := []string{}
		if dmBus != nil {
			dmRelays = dmBus.Relays()
		}
		if controlBus != nil {
			controlRelays = controlBus.Relays()
		}
		return nostruntime.ControlRPCResult{Result: methods.RelayPolicyResponse{
			ReadRelays:           append([]string{}, cfg.Relays.Read...),
			WriteRelays:          append([]string{}, cfg.Relays.Write...),
			RuntimeDMRelays:      dmRelays,
			RuntimeControlRelays: controlRelays,
		}}, nil
	case methods.MethodListGet:
		req, err := methods.DecodeListGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		list, err := docsRepo.GetList(ctx, req.Name)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: list}, nil
	case methods.MethodListPut:
		req, err := methods.DecodeListPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if req.ExpectedVersionSet || req.ExpectedEvent != "" {
			current, evt, err := docsRepo.GetListWithEvent(ctx, req.Name)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					if req.ExpectedVersionSet && req.ExpectedVersion == 0 && req.ExpectedEvent == "" {
						goto controlListPreconditionsSatisfied
					}
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nostruntime.ControlRPCResult{}, err
			}
			if req.ExpectedVersionSet {
				if req.ExpectedVersion == 0 {
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				} else if current.Version != req.ExpectedVersion {
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
					Resource:        "list:" + req.Name,
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
	controlListPreconditionsSatisfied:
		newVersion := 1
		if req.ExpectedVersionSet && req.ExpectedVersion > 0 {
			newVersion = req.ExpectedVersion + 1
		}
		if _, err := docsRepo.PutList(ctx, req.Name, state.ListDoc{Version: newVersion, Name: req.Name, Items: req.Items}); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil
	case methods.MethodConfigPut:
		req, err := methods.DecodeConfigPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if req.ExpectedVersionSet || req.ExpectedEvent != "" {
			current, evt, err := docsRepo.GetConfigWithEvent(ctx)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					if req.ExpectedVersionSet && req.ExpectedVersion == 0 && req.ExpectedEvent == "" {
						goto controlConfigPreconditionsSatisfied
					}
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nostruntime.ControlRPCResult{}, err
			}
			if req.ExpectedVersionSet {
				if req.ExpectedVersion == 0 {
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				} else if current.Version != req.ExpectedVersion {
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
					Resource:        "config",
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
	controlConfigPreconditionsSatisfied:
		req.Config = policy.NormalizeConfig(req.Config)
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := policy.ValidateConfig(req.Config); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		newVersion := 1
		if req.ExpectedVersionSet && req.ExpectedVersion > 0 {
			newVersion = req.ExpectedVersion + 1
		}
		req.Config.Version = newVersion
		if err := persistRuntimeConfigFile(req.Config); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.PutConfig(ctx, req.Config); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		configState.Set(req.Config)
		restartPending := scheduleRestartIfNeeded(cfg, req.Config, 0)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": req.Config.Hash(), "restart_pending": restartPending}}, nil
	case methods.MethodConfigSet:
		req, err := methods.DecodeConfigSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next, err := methods.ApplyConfigSet(cfg, req.Key, req.Value)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next = policy.NormalizeConfig(next)
		if err := policy.ValidateConfig(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := persistRuntimeConfigFile(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		configState.Set(next)
		restartPending := scheduleRestartIfNeeded(cfg, next, 0)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": next.Hash(), "restart_pending": restartPending}}, nil
	case methods.MethodConfigApply:
		req, err := methods.DecodeConfigApplyParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next := policy.NormalizeConfig(req.Config)
		if err := policy.ValidateConfig(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := persistRuntimeConfigFile(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		configState.Set(next)
		restartPending := scheduleRestartIfNeeded(cfg, next, req.RestartDelayMS)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": next.Hash(), "restart_pending": restartPending}}, nil
	case methods.MethodConfigPatch:
		req, err := methods.DecodeConfigPatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next, err := methods.ApplyConfigPatch(cfg, req.Patch)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next = policy.NormalizeConfig(next)
		if err := policy.ValidateConfig(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := persistRuntimeConfigFile(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		configState.Set(next)
		restartPending := scheduleRestartIfNeeded(cfg, next, req.RestartDelayMS)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": next.Hash(), "restart_pending": restartPending}}, nil
	case methods.MethodConfigSchema:
		return nostruntime.ControlRPCResult{Result: methods.ConfigSchema(cfg)}, nil
	case methods.MethodConfigSchemaLookup:
		// Look up a schema property by dot-notation path (e.g. "agents.model").
		// Returns the full schema when path is empty.
		path := ""
		if in.Params != nil {
			var p struct {
				Path  string `json:"path"`
				Field string `json:"field"`
			}
			if err := json.Unmarshal(in.Params, &p); err == nil {
				path = strings.TrimSpace(p.Path)
				if path == "" {
					path = strings.TrimSpace(p.Field)
				}
			}
		}
		full := methods.ConfigSchema(cfg)
		if path == "" {
			return nostruntime.ControlRPCResult{Result: full}, nil
		}
		// Walk the schema map by dot-separated segments.
		var cur any = full
		for _, seg := range strings.Split(path, ".") {
			m, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			if v, found := m[seg]; found {
				cur = v
			} else if props, hasProps := m["properties"].(map[string]any); hasProps {
				cur = props[seg]
			} else {
				cur = nil
				break
			}
		}
		if cur == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("schema path %q not found", path)
		}
		return nostruntime.ControlRPCResult{Result: cur}, nil
	case methods.MethodSecurityAudit:
		// Run security posture checks and return findings.
		report := securitypkg.Audit(securitypkg.AuditOptions{
			ConfigDoc: &cfg,
		})
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"findings": report.Findings,
			"critical": report.Critical,
			"warn":     report.Warn,
			"info":     report.Info,
		}}, nil

	// ── ACP (Agent Control Protocol) ────────────────────────────────────────
	case methods.MethodACPRegister:
		var req methods.ACPRegisterRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.register: invalid params: %w", err)
		}
		pk := strings.TrimSpace(req.PubKey)
		if pk == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.register: pubkey required")
		}
		if err := controlACPPeers.Register(acppkg.PeerEntry{
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
		controlACPPeers.Remove(req.PubKey)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil

	case methods.MethodACPPeers:
		peers := controlACPPeers.List()
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
		recordACPDelegatedChild(controlSessionStore, taskPayload, taskID)
		acpMsg := acppkg.NewTask(taskID, senderPubKey, taskPayload)
		payload, err := json.Marshal(acpMsg)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: marshal: %w", err)
		}
		waitRegistered := false
		if req.Wait {
			controlACPDispatcher.Register(taskID)
			waitRegistered = true
		}
		if err := sendACPDMWithTransport(ctx, dmBus, dmScheme, target, string(payload)); err != nil {
			if waitRegistered {
				controlACPDispatcher.Cancel(taskID)
			}
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: send DM: %w", err)
		}

		// If wait==true, block until result arrives.
		if req.Wait {
			timeout := time.Duration(req.TimeoutMS) * time.Millisecond
			if timeout == 0 {
				timeout = 60 * time.Second
			}
			result, waitErr := controlACPDispatcher.Wait(ctx, taskID, timeout)
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
			recordACPDelegatedChild(controlSessionStore, payload, taskID)
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
			pipelineResults, pipelineErr = pipeline.RunParallel(ctx, controlACPDispatcher, sendFn)
		} else {
			pipelineResults, pipelineErr = pipeline.RunSequential(ctx, controlACPDispatcher, sendFn)
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

	default:
		return nostruntime.ControlRPCResult{}, fmt.Errorf("unknown method %q", method)
	}

}
