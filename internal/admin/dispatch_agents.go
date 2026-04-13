package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
)

func dispatchAgents(ctx context.Context, opts ServerOptions, method string, call methods.CallRequest, cfg state.ConfigDoc) (any, int, error) {
	switch method {
	case methods.MethodAgent:
		req, err := methods.DecodeAgentParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.StartAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agent runtime not configured")
		}
		out, err := opts.StartAgent(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodAgentWait:
		req, err := methods.DecodeAgentWaitParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.WaitAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agent runtime not configured")
		}
		out, err := opts.WaitAgent(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodAgentIdentityGet:
		req, err := methods.DecodeAgentIdentityParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.AgentIdentity == nil {
			return methods.ApplyCompatResponseAliases(map[string]any{"agent_id": "main", "display_name": "Metiq Agent", "session_id": req.SessionID}), http.StatusOK, nil
		}
		out, err := opts.AgentIdentity(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodGatewayIdentityGet:
		if opts.GatewayIdentity == nil {
			return map[string]any{"deviceId": "metiq", "publicKey": ""}, http.StatusOK, nil
		}
		out, err := opts.GatewayIdentity(ctx)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodAgentsList:
		req, err := methods.DecodeAgentsListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ListAgents == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.ListAgents(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodAgentsCreate:
		req, err := methods.DecodeAgentsCreateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.CreateAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.CreateAgent(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodAgentsUpdate:
		req, err := methods.DecodeAgentsUpdateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.UpdateAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.UpdateAgent(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodAgentsDelete:
		req, err := methods.DecodeAgentsDeleteParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.DeleteAgent == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.DeleteAgent(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodAgentsFilesList:
		req, err := methods.DecodeAgentsFilesListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ListAgentFiles == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.ListAgentFiles(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodAgentsFilesGet:
		req, err := methods.DecodeAgentsFilesGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.GetAgentFile == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.GetAgentFile(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodAgentsFilesSet:
		req, err := methods.DecodeAgentsFilesSetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SetAgentFile == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("agents provider not configured")
		}
		out, err := opts.SetAgentFile(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodModelsList:
		req, err := methods.DecodeModelsListParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ListModels == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("models provider not configured")
		}
		out, err := opts.ListModels(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodToolsCatalog:
		req, err := methods.DecodeToolsCatalogParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ToolsCatalog == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tools catalog provider not configured")
		}
		out, err := opts.ToolsCatalog(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodToolsProfileGet:
		req, err := methods.DecodeToolsProfileGetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ToolsProfileGet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tools profile not configured")
		}
		out, err := opts.ToolsProfileGet(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodToolsProfileSet:
		req, err := methods.DecodeToolsProfileSetParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.ToolsProfileSet == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("tools profile not configured")
		}
		out, err := opts.ToolsProfileSet(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSkillsStatus:
		req, err := methods.DecodeSkillsStatusParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SkillsStatus == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("skills provider not configured")
		}
		out, err := opts.SkillsStatus(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSkillsBins:
		req, err := methods.DecodeSkillsBinsParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SkillsBins == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("skills provider not configured")
		}
		out, err := opts.SkillsBins(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSkillsInstall:
		req, err := methods.DecodeSkillsInstallParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SkillsInstall == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("skills provider not configured")
		}
		out, err := opts.SkillsInstall(ctx, req)
		if err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	case methods.MethodSkillsUpdate:
		req, err := methods.DecodeSkillsUpdateParams(call.Params)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		if opts.SkillsUpdate == nil {
			return nil, http.StatusNotImplemented, fmt.Errorf("skills provider not configured")
		}
		out, err := opts.SkillsUpdate(ctx, req)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, http.StatusNotFound, fmt.Errorf("not found")
			}
			return nil, http.StatusInternalServerError, err
		}
		return methods.ApplyCompatResponseAliases(out), http.StatusOK, nil
	default:
		return nil, 0, nil
	}
}
