package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	secretspkg "metiq/internal/secrets"
	"metiq/internal/store/state"
)

type gatewayProtectedBackend struct {
	mu    sync.Mutex
	items map[string]string
}

func (b *gatewayProtectedBackend) Name() string          { return "test-protected" }
func (b *gatewayProtectedBackend) ProtectedAtRest() bool { return true }
func (b *gatewayProtectedBackend) Get(key string) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	value, ok := b.items[key]
	return value, ok, nil
}
func (b *gatewayProtectedBackend) Set(key, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.items == nil {
		b.items = map[string]string{}
	}
	b.items[key] = value
	return nil
}
func (b *gatewayProtectedBackend) Delete(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.items, key)
	return nil
}

func secretsStoreCall(t *testing.T, h controlRPCHandler, method, params string, internal bool) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handleOpsRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method, Params: json.RawMessage(params), FromPubKey: "operator-pubkey", Internal: internal,
	}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("%s was not handled", method)
	}
	return result, err
}

func TestSecretsStoreRPCProtectedCRUDAndNoSecretProjection(t *testing.T) {
	store := secretspkg.NewStore(nil)
	store.SetBackend(&gatewayProtectedBackend{items: map[string]string{}})
	h := newControlRPCHandler(controlRPCDeps{services: &daemonServices{handlers: handlerServices{secretsStore: store}}})

	secretValue := "unique-super-secret-value"
	if _, err := secretsStoreCall(t, h, methods.MethodSecretsStoreSet,
		`{"name":"API_TOKEN","value":"`+secretValue+`","kind":"secret","allowedHosts":["api.example.com"]}`, true); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	if _, err := secretsStoreCall(t, h, methods.MethodSecretsStoreSet,
		`{"name":"PUBLIC_MODE","value":"enabled","kind":"env"}`, true); err != nil {
		t.Fatalf("set env: %v", err)
	}
	result, err := secretsStoreCall(t, h, methods.MethodSecretsStoreList, `{}`, true)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	raw, err := json.Marshal(result.Result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secretValue) {
		t.Fatalf("secret-kind value leaked in response: %s", raw)
	}
	if !strings.Contains(string(raw), `"value":"enabled"`) {
		t.Fatalf("env-kind value missing from upstream-compatible projection: %s", raw)
	}
	if _, err := secretsStoreCall(t, h, methods.MethodSecretsStoreDelete, `{"name":"API_TOKEN"}`, true); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestSecretsStoreRPCRejectsNostrTransport(t *testing.T) {
	store := secretspkg.NewStore(nil)
	store.SetBackend(&gatewayProtectedBackend{items: map[string]string{}})
	h := newControlRPCHandler(controlRPCDeps{services: &daemonServices{handlers: handlerServices{secretsStore: store}}})
	if _, err := secretsStoreCall(t, h, methods.MethodSecretsStoreSet,
		`{"name":"API_TOKEN","value":"must-not-persist","kind":"secret"}`, false); err == nil || !strings.Contains(err.Error(), "WebSocket") {
		t.Fatalf("expected WebSocket-only error, got %v", err)
	}
	records, err := store.ListStoredSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("rejected Nostr request mutated store: %+v", records)
	}
}
