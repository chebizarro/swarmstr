package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/protocol"
	gatewayws "metiq/internal/gateway/ws"
)

type storeSetCall struct {
	params       json.RawMessage
	connectionID bool
}

// startSecretsGateway runs the real gateway WebSocket runtime with a handler
// that records secrets.store.set requests, and returns its listen address.
func startSecretsGateway(t *testing.T, token string) (string, <-chan storeSetCall) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	calls := make(chan storeSetCall, 1)
	names := append(methods.SupportedMethods(), gatewayws.MethodEventsList, gatewayws.MethodEventsSubscribe, gatewayws.MethodEventsUnsubscribe)
	ctx, cancel := context.WithCancel(context.Background())
	rt, err := gatewayws.Start(ctx, gatewayws.RuntimeOptions{
		Addr:              addr,
		Token:             token,
		Methods:           names,
		MethodDescriptors: methods.MethodDescriptors(names),
		Version:           "test",
		HandshakeTTL:      5 * time.Second,
		HandleRequest: func(reqCtx context.Context, req protocol.RequestFrame) (any, *protocol.ErrorShape) {
			_, hasConn := gatewayws.ConnectionIDFromContext(reqCtx)
			calls <- storeSetCall{params: req.Params, connectionID: hasConn}
			return map[string]any{"ok": true}, nil
		},
	})
	if err != nil {
		cancel()
		t.Fatalf("start gateway: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = rt.Shutdown(shutdownCtx)
		cancel()
	})
	// Start serves in the background; wait until the listener accepts.
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway never listened on %s: %v", addr, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return addr, calls
}

func TestSecretsSetStoresValueOverGatewayWebSocket(t *testing.T) {
	addr, calls := startSecretsGateway(t, "gw-token")
	bootstrap := filepath.Join(t.TempDir(), "bootstrap.json")
	raw, _ := json.Marshal(map[string]any{"gateway_ws_listen_addr": addr, "gateway_ws_token": "gw-token"})
	if err := os.WriteFile(bootstrap, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := secretsSet([]string{"--bootstrap", bootstrap, "ZALO_REFRESH_TOKEN"}, strings.NewReader("rt-123\n"), &out)
	if err != nil {
		t.Fatalf("secrets set: %v", err)
	}
	call := <-calls
	if !call.connectionID {
		t.Fatal("request lacked a gateway connection id; the daemon rejects secrets.store.set without one")
	}
	var got methods.SecretsStoreSetRequest
	if err := json.Unmarshal(call.params, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "ZALO_REFRESH_TOKEN" || got.Value != "rt-123" || got.Kind != "secret" {
		t.Fatalf("params = %+v", got)
	}
	if want := `{"source":"store","provider":"gateway-store","id":"ZALO_REFRESH_TOKEN"}`; !strings.Contains(out.String(), want) {
		t.Fatalf("output %q does not print the ref %s", out.String(), want)
	}
}

func TestSecretsSetRejectsWrongGatewayToken(t *testing.T) {
	addr, calls := startSecretsGateway(t, "gw-token")
	err := secretsSet([]string{"--ws-url", "ws://" + addr + "/ws", "--ws-token", "wrong", "NAME"}, strings.NewReader("v"), &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "connect:") {
		t.Fatalf("wrong token err = %v, want connect rejection", err)
	}
	select {
	case call := <-calls:
		t.Fatalf("handler reached with bad token: %s", call.params)
	default:
	}
}

func TestSecretsSetReadsValueOnlyFromStdin(t *testing.T) {
	if err := secretsSet([]string{"--ws-url", "ws://127.0.0.1:1/ws", "--ws-token", "t", "NAME", "inline-value"}, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("argv value err = %v", err)
	}
	if err := secretsSet([]string{"--ws-url", "ws://127.0.0.1:1/ws", "--ws-token", "t", "NAME"}, strings.NewReader("\n"), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty value err = %v", err)
	}
}
