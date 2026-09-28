package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"metiq/internal/config"
	"metiq/internal/gateway/protocol"
)

// resolveGatewayWSURL returns the gateway WebSocket URL and token from flags,
// falling back to the gateway_ws_* fields of the bootstrap config the daemon
// itself reads.
func resolveGatewayWSURL(urlFlag, tokenFlag, bootstrapPath string) (string, string, error) {
	wsURL, token := strings.TrimSpace(urlFlag), strings.TrimSpace(tokenFlag)
	if wsURL != "" && token != "" {
		return wsURL, token, nil
	}
	if bootstrapPath == "" {
		p, err := config.DefaultBootstrapPath()
		if err != nil {
			return "", "", err
		}
		bootstrapPath = p
	}
	var bs config.BootstrapConfig
	if raw, err := os.ReadFile(bootstrapPath); err == nil {
		if err := json.Unmarshal(raw, &bs); err != nil {
			return "", "", fmt.Errorf("parse bootstrap config %s: %w", bootstrapPath, err)
		}
	}
	if token == "" {
		token = strings.TrimSpace(bs.GatewayWSToken)
	}
	if wsURL == "" {
		addr := strings.TrimSpace(bs.GatewayWSListenAddr)
		if addr == "" {
			return "", "", fmt.Errorf("gateway websocket not configured: set gateway_ws_listen_addr in %s or pass --ws-url", bootstrapPath)
		}
		if host, port, err := net.SplitHostPort(addr); err == nil && (host == "" || host == "0.0.0.0" || host == "::") {
			addr = net.JoinHostPort("127.0.0.1", port)
		}
		path := strings.TrimSpace(bs.GatewayWSPath)
		if path == "" {
			path = "/ws"
		}
		wsURL = "ws://" + addr + path
	}
	return wsURL, token, nil
}

// gatewayWSCall performs one operator request over the gateway WebSocket.
// Methods that handle secret values (secrets.store.*) are accepted only on
// this transport, never over admin HTTP or the Nostr control bus.
func gatewayWSCall(ctx context.Context, wsURL, token, method string, params any) (json.RawMessage, error) {
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial gateway websocket %s: %w", wsURL, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	// hello-ok carries the full method/descriptor catalog, well past the
	// library's 32 KiB default.
	conn.SetReadLimit(16 << 20)

	var challenge struct {
		Event   string `json:"event"`
		Payload struct {
			Nonce string `json:"nonce"`
		} `json:"payload"`
	}
	if err := wsjson.Read(ctx, conn, &challenge); err != nil {
		return nil, fmt.Errorf("read gateway challenge: %w", err)
	}
	if challenge.Event != "connect.challenge" || challenge.Payload.Nonce == "" {
		return nil, fmt.Errorf("gateway sent %q instead of connect.challenge", challenge.Event)
	}
	connect := protocol.ConnectParams{
		MinProtocol: protocol.MinProtocolVersion,
		MaxProtocol: protocol.CurrentProtocolVersion,
		Client:      protocol.ConnectClient{ID: "cli", Version: version, Platform: runtime.GOOS, Mode: "cli"},
		Role:        "operator",
		Auth:        &protocol.ConnectAuth{Token: token, Nonce: challenge.Payload.Nonce},
	}
	if _, err := gatewayWSRoundTrip(ctx, conn, "connect", "connect", connect); err != nil {
		return nil, err
	}
	return gatewayWSRoundTrip(ctx, conn, "call", method, params)
}

func gatewayWSRoundTrip(ctx context.Context, conn *websocket.Conn, id, method string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req := protocol.RequestFrame{Type: protocol.FrameTypeRequest, ID: id, Method: method, Params: raw}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		return nil, fmt.Errorf("%s: write request: %w", method, err)
	}
	for {
		var frame protocol.ResponseFrame
		if err := wsjson.Read(ctx, conn, &frame); err != nil {
			return nil, fmt.Errorf("%s: read response: %w", method, err)
		}
		if frame.Type != protocol.FrameTypeResponse || frame.ID != id {
			continue // interleaved push events (presence, ticks)
		}
		if !frame.OK {
			msg := "request failed"
			if frame.Error != nil {
				msg = frame.Error.Message
			}
			return nil, fmt.Errorf("%s: %s", method, msg)
		}
		return frame.Payload, nil
	}
}
