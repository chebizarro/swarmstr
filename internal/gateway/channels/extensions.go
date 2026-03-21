// Package channels — extension registry for channel plugins.
//
// Built-in channel extensions (telegram, discord, etc.) register themselves via
// RegisterChannelPlugin() in their package init() functions. The daemon wires
// the registry into the DM bus on startup.
//
// Plugin registration flow:
//
//	1. Extension package init() calls RegisterChannelPlugin(plugin).
//	2. Daemon's main.go blank-imports the extension packages.
//	3. startChannelExtensions() is called during startup; it reads the live
//	   config, finds any nostr_channels entries whose "kind" matches a
//	   registered plugin ID, and calls plugin.Connect() for each.
//	4. Messages from connected channels are forwarded to the DM bus via the
//	   onMessage callback.

package channels

import (
	"context"
	"fmt"
	"log"
	"sync"

	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

// ─── Global plugin registry ───────────────────────────────────────────────────

var (
	pluginMu    sync.RWMutex
	pluginsByID = map[string]sdk.ChannelPlugin{}
	pluginOrder []string
)

// RegisterChannelPlugin adds a ChannelPlugin to the global registry.
// Call this from an extension's init() function.
// Panics if a plugin with the same ID has already been registered.
func RegisterChannelPlugin(p sdk.ChannelPlugin) {
	pluginMu.Lock()
	defer pluginMu.Unlock()
	if _, ok := pluginsByID[p.ID()]; ok {
		panic(fmt.Sprintf("channel plugin %q already registered", p.ID()))
	}
	pluginsByID[p.ID()] = p
	pluginOrder = append(pluginOrder, p.ID())
	log.Printf("channel plugin registered: %s (%s)", p.ID(), p.Type())
}

// ListChannelPlugins returns all registered channel plugins in registration order.
func ListChannelPlugins() []sdk.ChannelPlugin {
	pluginMu.RLock()
	defer pluginMu.RUnlock()
	out := make([]sdk.ChannelPlugin, 0, len(pluginOrder))
	for _, id := range pluginOrder {
		out = append(out, pluginsByID[id])
	}
	return out
}

// GetChannelPlugin looks up a registered channel plugin by ID.
func GetChannelPlugin(id string) (sdk.ChannelPlugin, bool) {
	pluginMu.RLock()
	defer pluginMu.RUnlock()
	p, ok := pluginsByID[id]
	return p, ok
}

// ─── Extension channel handles ────────────────────────────────────────────────

// ExtensionHandle adapts an sdk.ChannelHandle to the Channel interface.
type ExtensionHandle struct {
	handle sdk.ChannelHandle
}

func (e *ExtensionHandle) ID() string   { return e.handle.ID() }
func (e *ExtensionHandle) Type() string { return "extension" }
func (e *ExtensionHandle) Send(ctx context.Context, text string) error {
	return e.handle.Send(ctx, text)
}
func (e *ExtensionHandle) Close() { e.handle.Close() }

// ─── Extension startup ────────────────────────────────────────────────────────

// ExtensionConnectResult holds a connected extension channel.
type ExtensionConnectResult struct {
	PluginID  string
	ChannelID string
	Handle    Channel
	// RawHandle is the underlying sdk.ChannelHandle returned by the plugin.
	// Callers can perform interface assertions (e.g. sdk.TypingHandle) on it
	// to access optional channel features.
	RawHandle sdk.ChannelHandle
	// Capabilities is the declared feature set for this channel instance.
	// It is populated when the plugin implements sdk.ChannelPluginWithCapabilities.
	Capabilities sdk.ChannelCapabilities
}

// ConnectExtensions reads the live config, finds nostr_channels entries whose
// "kind" matches a registered plugin ID, and starts each one.
// onMessage is called for each message received from any extension channel.
func ConnectExtensions(
	ctx context.Context,
	cfg state.ConfigDoc,
	onMessage func(sdk.InboundChannelMessage),
) ([]ExtensionConnectResult, error) {
	pluginMu.RLock()
	defer pluginMu.RUnlock()

	if len(pluginsByID) == 0 {
		return nil, nil
	}

	var results []ExtensionConnectResult

	for channelID, chanCfg := range cfg.NostrChannels {
		plugin, ok := pluginsByID[chanCfg.Kind]
		if !ok {
			// Not a registered extension kind — handled by native channel code.
			continue
		}

		// Serialize the NostrChannelConfig to a map so plugins get all fields.
		entryCfg := channelConfigToMap(chanCfg)

		log.Printf("connecting extension channel: %s (kind=%s)", channelID, chanCfg.Kind)
		handle, err := plugin.Connect(ctx, channelID, entryCfg, onMessage)
		if err != nil {
			log.Printf("extension channel %s connect error: %v", channelID, err)
			continue
		}

		var caps sdk.ChannelCapabilities
		if cp, ok := plugin.(sdk.ChannelPluginWithCapabilities); ok {
			caps = cp.Capabilities()
		}
		results = append(results, ExtensionConnectResult{
			PluginID:     chanCfg.Kind,
			ChannelID:    channelID,
			Handle:       &ExtensionHandle{handle: handle},
			RawHandle:    handle,
			Capabilities: caps,
		})
	}

	return results, nil
}

// channelConfigToMap serialises a NostrChannelConfig to a plain map
// so it can be passed generically to channel plugins.
func channelConfigToMap(c state.NostrChannelConfig) map[string]any {
	m := map[string]any{
		"kind":    c.Kind,
		"enabled": c.Enabled,
	}
	if c.GroupAddress != "" {
		m["group_address"] = c.GroupAddress
	}
	if c.ChannelID != "" {
		m["channel_id"] = c.ChannelID
	}
	if len(c.Relays) > 0 {
		m["relays"] = c.Relays
	}
	if c.AgentID != "" {
		m["agent_id"] = c.AgentID
	}
	if len(c.Tags) > 0 {
		m["tags"] = c.Tags
	}
	// Merge extension-specific config on top (allows token, webhook_url, etc.)
	for k, v := range c.Config {
		m[k] = v
	}
	return m
}

// ExtraGatewayMethods collects all gateway methods contributed by registered
// channel plugins that implement ChannelPluginWithMethods.
func ExtraGatewayMethods() []sdk.GatewayMethod {
	pluginMu.RLock()
	defer pluginMu.RUnlock()
	var methods []sdk.GatewayMethod
	for _, p := range pluginsByID {
		if wp, ok := p.(sdk.ChannelPluginWithMethods); ok {
			methods = append(methods, wp.GatewayMethods()...)
		}
	}
	return methods
}
