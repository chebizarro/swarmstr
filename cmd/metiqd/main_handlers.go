package main

// main_handlers.go — Gateway RPC handler functions: talk config, update,
// heartbeat, wake, system presence, send, browser, voicewake, TTS, and
// memory persistence.
//
// Extracted from main.go to reduce god-file size. All functions remain in
// package main and reference the same globals/helpers as before.

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	browserpkg "metiq/internal/browser"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/memory"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"

)

// ---------------------------------------------------------------------------
// Talk / TTS / voice handlers
// ---------------------------------------------------------------------------

func applyTalkConfig(cfg state.ConfigDoc, reg *operationsRegistry, req methods.TalkConfigRequest) (map[string]any, error) {
	// Build the talk section by merging persisted config with live registry state.
	talk := map[string]any{
		"enabled":      false,
		"mode":         "disabled",
		"hotword":      []string{"openclaw", "metiq"},
		"sensitivity":  0.5,
		"tts_provider": "openai",
		"stt_provider": "openai-whisper",
		"voice_model":  "alloy",
	}

	// Overlay persisted talk config from cfg.Extra["talk"].
	if cfg.Extra != nil {
		if raw, ok := cfg.Extra["talk"].(map[string]any); ok {
			for k, v := range raw {
				talk[k] = v
			}
		}
	}

	// Overlay live state from ops registry.
	if reg != nil {
		ttsEnabled, ttsProvider := reg.TTSStatus()
		talkMode := reg.TalkMode()
		voicewake := reg.Voicewake()
		talk["enabled"] = ttsEnabled
		talk["mode"] = talkMode
		if ttsProvider != "" {
			talk["tts_provider"] = ttsProvider
		}
		if len(voicewake) > 0 {
			talk["hotword"] = voicewake
		}
	}

	// Optionally redact API keys unless includeSecrets is set.
	if !req.IncludeSecrets {
		delete(talk, "api_key")
		delete(talk, "apiKey")
	}

	configPayload := map[string]any{"talk": talk}

	// Include additional config sections.
	if cfg.Extra != nil {
		if session, ok := cfg.Extra["session"]; ok {
			configPayload["session"] = session
		}
		if ui, ok := cfg.Extra["ui"]; ok {
			configPayload["ui"] = ui
		}
	}
	return map[string]any{"config": configPayload}, nil
}

func applyUpdateRun(reg *operationsRegistry, req methods.UpdateRunRequest) (map[string]any, error) {
	svc := controlServices
	if svc == nil {
		svc = &daemonServices{emitter: controlWsEmitter, emitterMu: &controlWsEmitterMu}
	}
	return svc.applyUpdateRun(reg, req)
}

func (s *daemonServices) applyUpdateRun(reg *operationsRegistry, req methods.UpdateRunRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("update runtime not configured")
	}
	checkedAt := reg.RecordUpdateCheck()

	// Use the shared version checker (initialised in main).
	if s.handlers.updateChecker == nil {
		return map[string]any{"ok": true, "status": "checker_unavailable", "checked_at_ms": checkedAt}, nil
	}

	result := s.handlers.updateChecker.Check(context.Background(), req.Force)

	out := map[string]any{
		"ok":               true,
		"current_version":  result.Current,
		"latest_version":   result.Latest,
		"update_available": result.Available,
		"checked_at_ms":    result.CheckedAt,
	}
	if result.Error != "" {
		out["error"] = result.Error
		out["status"] = "error"
	} else if result.Available {
		out["status"] = "update_available"
		s.emitWSEvent(gatewayws.EventUpdateAvailable, gatewayws.UpdateAvailablePayload{
			TS:      result.CheckedAt,
			Version: result.Latest,
			Source:  "update.run",
		})
	} else {
		out["status"] = "up_to_date"
	}
	return out, nil
}

// validTalkModes lists the modes accepted by talk.mode.
var validTalkModes = map[string]bool{
	"disabled":     true,
	"off":          true,
	"push-to-talk": true,
	"always-on":    true,
	"hotword":      true,
}

func applyTalkMode(reg *operationsRegistry, req methods.TalkModeRequest) (map[string]any, error) {
	svc := controlServices
	if svc == nil {
		svc = &daemonServices{emitter: controlWsEmitter, emitterMu: &controlWsEmitterMu}
	}
	return svc.applyTalkMode(reg, req)
}

func (s *daemonServices) applyTalkMode(reg *operationsRegistry, req methods.TalkModeRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("talk runtime not configured")
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if !validTalkModes[mode] {
		return nil, fmt.Errorf("invalid talk mode %q; valid modes: disabled, off, push-to-talk, always-on, hotword", req.Mode)
	}
	mode = reg.SetTalkMode(mode)
	ts := time.Now().UnixMilli()
	s.emitWSEvent(gatewayws.EventTalkMode, gatewayws.TalkModePayload{TS: ts, Mode: mode})
	return map[string]any{"mode": mode, "ts": ts}, nil
}

func applyLastHeartbeat(reg *operationsRegistry, _ methods.LastHeartbeatRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("heartbeat runtime not configured")
	}
	status := reg.HeartbeatStatus()
	return map[string]any{
		"last_heartbeat_ms": status.LastRunMS,
		"last_run_ms":       status.LastRunMS,
		"last_wake_ms":      status.LastWakeMS,
		"enabled":           status.Enabled,
		"interval_ms":       status.IntervalMS,
		"pending_wakes":     status.PendingWakes,
	}, nil
}

func applySetHeartbeats(reg *operationsRegistry, req methods.SetHeartbeatsRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("heartbeat runtime not configured")
	}
	status := reg.SetHeartbeats(req.Enabled, req.IntervalMS)
	return map[string]any{
		"ok":                true,
		"enabled":           status.Enabled,
		"interval_ms":       status.IntervalMS,
		"last_heartbeat_ms": status.LastRunMS,
		"last_run_ms":       status.LastRunMS,
		"last_wake_ms":      status.LastWakeMS,
		"pending_wakes":     status.PendingWakes,
	}, nil
}

func applyWake(reg *operationsRegistry, req methods.WakeRequest) (map[string]any, error) {
	svc := controlServices
	if svc == nil {
		svc = &daemonServices{emitter: controlWsEmitter, emitterMu: &controlWsEmitterMu}
	}
	return svc.applyWake(reg, req)
}

func (s *daemonServices) applyWake(reg *operationsRegistry, req methods.WakeRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wake runtime not configured")
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		agentID = "main"
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "control"
	}
	status := reg.QueueHeartbeatWake(agentID, source, req.Text, req.Mode)
	at := status.LastWakeMS
	// Emit voice.wake when the source is voice-related.
	if source == "voice" || source == "voicewake" || source == "hotword" {
		s.emitWSEvent(gatewayws.EventVoicewake, gatewayws.VoicewakePayload{
			TS:     at,
			Source: source,
		})
	}
	return map[string]any{
		"ok":                true,
		"woken":             true,
		"agent_id":          agentID,
		"source":            source,
		"mode":              req.Mode,
		"text":              req.Text,
		"wake_at_ms":        at,
		"last_heartbeat_ms": status.LastRunMS,
		"last_run_ms":       status.LastRunMS,
		"last_wake_ms":      status.LastWakeMS,
		"pending_wakes":     status.PendingWakes,
	}, nil
}

func applySystemPresence(reg *operationsRegistry, _ methods.SystemPresenceRequest) ([]map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("system runtime not configured")
	}
	return reg.ListSystemPresence(), nil
}

func applySystemEvent(reg *operationsRegistry, req methods.SystemEventRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("system runtime not configured")
	}
	_ = reg.RecordSystemEvent(req)
	return map[string]any{"ok": true}, nil
}

func applySend(ctx context.Context, dmBus nostruntime.DMTransport, req methods.SendRequest) (map[string]any, error) {
	if dmBus == nil {
		return nil, fmt.Errorf("send runtime not configured")
	}
	if err := dmBus.SendDM(ctx, req.To, req.Message); err != nil {
		return nil, err
	}
	messageID := fmt.Sprintf("msg-%d", time.Now().UnixNano())
	return map[string]any{"runId": req.IdempotencyKey, "messageId": messageID, "channel": "nostr"}, nil
}

// browserBridgePaths are path prefixes that must go through the browser
// bridge proxy (Playwright sandbox).  All other paths are treated as direct
// HTTP fetch targets.
var browserBridgePaths = []string{
	"/act", "/snapshot", "/screenshot", "/evaluate",
	"/tabs", "/storage", "/fetch",
}

func applyBrowserRequest(req methods.BrowserRequestRequest) (map[string]any, error) {
	// browser.request routes through a local browser proxy (e.g. a Playwright
	// bridge server).  The proxy base URL is configured via METIQ_BROWSER_URL.
	// When the env var is absent, browser control is disabled.
	proxyBase := strings.TrimRight(os.Getenv("METIQ_BROWSER_URL"), "/")
	if proxyBase == "" {
		return nil, fmt.Errorf("browser control is disabled")
	}

	path := req.Path
	if path == "" {
		path = "/"
	}

	// Route browser automation paths to the bridge proxy.
	isBridgePath := false
	for _, prefix := range browserBridgePaths {
		if path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix+"?") {
			isBridgePath = true
			break
		}
	}

	// Check if path looks like an absolute URL (direct fetch).
	isAbsoluteURL := strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")

	// Build the full URL.
	var fullURL string
	if isAbsoluteURL {
		// Direct HTTP fetch — do not proxy through bridge.
		fullURL = path
	} else {
		fullURL = proxyBase + path
	}

	_ = isBridgePath // available for future routing decisions

	headers := map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}
	if isBridgePath {
		headers["Accept"] = "application/json"
		headers["Content-Type"] = "application/json"
	}

	// Pass auth token if configured.
	if token := os.Getenv("METIQ_BROWSER_TOKEN"); token != "" {
		headers["Authorization"] = "Bearer " + token
	}

	var bodyVal any
	if req.Body != nil {
		bodyVal = req.Body
	}

	fetchResp, err := browserpkg.Fetch(context.Background(), browserpkg.Request{
		Method:    req.Method,
		URL:       fullURL,
		Query:     req.Query,
		Headers:   headers,
		Body:      bodyVal,
		TimeoutMS: req.TimeoutMS,
	})
	if err != nil {
		return nil, fmt.Errorf("browser.request: %w", err)
	}

	out := map[string]any{
		"ok":           true,
		"status_code":  fetchResp.StatusCode,
		"content_type": fetchResp.ContentType,
		"url":          fetchResp.URL,
	}
	if fetchResp.Text != "" {
		out["text"] = fetchResp.Text
	}
	if fetchResp.Body != "" {
		out["body"] = fetchResp.Body
	}
	return out, nil
}

func applyVoicewakeGet(reg *operationsRegistry, _ methods.VoicewakeGetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("voicewake runtime not configured")
	}
	return map[string]any{"triggers": reg.Voicewake()}, nil
}

func applyVoicewakeSet(reg *operationsRegistry, req methods.VoicewakeSetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("voicewake runtime not configured")
	}
	return map[string]any{"triggers": reg.SetVoicewake(req.Triggers)}, nil
}

func applyTTSStatus(reg *operationsRegistry, _ methods.TTSStatusRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	enabled, provider := reg.TTSStatus()
	return map[string]any{"enabled": enabled, "provider": provider}, nil
}

func applyTTSProviders(reg *operationsRegistry, req methods.TTSProvidersRequest) (map[string]any, error) {
	svc := controlServices
	if svc == nil {
		svc = &daemonServices{emitter: controlWsEmitter, emitterMu: &controlWsEmitterMu}
	}
	return svc.applyTTSProviders(reg, req)
}

func (s *daemonServices) applyTTSProviders(reg *operationsRegistry, _ methods.TTSProvidersRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	_, active := reg.TTSStatus()
	var providers []map[string]any
	if s.handlers.ttsManager != nil {
		providers = s.handlers.ttsManager.Providers()
	} else {
		providers = []map[string]any{
			{"id": "openai", "name": "OpenAI TTS", "configured": false, "voices": []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}},
			{"id": "kokoro", "name": "Kokoro TTS (local)", "configured": false, "voices": []string{}},
		}
	}
	return map[string]any{"providers": providers, "active": active}, nil
}

func applyTTSSetProvider(reg *operationsRegistry, req methods.TTSSetProviderRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	provider := reg.SetTTSProvider(req.Provider)
	return map[string]any{"ok": true, "provider": provider}, nil
}

func applyTTSEnable(reg *operationsRegistry, _ methods.TTSEnableRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	return map[string]any{"enabled": reg.SetTTSEnabled(true)}, nil
}

func applyTTSDisable(reg *operationsRegistry, _ methods.TTSDisableRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	return map[string]any{"enabled": reg.SetTTSEnabled(false)}, nil
}

// countEligible counts how many hook status maps have eligible=true.
func countEligible(statuses []map[string]any) int {
	n := 0
	for _, s := range statuses {
		if v, ok := s["eligible"]; ok {
			if b, ok := v.(bool); ok && b {
				n++
			}
		}
	}
	return n
}

func applyTTSConvert(ctx context.Context, reg *operationsRegistry, req methods.TTSConvertRequest) (map[string]any, error) {
	svc := controlServices
	if svc == nil {
		svc = &daemonServices{emitter: controlWsEmitter, emitterMu: &controlWsEmitterMu}
	}
	return svc.applyTTSConvert(ctx, reg, req)
}

func (s *daemonServices) applyTTSConvert(ctx context.Context, reg *operationsRegistry, req methods.TTSConvertRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	enabled, activeProvider := reg.TTSStatus()
	providerID := activeProvider
	if req.Provider != "" {
		providerID = req.Provider
	}

	// If TTS is disabled, the manager is unavailable, or the provider is not
	// configured, return a metadata-only response (no audio) so callers can
	// always query the method without an error.
	doConvert := enabled && s.handlers.ttsManager != nil
	if doConvert {
		if p := s.handlers.ttsManager.Get(providerID); p == nil || !p.Configured() {
			doConvert = false
		}
	}

	if doConvert {
		result, err := s.handlers.ttsManager.Convert(ctx, providerID, req.Text, req.Voice)
		if err != nil {
			return nil, fmt.Errorf("tts.convert: %w", err)
		}
		return map[string]any{
			"ok":           true,
			"audioPath":    result.AudioPath,
			"audioBase64":  result.AudioBase64,
			"provider":     result.Provider,
			"voice":        result.Voice,
			"outputFormat": result.Format,
			"text":         req.Text,
		}, nil
	}

	// Synthesis is not available — return an error with diagnostics so the
	// caller knows *why* conversion was skipped rather than silently getting
	// empty audio fields.
	reason := "tts disabled"
	if !enabled {
		reason = "tts is disabled (call tts.enable first)"
	} else if s.handlers.ttsManager == nil {
		reason = "tts manager not initialised"
	} else if p := s.handlers.ttsManager.Get(providerID); p == nil {
		reason = fmt.Sprintf("unknown tts provider %q", providerID)
	} else if !p.Configured() {
		reason = fmt.Sprintf("tts provider %q is not configured (check environment variables)", providerID)
	}
	return nil, fmt.Errorf("tts.convert: %s", reason)
}

func persistMemories(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	repo *state.MemoryRepository,
	index memory.Store,
	tracker *memoryIndexTracker,
	docs []state.MemoryDoc,
) {
	for _, doc := range docs {
		if _, err := repo.Put(ctx, doc); err != nil {
			log.Printf("persist memory failed memory_id=%s err=%v", doc.MemoryID, err)
			continue
		}
		memory.AddDoc(ctx, index, doc)
		if err := index.Save(); err != nil {
			log.Printf("memory index save failed memory_id=%s err=%v", doc.MemoryID, err)
		}
		if err := tracker.MarkIndexed(ctx, docsRepo, doc.MemoryID, doc.Unix); err != nil {
			log.Printf("memory index checkpoint failed memory_id=%s err=%v", doc.MemoryID, err)
		}
	}
}








