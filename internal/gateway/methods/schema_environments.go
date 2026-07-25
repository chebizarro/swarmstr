package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Environment method schemas. Params mirror the OpenClaw environments.* wire
// contract: list is empty, status/destroy address one environment id, and
// create instantiates a configured profile with a caller-supplied idempotency
// key. Environments are managed by internal/gateway/environments on top of the
// docker sandbox subsystem.

type EnvironmentsListRequest struct{}

type EnvironmentsStatusRequest struct {
	EnvironmentID string `json:"environmentId"`
}

type EnvironmentsCreateRequest struct {
	ProfileID      string `json:"profileId"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type EnvironmentsDestroyRequest struct {
	EnvironmentID string `json:"environmentId"`
	Force         bool   `json:"force,omitempty"`
}

func (r EnvironmentsStatusRequest) Normalize() (EnvironmentsStatusRequest, error) {
	r.EnvironmentID = strings.TrimSpace(r.EnvironmentID)
	if r.EnvironmentID == "" {
		return r, fmt.Errorf("invalid environments.status params: environmentId is required")
	}
	return r, nil
}

func (r EnvironmentsCreateRequest) Normalize() (EnvironmentsCreateRequest, error) {
	r.ProfileID = strings.TrimSpace(r.ProfileID)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if r.ProfileID == "" {
		return r, fmt.Errorf("invalid environments.create params: profileId is required")
	}
	if r.IdempotencyKey == "" {
		return r, fmt.Errorf("invalid environments.create params: idempotencyKey is required")
	}
	return r, nil
}

func (r EnvironmentsDestroyRequest) Normalize() (EnvironmentsDestroyRequest, error) {
	r.EnvironmentID = strings.TrimSpace(r.EnvironmentID)
	if r.EnvironmentID == "" {
		return r, fmt.Errorf("invalid environments.destroy params: environmentId is required")
	}
	return r, nil
}

func DecodeEnvironmentsListParams(params json.RawMessage) (EnvironmentsListRequest, error) {
	return decodeMethodParams[EnvironmentsListRequest](params)
}

func DecodeEnvironmentsStatusParams(params json.RawMessage) (EnvironmentsStatusRequest, error) {
	return decodeMethodParams[EnvironmentsStatusRequest](params)
}

func DecodeEnvironmentsCreateParams(params json.RawMessage) (EnvironmentsCreateRequest, error) {
	return decodeMethodParams[EnvironmentsCreateRequest](params)
}

func DecodeEnvironmentsDestroyParams(params json.RawMessage) (EnvironmentsDestroyRequest, error) {
	return decodeMethodParams[EnvironmentsDestroyRequest](params)
}
