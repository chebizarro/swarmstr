package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"swarmstr/internal/gateway/methods"
	"swarmstr/internal/nostr/events"
	nostruntime "swarmstr/internal/nostr/runtime"
	"swarmstr/internal/store/state"
)

func TestHandleControlRPCRequest_SupportedMethods(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodSupportedMethods,
		Params:     json.RawMessage(`[]`),
	}, nil, nil, nil, nil, nil, cfgState, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("handleControlRPCRequest error: %v", err)
	}
	list, ok := res.Result.([]string)
	if !ok || len(list) == 0 {
		t.Fatalf("unexpected result: %#v", res.Result)
	}
}

func TestHandleControlRPCRequest_AuthzDenied(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{
		RequireAuth: true,
		Admins:      []state.ControlAdmin{{PubKey: "admin-a", Methods: []string{"status.get"}}},
	}})
	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller-b",
		Method:     methods.MethodConfigPut,
		Params:     json.RawMessage(`[{"dm":{"policy":"open"}}]`),
	}, nil, nil, nil, nil, nil, cfgState, time.Now())
	if err == nil {
		t.Fatal("expected authorization error")
	}
}

func TestHandleControlRPCRequest_ListGetAndPut(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})

	putRaw := json.RawMessage(`["allowlist",["npub1","npub2","npub1"]]`)
	putRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListPut,
		Params:     putRaw,
	}, nil, nil, docs, nil, nil, cfgState, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("list.put error: %v", err)
	}
	if putRes.Error != "" {
		t.Fatalf("unexpected list.put error result: %s", putRes.Error)
	}

	getRaw := json.RawMessage(`["allowlist"]`)
	getRes, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListGet,
		Params:     getRaw,
	}, nil, nil, docs, nil, nil, cfgState, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("list.get error: %v", err)
	}
	list, ok := getRes.Result.(state.ListDoc)
	if !ok {
		t.Fatalf("unexpected list.get type: %T", getRes.Result)
	}
	if list.Name != "allowlist" || len(list.Items) != 2 {
		t.Fatalf("unexpected list.get payload: %+v", list)
	}
}

func TestHandleControlRPCRequest_ListPutPreconditionConflict(t *testing.T) {
	store := newTestStore()
	docs := state.NewDocsRepository(store, "author")
	cfgState := newRuntimeConfigStore(state.ConfigDoc{Control: state.ControlPolicy{RequireAuth: false}})
	if _, err := docs.PutList(context.Background(), "allowlist", state.ListDoc{Version: 2, Name: "allowlist", Items: []string{"a"}}); err != nil {
		t.Fatalf("seed list: %v", err)
	}
	_, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodListPut,
		Params:     json.RawMessage(`{"name":"allowlist","items":["b"],"expected_version":1}`),
	}, nil, nil, docs, nil, nil, cfgState, time.Now().Add(-time.Minute))
	if err == nil {
		t.Fatal("expected precondition conflict")
	}
	if !strings.Contains(err.Error(), "current_version=2") {
		t.Fatalf("expected current version metadata in error, got: %v", err)
	}
}

func TestHandleControlRPCRequest_RelayPolicyGet(t *testing.T) {
	cfgState := newRuntimeConfigStore(state.ConfigDoc{
		Control: state.ControlPolicy{RequireAuth: false},
		Relays:  state.RelayPolicy{Read: []string{"wss://read"}, Write: []string{"wss://write"}},
	})
	res, err := handleControlRPCRequest(context.Background(), nostruntime.ControlRPCInbound{
		FromPubKey: "caller",
		Method:     methods.MethodRelayPolicyGet,
		Params:     json.RawMessage(`[]`),
	}, nil, nil, nil, nil, nil, cfgState, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("relay.policy.get error: %v", err)
	}
	view, ok := res.Result.(methods.RelayPolicyResponse)
	if !ok {
		t.Fatalf("unexpected result type: %T", res.Result)
	}
	if len(view.ReadRelays) != 1 || view.ReadRelays[0] != "wss://read" {
		t.Fatalf("unexpected read relays: %+v", view.ReadRelays)
	}
}

type testStore struct {
	replaceable map[string]state.Event
}

func newTestStore() *testStore {
	return &testStore{replaceable: map[string]state.Event{}}
}

func (s *testStore) GetLatestReplaceable(_ context.Context, addr state.Address) (state.Event, error) {
	evt, ok := s.replaceable[s.key(addr)]
	if !ok {
		return state.Event{}, state.ErrNotFound
	}
	return evt, nil
}

func (s *testStore) PutReplaceable(_ context.Context, addr state.Address, content string, extraTags [][]string) (state.Event, error) {
	evt := state.Event{
		ID:        fmt.Sprintf("evt:%s", s.key(addr)),
		PubKey:    addr.PubKey,
		Kind:      addr.Kind,
		CreatedAt: time.Now().Unix(),
		Tags:      append(extraTags, []string{"d", addr.DTag}),
		Content:   content,
	}
	s.replaceable[s.key(addr)] = evt
	return evt, nil
}

func (s *testStore) PutAppend(_ context.Context, _ state.Address, _ string, _ [][]string) (state.Event, error) {
	return state.Event{}, nil
}

func (s *testStore) ListByTag(_ context.Context, _ events.Kind, _, _ string, _ int) ([]state.Event, error) {
	return nil, nil
}

func (s *testStore) ListByTagForAuthor(_ context.Context, _ events.Kind, _, _, _ string, _ int) ([]state.Event, error) {
	return nil, nil
}

func (s *testStore) key(addr state.Address) string {
	return fmt.Sprintf("%d|%s|%s", addr.Kind, addr.PubKey, addr.DTag)
}
