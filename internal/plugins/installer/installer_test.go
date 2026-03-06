package installer_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"swarmstr/internal/plugins/installer"
)

// ---- helpers ---------------------------------------------------------------

func writeTarGz(t *testing.T, dest string, files map[string]string) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Size: int64(len(content)), Mode: 0o644, Typeflag: tar.TypeReg}
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
}

func writeZip(t *testing.T, dest string, files map[string]string) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
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
}

// ---- tests -----------------------------------------------------------------

func TestExtractArchive_TarGz_StripsTopLevel(t *testing.T) {
	tmpSrc := t.TempDir()
	tmpDest := t.TempDir()

	archivePath := filepath.Join(tmpSrc, "plugin.tar.gz")
	writeTarGz(t, archivePath, map[string]string{
		"package/index.js":      "console.log('hello')",
		"package/package.json":  `{"name":"my-plugin","version":"1.0.0"}`,
		"package/sub/helper.js": "module.exports={}",
	})

	inst := installer.New()
	res, err := inst.ExtractArchive(context.Background(), archivePath, tmpDest)
	if err != nil {
		t.Fatalf("ExtractArchive error: %v", err)
	}
	if res.InstallPath == "" {
		t.Error("expected non-empty InstallPath")
	}

	// Top-level "package/" should be stripped
	for _, f := range []string{"index.js", "package.json", "sub/helper.js"} {
		if _, err := os.Stat(filepath.Join(tmpDest, f)); err != nil {
			t.Errorf("expected extracted file %s: %v", f, err)
		}
	}
}

func TestExtractArchive_Zip(t *testing.T) {
	tmpSrc := t.TempDir()
	tmpDest := t.TempDir()

	archivePath := filepath.Join(tmpSrc, "plugin.zip")
	writeZip(t, archivePath, map[string]string{
		"plugin/index.js":     "module.exports={}",
		"plugin/package.json": `{"name":"zip-plugin","version":"2.0.0"}`,
	})

	inst := installer.New()
	res, err := inst.ExtractArchive(context.Background(), archivePath, tmpDest)
	if err != nil {
		t.Fatalf("ExtractArchive error: %v", err)
	}
	if res.InstallPath == "" {
		t.Error("expected non-empty InstallPath")
	}
	for _, f := range []string{"index.js", "package.json"} {
		if _, err := os.Stat(filepath.Join(tmpDest, f)); err != nil {
			t.Errorf("expected extracted file %s: %v", f, err)
		}
	}
}

func TestExtractArchive_ZipSlipRejected(t *testing.T) {
	tmpSrc := t.TempDir()
	tmpDest := t.TempDir()

	// Craft a zip with a path-traversal entry
	archivePath := filepath.Join(tmpSrc, "evil.zip")
	writeZip(t, archivePath, map[string]string{
		"package/../../evil.txt": "pwned",
	})

	inst := installer.New()
	_, err := inst.ExtractArchive(context.Background(), archivePath, tmpDest)
	if err == nil {
		t.Fatal("expected error for zip-slip archive, got nil")
	}
	if !strings.Contains(err.Error(), "escapes") && !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestExtractArchive_TarGz_ZipSlipRejected(t *testing.T) {
	tmpSrc := t.TempDir()
	tmpDest := t.TempDir()

	archivePath := filepath.Join(tmpSrc, "evil.tar.gz")
	writeTarGz(t, archivePath, map[string]string{
		"package/../../evil.txt": "pwned",
	})

	inst := installer.New()
	_, err := inst.ExtractArchive(context.Background(), archivePath, tmpDest)
	if err == nil {
		t.Fatal("expected error for path-traversal tar archive, got nil")
	}
}

func TestExtractArchive_UnsupportedFormat(t *testing.T) {
	inst := installer.New()
	_, err := inst.ExtractArchive(context.Background(), "/tmp/some.rar", "/tmp/out")
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestExtractArchive_MissingSource(t *testing.T) {
	inst := installer.New()
	_, err := inst.ExtractArchive(context.Background(), "", "/tmp/out")
	if err == nil {
		t.Fatal("expected error for empty sourcePath")
	}
}

func TestEnsureDir_CreatesDir(t *testing.T) {
	tmp := t.TempDir()
	newDir := filepath.Join(tmp, "sub", "child")
	if err := installer.EnsureDir(newDir); err != nil {
		t.Fatalf("EnsureDir error: %v", err)
	}
	info, err := os.Stat(newDir)
	if err != nil {
		t.Fatalf("stat new dir: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestEnsureDir_ExistingDir(t *testing.T) {
	tmp := t.TempDir()
	// Should not error on existing directory
	if err := installer.EnsureDir(tmp); err != nil {
		t.Fatalf("EnsureDir on existing dir: %v", err)
	}
}

func TestEnsureDir_PathIsFile(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := installer.EnsureDir(filePath)
	if err == nil {
		t.Fatal("expected error when path is a file")
	}
}
