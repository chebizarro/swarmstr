package channels

import (
	"fmt"

	gatewaychannels "metiq/internal/gateway/channels"
	"metiq/internal/plugins/runtime"
	"metiq/internal/plugins/sdk"
)

// BridgesFromLoadResult creates ChannelPlugin bridges for every channel
// capability registered by an OpenClaw plugin load result.
func BridgesFromLoadResult(host *runtime.OpenClawPluginHost, result runtime.OpenClawLoadResult) ([]sdk.ChannelPlugin, error) {
	var out []sdk.ChannelPlugin
	for _, reg := range result.Registrations {
		if reg.Type != "channel" {
			continue
		}
		if reg.PluginID == "" {
			reg.PluginID = result.PluginID
		}
		bridge, err := NewPluginChannelBridgeFromRegistration(host, reg)
		if err != nil {
			return nil, fmt.Errorf("channel %q: %w", reg.ID, err)
		}
		out = append(out, bridge)
	}
	return out, nil
}

// RegisterGatewayChannelBridges registers OpenClaw channel bridges with the
// existing gateway channel plugin registry, so channels.ConnectExtensions can
// connect configured OpenClaw channels exactly like native Go extensions.
func RegisterGatewayChannelBridges(host *runtime.OpenClawPluginHost, result runtime.OpenClawLoadResult) (int, error) {
	bridges, err := BridgesFromLoadResult(host, result)
	if err != nil {
		return 0, err
	}
	for _, bridge := range bridges {
		gatewaychannels.RegisterChannelPlugin(bridge)
	}
	return len(bridges), nil
}
