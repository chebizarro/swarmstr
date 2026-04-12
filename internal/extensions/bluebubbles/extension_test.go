package bluebubbles

import (
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &BlueBubblesPlugin{}
	if id := p.ID(); id == "" {
		t.Fatal("ID must not be empty")
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &BlueBubblesPlugin{}
	if typ := p.Type(); typ == "" {
		t.Fatal("Type must not be empty")
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &BlueBubblesPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"server_url", "password", "chat_guid"} {
		if _, ok := props[key]; !ok {
			t.Errorf("ConfigSchema missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &BlueBubblesPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions {
		t.Error("expected Reactions capability")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &BlueBubblesPlugin{}
	methods := p.GatewayMethods()
	if methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*BlueBubblesPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &bbBot{channelID: "test-ch"}
	if b.ID() != "test-ch" {
		t.Errorf("expected test-ch, got %s", b.ID())
	}
}
