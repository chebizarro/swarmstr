package media

import (
	"context"
	"fmt"
	"strings"
)

type ImageDescriber interface {
	DescribeImage(ctx context.Context, image ImageRef, prompt string) (string, error)
	Configured() bool
}

type VideoDescriber interface {
	DescribeVideo(ctx context.Context, att MediaAttachment, prompt string) (string, error)
	Configured() bool
}

func selectImageDescriber(providers []ImageDescriber) (ImageDescriber, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no image understanding providers registered")
	}
	for _, p := range providers {
		if p != nil && p.Configured() {
			return p, nil
		}
	}
	for _, p := range providers {
		if p != nil {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no image understanding providers registered")
}
func selectTranscriber(providers []Transcriber) (Transcriber, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no audio transcribers registered")
	}
	for _, p := range providers {
		if p != nil && p.Configured() {
			return p, nil
		}
	}
	for _, p := range providers {
		if p != nil {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no audio transcribers registered")
}
func selectVideoDescriber(providers []VideoDescriber) (VideoDescriber, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("no video understanding providers registered")
	}
	for _, p := range providers {
		if p != nil && p.Configured() {
			return p, nil
		}
	}
	for _, p := range providers {
		if p != nil {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no video understanding providers registered")
}
func normalizeUnderstandingMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "describe"
	}
	return mode
}
func normalizePrompt(prompt string) string { return strings.TrimSpace(prompt) }
