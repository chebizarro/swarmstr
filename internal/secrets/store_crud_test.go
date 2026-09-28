package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStoredSecretCRUDAndStructuredRef(t *testing.T) {
	backend := &protectedMemoryBackend{items: map[string]string{}}
	store := NewStore(nil)
	store.SetBackend(backend)

	meta, err := store.SetStoredSecret("API_TOKEN", "super-secret-token", StoredSecretKindSecret, []string{"api.example.com"}, "operator-pubkey")
	if err != nil {
		t.Fatalf("set secret: %v", err)
	}
	if meta.Name != "API_TOKEN" || meta.Kind != StoredSecretKindSecret || meta.CreatedAtMS == 0 {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	if _, err := store.SetStoredSecret("PUBLIC_MODE", "enabled", StoredSecretKindEnv, nil, "operator-pubkey"); err != nil {
		t.Fatalf("set env: %v", err)
	}

	records, err := store.ListStoredSecrets()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 2 || records[0].Name != "API_TOKEN" || records[1].Name != "PUBLIC_MODE" {
		t.Fatalf("unexpected records: %+v", records)
	}
	if records[0].Value != "" || records[1].Value != "enabled" {
		t.Fatalf("unexpected value projection: %+v", records)
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super-secret-token") || strings.Contains(string(raw), "enabled") {
		t.Fatalf("record JSON leaked a stored value: %s", raw)
	}

	resolved, err := NewLifecycle(store).ResolveRef(context.Background(), StoredSecretRef("API_TOKEN"))
	if err != nil || resolved != "super-secret-token" {
		t.Fatalf("resolve structured ref: value=%q err=%v", resolved, err)
	}
	deleted, err := store.DeleteStoredSecret("API_TOKEN")
	if err != nil || !deleted {
		t.Fatalf("delete: deleted=%v err=%v", deleted, err)
	}
	if _, err := NewLifecycle(store).ResolveRef(context.Background(), StoredSecretRef("API_TOKEN")); !errors.Is(err, errSecretNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestStoredSecretRequiresProtectedBackendAndHidesInternalHandles(t *testing.T) {
	store := NewStore(nil)
	store.SetBackend(NewFileBackend(t.TempDir() + "/plain.json"))
	if _, err := store.SetStoredSecret("API_TOKEN", "value", StoredSecretKindSecret, nil, ""); !errors.Is(err, ErrProtectedBackendUnavailable) {
		t.Fatalf("expected protected backend error, got %v", err)
	}

	backend := &protectedMemoryBackend{items: map[string]string{}}
	store.SetBackend(backend)
	if _, err := store.SetStoredSecret("github-setup-0123456789abcdef0123456789abcdef", "one-time", StoredSecretKindSecret, nil, ""); err != nil {
		t.Fatalf("set internal handle: %v", err)
	}
	records, err := store.ListStoredSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("internal handles must not be listed: %+v", records)
	}
}

func TestRotateStoredSecretRequiresExistingEntry(t *testing.T) {
	store := NewStore(nil)
	store.SetBackend(&protectedMemoryBackend{items: map[string]string{}})
	if _, err := store.RotateStoredSecret("NOT_THERE", "value", "channel:x/y"); err == nil {
		t.Fatal("rotating a missing entry must fail rather than create one")
	}
	if records, err := store.ListStoredSecrets(); err != nil || len(records) != 0 {
		t.Fatalf("records = %+v, %v", records, err)
	}
}

func TestSecretRefFromConfig(t *testing.T) {
	ref, ok := SecretRefFromConfig(map[string]any{"source": "store", "provider": "gateway-store", "id": "ZALO_RT"})
	if !ok || ref != StoredSecretRef("ZALO_RT") {
		t.Fatalf("ref = %+v, %v", ref, ok)
	}
	if name, ok := StoredSecretName(ref); !ok || name != "ZALO_RT" {
		t.Fatalf("stored name = %q, %v", name, ok)
	}
	if _, ok := StoredSecretName(SecretRef{Source: SecretRefEnv, ID: "ZALO_RT"}); ok {
		t.Fatal("env refs are not gateway-store entries")
	}
	for _, value := range []any{
		"literal",
		map[string]any{"id": "X"},
		map[string]any{"source": "store", "id": "X", "extra": "y"},
		map[string]any{"source": "store", "id": 1},
	} {
		if _, ok := SecretRefFromConfig(value); ok {
			t.Fatalf("%#v must not parse as a secret ref", value)
		}
	}
}
