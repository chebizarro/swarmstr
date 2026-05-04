package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func useInstallerHTTPClient(t *testing.T, client *http.Client) {
	t.Helper()
	installerHTTPClientMu.Lock()
	old := newInstallerHTTPClient
	newInstallerHTTPClient = func(timeout time.Duration) *http.Client {
		cp := *client
		cp.Timeout = timeout
		return &cp
	}
	installerHTTPClientMu.Unlock()
	t.Cleanup(func() {
		installerHTTPClientMu.Lock()
		newInstallerHTTPClient = old
		installerHTTPClientMu.Unlock()
	})
}

func makeTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeTestTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTempArchive(t *testing.T, suffix string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin"+suffix)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDownloadURLAndFetchRegistrySuccessWithInjectedClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugin.tar.gz":
			w.Write(makeTestTarGz(t, map[string]string{"package/index.js": "module.exports = {}"}))
		case "/registry.json":
			_ = json.NewEncoder(w).Encode(RegistryIndex{Version: "1", Plugins: []RegistryPlugin{{ID: "p", Name: "Plugin", URL: "https://example.com/p.tgz"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	useInstallerHTTPClient(t, srv.Client())

	downloaded, err := DownloadURL(context.Background(), srv.URL+"/plugin.tar.gz")
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	defer os.Remove(downloaded)
	if !strings.HasSuffix(downloaded, ".tar.gz") {
		t.Fatalf("expected .tar.gz temp suffix, got %q", downloaded)
	}
	if info, err := os.Stat(downloaded); err != nil || info.Size() == 0 {
		t.Fatalf("downloaded file stat=%v err=%v", info, err)
	}

	idx, err := FetchRegistry(context.Background(), srv.URL+"/registry.json")
	if err != nil {
		t.Fatalf("FetchRegistry: %v", err)
	}
	if idx.Version != "1" || len(idx.Plugins) != 1 || idx.Plugins[0].ID != "p" {
		t.Fatalf("unexpected index: %+v", idx)
	}
}

func TestFetchRegistryTrustedClientErrorBranches(t *testing.T) {
	tests := []struct {
		path string
		body string
	}{
		{"/invalid", "not json"},
		{"/empty", `{}`},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, tc := range tests {
			if r.URL.Path == tc.path {
				w.Write([]byte(tc.body))
				return
			}
		}
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	useInstallerHTTPClient(t, srv.Client())

	for _, tc := range tests {
		if _, err := FetchRegistry(context.Background(), srv.URL+tc.path); err == nil {
			t.Fatalf("FetchRegistry(%s) expected error", tc.path)
		}
	}
	if _, err := FetchRegistry(context.Background(), srv.URL+"/missing"); err == nil || !strings.Contains(err.Error(), "HTTP 418") {
		t.Fatalf("expected HTTP 418 error, got %v", err)
	}
}

func TestExtractArchiveSuccessZipAndTarGz(t *testing.T) {
	cases := []struct {
		name   string
		suffix string
		data   []byte
	}{
		{"zip", ".zip", makeTestZip(t, map[string]string{"package/index.js": "zip", "package/lib/helper.js": "helper"})},
		{"tar", ".tar.gz", makeTestTarGz(t, map[string]string{"package/index.js": "tar", "package/lib/helper.js": "helper"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := writeTempArchive(t, tc.suffix, tc.data)
			dest := t.TempDir()
			res, err := extractArchive(context.Background(), source, dest)
			if err != nil {
				t.Fatalf("extractArchive: %v", err)
			}
			if res.InstallPath == "" {
				t.Fatal("InstallPath not set")
			}
			if got, err := os.ReadFile(filepath.Join(dest, "index.js")); err != nil || len(got) == 0 {
				t.Fatalf("index.js not extracted: %q err=%v", got, err)
			}
			if _, err := os.Stat(filepath.Join(dest, "lib", "helper.js")); err != nil {
				t.Fatalf("nested file not extracted: %v", err)
			}
		})
	}
}

func TestLoadOpenClawManifestExplicitAndPackageFallback(t *testing.T) {
	explicitDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(explicitDir, "openclaw.plugin.json"), []byte(`{"id":"explicit","name":"Explicit","version":"1.0.0","entry":"index.js"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mf, err := LoadOpenClawManifest(explicitDir)
	if err != nil || mf.ID != "explicit" || mf.Name != "Explicit" {
		t.Fatalf("explicit manifest=%+v err=%v", mf, err)
	}

	pkgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(`{"name":"pkg-plugin","version":"2.0.0","description":"fallback","main":"main.js","openclaw":{"kind":["tool"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mf, err = LoadOpenClawManifest(pkgDir)
	if err != nil || mf.ID != "pkg-plugin" || mf.Name != "pkg-plugin" || mf.Entry != "main.js" {
		t.Fatalf("package fallback=%+v err=%v", mf, err)
	}

	missingID := t.TempDir()
	if err := os.WriteFile(filepath.Join(missingID, "package.json"), []byte(`{"version":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOpenClawManifest(missingID); err == nil {
		t.Fatal("expected missing id error")
	}
}

func TestClawHubClientSearchInfoInstallAndUpdateArchive(t *testing.T) {
	archiveSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(makeTestZip(t, map[string]string{"package/index.js": "archive"}))
	}))
	defer archiveSrv.Close()
	useInstallerHTTPClient(t, archiveSrv.Client())

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/plugins":
			_ = json.NewEncoder(w).Encode(map[string]any{"plugins": []ClawHubPlugin{{ID: "p", Name: "Plugin", Version: "1.0.0", DistURL: archiveSrv.URL + "/plugin.zip"}}})
		case "/v1/plugins/p":
			if r.URL.Query().Get("version") != "" && r.URL.Query().Get("version") != "1.0.0" {
				t.Fatalf("unexpected version query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(ClawHubPlugin{ID: "p", Name: "Plugin", Version: "1.0.0", DistURL: archiveSrv.URL + "/plugin.zip"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiSrv.Close()

	client := NewClawHubClient(apiSrv.URL, apiSrv.Client())
	plugins, err := client.Search(context.Background(), "plug in")
	if err != nil || len(plugins) != 1 || plugins[0].ID != "p" {
		t.Fatalf("Search=%+v err=%v", plugins, err)
	}
	info, err := client.GetPluginInfo(context.Background(), "p")
	if err != nil || info.ID != "p" {
		t.Fatalf("GetPluginInfo=%+v err=%v", info, err)
	}
	installDir := filepath.Join(t.TempDir(), "install")
	if err := client.Install(context.Background(), "p", "1.0.0", installDir); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "index.js")); err != nil {
		t.Fatalf("installed file missing: %v", err)
	}
	if err := client.Update(context.Background(), "p", "", installDir); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestDefaultInstallerNPMWrappersWithFakeNPM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell npm is Unix-only")
	}
	binDir := t.TempDir()
	npmPath := filepath.Join(binDir, "npm")
	script := `#!/bin/sh
cmd="$1"
shift
prefix=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--prefix" ]; then
    shift
    prefix="$1"
  fi
  shift
done
if [ "$cmd" = "ls" ]; then
  echo '{"dependencies":{"fake-pkg":{"version":"9.9.9","integrity":"sha512-ls"}}}'
  exit 0
fi
mkdir -p "$prefix/node_modules/fake-pkg"
printf '{"name":"fake-pkg","version":"9.9.9"}' > "$prefix/node_modules/fake-pkg/package.json"
printf '{"packages":{"node_modules/fake-pkg":{"version":"9.9.9","integrity":"sha512-lock"}}}' > "$prefix/package-lock.json"
echo installed
`
	if err := os.WriteFile(npmPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	inst := New()
	installDir := t.TempDir()
	res, err := inst.InstallNPM(context.Background(), "fake-pkg@9.9.9", installDir)
	if err != nil {
		t.Fatalf("InstallNPM: %v", err)
	}
	if res.ResolvedSpec != "fake-pkg@9.9.9" || res.Integrity == "" || !strings.Contains(res.Stdout, "installed") {
		t.Fatalf("unexpected install result: %+v", res)
	}
	res, err = inst.UpdateNPM(context.Background(), "fake-pkg", installDir)
	if err != nil {
		t.Fatalf("UpdateNPM: %v", err)
	}
	if res.ResolvedVersion != "9.9.9" || res.ResolvedSpec != "fake-pkg@9.9.9" {
		t.Fatalf("unexpected update result: %+v", res)
	}
}
