package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dop251/goja"
	"metiq/internal/plugins/sdk"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Stubs for coverage tests
// ═══════════════════════════════════════════════════════════════════════════════

type stubStorage struct {
	data map[string][]byte
}

func (s *stubStorage) Get(_ context.Context, key string) ([]byte, error) {
	v, ok := s.data[key]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (s *stubStorage) Set(_ context.Context, key string, value []byte) error {
	s.data[key] = value
	return nil
}

func (s *stubStorage) Del(_ context.Context, key string) error {
	delete(s.data, key)
	return nil
}

type stubNostr struct {
	published []map[string]any
	events    []map[string]any
}

type liveHTTPHost struct{}

func (h *liveHTTPHost) Get(ctx context.Context, url string, headers map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

func (h *liveHTTPHost) Post(ctx context.Context, url string, body []byte, headers map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, respBody, nil
}

func (s *stubNostr) Publish(_ context.Context, evt map[string]any) error {
	s.published = append(s.published, evt)
	return nil
}

func (s *stubNostr) Fetch(_ context.Context, _ map[string]any, _ int) ([]map[string]any, error) {
	return s.events, nil
}

func (s *stubNostr) Encrypt(_ context.Context, _, content string) (string, error) {
	return "enc:" + content, nil
}

func (s *stubNostr) Decrypt(_ context.Context, _, ciphertext string) (string, error) {
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

type stubAgent struct {
	reply string
}

func (s *stubAgent) Complete(_ context.Context, _ string, _ sdk.CompletionOpts) (string, error) {
	return s.reply, nil
}

type errStorage struct{}

func (e *errStorage) Get(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("storage unavailable")
}
func (e *errStorage) Set(_ context.Context, _ string, _ []byte) error {
	return fmt.Errorf("storage unavailable")
}
func (e *errStorage) Del(_ context.Context, _ string) error {
	return fmt.Errorf("storage unavailable")
}

// ═══════════════════════════════════════════════════════════════════════════════
// exportStringMap tests
// ═══════════════════════════════════════════════════════════════════════════════

func TestExportStringMap_Nil(t *testing.T) {
	if got := exportStringMap(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestExportStringMap_Undefined(t *testing.T) {
	if got := exportStringMap(goja.Undefined()); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestExportStringMap_Null(t *testing.T) {
	if got := exportStringMap(goja.Null()); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestExportStringMap_NonMap(t *testing.T) {
	vm := goja.New()
	if got := exportStringMap(vm.ToValue("a plain string")); got != nil {
		t.Errorf("expected nil for non-map, got %v", got)
	}
}

func TestExportStringMap_ValidMap(t *testing.T) {
	vm := goja.New()
	obj := vm.NewObject()
	_ = obj.Set("Content-Type", "application/json")
	_ = obj.Set("Authorization", "Bearer token123")
	got := exportStringMap(obj)
	if got == nil {
		t.Fatal("expected non-nil map")
	}
	if got["Content-Type"] != "application/json" {
		t.Errorf("Content-Type: %q", got["Content-Type"])
	}
	if got["Authorization"] != "Bearer token123" {
		t.Errorf("Authorization: %q", got["Authorization"])
	}
}

func TestExportStringMap_MixedValues(t *testing.T) {
	vm := goja.New()
	obj := vm.NewObject()
	_ = obj.Set("str", "value")
	_ = obj.Set("num", 42)   // non-string → should be skipped
	_ = obj.Set("bool", true) // non-string → should be skipped
	got := exportStringMap(obj)
	if got == nil {
		t.Fatal("expected non-nil map")
	}
	if got["str"] != "value" {
		t.Errorf("str: %q", got["str"])
	}
	if _, ok := got["num"]; ok {
		t.Error("num should not be in map")
	}
	if _, ok := got["bool"]; ok {
		t.Error("bool should not be in map")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// joinArgs tests
// ═══════════════════════════════════════════════════════════════════════════════

func TestJoinArgs_Empty(t *testing.T) {
	if got := joinArgs(nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestJoinArgs_Single(t *testing.T) {
	vm := goja.New()
	args := []goja.Value{vm.ToValue("hello")}
	if got := joinArgs(args); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestJoinArgs_Multiple(t *testing.T) {
	vm := goja.New()
	args := []goja.Value{vm.ToValue("hello"), vm.ToValue("world"), vm.ToValue(42)}
	got := joinArgs(args)
	if got != "hello world 42" {
		t.Errorf("got %q", got)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// wrapModule tests
// ═══════════════════════════════════════════════════════════════════════════════

func TestWrapModule(t *testing.T) {
	src := `exports.foo = 42;`
	got := wrapModule(src)
	if !strings.Contains(got, "function(exports, require, module)") {
		t.Error("missing CommonJS wrapper header")
	}
	if !strings.Contains(got, src) {
		t.Error("source not embedded in wrapper")
	}
	if !strings.HasSuffix(got, "(exports, require, {exports: exports});") {
		t.Error("missing wrapper footer")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// setStubNamespace tests
// ═══════════════════════════════════════════════════════════════════════════════

func TestSetStubNamespace_CallPanics(t *testing.T) {
	vm := goja.New()
	if err := setStubNamespace(vm, "test_ns", "not available"); err != nil {
		t.Fatal(err)
	}
	// Calling any method on the stub should throw a TypeError.
	_, err := vm.RunString("test_ns.get('key')")
	if err == nil {
		t.Fatal("expected panic from stub namespace")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("error should contain reason, got: %v", err)
	}
}

func TestSetStubNamespace_AllMethodsStubbed(t *testing.T) {
	vm := goja.New()
	_ = setStubNamespace(vm, "myns", "disabled")
	methods := []string{"publish", "fetch", "encrypt", "decrypt",
		"get", "set", "del", "post", "complete", "info", "warn", "error"}
	for _, m := range methods {
		_, err := vm.RunString(fmt.Sprintf("myns.%s()", m))
		if err == nil {
			t.Errorf("method %q should have panicked", m)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// defaultLogHost tests
// ═══════════════════════════════════════════════════════════════════════════════

func TestDefaultLogHost_DoesNotPanic(t *testing.T) {
	h := &defaultLogHost{}
	// These should not panic; they just forward to slog.
	h.Info("info message", "key", "val")
	h.Warn("warn message")
	h.Error("error message", "err", "boom")
}

// ═══════════════════════════════════════════════════════════════════════════════
// wireHTTP integration via JS plugin
// ═══════════════════════════════════════════════════════════════════════════════

func TestWireHTTP_JSGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"msg":"hi"}`))
	}))
	defer srv.Close()

	src := fmt.Sprintf(`
exports.manifest = { id: "http-test", version: "1.0.0" };
exports.invoke = function(tool, args) {
	var resp = http.get("%s", {});
	return { status: resp.status, body: resp.body };
};
`, srv.URL)

	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		HTTP:   &liveHTTPHost{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "fetch"})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("not a map: %T", res.Value)
	}
	if m["body"] != `{"msg":"hi"}` {
		t.Errorf("body: %v", m["body"])
	}
}

func TestWireHTTP_JSPost(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 1024)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	src := fmt.Sprintf(`
exports.manifest = { id: "http-post-test", version: "1.0.0" };
exports.invoke = function(tool, args) {
	var resp = http.post("%s", "hello body", {"Content-Type": "text/plain"});
	return { status: resp.status, body: resp.body };
};
`, srv.URL)

	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		HTTP:   &liveHTTPHost{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "post"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["body"] != "ok" {
		t.Errorf("body: %v", m["body"])
	}
	if gotBody != "hello body" {
		t.Errorf("server got body: %q", gotBody)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// wireStorage integration via JS plugin
// ═══════════════════════════════════════════════════════════════════════════════

func TestWireStorage_SetGetDel(t *testing.T) {
	store := &stubStorage{data: make(map[string][]byte)}
	src := `
exports.manifest = { id: "storage-test", version: "1.0.0" };
exports.invoke = function(tool, args) {
	storage.set("mykey", "myvalue");
	var v = storage.get("mykey");
	storage.del("mykey");
	var after = storage.get("mykey");
	return { before: v, after: after };
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:     &stubLog{},
		Config:  &stubConfig{},
		Storage: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["before"] != "myvalue" {
		t.Errorf("before: %v", m["before"])
	}
	// after del, get returns nil → null in JS
	if m["after"] != nil {
		t.Errorf("after: %v (expected nil)", m["after"])
	}
}

func TestWireStorage_NilFallsBackToStub(t *testing.T) {
	src := `
exports.manifest = { id: "storage-stub-test", version: "1.0.0" };
exports.invoke = function() {
	try { storage.get("key"); return { ok: false }; } catch(e) { return { ok: true, msg: e.message }; }
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		HTTP:   &liveHTTPHost{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("stub should have thrown: %v", m)
	}
}

func TestWireStorage_GetError(t *testing.T) {
	src := `
exports.manifest = { id: "storage-err-test", version: "1.0.0" };
exports.invoke = function() {
	var v = storage.get("key");
	return { val: v };
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:     &stubLog{},
		Config:  &stubConfig{},
		Storage: &errStorage{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	// On storage.get error the code returns goja.Null()
	m := res.Value.(map[string]any)
	if m["val"] != nil {
		t.Errorf("expected null on get error, got: %v", m["val"])
	}
}

func TestWireStorage_SetError(t *testing.T) {
	src := `
exports.manifest = { id: "storage-set-err", version: "1.0.0" };
exports.invoke = function() {
	try {
		storage.set("key", "value");
		return { ok: false };
	} catch(e) {
		return { ok: true };
	}
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:     &stubLog{},
		Config:  &stubConfig{},
		Storage: &errStorage{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("expected panic on set error: %v", m)
	}
}

func TestWireStorage_DelError(t *testing.T) {
	src := `
exports.manifest = { id: "storage-del-err", version: "1.0.0" };
exports.invoke = function() {
	try {
		storage.del("key");
		return { ok: false };
	} catch(e) {
		return { ok: true };
	}
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:     &stubLog{},
		Config:  &stubConfig{},
		Storage: &errStorage{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("expected panic on del error: %v", m)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// wireNostr integration via JS plugin
// ═══════════════════════════════════════════════════════════════════════════════

func TestWireNostr_PublishAndFetch(t *testing.T) {
	ns := &stubNostr{events: []map[string]any{{"id": "abc", "kind": 1}}}
	src := `
exports.manifest = { id: "nostr-test", version: "1.0.0" };
exports.invoke = function() {
	nostr.publish({ kind: 1, content: "hello" });
	var evts = nostr.fetch({ kinds: [1] }, 10);
	return { count: evts.length, first: evts[0].id };
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		Nostr:  ns,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if len(ns.published) != 1 {
		t.Errorf("expected 1 published event, got %d", len(ns.published))
	}
	if m["first"] != "abc" {
		t.Errorf("first event ID: %v", m["first"])
	}
}

func TestWireNostr_EncryptDecrypt(t *testing.T) {
	ns := &stubNostr{}
	src := `
exports.manifest = { id: "nostr-enc-test", version: "1.0.0" };
exports.invoke = function() {
	var cipher = nostr.encrypt("pubkey123", "secret");
	var plain = nostr.decrypt("pubkey123", cipher);
	return { cipher: cipher, plain: plain };
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		Nostr:  ns,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["cipher"] != "enc:secret" {
		t.Errorf("cipher: %v", m["cipher"])
	}
	if m["plain"] != "secret" {
		t.Errorf("plain: %v", m["plain"])
	}
}

func TestWireNostr_NilFallsBackToStub(t *testing.T) {
	src := `
exports.manifest = { id: "nostr-stub-test", version: "1.0.0" };
exports.invoke = function() {
	try { nostr.publish({}); return { ok: false }; } catch(e) { return { ok: true }; }
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		HTTP:   &liveHTTPHost{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("stub should have thrown: %v", m)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// wireAgent integration via JS plugin
// ═══════════════════════════════════════════════════════════════════════════════

func TestWireAgent_Complete(t *testing.T) {
	agent := &stubAgent{reply: "I am a helpful assistant"}
	src := `
exports.manifest = { id: "agent-test", version: "1.0.0" };
exports.invoke = function() {
	var reply = agent.complete("Hello!", { model: "claude-opus-4", max_tokens: 100 });
	return { reply: reply };
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		Agent:  agent,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["reply"] != "I am a helpful assistant" {
		t.Errorf("reply: %v", m["reply"])
	}
}

func TestWireAgent_NilFallsBackToStub(t *testing.T) {
	src := `
exports.manifest = { id: "agent-stub-test", version: "1.0.0" };
exports.invoke = function() {
	try { agent.complete("hi"); return { ok: false }; } catch(e) { return { ok: true }; }
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		HTTP:   &liveHTTPHost{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("stub should have thrown: %v", m)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// wireConfig with nil (stub fallback)
// ═══════════════════════════════════════════════════════════════════════════════

func TestWireConfig_NilFallsBackToStub(t *testing.T) {
	src := `
exports.manifest = { id: "config-stub-test", version: "1.0.0" };
exports.invoke = function() {
	try { config.get("key"); return { ok: false }; } catch(e) { return { ok: true }; }
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log: &stubLog{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("stub should have thrown: %v", m)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// wireLog with nil (uses defaultLogHost)
// ═══════════════════════════════════════════════════════════════════════════════

func TestWireLog_NilUsesDefault(t *testing.T) {
	src := `
exports.manifest = { id: "log-nil-test", version: "1.0.0" };
exports.invoke = function() {
	log.info("test info");
	log.warn("test warn");
	log.error("test error");
	return { ok: true };
};
`
	// Pass nil Log host — should use defaultLogHost
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Config: &stubConfig{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("expected ok: %v", m)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Invoke edge cases
// ═══════════════════════════════════════════════════════════════════════════════

func TestInvoke_CancelledContext(t *testing.T) {
	p, err := LoadPlugin(context.Background(), []byte(minimalPlugin), makeHost(&stubLog{}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err = p.Invoke(ctx, sdk.InvokeRequest{Tool: "echo"})
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestInvoke_PluginThrows(t *testing.T) {
	src := `
exports.manifest = { id: "throw-test", version: "1.0.0" };
exports.invoke = function() { throw new Error("kaboom"); };
`
	p, err := LoadPlugin(context.Background(), []byte(src), makeHost(&stubLog{}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "anything"})
	if err == nil {
		t.Error("expected error when plugin throws")
	}
	if res.Error == "" {
		t.Error("expected Error string in result")
	}
}

func TestInvoke_ReturnsPromiseFulfilled(t *testing.T) {
	src := `
exports.manifest = { id: "promise-test", version: "1.0.0" };
exports.invoke = function() { return Promise.resolve({ val: 42 }); };
`
	p, err := LoadPlugin(context.Background(), []byte(src), makeHost(&stubLog{}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := res.Value.(map[string]any)
	if !ok {
		t.Fatalf("not a map: %T", res.Value)
	}
	if m["val"] != int64(42) {
		t.Errorf("val: %v (%T)", m["val"], m["val"])
	}
}

func TestInvoke_ReturnsPromiseRejected(t *testing.T) {
	src := `
exports.manifest = { id: "reject-test", version: "1.0.0" };
exports.invoke = function() { return Promise.reject("nope"); };
`
	p, err := LoadPlugin(context.Background(), []byte(src), makeHost(&stubLog{}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err == nil {
		t.Error("expected error for rejected promise")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error: %v", err)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// wireHTTP error path via JS (http.get with bad server)
// ═══════════════════════════════════════════════════════════════════════════════

func TestWireHTTP_JSGetError(t *testing.T) {
	src := `
exports.manifest = { id: "http-err", version: "1.0.0" };
exports.invoke = function() {
	try {
		http.get("http://127.0.0.1:1", {});
		return { ok: false };
	} catch(e) {
		return { ok: true };
	}
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		HTTP:   &liveHTTPHost{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("expected caught error: %v", m)
	}
}

func TestWireHTTP_JSPostError(t *testing.T) {
	src := `
exports.manifest = { id: "http-post-err", version: "1.0.0" };
exports.invoke = function() {
	try {
		http.post("http://127.0.0.1:1", "body", {});
		return { ok: false };
	} catch(e) {
		return { ok: true };
	}
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		HTTP:   &liveHTTPHost{},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("expected caught error: %v", m)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// wireNostr error: publish non-object arg triggers TypeError
// ═══════════════════════════════════════════════════════════════════════════════

func TestWireNostr_PublishNonObject(t *testing.T) {
	ns := &stubNostr{}
	src := `
exports.manifest = { id: "nostr-bad-pub", version: "1.0.0" };
exports.invoke = function() {
	try {
		nostr.publish("not an object");
		return { ok: false };
	} catch(e) {
		return { ok: true, msg: e.message };
	}
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		Nostr:  ns,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("expected caught type error: %v", m)
	}
}

func TestWireNostr_FetchNonObject(t *testing.T) {
	ns := &stubNostr{}
	src := `
exports.manifest = { id: "nostr-bad-fetch", version: "1.0.0" };
exports.invoke = function() {
	try {
		nostr.fetch("not an object");
		return { ok: false };
	} catch(e) {
		return { ok: true };
	}
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{},
		Nostr:  ns,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["ok"] != true {
		t.Errorf("expected caught type error: %v", m)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// wireConfig get returning null for missing key
// ═══════════════════════════════════════════════════════════════════════════════

func TestWireConfig_GetMissingKeyReturnsNull(t *testing.T) {
	src := `
exports.manifest = { id: "config-null-test", version: "1.0.0" };
exports.invoke = function() {
	var val = config.get("nonexistent.key");
	return { isNull: val === null };
};
`
	p, err := LoadPlugin(context.Background(), []byte(src), &sdk.Host{
		Log:    &stubLog{},
		Config: &stubConfig{data: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Invoke(context.Background(), sdk.InvokeRequest{Tool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Value.(map[string]any)
	if m["isNull"] != true {
		t.Errorf("expected null for missing config key: %v", m)
	}
}
