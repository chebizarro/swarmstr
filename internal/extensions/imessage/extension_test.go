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
	if !p.Capabilities().Reactions {
		t.Fatalf("expected reactions capability")
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
