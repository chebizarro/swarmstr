package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	mcppkg "metiq/internal/mcp"
	"metiq/internal/memory"
	"metiq/internal/store/state"
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

func TestDispatchMethodCallSupportedMethodsUsesProvider(t *testing.T) {
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodSupportedMethods, nil)
	result, status, err := dispatchMethodCall(context.Background(), rr, req, ServerOptions{
		SupportedMethods: func(context.Context) ([]string, error) {
			return []string{"ext.one", "ext.two"}, nil
		},
	})
	if err != nil {
		t.Fatalf("dispatchMethodCall error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d", status, http.StatusOK)
	}
	list, ok := result.([]string)
	if !ok || len(list) != 2 || list[0] != "ext.one" || list[1] != "ext.two" {
		t.Fatalf("unexpected provider result type/value: %#v", result)
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
	if res.Version != "metiqd" {
		t.Fatalf("unexpected status version: %q", res.Version)
	}
	if res.UptimeMS <= 0 {
		t.Fatalf("expected uptime_ms > 0, got %d", res.UptimeMS)
	}
}

func TestDispatchMethodCallStatusAlias(t *testing.T) {
	opts := ServerOptions{Status: StatusProvider{PubKey: "pub", Relays: []string{"wss://r"}, DMPolicy: "open", Started: time.Now().Add(-5 * time.Second)}}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodStatusAlias, nil)

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
	if res.Version != "metiqd" {
		t.Fatalf("unexpected status version: %q", res.Version)
	}
	if res.UptimeMS <= 0 {
		t.Fatalf("expected uptime_ms > 0, got %d", res.UptimeMS)
	}
}

func TestDispatchMethodCallStatusGetUsesLiveRelaysProvider(t *testing.T) {
	opts := ServerOptions{
		Status: StatusProvider{PubKey: "pub", Relays: []string{"wss://stale"}, DMPolicy: "open", Started: time.Now().Add(-5 * time.Second)},
		StatusRelays: func() []string {
			return []string{"wss://live-a", "wss://live-b"}
		},
	}
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
	if len(res.Relays) != 2 || res.Relays[0] != "wss://live-a" || res.Relays[1] != "wss://live-b" {
		t.Fatalf("unexpected relays from live provider: %+v", res.Relays)
	}
	if res.Version != "metiqd" {
		t.Fatalf("unexpected status version: %q", res.Version)
	}
	if res.UptimeMS <= 0 {
		t.Fatalf("expected uptime_ms > 0, got %d", res.UptimeMS)
	}
}

func TestDispatchMethodCallStatusGetIncludesMCPProvider(t *testing.T) {
	opts := ServerOptions{
		Status: StatusProvider{PubKey: "pub", Relays: []string{"wss://r"}, DMPolicy: "open", Started: time.Now().Add(-5 * time.Second)},
		StatusMCP: func() *mcppkg.TelemetrySnapshot {
			return &mcppkg.TelemetrySnapshot{
				Enabled: true,
				Summary: mcppkg.TelemetrySummary{Healthy: false, TotalServers: 1, NeedsAuthServers: 1},
				Servers: []mcppkg.TelemetryServer{{
					Name:           "remote",
					State:          string(mcppkg.ConnectionStateNeedsAuth),
					RuntimePresent: true,
				}},
			}
		},
	}
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
	if res.MCP == nil || res.MCP.Summary.NeedsAuthServers != 1 {
		t.Fatalf("unexpected mcp status payload: %+v", res.MCP)
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

func TestDispatchMethodCallListPutExpectedVersionZeroSemantics(t *testing.T) {
	putCalled := false
	opts := ServerOptions{
		GetListWithEvent: func(_ context.Context, name string) (state.ListDoc, state.Event, error) {
			if name == "missing" {
				return state.ListDoc{}, state.Event{}, state.ErrNotFound
			}
			return state.ListDoc{Version: 2, Name: name}, state.Event{ID: "evt-2"}, nil
		},
		PutList: func(_ context.Context, _ string, _ state.ListDoc) error {
			putCalled = true
			return nil
		},
	}

	rr := httptest.NewRecorder()
	req := newRawMethodRequest(t, `{"method":"list.put","params":{"name":"missing","items":["a"],"expected_version":0}}`)
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("expected create-if-missing success status=%d err=%v", status, err)
	}
	if !putCalled {
		t.Fatal("expected put list callback for expected_version=0 on missing list")
	}

	putCalled = false
	rr = httptest.NewRecorder()
	req = newRawMethodRequest(t, `{"method":"list.put","params":{"name":"allowlist","items":["a"],"expected_version":0}}`)
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil {
		t.Fatal("expected conflict when expected_version=0 and list exists")
	}
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
	if putCalled {
		t.Fatal("put list must not be called on expected_version=0 conflict")
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

func TestDispatchMethodCallConfigPutExpectedVersionZeroSemantics(t *testing.T) {
	putCalled := false
	opts := ServerOptions{
		GetConfigWithEvent: func(_ context.Context) (state.ConfigDoc, state.Event, error) {
			if !putCalled {
				return state.ConfigDoc{}, state.Event{}, state.ErrNotFound
			}
			return state.ConfigDoc{Version: 2, DM: state.DMPolicy{Policy: "open"}}, state.Event{ID: "evt-2"}, nil
		},
		PutConfig: func(_ context.Context, _ state.ConfigDoc) error {
			putCalled = true
			return nil
		},
	}

	rr := httptest.NewRecorder()
	req := newRawMethodRequest(t, `{"method":"config.put","params":{"config":{"dm":{"policy":"open"}},"expected_version":0}}`)
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("expected create-if-missing success status=%d err=%v", status, err)
	}
	if !putCalled {
		t.Fatal("expected put config callback for expected_version=0 on missing config")
	}

	rr = httptest.NewRecorder()
	req = newRawMethodRequest(t, `{"method":"config.put","params":{"config":{"dm":{"policy":"open"}},"expected_version":0}}`)
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil {
		t.Fatal("expected conflict when expected_version=0 and config exists")
	}
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d", status, http.StatusConflict)
	}
}

func TestDispatchMethodCallNIP86PreconditionConflictDataFixtures(t *testing.T) {
	type fixtureCase struct {
		Name            string         `json:"name"`
		Method          string         `json:"method"`
		RawParams       map[string]any `json:"raw_params"`
		Resource        string         `json:"resource"`
		ExpectedVersion int            `json:"expected_version"`
		CurrentVersion  int            `json:"current_version"`
		ExpectedEvent   string         `json:"expected_event"`
		CurrentEvent    string         `json:"current_event"`
	}
	type fixtureFile struct {
		Cases []fixtureCase `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "parity", "precondition_nip86_cases.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			opts := ServerOptions{
				GetListWithEvent: func(_ context.Context, _ string) (state.ListDoc, state.Event, error) {
					return state.ListDoc{Version: tc.CurrentVersion, Name: "allowlist"}, state.Event{ID: tc.CurrentEvent}, nil
				},
				PutList: func(_ context.Context, _ string, _ state.ListDoc) error {
					t.Fatal("put list must not be called on precondition failure")
					return nil
				},
				GetConfigWithEvent: func(_ context.Context) (state.ConfigDoc, state.Event, error) {
					return state.ConfigDoc{Version: tc.CurrentVersion, DM: state.DMPolicy{Policy: "open"}}, state.Event{ID: tc.CurrentEvent}, nil
				},
				PutConfig: func(_ context.Context, _ state.ConfigDoc) error {
					t.Fatal("put config must not be called on precondition failure")
					return nil
				},
			}
			rr := httptest.NewRecorder()
			req := newMethodRequest(t, tc.Method, tc.RawParams)
			_, status, callErr := dispatchMethodCall(context.Background(), rr, req, opts)
			if callErr == nil {
				t.Fatalf("expected precondition conflict")
			}
			if status != http.StatusConflict {
				t.Fatalf("status=%d want=%d", status, http.StatusConflict)
			}
			env := map[string]any{"error": methods.MapNIP86Error(status, callErr)}
			rawEnv, err := json.Marshal(env)
			if err != nil {
				t.Fatalf("marshal envelope: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(rawEnv, &body); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			errObj, _ := body["error"].(map[string]any)
			if int(errObj["code"].(float64)) != -32010 {
				t.Fatalf("unexpected nip86 code: %#v", errObj)
			}
			data, _ := errObj["data"].(map[string]any)
			if data["resource"] != tc.Resource {
				t.Fatalf("resource=%v want=%q", data["resource"], tc.Resource)
			}
			if int(data["expected_version"].(float64)) != tc.ExpectedVersion {
				t.Fatalf("expected_version=%v want=%d", data["expected_version"], tc.ExpectedVersion)
			}
			if int(data["current_version"].(float64)) != tc.CurrentVersion {
				t.Fatalf("current_version=%v want=%d", data["current_version"], tc.CurrentVersion)
			}
			if data["expected_event"] != tc.ExpectedEvent {
				t.Fatalf("expected_event=%v want=%q", data["expected_event"], tc.ExpectedEvent)
			}
			if data["current_event"] != tc.CurrentEvent {
				t.Fatalf("current_event=%v want=%q", data["current_event"], tc.CurrentEvent)
			}
		})
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

func TestDispatchMethodCallConfigSetApplyPatchSchema(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "pairing"}, Relays: state.RelayPolicy{Read: []string{"wss://r"}, Write: []string{"wss://r"}}}
	opts := ServerOptions{
		GetConfig: func(context.Context) (state.ConfigDoc, error) { return cfg, nil },
		PutConfig: func(_ context.Context, next state.ConfigDoc) error {
			cfg = next
			return nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodConfigSet, map[string]any{"key": "dm.policy", "value": "open"})
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config.set failed status=%d err=%v", status, err)
	}
	setOut, _ := result.(map[string]any)
	if setOut["ok"] != true || setOut["path"] != "dm.policy" || setOut["config"] == nil {
		t.Fatalf("unexpected config.set response: %#v", result)
	}
	if cfg.DM.Policy != "open" {
		t.Fatalf("expected dm.policy open, got %q", cfg.DM.Policy)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodConfigApply, map[string]any{"config": map[string]any{"version": 2, "dm": map[string]any{"policy": "pairing"}, "relays": map[string]any{"read": []string{"wss://r2"}, "write": []string{"wss://r2"}}}})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config.apply failed status=%d err=%v", status, err)
	}
	applyOut, _ := result.(map[string]any)
	if applyOut["ok"] != true || applyOut["config"] == nil {
		t.Fatalf("unexpected config.apply response: %#v", result)
	}
	if cfg.DM.Policy != "pairing" {
		t.Fatalf("expected dm.policy pairing, got %q", cfg.DM.Policy)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodConfigPatch, map[string]any{"patch": map[string]any{"dm": map[string]any{"policy": "open"}}})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config.patch failed status=%d err=%v", status, err)
	}
	patchOut, _ := result.(map[string]any)
	if patchOut["ok"] != true || patchOut["config"] == nil {
		t.Fatalf("unexpected config.patch response: %#v", result)
	}
	if cfg.DM.Policy != "open" {
		t.Fatalf("expected dm.policy open after patch, got %q", cfg.DM.Policy)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodConfigSchema, nil)
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config.schema failed status=%d err=%v", status, err)
	}
	out, _ := result.(map[string]any)
	if out["fields"] == nil {
		t.Fatalf("unexpected config.schema result: %#v", result)
	}
}

func TestDispatchMethodCallConfigRawMutations(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "pairing"}, Relays: state.RelayPolicy{Read: []string{"wss://r"}, Write: []string{"wss://r"}}}
	opts := ServerOptions{
		GetConfig: func(context.Context) (state.ConfigDoc, error) { return cfg, nil },
		PutConfig: func(_ context.Context, next state.ConfigDoc) error {
			cfg = next
			return nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodConfigApply, map[string]any{"raw": `{"version":4,"dm":{"policy":"open"},"relays":{"read":["wss://a"],"write":["wss://a"]}}`})
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config.apply raw failed status=%d err=%v", status, err)
	}
	if cfg.Version != 4 || cfg.DM.Policy != "open" {
		t.Fatalf("unexpected config after apply raw: %#v", cfg)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodConfigPatch, map[string]any{"raw": `{"dm":{"policy":"pairing"}}`})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config.patch raw failed status=%d err=%v", status, err)
	}
	if cfg.DM.Policy != "pairing" {
		t.Fatalf("unexpected config after patch raw: %#v", cfg)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodConfigSet, map[string]any{"raw": `{"version":5,"dm":{"policy":"open"},"relays":{"read":["wss://b"],"write":["wss://b"]},"secrets":{"api_key":"supersecret"}}`})
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config.set raw failed status=%d err=%v", status, err)
	}
	if cfg.Version != 5 || cfg.DM.Policy != "open" {
		t.Fatalf("unexpected config after set raw: %#v", cfg)
	}
	if cfg.Secrets["api_key"] != "supersecret" {
		t.Fatalf("expected stored config to retain secret, got: %#v", cfg.Secrets)
	}
	out, _ := result.(map[string]any)
	if out["path"] != "raw" {
		t.Fatalf("expected raw path response, got: %#v", out)
	}
	redactedCfg, ok := out["config"].(state.ConfigDoc)
	if !ok {
		t.Fatalf("expected config.set raw response config to be ConfigDoc, got: %#v", out["config"])
	}
	if redactedCfg.Secrets["api_key"] != config.RedactedValue {
		t.Fatalf("expected config.set raw response to redact secrets, got: %#v", redactedCfg.Secrets)
	}
}

func TestDispatchMethodCallConfigGetResponseShape(t *testing.T) {
	opts := ServerOptions{
		GetConfig: func(context.Context) (state.ConfigDoc, error) {
			return state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "open"}}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodConfigGet, nil)
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config.get failed status=%d err=%v", status, err)
	}
	out, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("config.get result should be map[string]any, got %T (%#v)", result, result)
	}
	if _, hasConfig := out["config"]; !hasConfig {
		t.Fatalf("config.get result missing 'config' key: %#v", out)
	}
	if hash, _ := out["base_hash"].(string); hash == "" {
		t.Fatalf("config.get result missing 'base_hash': %#v", out)
	}
	if hash, _ := out["hash"].(string); hash == "" {
		t.Fatalf("config.get result missing 'hash': %#v", out)
	}
}

func TestDispatchMethodCallConfigRawMutationsRequireMatchingBaseHash(t *testing.T) {
	cfg := state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "open"}, Relays: state.RelayPolicy{Read: []string{"wss://r"}, Write: []string{"wss://r"}}}
	putCalled := false
	opts := ServerOptions{
		GetConfig: func(context.Context) (state.ConfigDoc, error) { return cfg, nil },
		PutConfig: func(_ context.Context, next state.ConfigDoc) error {
			putCalled = true
			cfg = next
			return nil
		},
	}

	cases := []struct {
		name   string
		method string
		params map[string]any
	}{
		{
			name:   "config.put",
			method: methods.MethodConfigPut,
			params: map[string]any{"config": map[string]any{"dm": map[string]any{"policy": "pairing"}}, "base_hash": "deadbeef"},
		},
		{
			name:   "config.set raw",
			method: methods.MethodConfigSet,
			params: map[string]any{"raw": `{"version":2,"dm":{"policy":"pairing"},"relays":{"read":["wss://r"],"write":["wss://r"]}}`, "base_hash": "deadbeef"},
		},
		{
			name:   "config.apply raw",
			method: methods.MethodConfigApply,
			params: map[string]any{"raw": `{"version":3,"dm":{"policy":"pairing"},"relays":{"read":["wss://r"],"write":["wss://r"]}}`, "base_hash": "deadbeef"},
		},
		{
			name:   "config.patch raw",
			method: methods.MethodConfigPatch,
			params: map[string]any{"raw": `{"dm":{"policy":"pairing"}}`, "base_hash": "deadbeef"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			putCalled = false
			rr := httptest.NewRecorder()
			req := newMethodRequest(t, tc.method, tc.params)
			_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
			if err == nil {
				t.Fatal("expected conflict error")
			}
			if status != http.StatusConflict {
				t.Fatalf("status=%d want=%d err=%v", status, http.StatusConflict, err)
			}
			if !errors.Is(err, methods.ErrConfigConflict) {
				t.Fatalf("expected ErrConfigConflict, got: %v", err)
			}
			if putCalled {
				t.Fatal("PutConfig must not be called on base_hash conflict")
			}
		})
	}
}

func TestDispatchMethodCallConfigBaseHashRequiresGetConfigProvider(t *testing.T) {
	opts := ServerOptions{
		PutConfig: func(context.Context, state.ConfigDoc) error { return nil },
	}

	cases := []struct {
		name   string
		method string
		params map[string]any
	}{
		{
			name:   "config.put",
			method: methods.MethodConfigPut,
			params: map[string]any{"config": map[string]any{"dm": map[string]any{"policy": "open"}}, "base_hash": "abc"},
		},
		{
			name:   "config.apply",
			method: methods.MethodConfigApply,
			params: map[string]any{"config": map[string]any{"dm": map[string]any{"policy": "open"}}, "base_hash": "abc"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := newMethodRequest(t, tc.method, tc.params)
			_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
			if err == nil {
				t.Fatal("expected error")
			}
			if status != http.StatusNotImplemented {
				t.Fatalf("status=%d want=%d err=%v", status, http.StatusNotImplemented, err)
			}
		})
	}
}

func TestDispatchMethodCallHealthAndSessionsListAndConfigSet(t *testing.T) {
	opts := ServerOptions{
		GetConfig: func(context.Context) (state.ConfigDoc, error) {
			return state.ConfigDoc{Version: 1, DM: state.DMPolicy{Policy: "pairing"}}, nil
		},
		PutConfig: func(context.Context, state.ConfigDoc) error { return nil },
		ListSessions: func(context.Context, int) ([]state.SessionDoc, error) {
			return []state.SessionDoc{{SessionID: "s1"}}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodHealth, nil)
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("health failed status=%d err=%v", status, err)
	}
	if out, _ := result.(map[string]any); out["ok"] != true {
		t.Fatalf("unexpected health result: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSessionsList, map[string]any{"limit": 10})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("sessions.list failed status=%d err=%v", status, err)
	}
	if out, _ := result.(map[string]any); out["sessions"] == nil {
		t.Fatalf("unexpected sessions.list result: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodConfigSet, map[string]any{"key": "dm.policy", "value": "open"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("config.set failed status=%d err=%v", status, err)
	}
}

func TestDispatchMethodCallRuntimeCallbacks(t *testing.T) {
	var gotCursor int64
	var gotLimit int
	var gotMaxBytes int
	var gotLogout string
	var gotObserve methods.RuntimeObserveRequest
	opts := ServerOptions{
		TailLogs: func(_ context.Context, cursor int64, limit int, maxBytes int) (map[string]any, error) {
			gotCursor, gotLimit, gotMaxBytes = cursor, limit, maxBytes
			return map[string]any{"cursor": cursor, "lines": []string{"a"}, "truncated": false, "reset": false}, nil
		},
		ObserveRuntime: func(_ context.Context, req methods.RuntimeObserveRequest) (map[string]any, error) {
			gotObserve = req
			return map[string]any{"events": map[string]any{"cursor": req.EventCursor, "events": []map[string]any{{"event": "tool.start"}}, "truncated": false, "reset": false}, "timed_out": false, "waited_ms": int64(0)}, nil
		},
		ChannelsStatus: func(context.Context, methods.ChannelsStatusRequest) (map[string]any, error) {
			return map[string]any{"channels": []map[string]any{{"id": "nostr", "connected": true}}}, nil
		},
		ChannelsLogout: func(_ context.Context, channel string) (map[string]any, error) {
			gotLogout = channel
			return map[string]any{"ok": true, "channel": channel}, nil
		},
		UsageStatus: func(context.Context) (map[string]any, error) {
			return map[string]any{"ok": true, "totals": map[string]any{"control_calls": int64(3)}}, nil
		},
		UsageCost: func(_ context.Context, req methods.UsageCostRequest) (map[string]any, error) {
			if req.Days != 7 {
				t.Fatalf("unexpected usage.cost days: %d", req.Days)
			}
			return map[string]any{"ok": true, "total_usd": 1.23}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodLogsTail, map[string]any{"cursor": 9, "limit": 5, "max_bytes": 99})
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("logs.tail failed status=%d err=%v", status, err)
	}
	if gotCursor != 9 || gotLimit != 5 || gotMaxBytes != 99 {
		t.Fatalf("unexpected logs params cursor=%d limit=%d maxBytes=%d", gotCursor, gotLimit, gotMaxBytes)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodRuntimeObserve, map[string]any{"include_events": true, "include_logs": false, "event_cursor": 11, "event_limit": 4, "events": []string{"tool.start"}, "agent_id": "agent-1"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("runtime.observe failed status=%d err=%v", status, err)
	}
	if gotObserve.EventCursor != 11 || gotObserve.EventLimit != 4 || gotObserve.AgentID != "agent-1" {
		t.Fatalf("unexpected runtime.observe params: %#v", gotObserve)
	}
	if gotObserve.IncludeLogs == nil || *gotObserve.IncludeLogs {
		t.Fatalf("expected include_logs=false to be preserved: %#v", gotObserve)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodChannelsStatus, map[string]any{"probe": true, "timeout_ms": 123})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("channels.status failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodChannelsLogout, map[string]any{"channel": "nostr"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("channels.logout failed status=%d err=%v", status, err)
	}
	if gotLogout != "nostr" {
		t.Fatalf("unexpected logout channel: %q", gotLogout)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodUsageStatus, nil)
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("usage.status failed status=%d err=%v", status, err)
	}
	out, _ := result.(map[string]any)
	totals, _ := out["totals"].(map[string]any)
	if totals["control_calls"].(int64) != 3 {
		t.Fatalf("unexpected usage.status result: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodUsageCost, map[string]any{"days": 7})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("usage.cost failed status=%d err=%v", status, err)
	}
}

func TestDispatchMethodCallChatHistoryAndSessionViews(t *testing.T) {
	session := state.SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer", LastInboundAt: time.Now().Unix()}
	entries := []state.TranscriptEntryDoc{
		{EntryID: "1", SessionID: "s1", Role: "user", Text: "Need briefing", Unix: time.Now().Unix()},
		{EntryID: "2", SessionID: "s1", Role: "assistant", Text: "Here is the latest update", Unix: time.Now().Unix() + 1},
	}
	sessionStore, err := state.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	if err := sessionStore.Put("s1", state.SessionEntry{SessionID: "s1", AgentID: "main", Label: "Briefing", LastChannel: "nostr", LastTo: "peer", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("seed session store: %v", err)
	}
	opts := ServerOptions{
		GetSession: func(_ context.Context, id string) (state.SessionDoc, error) {
			if id == "missing" {
				return state.SessionDoc{}, state.ErrNotFound
			}
			return session, nil
		},
		ListSessions: func(context.Context, int) ([]state.SessionDoc, error) {
			return []state.SessionDoc{session}, nil
		},
		SessionStore: sessionStore,
		GetConfig: func(context.Context) (state.ConfigDoc, error) {
			return state.ConfigDoc{Agent: state.AgentPolicy{DefaultModel: "gpt-test"}}, nil
		},
		ListTranscript: func(context.Context, string, int) ([]state.TranscriptEntryDoc, error) {
			return entries, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodSessionsList, map[string]any{"limit": 10, "label": "Briefing", "agentId": "main", "includeDerivedTitles": true, "includeLastMessage": true})
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("sessions.list failed status=%d err=%v", status, err)
	}
	out, _ := result.(map[string]any)
	sessions, ok := out["sessions"].([]map[string]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("unexpected sessions.list result: %#v", result)
	}
	if sessions[0]["key"] != "s1" || sessions[0]["label"] != "Briefing" || sessions[0]["lastMessagePreview"] != "Here is the latest update" {
		t.Fatalf("unexpected sessions.list session row: %#v", sessions[0])
	}
	if out["count"].(int) != 1 || out["total"].(int) != 1 || out["defaults"] == nil {
		t.Fatalf("unexpected sessions.list compatibility fields: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSessionsPreview, map[string]any{"session_id": "s1", "limit": 5})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("sessions.preview failed status=%d err=%v", status, err)
	}
	out, _ = result.(map[string]any)
	if len(out["preview"].([]state.TranscriptEntryDoc)) != 2 {
		t.Fatalf("unexpected sessions.preview result: %#v", result)
	}
	if previews, ok := out["previews"].([]map[string]any); !ok || len(previews) != 1 {
		t.Fatalf("unexpected sessions.preview compatibility result: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSessionsPreview, map[string]any{"keys": []string{"s1", "missing"}, "limit": 5})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("sessions.preview(keys) failed status=%d err=%v", status, err)
	}
	out, _ = result.(map[string]any)
	previews, ok := out["previews"].([]map[string]any)
	if !ok || len(previews) != 2 {
		t.Fatalf("unexpected sessions.preview(keys) result: %#v", result)
	}
	if previews[0]["status"] != "ok" || previews[1]["status"] != "missing" {
		t.Fatalf("unexpected preview statuses: %#v", previews)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodChatHistory, map[string]any{"session_id": "s1", "limit": 5})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("chat.history failed status=%d err=%v", status, err)
	}
	out, _ = result.(map[string]any)
	if len(out["entries"].([]state.TranscriptEntryDoc)) != 2 {
		t.Fatalf("unexpected chat.history result: %#v", result)
	}
}

func TestDispatchMethodCallAgentMethods(t *testing.T) {
	opts := ServerOptions{
		StartAgent: func(_ context.Context, req methods.AgentRequest) (map[string]any, error) {
			if req.Message != "hello" {
				t.Fatalf("unexpected agent message: %q", req.Message)
			}
			return map[string]any{"run_id": "run-1", "status": "accepted"}, nil
		},
		WaitAgent: func(_ context.Context, req methods.AgentWaitRequest) (map[string]any, error) {
			if req.RunID != "run-1" {
				t.Fatalf("unexpected run_id: %q", req.RunID)
			}
			return map[string]any{"run_id": req.RunID, "status": "ok"}, nil
		},
		AgentIdentity: func(_ context.Context, req methods.AgentIdentityRequest) (map[string]any, error) {
			return map[string]any{"agent_id": "main", "display_name": "Main Agent", "session_id": req.SessionID}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodAgent, map[string]any{"message": "hello", "session_id": "s1"})
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("agent failed status=%d err=%v", status, err)
	}
	out, _ := result.(map[string]any)
	if out["status"] != "accepted" || out["run_id"] != "run-1" || out["runId"] != "run-1" {
		t.Fatalf("unexpected agent result: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodAgentWait, map[string]any{"run_id": "run-1"})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("agent.wait failed status=%d err=%v", status, err)
	}
	out, _ = result.(map[string]any)
	if out["status"] != "ok" || out["run_id"] != "run-1" || out["runId"] != "run-1" {
		t.Fatalf("unexpected agent.wait result: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodAgentIdentityGet, map[string]any{"session_id": "s1"})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("agent.identity.get failed status=%d err=%v", status, err)
	}
	out, _ = result.(map[string]any)
	if out["agent_id"] != "main" || out["agentId"] != "main" || out["displayName"] != "Main Agent" || out["sessionId"] != "s1" {
		t.Fatalf("unexpected identity result: %#v", result)
	}
}

func TestDispatchMethodCallAgentsMethods(t *testing.T) {
	agents := map[string]state.AgentDoc{}
	files := map[string]string{}
	opts := ServerOptions{
		ListAgents: func(_ context.Context, _ methods.AgentsListRequest) (map[string]any, error) {
			out := make([]state.AgentDoc, 0, len(agents))
			for _, doc := range agents {
				out = append(out, doc)
			}
			return map[string]any{"agents": out}, nil
		},
		CreateAgent: func(_ context.Context, req methods.AgentsCreateRequest) (map[string]any, error) {
			doc := state.AgentDoc{Version: 1, AgentID: req.AgentID, Name: req.Name}
			agents[req.AgentID] = doc
			return map[string]any{"ok": true, "agent": doc}, nil
		},
		UpdateAgent: func(_ context.Context, req methods.AgentsUpdateRequest) (map[string]any, error) {
			doc, ok := agents[req.AgentID]
			if !ok {
				return nil, state.ErrNotFound
			}
			doc.Name = req.Name
			agents[req.AgentID] = doc
			return map[string]any{"ok": true, "agent": doc}, nil
		},
		DeleteAgent: func(_ context.Context, req methods.AgentsDeleteRequest) (map[string]any, error) {
			doc, ok := agents[req.AgentID]
			if !ok {
				return nil, state.ErrNotFound
			}
			doc.Deleted = true
			agents[req.AgentID] = doc
			return map[string]any{"ok": true, "agent_id": req.AgentID, "deleted": true}, nil
		},
		ListAgentFiles: func(_ context.Context, req methods.AgentsFilesListRequest) (map[string]any, error) {
			name, ok := files[req.AgentID]
			if !ok {
				return map[string]any{"agent_id": req.AgentID, "files": []map[string]any{}}, nil
			}
			return map[string]any{"agent_id": req.AgentID, "files": []map[string]any{{"name": name, "size": len(name)}}}, nil
		},
		GetAgentFile: func(_ context.Context, req methods.AgentsFilesGetRequest) (map[string]any, error) {
			content, ok := files[req.AgentID+":"+req.Name]
			if !ok {
				return map[string]any{"agent_id": req.AgentID, "file": map[string]any{"name": req.Name, "missing": true}}, nil
			}
			return map[string]any{"agent_id": req.AgentID, "file": map[string]any{"name": req.Name, "missing": false, "content": content}}, nil
		},
		SetAgentFile: func(_ context.Context, req methods.AgentsFilesSetRequest) (map[string]any, error) {
			files[req.AgentID+":"+req.Name] = req.Content
			files[req.AgentID] = req.Name
			return map[string]any{"ok": true, "agent_id": req.AgentID, "file": map[string]any{"name": req.Name, "missing": false, "content": req.Content}}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodAgentsCreate, map[string]any{"agent_id": "main", "name": "Main"})
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("agents.create failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodAgentsList, map[string]any{"limit": 10})
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("agents.list failed status=%d err=%v", status, err)
	}
	out, _ := result.(map[string]any)
	listed, _ := out["agents"].([]state.AgentDoc)
	if len(listed) != 1 {
		t.Fatalf("unexpected agents.list payload: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodAgentsFilesSet, map[string]any{"agent_id": "main", "name": "instructions.md", "content": "hello"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("agents.files.set failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodAgentsFilesGet, map[string]any{"agent_id": "main", "name": "instructions.md"})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("agents.files.get failed status=%d err=%v", status, err)
	}
	out, _ = result.(map[string]any)
	file, _ := out["file"].(map[string]any)
	if file["missing"] != false || file["content"] != "hello" {
		t.Fatalf("unexpected agents.files.get payload: %#v", result)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodAgentsDelete, map[string]any{"agent_id": "main"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("agents.delete failed status=%d err=%v", status, err)
	}
}

func TestDispatchMethodCallSessionMutations(t *testing.T) {
	session := state.SessionDoc{Version: 1, SessionID: "s1", PeerPubKey: "peer", LastInboundAt: 10, LastReplyAt: 11, Meta: map[string]any{"a": "b"}}
	abortCalls := 0
	opts := ServerOptions{
		GetSession: func(context.Context, string) (state.SessionDoc, error) { return session, nil },
		PutSession: func(_ context.Context, _ string, next state.SessionDoc) error {
			session = next
			return nil
		},
		AbortChat: func(context.Context, string) (int, error) {
			abortCalls++
			return 1, nil
		},
		ListTranscript: func(context.Context, string, int) ([]state.TranscriptEntryDoc, error) {
			return []state.TranscriptEntryDoc{{EntryID: "1"}, {EntryID: "2"}, {EntryID: "3"}}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodSessionsPatch, map[string]any{"key": "s1", "meta": map[string]any{"x": "y"}})
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK || session.Meta["x"] != "y" {
		t.Fatalf("sessions.patch failed status=%d err=%v session=%+v", status, err, session)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSessionsReset, map[string]any{"sessionKey": "s1"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK || session.LastInboundAt != 0 || len(session.Meta) != 0 {
		t.Fatalf("sessions.reset failed status=%d err=%v session=%+v", status, err, session)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSessionsDelete, map[string]any{"key": "s1"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK || session.Meta["deleted"] != true {
		t.Fatalf("sessions.delete failed status=%d err=%v session=%+v", status, err, session)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSessionsCompact, map[string]any{"key": "s1", "maxLines": 1})
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("sessions.compact failed status=%d err=%v", status, err)
	}
	out, _ := result.(map[string]any)
	if out["dropped"].(int) != 2 || out["key"] != "s1" || out["sessionId"] != "s1" || out["fromEntries"].(int) != 3 {
		t.Fatalf("unexpected compact result: %#v", out)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodChatAbort, map[string]any{"sessionKey": "s1"})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("chat.abort failed status=%d err=%v", status, err)
	}
	out, _ = result.(map[string]any)
	if out["aborted"] != true || out["aborted_count"].(int) != 1 || out["abortedCount"].(int) != 1 || out["key"] != "s1" || out["sessionId"] != "s1" {
		t.Fatalf("unexpected chat.abort result: %#v", out)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodChatAbort, map[string]any{"runId": "run-1"})
	result, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("chat.abort (runId only) failed status=%d err=%v", status, err)
	}
	out, _ = result.(map[string]any)
	if out["aborted"] != false || out["run_id"] != "run-1" || out["runId"] != "run-1" || out["abortedCount"].(int) != 0 {
		t.Fatalf("unexpected run-only chat.abort result: %#v", out)
	}
	if abortCalls != 1 {
		t.Fatalf("abort callback should not be called for runId-only request, got=%d", abortCalls)
	}
}

func TestDispatchMethodCallChatSendOpenClawShape(t *testing.T) {
	var gotTo string
	var gotText string
	opts := ServerOptions{
		SendDM: func(_ context.Context, to string, text string) error {
			gotTo = to
			gotText = text
			return nil
		},
	}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodChatSend, map[string]any{
		"sessionKey":     "npub1alice",
		"message":        "hello",
		"idempotencyKey": "run-1",
	})
	result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("chat.send failed status=%d err=%v", status, err)
	}
	if gotTo != "npub1alice" || gotText != "hello" {
		t.Fatalf("unexpected send args to=%q text=%q", gotTo, gotText)
	}
	out, _ := result.(map[string]any)
	if out["status"] != "sent" || out["run_id"] != "run-1" {
		t.Fatalf("unexpected chat.send result: %#v", out)
	}
}

func TestDispatchMethodCallModelsToolsSkillsMethods(t *testing.T) {
	opts := ServerOptions{
		ListModels: func(context.Context, methods.ModelsListRequest) (map[string]any, error) {
			return map[string]any{"models": []map[string]any{{"id": "echo"}}}, nil
		},
		ToolsCatalog: func(_ context.Context, req methods.ToolsCatalogRequest) (map[string]any, error) {
			return map[string]any{"agent_id": req.AgentID, "profiles": []map[string]any{{"id": "full"}}, "groups": []map[string]any{}}, nil
		},
		SkillsStatus: func(_ context.Context, req methods.SkillsStatusRequest) (map[string]any, error) {
			return map[string]any{"agent_id": req.AgentID, "skills": []map[string]any{}}, nil
		},
		SkillsBins: func(_ context.Context, req methods.SkillsBinsRequest) (map[string]any, error) {
			_ = req
			return map[string]any{"bins": []map[string]any{}}, nil
		},
		SkillsInstall: func(_ context.Context, req methods.SkillsInstallRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "name": req.Name, "install_id": req.InstallID}, nil
		},
		SkillsUpdate: func(_ context.Context, req methods.SkillsUpdateRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "skill_key": req.SkillKey}, nil
		},
		PluginsInstall: func(_ context.Context, req methods.PluginsInstallRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "pluginId": req.PluginID}, nil
		},
		PluginsUninstall: func(_ context.Context, req methods.PluginsUninstallRequest) (map[string]any, error) {
			if req.PluginID == "missing" {
				return nil, state.ErrNotFound
			}
			return map[string]any{"ok": true, "pluginId": req.PluginID}, nil
		},
		PluginsUpdate: func(_ context.Context, req methods.PluginsUpdateRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "dryRun": req.DryRun}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodModelsList, map[string]any{})
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("models.list failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodToolsCatalog, map[string]any{"agent_id": "main"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("tools.catalog failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSkillsStatus, map[string]any{"agent_id": "main"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("skills.status failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSkillsBins, map[string]any{})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("skills.bins failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSkillsInstall, map[string]any{"name": "nostr-core", "install_id": "builtin"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("skills.install failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodSkillsUpdate, map[string]any{"skill_key": "nostr-core", "enabled": true})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("skills.update failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodPluginsInstall, map[string]any{"plugin_id": "codegen", "install": map[string]any{"source": "path", "sourcePath": "./ext/codegen"}})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("plugins.install failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodPluginsUninstall, map[string]any{"plugin_id": "missing"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil || status != http.StatusNotFound {
		t.Fatalf("plugins.uninstall missing expected status=404 got status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodPluginsUpdate, map[string]any{"plugin_ids": []string{"codegen"}, "dry_run": true})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("plugins.update failed status=%d err=%v", status, err)
	}
}

func TestDispatchMethodCallNodeDevicePairingMethods(t *testing.T) {
	opts := ServerOptions{
		NodePairRequest: func(_ context.Context, req methods.NodePairRequest) (map[string]any, error) {
			return map[string]any{"status": "pending", "request": map[string]any{"request_id": "r1", "node_id": req.NodeID}}, nil
		},
		NodePairList: func(context.Context, methods.NodePairListRequest) (map[string]any, error) {
			return map[string]any{"pending": []map[string]any{}, "paired": []map[string]any{}}, nil
		},
		NodePairApprove: func(_ context.Context, req methods.NodePairApproveRequest) (map[string]any, error) {
			return map[string]any{"request_id": req.RequestID}, nil
		},
		DevicePairList: func(context.Context, methods.DevicePairListRequest) (map[string]any, error) {
			return map[string]any{"pending": []map[string]any{}, "paired": []map[string]any{}}, nil
		},
		DeviceTokenRotate: func(_ context.Context, req methods.DeviceTokenRotateRequest) (map[string]any, error) {
			return map[string]any{"device_id": req.DeviceID, "role": req.Role, "token": "tok"}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodNodePairRequest, map[string]any{"node_id": "n1"})
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("node.pair.request failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodNodePairList, map[string]any{})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("node.pair.list failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodDevicePairList, map[string]any{})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("device.pair.list failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodDeviceTokenRotate, map[string]any{"device_id": "d1", "role": "node"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("device.token.rotate failed status=%d err=%v", status, err)
	}
}

func TestDispatchMethodCallNodeInvokeAndCronMethods(t *testing.T) {
	opts := ServerOptions{
		NodeList: func(_ context.Context, req methods.NodeListRequest) (map[string]any, error) {
			return map[string]any{"nodes": []map[string]any{{"node_id": "node-a"}}, "count": 1, "limit": req.Limit}, nil
		},
		NodeDescribe: func(_ context.Context, req methods.NodeDescribeRequest) (map[string]any, error) {
			return map[string]any{"node": map[string]any{"node_id": req.NodeID}, "status": "paired"}, nil
		},
		NodeRename: func(_ context.Context, req methods.NodeRenameRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "node_id": req.NodeID, "name": req.Name}, nil
		},
		NodeCanvasCapabilityRefresh: func(_ context.Context, req methods.NodeCanvasCapabilityRefreshRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "node_id": req.NodeID, "caps": []string{"canvas"}}, nil
		},
		NodeInvoke: func(_ context.Context, req methods.NodeInvokeRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "run_id": "node-run-1", "node_id": req.NodeID, "command": req.Command}, nil
		},
		NodeEvent: func(_ context.Context, req methods.NodeEventRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "run_id": req.RunID, "status": req.Status}, nil
		},
		NodeResult: func(_ context.Context, req methods.NodeResultRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "run_id": req.RunID, "status": req.Status}, nil
		},
		CronAdd: func(_ context.Context, req methods.CronAddRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "job": map[string]any{"id": "c1", "method": req.Method}}, nil
		},
		CronList: func(context.Context, methods.CronListRequest) (map[string]any, error) {
			return map[string]any{"jobs": []map[string]any{{"id": "c1"}}, "count": 1}, nil
		},
		CronStatus: func(_ context.Context, req methods.CronStatusRequest) (map[string]any, error) {
			return map[string]any{"job": map[string]any{"id": req.ID}}, nil
		},
		CronRun: func(_ context.Context, req methods.CronRunRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "run": map[string]any{"job_id": req.ID}}, nil
		},
		CronRuns: func(context.Context, methods.CronRunsRequest) (map[string]any, error) {
			return map[string]any{"runs": []map[string]any{{"run_id": "r1"}}, "count": 1}, nil
		},
		CronUpdate: func(_ context.Context, req methods.CronUpdateRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "job": map[string]any{"id": req.ID}}, nil
		},
		CronRemove: func(_ context.Context, req methods.CronRemoveRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "id": req.ID, "removed": true}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodNodeList, map[string]any{"limit": 10})
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("node.list failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodNodeDescribe, map[string]any{"node_id": "node-a"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("node.describe failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodNodeRename, map[string]any{"node_id": "node-a", "name": "Kitchen"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("node.rename failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodNodeCanvasCapabilityRefresh, map[string]any{"node_id": "node-a"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("node.canvas.capability.refresh failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodNodeInvoke, map[string]any{"node_id": "node-a", "command": "ping"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("node.invoke failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodNodeInvokeResult, map[string]any{"run_id": "node-run-1", "status": "ok"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("node.invoke.result failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodCronAdd, map[string]any{"schedule": "* * * * *", "method": "status.get"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("cron.add failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodCronList, map[string]any{"limit": 10})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("cron.list failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodCronStatus, map[string]any{"id": "c1"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("cron.status failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodCronUpdate, map[string]any{"id": "c1", "enabled": false})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("cron.update failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodCronRun, map[string]any{"id": "c1"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("cron.run failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodCronRuns, map[string]any{"limit": 10})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("cron.runs failed status=%d err=%v", status, err)
	}

	rr = httptest.NewRecorder()
	req = newMethodRequest(t, methods.MethodCronRemove, map[string]any{"id": "c1"})
	_, status, err = dispatchMethodCall(context.Background(), rr, req, opts)
	if err != nil || status != http.StatusOK {
		t.Fatalf("cron.remove failed status=%d err=%v", status, err)
	}
}

func TestDispatchMethodCallOperationalBundles(t *testing.T) {
	opts := ServerOptions{
		ExecApprovalsGet: func(context.Context, methods.ExecApprovalsGetRequest) (map[string]any, error) {
			return map[string]any{"approvals": map[string]any{"allow": true}}, nil
		},
		ExecApprovalRequest: func(context.Context, methods.ExecApprovalRequestRequest) (map[string]any, error) {
			return map[string]any{"id": "approval-1", "status": "accepted"}, nil
		},
		ExecApprovalWaitDecision: func(_ context.Context, req methods.ExecApprovalWaitDecisionRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "id": req.ID, "resolved": true, "decision": "approve"}, nil
		},
		SecretsResolve: func(context.Context, methods.SecretsResolveRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "assignments": []map[string]any{}}, nil
		},
		WizardStart: func(context.Context, methods.WizardStartRequest) (map[string]any, error) {
			return map[string]any{"session_id": "wizard-1", "status": "running"}, nil
		},
		WizardNext: func(_ context.Context, req methods.WizardNextRequest) (map[string]any, error) {
			return map[string]any{"id": req.ID, "status": "running"}, nil
		},
		WizardCancel: func(_ context.Context, req methods.WizardCancelRequest) (map[string]any, error) {
			return map[string]any{"id": req.ID, "status": "cancelled"}, nil
		},
		WizardStatus: func(_ context.Context, req methods.WizardStatusRequest) (map[string]any, error) {
			return map[string]any{"id": req.ID, "status": "running"}, nil
		},
		UpdateRun: func(context.Context, methods.UpdateRunRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "channel": "stable"}, nil
		},
		TalkConfig: func(context.Context, methods.TalkConfigRequest) (map[string]any, error) {
			return map[string]any{"config": map[string]any{}}, nil
		},
		TalkMode: func(_ context.Context, req methods.TalkModeRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "mode": req.Mode}, nil
		},
		LastHeartbeat: func(context.Context, methods.LastHeartbeatRequest) (map[string]any, error) {
			return map[string]any{"at": time.Now().Unix(), "state": "idle"}, nil
		},
		Wake: func(_ context.Context, req methods.WakeRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "source": req.Source, "mode": req.Mode}, nil
		},
		SystemPresence: func(context.Context, methods.SystemPresenceRequest) ([]map[string]any, error) {
			return []map[string]any{{"key": "default"}}, nil
		},
		SystemEvent: func(_ context.Context, req methods.SystemEventRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "text": req.Text}, nil
		},
		Send: func(_ context.Context, req methods.SendRequest) (map[string]any, error) {
			return map[string]any{"runId": req.IdempotencyKey, "channel": "nostr"}, nil
		},
		SendPoll: func(_ context.Context, req methods.PollRequest) (map[string]any, error) {
			return map[string]any{"runId": req.IdempotencyKey, "channel": "nostr", "messageId": "poll-msg-1"}, nil
		},
		BrowserRequest: func(_ context.Context, req methods.BrowserRequestRequest) (map[string]any, error) {
			return map[string]any{"ok": false, "method": req.Method, "path": req.Path}, nil
		},
		VoicewakeGet: func(context.Context, methods.VoicewakeGetRequest) (map[string]any, error) {
			return map[string]any{"triggers": []string{"openclaw"}}, nil
		},
		VoicewakeSet: func(_ context.Context, req methods.VoicewakeSetRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "triggers": req.Triggers}, nil
		},
		TTSStatus: func(context.Context, methods.TTSStatusRequest) (map[string]any, error) {
			return map[string]any{"enabled": true, "provider": "openai"}, nil
		},
		TTSProviders: func(context.Context, methods.TTSProvidersRequest) (map[string]any, error) {
			return map[string]any{"providers": []string{"openai", "kokoro"}}, nil
		},
		TTSEnable: func(context.Context, methods.TTSEnableRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "enabled": true}, nil
		},
		TTSDisable: func(context.Context, methods.TTSDisableRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "enabled": false}, nil
		},
		TTSSetProvider: func(_ context.Context, req methods.TTSSetProviderRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "provider": req.Provider}, nil
		},
		TTSConvert: func(context.Context, methods.TTSConvertRequest) (map[string]any, error) {
			return map[string]any{"provider": "openai", "audioPath": ""}, nil
		},
	}

	cases := []struct {
		method string
		params map[string]any
	}{
		{method: methods.MethodExecApprovalsGet, params: map[string]any{}},
		{method: methods.MethodExecApprovalRequest, params: map[string]any{"command": "ls"}},
		{method: methods.MethodExecApprovalWaitDecision, params: map[string]any{"id": "approval-1"}},
		{method: methods.MethodSecretsResolve, params: map[string]any{"commandName": "memory status", "targetIds": []string{"talk.apiKey"}}},
		{method: methods.MethodWizardStart, params: map[string]any{"mode": "local"}},
		{method: methods.MethodWizardNext, params: map[string]any{"id": "wizard-1", "input": map[string]any{"step": "confirm"}}},
		{method: methods.MethodWizardCancel, params: map[string]any{"id": "wizard-1"}},
		{method: methods.MethodWizardStatus, params: map[string]any{"id": "wizard-1"}},
		{method: methods.MethodUpdateRun, params: map[string]any{"force": true}},
		{method: methods.MethodTalkConfig, params: map[string]any{}},
		{method: methods.MethodTalkMode, params: map[string]any{"mode": "voice"}},
		{method: methods.MethodLastHeartbeat, params: map[string]any{}},
		{method: methods.MethodWake, params: map[string]any{"source": "manual", "mode": "now", "text": "wake now"}},
		{method: methods.MethodSystemPresence, params: map[string]any{}},
		{method: methods.MethodSystemEvent, params: map[string]any{"text": "Node: up"}},
		{method: methods.MethodSend, params: map[string]any{"to": "0000000000000000000000000000000000000000000000000000000000000001", "message": "hello", "idempotencyKey": "idem-1"}},
		{method: methods.MethodPoll, params: map[string]any{"to": "0000000000000000000000000000000000000000000000000000000000000001", "question": "Favorite?", "options": []string{"A", "B"}, "idempotencyKey": "poll-1"}},
		{method: methods.MethodBrowserRequest, params: map[string]any{"method": "GET", "path": "/status"}},
		{method: methods.MethodVoicewakeGet, params: map[string]any{}},
		{method: methods.MethodVoicewakeSet, params: map[string]any{"triggers": []string{"openclaw"}}},
		{method: methods.MethodTTSStatus, params: map[string]any{}},
		{method: methods.MethodTTSProviders, params: map[string]any{}},
		{method: methods.MethodTTSEnable, params: map[string]any{}},
		{method: methods.MethodTTSDisable, params: map[string]any{}},
		{method: methods.MethodTTSSetProvider, params: map[string]any{"provider": "openai"}},
		{method: methods.MethodTTSConvert, params: map[string]any{"text": "hello"}},
	}

	for _, tc := range cases {
		rr := httptest.NewRecorder()
		req := newMethodRequest(t, tc.method, tc.params)
		_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
		if err != nil || status != http.StatusOK {
			t.Fatalf("%s failed status=%d err=%v", tc.method, status, err)
		}
	}
}

func TestDispatchMethodCallSetHeartbeatsRequiresEnabled(t *testing.T) {
	opts := ServerOptions{
		SetHeartbeats: func(context.Context, methods.SetHeartbeatsRequest) (map[string]any, error) {
			return map[string]any{"ok": true}, nil
		},
	}
	rr := httptest.NewRecorder()
	req := newMethodRequest(t, methods.MethodSetHeartbeats, map[string]any{"interval_ms": 30000})
	_, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d", status, http.StatusBadRequest)
	}
}

func TestDispatchMethodCall_OpenClawHighRiskParityFixtures(t *testing.T) {
	type fixtureCase struct {
		Name                string         `json:"name"`
		Method              string         `json:"method"`
		Params              map[string]any `json:"params"`
		ExpectedStatus      int            `json:"expected_status"`
		ExpectErrorContains string         `json:"expect_error_contains"`
		ResultKind          string         `json:"result_kind"`
	}
	type fixtureFile struct {
		Cases []fixtureCase `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "parity", "high_risk_methods.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	opts := ServerOptions{
		SetHeartbeats: func(_ context.Context, req methods.SetHeartbeatsRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "enabled": req.Enabled != nil && *req.Enabled}, nil
		},
		SystemPresence: func(context.Context, methods.SystemPresenceRequest) ([]map[string]any, error) {
			return []map[string]any{{"key": "default"}}, nil
		},
		SystemEvent: func(_ context.Context, req methods.SystemEventRequest) (map[string]any, error) {
			return map[string]any{"ok": true, "text": req.Text}, nil
		},
		BrowserRequest: func(context.Context, methods.BrowserRequestRequest) (map[string]any, error) {
			return nil, fmt.Errorf("browser control is disabled")
		},
	}
	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := newMethodRequest(t, tc.Method, tc.Params)
			result, status, err := dispatchMethodCall(context.Background(), rr, req, opts)
			if status != tc.ExpectedStatus {
				t.Fatalf("status=%d want=%d err=%v", status, tc.ExpectedStatus, err)
			}
			if tc.ExpectErrorContains != "" {
				if err == nil || !strings.Contains(err.Error(), tc.ExpectErrorContains) {
					t.Fatalf("error=%v, want contains %q", err, tc.ExpectErrorContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			switch tc.ResultKind {
			case "array":
				if _, ok := result.([]map[string]any); !ok {
					t.Fatalf("result kind mismatch: %#v", result)
				}
			}
		})
	}
}

func TestDispatchMethodCallDelegatesPreviouslyMissingMethods(t *testing.T) {
	cases := []struct {
		method string
		params map[string]any
	}{
		{methods.MethodChannelsJoin, map[string]any{"group_address": "naddr1example"}},
		{methods.MethodChannelsLeave, map[string]any{"channel_id": "chan-1"}},
		{methods.MethodChannelsList, map[string]any{}},
		{methods.MethodChannelsSend, map[string]any{"channel_id": "chan-1", "text": "hello"}},
		{methods.MethodMemoryCompact, map[string]any{"session_id": "sess-1"}},
		{methods.MethodSessionsSpawn, map[string]any{"message": "hello"}},
		{methods.MethodSessionsExport, map[string]any{"session_id": "sess-1", "format": "html"}},
		{methods.MethodTasksCreate, map[string]any{"task": map[string]any{"title": "T", "instructions": "Do it"}}},
		{methods.MethodTasksGet, map[string]any{"task_id": "task-1"}},
		{methods.MethodTasksList, map[string]any{"limit": 5}},
		{methods.MethodTasksCancel, map[string]any{"task_id": "task-1"}},
		{methods.MethodTasksResume, map[string]any{"task_id": "task-1"}},
		{methods.MethodTasksDoctor, map[string]any{"task_id": "task-1"}},
		{methods.MethodTasksSummary, map[string]any{}},
		{methods.MethodTasksAuditExport, map[string]any{"goal_id": "goal-1"}},
		{methods.MethodTasksTrace, map[string]any{"task_id": "task-1"}},
		{methods.MethodConfigSchemaLookup, map[string]any{"path": "dm.policy"}},
		{methods.MethodSecurityAudit, map[string]any{}},
		{methods.MethodACPRegister, map[string]any{"pubkey": strings.Repeat("a", 64)}},
		{methods.MethodACPUnregister, map[string]any{"pubkey": strings.Repeat("a", 64)}},
		{methods.MethodACPPeers, map[string]any{}},
		{methods.MethodACPDispatch, map[string]any{"target_pubkey": strings.Repeat("b", 64), "instructions": "Do it"}},
		{methods.MethodACPPipeline, map[string]any{"steps": []map[string]any{{"peer_pubkey": strings.Repeat("b", 64), "instructions": "Step 1"}}}},
		{methods.MethodAgentsAssign, map[string]any{"session_id": "sess-1", "agent_id": "main"}},
		{methods.MethodAgentsUnassign, map[string]any{"session_id": "sess-1"}},
		{methods.MethodAgentsActive, map[string]any{"limit": 5}},
		{methods.MethodCanvasGet, map[string]any{"id": "canvas-1"}},
		{methods.MethodCanvasList, map[string]any{}},
		{methods.MethodCanvasUpdate, map[string]any{"id": "canvas-1", "content_type": "text/plain", "data": "hello"}},
		{methods.MethodCanvasDelete, map[string]any{"id": "canvas-1"}},
		{methods.MethodHooksList, map[string]any{}},
		{methods.MethodHooksEnable, map[string]any{"hookKey": "wake"}},
		{methods.MethodHooksDisable, map[string]any{"hookKey": "wake"}},
		{methods.MethodHooksInfo, map[string]any{"hookKey": "wake"}},
		{methods.MethodHooksCheck, map[string]any{}},
	}

	delegated := map[string]int{}
	opts := ServerOptions{
		GetConfig: func(context.Context) (state.ConfigDoc, error) {
			return state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: true, LegacyTokenFallback: true}}, nil
		},
		DelegateControlCall: func(ctx context.Context, method string, params json.RawMessage) (any, int, error) {
			if got := CallerPubKeyFromContext(ctx); got != "token-local" {
				t.Fatalf("caller pubkey = %q, want token-local", got)
			}
			delegated[method]++
			var decoded map[string]any
			if len(params) > 0 {
				if err := json.Unmarshal(params, &decoded); err != nil {
					t.Fatalf("unmarshal delegated params for %s: %v", method, err)
				}
			}
			return map[string]any{"method": method, "params": decoded}, http.StatusOK, nil
		},
	}
	ctx := context.WithValue(context.Background(), tokenAuthContextKey, true)

	for _, tc := range cases {
		rr := httptest.NewRecorder()
		result, status, err := dispatchMethodCall(ctx, rr, newMethodRequest(t, tc.method, tc.params), opts)
		if err != nil {
			t.Fatalf("%s err=%v", tc.method, err)
		}
		if status != http.StatusOK {
			t.Fatalf("%s status=%d want=%d", tc.method, status, http.StatusOK)
		}
		out, ok := result.(map[string]any)
		if !ok || out["method"] != tc.method {
			t.Fatalf("%s unexpected result: %#v", tc.method, result)
		}
	}

	for _, tc := range cases {
		if delegated[tc.method] != 1 {
			t.Fatalf("delegate count for %s = %d, want 1", tc.method, delegated[tc.method])
		}
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

func TestEnvelopeParityFixtures(t *testing.T) {
	type fixtureCase struct {
		Name              string         `json:"name"`
		NIP86             bool           `json:"nip86"`
		Status            int            `json:"status"`
		OK                bool           `json:"ok"`
		Result            map[string]any `json:"result"`
		Error             string         `json:"error"`
		ExpectErrorCode   int            `json:"expect_error_code"`
		ExpectContentType string         `json:"expect_content_type"`
	}
	type fixtureFile struct {
		Cases []fixtureCase `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "parity", "envelope_cases.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			if tc.NIP86 {
				if tc.Error != "" {
					writeNIP86JSON(rr, map[string]any{"error": methods.MapNIP86Error(tc.Status, errors.New(tc.Error))})
				} else {
					writeNIP86JSON(rr, map[string]any{"result": tc.Result})
				}
			} else {
				if tc.Error != "" {
					writeJSON(rr, tc.Status, methods.CallResponse{OK: false, Error: tc.Error})
				} else {
					writeJSON(rr, tc.Status, methods.CallResponse{OK: tc.OK, Result: tc.Result})
				}
			}
			if got := rr.Header().Get("Content-Type"); got != tc.ExpectContentType {
				t.Fatalf("content-type=%q want=%q", got, tc.ExpectContentType)
			}
			var body map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if tc.NIP86 {
				if tc.Error != "" {
					errObj, ok := body["error"].(map[string]any)
					if !ok {
						t.Fatalf("missing error object: %#v", body)
					}
					if int(errObj["code"].(float64)) != tc.ExpectErrorCode {
						t.Fatalf("error code=%v want=%d", errObj["code"], tc.ExpectErrorCode)
					}
				} else if _, ok := body["result"]; !ok {
					t.Fatalf("missing result object: %#v", body)
				}
			} else {
				if tc.Error != "" {
					if body["ok"] != false || body["error"] != tc.Error {
						t.Fatalf("unexpected call error envelope: %#v", body)
					}
				} else {
					if body["ok"] != tc.OK {
						t.Fatalf("unexpected call success envelope: %#v", body)
					}
				}
			}
		})
	}
}

func TestCallRouteEnvelopeParityFixtures(t *testing.T) {
	type fixtureCase struct {
		Name                  string         `json:"name"`
		NIP86                 bool           `json:"nip86"`
		Body                  map[string]any `json:"body"`
		Scenario              string         `json:"scenario"`
		ExpectedHTTPStatus    int            `json:"expected_http_status"`
		ExpectedContentType   string         `json:"expected_content_type"`
		ExpectedOK            *bool          `json:"expected_ok,omitempty"`
		ExpectedErrorContains string         `json:"expected_error_contains"`
		ExpectedNIP86Code     int            `json:"expected_nip86_code"`
		ExpectedResultKey     string         `json:"expected_result_key"`
		ExpectedErrorDataKey  string         `json:"expected_error_data_key"`
	}
	type fixtureFile struct {
		Cases []fixtureCase `json:"cases"`
	}

	raw, err := os.ReadFile(filepath.Join("testdata", "parity", "call_route_envelope_cases.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			rawBody, err := json.Marshal(tc.Body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(rawBody))
			if tc.NIP86 {
				req.Header.Set("Content-Type", "application/nostr+json+rpc")
			}
			result, status, callErr := dispatchMethodCall(context.Background(), rr, req, callRouteFixtureOptions(tc.Scenario))
			if isNIP86RPC(req) {
				if callErr != nil {
					writeNIP86JSON(rr, map[string]any{"error": methods.MapNIP86Error(status, callErr)})
				} else {
					writeNIP86JSON(rr, map[string]any{"result": result})
				}
			} else {
				if callErr != nil {
					writeJSON(rr, status, methods.CallResponse{OK: false, Error: callErr.Error()})
				} else {
					writeJSON(rr, status, methods.CallResponse{OK: true, Result: result})
				}
			}

			if rr.Code != tc.ExpectedHTTPStatus {
				t.Fatalf("status=%d want=%d", rr.Code, tc.ExpectedHTTPStatus)
			}
			if got := rr.Header().Get("Content-Type"); got != tc.ExpectedContentType {
				t.Fatalf("content-type=%q want=%q", got, tc.ExpectedContentType)
			}
			var body map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if tc.ExpectedOK != nil {
				if body["ok"] != *tc.ExpectedOK {
					t.Fatalf("ok=%v want=%v body=%#v", body["ok"], *tc.ExpectedOK, body)
				}
			}
			if tc.ExpectedErrorContains != "" {
				if tc.NIP86 {
					errObj, _ := body["error"].(map[string]any)
					msg, _ := errObj["message"].(string)
					if !strings.Contains(msg, tc.ExpectedErrorContains) {
						t.Fatalf("error message=%q want contains %q", msg, tc.ExpectedErrorContains)
					}
				} else {
					msg, _ := body["error"].(string)
					if !strings.Contains(msg, tc.ExpectedErrorContains) {
						t.Fatalf("error=%q want contains %q", msg, tc.ExpectedErrorContains)
					}
				}
			}
			if tc.ExpectedNIP86Code != 0 {
				errObj, _ := body["error"].(map[string]any)
				if int(errObj["code"].(float64)) != tc.ExpectedNIP86Code {
					t.Fatalf("nip86 code=%v want=%d", errObj["code"], tc.ExpectedNIP86Code)
				}
			}
			if tc.ExpectedResultKey != "" {
				resultObj, _ := body["result"].(map[string]any)
				if _, ok := resultObj[tc.ExpectedResultKey]; !ok {
					t.Fatalf("result missing key %q: %#v", tc.ExpectedResultKey, resultObj)
				}
			}
			if tc.ExpectedErrorDataKey != "" {
				errObj, _ := body["error"].(map[string]any)
				data, _ := errObj["data"].(map[string]any)
				if _, ok := data[tc.ExpectedErrorDataKey]; !ok {
					t.Fatalf("error.data missing key %q: %#v", tc.ExpectedErrorDataKey, data)
				}
			}
		})
	}
}

func callRouteFixtureOptions(scenario string) ServerOptions {
	switch scenario {
	case "list_put_conflict":
		return ServerOptions{
			GetListWithEvent: func(_ context.Context, _ string) (state.ListDoc, state.Event, error) {
				return state.ListDoc{Version: 2, Name: "allowlist"}, state.Event{ID: "evt-2"}, nil
			},
			PutList: func(_ context.Context, _ string, _ state.ListDoc) error { return nil },
		}
	default:
		return ServerOptions{}
	}
}

func TestIsNIP86RPC(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/call", bytes.NewReader(nil))
	req.Header.Set("Content-Type", "application/nostr+json+rpc")
	if !isNIP86RPC(req) {
		t.Fatal("expected NIP86 profile detection from content type")
	}
}

func TestProviderNotConfiguredEnvelopeParityFixtures(t *testing.T) {
	type fixtureCase struct {
		Name              string         `json:"name"`
		Method            string         `json:"method"`
		Params            map[string]any `json:"params"`
		ExpectedStatus    int            `json:"expected_status"`
		ErrorContains     string         `json:"error_contains"`
		ExpectedNIP86Code int            `json:"expected_nip86_code"`
	}
	type fixtureFile struct {
		Cases []fixtureCase `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "parity", "provider_not_configured_cases.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := newMethodRequest(t, tc.Method, tc.Params)
			_, status, callErr := dispatchMethodCall(context.Background(), rr, req, ServerOptions{})
			if callErr == nil {
				t.Fatalf("expected not-configured error")
			}
			if status != tc.ExpectedStatus {
				t.Fatalf("status=%d want=%d", status, tc.ExpectedStatus)
			}
			if !strings.Contains(callErr.Error(), tc.ErrorContains) {
				t.Fatalf("error=%v want contains %q", callErr, tc.ErrorContains)
			}
			nip86 := methods.MapNIP86Error(status, callErr)
			if nip86.Code != tc.ExpectedNIP86Code {
				t.Fatalf("nip86 code=%d want=%d", nip86.Code, tc.ExpectedNIP86Code)
			}
		})
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
