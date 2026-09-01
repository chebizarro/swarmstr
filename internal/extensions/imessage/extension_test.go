package imessage

import (
	"testing"

	"metiq/internal/plugins/sdk"
)

func TestIMessagePluginAlias(t *testing.T) {
	p := &IMessagePlugin{}
	if p.ID() != "imessage" {
		t.Fatalf("expected imessage, got %s", p.ID())
	}
	if p.Type() != "iMessage (BlueBubbles)" {
		t.Fatalf("unexpected type: %s", p.Type())
	}
	caps := p.Capabilities()
	if !caps.Reactions || !caps.Media || !caps.DirectTextMedia || !caps.MultiAccount {
		t.Fatalf("expected reactions, media, direct text/media, and multi-account capabilities: %+v", caps)
	}
	methods := p.GatewayMethods()
	wantMethods := []string{"imessage.send", "imessage.add_reaction", "imessage.remove_reaction"}
	if len(methods) != len(wantMethods) {
		t.Fatalf("expected %d methods, got %d", len(wantMethods), len(methods))
	}
	for i, want := range wantMethods {
		if methods[i].Method != want {
			t.Fatalf("method %d: want %q, got %q", i, want, methods[i].Method)
		}
	}
	var _ sdk.ChannelPlugin = p
}

func TestIMessageConfigSchemaMatchesBlueBubblesConfig(t *testing.T) {
	schema := (&IMessagePlugin{}).ConfigSchema()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("expected required []string, got %T", schema["required"])
	}
	want := map[string]bool{"server_url": false, "password": false, "chat_guid": false}
	for _, key := range required {
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Fatalf("required field %q missing from schema", key)
		}
	}
}
