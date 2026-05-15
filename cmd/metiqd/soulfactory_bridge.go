package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

const soulFactoryRuntimeName = "metiq"

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

func soulFactoryMethods() []string {
	return methods.SoulFactoryMethods()
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

	result := map[string]any{
		"agent_id":        soulFactoryTagValue(in.Tags, "agent-id"),
		"runtime":         soulFactoryRuntimeName,
		"runtime_binding": fmt.Sprintf("metiq://agents/%s", soulFactoryTagValue(in.Tags, "agent-id")),
		"state":           soulFactoryStateForMethod(method),
		"spec_hash":       soulFactoryTagValue(in.Tags, "spec-hash"),
		"capability_ref":  soulFactoryTagValue(in.Tags, "capability"),
		"observed_at":     time.Now().Unix(),
		"warnings":        []string{"Metiq SoulFactory bridge scaffold validated request; local runtime mutation is deferred to execution work."},
	}
	errShape = soulFactoryValidationError("execution_failed", "Metiq SoulFactory bridge validated request; execution is not implemented in this scaffold", map[string]any{"runtime": soulFactoryRuntimeName})
	return soulFactoryRawControlResult(in, method, env, "failed", result, errShape), true, nil
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
	case methods.MethodSoulFactoryResume, methods.MethodSoulFactoryProvision, methods.MethodSoulFactoryRedeploy, methods.MethodSoulFactoryUpdate:
		return "running"
	case methods.MethodSoulFactoryRevoke:
		return "revoked"
	default:
		return "accepted"
	}
}
