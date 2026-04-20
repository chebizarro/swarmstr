package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/gateway/methods"
	hookspkg "metiq/internal/hooks"
	mcppkg "metiq/internal/mcp"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func (h controlRPCHandler) handleOpsRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	dmBus := h.deps.dmBus
	controlBus := h.deps.controlBus
	usageState := h.deps.usageState
	logBuffer := h.deps.logBuffer
	docsRepo := h.deps.docsRepo
	memoryIndex := h.deps.memoryIndex
	configState := h.deps.configState
	startedAt := h.deps.startedAt
	switch method {
	case methods.MethodLogsTail:
		req, err := methods.DecodeLogsTailParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if logBuffer == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"cursor": req.Cursor, "lines": []string{}, "truncated": false, "reset": false}}, true, nil
		}
		return nostruntime.ControlRPCResult{Result: logBuffer.Tail(req.Cursor, req.Limit, req.MaxBytes)}, true, nil
	case methods.MethodRuntimeObserve:
		req, err := methods.DecodeRuntimeObserveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := toolbuiltin.ObserveRuntime(ctx, runtimeObserveToolRequest(req))
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: out}, true, nil
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
		var fipsHealth any
		if fipsHealthOpts != nil {
			fipsHealth = toolbuiltin.BuildFIPSHealthInfo(*fipsHealthOpts)
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
			FIPS:          fipsHealth,
		}}, true, nil
	case methods.MethodUsageStatus:
		if usageState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "totals": map[string]any{"requests": 0, "tokens": 0}}}, true, nil
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "totals": usageState.Status()}}, true, nil
	case methods.MethodUsageCost:
		req, err := methods.DecodeUsageCostParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if usageState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "total_usd": 0, "filtered": false}}, true, nil
		}
		if req.StartDate != "" || req.EndDate != "" || req.Days > 0 {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("usage.cost: date filtering is not supported")
		}
		cost := usageState.Cost()
		result := map[string]any{"ok": true, "total_usd": cost["total_usd"], "estimate": cost, "filtered": false}
		return nostruntime.ControlRPCResult{Result: result}, true, nil
	case methods.MethodMemorySearch:
		req, err := methods.DecodeMemorySearchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.MemorySearchResponse{Results: memoryIndex.Search(req.Query, req.Limit)}}, true, nil

	case methods.MethodMemoryCompact:
		var compactReq methods.MemoryCompactRequest
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &compactReq)
		}
		if controlContextEngine == nil {
			return nostruntime.ControlRPCResult{Result: methods.MemoryCompactResponse{OK: false, Summary: "no context engine active"}}, true, nil
		}
		sessionToCompact := compactReq.SessionID
		flushOutcome, err := ensureSessionMemoryCurrent(ctx, configState.Get(), sessionToCompact, controlSessionStore)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("memory.compact session memory flush: %w", err)
		}
		cr, cErr := controlContextEngine.Compact(ctx, sessionToCompact)
		if cErr != nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("memory.compact: %w", cErr)
		}
		recordSessionCompaction(controlSessionStore, sessionToCompact, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
		return nostruntime.ControlRPCResult{Result: methods.MemoryCompactResponse{
			OK:           cr.OK,
			SessionsRun:  1,
			TokensBefore: cr.TokensBefore,
			TokensAfter:  cr.TokensAfter,
			Summary:      cr.Summary,
		}}, true, nil
	case methods.MethodGatewayIdentityGet:
		pubkey := strings.TrimSpace(in.FromPubKey)
		if dmBus != nil {
			pubkey = dmBus.PublicKey()
		}
		deviceID := pubkey
		if len(deviceID) > 24 {
			deviceID = deviceID[:24]
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"deviceId": deviceID, "publicKey": pubkey, "pubkey": pubkey}}, true, nil
	case methods.MethodCronList:
		req, err := methods.DecodeCronListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyCronList(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodCronStatus:
		req, err := methods.DecodeCronStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyCronStatus(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodCronAdd:
		req, err := methods.DecodeCronAddParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyCronAdd(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron jobs save warning (add): %v", saveErr)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodCronUpdate:
		req, err := methods.DecodeCronUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyCronUpdate(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron jobs save warning (update): %v", saveErr)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodCronRemove:
		req, err := methods.DecodeCronRemoveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyCronRemove(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron jobs save warning (remove): %v", saveErr)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodCronRun:
		req, err := methods.DecodeCronRunParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyCronRun(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodCronRuns:
		req, err := methods.DecodeCronRunsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyCronRuns(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodExecApprovalsGet:
		req, err := methods.DecodeExecApprovalsGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyExecApprovalsGet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodExecApprovalsSet:
		req, err := methods.DecodeExecApprovalsSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyExecApprovalsSet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodExecApprovalsNodeGet:
		req, err := methods.DecodeExecApprovalsNodeGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyExecApprovalsNodeGet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodExecApprovalsNodeSet:
		req, err := methods.DecodeExecApprovalsNodeSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyExecApprovalsNodeSet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodExecApprovalRequest:
		req, err := methods.DecodeExecApprovalRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyExecApprovalRequest(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodExecApprovalWaitDecision:
		req, err := methods.DecodeExecApprovalWaitDecisionParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyExecApprovalWaitDecision(ctx, controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodExecApprovalResolve:
		req, err := methods.DecodeExecApprovalResolveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyExecApprovalResolve(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodSandboxRun:
		req, err := methods.DecodeSandboxRunParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applySandboxRun(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodMCPList:
		req, err := methods.DecodeMCPListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyList(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodMCPGet:
		req, err := methods.DecodeMCPGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyGet(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodMCPPut:
		req, err := methods.DecodeMCPPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyPut(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodMCPRemove:
		req, err := methods.DecodeMCPRemoveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyRemove(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodMCPTest:
		req, err := methods.DecodeMCPTestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyTest(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodMCPReconnect:
		req, err := methods.DecodeMCPReconnectParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyReconnect(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodMCPAuthStart:
		req, err := methods.DecodeMCPAuthStartParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if controlMCPAuth == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp auth not configured")
		}
		out, err := controlMCPAuth.applyStart(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodMCPAuthRefresh:
		req, err := methods.DecodeMCPAuthRefreshParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if controlMCPAuth == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp auth not configured")
		}
		out, err := controlMCPAuth.applyRefresh(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodMCPAuthClear:
		req, err := methods.DecodeMCPAuthClearParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if controlMCPAuth == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("mcp auth not configured")
		}
		out, err := controlMCPAuth.applyClear(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodSecretsReload:
		req, err := methods.DecodeSecretsReloadParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applySecretsReload(req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodSecretsResolve:
		req, err := methods.DecodeSecretsResolveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applySecretsResolve(req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodWizardStart:
		req, err := methods.DecodeWizardStartParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyWizardStart(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodWizardNext:
		req, err := methods.DecodeWizardNextParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyWizardNext(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodWizardCancel:
		req, err := methods.DecodeWizardCancelParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyWizardCancel(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodWizardStatus:
		req, err := methods.DecodeWizardStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyWizardStatus(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodUpdateRun:
		req, err := methods.DecodeUpdateRunParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyUpdateRun(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodTalkConfig:
		req, err := methods.DecodeTalkConfigParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyTalkConfig(cfg, controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodTalkMode:
		req, err := methods.DecodeTalkModeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyTalkMode(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodLastHeartbeat:
		req, err := methods.DecodeLastHeartbeatParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyLastHeartbeat(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodSetHeartbeats:
		req, err := methods.DecodeSetHeartbeatsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applySetHeartbeats(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodWake:
		req, err := methods.DecodeWakeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyWake(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodSystemPresence:
		req, err := methods.DecodeSystemPresenceParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applySystemPresence(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodSystemEvent:
		req, err := methods.DecodeSystemEventParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applySystemEvent(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodSend:
		req, err := methods.DecodeSendParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applySend(ctx, dmBus, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodBrowserRequest:
		req, err := methods.DecodeBrowserRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyBrowserRequest(req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodVoicewakeGet:
		req, err := methods.DecodeVoicewakeGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyVoicewakeGet(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodVoicewakeSet:
		req, err := methods.DecodeVoicewakeSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyVoicewakeSet(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodTTSStatus:
		req, err := methods.DecodeTTSStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyTTSStatus(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodTTSProviders:
		req, err := methods.DecodeTTSProvidersParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyTTSProviders(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodTTSSetProvider:
		req, err := methods.DecodeTTSSetProviderParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyTTSSetProvider(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodTTSEnable:
		req, err := methods.DecodeTTSEnableParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyTTSEnable(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodTTSDisable:
		req, err := methods.DecodeTTSDisableParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyTTSDisable(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil
	case methods.MethodTTSConvert:
		req, err := methods.DecodeTTSConvertParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		out, err := applyTTSConvert(ctx, controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, true, nil

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
		return nostruntime.ControlRPCResult{Result: map[string]any{"hooks": statuses}}, true, nil

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
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("hookKey is required")
		}
		if controlHooksMgr != nil {
			controlHooksMgr.SetEnabled(key, true)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hookKey": key, "enabled": true}}, true, nil

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
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("hookKey is required")
		}
		if controlHooksMgr != nil {
			controlHooksMgr.SetEnabled(key, false)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hookKey": key, "enabled": false}}, true, nil

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
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("hookKey is required")
		}
		if controlHooksMgr == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("hook %q not found", key)
		}
		info := controlHooksMgr.Info(key)
		if info == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("hook %q not found", key)
		}
		return nostruntime.ControlRPCResult{Result: hookspkg.StatusToMap(*info)}, true, nil

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
		}}, true, nil

	default:
		return nostruntime.ControlRPCResult{}, false, nil
	}
}
