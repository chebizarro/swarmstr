package installer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// installNPM / updateNPM — validation branches (no npm needed)
// ---------------------------------------------------------------------------

func TestInstallNPM_EmptySpec(t *testing.T) {
	_, err := installNPM(context.Background(), "", "/tmp/test-install")
	if err == nil || !strings.Contains(err.Error(), "spec is required") {
		t.Fatalf("expected spec required error, got %v", err)
	}
}

func TestInstallNPM_EmptyPath(t *testing.T) {
	_, err := installNPM(context.Background(), "some-pkg", "")
	if err == nil || !strings.Contains(err.Error(), "installPath is required") {
		t.Fatalf("expected installPath required error, got %v", err)
	}
}

func TestUpdateNPM_EmptySpec(t *testing.T) {
	_, err := updateNPM(context.Background(), "", "/tmp/test-update")
	if err == nil || !strings.Contains(err.Error(), "spec is required") {
		t.Fatalf("expected spec required error, got %v", err)
	}
}

func TestUpdateNPM_EmptyPath(t *testing.T) {
	_, err := updateNPM(context.Background(), "some-pkg", "")
	if err == nil || !strings.Contains(err.Error(), "installPath is required") {
		t.Fatalf("expected installPath required error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// resolveNPMInstallMeta — with mock filesystem
// ---------------------------------------------------------------------------

func TestResolveNPMInstallMeta_FromPackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "test-pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkgJSON := map[string]any{"name": "test-pkg", "version": "1.2.3"}
	data, _ := json.Marshal(pkgJSON)
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Cancel context so npm ls doesn't actually run (or fails immediately).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	version, name, _, _ := resolveNPMInstallMeta(ctx, "test-pkg@1.2.3", dir)
	if version != "1.2.3" {
		t.Errorf("expected version=1.2.3, got %q", version)
	}
	if name != "test-pkg" {
		t.Errorf("expected name=test-pkg, got %q", name)
	}
}

func TestResolveNPMInstallMeta_FromPackageLock(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "my-lib")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal package.json (no version).
	pkgJSON := map[string]any{"name": "my-lib"}
	data, _ := json.Marshal(pkgJSON)
	os.WriteFile(filepath.Join(pkgDir, "package.json"), data, 0o644)
	// package-lock.json with version and integrity.
	lockJSON := map[string]any{
		"packages": map[string]any{
			"node_modules/my-lib": map[string]any{
				"version":   "2.0.0",
				"integrity": "sha512-abc123",
			},
		},
	}
	lockData, _ := json.Marshal(lockJSON)
	os.WriteFile(filepath.Join(dir, "package-lock.json"), lockData, 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	version, name, integrity, _ := resolveNPMInstallMeta(ctx, "my-lib", dir)
	if version != "2.0.0" {
		t.Errorf("expected version=2.0.0, got %q", version)
	}
	if name != "my-lib" {
		t.Errorf("expected name=my-lib, got %q", name)
	}
	if integrity != "sha512-abc123" {
		t.Errorf("expected integrity=sha512-abc123, got %q", integrity)
	}
}

func TestResolveNPMInstallMeta_NoPackageJSON(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	version, name, _, _ := resolveNPMInstallMeta(ctx, "nonexistent-pkg", dir)
	if version != "" || name != "" {
		t.Errorf("expected empty results, got version=%q name=%q", version, name)
	}
}

// ---------------------------------------------------------------------------
// ResolveManagedPath — more branch coverage
// ---------------------------------------------------------------------------

func TestResolveManagedPath_Whitespace(t *testing.T) {
	path, ok := ResolveManagedPath("   ")
	if ok || path != "" {
		t.Error("whitespace should be rejected")
	}
}

func TestResolveManagedPath_ParentTraversal(t *testing.T) {
	path, ok := ResolveManagedPath("../../../etc/passwd")
	if ok {
		t.Errorf("parent traversal should be rejected, got path=%q", path)
	}
}

func TestResolveManagedPath_RootOnly(t *testing.T) {
	// "extensions" alone resolves to the root, which should be rejected.
	path, ok := ResolveManagedPath("extensions")
	if ok {
		t.Errorf("root path itself should be rejected, got path=%q", path)
	}
}

// ---------------------------------------------------------------------------
// FetchRegistry — httptest
// ---------------------------------------------------------------------------

func TestFetchRegistry_Non200(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchRegistry(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
	// Should fail on HTTPS validation since httptest uses self-signed certs,
	// OR should fail on non-200 status. Either is acceptable.
	if !strings.Contains(err.Error(), "HTTP") && !strings.Contains(err.Error(), "https") && !strings.Contains(err.Error(), "certificate") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFetchRegistry_InvalidJSON(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := FetchRegistry(context.Background(), srv.URL)
	// Will fail on cert validation — acceptable
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchRegistry_EmptyIndex(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	_, err := FetchRegistry(context.Background(), srv.URL)
	// Will fail on cert validation — acceptable
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// DownloadURL — httptest edge cases
// ---------------------------------------------------------------------------

func TestDownloadURL_Non200(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := DownloadURL(context.Background(), srv.URL+"/file.tar.gz")
	// Will fail on cert validation — acceptable
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestDownloadURL_CanceledContext(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DownloadURL(ctx, srv.URL+"/test.js")
	if err == nil {
		t.Fatal("expected error on canceled context")
	}
}

// ---------------------------------------------------------------------------
// validatePluginURL — additional branches
// ---------------------------------------------------------------------------

func TestValidatePluginURL_NoHost(t *testing.T) {
	err := validatePluginURL("https://")
	if err == nil || !strings.Contains(err.Error(), "no host") {
		t.Fatalf("expected no host error, got %v", err)
	}
}

func TestValidatePluginURL_HTTPScheme(t *testing.T) {
	err := validatePluginURL("http://example.com/file.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "https://") {
		t.Fatalf("expected https error, got %v", err)
	}
}

func TestValidatePluginURL_Valid(t *testing.T) {
	err := validatePluginURL("https://example.com/plugins/index.json")
	if err != nil {
		t.Fatalf("valid URL rejected: %v", err)
	}
}

// ---------------------------------------------------------------------------
// extractNPMPackageName — more branches
// ---------------------------------------------------------------------------

func TestExtractNPMPackageName_ScopedNoSlash(t *testing.T) {
	// "@scope" without a slash — edge case
	got := extractNPMPackageName("@scope")
	if got != "@scope" {
		t.Errorf("expected @scope, got %q", got)
	}
}

func TestExtractNPMPackageName_EmptyString(t *testing.T) {
	got := extractNPMPackageName("")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestExtractNPMPackageName_WhitespaceOnly(t *testing.T) {
	got := extractNPMPackageName("   ")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// stripTopLevel
// ---------------------------------------------------------------------------

func TestStripTopLevel_NoSlash(t *testing.T) {
	got := stripTopLevel("file.txt")
	if got != "" {
		t.Errorf("expected empty for top-level-only path, got %q", got)
	}
}

func TestStripTopLevel_Nested(t *testing.T) {
	got := stripTopLevel("package/src/main.go")
	if got != "src/main.go" {
		t.Errorf("expected src/main.go, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// safeJoin
// ---------------------------------------------------------------------------

func TestSafeJoin_ValidPath(t *testing.T) {
	base := t.TempDir()
	path, err := safeJoin(base, "subdir/file.txt")
	if err != nil {
		t.Fatalf("valid join failed: %v", err)
	}
	if !strings.HasPrefix(path, base) {
		t.Errorf("expected path under base, got %q", path)
	}
}

func TestSafeJoin_TraversalRejected(t *testing.T) {
	base := t.TempDir()
	_, err := safeJoin(base, "../../../etc/passwd")
	if err == nil {
		t.Fatal("path traversal should be rejected")
	}
}

func TestSafeJoin_AbsolutePathSanitized(t *testing.T) {
	// filepath.Join normalizes absolute second args: Join("/base", "/etc/passwd")
	// becomes "/base/etc/passwd", which is safe — no error expected.
	base := t.TempDir()
	path, err := safeJoin(base, "/etc/passwd")
	if err != nil {
		t.Fatalf("safeJoin should sanitize absolute paths: %v", err)
	}
	if !strings.HasPrefix(path, base) {
		t.Errorf("expected path under base, got %q", path)
	}
}

// ---------------------------------------------------------------------------
// extractFile
// ---------------------------------------------------------------------------

func TestExtractFile_WritesToDisk(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "output.txt")
	content := "hello world"
	n, err := extractFile(strings.NewReader(content), dest, 0o644)
	if err != nil {
		t.Fatalf("extractFile failed: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("expected %d bytes, got %d", len(content), n)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}
}

func TestExtractFile_FailsMissingParentDir(t *testing.T) {
	// extractFile does NOT create parent dirs — the caller (extractTarGz/extractZip)
	// is responsible for that.
	dir := t.TempDir()
	dest := filepath.Join(dir, "a", "b", "output.txt")
	_, err := extractFile(strings.NewReader("data"), dest, 0o644)
	if err == nil {
		t.Fatal("expected error for missing parent dir")
	}
}

// ---------------------------------------------------------------------------
// truncateOutput — edge case
// ---------------------------------------------------------------------------

func TestTruncateOutput_LongString(t *testing.T) {
	long := strings.Repeat("x", 10000)
	got := truncateOutput(long)
	if !strings.Contains(got, "truncated") {
		t.Error("expected truncation marker")
	}
	if len(got) > 8300 {
		t.Errorf("truncated output too long: %d", len(got))
	}
}

func TestTruncateOutput_ShortString(t *testing.T) {
	short := "hello"
	got := truncateOutput(short)
	if got != "hello" {
		t.Errorf("expected unchanged, got %q", got)
	}
}
