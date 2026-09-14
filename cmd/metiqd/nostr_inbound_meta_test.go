package main

import (
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/gateway/channels"
	nostrmeta "metiq/internal/nostr/metadata"
	nostruntime "metiq/internal/nostr/runtime"
)

func TestRenderInboundBlockOmitWhenEmptyOneToOneNIP17(t *testing.T) {
	// 1:1 NIP-17 DM — only self and sender as p-tags. After excluding
	// self and sender, no participants remain → protocol-only → omit.
	self := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sender := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tags := nostr.Tags{
		{"p", self},
		{"p", sender},
	}
	msg := nostruntime.InboundDM{
		FromPubKey: sender,
		Kind:       nostr.Kind(14),
		Tags:       tags,
		Scheme:     "nip17",
	}
	block := renderInboundBlock(msg, self)
	if block != "" {
		t.Errorf("expected empty block for 1:1 NIP-17, got: %s", block)
	}
}

func TestRenderInboundBlockMultiPartyNIP17(t *testing.T) {
	// Multi-party NIP-17 DM — three other participants after excluding
	// self and sender → participants + reply_scope → block.
	self := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sender := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	other1 := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	other2 := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	other3 := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	tags := nostr.Tags{
		{"p", self},
		{"p", sender},
		{"p", other1},
		{"p", other2},
		{"p", other3},
	}
	msg := nostruntime.InboundDM{
		FromPubKey: sender,
		Kind:       nostr.Kind(14),
		Tags:       tags,
		Scheme:     "nip17",
	}
	block := renderInboundBlock(msg, self)
	if block == "" {
		t.Fatal("expected non-empty block for multi-party NIP-17")
	}
	if !strings.Contains(block, "```json") {
		t.Errorf("block missing json fence: %s", block)
	}
	if !strings.Contains(block, "reply_scope") {
		t.Errorf("multi-party block missing reply_scope: %s", block)
	}
	if !strings.Contains(block, "\"participants\"") {
		t.Errorf("multi-party block missing participants: %s", block)
	}
}

func TestRenderInboundBlockDeletionReturnsEmpty(t *testing.T) {
	self := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sender := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	msg := nostruntime.InboundDM{
		FromPubKey: sender,
		Kind:       nostr.Kind(5), // KindDeletion
		Tags:       nostr.Tags{{"e", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"}},
		Scheme:     "nip17",
	}
	block := renderInboundBlock(msg, self)
	if block != "" {
		t.Errorf("expected empty block for deletion, got: %s", block)
	}
}

func TestRenderInboundBlockReactionMinimal(t *testing.T) {
	self := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sender := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	parent := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	tags := nostr.Tags{
		{"e", parent},
		{"p", sender},
	}
	msg := nostruntime.InboundDM{
		FromPubKey: sender,
		Kind:       nostr.Kind(7), // KindReaction
		Tags:       tags,
		Scheme:     "nip17",
	}
	block := renderInboundBlock(msg, self)
	if block == "" {
		t.Fatal("expected non-empty block for reaction")
	}
	// Reaction should have protocol + thread.parent, nothing else.
	if strings.Contains(block, "participants") {
		t.Errorf("reaction should not carry participants: %s", block)
	}
	if strings.Contains(block, "subject") {
		t.Errorf("reaction should not carry subject: %s", block)
	}
	if strings.Contains(block, "attachments") {
		t.Errorf("reaction should not carry attachments: %s", block)
	}
}

func TestRenderInboundBlockKind15HasAttachments(t *testing.T) {
	self := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sender := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sha256 := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	tags := nostr.Tags{
		{"imeta", "url https://example.com/photo.jpg", "m image/jpeg",
			"x " + sha256, "size 2048", "dim 800x600"},
		{"p", sender},
	}
	msg := nostruntime.InboundDM{
		FromPubKey: sender,
		Kind:       nostr.Kind(15), // KindFileMessage
		Tags:       tags,
		Scheme:     "nip17",
		Text:       "check out this photo",
	}
	block := renderInboundBlock(msg, self)
	if block == "" {
		t.Fatal("expected non-empty block for kind:15")
	}
	if !strings.Contains(block, "attachments") {
		t.Errorf("kind:15 block missing attachments: %s", block)
	}
}

func TestRenderRoomInboundBlockNIP29Reply(t *testing.T) {
	bot := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sender := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rootID := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	parentID := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	msg := channels.InboundMessage{
		Protocol:  nostrmeta.ProtocolNIP29,
		FromPubKey: sender,
		Text:      "hello in the group!",
		EventID:   "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Tags: []nostr.Tag{
			{"e", rootID, "", "root"},
			{"e", parentID, "", "reply"},
			{"p", bot},
			{"p", sender},
		},
	}
	block := renderRoomInboundBlock(msg, bot)
	if block == "" {
		t.Fatal("expected non-empty block for NIP-29 reply")
	}
	if !strings.Contains(block, "```json") {
		t.Errorf("block missing json fence: %s", block)
	}
	if !strings.Contains(block, "thread") {
		t.Errorf("NIP-29 reply block missing thread: %s", block)
	}
}

func TestRenderRoomInboundBlockNIP28(t *testing.T) {
	bot := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sender := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	parent := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	msg := channels.InboundMessage{
		Protocol:   nostrmeta.ProtocolNIP28,
		FromPubKey: sender,
		Text:       "hello in public channel",
		EventID:    "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Tags:       []nostr.Tag{{"e", parent}},
	}
	block := renderRoomInboundBlock(msg, bot)
	if block == "" {
		t.Fatal("expected non-empty block for NIP-28 message")
	}
	if !strings.Contains(block, "```json") {
		t.Errorf("block missing json fence: %s", block)
	}
}

func TestRenderRoomInboundBlockEmptyWhenProtocolEmpty(t *testing.T) {
	msg := channels.InboundMessage{
		Text:       "no protocol set",
		FromPubKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	block := renderRoomInboundBlock(msg, "")
	if block != "" {
		t.Errorf("expected empty when protocol empty, got: %s", block)
	}
}