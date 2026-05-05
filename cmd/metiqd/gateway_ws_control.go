package main

import (
	"strings"

	gatewayprotocol "metiq/internal/gateway/protocol"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
)

func gatewayControlRPCInbound(principal gatewayws.ControlPrincipal, req gatewayprotocol.RequestFrame) nostruntime.ControlRPCInbound {
	method := strings.TrimSpace(req.Method)
	callerPubKey := strings.TrimSpace(principal.PubKey)
	authenticated := principal.Authenticated
	if strings.EqualFold(strings.TrimSpace(principal.Method), "none") {
		authenticated = false
	}
	return nostruntime.ControlRPCInbound{
		FromPubKey:    callerPubKey,
		Method:        method,
		Params:        req.Params,
		Authenticated: authenticated,
	}
}
