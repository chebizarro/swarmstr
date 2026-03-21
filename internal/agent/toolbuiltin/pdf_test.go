package toolbuiltin

import (
	"context"
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
	_, err := tool(context.Background(), map[string]any{"path": "/tmp/swarmstr-nonexistent-99999.pdf"})
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestPDFTool_ExtractorUnavailable(t *testing.T) {
	if media.PDFExtractorAvailable() {
		t.Skip("pdftotext is installed; skipping unavailability test")
	}

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
		t.Error("expected error when pdftotext is not available")
	}
}

func TestPDFTool_NilRootsAllowsAll(t *testing.T) {
	if media.PDFExtractorAvailable() {
		t.Skip("pdftotext is installed; test would attempt real extraction")
	}
	// With nil roots any path is allowed (path guard passes, error comes from pdftotext).
	tool := PDFTool(nil)
	f, err := os.CreateTemp("", "test-*.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Write([]byte("%PDF fake"))
	f.Close()

	_, err = tool(context.Background(), map[string]any{"path": f.Name()})
	// Error is expected (pdftotext unavailable), but it should NOT be a path-guard error.
	if err != nil && containsStr(err.Error(), "outside allowed roots") {
		t.Error("nil roots should not produce a path-guard error")
	}
}

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}
