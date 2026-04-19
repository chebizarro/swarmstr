package toolbuiltin

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"metiq/internal/agent"
)

const (
	defaultImagePrompt   = "Describe this image."
	defaultImageMaxBytes = 20 * 1024 * 1024 // 20 MiB
	imageTimeoutSec      = 30
)

// ImageOpts configures the image analysis tool.
type ImageOpts struct {
	// AllowedRoots restricts which directories may be read for local files.
	// Nil allows any path (use with caution).
	AllowedRoots []string
	// MaxBytes limits the download size for URL images (default 20 MiB).
	MaxBytes int64
}

// ImageTool returns an agent.ToolFunc for the "image" tool.
//
// The tool submits an image to the configured agent runtime for description
// or analysis.  The runtime must use a vision-capable provider (e.g. OpenAI
// gpt-4o) or the underlying model will produce a text-only response.
//
// Tool parameters:
//   - url (string, optional) – HTTPS image URL to fetch and analyse
//   - path (string, optional) – local file path to read and analyse
//   - prompt (string, optional, default "Describe this image.")
//
// Exactly one of url or path must be provided.
func ImageTool(rt agent.Runtime, opts ImageOpts) agent.ToolFunc {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultImageMaxBytes
	}
	return func(ctx context.Context, args map[string]any) (string, error) {
		imageURL := agent.ArgString(args, "url")
		imagePath := agent.ArgString(args, "path")
		prompt := agent.ArgString(args, "prompt")
		if prompt == "" {
			prompt = defaultImagePrompt
		}

		if imageURL == "" && imagePath == "" {
			return "", fmt.Errorf("image: one of 'url' or 'path' is required")
		}
		if imageURL != "" && imagePath != "" {
			return "", fmt.Errorf("image: provide either 'url' or 'path', not both")
		}

		var data []byte
		var mimeType string
		var err error

		if imageURL != "" {
			if valErr := ValidateFetchURL(imageURL, false); valErr != nil {
				return "", fmt.Errorf("image: %w", valErr)
			}
			data, mimeType, err = fetchImageURL(ctx, imageURL, opts.MaxBytes)
			if err != nil {
				return "", fmt.Errorf("image: fetch %s: %w", imageURL, err)
			}
		} else {
			if !IsPathAllowed(imagePath, opts.AllowedRoots) {
				return "", fmt.Errorf("image: path %q is outside allowed roots", imagePath)
			}
			data, err = os.ReadFile(imagePath)
			if err != nil {
				return "", fmt.Errorf("image: read file: %w", err)
			}
			mimeType = guessMIMEFromPath(imagePath, data)
		}

		encoded := base64.StdEncoding.EncodeToString(data)
		turn := agent.Turn{
			UserText: prompt,
			Images:   []agent.ImageRef{{Base64: encoded, MimeType: mimeType}},
		}

		result, err := rt.ProcessTurn(ctx, turn)
		if err != nil {
			return "", fmt.Errorf("image: model: %w", err)
		}
		return result.Text, nil
	}
}

// fetchImageURL downloads an image from a URL, returning its bytes and MIME type.
func fetchImageURL(ctx context.Context, rawURL string, maxBytes int64) ([]byte, string, error) {
	ctx2, cancel := context.WithTimeout(ctx, imageTimeout(ctx))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx2, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "metiqd/image-tool")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("image too large (>%d bytes)", maxBytes)
	}

	ct := resp.Header.Get("Content-Type")
	mime := ct
	if idx := strings.Index(ct, ";"); idx >= 0 {
		mime = strings.TrimSpace(ct[:idx])
	}
	if !isImageMIME(mime) {
		mime = sniffMIMEFromBytes(data)
	}
	return data, mime, nil
}

// guessMIMEFromPath returns a MIME type for a file based on extension then magic bytes.
func guessMIMEFromPath(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return sniffMIMEFromBytes(data)
}

// sniffMIMEFromBytes detects image type from magic bytes; defaults to image/jpeg.
func sniffMIMEFromBytes(data []byte) string {
	if len(data) >= 4 {
		switch {
		case data[0] == 0xFF && data[1] == 0xD8:
			return "image/jpeg"
		case data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47:
			return "image/png"
		case data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46:
			return "image/gif"
		case len(data) >= 12 && string(data[8:12]) == "WEBP":
			return "image/webp"
		}
	}
	return "image/jpeg"
}

func isImageMIME(mime string) bool {
	return strings.HasPrefix(strings.ToLower(mime), "image/")
}
