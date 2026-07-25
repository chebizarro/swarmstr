package methods

// Param schemas for the gateway lifecycle surface (swarmstr-iiot):
// gateway.restart.preflight (readiness snapshot, no params) and
// gateway.restart.request (trigger the daemon restart scheduler).
//
// Shapes mirror OpenClaw src/gateway/server-methods/restart.ts. Metiq does not
// implement OpenClaw's targeted (pid/ownerId/port) restart intent — the daemon
// owns a single restart channel — so only reason + delay are accepted.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxRestartReasonLength mirrors OpenClaw's operator-visible reason cap.
const maxRestartReasonLength = 200

// MaxRestartDelayMS bounds the caller-requested restart grace period.
const MaxRestartDelayMS = 5 * 60 * 1000

// GatewayRestartPreflightRequest takes no parameters.
type GatewayRestartPreflightRequest struct{}

// GatewayRestartRequestRequest triggers a daemon restart. Reason is
// operator-visible log context; DelayMS is the grace period before the daemon
// tears down (defaults applied by the scheduler when zero).
type GatewayRestartRequestRequest struct {
	Reason  string `json:"reason,omitempty"`
	DelayMS int    `json:"delayMs,omitempty"`
}

func (r GatewayRestartPreflightRequest) Normalize() (GatewayRestartPreflightRequest, error) {
	return r, nil
}

func (r GatewayRestartRequestRequest) Normalize() (GatewayRestartRequestRequest, error) {
	r.Reason = strings.TrimSpace(r.Reason)
	if len([]rune(r.Reason)) > maxRestartReasonLength {
		r.Reason = string([]rune(r.Reason)[:maxRestartReasonLength])
	}
	if r.DelayMS < 0 {
		return r, fmt.Errorf("invalid gateway.restart.request params: delayMs must not be negative")
	}
	if r.DelayMS > MaxRestartDelayMS {
		return r, fmt.Errorf("invalid gateway.restart.request params: delayMs exceeds maximum")
	}
	return r, nil
}

func DecodeGatewayRestartPreflightParams(params json.RawMessage) (GatewayRestartPreflightRequest, error) {
	return decodeMethodParams[GatewayRestartPreflightRequest](params)
}

func DecodeGatewayRestartRequestParams(params json.RawMessage) (GatewayRestartRequestRequest, error) {
	return decodeMethodParams[GatewayRestartRequestRequest](params)
}
