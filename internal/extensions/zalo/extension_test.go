package zalo

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &ZaloPlugin{}
	if id := p.ID(); id != "zalo" {
		t.Fatalf("expected zalo, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &ZaloPlugin{}
	if typ := p.Type(); typ != "Zalo OA" {
		t.Fatalf("expected Zalo OA, got %s", typ)
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &ZaloPlugin{}
	schema := p.ConfigSchema()
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"app_id", "app_secret", "refresh_token", "oa_id"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &ZaloPlugin{}
	_ = p.Capabilities() // no panic
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &ZaloPlugin{}
	if methods := p.GatewayMethods(); methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*ZaloPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &zaloBot{channelID: "zalo-1"}
	if b.ID() != "zalo-1" {
		t.Errorf("expected zalo-1, got %s", b.ID())
	}
}

func TestBotGetToken_Empty(t *testing.T) {
	b := &zaloBot{}
	if tok := b.getToken(); tok != "" {
		t.Errorf("expected empty token, got %q", tok)
	}
}

func TestVerifySignature_Valid(t *testing.T) {
	secret := "test-app-secret"
	body := []byte(`{"event_name":"user_send_text"}`)

	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	mac := hex.EncodeToString(h.Sum(nil))

	b := &zaloBot{appSecret: secret}
	if !b.verifySignature(body, mac) {
		t.Error("expected valid signature")
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	b := &zaloBot{appSecret: "secret"}
	if b.verifySignature([]byte("body"), "deadbeef") {
		t.Error("expected invalid signature")
	}
}
