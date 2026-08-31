package main

import (
	"context"
	"fmt"

	"metiq/internal/agent/toolbuiltin"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// fipsDaemonRuntime owns the daemon-side FIPS transport, local control-socket
// client, and lifecycle-aware selector as one startup/cleanup unit.
type fipsDaemonRuntime struct {
	transport *nostruntime.FIPSTransport
	control   *nostruntime.FIPSControlClient
	selector  *nostruntime.TransportSelector
}

func startFIPSDaemonRuntime(
	cfg state.FIPSConfig,
	pubKeyHex string,
	relay nostruntime.DMTransport,
	onMessage func(context.Context, nostruntime.InboundDM) error,
	onError func(error),
) (*fipsDaemonRuntime, error) {
	control, err := nostruntime.NewFIPSControlClient(fipsControlClientOptionsFromConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("create FIPS daemon control client: %w", err)
	}

	transportOpts := fipsTransportOptionsFromConfig(cfg)
	transportOpts.PubkeyHex = pubKeyHex
	transportOpts.OnMessage = onMessage
	transportOpts.OnError = onError
	transport, err := nostruntime.NewFIPSTransport(transportOpts)
	if err != nil {
		return nil, fmt.Errorf("create FIPS transport: %w", err)
	}
	if err := transport.Start(); err != nil {
		transport.Close()
		return nil, fmt.Errorf("start FIPS transport: %w", err)
	}

	selectorOpts := transportSelectorOptionsFromConfig(cfg)
	selectorOpts.FIPS = transport
	selectorOpts.Relay = relay
	selectorOpts.DaemonState = control.DaemonState
	selector, err := nostruntime.NewTransportSelector(selectorOpts)
	if err != nil {
		transport.Close()
		return nil, fmt.Errorf("create FIPS transport selector: %w", err)
	}

	return &fipsDaemonRuntime{transport: transport, control: control, selector: selector}, nil
}

func (r *fipsDaemonRuntime) Close() {
	if r != nil && r.transport != nil {
		r.transport.Close()
	}
}

func (r *fipsDaemonRuntime) healthOptions() *toolbuiltin.FIPSStatusOpts {
	if r == nil {
		return nil
	}
	return &toolbuiltin.FIPSStatusOpts{
		Transport: func() *toolbuiltin.FIPSTransportHealth {
			addr := r.transport.ListenerAddr()
			return &toolbuiltin.FIPSTransportHealth{
				Listening:         addr != "",
				ListenAddr:        addr,
				ActiveConnections: r.transport.ConnectionCount(),
				IdentityCacheSize: r.transport.IdentityCacheSize(),
			}
		},
		Selector: func() *toolbuiltin.FIPSSelectorHealth {
			return &toolbuiltin.FIPSSelectorHealth{
				Preference:            r.selector.Preference(),
				ReachabilityCacheSize: r.selector.ReachabilityCacheSize(),
			}
		},
	}
}
