package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const (
	soulFactoryRuntimeName            = "metiq"
	soulFactoryRuntimeCheckpointName  = "soulfactory_runtime"
	soulFactoryRuntimeCheckpointLimit = 512
)

var soulFactoryRuntimeMu sync.Mutex

type soulFactoryControlEnvelope struct {
	Schema         string                        `json:"schema"`
	Method         string                        `json:"method"`
	IdempotencyKey string                        `json:"idempotency_key"`
	RequestedAt    int64                         `json:"requested_at,omitempty"`
	Operator       soulFactoryOperatorEnvelope   `json:"operator,omitempty"`
	Controller     soulFactoryControllerEnvelope `json:"controller,omitempty"`
	Target         soulFactoryTargetEnvelope     `json:"target,omitempty"`
	Soul           soulFactorySoulEnvelope       `json:"soul,omitempty"`
	Params         json.RawMessage               `json:"params"`
}

type soulFactoryOperatorEnvelope struct {
	PubKey       string `json:"pubkey,omitempty"`
	RequestEvent string `json:"request_event,omitempty"`
}

type soulFactoryControllerEnvelope struct {
	PubKey string `json:"pubkey,omitempty"`
}

type soulFactoryTargetEnvelope struct {
	Runtime       string `json:"runtime,omitempty"`
	RuntimePubKey string `json:"runtime_pubkey,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
}

type soulFactorySoulEnvelope struct {
	ID       string `json:"id,omitempty"`
	Event    string `json:"event,omitempty"`
	Draft    string `json:"draft,omitempty"`
	SpecHash string `json:"spec_hash,omitempty"`
}

type soulFactoryError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type soulFactoryReplaySignature struct {
	Method        string
	RuntimePubKey string
	AgentID       string
	SpecHash      string
	ParamsHash    string
}

func soulFactoryMethods() []string {
	return methods.SoulFactoryMethods()
}

func soulFactoryFeatureCapabilities() []nostruntime.SoulFactoryFeatureCapability {
	return []nostruntime.SoulFactoryFeatureCapability{
		{
			Name:           "avatar",
			Methods:        []string{methods.MethodSoulFactoryAvatarGenerate, methods.MethodSoulFactoryAvatarSet},
			Status:         "partial",
			OpenClawParity: "partial",
			Notes:          []string{"avatar.set applies stored refs", "avatar.generate is accepted but persisted for a backend worker"},
		},
		{
			Name:           "voice",
			Methods:        []string{methods.MethodSoulFactoryVoiceConfigure, methods.MethodSoulFactoryVoiceSample},
			Status:         "stubbed",
			OpenClawParity: "partial",
			Notes:          []string{"voice configuration and sample requests are persisted; live TTS provider hot-reload is not wired"},
		},
		{
			Name:           "memory",
			Methods:        []string{methods.MethodSoulFactoryMemoryConfigure, methods.MethodSoulFactoryMemoryReindex},
			Status:         "stubbed",
			OpenClawParity: "partial",
			Notes:          []string{"memory configuration and reindex requests are persisted; live memory backend reconfiguration is not wired"},
		},
		{
			Name:           "persona",
			Methods:        []string{methods.MethodSoulFactoryPersonaUpdate},
			Status:         "partial",
			OpenClawParity: "partial",
			Notes:          []string{"persona metadata and identity updates are persisted on the managed agent document; live system-prompt hot-reload is not wired"},
		},
		{
			Name:           "config_reload",
			Methods:        []string{methods.MethodSoulFactoryConfigReload},
			Status:         "partial",
			OpenClawParity: "partial",
			Notes:          []string{"config reload applies supported fields and persists provider-specific patches; provider-specific hot-reload hooks are not wired"},
		},
	}
}

func soulFactoryOpenClawParity() nostruntime.SoulFactoryFeatureParity {
	return nostruntime.SoulFactoryFeatureParity{
		Runtime:      "openclaw",
		Status:       "partial",
		MethodParity: true,
		Notes:        []string{"Metiq advertises the same SoulFactory customization method names as OpenClaw; several provider-specific live hooks are currently persisted/stubbed"},
	}
}

func soulFactoryControllerPubKeys(cfg state.ConfigDoc) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(cfg.Control.Admins))
	for _, admin := range cfg.Control.Admins {
		pubkey := strings.ToLower(strings.TrimSpace(admin.PubKey))
		if pubkey == "" || !soulFactoryAdminAllowsAny(admin.Methods) {
			continue
		}
		if _, ok := seen[pubkey]; ok {
			continue
		}
		seen[pubkey] = struct{}{}
		out = append(out, pubkey)
	}
	return out
}

func soulFactoryAdminAllowsAny(allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, method := range allowed {
		method = strings.ToLower(strings.TrimSpace(method))
		if method == "*" || method == "soulfactory.*" || methods.IsSoulFactoryMethod(method) {
			return true
		}
	}
	return false
}

func (h controlRPCHandler) handleSoulFactoryRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (nostruntime.ControlRPCResult, bool, error) {
	method = strings.TrimSpace(method)
	if !methods.IsSoulFactoryMethod(method) {
		return nostruntime.ControlRPCResult{}, false, nil
	}

	env, errShape := validateSoulFactoryRequest(in, method, cfg)
	if errShape != nil {
		return soulFactoryRawControlResult(in, method, env, "rejected", nil, errShape), true, nil
	}

	return h.executeSoulFactoryRPC(ctx, in, method, env, cfg), true, nil
}

func (h controlRPCHandler) executeSoulFactoryRPC(ctx context.Context, in nostruntime.ControlRPCInbound, method string, env soulFactoryControlEnvelope, cfg state.ConfigDoc) nostruntime.ControlRPCResult {
	repo := h.deps.docsRepo
	if repo == nil {
		errShape := soulFactoryValidationError("runtime_unavailable", "Metiq docs repository is not configured", map[string]any{"runtime": soulFactoryRuntimeName})
		return soulFactoryRawControlResult(in, method, env, "failed", nil, errShape)
	}

	controller := strings.ToLower(strings.TrimSpace(soulFactoryTagValue(in.Tags, "controller")))
	idempotencyKey := strings.TrimSpace(env.IdempotencyKey)
	sig := soulFactoryReplaySignatureFor(in, method, env)

	soulFactoryRuntimeMu.Lock()
	defer soulFactoryRuntimeMu.Unlock()

	checkpoint, err := soulFactoryLoadRuntimeCheckpoint(ctx, repo)
	if err != nil {
		errShape := soulFactoryValidationError("runtime_unavailable", "load SoulFactory runtime checkpoint failed", map[string]any{"error": err.Error()})
		return soulFactoryRawControlResult(in, method, env, "failed", nil, errShape)
	}
	if prior, ok := soulFactoryFindIdempotencyRecord(checkpoint, controller, idempotencyKey); ok {
		if soulFactoryReplayMatches(prior, sig) {
			return nostruntime.ControlRPCResult{RawPayload: prior.Payload, RawStatus: soulFactoryRecordStatus(prior)}
		}
		errShape := soulFactoryValidationError("duplicate_conflict", "idempotency key was already used for a different SoulFactory request", map[string]any{
			"idempotency_key": idempotencyKey,
			"method":          method,
			"agent_id":        sig.AgentID,
			"spec_hash":       sig.SpecHash,
		})
		return soulFactoryRawControlResult(in, method, env, "rejected", nil, errShape)
	}

	result, errShape := h.applySoulFactorySideEffect(ctx, in, method, env, cfg)
	if errShape != nil {
		return soulFactoryRawControlResult(in, method, env, "failed", result, errShape)
	}

	out := soulFactoryRawControlResult(in, method, env, "success", result, nil)
	if strings.TrimSpace(out.RawPayload) == "" {
		return out
	}
	if err := soulFactoryPersistIdempotencyRecord(ctx, repo, checkpoint, controller, idempotencyKey, in, sig, out.RawPayload, out.RawStatus); err != nil {
		errShape := soulFactoryValidationError("publish_failed", "persist SoulFactory idempotency state failed", map[string]any{"error": err.Error()})
		return soulFactoryRawControlResult(in, method, env, "failed", result, errShape)
	}
	return out
}

func (h controlRPCHandler) applySoulFactorySideEffect(ctx context.Context, in nostruntime.ControlRPCInbound, method string, env soulFactoryControlEnvelope, cfg state.ConfigDoc) (map[string]any, *soulFactoryError) {
	repo := h.deps.docsRepo
	agentID := strings.TrimSpace(env.Target.AgentID)
	if agentID == "" {
		agentID = soulFactoryTagValue(in.Tags, "agent-id")
	}
	if agentID == "" {
		return nil, soulFactoryValidationError("invalid_schema", "agent id is required", nil)
	}

	params := soulFactoryParamsMap(env.Params)
	now := time.Now().Unix()
	customizationResult := map[string]any(nil)
	customizationWarnings := []string(nil)
	doc, err := repo.GetAgent(ctx, agentID)
	found := err == nil
	previousState := soulFactoryStateFromMeta(doc.Meta)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return nil, soulFactoryValidationError("runtime_unavailable", "load managed agent failed", map[string]any{"agent_id": agentID, "error": err.Error()})
	}

	switch method {
	case methods.MethodSoulFactoryProvision:
		if !found || doc.AgentID == "" {
			doc = state.AgentDoc{Version: 1, AgentID: agentID}
		}
		doc.Deleted = false
		if name := soulFactoryNestedString(params, "identity", "name"); name != "" {
			doc.Name = name
		} else if doc.Name == "" {
			doc.Name = agentID
		}
		if workspace := soulFactoryWorkspaceValue(params); workspace != "" {
			doc.Workspace = workspace
		}
		if doc.Model == "" {
			doc.Model = strings.TrimSpace(cfg.Agent.DefaultModel)
		}
		if doc.Model == "" {
			doc.Model = soulFactoryNestedString(params, "runtime", "model")
		}
	case methods.MethodSoulFactoryUpdate:
		if !found || doc.Deleted {
			return nil, soulFactoryValidationError("execution_failed", "managed agent does not exist", map[string]any{"agent_id": agentID})
		}
		previousSpecHash := soulFactoryString(params, "previous_spec_hash")
		currentSpecHash := soulFactorySpecHashFromAgent(doc)
		if previousSpecHash != "" && currentSpecHash != "" && previousSpecHash != currentSpecHash {
			return nil, soulFactoryValidationError("spec_hash_mismatch", "previous_spec_hash does not match the managed agent's current spec hash", map[string]any{"agent_id": agentID, "previous_spec_hash": previousSpecHash, "current_spec_hash": currentSpecHash})
		}
		newSpecHash := soulFactoryString(params, "new_spec_hash")
		if newSpecHash != "" && newSpecHash != soulFactoryTagValue(in.Tags, "spec-hash") {
			return nil, soulFactoryValidationError("spec_hash_mismatch", "new_spec_hash must match the request spec-hash tag", map[string]any{"agent_id": agentID, "new_spec_hash": newSpecHash, "spec_hash": soulFactoryTagValue(in.Tags, "spec-hash")})
		}
		resolvedSpec := soulFactoryNestedMap(params, "resolved_spec")
		patchSpec := soulFactoryNestedMap(params, "patch")
		if name := soulFactoryNestedString(resolvedSpec, "identity", "name"); name != "" {
			doc.Name = name
		} else if name := soulFactoryNestedString(patchSpec, "identity", "name"); name != "" {
			doc.Name = name
		}
		if workspace := soulFactoryWorkspaceValueFrom(resolvedSpec); workspace != "" {
			doc.Workspace = workspace
		} else if workspace := soulFactoryWorkspaceValueFrom(patchSpec); workspace != "" {
			doc.Workspace = workspace
		}
	case methods.MethodSoulFactorySuspend:
		if !found || doc.Deleted {
			return nil, soulFactoryValidationError("execution_failed", "managed agent does not exist", map[string]any{"agent_id": agentID})
		}
		if err := h.disableSoulFactoryLiveAgent(ctx, repo, agentID, false); err != nil {
			return nil, soulFactoryValidationError("execution_failed", "suspend managed agent failed", map[string]any{"agent_id": agentID, "error": err.Error()})
		}
	case methods.MethodSoulFactoryResume, methods.MethodSoulFactoryRedeploy:
		if !found || doc.Deleted {
			return nil, soulFactoryValidationError("execution_failed", "managed agent does not exist", map[string]any{"agent_id": agentID})
		}
	case methods.MethodSoulFactoryRevoke:
		if !found {
			return nil, soulFactoryValidationError("execution_failed", "managed agent does not exist", map[string]any{"agent_id": agentID})
		}
		doc.Deleted = true
		if err := h.disableSoulFactoryLiveAgent(ctx, repo, agentID, true); err != nil {
			return nil, soulFactoryValidationError("execution_failed", "revoke managed agent runtime bindings failed", map[string]any{"agent_id": agentID, "error": err.Error()})
		}
	case methods.MethodSoulFactoryAvatarGenerate,
		methods.MethodSoulFactoryAvatarSet,
		methods.MethodSoulFactoryVoiceConfigure,
		methods.MethodSoulFactoryVoiceSample,
		methods.MethodSoulFactoryMemoryConfigure,
		methods.MethodSoulFactoryMemoryReindex,
		methods.MethodSoulFactoryPersonaUpdate,
		methods.MethodSoulFactoryConfigReload:
		if !found || doc.Deleted {
			return nil, soulFactoryValidationError("execution_failed", "managed agent does not exist", map[string]any{"agent_id": agentID})
		}
		var errShape *soulFactoryError
		customizationResult, customizationWarnings, errShape = soulFactoryApplyCustomization(method, params, &doc, cfg, now)
		if errShape != nil {
			return nil, errShape
		}
	default:
		return nil, soulFactoryValidationError("unsupported_method", "unsupported SoulFactory method", map[string]any{"method": method})
	}

	stateValue := soulFactoryStateForMethod(method)
	if previousState == "suspended" && !soulFactoryMethodActivatesRuntime(method) {
		stateValue = "suspended"
	}
	meta := soulFactoryRuntimeMeta(in, method, env, params, stateValue, now)
	if customization := soulFactoryCustomizationFromMeta(doc.Meta); len(customization) > 0 {
		meta["customization"] = customization
	}
	doc.Meta = mergeSessionMeta(doc.Meta, map[string]any{"soulfactory": meta})
	if doc.Version == 0 {
		doc.Version = 1
	}
	if _, err := repo.PutAgent(ctx, agentID, doc); err != nil {
		return nil, soulFactoryValidationError("execution_failed", "persist managed agent failed", map[string]any{"agent_id": agentID, "error": err.Error()})
	}
	if h.deps.agentRegistry != nil && !doc.Deleted && stateValue == "running" && strings.TrimSpace(doc.Model) != "" {
		if rt, rtErr := agent.BuildRuntimeForModel(doc.Model, h.deps.tools); rtErr == nil && rt != nil {
			h.deps.agentRegistry.Set(agentID, rt)
		}
	}

	result := map[string]any{
		"agent_id":        agentID,
		"runtime":         soulFactoryRuntimeName,
		"runtime_binding": fmt.Sprintf("metiq://agents/%s", agentID),
		"state":           stateValue,
		"spec_hash":       soulFactoryTagValue(in.Tags, "spec-hash"),
		"capability_ref":  soulFactoryTagValue(in.Tags, "capability"),
		"observed_at":     now,
		"warnings":        customizationWarnings,
	}
	if customizationResult != nil {
		result["customization"] = customizationResult
	}
	if method == methods.MethodSoulFactoryRedeploy {
		result["deployment_generation"] = soulFactoryDeploymentGeneration(doc.Meta)
	}
	return result, nil
}

func soulFactoryApplyCustomization(method string, params map[string]any, doc *state.AgentDoc, cfg state.ConfigDoc, observedAt int64) (map[string]any, []string, *soulFactoryError) {
	_ = cfg
	customization := soulFactoryCustomizationFromMeta(doc.Meta)
	if customization == nil {
		customization = map[string]any{}
	}
	warnings := []string{}
	result := map[string]any{"status": "applied", "applied": []string{}}
	setSection := func(section string, value map[string]any) {
		if value == nil {
			value = map[string]any{}
		}
		value["updated_at"] = observedAt
		customization[section] = value
	}
	setApplied := func(values ...string) {
		applied := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				applied = append(applied, value)
			}
		}
		result["applied"] = applied
	}

	switch method {
	case methods.MethodSoulFactoryAvatarGenerate:
		avatar := soulFactorySectionParams(params, "avatar")
		if generation := soulFactoryNestedMap(params, "generation"); generation != nil && soulFactoryNestedMap(avatar, "generation") == nil {
			avatar["generation"] = soulFactoryCloneMap(generation)
		}
		avatar["status"] = "generation_requested"
		setSection("avatar", avatar)
		result["section"] = "avatar"
		result["status"] = "accepted"
		setApplied("avatar.generation")
		warnings = append(warnings, "TODO(metiq): avatar image generation provider is not wired; request was persisted for the runtime/backend worker")
	case methods.MethodSoulFactoryAvatarSet:
		avatar := soulFactorySectionParams(params, "avatar")
		ref := soulFactoryFirstString(params, "avatar_ref", "ref", "uploaded_ref", "generated_ref")
		if ref == "" {
			ref = soulFactoryFirstString(avatar, "avatar_ref", "ref", "uploaded_ref", "generated_ref")
		}
		if ref == "" {
			return nil, nil, soulFactoryValidationError("missing_required_param", "missing required avatar ref param", map[string]any{"param": "avatar_ref|ref|uploaded_ref|generated_ref"})
		}
		current := soulFactoryFirstString(params, "current", "source")
		if current == "" {
			current = soulFactoryFirstString(avatar, "current", "source")
		}
		if current == "" {
			current = "uploaded"
		}
		avatar["ref"] = ref
		avatar["current"] = current
		avatar["status"] = "set"
		setSection("avatar", avatar)
		result["section"] = "avatar"
		result["avatar_ref"] = ref
		setApplied("avatar.ref")
	case methods.MethodSoulFactoryVoiceConfigure:
		voice := soulFactorySectionParams(params, "voice")
		voice["status"] = "configured"
		setSection("voice", voice)
		result["section"] = "voice"
		setApplied("voice.config")
		warnings = append(warnings, "TODO(metiq): live TTS provider hot-reload is not wired; voice config was persisted")
	case methods.MethodSoulFactoryVoiceSample:
		sample := map[string]any{"request": soulFactoryCloneMap(params), "status": "sample_requested"}
		setSection("voice_sample", sample)
		result["section"] = "voice"
		result["status"] = "accepted"
		setApplied("voice.sample")
		warnings = append(warnings, "TODO(metiq): voice sample generation is not wired; sample request was persisted")
	case methods.MethodSoulFactoryMemoryConfigure:
		memory := soulFactorySectionParams(params, "memory")
		memory["status"] = "configured"
		setSection("memory", memory)
		result["section"] = "memory"
		setApplied("memory.config")
		warnings = append(warnings, "TODO(metiq): live memory backend reconfiguration is not wired; memory config was persisted")
	case methods.MethodSoulFactoryMemoryReindex:
		reindex := map[string]any{"request": soulFactoryCloneMap(params), "status": "reindex_requested"}
		setSection("memory_reindex", reindex)
		result["section"] = "memory"
		result["status"] = "accepted"
		setApplied("memory.reindex")
		warnings = append(warnings, "TODO(metiq): memory reindex orchestration is not wired; reindex request was persisted")
	case methods.MethodSoulFactoryPersonaUpdate:
		persona := soulFactorySectionParams(params, "persona")
		if identity := soulFactoryNestedMap(params, "identity"); identity != nil {
			persona["identity"] = soulFactoryCloneMap(identity)
			if name := soulFactoryString(identity, "name"); name != "" {
				doc.Name = name
			}
		}
		if prompt := soulFactoryString(params, "system_prompt"); prompt != "" {
			persona["system_prompt"] = prompt
		}
		if sections := soulFactoryNestedMap(params, "system_prompt_sections"); sections != nil {
			persona["system_prompt_sections"] = soulFactoryCloneMap(sections)
		}
		persona["status"] = "updated"
		setSection("persona", persona)
		result["section"] = "persona"
		setApplied("persona.config")
	case methods.MethodSoulFactoryConfigReload:
		patch := soulFactoryFirstNestedMap(params, "resolved_spec", "config", "patch")
		if patch == nil {
			patch = params
		}
		applied := []string{}
		if identity := soulFactoryNestedMap(patch, "identity"); identity != nil {
			identityCopy := soulFactoryCloneMap(identity)
			identityCopy["updated_at"] = observedAt
			customization["identity"] = identityCopy
			applied = append(applied, "identity")
			if name := soulFactoryString(identity, "name"); name != "" {
				doc.Name = name
			}
		}
		if runtimeSpec := soulFactoryNestedMap(patch, "runtime"); runtimeSpec != nil {
			if model := soulFactoryString(runtimeSpec, "model"); model != "" {
				doc.Model = model
				applied = append(applied, "runtime.model")
			}
		}
		for _, section := range []string{"avatar", "voice", "memory", "persona"} {
			if value := soulFactoryNestedMap(patch, section); value != nil {
				copyValue := soulFactoryCloneMap(value)
				copyValue["status"] = "reloaded"
				setSection(section, copyValue)
				applied = append(applied, section)
			}
		}
		result["section"] = "config"
		if len(applied) == 0 {
			result["status"] = "accepted"
			warnings = append(warnings, "Metiq config reload found no avatar, voice, memory, persona, identity, or runtime model fields to apply")
		} else {
			result["applied"] = applied
		}
		warnings = append(warnings, "TODO(metiq): provider-specific hot-reload hooks are not wired; applicable config was persisted")
	default:
		return nil, nil, soulFactoryValidationError("unsupported_method", "unsupported SoulFactory customization method", map[string]any{"method": method})
	}

	soulFactorySetCustomizationMeta(doc, customization)
	result["runtime_model"] = doc.Model
	result["agent_name"] = doc.Name
	return result, warnings, nil
}

func soulFactoryRuntimeMeta(in nostruntime.ControlRPCInbound, method string, env soulFactoryControlEnvelope, params map[string]any, stateValue string, observedAt int64) map[string]any {
	meta := map[string]any{
		"schema":                 nostruntime.SoulFactoryRuntimeControlSchema,
		"runtime":                soulFactoryRuntimeName,
		"method":                 method,
		"state":                  stateValue,
		"spec_hash":              soulFactoryTagValue(in.Tags, "spec-hash"),
		"idempotency_key":        strings.TrimSpace(env.IdempotencyKey),
		"request_event":          in.EventID,
		"operator_request_event": soulFactoryTagValue(in.Tags, "e"),
		"controller":             soulFactoryTagValue(in.Tags, "controller"),
		"runtime_pubkey":         soulFactoryTagValue(in.Tags, "p"),
		"soul":                   soulFactoryTagValue(in.Tags, "soul"),
		"observed_at":            observedAt,
	}
	if env.Soul.Draft != "" {
		meta["draft"] = env.Soul.Draft
	}
	if env.Soul.Event != "" {
		meta["soul_event"] = env.Soul.Event
	}
	if capability := soulFactoryTagValue(in.Tags, "capability"); capability != "" {
		meta["capability_ref"] = capability
	}
	if reason := soulFactoryString(params, "reason"); reason != "" {
		meta["reason"] = reason
	}
	if strategy := soulFactoryString(params, "strategy"); strategy != "" {
		meta["redeploy_strategy"] = strategy
	}
	if method == methods.MethodSoulFactoryRedeploy {
		meta["deployment_generation"] = observedAt
	}
	if method == methods.MethodSoulFactoryRevoke {
		meta["revoked_at"] = observedAt
		if value, ok := params["revoke_runtime_credentials"]; ok {
			meta["revoke_runtime_credentials"] = value
		}
		if value, ok := params["delete_workspace"]; ok {
			meta["delete_workspace"] = value
		}
	}
	return meta
}

func soulFactoryDeploymentGeneration(meta map[string]any) any {
	if meta == nil {
		return nil
	}
	if sf, ok := meta["soulfactory"].(map[string]any); ok {
		return sf["deployment_generation"]
	}
	return nil
}

func soulFactorySpecHashFromAgent(doc state.AgentDoc) string {
	if doc.Meta == nil {
		return ""
	}
	if sf, ok := doc.Meta["soulfactory"].(map[string]any); ok {
		if specHash, ok := sf["spec_hash"].(string); ok {
			return strings.TrimSpace(specHash)
		}
	}
	return ""
}

func soulFactoryAgentRuntimeDisabled(ctx context.Context, repo *state.DocsRepository, agentID string) (string, bool) {
	agentID = strings.TrimSpace(agentID)
	if repo == nil || agentID == "" {
		return "", false
	}
	doc, err := repo.GetAgent(ctx, agentID)
	if err != nil || doc.Meta == nil {
		return "", false
	}
	sf, _ := doc.Meta["soulfactory"].(map[string]any)
	stateValue, _ := sf["state"].(string)
	stateValue = strings.TrimSpace(stateValue)
	return stateValue, stateValue == "suspended" || stateValue == "revoked"
}

func (h controlRPCHandler) disableSoulFactoryLiveAgent(ctx context.Context, repo *state.DocsRepository, agentID string, clearPersistentSessions bool) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	if h.deps.agentRegistry != nil {
		h.deps.agentRegistry.Remove(agentID)
	}
	if h.deps.sessionRouter != nil {
		for sessionID, activeAgentID := range h.deps.sessionRouter.List() {
			if activeAgentID == agentID {
				if h.deps.chatCancels != nil {
					h.deps.chatCancels.AbortWithCause(sessionID, fmt.Errorf("SoulFactory agent %s disabled", agentID))
				}
				if clearPersistentSessions {
					h.deps.sessionRouter.Unassign(sessionID)
				}
			}
		}
	}
	if !clearPersistentSessions || repo == nil {
		return nil
	}
	sessions, err := repo.ListSessions(ctx, 5000)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	for _, sess := range sessions {
		if sess.Meta == nil {
			continue
		}
		assignedAgent, _ := sess.Meta["agent_id"].(string)
		if assignedAgent != agentID {
			continue
		}
		sessionID := strings.TrimSpace(sess.SessionID)
		if sessionID == "" {
			continue
		}
		if _, err := updateExistingSessionDoc(ctx, repo, sessionID, sess.PeerPubKey, func(session *state.SessionDoc) error {
			if session.Meta != nil {
				delete(session.Meta, "agent_id")
			}
			return nil
		}); err != nil && !errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("clear session %q assignment: %w", sessionID, err)
		}
	}
	return nil
}

func validateSoulFactoryRequest(in nostruntime.ControlRPCInbound, method string, cfg state.ConfigDoc) (soulFactoryControlEnvelope, *soulFactoryError) {
	env := decodeSoulFactoryEnvelope(in)
	controller := soulFactoryTagValue(in.Tags, "controller")
	if strings.TrimSpace(controller) == "" {
		return env, soulFactoryValidationError("missing_required_tag", "missing required controller tag", map[string]any{"tag": "controller"})
	}
	if strings.ToLower(strings.TrimSpace(controller)) != strings.ToLower(strings.TrimSpace(in.FromPubKey)) {
		return env, soulFactoryValidationError("unauthorized_controller", "controller tag must match event pubkey", map[string]any{"controller": controller, "pubkey": in.FromPubKey})
	}
	if !soulFactoryControllerTrusted(controller, method, cfg) {
		return env, soulFactoryValidationError("unauthorized_controller", "controller pubkey is not trusted by this runtime", map[string]any{"controller": controller})
	}
	for _, tag := range []string{"p", "method", "e", "soul", "agent-id", "controller", "idempotency-key", "spec-hash", "schema"} {
		if strings.TrimSpace(soulFactoryTagValue(in.Tags, tag)) == "" {
			return env, soulFactoryValidationError("missing_required_tag", "missing required "+tag+" tag", map[string]any{"tag": tag})
		}
	}
	if got := soulFactoryTagValue(in.Tags, "method"); got != method {
		return env, soulFactoryValidationError("invalid_schema", "method tag does not match control method", map[string]any{"tag_method": got, "method": method})
	}
	if got := soulFactoryTagValue(in.Tags, "schema"); got != nostruntime.SoulFactoryRuntimeControlSchema {
		return env, soulFactoryValidationError("unsupported_schema_version", "unsupported SoulFactory control schema", map[string]any{"schema": got})
	}
	if strings.TrimSpace(env.Schema) != nostruntime.SoulFactoryRuntimeControlSchema {
		return env, soulFactoryValidationError("invalid_schema", "content schema is required and must match SoulFactory control schema", map[string]any{"schema": env.Schema})
	}
	if strings.TrimSpace(env.Method) != method {
		return env, soulFactoryValidationError("invalid_schema", "content method must match control method", map[string]any{"content_method": env.Method, "method": method})
	}
	if strings.TrimSpace(env.IdempotencyKey) == "" || env.IdempotencyKey != soulFactoryTagValue(in.Tags, "idempotency-key") {
		return env, soulFactoryValidationError("invalid_schema", "idempotency key tag and content field must match", map[string]any{"tag": soulFactoryTagValue(in.Tags, "idempotency-key"), "content": env.IdempotencyKey})
	}
	if err := validateSoulFactoryEnvelopeRefs(in, env); err != nil {
		return env, err
	}
	if err := validateSoulFactoryParams(method, env.Params); err != nil {
		return env, err
	}
	return env, nil
}

func decodeSoulFactoryEnvelope(in nostruntime.ControlRPCInbound) soulFactoryControlEnvelope {
	env := soulFactoryControlEnvelope{Method: strings.TrimSpace(in.Method), Params: in.Params}
	if len(in.RawContent) == 0 {
		return env
	}
	var decoded soulFactoryControlEnvelope
	if err := json.Unmarshal(in.RawContent, &decoded); err != nil {
		return env
	}
	if len(decoded.Params) == 0 {
		decoded.Params = in.Params
	}
	return decoded
}

func validateSoulFactoryEnvelopeRefs(in nostruntime.ControlRPCInbound, env soulFactoryControlEnvelope) *soulFactoryError {
	checks := []struct {
		name string
		got  string
		want string
	}{
		{name: "controller.pubkey", got: env.Controller.PubKey, want: soulFactoryTagValue(in.Tags, "controller")},
		{name: "operator.request_event", got: env.Operator.RequestEvent, want: soulFactoryTagValue(in.Tags, "e")},
		{name: "target.runtime", got: env.Target.Runtime, want: soulFactoryRuntimeName},
		{name: "target.runtime_pubkey", got: env.Target.RuntimePubKey, want: soulFactoryTagValue(in.Tags, "p")},
		{name: "target.agent_id", got: env.Target.AgentID, want: soulFactoryTagValue(in.Tags, "agent-id")},
		{name: "soul.spec_hash", got: env.Soul.SpecHash, want: soulFactoryTagValue(in.Tags, "spec-hash")},
	}
	for _, check := range checks {
		got := strings.TrimSpace(check.got)
		want := strings.TrimSpace(check.want)
		if got == "" {
			return soulFactoryValidationError("invalid_schema", "missing required "+check.name+" content field", map[string]any{"field": check.name})
		}
		if !strings.EqualFold(got, want) {
			return soulFactoryValidationError("invalid_schema", check.name+" does not match required tag/runtime value", map[string]any{"field": check.name, "content": got, "expected": want})
		}
	}
	return nil
}

func validateSoulFactoryParams(method string, raw json.RawMessage) *soulFactoryError {
	var params map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" {
		params = map[string]json.RawMessage{}
	} else if err := json.Unmarshal(raw, &params); err != nil {
		return soulFactoryValidationError("invalid_schema", "params must be a JSON object", nil)
	}
	require := func(keys ...string) *soulFactoryError {
		for _, key := range keys {
			if len(params[key]) == 0 || string(params[key]) == "null" {
				return soulFactoryValidationError("missing_required_param", "missing required "+key+" param", map[string]any{"param": key})
			}
		}
		return nil
	}
	switch method {
	case methods.MethodSoulFactoryProvision:
		return require("identity", "runtime", "permissions", "relay_policy", "workspace", "assets")
	case methods.MethodSoulFactoryUpdate:
		if len(params["patch"]) == 0 && len(params["resolved_spec"]) == 0 {
			return soulFactoryValidationError("missing_required_param", "missing required patch or resolved_spec param", map[string]any{"param": "patch|resolved_spec"})
		}
		return require("previous_spec_hash", "new_spec_hash", "update_mode")
	case methods.MethodSoulFactorySuspend:
		return require("reason")
	case methods.MethodSoulFactoryResume:
		return require("reason")
	case methods.MethodSoulFactoryRedeploy:
		return require("reason", "strategy")
	case methods.MethodSoulFactoryRevoke:
		return require("reason", "revoke_runtime_credentials")
	case methods.MethodSoulFactoryAvatarGenerate,
		methods.MethodSoulFactoryAvatarSet,
		methods.MethodSoulFactoryVoiceConfigure,
		methods.MethodSoulFactoryVoiceSample,
		methods.MethodSoulFactoryMemoryConfigure,
		methods.MethodSoulFactoryMemoryReindex,
		methods.MethodSoulFactoryPersonaUpdate,
		methods.MethodSoulFactoryConfigReload:
		return nil
	default:
		return soulFactoryValidationError("unsupported_method", "unsupported SoulFactory method", map[string]any{"method": method})
	}
}

func soulFactoryControllerTrusted(controller, method string, cfg state.ConfigDoc) bool {
	controller = strings.ToLower(strings.TrimSpace(controller))
	for _, admin := range cfg.Control.Admins {
		if strings.ToLower(strings.TrimSpace(admin.PubKey)) != controller {
			continue
		}
		if len(admin.Methods) == 0 {
			return true
		}
		for _, allowed := range admin.Methods {
			allowed = strings.ToLower(strings.TrimSpace(allowed))
			if allowed == "*" || allowed == "soulfactory.*" || allowed == method {
				return true
			}
		}
	}
	return false
}

func soulFactoryRawControlResult(in nostruntime.ControlRPCInbound, method string, env soulFactoryControlEnvelope, status string, result map[string]any, errShape *soulFactoryError) nostruntime.ControlRPCResult {
	envelope := soulFactoryResultEnvelope(in, method, env, status, result, errShape)
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nostruntime.ControlRPCResult{Error: "marshal SoulFactory result: " + err.Error()}
	}
	rawStatus := "ok"
	if status != "success" {
		rawStatus = "error"
	}
	return nostruntime.ControlRPCResult{RawPayload: string(raw), RawStatus: rawStatus}
}

func soulFactoryResultEnvelope(in nostruntime.ControlRPCInbound, method string, env soulFactoryControlEnvelope, status string, result map[string]any, errShape *soulFactoryError) map[string]any {
	idempotencyKey := strings.TrimSpace(env.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = soulFactoryTagValue(in.Tags, "idempotency-key")
	}
	var errValue any
	if errShape != nil {
		errValue = errShape
	}
	return map[string]any{
		"schema":                 nostruntime.SoulFactoryRuntimeControlSchema,
		"method":                 method,
		"idempotency_key":        idempotencyKey,
		"request_event":          in.EventID,
		"operator_request_event": soulFactoryTagValue(in.Tags, "e"),
		"status":                 status,
		"result":                 result,
		"error":                  errValue,
	}
}

func soulFactoryValidationError(code, message string, details map[string]any) *soulFactoryError {
	return &soulFactoryError{Code: code, Message: message, Retryable: false, Details: details}
}

func soulFactoryTagValue(tags nostr.Tags, key string) string {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == key {
			return strings.TrimSpace(tag[1])
		}
	}
	return ""
}

func soulFactoryStateForMethod(method string) string {
	switch method {
	case methods.MethodSoulFactorySuspend:
		return "suspended"
	case methods.MethodSoulFactoryResume,
		methods.MethodSoulFactoryProvision,
		methods.MethodSoulFactoryRedeploy,
		methods.MethodSoulFactoryUpdate,
		methods.MethodSoulFactoryAvatarGenerate,
		methods.MethodSoulFactoryAvatarSet,
		methods.MethodSoulFactoryVoiceConfigure,
		methods.MethodSoulFactoryVoiceSample,
		methods.MethodSoulFactoryMemoryConfigure,
		methods.MethodSoulFactoryMemoryReindex,
		methods.MethodSoulFactoryPersonaUpdate,
		methods.MethodSoulFactoryConfigReload:
		return "running"
	case methods.MethodSoulFactoryRevoke:
		return "revoked"
	default:
		return "accepted"
	}
}

func soulFactoryMethodActivatesRuntime(method string) bool {
	switch method {
	case methods.MethodSoulFactoryProvision, methods.MethodSoulFactoryResume, methods.MethodSoulFactoryRedeploy:
		return true
	default:
		return false
	}
}

func soulFactoryStateFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	sf, _ := meta["soulfactory"].(map[string]any)
	stateValue, _ := sf["state"].(string)
	return strings.TrimSpace(stateValue)
}

func soulFactoryLoadRuntimeCheckpoint(ctx context.Context, repo *state.DocsRepository) (state.CheckpointDoc, error) {
	doc, err := repo.GetCheckpoint(ctx, soulFactoryRuntimeCheckpointName)
	if err == nil {
		if doc.Name == "" {
			doc.Name = soulFactoryRuntimeCheckpointName
		}
		if doc.Version == 0 {
			doc.Version = 1
		}
		return doc, nil
	}
	if errors.Is(err, state.ErrNotFound) {
		return state.CheckpointDoc{Version: 1, Name: soulFactoryRuntimeCheckpointName}, nil
	}
	return state.CheckpointDoc{}, err
}

func soulFactoryFindIdempotencyRecord(doc state.CheckpointDoc, controller string, idempotencyKey string) (state.ControlResponseCacheDoc, bool) {
	controller = strings.ToLower(strings.TrimSpace(controller))
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if controller == "" || idempotencyKey == "" {
		return state.ControlResponseCacheDoc{}, false
	}
	for i := len(doc.ControlResponses) - 1; i >= 0; i-- {
		entry := doc.ControlResponses[i]
		if strings.EqualFold(strings.TrimSpace(entry.CallerPubKey), controller) && strings.TrimSpace(entry.RequestID) == idempotencyKey {
			return entry, true
		}
	}
	return state.ControlResponseCacheDoc{}, false
}

func soulFactoryPersistIdempotencyRecord(ctx context.Context, repo *state.DocsRepository, checkpoint state.CheckpointDoc, controller string, idempotencyKey string, in nostruntime.ControlRPCInbound, sig soulFactoryReplaySignature, payload string, rawStatus string) error {
	controller = strings.ToLower(strings.TrimSpace(controller))
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	status := strings.TrimSpace(rawStatus)
	if status == "" {
		status = "ok"
	}
	entry := state.ControlResponseCacheDoc{
		CallerPubKey: controller,
		RequestID:    idempotencyKey,
		Payload:      payload,
		EventUnix:    time.Now().Unix(),
		Tags: [][]string{
			{"e", in.EventID},
			{"status", status},
			{"method", sig.Method},
			{"runtime_pubkey", sig.RuntimePubKey},
			{"agent-id", sig.AgentID},
			{"spec-hash", sig.SpecHash},
			{"params_sha256", sig.ParamsHash},
			{"idempotency-key", idempotencyKey},
		},
	}
	kept := checkpoint.ControlResponses[:0]
	for _, existing := range checkpoint.ControlResponses {
		if strings.EqualFold(existing.CallerPubKey, controller) && strings.TrimSpace(existing.RequestID) == idempotencyKey {
			continue
		}
		kept = append(kept, existing)
	}
	checkpoint.ControlResponses = append(kept, entry)
	if len(checkpoint.ControlResponses) > soulFactoryRuntimeCheckpointLimit {
		checkpoint.ControlResponses = append([]state.ControlResponseCacheDoc{}, checkpoint.ControlResponses[len(checkpoint.ControlResponses)-soulFactoryRuntimeCheckpointLimit:]...)
	}
	checkpoint.Version = 1
	checkpoint.Name = soulFactoryRuntimeCheckpointName
	checkpoint.LastEvent = in.EventID
	checkpoint.LastUnix = entry.EventUnix
	_, err := repo.PutCheckpoint(ctx, soulFactoryRuntimeCheckpointName, checkpoint)
	return err
}

func soulFactoryReplaySignatureFor(in nostruntime.ControlRPCInbound, method string, env soulFactoryControlEnvelope) soulFactoryReplaySignature {
	return soulFactoryReplaySignature{
		Method:        strings.TrimSpace(method),
		RuntimePubKey: soulFactoryTagValue(in.Tags, "p"),
		AgentID:       soulFactoryTagValue(in.Tags, "agent-id"),
		SpecHash:      soulFactoryTagValue(in.Tags, "spec-hash"),
		ParamsHash:    soulFactoryParamsHash(env.Params),
	}
}

func soulFactoryReplayMatches(entry state.ControlResponseCacheDoc, sig soulFactoryReplaySignature) bool {
	return controlResponseDocFirstTagValue(entry.Tags, "method") == sig.Method &&
		controlResponseDocFirstTagValue(entry.Tags, "runtime_pubkey") == sig.RuntimePubKey &&
		controlResponseDocFirstTagValue(entry.Tags, "agent-id") == sig.AgentID &&
		controlResponseDocFirstTagValue(entry.Tags, "spec-hash") == sig.SpecHash &&
		controlResponseDocFirstTagValue(entry.Tags, "params_sha256") == sig.ParamsHash
}

func soulFactoryRecordStatus(entry state.ControlResponseCacheDoc) string {
	status := controlResponseDocFirstTagValue(entry.Tags, "status")
	if status == "" {
		status = "ok"
	}
	return status
}

func soulFactoryParamsHash(params json.RawMessage) string {
	sum := sha256.Sum256([]byte(params))
	return hex.EncodeToString(sum[:])
}

func soulFactoryParamsMap(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 || string(raw) == "null" {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func soulFactoryString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func soulFactoryNestedMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func soulFactoryNestedString(m map[string]any, keys ...string) string {
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			return soulFactoryString(current, key)
		}
		current = soulFactoryNestedMap(current, key)
		if current == nil {
			return ""
		}
	}
	return ""
}

func soulFactoryFirstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := soulFactoryString(m, key); value != "" {
			return value
		}
	}
	return ""
}

func soulFactoryFirstNestedMap(m map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value := soulFactoryNestedMap(m, key); value != nil {
			return value
		}
	}
	return nil
}

func soulFactorySectionParams(params map[string]any, section string) map[string]any {
	if nested := soulFactoryNestedMap(params, section); nested != nil {
		return soulFactoryCloneMap(nested)
	}
	return soulFactoryCloneMap(params)
}

func soulFactoryCustomizationFromMeta(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	sf, _ := meta["soulfactory"].(map[string]any)
	customization, _ := sf["customization"].(map[string]any)
	return soulFactoryCloneMap(customization)
}

func soulFactorySetCustomizationMeta(doc *state.AgentDoc, customization map[string]any) {
	if doc == nil {
		return
	}
	sf := map[string]any{}
	if doc.Meta != nil {
		if existing, ok := doc.Meta["soulfactory"].(map[string]any); ok {
			sf = soulFactoryCloneMap(existing)
		}
	}
	sf["customization"] = soulFactoryCloneMap(customization)
	doc.Meta = mergeSessionMeta(doc.Meta, map[string]any{"soulfactory": sf})
}

func soulFactoryCloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = soulFactoryCloneValue(value)
	}
	return out
}

func soulFactoryCloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return soulFactoryCloneMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = soulFactoryCloneValue(item)
		}
		return out
	default:
		return typed
	}
}

func soulFactoryWorkspaceValue(params map[string]any) string {
	return soulFactoryWorkspaceValueFrom(soulFactoryNestedMap(params, "workspace"))
}

func soulFactoryWorkspaceValueFrom(workspace map[string]any) string {
	if workspace == nil {
		return ""
	}
	for _, key := range []string{"repo", "environment", "branch"} {
		if v := soulFactoryString(workspace, key); v != "" {
			return v
		}
	}
	return ""
}
