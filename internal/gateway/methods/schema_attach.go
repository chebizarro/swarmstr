package methods

import (
	"encoding/json"
	"fmt"
	"strings"
)

// attach.* method schemas (OpenClaw parity, WS-A/A7). attach.grant mints a
// bearer token bound to one session key for the MCP loopback surface;
// attach.revoke invalidates it. Metiq deviation: sessionKey is required —
// there is no implicit "main session" default to fall back to.

// AttachGrantRequest mints one attach grant.
type AttachGrantRequest struct {
	SessionKey string `json:"sessionKey"`
	// TTLMs is the requested lifetime. Non-positive or absent values fall back
	// to the store default; oversized values are clamped (mirrors OpenClaw).
	TTLMs int64 `json:"ttl_ms,omitempty"`
}

// AttachRevokeRequest invalidates one attach grant token.
type AttachRevokeRequest struct {
	Token string `json:"token"`
}

func (r AttachGrantRequest) Normalize() (AttachGrantRequest, error) {
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	if r.SessionKey == "" {
		return r, fmt.Errorf("invalid attach.grant params: sessionKey is required")
	}
	return r, nil
}

func (r AttachRevokeRequest) Normalize() (AttachRevokeRequest, error) {
	r.Token = strings.TrimSpace(r.Token)
	if r.Token == "" {
		return r, fmt.Errorf("invalid attach.revoke params: token is required")
	}
	return r, nil
}

func DecodeAttachGrantParams(params json.RawMessage) (AttachGrantRequest, error) {
	return decodeMethodParams[AttachGrantRequest](params)
}

func DecodeAttachRevokeParams(params json.RawMessage) (AttachRevokeRequest, error) {
	return decodeMethodParams[AttachRevokeRequest](params)
}
