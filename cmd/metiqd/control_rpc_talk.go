package main

// control_rpc_talk.go — control-RPC handlers for the voice/talk long-tail
// surface (swarmstr-0tfj): the tts persona controls, talk.catalog/talk.speak,
// voicewake.routing.*, the talk.session.* turn lifecycle over gateway-relay,
// and the talk.client.* client-owned sessions. Served over the control-RPC
// talk surface, mirroring the skills/workspace surface wiring.
//
// Capability honesty: talk.speak, tts.personas/setPersona, talk.catalog's
// speech section and voicewake.routing.* are backed by live runtime (the tts
// manager + config), so they are implemented for real. The session/client
// audio-transport paths resolve a provider from the realtimevoice/realtimestt
// registries, which the daemon does not currently populate, so they return a
// clear talk.ErrUnavailable rather than a fabricated stub. managed-room-only
// operations return talk.ErrUnsupported (accepted deviation).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"metiq/internal/gateway/methods"
	talkpkg "metiq/internal/gateway/talk"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
	"metiq/internal/tts"
)

// EventVoicewakeRoutingChanged is broadcast when the voicewake routing table
// changes.
const EventVoicewakeRoutingChanged = "voicewake.routing.changed"

func (h controlRPCHandler) ttsManager() *tts.Manager {
	if h.deps.services != nil {
		return h.deps.services.handlers.ttsManager
	}
	return nil
}

func (h controlRPCHandler) handleTalkRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	switch method {
	// ── Phase A: personas / catalog / speak / voicewake routing ─────────────
	case methods.MethodTTSPersonas:
		if _, err := methods.DecodeTTSPersonasParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		active := ""
		if h.deps.ops != nil {
			active = h.deps.ops.TTSPersona()
		}
		personas, resolved := talkpkg.ListPersonas(cfg.Extra, active)
		return nostruntime.ControlRPCResult{Result: map[string]any{"personas": personas, "active": resolved}}, true, nil

	case methods.MethodTTSSetPersona:
		req, err := methods.DecodeTTSSetPersonaParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		resolved, cleared, err := talkpkg.ValidatePersona(cfg.Extra, req.Persona)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.ops == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("tts runtime not configured")
		}
		active := h.deps.ops.SetTTSPersona(resolved)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "active": active, "cleared": cleared}}, true, nil

	case methods.MethodTalkCatalog:
		if _, err := methods.DecodeTalkCatalogParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		var speech []map[string]any
		if mgr := h.ttsManager(); mgr != nil {
			speech = mgr.Providers()
		}
		active := ""
		if h.deps.ops != nil {
			_, active = h.deps.ops.TTSStatus()
		}
		// Transcription/realtime registries are unwired: honest empty inventory.
		cat := talkpkg.BuildCatalog(talkpkg.CatalogInput{
			SpeechProviders: speech,
			ActiveSpeech:    active,
			Transcription:   nil,
			Realtime:        nil,
			BrowserRealtime: false,
		})
		return nostruntime.ControlRPCResult{Result: cat}, true, nil

	case methods.MethodTalkSpeak:
		req, err := methods.DecodeTalkSpeakParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		result, err := talkpkg.Speak(ctx, h.ttsManager(), cfg.Extra, talkpkg.SpeakRequest{
			Text:     req.Text,
			Provider: req.Provider,
			Voice:    req.Voice,
			Persona:  req.Persona,
			Format:   req.Format,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		return nostruntime.ControlRPCResult{Result: result}, true, nil

	case methods.MethodVoicewakeRoutingGet:
		if _, err := methods.DecodeVoicewakeRoutingGetParams(in.Params); err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.talkRouting == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("voicewake routing not configured")
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"routing": h.deps.talkRouting.Get()}}, true, nil

	case methods.MethodVoicewakeRoutingSet:
		req, err := methods.DecodeVoicewakeRoutingSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.talkRouting == nil {
			return nostruntime.ControlRPCResult{}, true, fmt.Errorf("voicewake routing not configured")
		}
		routes := make([]talkpkg.RoutingRoute, 0, len(req.Routes))
		for _, r := range req.Routes {
			routes = append(routes, talkpkg.RoutingRoute{Trigger: r.Trigger, Target: r.Target, Mode: r.Mode})
		}
		stored, err := h.deps.talkRouting.Set(talkpkg.RoutingConfig{
			Version: req.Version, DefaultTarget: req.DefaultTarget, Routes: routes,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, true, err
		}
		if h.deps.services != nil {
			h.deps.services.emitWSEvent(EventVoicewakeRoutingChanged, map[string]any{
				"ts": time.Now().UnixMilli(), "routing": stored,
			})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"routing": stored, "changed": true}}, true, nil
	}

	// Phase B / C require the connection-owned session managers.
	if handled, result, err := h.handleTalkSessionRPC(ctx, in, method); handled {
		return result, true, err
	}
	if handled, result, err := h.handleTalkClientRPC(ctx, in, method); handled {
		return result, true, err
	}
	return nostruntime.ControlRPCResult{}, false, nil
}

// ── Phase B: talk.session.* ─────────────────────────────────────────────────

func (h controlRPCHandler) handleTalkSessionRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string) (bool, nostruntime.ControlRPCResult, error) {
	if !strings.HasPrefix(method, "talk.session.") {
		return false, nostruntime.ControlRPCResult{}, nil
	}
	if h.deps.talkSessions == nil {
		return true, nostruntime.ControlRPCResult{}, fmt.Errorf("talk sessions are not available")
	}
	connID, err := terminalConnID(ctx)
	if err != nil {
		return true, nostruntime.ControlRPCResult{}, err
	}
	mgr := h.deps.talkSessions

	switch method {
	case methods.MethodTalkSessionCreate:
		req, err := methods.DecodeTalkSessionCreateParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.Create(ctx, talkpkg.CreateInput{
			ConnID: connID, SessionID: req.SessionID, Mode: req.Mode, Transport: req.Transport,
			Provider: req.Provider, Voice: req.Voice, Language: req.Language, SystemPrompt: req.SystemPrompt,
		})
		return resultOrErr(out, err)
	case methods.MethodTalkSessionJoin:
		req, err := decodeNorm(in.Params, methods.DecodeTalkSessionJoinParams)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.Join(connID, req.SessionID)
		return resultOrErr(out, err)
	case methods.MethodTalkSessionAppendAudio:
		req, err := methods.DecodeTalkSessionAppendAudioParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.AppendAudio(connID, req.SessionID, req.AudioBase64)
		return resultOrErr(out, err)
	case methods.MethodTalkSessionStartTurn:
		req, err := decodeNorm(in.Params, methods.DecodeTalkSessionStartTurnParams)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.StartTurn(connID, req.SessionID)
		return resultOrErr(out, err)
	case methods.MethodTalkSessionEndTurn:
		req, err := decodeNorm(in.Params, methods.DecodeTalkSessionEndTurnParams)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.EndTurn(connID, req.SessionID)
		return resultOrErr(out, err)
	case methods.MethodTalkSessionCancelTurn:
		req, err := decodeNorm(in.Params, methods.DecodeTalkSessionCancelTurnParams)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.CancelTurn(connID, req.SessionID)
		return resultOrErr(out, err)
	case methods.MethodTalkSessionCancelOutput:
		req, err := decodeNorm(in.Params, methods.DecodeTalkSessionCancelOutputParams)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.CancelOutput(connID, req.SessionID)
		return resultOrErr(out, err)
	case methods.MethodTalkSessionAcknowledgeMark:
		req, err := methods.DecodeTalkSessionAcknowledgeMarkParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.AcknowledgeMark(connID, req.SessionID, req.Mark)
		return resultOrErr(out, err)
	case methods.MethodTalkSessionSubmitToolResult:
		req, err := methods.DecodeTalkSessionSubmitToolResultParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.SubmitToolResult(connID, req.SessionID, req.ToolCallID, req.Result, req.Error)
		return resultOrErr(out, err)
	case methods.MethodTalkSessionSteer:
		req, err := methods.DecodeTalkSessionSteerParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.Steer(connID, req.SessionID, req.Text)
		return resultOrErr(out, err)
	case methods.MethodTalkSessionClose:
		req, err := decodeNorm(in.Params, methods.DecodeTalkSessionCloseParams)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := mgr.Close(connID, req.SessionID)
		return resultOrErr(out, err)
	}
	return false, nostruntime.ControlRPCResult{}, nil
}

// ── Phase C: talk.client.* ──────────────────────────────────────────────────

func (h controlRPCHandler) handleTalkClientRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string) (bool, nostruntime.ControlRPCResult, error) {
	if !strings.HasPrefix(method, "talk.client.") {
		return false, nostruntime.ControlRPCResult{}, nil
	}
	if h.deps.talkClients == nil {
		return true, nostruntime.ControlRPCResult{}, fmt.Errorf("talk client sessions are not available")
	}
	store := h.deps.talkClients

	switch method {
	case methods.MethodTalkClientCreate:
		req, err := methods.DecodeTalkClientCreateParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := store.Create(ctx, talkpkg.ClientCreateInput{
			SessionID: req.SessionID, Transport: req.Transport, Provider: req.Provider,
			Voice: req.Voice, Language: req.Language, Model: req.Model, AgentID: req.AgentID,
		})
		return resultOrErr(out, err)
	case methods.MethodTalkClientTranscript:
		req, err := methods.DecodeTalkClientTranscriptParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := store.Transcript(req.SessionID, talkpkg.TranscriptEntry{Role: req.Role, Text: req.Text, Final: req.Final})
		return resultOrErr(out, err)
	case methods.MethodTalkClientClose:
		req, err := methods.DecodeTalkClientCloseParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := store.Close(req.SessionID)
		return resultOrErr(out, err)
	case methods.MethodTalkClientToolCall:
		req, err := methods.DecodeTalkClientToolCallParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := store.ToolCall(ctx, talkpkg.ToolCallInput{
			SessionID: req.SessionID, Tool: req.Tool, ToolCallID: req.ToolCallID,
			AgentID: req.AgentID, Arguments: req.Arguments,
		}, h.talkToolCallBridge())
		return resultOrErr(out, err)
	case methods.MethodTalkClientSteer:
		req, err := methods.DecodeTalkClientSteerParams(in.Params)
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return true, nostruntime.ControlRPCResult{}, err
		}
		out, err := store.Steer(req.SessionID, req.Text)
		return resultOrErr(out, err)
	}
	return false, nostruntime.ControlRPCResult{}, nil
}

// talkToolCallBridge dispatches a bridged agent-consult run backed by the live
// agent runtime, returning the managed run id.
func (h controlRPCHandler) talkToolCallBridge() talkpkg.ToolCallBridge {
	return func(ctx context.Context, in talkpkg.ToolCallInput) (string, error) {
		controller := currentAgentRunController()
		agentID, rt := controller.resolveInboundChannelRuntime(in.AgentID, in.SessionID)
		if rt == nil {
			return "", fmt.Errorf("%w: agent runtime not configured", talkpkg.ErrUnavailable)
		}
		_ = agentID
		runID := fmt.Sprintf("talk-consult-%d", time.Now().UnixNano())
		agentReq := methods.AgentRequest{
			SessionID: in.SessionID,
			Message:   talkConsultMessage(in.Tool, in.Arguments),
		}
		if err := controller.launchManagedRun(runID, agentReq, rt, nil, nil, h.deps.memoryIndex, controller.jobs, nil); err != nil {
			return "", err
		}
		return runID, nil
	}
}

// talkConsultMessage renders a realtime tool-call into an agent prompt. A
// query/prompt/text string argument is used verbatim; otherwise the raw
// arguments are attached to a tool-named instruction.
func talkConsultMessage(tool string, args json.RawMessage) string {
	if len(args) > 0 {
		var fields map[string]any
		if err := json.Unmarshal(args, &fields); err == nil {
			for _, key := range []string{"query", "prompt", "text", "question", "message"} {
				if v, ok := fields[key].(string); ok && strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			}
		}
		return fmt.Sprintf("Voice tool call %q with arguments: %s", tool, strings.TrimSpace(string(args)))
	}
	return fmt.Sprintf("Voice tool call %q", tool)
}

func decodeNorm[T interface{ Normalize() (T, error) }](params json.RawMessage, decode func(json.RawMessage) (T, error)) (T, error) {
	req, err := decode(params)
	if err != nil {
		return req, err
	}
	return req.Normalize()
}

func resultOrErr(out map[string]any, err error) (bool, nostruntime.ControlRPCResult, error) {
	if err != nil {
		return true, nostruntime.ControlRPCResult{}, err
	}
	return true, nostruntime.ControlRPCResult{Result: out}, nil
}
