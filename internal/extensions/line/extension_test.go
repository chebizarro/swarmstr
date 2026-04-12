package line

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &LINEPlugin{}
	if id := p.ID(); id != "line" {
		t.Fatalf("expected line, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &LINEPlugin{}
	if typ := p.Type(); typ != "LINE" {
		t.Fatalf("expected LINE, got %s", typ)
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &LINEPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"channel_access_token", "channel_secret"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &LINEPlugin{}
	caps := p.Capabilities()
	if !caps.MultiAccount {
		t.Error("expected MultiAccount capability")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &LINEPlugin{}
	if methods := p.GatewayMethods(); methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*LINEPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &lineBot{channelID: "line-123"}
	if b.ID() != "line-123" {
		t.Errorf("expected line-123, got %s", b.ID())
	}
}

func TestVerifySignature_Valid(t *testing.T) {
	secret := "test-secret"
	body := []byte(`{"events":[]}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	b := &lineBot{channelSecret: secret}
	if !b.verifySignature(body, sig) {
		t.Error("expected valid signature")
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	b := &lineBot{channelSecret: "secret"}
	if b.verifySignature([]byte("body"), "badsig") {
		t.Error("expected invalid signature")
	}
}
