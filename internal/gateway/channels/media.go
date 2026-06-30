package channels

import (
	"fmt"
	"mime"
	"net/url"
	"path/filepath"
	"strings"
)

const MB = 1024 * 1024

const (
	DefaultMaxMediaBytes = 25 * MB
	MaxMediaItems        = 10
)

type MediaPayloadInput struct {
	Path        string
	ContentType string
	SizeBytes   int64
}

type MediaPayload struct {
	MediaPath  string
	MediaType  string
	MediaURL   string
	MediaPaths []string
	MediaURLs  []string
	MediaTypes []string
}

type MediaLimits struct {
	MaxBytes     int64
	MaxItems     int
	AllowedMIMEs []string
}

type MediaValidationError struct {
	Index  int
	Reason string
}

func (e MediaValidationError) Error() string {
	if e.Index >= 0 {
		return fmt.Sprintf("media[%d]: %s", e.Index, e.Reason)
	}
	return e.Reason
}

type DirectTextMediaPayload struct {
	To        string
	Text      string
	Media     []MediaPayloadInput
	ReplyToID string
	AccountID string
}

func BuildMediaPayload(mediaList []MediaPayloadInput, preserveMediaTypeCardinality bool) MediaPayload {
	payload := MediaPayload{}
	if len(mediaList) == 0 {
		return payload
	}
	payload.MediaPath = mediaList[0].Path
	payload.MediaURL = mediaList[0].Path
	payload.MediaType = mediaList[0].ContentType
	payload.MediaPaths = make([]string, 0, len(mediaList))
	payload.MediaURLs = make([]string, 0, len(mediaList))
	payload.MediaTypes = make([]string, 0, len(mediaList))
	for _, media := range mediaList {
		payload.MediaPaths = append(payload.MediaPaths, media.Path)
		payload.MediaURLs = append(payload.MediaURLs, media.Path)
		if preserveMediaTypeCardinality || media.ContentType != "" {
			payload.MediaTypes = append(payload.MediaTypes, media.ContentType)
		}
	}
	if len(payload.MediaTypes) == 0 {
		payload.MediaTypes = nil
	}
	return payload
}

func ResolveMediaMaxBytes(channelLimitMB, defaultLimitMB int64) int64 {
	if channelLimitMB > 0 {
		return channelLimitMB * MB
	}
	if defaultLimitMB > 0 {
		return defaultLimitMB * MB
	}
	return 0
}

func ValidateMediaPayload(mediaList []MediaPayloadInput, limits MediaLimits) error {
	maxItems := limits.MaxItems
	if maxItems == 0 {
		maxItems = MaxMediaItems
	}
	if len(mediaList) > maxItems {
		return MediaValidationError{Index: -1, Reason: fmt.Sprintf("too many media items: %d > %d", len(mediaList), maxItems)}
	}
	maxBytes := limits.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxMediaBytes
	}
	for i, item := range mediaList {
		if strings.TrimSpace(item.Path) == "" {
			return MediaValidationError{Index: i, Reason: "path is required"}
		}
		if item.SizeBytes < 0 {
			return MediaValidationError{Index: i, Reason: "size_bytes must not be negative"}
		}
		if item.SizeBytes > maxBytes {
			return MediaValidationError{Index: i, Reason: fmt.Sprintf("size %d exceeds limit %d", item.SizeBytes, maxBytes)}
		}
		contentType := strings.TrimSpace(item.ContentType)
		if contentType != "" {
			if _, _, err := mime.ParseMediaType(contentType); err != nil {
				return MediaValidationError{Index: i, Reason: "invalid content_type"}
			}
			if len(limits.AllowedMIMEs) > 0 && !mimeAllowed(contentType, limits.AllowedMIMEs) {
				return MediaValidationError{Index: i, Reason: "content_type is not allowed"}
			}
		}
		if err := validateMediaPath(item.Path); err != nil {
			return MediaValidationError{Index: i, Reason: err.Error()}
		}
	}
	return nil
}

func validateMediaPath(path string) error {
	trimmed := strings.TrimSpace(path)
	if strings.ContainsAny(trimmed, "\x00\r\n") {
		return fmt.Errorf("path contains invalid characters")
	}
	if u, err := url.Parse(trimmed); err == nil && u.Scheme != "" {
		if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "file" {
			return fmt.Errorf("unsupported media URL scheme %q", u.Scheme)
		}
		if (u.Scheme == "http" || u.Scheme == "https") && u.Host == "" {
			return fmt.Errorf("media URL host is required")
		}
		return nil
	}
	if filepath.Clean(trimmed) == "." {
		return fmt.Errorf("path is required")
	}
	return nil
}

func mimeAllowed(contentType string, allowed []string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	for _, allow := range allowed {
		allow = strings.ToLower(strings.TrimSpace(allow))
		if allow == "" {
			continue
		}
		if allow == "*" || allow == mediaType {
			return true
		}
		if strings.HasSuffix(allow, "/*") && strings.HasPrefix(mediaType, strings.TrimSuffix(allow, "*")) {
			return true
		}
	}
	return false
}
