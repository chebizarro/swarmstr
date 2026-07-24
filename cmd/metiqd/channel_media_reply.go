package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"metiq/internal/extensions/channelmedia"
	"metiq/internal/plugins/sdk"
)

// channelMediaDispatch reports the outcome of delivering an agent media reply
// through a channel's optional media capabilities.
type channelMediaDispatch struct {
	// sent is true when the media was delivered through a media-capable handle.
	sent bool
	// method identifies the delivery path used: "media" (sdk.MediaHandle) or
	// "audio" (sdk.AudioHandle). Empty when nothing was sent.
	method string
	// err aggregates delivery/read errors from failed attempts. It can be
	// non-nil even when sent is true (e.g. SendMedia failed but the audio
	// fallback succeeded).
	err error
}

// dispatchChannelMediaReply delivers an agent media reply through the richest
// handle the channel supports, mirroring the sdk.AudioHandle pattern:
//
//  1. sdk.MediaHandle.SendMedia with the shared direct text/media contract
//     (the four media channels implement this via internal/extensions/channelmedia).
//  2. sdk.AudioHandle.SendAudio, for audio-kind media only, preserving the
//     legacy raw-bytes path for channels without MediaHandle.
//
// When neither succeeds the caller should fall back to text-only Send.
func dispatchChannelMediaReply(ctx context.Context, handle sdk.ChannelHandle, recipient, mediaPath string) channelMediaDispatch {
	var errs []error
	if mh, ok := handle.(sdk.MediaHandle); ok {
		payload := sdk.DirectTextMediaPayload{
			To:    recipient,
			Media: []sdk.MediaPayloadInput{{Path: mediaPath}},
		}
		if err := mh.SendMedia(ctx, payload); err == nil {
			return channelMediaDispatch{sent: true, method: "media"}
		} else {
			errs = append(errs, fmt.Errorf("media send: %w", err))
		}
	}
	if channelmedia.Kind(sdk.MediaPayloadInput{Path: mediaPath}) == channelmedia.KindAudio {
		if ah, ok := handle.(sdk.AudioHandle); ok {
			audioData, readErr := os.ReadFile(filepath.FromSlash(mediaPath))
			if readErr != nil {
				errs = append(errs, fmt.Errorf("audio read: %w", readErr))
			} else {
				format := strings.TrimPrefix(strings.ToLower(filepath.Ext(mediaPath)), ".")
				if format == "" {
					format = "mp3"
				}
				if err := ah.SendAudio(ctx, audioData, format); err == nil {
					return channelMediaDispatch{sent: true, method: "audio", err: errors.Join(errs...)}
				} else {
					errs = append(errs, fmt.Errorf("audio send: %w", err))
				}
			}
		}
	}
	return channelMediaDispatch{err: errors.Join(errs...)}
}

// mediaReplyFallbackText is the text-only placeholder sent when media delivery
// is unsupported or failed. Audio keeps its historical wording.
func mediaReplyFallbackText(mediaPath string) string {
	if channelmedia.Kind(sdk.MediaPayloadInput{Path: mediaPath}) == channelmedia.KindAudio {
		return fmt.Sprintf("[audio generated] %s", mediaPath)
	}
	return fmt.Sprintf("[media generated] %s", mediaPath)
}
