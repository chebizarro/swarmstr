package qqbot

import (
	"encoding/json"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPluginCapabilities(t *testing.T) {
	p := &QQBotPlugin{}
	if p.ID() != "qqbot" {
		t.Fatalf("expected qqbot, got %s", p.ID())
	}
	caps := p.Capabilities()
	if !caps.Typing || !caps.MultiAccount {
		t.Fatalf("expected typing and multi-account capabilities, got %+v", caps)
	}
	var _ sdk.ChannelPlugin = p
}

func TestNormalizeQQGatewayMessageC2C(t *testing.T) {
	raw := json.RawMessage(`{"id":"m1","content":" hello ","timestamp":"2026-06-30T12:00:00Z","author":{"user_openid":"u1"},"attachments":[{"url":"https://cdn.example/a.png","content_type":"image/png"}]}`)
	msg, ok := normalizeQQGatewayMessage("qq-1", "C2C_MESSAGE_CREATE", raw)
	if !ok {
		t.Fatal("expected message")
	}
	if msg.ChannelID != "qq-1" || msg.SenderID != "u1" || msg.Text != "hello" || msg.EventID != "m1" {
		t.Fatalf("unexpected normalized message: %+v", msg)
	}
	if msg.ThreadID != "c2c:u1" || msg.MediaMIME != "image/png" || msg.MediaURL == "" {
		t.Fatalf("unexpected routing/media: %+v", msg)
	}
	if msg.CreatedAt != 1782820800 {
		t.Fatalf("unexpected created_at: %d", msg.CreatedAt)
	}
}

func TestNormalizeQQGatewayMessageGroup(t *testing.T) {
	raw := json.RawMessage(`{"id":"m2","content":"hi group","group_openid":"g1","author":{"member_openid":"member1","username":"Alice"}}`)
	msg, ok := normalizeQQGatewayMessage("qq-1", "GROUP_AT_MESSAGE_CREATE", raw)
	if !ok {
		t.Fatal("expected message")
	}
	if msg.SenderID != "member1" || msg.ThreadID != "group:g1" {
		t.Fatalf("unexpected group message: %+v", msg)
	}
}

func TestNormalizeQQGatewayMessageIgnoresUnsupported(t *testing.T) {
	if _, ok := normalizeQQGatewayMessage("qq-1", "READY", json.RawMessage(`{}`)); ok {
		t.Fatal("expected unsupported event to be ignored")
	}
}

func TestQQMessagePath(t *testing.T) {
	cases := map[string]string{
		"c2c":     "/v2/users/abc/messages",
		"group":   "/v2/groups/abc/messages",
		"dm":      "/dms/abc/messages",
		"channel": "/channels/abc/messages",
	}
	for typ, want := range cases {
		if got := qqMessagePath(typ, "abc"); got != want {
			t.Fatalf("%s path: got %s want %s", typ, got, want)
		}
	}
}
