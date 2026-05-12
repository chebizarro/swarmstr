package secrets

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeEnvFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// ────────────────────────────────────────────────────────────────────────────
// parseEnvFile
// ────────────────────────────────────────────────────────────────────────────

func TestParseEnvFile_basic(t *testing.T) {
	dir := t.TempDir()
	p := writeEnvFile(t, dir, ".env", `
# comment
KEY1=value1
KEY2="quoted value"
KEY3='single quoted'
export KEY4=exported
KEY5=
`)
	kvs, err := parseEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"KEY1": "value1",
		"KEY2": "quoted value",
		"KEY3": "single quoted",
		"KEY4": "exported",
		"KEY5": "",
	}
	for k, want := range cases {
		if got := kvs[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestParseEnvFile_missing(t *testing.T) {
	_, err := parseEnvFile("/nonexistent/path/.env")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// ────────────────────────────────────────────────────────────────────────────
// parseSecretRef
// ────────────────────────────────────────────────────────────────────────────

func TestParseSecretRef(t *testing.T) {
	cases := []struct {
		in      string
		varName string
		isRef   bool
	}{
		{"$MYVAR", "MYVAR", true},
		{"${MYVAR}", "MYVAR", true},
		{"env:MYVAR", "MYVAR", true},
		{"plainvalue", "", false},
		{"https://example.com", "", false},
	}
	for _, tc := range cases {
		got, ok := parseSecretRef(tc.in)
		if ok != tc.isRef {
			t.Errorf("parseSecretRef(%q) isRef = %v, want %v", tc.in, ok, tc.isRef)
		}
		if got != tc.varName {
			t.Errorf("parseSecretRef(%q) varName = %q, want %q", tc.in, got, tc.varName)
		}
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Store
// ────────────────────────────────────────────────────────────────────────────

func TestStore_reload(t *testing.T) {
	dir := t.TempDir()
	p := writeEnvFile(t, dir, ".env", "SECRET_A=alpha\nSECRET_B=beta\n")

	s := NewStore([]string{p})
	count, warnings := s.Reload()
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if s.Count() != 2 {
		t.Errorf("Count() = %d, want 2", s.Count())
	}
}

func TestStore_reloadMissingFile(t *testing.T) {
	s := NewStore([]string{"/nonexistent/.env"})
	count, warnings := s.Reload()
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
	// Missing file is silently ignored (no warnings for not-exist).
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for missing file, got: %v", warnings)
	}
}

func TestStore_resolve_envVar(t *testing.T) {
	t.Setenv("TEST_SECRET_XYZ", "from-env")

	s := NewStore(nil)
	v, found := s.Resolve("$TEST_SECRET_XYZ")
	if !found {
		t.Error("expected found=true for env var")
	}
	if v != "from-env" {
		t.Errorf("v = %q, want from-env", v)
	}
}

func TestStore_resolve_dotenvValue(t *testing.T) {
	dir := t.TempDir()
	p := writeEnvFile(t, dir, ".env", "MY_SECRET=secretval\n")

	// Make sure it's not set in env.
	os.Unsetenv("MY_SECRET")

	s := NewStore([]string{p})
	s.Reload()

	v, found := s.Resolve("$MY_SECRET")
	if !found {
		t.Error("expected found=true")
	}
	if v != "secretval" {
		t.Errorf("v = %q, want secretval", v)
	}
}

func TestStore_resolve_plainString(t *testing.T) {
	s := NewStore(nil)
	v, found := s.Resolve("just-a-plain-string")
	if !found {
		t.Error("plain string should return found=true")
	}
	if v != "just-a-plain-string" {
		t.Errorf("v = %q, want just-a-plain-string", v)
	}
}

func TestStore_resolve_unknown(t *testing.T) {
	os.Unsetenv("METIQ_NONEXISTENT_VAR_ABC123")
	s := NewStore(nil)
	_, found := s.Resolve("$METIQ_NONEXISTENT_VAR_ABC123")
	if found {
		t.Error("expected found=false for unknown var")
	}
}

func TestStore_resolveMany(t *testing.T) {
	t.Setenv("METIQ_TEST_VAR_456", "hello")

	s := NewStore(nil)
	results := s.ResolveMany([]string{"$METIQ_TEST_VAR_456", "plaintext", "$UNKNOWN_METIQ_VAR_XYZ"})

	if len(results) != 3 {
		t.Fatalf("len = %d, want 3", len(results))
	}
	if !results[0].Found || results[0].Value != "hello" {
		t.Errorf("result[0] unexpected: %+v", results[0])
	}
	if !results[1].Found || !results[1].IsPlain {
		t.Errorf("result[1] should be plain: %+v", results[1])
	}
	if results[2].Found {
		t.Errorf("result[2] should be not found: %+v", results[2])
	}
}

func TestStore_envOverrideDotenv(t *testing.T) {
	dir := t.TempDir()
	p := writeEnvFile(t, dir, ".env", "PRIO_VAR=from-file\n")

	t.Setenv("PRIO_VAR", "from-env")

	s := NewStore([]string{p})
	s.Reload()

	v, found := s.Resolve("$PRIO_VAR")
	if !found {
		t.Error("expected found=true")
	}
	if v != "from-env" {
		t.Errorf("env should take priority: got %q, want from-env", v)
	}
}

type memoryBackend struct {
	items map[string]string
	fail  bool
}

func (m *memoryBackend) Name() string { return "memory" }
func (m *memoryBackend) Get(key string) (string, bool, error) {
	if m.fail {
		return "", false, os.ErrPermission
	}
	v, ok := m.items[key]
	return v, ok, nil
}
func (m *memoryBackend) Set(key, value string) error {
	if m.fail {
		return os.ErrPermission
	}
	if m.items == nil {
		m.items = map[string]string{}
	}
	m.items[key] = value
	return nil
}
func (m *memoryBackend) Delete(key string) error {
	delete(m.items, key)
	return nil
}

func TestStore_mcpCredentialUsesBackendAndRemovesFallback(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "mcp-auth.json")
	backend := &memoryBackend{items: map[string]string{}}
	s := NewStore(nil)
	s.SetMCPAuthPath(authPath)
	s.SetBackend(backend)
	if err := s.PutMCPCredential("demo", MCPAuthCredential{AccessToken: "token-a"}); err != nil {
		t.Fatalf("PutMCPCredential error: %v", err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("expected no plaintext fallback file, stat err=%v", err)
	}
	reloaded := NewStore(nil)
	reloaded.SetMCPAuthPath(authPath)
	reloaded.SetBackend(backend)
	if _, warnings := reloaded.Reload(); len(warnings) != 0 {
		t.Fatalf("unexpected reload warnings: %v", warnings)
	}
	got, ok := reloaded.GetMCPCredential("demo")
	if !ok || got.AccessToken != "token-a" {
		t.Fatalf("unexpected backend credential: %#v ok=%v", got, ok)
	}
}

func TestStore_mcpCredentialFallsBackWhenBackendFails(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "mcp-auth.json")
	s := NewStore(nil)
	s.SetMCPAuthPath(authPath)
	s.SetBackend(&memoryBackend{fail: true})
	if err := s.PutMCPCredential("demo", MCPAuthCredential{AccessToken: "token-a"}); err != nil {
		t.Fatalf("PutMCPCredential error: %v", err)
	}
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("expected plaintext fallback file: %v", err)
	}
}

func TestStore_mcpCredentialPersistence(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "mcp-auth.json")
	s := NewStore(nil)
	s.SetMCPAuthPath(authPath)
	s.SetBackend(nil)
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if err := s.PutMCPCredential("demo", MCPAuthCredential{
		AccessToken:  "token-a",
		RefreshToken: "refresh-a",
		TokenType:    "Bearer",
		Expiry:       expiry,
		ClientSecret: "secret-a",
		Scopes:       []string{"profile", "offline_access"},
	}); err != nil {
		t.Fatalf("PutMCPCredential error: %v", err)
	}
	got, ok := s.GetMCPCredential("demo")
	if !ok {
		t.Fatalf("expected stored credential")
	}
	if got.AccessToken != "token-a" || got.RefreshToken != "refresh-a" || got.ClientSecret != "secret-a" {
		t.Fatalf("unexpected stored credential: %#v", got)
	}
	reloaded := NewStore(nil)
	reloaded.SetMCPAuthPath(authPath)
	reloaded.SetBackend(nil)
	if _, warnings := reloaded.Reload(); len(warnings) != 0 {
		t.Fatalf("unexpected reload warnings: %v", warnings)
	}
	got, ok = reloaded.GetMCPCredential("demo")
	if !ok {
		t.Fatalf("expected persisted credential")
	}
	if !got.Expiry.Equal(expiry) {
		t.Fatalf("expected expiry %v, got %v", expiry, got.Expiry)
	}
	deleted, err := reloaded.DeleteMCPCredential("demo")
	if err != nil {
		t.Fatalf("DeleteMCPCredential error: %v", err)
	}
	if !deleted {
		t.Fatalf("expected credential deletion")
	}
	if _, ok := reloaded.GetMCPCredential("demo"); ok {
		t.Fatalf("expected credential to be removed")
	}
}
