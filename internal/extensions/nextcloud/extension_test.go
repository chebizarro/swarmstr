package nextcloud

import (
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestPlugin_ID(t *testing.T) {
	p := &NextcloudPlugin{}
	if id := p.ID(); id != "nextcloud-talk" {
		t.Fatalf("expected nextcloud-talk, got %s", id)
	}
}

func TestPlugin_Type(t *testing.T) {
	p := &NextcloudPlugin{}
	if typ := p.Type(); typ != "Nextcloud Talk" {
		t.Fatalf("expected Nextcloud Talk, got %s", typ)
	}
}

func TestPlugin_ConfigSchema(t *testing.T) {
	p := &NextcloudPlugin{}
	schema := p.ConfigSchema()
	if schema == nil {
		t.Fatal("ConfigSchema must not be nil")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"base_url", "username", "app_password", "room_token"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing expected property %q", key)
		}
	}
}

func TestPlugin_Capabilities(t *testing.T) {
	p := &NextcloudPlugin{}
	caps := p.Capabilities()
	if !caps.Reactions {
		t.Error("expected Reactions capability")
	}
	if !caps.MultiAccount {
		t.Error("expected MultiAccount capability")
	}
}

func TestPlugin_GatewayMethods(t *testing.T) {
	p := &NextcloudPlugin{}
	if methods := p.GatewayMethods(); methods != nil {
		t.Errorf("expected nil, got %v", methods)
	}
}

func TestPlugin_ImplementsChannelPlugin(t *testing.T) {
	var _ sdk.ChannelPlugin = (*NextcloudPlugin)(nil)
}

func TestBotID(t *testing.T) {
	b := &nextcloudBot{channelID: "nc-1"}
	if b.ID() != "nc-1" {
		t.Errorf("expected nc-1, got %s", b.ID())
	}
}
