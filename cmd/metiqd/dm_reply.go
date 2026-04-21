package main

import (
	"context"
	"fmt"

	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func resolveDMReplyMode(cfg state.ConfigDoc, inboundScheme string) (string, error) {
	mode := cfg.DMReplyScheme()
	if mode != "auto" {
		return mode, nil
	}
	inbound, ok := state.ParseDMReplyScheme(inboundScheme)
	if !ok || inbound == "auto" {
		return "", fmt.Errorf("unknown inbound dm scheme %q", inboundScheme)
	}
	return inbound, nil
}

func currentDMReplyTransportBus(mode string) nostruntime.DMTransport {
	return controlServices.currentDMReplyTransportBus(mode)
}

func (s *daemonServices) currentDMReplyTransportBus(mode string) nostruntime.DMTransport {
	s.relay.dmBusMu.RLock()
	defer s.relay.dmBusMu.RUnlock()
	switch mode {
	case "nip17":
		if s.relay.nip17Bus != nil {
			return s.relay.nip17Bus
		}
		if bus, ok := (*s.relay.dmBus).(*nostruntime.NIP17Bus); ok {
			return bus
		}
	case "nip04":
		if s.relay.nip04Bus != nil {
			return s.relay.nip04Bus
		}
		if bus, ok := (*s.relay.dmBus).(*nostruntime.DMBus); ok {
			return bus
		}
	}
	return nil
}

func resolveDMReplyTransport(cfg state.ConfigDoc, inboundScheme string) (nostruntime.DMTransport, string, error) {
	mode, err := resolveDMReplyMode(cfg, inboundScheme)
	if err != nil {
		return nil, "", err
	}
	bus := currentDMReplyTransportBus(mode)
	if bus == nil {
		return nil, "", fmt.Errorf("dm reply scheme %s requested but no local %s transport is available", mode, mode)
	}
	return bus, mode, nil
}

func sendDMReplyWithTransport(ctx context.Context, bus nostruntime.DMTransport, scheme string, toPubKey string, text string) error {
	if bus == nil {
		return fmt.Errorf("dm reply transport not available")
	}
	if schemeBus, ok := bus.(nostruntime.DMSchemeTransport); ok {
		return schemeBus.SendDMWithScheme(ctx, toPubKey, text, scheme)
	}
	return bus.SendDM(ctx, toPubKey, text)
}

func wrapInboundDMReply(cfgGetter func() state.ConfigDoc, msg nostruntime.InboundDM) nostruntime.InboundDM {
	if msg.Reply == nil {
		return msg
	}
	inboundReply := msg.Reply
	fromPubKey := msg.FromPubKey
	inboundSchemeRaw := msg.Scheme
	inboundScheme, inboundSchemeOK := state.ParseDMReplyScheme(inboundSchemeRaw)
	msg.Reply = func(replyCtx context.Context, text string) error {
		cfg := state.ConfigDoc{}
		if cfgGetter != nil {
			cfg = cfgGetter()
		}
		replyMode, err := resolveDMReplyMode(cfg, inboundSchemeRaw)
		if err != nil {
			return err
		}
		if inboundSchemeOK && inboundScheme != "auto" && replyMode == inboundScheme {
			return inboundReply(replyCtx, text)
		}
		bus, scheme, err := resolveDMReplyTransport(cfg, inboundSchemeRaw)
		if err != nil {
			return err
		}
		return sendDMReplyWithTransport(replyCtx, bus, scheme, fromPubKey, text)
	}
	return msg
}
