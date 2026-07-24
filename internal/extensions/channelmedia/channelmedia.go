// Package channelmedia bridges the plugin SDK outbound media contract
// (sdk.DirectTextMediaPayload) to the shared gateway media contract in
// internal/gateway/channels, so channel extensions validate and shape
// outbound media consistently instead of ad hoc.
package channelmedia

import (
	"mime"
	"net/url"
	"path/filepath"
	"strings"

	"metiq/internal/gateway/channels"
	"metiq/internal/plugins/sdk"
)

// Media kind buckets shared by channel extensions.
const (
	KindImage    = "image"
	KindVideo    = "video"
	KindAudio    = "audio"
	KindDocument = "document"
)

// ToChannelInputs converts SDK media references into the gateway contract shape.
func ToChannelInputs(media []sdk.MediaPayloadInput) []channels.MediaPayloadInput {
	if len(media) == 0 {
		return nil
	}
	out := make([]channels.MediaPayloadInput, 0, len(media))
	for _, item := range media {
		out = append(out, channels.MediaPayloadInput{
			Path:        item.Path,
			ContentType: item.ContentType,
			SizeBytes:   item.SizeBytes,
		})
	}
	return out
}

// Validate applies the shared outbound media validation to SDK media inputs.
func Validate(media []sdk.MediaPayloadInput, limits channels.MediaLimits) error {
	return channels.ValidateMediaPayload(ToChannelInputs(media), limits)
}

// BuildPayload produces the legacy-compatible media field shape from SDK
// media inputs via the shared gateway helper.
func BuildPayload(media []sdk.MediaPayloadInput, preserveMediaTypeCardinality bool) channels.MediaPayload {
	return channels.BuildMediaPayload(ToChannelInputs(media), preserveMediaTypeCardinality)
}

// Kind buckets one media item into image, video, audio, or document using its
// declared content type first and its path extension as a fallback.
func Kind(item sdk.MediaPayloadInput) string {
	contentType := strings.ToLower(strings.TrimSpace(item.ContentType))
	if contentType != "" {
		if mediaType, _, err := mime.ParseMediaType(contentType); err == nil {
			switch {
			case strings.HasPrefix(mediaType, "image/"):
				return KindImage
			case strings.HasPrefix(mediaType, "video/"):
				return KindVideo
			case strings.HasPrefix(mediaType, "audio/"):
				return KindAudio
			}
			return KindDocument
		}
	}
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(pathWithoutQuery(item.Path)), ".")) {
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "heic", "heif":
		return KindImage
	case "mp4", "mov", "webm", "mkv", "avi", "m4v":
		return KindVideo
	case "mp3", "ogg", "oga", "opus", "wav", "flac", "m4a", "aac":
		return KindAudio
	default:
		return KindDocument
	}
}

// IsHTTPURL reports whether the media path is an http(s) URL that remote
// channel APIs can fetch directly.
func IsHTTPURL(path string) bool {
	u, err := url.Parse(strings.TrimSpace(path))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func pathWithoutQuery(path string) string {
	trimmed := strings.TrimSpace(path)
	if u, err := url.Parse(trimmed); err == nil && u.Scheme != "" {
		return u.Path
	}
	return trimmed
}
