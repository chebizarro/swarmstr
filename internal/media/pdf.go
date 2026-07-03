package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LookPath resolves the pdftotext binary. It is a variable (defaulting to
// exec.LookPath) so tests can deterministically simulate the "installed" and
// "missing" code paths without depending on the host environment.
var LookPath = exec.LookPath

// RunPDFExtract runs the extractor with the resolved binary and input/output
// paths. It is a variable so tests can simulate success or failure without a
// real pdftotext binary. On success the implementation is expected to have
// written the extracted text to outPath.
var RunPDFExtract = func(ctx context.Context, bin, inPath, outPath string) ([]byte, error) {
	return exec.CommandContext(ctx, bin, inPath, outPath).CombinedOutput()
}

// PDFExtractorAvailable reports whether pdftotext is installed on this system.
func PDFExtractorAvailable() bool {
	_, err := LookPath("pdftotext")
	return err == nil
}

// ExtractPDFText extracts text from PDF bytes using the pdftotext command-line tool.
// Returns an error if pdftotext is not available or the extraction fails.
// pdftotext is part of poppler-utils (Linux: apt install poppler-utils,
// macOS: brew install poppler).
func ExtractPDFText(ctx context.Context, data []byte) (string, error) {
	path, err := LookPath("pdftotext")
	if err != nil {
		return "", fmt.Errorf("pdftotext not found: install poppler-utils (brew install poppler / apt install poppler-utils)")
	}

	// Write PDF bytes to a temp file.
	tmpIn, err := os.CreateTemp("", "metiq-pdf-*.pdf")
	if err != nil {
		return "", fmt.Errorf("pdf temp input: %w", err)
	}
	defer os.Remove(tmpIn.Name())
	if _, err := tmpIn.Write(data); err != nil {
		tmpIn.Close()
		return "", fmt.Errorf("pdf temp write: %w", err)
	}
	tmpIn.Close()

	// Output text file.
	tmpOut, err := os.CreateTemp("", "metiq-pdf-*.txt")
	if err != nil {
		return "", fmt.Errorf("pdf temp output: %w", err)
	}
	tmpOut.Close()
	defer os.Remove(tmpOut.Name())

	// pdftotext <input.pdf> <output.txt>
	if out, err := RunPDFExtract(ctx, path, tmpIn.Name(), tmpOut.Name()); err != nil {
		return "", fmt.Errorf("pdftotext failed: %s", strings.TrimSpace(string(out)))
	}

	text, err := os.ReadFile(tmpOut.Name())
	if err != nil {
		return "", fmt.Errorf("pdf text read: %w", err)
	}
	return strings.TrimSpace(string(text)), nil
}
