package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nostruntime "metiq/internal/nostr/runtime"
)

type stubGatewayClient struct {
	method string
	params any
	result map[string]any
	err    error
}

func (s *stubGatewayClient) call(method string, params any) (map[string]any, error) {
	s.method = method
	s.params = params
	return s.result, s.err
}

func writeBootstrapFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}
	return path
}

func TestRunGW_UsesConfiguredNostrTransport(t *testing.T) {
	oldResolver := resolveGWClientFn
	defer func() { resolveGWClientFn = oldResolver }()

	stub := &stubGatewayClient{result: map[string]any{"ok": true}}
	var gotTransport, gotTarget, gotSigner string
	var gotTimeout time.Duration
	resolveGWClientFn = func(transport, addrFlag, tokenFlag, bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (gatewayCaller, error) {
		gotTransport = transport
		gotTarget = controlTargetPubKey
		gotSigner = controlSignerURL
		gotTimeout = timeout
		return stub, nil
	}

	out, err := captureStdout(t, func() error {
		return runGW([]string{
			"--transport", "nostr",
			"--control-target-pubkey", "npub1target",
			"--control-signer-url", "env://METIQ_CONTROL_SIGNER",
			"--timeout", "12",
			"status.get",
			`{"verbose":true}`,
		})
	})
	if err != nil {
		t.Fatalf("runGW error: %v", err)
	}
	if gotTransport != "nostr" {
		t.Fatalf("unexpected transport: %q", gotTransport)
	}
	if gotTarget != "npub1target" {
		t.Fatalf("unexpected control target: %q", gotTarget)
	}
	if gotSigner != "env://METIQ_CONTROL_SIGNER" {
		t.Fatalf("unexpected control signer: %q", gotSigner)
	}
	if gotTimeout != 12*time.Second {
		t.Fatalf("unexpected timeout: %v", gotTimeout)
	}
	raw, ok := stub.params.(json.RawMessage)
	if !ok {
		t.Fatalf("expected raw params, got %T", stub.params)
	}
	if strings.TrimSpace(string(raw)) != `{"verbose":true}` {
		t.Fatalf("unexpected raw params: %s", string(raw))
	}
	if stub.method != "status.get" {
		t.Fatalf("unexpected method: %s", stub.method)
	}
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestResolveNostrControlClientRequiresTargetPubKey(t *testing.T) {
	bootstrapPath := writeBootstrapFile(t, `{
  "private_key":"1111111111111111111111111111111111111111111111111111111111111111",
  "relays":["wss://relay.example.com"]
}`)
	_, err := resolveNostrControlClient(bootstrapPath, "", "", time.Second)
	if err == nil || !strings.Contains(err.Error(), "target pubkey not configured") {
		t.Fatalf("expected missing target error, got %v", err)
	}
}

func TestResolveNostrControlClientRejectsSelfRequestSigner(t *testing.T) {
	privateKey := "1111111111111111111111111111111111111111111111111111111111111111"
	pubkey := nostruntime.MustPublicKeyHex(privateKey)
	bootstrapPath := writeBootstrapFile(t, `{
  "private_key":"`+privateKey+`",
  "relays":["wss://relay.example.com"],
  "control_target_pubkey":"`+pubkey+`"
}`)
	_, err := resolveNostrControlClient(bootstrapPath, "", "", time.Second)
	if err == nil || !strings.Contains(err.Error(), "matches target daemon pubkey") {
		t.Fatalf("expected self-request error, got %v", err)
	}
}

func TestResolveNostrControlClientPrefersExplicitControlSigner(t *testing.T) {
	targetKey := "1111111111111111111111111111111111111111111111111111111111111111"
	callerKey := "2222222222222222222222222222222222222222222222222222222222222222"
	targetPubKey := nostruntime.MustPublicKeyHex(targetKey)
	callerPubKey := nostruntime.MustPublicKeyHex(callerKey)
	bootstrapPath := writeBootstrapFile(t, `{
  "private_key":"`+targetKey+`",
  "relays":["wss://relay.example.com"],
  "control_target_pubkey":"`+targetPubKey+`",
  "control_signer_url":"`+callerKey+`"
}`)
	client, err := resolveNostrControlClient(bootstrapPath, "", "", time.Second)
	if err != nil {
		t.Fatalf("resolveNostrControlClient error: %v", err)
	}
	defer client.Close()
	if client.callerPubKey != callerPubKey {
		t.Fatalf("unexpected caller pubkey: got %s want %s", client.callerPubKey, callerPubKey)
	}
	if client.targetPubKey != targetPubKey {
		t.Fatalf("unexpected target pubkey: got %s want %s", client.targetPubKey, targetPubKey)
	}
}
