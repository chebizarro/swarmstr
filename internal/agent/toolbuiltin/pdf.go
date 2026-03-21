package toolbuiltin

import (
	"context"
	"fmt"
	"os"

	"metiq/internal/agent"
	"metiq/internal/media"
)

const defaultPDFMaxChars = 100_000

// PDFTool returns an agent.ToolFunc for the "read_pdf" tool.
//
// Tool parameters:
//   - path (string, required) – local file system path to the PDF
//   - max_chars (int, default 100000) – truncation limit
//
// allowedRoots restricts which directories may be read.  Pass nil to allow
// any path (not recommended in production).
func PDFTool(allowedRoots []string) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		path := agent.ArgString(args, "path")
		if path == "" {
			return "", fmt.Errorf("read_pdf: path is required")
		}
		maxChars := agent.ArgInt(args, "max_chars", defaultPDFMaxChars)
		if maxChars <= 0 {
			maxChars = defaultPDFMaxChars
		}

		if !IsPathAllowed(path, allowedRoots) {
			return "", fmt.Errorf("read_pdf: path %q is outside allowed roots", path)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read_pdf: read file: %w", err)
		}

		text, err := media.ExtractPDFText(ctx, data)
		if err != nil {
			return "", fmt.Errorf("read_pdf: %w", err)
		}

		return Truncate(text, maxChars), nil
	}
}
