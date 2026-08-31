package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
			"--json",
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
	if gotTimeout != 12*time.Millisecond {
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

func TestRunGW_DefaultsToAutoTransport(t *testing.T) {
	oldResolver := resolveGWClientFn
	defer func() { resolveGWClientFn = oldResolver }()

	stub := &stubGatewayClient{result: map[string]any{"ok": true}}
	var gotTransport string
	var gotTimeout time.Duration
	resolveGWClientFn = func(transport, addrFlag, tokenFlag, bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (gatewayCaller, error) {
		gotTransport = transport
		gotTimeout = timeout
		return stub, nil
	}

	out, err := captureStdout(t, func() error {
		return runGW([]string{"status.get"})
	})
	if err != nil {
		t.Fatalf("runGW error: %v", err)
	}
	if gotTransport != "auto" {
		t.Fatalf("unexpected default transport: %q", gotTransport)
	}
	if gotTimeout != 30*time.Second {
		t.Fatalf("unexpected default timeout: %v", gotTimeout)
	}
	if strings.Contains(out, `"ok"`) || !strings.Contains(out, "map[ok:true]") {
		t.Fatalf("expected human-readable output by default, got %q", out)
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

func newCallCaptureServer(t *testing.T) (*httptest.Server, *string, *map[string]any) {
	t.Helper()
	var gotMethod string
	var gotParams map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/call" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotMethod = req.Method
		gotParams = req.Params
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"ok":true}}`))
	}))
	return srv, &gotMethod, &gotParams
}

func TestRunGW_NewSubcommands(t *testing.T) {
	oldResolver := resolveGWClientFn
	defer func() { resolveGWClientFn = oldResolver }()
	stub := &stubGatewayClient{result: map[string]any{"ok": true}}
	resolveGWClientFn = func(transport, addrFlag, tokenFlag, bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (gatewayCaller, error) {
		return stub, nil
	}
	if _, err := captureStdout(t, func() error { return runGW([]string{"run", `{"port":8787}`}) }); err != nil {
		t.Fatalf("runGW run error: %v", err)
	}
	if stub.method != "gateway.run" {
		t.Fatalf("unexpected run method: %s", stub.method)
	}
	if _, err := captureStdout(t, func() error { return runGW([]string{"call", "status.get"}) }); err != nil {
		t.Fatalf("runGW call error: %v", err)
	}
	if stub.method != "status.get" {
		t.Fatalf("unexpected call method: %s", stub.method)
	}
}

func TestNewCommandWrappers_CallExpectedMethods(t *testing.T) {
	srv, gotMethod, gotParams := newCallCaptureServer(t)
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	if _, err := captureStdout(t, func() error {
		return runCron([]string{"edit", "--admin-addr", addr, "--schedule", "@daily", "--disable", "job-1"})
	}); err != nil {
		t.Fatalf("cron edit error: %v", err)
	}
	if *gotMethod != "cron.update" || (*gotParams)["id"] != "job-1" {
		t.Fatalf("unexpected cron call: %s %#v", *gotMethod, *gotParams)
	}

	if _, err := captureStdout(t, func() error {
		return runNodes([]string{"notify", "--admin-addr", addr, "--node", "node-1", "--args", `{"title":"hi"}`})
	}); err != nil {
		t.Fatalf("nodes notify error: %v", err)
	}
	if *gotMethod != "node.invoke" || (*gotParams)["command"] != "notify" || (*gotParams)["node_id"] != "node-1" {
		t.Fatalf("unexpected nodes call: %s %#v", *gotMethod, *gotParams)
	}

	if _, err := captureStdout(t, func() error {
		return runUpdate([]string{"wizard", "--admin-addr", addr})
	}); err != nil {
		t.Fatalf("update wizard error: %v", err)
	}
	if *gotMethod != "update.wizard" {
		t.Fatalf("unexpected update call: %s", *gotMethod)
	}
}

func TestResolveGWClient_AutoFallsBackToHTTPWithoutNostrHints(t *testing.T) {
	oldAdmin := resolveAdminGatewayClientFn
	oldNostr := resolveNostrGatewayClientFn
	defer func() {
		resolveAdminGatewayClientFn = oldAdmin
		resolveNostrGatewayClientFn = oldNostr
	}()

	admin := &stubGatewayClient{result: map[string]any{"transport": "http"}}
	var adminCalls, nostrCalls int
	resolveAdminGatewayClientFn = func(addrFlag, tokenFlag, bootstrapPath string) (gatewayCaller, error) {
		adminCalls++
		return admin, nil
	}
	resolveNostrGatewayClientFn = func(bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (gatewayCaller, error) {
		nostrCalls++
		return &stubGatewayClient{result: map[string]any{"transport": "nostr"}}, nil
	}

	client, err := resolveGWClient("auto", "", "", writeBootstrapFile(t, `{
  "private_key":"1111111111111111111111111111111111111111111111111111111111111111",
  "relays":["wss://relay.example.com"]
}`), "", "", time.Second)
	if err != nil {
		t.Fatalf("resolveGWClient error: %v", err)
	}
	result, err := client.call("status.get", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("auto client call error: %v", err)
	}
	if result["transport"] != "http" {
		t.Fatalf("unexpected transport result: %#v", result)
	}
	if adminCalls != 1 || nostrCalls != 0 {
		t.Fatalf("unexpected resolver counts admin=%d nostr=%d", adminCalls, nostrCalls)
	}
}

func TestResolveGWClient_AutoIgnoresSignerOnlyHintWithoutTarget(t *testing.T) {
	oldAdmin := resolveAdminGatewayClientFn
	oldNostr := resolveNostrGatewayClientFn
	defer func() {
		resolveAdminGatewayClientFn = oldAdmin
		resolveNostrGatewayClientFn = oldNostr
	}()

	var adminCalls, nostrCalls int
	resolveAdminGatewayClientFn = func(addrFlag, tokenFlag, bootstrapPath string) (gatewayCaller, error) {
		adminCalls++
		return &stubGatewayClient{result: map[string]any{"transport": "http"}}, nil
	}
	resolveNostrGatewayClientFn = func(bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (gatewayCaller, error) {
		nostrCalls++
		return &stubGatewayClient{result: map[string]any{"transport": "nostr"}}, nil
	}

	client, err := resolveGWClient("auto", "", "", writeBootstrapFile(t, `{
  "private_key":"1111111111111111111111111111111111111111111111111111111111111111",
  "relays":["wss://relay.example.com"]
}`), "", "env://METIQ_CONTROL_SIGNER", time.Second)
	if err != nil {
		t.Fatalf("resolveGWClient error: %v", err)
	}
	result, err := client.call("status.get", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("auto client call error: %v", err)
	}
	if result["transport"] != "http" {
		t.Fatalf("unexpected transport result: %#v", result)
	}
	if adminCalls != 1 || nostrCalls != 0 {
		t.Fatalf("unexpected resolver counts admin=%d nostr=%d", adminCalls, nostrCalls)
	}
}

func TestResolveGWClient_AutoPrefersNostrWithConfiguredTarget(t *testing.T) {
	oldAdmin := resolveAdminGatewayClientFn
	oldNostr := resolveNostrGatewayClientFn
	defer func() {
		resolveAdminGatewayClientFn = oldAdmin
		resolveNostrGatewayClientFn = oldNostr
	}()

	var adminCalls, nostrCalls int
	resolveAdminGatewayClientFn = func(addrFlag, tokenFlag, bootstrapPath string) (gatewayCaller, error) {
		adminCalls++
		return &stubGatewayClient{result: map[string]any{"transport": "http"}}, nil
	}
	resolveNostrGatewayClientFn = func(bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (gatewayCaller, error) {
		nostrCalls++
		return &stubGatewayClient{result: map[string]any{"transport": "nostr"}}, nil
	}

	client, err := resolveGWClient("auto", "", "", writeBootstrapFile(t, `{
  "private_key":"1111111111111111111111111111111111111111111111111111111111111111",
  "relays":["wss://relay.example.com"],
  "control_target_pubkey":"npub1target"
}`), "", "", time.Second)
	if err != nil {
		t.Fatalf("resolveGWClient error: %v", err)
	}
	result, err := client.call("status.get", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("auto client call error: %v", err)
	}
	if result["transport"] != "nostr" {
		t.Fatalf("unexpected transport result: %#v", result)
	}
	if adminCalls != 0 || nostrCalls != 1 {
		t.Fatalf("unexpected resolver counts admin=%d nostr=%d", adminCalls, nostrCalls)
	}
}

func TestResolveGWClient_HTTPOverrideUsesAdminEvenWithConfiguredTarget(t *testing.T) {
	oldAdmin := resolveAdminGatewayClientFn
	oldNostr := resolveNostrGatewayClientFn
	defer func() {
		resolveAdminGatewayClientFn = oldAdmin
		resolveNostrGatewayClientFn = oldNostr
	}()

	var adminCalls, nostrCalls int
	resolveAdminGatewayClientFn = func(addrFlag, tokenFlag, bootstrapPath string) (gatewayCaller, error) {
		adminCalls++
		return &stubGatewayClient{result: map[string]any{"transport": "http"}}, nil
	}
	resolveNostrGatewayClientFn = func(bootstrapPath, controlTargetPubKey, controlSignerURL string, timeout time.Duration) (gatewayCaller, error) {
		nostrCalls++
		return &stubGatewayClient{result: map[string]any{"transport": "nostr"}}, nil
	}

	client, err := resolveGWClient("http", "", "", writeBootstrapFile(t, `{
  "private_key":"1111111111111111111111111111111111111111111111111111111111111111",
  "relays":["wss://relay.example.com"],
  "control_target_pubkey":"npub1target"
}`), "", "", time.Second)
	if err != nil {
		t.Fatalf("resolveGWClient error: %v", err)
	}
	result, err := client.call("status.get", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("http override client call error: %v", err)
	}
	if result["transport"] != "http" {
		t.Fatalf("unexpected transport result: %#v", result)
	}
	if adminCalls != 1 || nostrCalls != 0 {
		t.Fatalf("unexpected resolver counts admin=%d nostr=%d", adminCalls, nostrCalls)
	}
}
