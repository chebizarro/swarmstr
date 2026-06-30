// Package imessage registers an iMessage channel id backed by BlueBubbles.
package imessage

import (
	"context"

	"metiq/internal/extensions/bluebubbles"
	"metiq/internal/plugins/sdk"
)

func init() {
	sdk.RegisterChannelConstructor("imessage", func() sdk.ChannelPlugin { return &IMessagePlugin{} })
}

// IMessagePlugin exposes the BlueBubbles iMessage transport under the
// user-facing "imessage" channel kind.
type IMessagePlugin struct {
	inner bluebubbles.BlueBubblesPlugin
}

func (p *IMessagePlugin) ID() string   { return "imessage" }
func (p *IMessagePlugin) Type() string { return "iMessage (BlueBubbles)" }

func (p *IMessagePlugin) ConfigSchema() map[string]any {
	schema := p.inner.ConfigSchema()
	if props, ok := schema["properties"].(map[string]any); ok {
		props["server_url"] = map[string]any{"type": "string", "description": "Base URL of the BlueBubbles server, e.g. http://192.168.1.10:1234."}
		props["password"] = map[string]any{"type": "string", "description": "BlueBubbles server password."}
		props["chat_guid"] = map[string]any{"type": "string", "description": "iMessage chat GUID, e.g. iMessage;-;+11234567890 or iMessage;+;chatroom-uuid."}
	}
	return schema
}

func (p *IMessagePlugin) Capabilities() sdk.ChannelCapabilities {
	return p.inner.Capabilities()
}

func (p *IMessagePlugin) GatewayMethods() []sdk.GatewayMethod { return nil }

func (p *IMessagePlugin) Connect(ctx context.Context, channelID string, cfg map[string]any, onMessage func(sdk.InboundChannelMessage)) (sdk.ChannelHandle, error) {
	return p.inner.Connect(ctx, channelID, cfg, onMessage)
}
