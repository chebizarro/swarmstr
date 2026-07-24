package main

import (
	"testing"

	"metiq/internal/gateway/protocol"
	"metiq/internal/store/state"
)

func TestGatewayDeviceTokenValidatorUsesPersistedNodePairing(t *testing.T) {
	cfg := state.ConfigDoc{Extra: map[string]any{"pairing": map[string]any{
		"node_paired": []map[string]any{{"node_id": "node-1", "token": "node-secret"}},
	}}}
	validate := gatewayDeviceTokenValidator(newRuntimeConfigStore(cfg))
	connect := protocol.ConnectParams{Role: "node", Device: &protocol.ConnectDevice{ID: "node-1"}}
	if decision := validate(connect, "node-secret"); !decision.OK || decision.Role != "node" {
		t.Fatalf("valid node token rejected: %+v", decision)
	}
	if decision := validate(connect, "revoked"); decision.OK || decision.Code != "DEVICE_AUTH_TOKEN_MISMATCH" {
		t.Fatalf("invalid node token accepted: %+v", decision)
	}
	connect.Device.ID = "removed-node"
	if decision := validate(connect, "node-secret"); decision.OK || decision.Code != "DEVICE_AUTH_NOT_PAIRED" {
		t.Fatalf("removed node accepted: %+v", decision)
	}
}

func TestGatewayDeviceTokenValidatorEnforcesApprovedScopes(t *testing.T) {
	cfg := state.ConfigDoc{Extra: map[string]any{"pairing": map[string]any{
		"device_paired": []map[string]any{{
			"device_id":       "device-1",
			"approved_scopes": []string{"operator.read"},
			"tokens": map[string]any{
				"operator": map[string]any{"token": "device-secret", "scopes": []string{"operator.read"}},
			},
		}},
	}}}
	validate := gatewayDeviceTokenValidator(newRuntimeConfigStore(cfg))
	connect := protocol.ConnectParams{
		Role:   "operator",
		Scopes: []string{"operator.read"},
		Device: &protocol.ConnectDevice{ID: "device-1"},
	}
	if decision := validate(connect, "device-secret"); !decision.OK || len(decision.Scopes) != 1 || decision.Scopes[0] != "operator.read" {
		t.Fatalf("approved device token rejected: %+v", decision)
	}
	connect.Scopes = []string{"operator.admin"}
	if decision := validate(connect, "device-secret"); decision.OK || decision.Code != "DEVICE_AUTH_SCOPE_MISMATCH" {
		t.Fatalf("scope escalation accepted: %+v", decision)
	}
}
