package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"metiq/internal/plugins/sdk"
)

// fakeTextChannel implements only the base sdk.ChannelHandle contract.
type fakeTextChannel struct {
	sent []string
}

func (f *fakeTextChannel) ID() string { return "fake" }
func (f *fakeTextChannel) Send(_ context.Context, text string) error {
	f.sent = append(f.sent, text)
	return nil
}
func (f *fakeTextChannel) Close() {}

// fakeMediaChannel implements sdk.MediaHandle.
type fakeMediaChannel struct {
	fakeTextChannel
	mediaErr error
	payloads []sdk.DirectTextMediaPayload
}

func (f *fakeMediaChannel) SendMedia(_ context.Context, payload sdk.DirectTextMediaPayload) error {
	if f.mediaErr != nil {
		return f.mediaErr
	}
	f.payloads = append(f.payloads, payload)
	return nil
}

// fakeAudioChannel implements sdk.AudioHandle.
type fakeAudioChannel struct {
	fakeTextChannel
	audioErr error
	audio    [][]byte
	formats  []string
}

func (f *fakeAudioChannel) SendAudio(_ context.Context, audio []byte, format string) error {
	if f.audioErr != nil {
		return f.audioErr
	}
	f.audio = append(f.audio, audio)
	f.formats = append(f.formats, format)
	return nil
}

// fakeMediaAudioChannel implements both sdk.MediaHandle and sdk.AudioHandle.
type fakeMediaAudioChannel struct {
	fakeMediaChannel
	fakeAudioChannel
}

func (f *fakeMediaAudioChannel) ID() string                                 { return "fake" }
func (f *fakeMediaAudioChannel) Send(ctx context.Context, text string) error { return f.fakeMediaChannel.Send(ctx, text) }
func (f *fakeMediaAudioChannel) Close()                                     {}

var (
	_ sdk.MediaHandle = (*fakeMediaChannel)(nil)
	_ sdk.AudioHandle = (*fakeAudioChannel)(nil)
	_ sdk.MediaHandle = (*fakeMediaAudioChannel)(nil)
	_ sdk.AudioHandle = (*fakeMediaAudioChannel)(nil)
)

func writeTempMedia(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDispatchChannelMediaReplyPrefersMediaHandle(t *testing.T) {
	ch := &fakeMediaChannel{}
	res := dispatchChannelMediaReply(context.Background(), ch, "user-1", "/tmp/pic.png")
	if !res.sent || res.method != "media" {
		t.Fatalf("expected sent via media, got %+v", res)
	}
	if res.err != nil {
		t.Fatalf("unexpected err: %v", res.err)
	}
	if len(ch.payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(ch.payloads))
	}
	p := ch.payloads[0]
	if p.To != "user-1" {
		t.Errorf("expected To=user-1, got %q", p.To)
	}
	if len(p.Media) != 1 || p.Media[0].Path != "/tmp/pic.png" {
		t.Errorf("unexpected media refs: %+v", p.Media)
	}
}

func TestDispatchChannelMediaReplyMediaHandlePreferredOverAudio(t *testing.T) {
	path := writeTempMedia(t, "reply.mp3", []byte("mp3bytes"))
	ch := &fakeMediaAudioChannel{}
	res := dispatchChannelMediaReply(context.Background(), ch, "user-1", path)
	if !res.sent || res.method != "media" {
		t.Fatalf("expected sent via media, got %+v", res)
	}
	if len(ch.fakeAudioChannel.audio) != 0 {
		t.Errorf("SendAudio should not be called when SendMedia succeeds")
	}
}

func TestDispatchChannelMediaReplyAudioFallbackWhenMediaFails(t *testing.T) {
	path := writeTempMedia(t, "reply.ogg", []byte("oggbytes"))
	ch := &fakeMediaAudioChannel{}
	ch.fakeMediaChannel.mediaErr = errors.New("boom")
	res := dispatchChannelMediaReply(context.Background(), ch, "user-1", path)
	if !res.sent || res.method != "audio" {
		t.Fatalf("expected audio fallback, got %+v", res)
	}
	if res.err == nil {
		t.Errorf("expected media failure recorded in err")
	}
	if len(ch.fakeAudioChannel.audio) != 1 || string(ch.fakeAudioChannel.audio[0]) != "oggbytes" {
		t.Fatalf("unexpected audio sends: %v", ch.fakeAudioChannel.audio)
	}
	if ch.fakeAudioChannel.formats[0] != "ogg" {
		t.Errorf("expected format ogg, got %q", ch.fakeAudioChannel.formats[0])
	}
}

func TestDispatchChannelMediaReplyAudioOnlyChannel(t *testing.T) {
	path := writeTempMedia(t, "reply.wav", []byte("wavbytes"))
	ch := &fakeAudioChannel{}
	res := dispatchChannelMediaReply(context.Background(), ch, "user-1", path)
	if !res.sent || res.method != "audio" {
		t.Fatalf("expected sent via audio, got %+v", res)
	}
	if ch.formats[0] != "wav" {
		t.Errorf("expected format wav, got %q", ch.formats[0])
	}
}

func TestDispatchChannelMediaReplyNoAudioFallbackForNonAudioMedia(t *testing.T) {
	// Audio-only channel must not receive an image through SendAudio.
	ch := &fakeAudioChannel{}
	res := dispatchChannelMediaReply(context.Background(), ch, "user-1", "/tmp/pic.png")
	if res.sent {
		t.Fatalf("expected not sent, got %+v", res)
	}
	if len(ch.audio) != 0 {
		t.Errorf("SendAudio should not be called for non-audio media")
	}
}

func TestDispatchChannelMediaReplyTextOnlyChannel(t *testing.T) {
	ch := &fakeTextChannel{}
	res := dispatchChannelMediaReply(context.Background(), ch, "user-1", "/tmp/pic.png")
	if res.sent {
		t.Fatalf("expected not sent, got %+v", res)
	}
	if res.err != nil {
		t.Fatalf("no delivery attempted, expected nil err, got %v", res.err)
	}
	if len(ch.sent) != 0 {
		t.Errorf("text Send must be left to the caller fallback")
	}
}

func TestDispatchChannelMediaReplyMediaFailureNonAudio(t *testing.T) {
	ch := &fakeMediaChannel{mediaErr: errors.New("rejected")}
	res := dispatchChannelMediaReply(context.Background(), ch, "user-1", "/tmp/doc.pdf")
	if res.sent {
		t.Fatalf("expected not sent, got %+v", res)
	}
	if res.err == nil {
		t.Fatal("expected err from failed SendMedia")
	}
}

func TestDispatchChannelMediaReplyAudioReadError(t *testing.T) {
	ch := &fakeAudioChannel{}
	res := dispatchChannelMediaReply(context.Background(), ch, "user-1", filepath.Join(t.TempDir(), "missing.mp3"))
	if res.sent {
		t.Fatalf("expected not sent, got %+v", res)
	}
	if res.err == nil {
		t.Fatal("expected read error")
	}
	if len(ch.audio) != 0 {
		t.Errorf("SendAudio should not be called on read error")
	}
}

func TestDispatchChannelMediaReplyNilHandle(t *testing.T) {
	res := dispatchChannelMediaReply(context.Background(), nil, "user-1", "/tmp/pic.png")
	if res.sent || res.err != nil {
		t.Fatalf("expected inert result for nil handle, got %+v", res)
	}
}

func TestMediaReplyFallbackText(t *testing.T) {
	if got := mediaReplyFallbackText("/tmp/voice.mp3"); got != "[audio generated] /tmp/voice.mp3" {
		t.Errorf("unexpected audio fallback: %q", got)
	}
	if got := mediaReplyFallbackText("/tmp/pic.png"); got != "[media generated] /tmp/pic.png" {
		t.Errorf("unexpected media fallback: %q", got)
	}
}
