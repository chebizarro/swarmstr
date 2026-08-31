package whatsapp

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

// TestWhatsAppCapabilitiesAudioFalse asserts the plugin no longer advertises the
// raw-audio capability, since SendAudio always returns an unsupported error.
func TestWhatsAppCapabilitiesAudioFalse(t *testing.T) {
	caps := (&WhatsAppPlugin{}).Capabilities()
	if caps.Audio {
		t.Fatal("whatsapp must not advertise Audio capability; raw SendAudio is unsupported")
	}
	if caps.Threads || !caps.Reply {
		t.Fatalf("WhatsApp must advertise message replies without claiming threads: %+v", caps)
	}
	var handle sdk.ChannelHandle = &whatsappBot{}
	if _, ok := handle.(sdk.ThreadHandle); ok {
		t.Fatal("WhatsApp handle must not satisfy ThreadHandle")
	}
	if _, ok := handle.(sdk.ReplyHandle); !ok {
		t.Fatal("WhatsApp handle must satisfy ReplyHandle")
	}
	if err := sdk.ValidateChannelCapabilityContract(caps, handle); err != nil {
		t.Fatalf("capability contract: %v", err)
	}
}

// TestWhatsAppSendAudioReturnsError asserts SendAudio fails loudly.
func TestWhatsAppSendAudioReturnsError(t *testing.T) {
	b := &whatsappBot{channelID: "wa-main"}
	if err := b.SendAudio(context.Background(), []byte("x"), "ogg"); err == nil {
		t.Fatal("expected SendAudio to return an unsupported error")
	}
}

// TestWhatsAppSendTypingWithoutInboundErrors asserts SendTyping fails explicitly
// when there is no prior inbound message to target, instead of silently
// returning nil (which advertised a working typing indicator that never fired).
func TestWhatsAppSendTypingWithoutInboundErrors(t *testing.T) {
	b := &whatsappBot{channelID: "wa-main"}
	err := b.SendTyping(context.Background(), 0)
	if err == nil {
		t.Fatal("expected SendTyping to error without a prior inbound message id")
	}
	if !strings.Contains(err.Error(), "prior inbound") {
		t.Fatalf("expected prior-inbound error, got: %v", err)
	}
}
