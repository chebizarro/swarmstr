package main

import (
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// fipsTransportOptionsFromConfig builds FIPSTransportOptions from the parsed
// FIPSConfig, wiring ConnTimeout → DialTimeout and AgentPort.  The caller
// must still supply PubkeyHex, OnMessage, and OnError.
func fipsTransportOptionsFromConfig(cfg state.FIPSConfig) nostruntime.FIPSTransportOptions {
	return nostruntime.FIPSTransportOptions{
		AgentPort:   cfg.EffectiveAgentPort(),
		DialTimeout: cfg.EffectiveConnTimeout(),
	}
}

// transportSelectorOptionsFromConfig builds TransportSelectorOptions from the
// parsed FIPSConfig, wiring ReachCacheTTL and TransportPref.  The caller must
// still supply the FIPS and Relay transports.
func transportSelectorOptionsFromConfig(cfg state.FIPSConfig) nostruntime.TransportSelectorOptions {
	return nostruntime.TransportSelectorOptions{
		Pref:          cfg.EffectiveTransportPref(),
		ReachCacheTTL: cfg.EffectiveReachCacheTTL(),
	}
}
