package media

import (
	"context"
	"encoding/base64"
	"sync/atomic"
	"testing"
	"time"
)

type imageDesc struct{ calls atomic.Int32 }

func (d *imageDesc) Configured() bool { return true }
func (d *imageDesc) DescribeImage(context.Context, ImageRef, string) (string, error) {
	d.calls.Add(1)
	return "image desc", nil
}

type audioTx struct{ calls atomic.Int32 }

func (a *audioTx) Configured() bool { return true }
func (a *audioTx) Transcribe(context.Context, []byte, string) (string, error) {
	a.calls.Add(1)
	return "audio text", nil
}

type videoDesc struct{ calls atomic.Int32 }

func (v *videoDesc) Configured() bool { return true }
func (v *videoDesc) DescribeVideo(context.Context, MediaAttachment, string) (string, error) {
	v.calls.Add(1)
	return "video desc", nil
}
func TestOrchestratorRoutesCachesAndDedupes(t *testing.T) {
	img := &imageDesc{}
	aud := &audioTx{}
	vid := &videoDesc{}
	cache := NewAttachmentCache(time.Hour, 10)
	o := NewOrchestrator(OrchestratorOptions{ImageProviders: []ImageDescriber{img}, AudioTranscribers: []Transcriber{aud}, VideoDescribers: []VideoDescriber{vid}, Cache: cache, MaxConcurrent: 2})
	imgB64 := base64.StdEncoding.EncodeToString([]byte("img"))
	audB64 := base64.StdEncoding.EncodeToString([]byte("aud"))
	res, err := o.Process(context.Background(), MediaUnderstandingRequest{Prompt: "what", Mode: "analyze", Attachments: []MediaAttachment{{Type: "image", Base64: imgB64, MimeType: "image/png"}, {Type: "image", Base64: imgB64, MimeType: "image/png"}, {Type: "audio", Base64: audB64, MimeType: "audio/wav"}, {Type: "video", URL: "https://example.invalid/v.mp4", MimeType: "video/mp4"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outputs) != 4 {
		t.Fatalf("outputs=%#v", res.Outputs)
	}
	if img.calls.Load() != 1 {
		t.Fatalf("image calls=%d", img.calls.Load())
	}
	if aud.calls.Load() != 1 || vid.calls.Load() != 1 {
		t.Fatalf("calls aud=%d vid=%d", aud.calls.Load(), vid.calls.Load())
	}
	_, err = o.Process(context.Background(), MediaUnderstandingRequest{Prompt: "what", Mode: "analyze", Attachments: []MediaAttachment{{Type: "image", Base64: imgB64, MimeType: "image/png"}}})
	if err != nil {
		t.Fatal(err)
	}
	if img.calls.Load() != 1 {
		t.Fatal("expected cache hit")
	}
}
