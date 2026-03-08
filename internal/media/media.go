// Package media provides utilities for handling media attachments in agent turns.
//
// Supported attachment types:
//   - image  → base64 or URL; forwarded to vision providers as multi-modal content
//   - audio  → transcribed to text via OpenAI Whisper before the DM is sent
//   - pdf    → text extracted via pdftotext before the DM is sent
package media

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MediaAttachment represents a media file attached to a chat message.
type MediaAttachment struct {
	Type     string `json:"type"`             // "image", "audio", "pdf"
	URL      string `json:"url,omitempty"`    // remote URL (optional)
	Base64   string `json:"base64,omitempty"` // base64-encoded raw bytes (optional)
	MimeType string `json:"mime_type,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// IsImage reports whether the attachment is an image.
func (a MediaAttachment) IsImage() bool { return strings.EqualFold(a.Type, "image") }

// IsAudio reports whether the attachment is audio.
func (a MediaAttachment) IsAudio() bool { return strings.EqualFold(a.Type, "audio") }

// IsPDF reports whether the attachment is a PDF document.
func (a MediaAttachment) IsPDF() bool { return strings.EqualFold(a.Type, "pdf") }

// ImageRef is a resolved image reference for passing to vision providers.
// Exactly one of URL or Base64 is set.
type ImageRef struct {
	URL      string // remote URL; provider may pass as URL reference
	Base64   string // base64-encoded binary (no data URI prefix)
	MimeType string // e.g. "image/jpeg", "image/png", "image/webp"
}

// ResolveImage converts an image attachment into an ImageRef ready for vision providers.
func ResolveImage(att MediaAttachment) (ImageRef, error) {
	if att.Base64 != "" {
		mt := strings.TrimSpace(att.MimeType)
		if mt == "" {
			mt = "image/jpeg"
		}
		return ImageRef{Base64: att.Base64, MimeType: mt}, nil
	}
	if att.URL != "" {
		return ImageRef{URL: att.URL, MimeType: att.MimeType}, nil
	}
	return ImageRef{}, fmt.Errorf("image attachment has neither url nor base64 content")
}

// InlineImageText returns a text representation of an image attachment suitable
// for injection into a text-only DM when vision multi-modal is not available.
func InlineImageText(att MediaAttachment) string {
	if att.URL != "" {
		return fmt.Sprintf("[Image: %s]", att.URL)
	}
	desc := att.MimeType
	if desc == "" {
		desc = "image"
	}
	if att.Filename != "" {
		desc = att.Filename
	}
	return fmt.Sprintf("[Attached image: %s]", desc)
}

// FetchAudioBytes resolves audio content from an attachment (base64 or URL).
// Returns the raw bytes and the effective MIME type.
func FetchAudioBytes(ctx context.Context, att MediaAttachment) ([]byte, string, error) {
	mimeType := strings.TrimSpace(att.MimeType)
	if mimeType == "" {
		mimeType = "audio/mpeg"
	}
	if att.Base64 != "" {
		data, err := base64.StdEncoding.DecodeString(att.Base64)
		if err != nil {
			return nil, "", fmt.Errorf("audio base64 decode: %w", err)
		}
		return data, mimeType, nil
	}
	if att.URL != "" {
		data, err := fetchURL(ctx, att.URL)
		return data, mimeType, err
	}
	return nil, "", fmt.Errorf("audio attachment has neither url nor base64 content")
}

// FetchPDFBytes resolves PDF content from an attachment (base64 or URL).
func FetchPDFBytes(ctx context.Context, att MediaAttachment) ([]byte, error) {
	if att.Base64 != "" {
		data, err := base64.StdEncoding.DecodeString(att.Base64)
		if err != nil {
			return nil, fmt.Errorf("pdf base64 decode: %w", err)
		}
		return data, nil
	}
	if att.URL != "" {
		return fetchURL(ctx, att.URL)
	}
	return nil, fmt.Errorf("pdf attachment has neither url nor base64 content")
}

// Transcriber transcribes audio bytes into text.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error)
	Configured() bool
}

// fetchURL downloads content from a URL and returns the raw bytes.
func fetchURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch build %s: %w", url, err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fetch %s read: %w", url, err)
	}
	return data, nil
}
