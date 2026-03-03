package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"swarmstr/internal/gateway/methods"
	"swarmstr/internal/memory"
	"swarmstr/internal/store/state"
)

func TestDispatchMethodCallSupportedMethods(t *testing.T) {
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodSupportedMethods, nil)

	result, status, err := dispatchMethodCall(context.Background(), rr, req, ServerOptions{})
	if err != nil {
		t.Fatalf("dispatchMethodCall error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	list, ok := result.([]string)
	if !ok || len(list) == 0 {
		t.Fatalf("unexpected result type/value: %#v", result)
	}
}

func TestDispatchMethodCallStatusGet(t *testing.T) {
	opts := ServerOptions{Status: StatusProvider{PubKey: "pub", Relays: []string{"wss://r"}, DMPolicy: "open", Started: time.Now().Add(-5 * time.Second)}}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodStatus, nil)

	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil {
		t.Fatalf("dispatchMethodCall error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	res, ok := result.(methods.StatusResponse)
	if !ok {
		t.Fatalf("result type = %T", result)
	}
	if res.PubKey != "pub" || res.DMPolicy != "open" {
		t.Fatalf("unexpected status response: %+v", res)
	}
}

func TestDispatchMethodCallMemorySearchPositionalParams(t *testing.T) {
	var gotQuery string
	var gotLimit int
	opts := ServerOptions{
		SearchMemory: func(query string, limit int) []memory.IndexedMemory {
			gotQuery = query
			gotLimit = limit
			return []memory.IndexedMemory{{MemoryID: "m1", Text: "note"}}
		},
	}
	rr := httptest.NewRecorder()
	req := newRawMethodRequest(t, `{"method":"memory.search","params":["hello",12]}`)

	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil {
		t.Fatalf("dispatchMethodCall error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if gotQuery != "hello" || gotLimit != 12 {
		t.Fatalf("unexpected positional params decode query=%q limit=%d", gotQuery, gotLimit)
	}
}

func TestDispatchMethodCallMemorySearchNormalization(t *testing.T) {
	var gotQuery string
	var gotLimit int
	opts := ServerOptions{
		SearchMemory: func(query string, limit int) []memory.IndexedMemory {
			gotQuery = query
			gotLimit = limit
			return []memory.IndexedMemory{{MemoryID: "m1", Text: "note"}}
		},
	}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodMemorySearch, map[string]any{"query": "  hello  ", "limit": 999})

	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil {
		t.Fatalf("dispatchMethodCall error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if gotQuery != "hello" {
		t.Fatalf("query = %q, want %q", gotQuery, "hello")
	}
	if gotLimit != 200 {
		t.Fatalf("limit = %d, want 200", gotLimit)
	}
	res, ok := result.(methods.MemorySearchResponse)
	if !ok || len(res.Results) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestDispatchMethodCallRejectsUnknownParams(t *testing.T) {
	opts := ServerOptions{SearchMemory: func(query string, limit int) []memory.IndexedMemory { return nil }}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodMemorySearch, map[string]any{"query": "x", "extra": true})

	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
}

func TestDispatchMethodCallRequiresAuthWhenConfigured(t *testing.T) {
	opts := ServerOptions{
		Status: StatusProvider{PubKey: "pub", Relays: []string{"wss://r"}, DMPolicy: "open", Started: time.Now().Add(-5 * time.Second)},
		GetConfig: func(context.Context) (state.ConfigDoc, error) {
			return state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, Admins: []state.ControlAdmin{{PubKey: "npub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq7m7p4j", Methods: []string{"*"}}}}}, nil
		},
	}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodStatus, nil)

	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil {
		t.Fatal("expected auth error")
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", status, http.StatusUnauthorized)
	}
}

func TestDispatchMethodCallFailsClosedWhenConfigLoadFails(t *testing.T) {
	opts := ServerOptions{
		Status: StatusProvider{PubKey: "pub", Relays: []string{"wss://r"}, DMPolicy: "open", Started: time.Now().Add(-5 * time.Second)},
		GetConfig: func(context.Context) (state.ConfigDoc, error) {
			return state.ConfigDoc{}, context.DeadlineExceeded
		},
	}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodStatus, nil)

	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil {
		t.Fatal("expected auth error")
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", status, http.StatusUnauthorized)
	}
}

func TestDispatchMethodCallAllowsLegacyTokenFallback(t *testing.T) {
	opts := ServerOptions{
		Status: StatusProvider{PubKey: "pub", Relays: []string{"wss://r"}, DMPolicy: "open", Started: time.Now().Add(-5 * time.Second)},
		GetConfig: func(context.Context) (state.ConfigDoc, error) {
			return state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, LegacyTokenFallback: true}}, nil
		},
	}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodStatus, nil)
	ctx := context.WithValue(context.Background(), tokenAuthContextKey, true)

	_, status, err := dispatchMethodCall(ctx, rr, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
}

func TestDispatchMethodCallConfigPutRequiresPolicy(t *testing.T) {
	called := false
	opts := ServerOptions{PutConfig: func(context.Context, state.ConfigDoc) error { called = true; return nil }}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodConfigPut, map[string]any{"config": map[string]any{"version": 1}})
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	if called {
		t.Fatal("put config should not be called on invalid request")
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodConfigPut, map[string]any{"config": map[string]any{"dm": map[string]any{"policy": "open"}}})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if !called {
		t.Fatal("put config should be called")
	}
}

func TestDispatchMethodCallListGetAndPut(t *testing.T) {
	getCalled := false
	putCalled := false
	opts := ServerOptions{
		GetList: func(_ context.Context, name string) (state.ListDoc, error) {
			getCalled = true
			return state.ListDoc{Version: 1, Name: name, Items: []string{"a", "b"}}, nil
		},
		PutList: func(_ context.Context, name string, doc state.ListDoc) error {
			putCalled = true
			if name != "allowlist" {
				t.Fatalf("unexpected list name: %q", name)
			}
			if len(doc.Items) != 2 {
				t.Fatalf("unexpected items: %+v", doc.Items)
			}
			return nil
		},
	}

	rr := httptest.NewRecorder()
	req := newRawMethodRequest(t, `{"method":"list.get","params":["allowlist"]}`)
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil {
		t.Fatalf("list.get error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if !getCalled {
		t.Fatal("expected get list callback")
	}
	list, ok := result.(state.ListDoc)
	if !ok || list.Name != "allowlist" {
		t.Fatalf("unexpected list.get result: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newRawMethodRequest(t, `{"method":"list.put","params":["allowlist",["a","a","b"," "]]}`)
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil {
		t.Fatalf("list.put error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	if !putCalled {
		t.Fatal("expected put list callback")
	}
}

func TestDispatchMethodCallListPutPreconditionConflict(t *testing.T) {
	opts := ServerOptions{
		GetListWithEvent: func(_ context.Context, _ string) (state.ListDoc, state.Event, error) {
			return state.ListDoc{Version: 2, Name: "allowlist"}, state.Event{ID: "evt-2"}, nil
		},
		PutList: func(_ context.Context, _ string, _ state.ListDoc) error {
			t.Fatal("put list must not be called on precondition failure")
			return nil
		},
	}
	rr := httptest.NewRecorder()
	req := newRawMethodRequest(t, `{"method":"list.put","params":{"name":"allowlist","items":["a"],"expected_version":1}}`)
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil {
		t.Fatal("expected precondition error")
	}
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if !strings.Contains(err.Error(), "current_version=2") {
		t.Fatalf("expected conflict metadata, got: %v", err)
	}
}

func TestDispatchMethodCallConfigPutPreconditionConflict(t *testing.T) {
	opts := ServerOptions{
		GetConfigWithEvent: func(_ context.Context) (state.ConfigDoc, state.Event, error) {
			return state.ConfigDoc{Version: 3, DM: state.DMPolicy{Policy: "open"}}, state.Event{ID: "evt-current"}, nil
		},
		PutConfig: func(_ context.Context, _ state.ConfigDoc) error {
			t.Fatal("put config must not be called on precondition failure")
			return nil
		},
	}
	rr := httptest.NewRecorder()
	req := newRawMethodRequest(t, `{"method":"config.put","params":{"config":{"dm":{"policy":"open"}},"expected_event":"evt-other"}}`)
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil {
		t.Fatal("expected precondition error")
	}
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if !strings.Contains(err.Error(), "current_event=evt-current") {
		t.Fatalf("expected conflict metadata, got: %v", err)
	}
}

func TestDispatchMethodCallRelayPolicyGet(t *testing.T) {
	opts := ServerOptions{
		GetRelayPolicy: func(context.Context) (methods.RelayPolicyResponse, error) {
			return methods.RelayPolicyResponse{
				ReadRelays:           []string{"wss://read"},
				WriteRelays:          []string{"wss://write"},
				RuntimeDMRelays:      []string{"wss://write"},
				RuntimeControlRelays: []string{"wss://write"},
			}, nil
		},
	}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodRelayPolicyGet, nil)
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil {
		t.Fatalf("relay.policy.get error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	view, ok := result.(methods.RelayPolicyResponse)
	if !ok || len(view.ReadRelays) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func newMethodRequest(t *testing.T, method string, params any) *http.Request {
	t.Helper()
	payload := map[string]any{"method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(raw))
}

func newRawMethodRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	return httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader([]byte(raw)))
}

func TestIsNIP86RPC(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(nil))
	req.Header.Set("Content-Type", "application/nostr+json+rpc")
	if !isNIP86RPC(req) {
		t.Fatal("expected NIP86 profile detection from content type")
	}
}

func TestWriteNIP86JSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeNIP86JSON(rr, map[string]any{"result": true})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/nostr+json+rpc" {
		t.Fatalf("content-type = %q", got)
	}
}
