package discord

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

// TestDiscordCapabilitiesAudioFalse asserts the plugin does not advertise raw
// audio delivery, which it cannot honestly provide.
func TestDiscordCapabilitiesAudioFalse(t *testing.T) {
	caps := (&DiscordPlugin{}).Capabilities()
	if caps.Audio {
		t.Fatal("discord must not advertise Audio capability; SendAudio is unsupported")
	}
}

// TestDiscordSendAudioReturnsExplicitError asserts SendAudio fails loudly instead
// of silently discarding the audio bytes and posting a text placeholder.
func TestDiscordSendAudioReturnsExplicitError(t *testing.T) {
	b := &discordBot{channelID: "discord-main", discordChannelID: "ch-123"}
	err := b.SendAudio(context.Background(), []byte("audio-bytes"), "mp3")
	if err == nil {
		t.Fatal("expected explicit unsupported error from SendAudio, got nil")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected unsupported error, got: %v", err)
	}
	// The handle must still satisfy the AudioHandle interface (so the dispatcher
	// can surface the explicit error) even though the capability is off.
	var _ sdk.AudioHandle = b
}
