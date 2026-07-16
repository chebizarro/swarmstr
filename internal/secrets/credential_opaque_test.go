package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type protectedMemoryBackend struct {
	mu    sync.Mutex
	items map[string]string
	fail  bool
}

func (b *protectedMemoryBackend) Name() string          { return "protected-memory" }
func (b *protectedMemoryBackend) ProtectedAtRest() bool { return true }
func (b *protectedMemoryBackend) Get(key string) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return "", false, os.ErrPermission
	}
	v, ok := b.items[key]
	return v, ok, nil
}
func (b *protectedMemoryBackend) Set(key, value string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return os.ErrPermission
	}
	if b.items == nil {
		b.items = map[string]string{}
	}
	b.items[key] = value
	return nil
}
func (b *protectedMemoryBackend) Delete(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return os.ErrPermission
	}
	delete(b.items, key)
	return nil
}

func TestStoreResolveBytesFileRotationAndEncoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "macaroon")
	if err := os.WriteFile(path, []byte{0x01, 0xab}, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(nil)
	got, err := store.ResolveBytes(context.Background(), CredentialSource{Ref: "file:" + path, Encoding: CredentialEncodingHex})
	if err != nil || string(got) != "01ab" {
		t.Fatalf("first resolve = %q, %v", got, err)
	}
	if err := os.WriteFile(path, []byte{0xff, 0x00}, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = store.ResolveBytes(context.Background(), CredentialSource{Ref: "file:" + path, Encoding: CredentialEncodingHex})
	if err != nil || string(got) != "ff00" {
		t.Fatalf("rotated resolve = %q, %v", got, err)
	}
}

func TestStoreResolveBytesSecretAndRejectsPlaintext(t *testing.T) {
	t.Setenv("MACAROON_TEXT", "ascii-value")
	store := NewStore(nil)
	got, err := store.ResolveBytes(context.Background(), CredentialSource{Ref: "secret:MACAROON_TEXT", Encoding: CredentialEncodingText})
	if err != nil || string(got) != "ascii-value" {
		t.Fatalf("resolved = %q, %v", got, err)
	}
	if got, found := store.Resolve("secret:MACAROON_TEXT"); !found || got != "secret:MACAROON_TEXT" {
		t.Fatalf("legacy Resolve contract changed: %q, %v", got, found)
	}
	if _, err := store.ResolveBytes(context.Background(), CredentialSource{Ref: "plaintext", Encoding: CredentialEncodingText}); err == nil {
		t.Fatal("expected plaintext rejection")
	}
	t.Setenv("NON_ASCII", "snowman-☃")
	if _, err := store.ResolveBytes(context.Background(), CredentialSource{Ref: "env:NON_ASCII", Encoding: CredentialEncodingText}); err == nil {
		t.Fatal("expected non-ASCII rejection")
	}
}

func TestOpenProtectedJSONNamespaceRequiresProtectedPrimary(t *testing.T) {
	store := NewStore(nil)
	store.SetMCPAuthPath(filepath.Join(t.TempDir(), "fallback.json"))
	store.SetBackend(&memoryBackend{items: map[string]string{}})
	if _, err := store.OpenProtectedJSONNamespace("l402-token-cache"); !errors.Is(err, ErrProtectedBackendUnavailable) {
		t.Fatalf("expected protected backend error, got %v", err)
	}
	if _, err := os.Stat(store.mcpAuthPath); !os.IsNotExist(err) {
		t.Fatalf("protected namespace must not create fallback: %v", err)
	}
}

func TestOpaqueJSONNamespaceRoundTripAndQuarantine(t *testing.T) {
	backend := &protectedMemoryBackend{items: map[string]string{}}
	store := NewStore(nil)
	store.SetBackend(backend)
	ns, err := store.OpenProtectedJSONNamespace("l402-token-cache")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"schema_version": float64(1), "records": []any{}}
	if err := ns.Put(context.Background(), "snapshot", want); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	found, err := ns.Get(context.Background(), "snapshot", &got)
	if err != nil || !found || got["schema_version"] != float64(1) {
		t.Fatalf("get = %#v, %v, %v", got, found, err)
	}
	backend.mu.Lock()
	backend.items["opaque/v1/l402-token-cache/snapshot"] = "{bad json"
	backend.mu.Unlock()
	if _, err := ns.Get(context.Background(), "snapshot", &got); !errors.Is(err, ErrOpaqueJSONCorrupt) {
		t.Fatalf("expected corruption, got %v", err)
	}
	if err := ns.Quarantine(context.Background(), "snapshot"); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if _, ok := backend.items["opaque/v1/l402-token-cache/snapshot"]; ok {
		t.Fatal("active corrupt record still exists")
	}
	foundQuarantine := false
	for key, raw := range backend.items {
		if strings.Contains(key, "/quarantine.snapshot.") && raw == "{bad json" {
			foundQuarantine = true
		}
	}
	if !foundQuarantine {
		t.Fatalf("quarantine copy missing: %#v", backend.items)
	}
}

func TestOpaqueJSONNamespaceBackendFailureAndCancellation(t *testing.T) {
	backend := &protectedMemoryBackend{items: map[string]string{}, fail: true}
	store := NewStore(nil)
	store.SetBackend(backend)
	ns, err := store.OpenProtectedJSONNamespace("l402-token-cache")
	if err != nil {
		t.Fatal(err)
	}
	if err := ns.Put(context.Background(), "snapshot", map[string]int{"v": 1}); err == nil || strings.Contains(err.Error(), "{\"v\"") {
		t.Fatalf("expected sanitized backend error, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ns.Put(ctx, "snapshot", struct{}{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestOpaqueJSONNamespaceSizeLimit(t *testing.T) {
	backend := &protectedMemoryBackend{items: map[string]string{}}
	store := NewStore(nil)
	store.SetBackend(backend)
	ns, err := store.OpenProtectedJSONNamespace("l402-token-cache")
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]string{"value": strings.Repeat("x", MaxOpaqueJSONBytes)}
	if raw, _ := json.Marshal(value); len(raw) <= MaxOpaqueJSONBytes {
		t.Fatal("test value is not oversized")
	}
	if err := ns.Put(context.Background(), "snapshot", value); err == nil {
		t.Fatal("expected size rejection")
	}
}

func TestLightningTargetRegistry(t *testing.T) {
	registry := LightningTargetRegistry()
	if err := registry.Validate("extra.lightning.wallets.default.uri", "nostr+walletconnect://plaintext"); err == nil {
		t.Fatal("expected plaintext wallet URI rejection")
	}
	if err := registry.Validate("extra.lightning.wallets.default.uri", "secret:NWC_CONNECTION_STRING"); err != nil {
		t.Fatalf("secret URI rejected: %v", err)
	}
	if err := registry.Validate("extra.lightning.lnd.profiles.primary.macaroon.ref", "file:/tmp/admin.macaroon"); err != nil {
		t.Fatalf("file ref rejected: %v", err)
	}
}
