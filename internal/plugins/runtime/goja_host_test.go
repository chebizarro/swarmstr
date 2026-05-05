package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

// ─── stub implementations ─────────────────────────────────────────────────────

type stubLog struct{ messages []string }

func (s *stubLog) Info(msg string, args ...any)  { s.messages = append(s.messages, "INFO: "+msg) }
func (s *stubLog) Warn(msg string, args ...any)  { s.messages = append(s.messages, "WARN: "+msg) }
func (s *stubLog) Error(msg string, args ...any) { s.messages = append(s.messages, "ERROR: "+msg) }

type stubConfig struct{ data map[string]any }

func (s *stubConfig) Get(key string) any { return s.data[key] }

// ─── minimal plugin ──────────────────────────────────────────────────────────

const minimalPlugin = `
exports.manifest = {
	id: "test-plugin",
	version: "1.0.0",
	description: "test",
	tools: [{ name: "echo", description: "echo the input" }],
};

exports.invoke = function(tool, args, meta) {
	if (tool === "echo") {
		return { echoed: args.message };
	}
	return { error: "unknown tool: " + tool };
};
`

func makeHost(l *stubLog) *sdk.Host {
	return &sdk.Host{
		Log:    l,
		Config: &stubConfig{data: map[string]any{"agent.default_model": "claude-opus-4"}},
	}
}

func TestLoadPlugin_minimalPlugin(t *testing.T) {
	l := &stubLog{}
	p, err := LoadPlugin(context.Background(), []byte(minimalPlugin), makeHost(l))
	if err != nil {
		t.Fatalf("LoadPlugin failed: %v", err)
	}
	if p.Manifest().ID != "test-plugin" {
		t.Errorf("manifest ID: %q", p.Manifest().ID)
	}
	if p.Manifest().Version != "1.0.0" {
		t.Errorf("manifest version: %q", p.Manifest().Version)
	}
	if len(p.Manifest().Tools) != 1 || p.Manifest().Tools[0].Name != "echo" {
		t.Errorf("manifest tools: %v", p.Manifest().Tools)
	}
}

func TestInvoke_echo(t *testing.T) {
	p, err := LoadPlugin(context.Background(), []byte(minimalPlugin), makeHost(&stubLog{}))
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{
		Tool: "echo",
		Args: map[string]any{"message": "hello metiq"},
	})
	if err != nil {
		t.Fatalf("Invoke error: %v", err)
	}
	m, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T %v", res.Value, res.Value)
	}
	if m["echoed"] != "hello metiq" {
		t.Errorf("echoed mismatch: %v", m["echoed"])
	}
}

func TestInvoke_unknownTool(t *testing.T) {
	p, err := LoadPlugin(context.Background(), []byte(minimalPlugin), makeHost(&stubLog{}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "nope"})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	m, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %v", res.Value)
	}
	if errMsg, _ := m["error"].(string); !strings.Contains(errMsg, "nope") {
		t.Errorf("expected tool name in error, got: %q", errMsg)
	}
}

func TestLoadPlugin_missingManifest(t *testing.T) {
	src := `exports.invoke = function(){};`
	_, err := LoadPlugin(context.Background(), []byte(src), makeHost(&stubLog{}))
	if err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Errorf("expected manifest error, got: %v", err)
	}
}

func TestLoadPlugin_missingInvoke(t *testing.T) {
	src := `exports.manifest = { id: "no-invoke", version: "1.0.0" };`
	_, err := LoadPlugin(context.Background(), []byte(src), makeHost(&stubLog{}))
	if err == nil || !strings.Contains(err.Error(), "invoke") {
		t.Errorf("expected invoke error, got: %v", err)
	}
}

func TestLoadPlugin_missingManifestID(t *testing.T) {
	src := `
exports.manifest = { version: "1.0.0" };
exports.invoke = function(){};
`
	_, err := LoadPlugin(context.Background(), []byte(src), makeHost(&stubLog{}))
	if err == nil || !strings.Contains(err.Error(), "id") {
		t.Errorf("expected id error, got: %v", err)
	}
}

func TestLoadPlugin_invalidToolSchema(t *testing.T) {
	src := `
exports.manifest = {
	id: "bad-schema",
	version: "1.0.0",
	tools: [{ name: "echo", parameters: { type: "string" } }],
};
exports.invoke = function(){};
`
	_, err := LoadPlugin(context.Background(), []byte(src), makeHost(&stubLog{}))
	if err == nil || !strings.Contains(err.Error(), "parameters.type must be object") {
		t.Errorf("expected schema validation error, got: %v", err)
	}
}

func TestLoadPlugin_configAccess(t *testing.T) {
	src := `
exports.manifest = { id: "cfg-plugin", version: "1.0.0" };
exports.invoke = function(tool, args) {
	return { model: config.get("agent.default_model") };
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), makeHost(&stubLog{}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("not a map: %v", res.Value)
	}
	if m["model"] != "claude-opus-4" {
		t.Errorf("config.get: %v", m["model"])
	}
}

func TestLoadPlugin_logCalls(t *testing.T) {
	l := &stubLog{}
	src := `
exports.manifest = { id: "log-plugin", version: "1.0.0" };
exports.invoke = function() {
	log.info("hello from plugin");
	log.warn("something odd");
	return {};
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{Log: l, Config: &stubConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.messages) < 2 {
		t.Errorf("expected 2 log messages, got %d: %v", len(l.messages), l.messages)
	}
}

func TestLoadPlugin_syntaxError(t *testing.T) {
	src := `this is not valid javascript @@@ {{{{`
	_, err := LoadPlugin(context.Background(), []byte(src), makeHost(&stubLog{}))
	if err == nil {
		t.Error("expected compile error")
	}
}

func TestLoadPlugin_missingHostNamespacesFailDeterministically(t *testing.T) {
	cases := []struct {
		name      string
		namespace string
		invoke    string
	}{
		{name: "nostr", namespace: "nostr", invoke: `return nostr.fetch({});`},
		{name: "config", namespace: "config", invoke: `return config.get("agent.default_model");`},
		{name: "http", namespace: "http", invoke: `return http.get("https://example.com");`},
		{name: "storage", namespace: "storage", invoke: `return storage.get("key");`},
		{name: "agent", namespace: "agent", invoke: `return agent.complete("hello");`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `
exports.manifest = { id: "` + tc.name + `-plugin", version: "1.0.0" };
exports.invoke = function() { ` + tc.invoke + ` };
`
			p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{Log: &stubLog{}})
			if err != nil {
				t.Fatalf("LoadPlugin with nil %s host should still load until namespace use: %v", tc.namespace, err)
			}

			_, err = p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "x"})
			if err == nil {
				t.Fatalf("expected invoke to fail when %s host is missing", tc.namespace)
			}
			var unavailable *HostUnavailableError
			if !errors.As(err, &unavailable) || unavailable.Namespace != tc.namespace {
				t.Fatalf("expected typed missing %s host error, got: %T %v", tc.namespace, err, err)
			}
		})
	}
}

func TestLoadPlugin_unavailableHostSentinelSupportsFallbackGuards(t *testing.T) {
	src := `
exports.manifest = { id: "guard-plugin", version: "1.0.0" };
exports.invoke = function() {
	if (http && http.available !== false && http.get) {
		return http.get("https://example.com");
	}
	return { fallback: true, reason: http.reason };
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{Log: &stubLog{}})
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "x"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	m, ok := res.Value.(map[string]any)
	if !ok || m["fallback"] != true {
		t.Fatalf("expected guarded fallback result, got %#v", res.Value)
	}
}

type blockingHTTPHost struct {
	entered chan struct{}
}

func (h *blockingHTTPHost) Get(ctx context.Context, _ string, _ map[string]string) (int, []byte, error) {
	close(h.entered)
	<-ctx.Done()
	return 0, nil, ctx.Err()
}

func (h *blockingHTTPHost) Post(ctx context.Context, _ string, _ []byte, _ map[string]string) (int, []byte, error) {
	close(h.entered)
	<-ctx.Done()
	return 0, nil, ctx.Err()
}

func TestInvoke_HTTPHostReceivesInvokeContextCancellation(t *testing.T) {
	src := `
exports.manifest = { id: "http-cancel-plugin", version: "1.0.0" };
exports.invoke = function() { return http.get("https://example.com"); };
`
	httpHost := &blockingHTTPHost{entered: make(chan struct{})}
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{Log: &stubLog{}, Config: &stubConfig{}, HTTP: httpHost})
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, invokeErr := p.Invoke(ctx, sdk.InvokeRequest{Tool: "x"})
		done <- invokeErr
	}()
	<-httpHost.entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation from HTTP host, got: %T %v", err, err)
	}
}
