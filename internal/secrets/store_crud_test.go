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
