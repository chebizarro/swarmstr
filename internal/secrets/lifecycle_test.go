package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

type lifecycleMemoryBackend struct {
	items map[string]string
}

func (b *lifecycleMemoryBackend) Name() string { return "memory" }
func (b *lifecycleMemoryBackend) Get(key string) (string, bool, error) {
	value, ok := b.items[key]
	return value, ok, nil
}
func (b *lifecycleMemoryBackend) Set(key, value string) error {
	b.items[key] = value
	return nil
}
func (b *lifecycleMemoryBackend) Delete(key string) error {
	delete(b.items, key)
	return nil
}

func TestLifecycleStructuredSources(t *testing.T) {
	ctx := context.Background()
	store := NewStore([]string{t.TempDir() + "/missing.env"})
	store.SetBackend(&lifecycleMemoryBackend{items: map[string]string{"team/token": "store-secret"}})
	lifecycle := NewLifecycle(store)

	t.Setenv("METIQ_LIFECYCLE_ENV", "env-secret")
	if got, err := lifecycle.ResolveRef(ctx, SecretRef{Source: SecretRefEnv, ID: "METIQ_LIFECYCLE_ENV"}); err != nil || got != "env-secret" {
		t.Fatalf("env got=%q err=%v", got, err)
	}

	filePath := t.TempDir() + "/secrets.json"
	if err := os.WriteFile(filePath, []byte(`{"nested":{"token":"file-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.SetProvider("local-file", SecretProviderConfig{FilePath: filePath}); err != nil {
		t.Fatal(err)
	}
	if got, err := lifecycle.ResolveRef(ctx, SecretRef{Source: SecretRefFile, Provider: "local-file", ID: "/nested/token"}); err != nil || got != "file-secret" {
		t.Fatalf("file got=%q err=%v", got, err)
	}

	t.Setenv("GO_WANT_SECRET_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.SetProvider("local-exec", SecretProviderConfig{Command: executable, Args: []string{"-test.run=TestSecretLifecycleExecHelper"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := lifecycle.ResolveRef(ctx, SecretRef{Source: SecretRefExec, Provider: "local-exec", ID: "/token"}); err != nil || got != "exec-secret" {
		t.Fatalf("exec got=%q err=%v", got, err)
	}

	if got, err := lifecycle.ResolveRef(ctx, SecretRef{Source: SecretRefStore, Provider: "team", ID: "token"}); err != nil || got != "store-secret" {
		t.Fatalf("store got=%q err=%v", got, err)
	}
}

func TestSecretLifecycleExecHelper(t *testing.T) {
	switch os.Getenv("GO_WANT_SECRET_HELPER") {
	case "1":
		fmt.Print(`{"token":"exec-secret"}`)
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stderr, "must-not-leak")
		os.Exit(7)
	}
}

func TestLifecycleSnapshotsColdFreshStaleAndEgressIsolation(t *testing.T) {
	store := NewStore([]string{t.TempDir() + "/missing.env"})
	lifecycle := NewLifecycle(store)
	missing := map[string]SecretRef{"api": {Source: SecretRefEnv, ID: "METIQ_LIFECYCLE_MISSING"}}
	cold := lifecycle.Refresh(context.Background(), "provider-a", missing)
	if cold.State != SecretSnapshotCold || cold.LastError == "" {
		t.Fatalf("cold snapshot=%+v", cold)
	}

	t.Setenv("METIQ_LIFECYCLE_KEY", "highly-sensitive-value")
	refs := map[string]SecretRef{"api": {Source: SecretRefEnv, ID: "METIQ_LIFECYCLE_KEY"}}
	fresh := lifecycle.Refresh(context.Background(), "provider-a", refs)
	sentinel, ok := fresh.Sentinel("api")
	if fresh.State != SecretSnapshotFresh || !ok || !strings.HasPrefix(sentinel, sentinelPrefix) {
		t.Fatalf("fresh snapshot=%+v sentinel=%q", fresh, sentinel)
	}
	raw, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "highly-sensitive-value") || strings.Contains(string(raw), sentinel) {
		t.Fatalf("snapshot JSON leaked secret material: %s", raw)
	}
	expanded, err := fresh.ExpandForEgress([]byte("Bearer " + sentinel))
	if err != nil || string(expanded) != "Bearer highly-sensitive-value" {
		t.Fatalf("expanded=%q err=%v", expanded, err)
	}

	foreign := lifecycle.Refresh(context.Background(), "provider-b", refs)
	foreignSentinel, _ := foreign.Sentinel("api")
	if _, err := fresh.ExpandForEgress([]byte(foreignSentinel)); err == nil {
		t.Fatal("foreign owner sentinel should fail closed")
	}

	t.Setenv("METIQ_LIFECYCLE_KEY", "")
	_ = os.Unsetenv("METIQ_LIFECYCLE_KEY")
	stale := lifecycle.Refresh(context.Background(), "provider-a", refs)
	staleSentinel, _ := stale.Sentinel("api")
	if stale.State != SecretSnapshotStale || staleSentinel != sentinel || stale.Generation <= fresh.Generation {
		t.Fatalf("stale snapshot=%+v sentinel=%q", stale, staleSentinel)
	}
	expanded, err = stale.ExpandForEgress([]byte(staleSentinel))
	if err != nil || string(expanded) != "highly-sensitive-value" {
		t.Fatalf("stale retained value failed: %q err=%v", expanded, err)
	}
}

func TestLifecycleRejectsUnsafeFileAndSanitizesExecFailure(t *testing.T) {
	lifecycle := NewLifecycle(NewStore([]string{t.TempDir() + "/missing.env"}))
	path := t.TempDir() + "/world-readable.json"
	if err := os.WriteFile(path, []byte(`{"token":"must-not-leak"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.SetProvider("unsafe", SecretProviderConfig{FilePath: path}); err != nil {
		t.Fatal(err)
	}
	_, err := lifecycle.ResolveRef(context.Background(), SecretRef{Source: SecretRefFile, Provider: "unsafe", ID: "/token"})
	if err == nil || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("unsafe file error=%v", err)
	}

	t.Setenv("GO_WANT_SECRET_HELPER", "fail")
	executable, execErr := os.Executable()
	if execErr != nil {
		t.Fatal(execErr)
	}
	if err := lifecycle.SetProvider("failing-exec", SecretProviderConfig{Command: executable, Args: []string{"-test.run=TestSecretLifecycleExecHelper"}}); err != nil {
		t.Fatal(err)
	}
	_, err = lifecycle.ResolveRef(context.Background(), SecretRef{Source: SecretRefExec, Provider: "failing-exec", ID: "/token"})
	if err == nil || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("exec failure was not sanitized: %v", err)
	}
}
