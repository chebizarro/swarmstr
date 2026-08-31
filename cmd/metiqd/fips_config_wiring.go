package main

import (
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// fipsControlClientOptionsFromConfig wires the configured daemon control
// endpoint and bounded local dial timeout.
func fipsControlClientOptionsFromConfig(cfg state.FIPSConfig) nostruntime.FIPSControlClientOptions {
	return nostruntime.FIPSControlClientOptions{
		ControlSocket: cfg.ControlSocket,
		DialTimeout:   cfg.EffectiveConnTimeout(),
	}
}

func fipsTransportOptionsFromConfig(cfg state.FIPSConfig) nostruntime.FIPSTransportOptions {
	return nostruntime.FIPSTransportOptions{
		AgentPort:   cfg.EffectiveAgentPort(),
		DialTimeout: cfg.EffectiveConnTimeout(),
	}
}

// transportSelectorOptionsFromConfig builds TransportSelectorOptions from the
// parsed FIPSConfig, wiring ReachCacheTTL and TransportPref. The caller must
// still supply the FIPS and Relay transports and the control client's
// DaemonState callback.
func transportSelectorOptionsFromConfig(cfg state.FIPSConfig) nostruntime.TransportSelectorOptions {
	return nostruntime.TransportSelectorOptions{
		Pref:          cfg.EffectiveTransportPref(),
		ReachCacheTTL: cfg.EffectiveReachCacheTTL(),
	}
}
