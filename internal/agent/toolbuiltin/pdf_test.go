package toolbuiltin

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"metiq/internal/media"
)

func TestPDFTool_MissingPath(t *testing.T) {
	tool := PDFTool(nil)
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing path")
	}
}

func TestPDFTool_PathNotAllowed(t *testing.T) {
	tool := PDFTool([]string{"/tmp/allowed"})
	_, err := tool(context.Background(), map[string]any{"path": "/etc/passwd"})
	if err == nil {
		t.Error("expected error for disallowed path")
	}
}

func TestPDFTool_FileNotFound(t *testing.T) {
	tool := PDFTool([]string{os.TempDir()})
	_, err := tool(context.Background(), map[string]any{"path": "/tmp/metiq-nonexistent-99999.pdf"})
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestPDFTool_ExtractorUnavailable(t *testing.T) {
	// Force pdftotext to look unavailable so the tool must surface an error
	// regardless of whether the host actually has pdftotext installed.
	orig := media.LookPath
	defer func() { media.LookPath = orig }()
	media.LookPath = func(string) (string, error) { return "", errors.New("not found") }

	f, err := os.CreateTemp("", "test-*.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Write([]byte("%PDF-1.4 fake content"))
	f.Close()

	tool := PDFTool([]string{os.TempDir()})
	_, err = tool(context.Background(), map[string]any{"path": f.Name()})
	if err == nil {
		t.Fatal("expected error when pdftotext is not available")
	}
	if !strings.Contains(err.Error(), "pdftotext not found") {
		t.Errorf("expected pdftotext-not-found error, got %v", err)
	}
}

func TestPDFTool_NilRootsAllowsAll(t *testing.T) {
	// Force pdftotext unavailable so extraction fails deterministically; the
	// point of this test is that with nil roots the path guard is skipped, so
	// the error must come from extraction, not from the path guard.
	orig := media.LookPath
	defer func() { media.LookPath = orig }()
	media.LookPath = func(string) (string, error) { return "", errors.New("not found") }

	tool := PDFTool(nil)
	f, err := os.CreateTemp("", "test-*.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Write([]byte("%PDF fake"))
	f.Close()

	_, err = tool(context.Background(), map[string]any{"path": f.Name()})
	if err == nil {
		t.Fatal("expected extraction error when pdftotext is unavailable")
	}
	// With nil roots the path guard is skipped, so this must NOT be a guard error.
	if containsStr(err.Error(), "outside allowed roots") {
		t.Errorf("nil roots should not produce a path-guard error, got %v", err)
	}
}

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}
