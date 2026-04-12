package synology

import (
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &SynologyPlugin{}
	if id := p.ID(); id != "synology-chat" {
		t.Fatalf("expected synology-chat, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &SynologyPlugin{}
	if typ := p.Type(); typ != "Synology Chat" {
		t.Fatalf("expected Synology Chat, got %s", typ)
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &SynologyPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"webhook_url", "incoming_token"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &SynologyPlugin{}
	_ = p.Capabilities() // no panic; minimal capabilities
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &SynologyPlugin{}
	if methods := p.GatewayMethods(); methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*SynologyPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &synologyBot{channelID: "syn-1"}
	if b.ID() != "syn-1" {
		t.Errorf("expected syn-1, got %s", b.ID())
	}
}
