package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// SandboxRunRequest is the request payload for sandbox.run.
type SandboxRunRequest struct {
	// Cmd is the command and arguments to execute.
	Cmd []string `json:"cmd"`
	// Env is a list of "KEY=VALUE" environment overrides.
	Env []string `json:"env,omitempty"`
	// Workdir is the working directory for the command.
	Workdir string `json:"workdir,omitempty"`
	// TimeoutSeconds overrides the daemon's configured sandbox timeout.
	TimeoutSeconds int `json:"timeout_s,omitempty"`
	// Driver overrides the daemon's configured sandbox driver.
	Driver string `json:"driver,omitempty"`
}

type WizardStartRequest struct {
	Mode string `json:"mode,omitempty"`
}

type WizardNextRequest struct {
	ID    string         `json:"id"`
	Input map[string]any `json:"input,omitempty"`
}

type WizardCancelRequest struct {
	ID string `json:"id"`
}

type WizardStatusRequest struct {
	ID string `json:"id,omitempty"`
}

type UpdateRunRequest struct {
	Force bool `json:"force,omitempty"`
}

type LastHeartbeatRequest struct{}

type SetHeartbeatsRequest struct {
	Enabled    *bool `json:"enabled,omitempty"`
	IntervalMS int   `json:"interval_ms,omitempty"`
}

type WakeRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	Source  string `json:"source,omitempty"`
	Text    string `json:"text,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type SystemPresenceRequest struct{}

type SystemEventRequest struct {
	Text             string   `json:"text"`
	DeviceID         string   `json:"device_id,omitempty"`
	InstanceID       string   `json:"instance_id,omitempty"`
	Host             string   `json:"host,omitempty"`
	IP               string   `json:"ip,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	Version          string   `json:"version,omitempty"`
	Platform         string   `json:"platform,omitempty"`
	DeviceFamily     string   `json:"device_family,omitempty"`
	ModelIdentifier  string   `json:"model_identifier,omitempty"`
	LastInputSeconds float64  `json:"last_input_seconds,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	Roles            []string `json:"roles,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

func (r WizardStartRequest) Normalize() (WizardStartRequest, error) {
	r.Mode = strings.TrimSpace(r.Mode)
	if r.Mode == "" {
		r.Mode = "local"
	}
	return r, nil
}

func (r WizardNextRequest) Normalize() (WizardNextRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	if r.Input == nil {
		r.Input = map[string]any{}
	}
	return r, nil
}

func (r WizardCancelRequest) Normalize() (WizardCancelRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	return r, nil
}

func (r WizardStatusRequest) Normalize() (WizardStatusRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	return r, nil
}

func (r UpdateRunRequest) Normalize() (UpdateRunRequest, error) { return r, nil }

func (r LastHeartbeatRequest) Normalize() (LastHeartbeatRequest, error) { return r, nil }

func (r SetHeartbeatsRequest) Normalize() (SetHeartbeatsRequest, error) {
	if r.Enabled == nil {
		return r, fmt.Errorf("enabled is required")
	}
	if r.IntervalMS < 0 {
		return r, fmt.Errorf("interval_ms cannot be negative")
	}
	if r.Enabled != nil && *r.Enabled && r.IntervalMS == 0 {
		return r, fmt.Errorf("interval_ms is required when enabled is true")
	}
	if r.IntervalMS > 0 {
		r.IntervalMS = normalizeLimit(r.IntervalMS, 60_000, 3_600_000)
	}
	return r, nil
}

func (r WakeRequest) Normalize() (WakeRequest, error) {
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.Source = strings.TrimSpace(r.Source)
	r.Text = strings.TrimSpace(r.Text)
	if r.Text == "" {
		return r, fmt.Errorf("text is required")
	}
	r.Mode = strings.ToLower(strings.TrimSpace(r.Mode))
	if r.Mode == "" {
		r.Mode = "now"
	}
	switch r.Mode {
	case "now", "next-heartbeat":
	default:
		return r, fmt.Errorf("mode must be one of: now, next-heartbeat")
	}
	return r, nil
}

func (r SystemPresenceRequest) Normalize() (SystemPresenceRequest, error) { return r, nil }

func (r SystemEventRequest) Normalize() (SystemEventRequest, error) {
	r.Text = strings.TrimSpace(r.Text)
	r.DeviceID = strings.TrimSpace(r.DeviceID)
	r.InstanceID = strings.TrimSpace(r.InstanceID)
	r.Host = strings.TrimSpace(r.Host)
	r.IP = strings.TrimSpace(r.IP)
	r.Mode = strings.TrimSpace(r.Mode)
	r.Version = strings.TrimSpace(r.Version)
	r.Platform = strings.TrimSpace(r.Platform)
	r.DeviceFamily = strings.TrimSpace(r.DeviceFamily)
	r.ModelIdentifier = strings.TrimSpace(r.ModelIdentifier)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.Text == "" {
		return r, fmt.Errorf("text is required")
	}
	r.Roles = compactStringSlice(r.Roles)
	r.Scopes = compactStringSlice(r.Scopes)
	r.Tags = compactStringSlice(r.Tags)
	if r.LastInputSeconds < 0 {
		r.LastInputSeconds = 0
	}
	return r, nil
}

func DecodeSandboxRunParams(params json.RawMessage) (SandboxRunRequest, error) {
	return decodeMethodParams[SandboxRunRequest](params)
}

func DecodeWizardStartParams(params json.RawMessage) (WizardStartRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return WizardStartRequest{}, nil
	}
	return decodeMethodParams[WizardStartRequest](params)
}

func DecodeWizardNextParams(params json.RawMessage) (WizardNextRequest, error) {
	return decodeMethodParams[WizardNextRequest](params)
}

func DecodeWizardCancelParams(params json.RawMessage) (WizardCancelRequest, error) {
	return decodeMethodParams[WizardCancelRequest](params)
}

func DecodeWizardStatusParams(params json.RawMessage) (WizardStatusRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return WizardStatusRequest{}, nil
	}
	return decodeMethodParams[WizardStatusRequest](params)
}

func DecodeUpdateRunParams(params json.RawMessage) (UpdateRunRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return UpdateRunRequest{}, nil
	}
	return decodeMethodParams[UpdateRunRequest](params)
}

func DecodeLastHeartbeatParams(params json.RawMessage) (LastHeartbeatRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return LastHeartbeatRequest{}, nil
	}
	return decodeMethodParams[LastHeartbeatRequest](params)
}

func DecodeSetHeartbeatsParams(params json.RawMessage) (SetHeartbeatsRequest, error) {
	return decodeMethodParams[SetHeartbeatsRequest](params)
}

func DecodeWakeParams(params json.RawMessage) (WakeRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return WakeRequest{}, nil
	}
	return decodeMethodParams[WakeRequest](params)
}

func DecodeSystemPresenceParams(params json.RawMessage) (SystemPresenceRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return SystemPresenceRequest{}, nil
	}
	return decodeMethodParams[SystemPresenceRequest](params)
}

func DecodeSystemEventParams(params json.RawMessage) (SystemEventRequest, error) {
	return decodeMethodParams[SystemEventRequest](params)
}
