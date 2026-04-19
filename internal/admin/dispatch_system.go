package admin

import (
	"context"
	"errors"
	"fmt"
	mcppkg "metiq/internal/mcp"
	"net/http"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchSystem(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodSupportedMethods:
		if opts.SupportedMethods != nil {
			list, err := opts.SupportedMethods(ctx)
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
			return list, http.StatusOK, nil
		}
		return methods.SupportedMethods(), http.StatusOK, nil
	case methods.MethodHealth:
		return map[string]any{"ok": true}, http.StatusOK, nil
	case methods.MethodDoctorMemoryStatus:
		indexInfo := map[string]any{"available": opts.SearchMemory != nil}
		if opts.MemoryStats != nil {
			count, sessionCount := opts.MemoryStats()
			indexInfo["entry_count"] = count
			indexInfo["session_count"] = sessionCount
		}
		return map[string]any{"ok": true, "index": indexInfo}, http.StatusOK, nil
	case methods.MethodStatus, methods.MethodStatusAlias:
		dmPolicy := opts.Status.DMPolicy
		if opts.StatusDMPolicy != nil {
			dmPolicy = opts.StatusDMPolicy()
		}
		relays := opts.Status.Relays
		if opts.StatusRelays != nil {
			relays = opts.StatusRelays()
		}
		var mcp *mcppkg.TelemetrySnapshot
		if opts.StatusMCP != nil {
			mcp = opts.StatusMCP()
		}
		return methods.StatusResponse{
			PubKey:        opts.Status.PubKey,
			Relays:        relays,
			DMPolicy:      dmPolicy,
			UptimeSeconds: int(time.Since(opts.Status.Started).Seconds()),
			UptimeMS:      time.Since(opts.Status.Started).Milliseconds(),
			Version:       "metiqd",
			MCP:           mcp,
		}, http.StatusOK, nil
	case methods.MethodUsageStatus:
		if opts.UsageStatus == nil {
			return map[string]any{"ok": true, "totals": map[string]any{"requests": 0, "tokens": 0}}, http.StatusOK, nil
		}
		out, err := opts.UsageStatus(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodMemorySearch:
		if opts.SearchMemory == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("memory search not configured")
		}
		req, err := methods.DecodeMemorySearchParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		return methods.MemorySearchResponse{Results: opts.SearchMemory(req.Query, req.Limit)}, http.StatusOK, nil
	case methods.MethodMemoryCompact:
		return delegateControlCall(ctx, opts, method, call.Params, "memory compaction not configured")
	case methods.MethodChatAbort:
		req, err := methods.DecodeChatAbortParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		aborted := 0
		// OpenClaw parity: RunID-only abort (without SessionID) is a no-op.
		// This prevents accidental global abort when only a run identifier is provided.
		if req.RunID != "" && strings.TrimSpace(req.SessionID) == "" {
			return methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "run_id": req.RunID, "aborted": false, "aborted_count": 0}), http.StatusOK, nil
		}
		if opts.AbortChat != nil {
			aborted, err = opts.AbortChat(ctx, req.SessionID)
			if err != nil {
				return nil, http.StatusInternalServerError, err
			}
		}
		result := map[string]any{"ok": true, "session_id": req.SessionID, "key": req.SessionID, "aborted": aborted > 0, "aborted_count": aborted}
		if req.RunID != "" {
			result["run_id"] = req.RunID
		}
		return methods.ApplyCompatResponseAliases(result), http.StatusOK, nil
	case methods.MethodSandboxRun:
		req, err := methods.DecodeSandboxRunParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SandboxRun == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("sandbox not configured")
		}
		out, err := opts.SandboxRun(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodWizardStart:
		req, err := methods.DecodeWizardStartParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.WizardStart == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("wizard provider not configured")
		}
		out, err := opts.WizardStart(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodWizardNext:
		req, err := methods.DecodeWizardNextParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.WizardNext == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("wizard provider not configured")
		}
		out, err := opts.WizardNext(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodWizardCancel:
		req, err := methods.DecodeWizardCancelParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.WizardCancel == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("wizard provider not configured")
		}
		out, err := opts.WizardCancel(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodWizardStatus:
		req, err := methods.DecodeWizardStatusParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.WizardStatus == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("wizard provider not configured")
		}
		out, err := opts.WizardStatus(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodUpdateRun:
		req, err := methods.DecodeUpdateRunParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.UpdateRun == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("update provider not configured")
		}
		out, err := opts.UpdateRun(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodLastHeartbeat:
		req, err := methods.DecodeLastHeartbeatParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.LastHeartbeat == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("heartbeat provider not configured")
		}
		out, err := opts.LastHeartbeat(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSetHeartbeats:
		req, err := methods.DecodeSetHeartbeatsParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SetHeartbeats == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("heartbeat provider not configured")
		}
		out, err := opts.SetHeartbeats(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodWake:
		req, err := methods.DecodeWakeParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.Wake == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("wake provider not configured")
		}
		out, err := opts.Wake(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSystemPresence:
		req, err := methods.DecodeSystemPresenceParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SystemPresence == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("system presence provider not configured")
		}
		out, err := opts.SystemPresence(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSystemEvent:
		req, err := methods.DecodeSystemEventParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SystemEvent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("system event provider not configured")
		}
		out, err := opts.SystemEvent(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSend:
		req, err := methods.DecodeSendParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.Send == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("send provider not configured")
		}
		out, err := opts.Send(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodPoll:
		req, err := methods.DecodePollParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SendPoll == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("poll provider not configured")
		}
		out, err := opts.SendPoll(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodHooksList,
		methods.MethodHooksEnable,
		methods.MethodHooksDisable,
		methods.MethodHooksInfo,
		methods.MethodHooksCheck:
		return delegateControlCall(ctx, opts, method, call.Params, "hooks provider not configured")
	default:
		return internalRoutingError("system", method)
	}
}
