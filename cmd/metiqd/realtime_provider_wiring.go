package main

import (
	"context"
	"errors"
	"fmt"

	"metiq/internal/plugins/registry"
	"metiq/internal/realtimestt"
	"metiq/internal/realtimevoice"
)

const realtimeVoiceCapabilityType = "voice_provider"

// realtimePluginInvoker is the provider invocation shape exposed by the
// OpenClaw host and consumed by the realtime provider adapters.
type realtimePluginInvoker interface {
	InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error)
}

// nodeCapabilityInvoker is the richer invocation shape exposed by the node
// plugin manager. A provider-specific adapter below binds the plugin and
// capability identities so it also satisfies realtimePluginInvoker.
type nodeCapabilityInvoker interface {
	InvokeProvider(ctx context.Context, pluginID, capabilityType, providerID, method string, args map[string]any) (any, error)
}

type boundNodeCapabilityInvoker struct {
	host           nodeCapabilityInvoker
	pluginID       string
	capabilityType string
}

func (i boundNodeCapabilityInvoker) InvokeProvider(ctx context.Context, providerID, method string, params any) (any, error) {
	if i.host == nil {
		return nil, fmt.Errorf("node provider %q has no plugin host", providerID)
	}
	args := map[string]any{}
	if params != nil {
		var ok bool
		args, ok = params.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("node provider %q parameters have type %T, want map[string]any", providerID, params)
		}
	}
	return i.host.InvokeProvider(ctx, i.pluginID, i.capabilityType, providerID, method, args)
}

// newDaemonRealtimeProviderRegistries assembles the native and plugin-backed
// realtime providers used by talk.session.* and talk.client.*. Registration
// failures are returned together while valid providers remain available.
func newDaemonRealtimeProviderRegistries(
	unified *registry.UnifiedRegistry,
	openClaw realtimePluginInvoker,
	node nodeCapabilityInvoker,
) (*realtimevoice.Registry, *realtimestt.Registry, error) {
	voice := realtimevoice.NewRegistry()
	stt := realtimestt.NewRegistry()
	var registrationErrors []error

	for _, provider := range []realtimevoice.Provider{
		realtimevoice.NewOpenAIRealtimeProvider(),
		realtimevoice.NewElevenLabsRealtimeProvider(),
	} {
		if err := voice.Register(provider); err != nil {
			registrationErrors = append(registrationErrors, err)
		}
	}
	for _, provider := range []realtimestt.Provider{
		realtimestt.NewDeepgramProvider(),
		realtimestt.NewAssemblyAIProvider(),
	} {
		if err := stt.Register(provider); err != nil {
			registrationErrors = append(registrationErrors, err)
		}
	}

	if unified == nil {
		return voice, stt, errors.Join(registrationErrors...)
	}

	for _, meta := range unified.GenericCapabilities().List(realtimeVoiceCapabilityType) {
		invoker, err := realtimeProviderInvoker(meta.Source, meta.PluginID, realtimeVoiceCapabilityType, openClaw, node)
		if err != nil {
			registrationErrors = append(registrationErrors, fmt.Errorf("realtime voice provider %q: %w", meta.ID, err))
			continue
		}
		if err := voice.Register(realtimevoice.NewPluginProvider(meta.ID, meta.Raw, invoker)); err != nil {
			registrationErrors = append(registrationErrors, err)
		}
	}
	for _, meta := range unified.TranscriptionProviders().List() {
		invoker, err := realtimeProviderInvoker(meta.Source, meta.PluginID, string(registry.CapabilityTypeTranscriptionProvider), openClaw, node)
		if err != nil {
			registrationErrors = append(registrationErrors, fmt.Errorf("realtime transcription provider %q: %w", meta.ID, err))
			continue
		}
		if err := stt.Register(realtimestt.NewPluginProvider(meta.ID, meta.Raw, invoker)); err != nil {
			registrationErrors = append(registrationErrors, err)
		}
	}

	return voice, stt, errors.Join(registrationErrors...)
}

func realtimeProviderInvoker(
	source registry.PluginSource,
	pluginID, capabilityType string,
	openClaw realtimePluginInvoker,
	node nodeCapabilityInvoker,
) (realtimePluginInvoker, error) {
	switch source {
	case registry.PluginSourceOpenClaw:
		if openClaw == nil {
			return nil, fmt.Errorf("OpenClaw plugin host is unavailable")
		}
		return openClaw, nil
	case registry.PluginSourceNode:
		if node == nil {
			return nil, fmt.Errorf("node plugin host is unavailable")
		}
		return boundNodeCapabilityInvoker{host: node, pluginID: pluginID, capabilityType: capabilityType}, nil
	default:
		return nil, fmt.Errorf("unsupported plugin source %q", source)
	}
}
