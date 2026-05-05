package main

import (
	"encoding/json"
	"testing"

	gatewayprotocol "metiq/internal/gateway/protocol"
	gatewayws "metiq/internal/gateway/ws"
)

func TestGatewayControlRPCInboundPreservesTokenAuthentication(t *testing.T) {
	params := json.RawMessage(`{"k":"v"}`)
	in := gatewayControlRPCInbound(gatewayws.ControlPrincipal{Authenticated: true, Method: "token"}, gatewayprotocol.RequestFrame{Method: "chat.send", Params: params})
	if !in.Authenticated || in.FromPubKey != "" || in.Method != "chat.send" || string(in.Params) != string(params) {
		t.Fatalf("unexpected token-auth inbound: %+v", in)
	}
}

func TestGatewayControlRPCInboundDoesNotAuthenticateNoneMethod(t *testing.T) {
	in := gatewayControlRPCInbound(gatewayws.ControlPrincipal{Authenticated: true, Method: "none"}, gatewayprotocol.RequestFrame{Method: " sessions.list "})
	if in.Authenticated || in.Method != "sessions.list" {
		t.Fatalf("unexpected none-auth inbound: %+v", in)
	}
}

func TestGatewayControlRPCInboundCarriesNIP98Caller(t *testing.T) {
	in := gatewayControlRPCInbound(gatewayws.ControlPrincipal{Authenticated: true, Method: "nip98", PubKey: " abc "}, gatewayprotocol.RequestFrame{Method: "status.get"})
	if !in.Authenticated || in.FromPubKey != "abc" {
		t.Fatalf("unexpected nip98 inbound: %+v", in)
	}
}

func TestGatewayControlRPCInboundCarriesDeviceCaller(t *testing.T) {
	deviceID := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	in := gatewayControlRPCInbound(gatewayws.ControlPrincipal{Authenticated: true, Method: "device", PubKey: " " + deviceID + " "}, gatewayprotocol.RequestFrame{Method: "config.set"})
	if !in.Authenticated || in.FromPubKey != deviceID || in.Method != "config.set" {
		t.Fatalf("unexpected device inbound: %+v", in)
	}
}
