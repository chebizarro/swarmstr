package sdk

import (
	"context"
	"testing"
)

type capabilityBaseHandle struct{}

func (capabilityBaseHandle) ID() string                         { return "test" }
func (capabilityBaseHandle) Send(context.Context, string) error { return nil }
func (capabilityBaseHandle) Close()                             {}

type capabilityFullHandle struct{ capabilityBaseHandle }

func (capabilityFullHandle) SendTyping(context.Context, int) error                   { return nil }
func (capabilityFullHandle) AddReaction(context.Context, string, string) error       { return nil }
func (capabilityFullHandle) RemoveReaction(context.Context, string, string) error    { return nil }
func (capabilityFullHandle) SendReply(context.Context, string, string) error         { return nil }
func (capabilityFullHandle) SendInThread(context.Context, string, string) error      { return nil }
func (capabilityFullHandle) SendAudio(context.Context, []byte, string) error         { return nil }
func (capabilityFullHandle) EditMessage(context.Context, string, string) error       { return nil }
func (capabilityFullHandle) SendMedia(context.Context, DirectTextMediaPayload) error { return nil }

func TestValidateChannelCapabilityContract(t *testing.T) {
	all := ChannelCapabilities{
		Typing: true, Reactions: true, Reply: true, Threads: true,
		Audio: true, Edit: true, Media: true, DirectTextMedia: true,
	}
	if err := ValidateChannelCapabilityContract(all, capabilityFullHandle{}); err != nil {
		t.Fatalf("full handle rejected: %v", err)
	}
	checks := []struct {
		name string
		caps ChannelCapabilities
	}{
		{"typing", ChannelCapabilities{Typing: true}},
		{"reactions", ChannelCapabilities{Reactions: true}},
		{"reply", ChannelCapabilities{Reply: true}},
		{"threads", ChannelCapabilities{Threads: true}},
		{"audio", ChannelCapabilities{Audio: true}},
		{"edit", ChannelCapabilities{Edit: true}},
		{"media", ChannelCapabilities{Media: true}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateChannelCapabilityContract(tc.caps, capabilityBaseHandle{}); err == nil {
				t.Fatal("dishonest capability accepted")
			}
		})
	}
	if err := ValidateChannelCapabilityContract(ChannelCapabilities{DirectTextMedia: true}, capabilityFullHandle{}); err == nil {
		t.Fatal("direct text/media accepted without base media capability")
	}
}
