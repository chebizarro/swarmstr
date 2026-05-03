package media

import (
	"context"
	"fmt"
	"sort"
)

type MediaUnderstandingRequest struct {
	Attachments []MediaAttachment `json:"attachments"`
	Prompt      string            `json:"prompt,omitempty"`
	Mode        string            `json:"mode,omitempty"`
}
type MediaUnderstandingResult struct {
	Outputs []MediaOutput `json:"outputs"`
}
type MediaOutput struct {
	AttachmentIndex int     `json:"attachment_index"`
	Type            string  `json:"type"`
	Text            string  `json:"text"`
	Confidence      float64 `json:"confidence,omitempty"`
}

type OrchestratorOptions struct {
	ImageProviders    []ImageDescriber
	AudioTranscribers []Transcriber
	VideoDescribers   []VideoDescriber
	Cache             *AttachmentCache
	MaxConcurrent     int
}
type Orchestrator struct {
	imageProviders    []ImageDescriber
	audioTranscribers []Transcriber
	videoDescribers   []VideoDescriber
	cache             *AttachmentCache
	maxConcurrent     int
}

func NewOrchestrator(opts OrchestratorOptions) *Orchestrator {
	if opts.Cache == nil {
		opts.Cache = NewAttachmentCache(0, 0)
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 4
	}
	return &Orchestrator{imageProviders: opts.ImageProviders, audioTranscribers: opts.AudioTranscribers, videoDescribers: opts.VideoDescribers, cache: opts.Cache, maxConcurrent: opts.MaxConcurrent}
}
func (o *Orchestrator) Process(ctx context.Context, req MediaUnderstandingRequest) (*MediaUnderstandingResult, error) {
	if o == nil {
		return nil, fmt.Errorf("media understanding orchestrator is nil")
	}
	outputs, err := o.processBatch(ctx, req)
	if err != nil {
		return nil, err
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].AttachmentIndex < outputs[j].AttachmentIndex })
	return &MediaUnderstandingResult{Outputs: outputs}, nil
}
func (o *Orchestrator) processOne(ctx context.Context, idx int, att MediaAttachment, prompt, mode string) (MediaOutput, error) {
	switch {
	case att.IsImage():
		p, err := selectImageDescriber(o.imageProviders)
		if err != nil {
			return MediaOutput{}, err
		}
		img, err := ResolveImage(att)
		if err != nil {
			return MediaOutput{}, err
		}
		txt, err := p.DescribeImage(ctx, img, prompt)
		if err != nil {
			return MediaOutput{}, err
		}
		return MediaOutput{AttachmentIndex: idx, Type: outputTypeForMode(mode, "description"), Text: txt}, nil
	case att.IsAudio():
		p, err := selectTranscriber(o.audioTranscribers)
		if err != nil {
			return MediaOutput{}, err
		}
		data, mime, err := FetchAudioBytes(ctx, att)
		if err != nil {
			return MediaOutput{}, err
		}
		txt, err := p.Transcribe(ctx, data, mime)
		if err != nil {
			return MediaOutput{}, err
		}
		return MediaOutput{AttachmentIndex: idx, Type: "transcript", Text: txt}, nil
	case att.IsVideo():
		p, err := selectVideoDescriber(o.videoDescribers)
		if err != nil {
			return MediaOutput{}, err
		}
		txt, err := p.DescribeVideo(ctx, att, prompt)
		if err != nil {
			return MediaOutput{}, err
		}
		return MediaOutput{AttachmentIndex: idx, Type: outputTypeForMode(mode, "analysis"), Text: txt}, nil
	default:
		return MediaOutput{}, errUnsupported
	}
}
func outputTypeForMode(mode, fallback string) string {
	switch normalizeUnderstandingMode(mode) {
	case "describe":
		return "description"
	case "transcribe":
		return "transcript"
	case "analyze", "analyse":
		return "analysis"
	default:
		return fallback
	}
}

var errUnsupported = fmt.Errorf("unsupported media attachment type")
