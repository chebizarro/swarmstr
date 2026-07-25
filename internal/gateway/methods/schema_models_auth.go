package methods

// Param schemas for the models.* provider-auth surface (swarmstr-kmhu, BUCKET 1):
// models.authStatus / models.authLogout / models.probe. Shapes mirror OpenClaw
// src/gateway/server-methods/models*.ts, adapted to swarmstr's provider layer
// (state.ProvidersConfig + env credentials + the OAuth adapters in
// internal/agent). No secret material crosses the wire — status reports only
// booleans + auth method, and probe derives its endpoint from operator config
// (never from request params, so it cannot be used as an SSRF primitive).

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ModelsAuthStatusRequest optionally narrows the per-provider report to a
// single provider.
type ModelsAuthStatusRequest struct {
	Provider string `json:"provider,omitempty"`
}

func (r ModelsAuthStatusRequest) Normalize() (ModelsAuthStatusRequest, error) {
	r.Provider = strings.ToLower(strings.TrimSpace(r.Provider))
	return r, nil
}

func DecodeModelsAuthStatusParams(params json.RawMessage) (ModelsAuthStatusRequest, error) {
	return decodeMethodParams[ModelsAuthStatusRequest](params)
}

// ModelsAuthLogoutRequest identifies the provider whose stored (config-backed)
// credentials should be cleared. Env-var credentials cannot be cleared from a
// running process and are reported as remaining.
type ModelsAuthLogoutRequest struct {
	Provider string `json:"provider"`
}

func (r ModelsAuthLogoutRequest) Normalize() (ModelsAuthLogoutRequest, error) {
	r.Provider = strings.ToLower(strings.TrimSpace(r.Provider))
	if r.Provider == "" {
		return r, fmt.Errorf("models.authLogout: provider is required")
	}
	return r, nil
}

func DecodeModelsAuthLogoutParams(params json.RawMessage) (ModelsAuthLogoutRequest, error) {
	return decodeMethodParams[ModelsAuthLogoutRequest](params)
}

// maxModelsProbeTimeoutMS bounds a single connectivity probe.
const maxModelsProbeTimeoutMS = 30000

// ModelsProbeRequest asks for a bounded reachability probe of one provider's
// configured endpoint. Model is advisory (echoed back / used for reporting).
type ModelsProbeRequest struct {
	Provider  string `json:"provider"`
	Model     string `json:"model,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

func (r ModelsProbeRequest) Normalize() (ModelsProbeRequest, error) {
	r.Provider = strings.ToLower(strings.TrimSpace(r.Provider))
	r.Model = strings.TrimSpace(r.Model)
	if r.Provider == "" {
		return r, fmt.Errorf("models.probe: provider is required")
	}
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("models.probe: timeout_ms must not be negative")
	}
	if r.TimeoutMS > maxModelsProbeTimeoutMS {
		r.TimeoutMS = maxModelsProbeTimeoutMS
	}
	return r, nil
}

func DecodeModelsProbeParams(params json.RawMessage) (ModelsProbeRequest, error) {
	return decodeMethodParams[ModelsProbeRequest](params)
}
