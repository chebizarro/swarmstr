package main

// main_relay_policy.go — Relay policy application, health monitoring, and
// Nostr channel status helpers.
//
// All functions are methods on *daemonServices, accessing relay state via
// s.relay.* instead of package-level globals.

import (
	"context"
	"log"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

// ---------------------------------------------------------------------------
// DM / control relay policy
// ---------------------------------------------------------------------------

func (s *daemonServices) applyDMRelayPolicy(relays []string) {
	r := &s.relay
	if r.nip17Bus != nil {
		if err := r.nip17Bus.SetRelays(relays); err != nil {
			log.Printf("nip17 relay policy update failed: %v", err)
		}
	}
	if r.nip04Bus != nil {
		if err := r.nip04Bus.SetRelays(relays); err != nil {
			log.Printf("nip04 relay policy update failed: %v", err)
		}
	}
	if r.nip17Bus == nil && r.nip04Bus == nil && r.dmBusMu != nil {
		r.dmBusMu.RLock()
		dmBus := *r.dmBus
		r.dmBusMu.RUnlock()
		if dmBus != nil {
			if err := dmBus.SetRelays(relays); err != nil {
				log.Printf("dm relay policy update failed: %v", err)
			}
		}
	}
}

func (s *daemonServices) applyControlRelayPolicy(relays []string) {
	if s.relay.controlBus != nil {
		if err := s.relay.controlBus.SetRelays(relays); err != nil {
			log.Printf("control relay policy update failed: %v", err)
		}
	}
}

func (s *daemonServices) applyRuntimeRelayPolicy(_ nostruntime.DMTransport, _ *nostruntime.ControlRPCBus, cfg state.ConfigDoc) {
	relays := nostruntime.MergeRelayLists(cfg.Relays.Read, cfg.Relays.Write)
	s.applyDMRelayPolicy(relays)
	s.applyControlRelayPolicy(relays)
	r := &s.relay
	if r.healthMonitor != nil && *r.healthMonitor != nil {
		(*r.healthMonitor).UpdateRelays(relays)
		(*r.healthMonitor).Trigger()
	}
	if r.watchRegistry != nil {
		r.watchRegistry.RebindRelays(relays)
	}
	if r.dvmHandler != nil {
		r.dvmHandler.SetRelays(relays)
	}

	// Update the NIP-65 relay selector fallbacks when relay config changes.
	if r.relaySelector != nil {
		r.relaySelector.SetFallbacks(cfg.Relays.Read, cfg.Relays.Write)
	}

	// Publish updated NIP-65 relay list and kind:10050 DM relay list to
	// reflect local config changes.  Uses categoriseRelays to ensure proper
	// read/write/both tagging per NIP-65.
	if r.keyer != nil && (len(cfg.Relays.Read) > 0 || len(cfg.Relays.Write) > 0) {
		s.scheduleRelayPolicyPublish(cfg.Relays.Read, cfg.Relays.Write)
	}
}

// ---------------------------------------------------------------------------
// Relay list publishing (debounced)
// ---------------------------------------------------------------------------

func (s *daemonServices) scheduleRelayPolicyPublish(readRelays, writeRelays []string) {
	pub := &s.relay.publish
	pub.mu.Lock()
	defer pub.mu.Unlock()

	pub.read = append([]string{}, readRelays...)
	pub.write = append([]string{}, writeRelays...)

	if pub.timer != nil {
		pub.timer.Stop()
	}
	pub.timer = time.AfterFunc(750*time.Millisecond, func() {
		pub.mu.Lock()
		read := append([]string{}, pub.read...)
		write := append([]string{}, pub.write...)
		pub.mu.Unlock()

		s.publishRelayPolicyLists(read, write)
	})
}

func (s *daemonServices) publishRelayPolicyLists(readRelays, writeRelays []string) {
	if s.relay.keyer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := nostr.NewPool(nostruntime.PoolOptsNIP42(s.relay.keyer))
	defer pool.Close("relay policy publish")
	publishRelays := nostruntime.MergeRelayLists(readRelays, writeRelays)
	if err := nostruntime.PublishStartupLists(ctx, nostruntime.StartupListPublishOptions{
		Keyer:         s.relay.keyer,
		Pool:          pool,
		PublishRelays: publishRelays,
		ReadRelays:    readRelays,
		WriteRelays:   writeRelays,
		ForcePublish:  true,
	}); err != nil {
		log.Printf("relay policy lists publish failed: %v", err)
	} else {
		log.Printf("published relay policy lists update")
	}
}

// ---------------------------------------------------------------------------
// Relay health monitoring
// ---------------------------------------------------------------------------

func (s *daemonServices) startRelayHealthMonitor(ctx context.Context, relays []string) {
	r := &s.relay
	if r.healthMonitor != nil && *r.healthMonitor != nil {
		(*r.healthMonitor).UpdateRelays(relays)
		(*r.healthMonitor).Trigger()
		return
	}
	monitor := nostruntime.NewRelayHealthMonitor(relays, nostruntime.RelayHealthMonitorOptions{
		Timeout:  8 * time.Second,
		Interval: 15 * time.Minute,
		OnResults: func(initial bool, results []nostruntime.RelayHealthResult) {
			s.logRelayHealthResults(initial, results)
		},
	})
	if r.healthMonitor != nil {
		*r.healthMonitor = monitor
	}
	monitor.Start(ctx)
}

func (s *daemonServices) logRelayHealthResults(initial bool, results []nostruntime.RelayHealthResult) {
	s.relay.healthStateMu.Lock()
	defer s.relay.healthStateMu.Unlock()

	if initial {
		failures := 0
		for _, res := range results {
			s.relay.healthState[res.URL] = res.Reachable
			s.emitWSEvent(gatewayws.EventRelayHealth, gatewayws.RelayHealthPayload{
				TS:        time.Now().UnixMilli(),
				URL:       res.URL,
				Reachable: res.Reachable,
				LatencyMS: res.Latency.Milliseconds(),
				Error:     relayHealthErrorString(res.Err),
				Initial:   true,
				Source:    "relay-monitor",
			})
			if res.Reachable {
				continue
			}
			failures++
			log.Printf("WARN relay healthcheck startup unreachable relay=%s err=%v", res.URL, res.Err)
		}
		if failures == 0 && len(results) > 0 {
			log.Printf("relay healthcheck startup ok relays=%d", len(results))
		}
		return
	}

	for _, res := range results {
		prev, seen := s.relay.healthState[res.URL]
		s.relay.healthState[res.URL] = res.Reachable
		s.emitWSEvent(gatewayws.EventRelayHealth, gatewayws.RelayHealthPayload{
			TS:        time.Now().UnixMilli(),
			URL:       res.URL,
			Reachable: res.Reachable,
			LatencyMS: res.Latency.Milliseconds(),
			Error:     relayHealthErrorString(res.Err),
			Source:    "relay-monitor",
		})
		if res.Reachable {
			if seen && !prev {
				log.Printf("relay healthcheck recovered relay=%s latency_ms=%d", res.URL, res.Latency.Milliseconds())
			}
			continue
		}
		if !seen || prev {
			log.Printf("WARN relay healthcheck unreachable relay=%s err=%v", res.URL, res.Err)
		}
	}
}

// relayHealthErrorString is a stateless helper — no receiver needed.
func relayHealthErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// Nostr channel status (stateless helpers — no receiver needed)
// ---------------------------------------------------------------------------

func resolveNostrChannelStatus(connected bool, loggedOut bool, fallback string) string {
	if loggedOut {
		return "logged_out"
	}
	if connected {
		return "connected"
	}
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	return "disconnected"
}

func buildNostrChannelStatusRow(status map[string]any, fallbackStatus string) map[string]any {
	connected, _ := status["connected"].(bool)
	loggedOut, _ := status["logged_out"].(bool)
	return map[string]any{
		"id":                     "nostr",
		"kind":                   "nostr",
		"channel":                "nostr",
		"status":                 resolveNostrChannelStatus(connected, loggedOut, fallbackStatus),
		"connected":              connected,
		"logged_out":             loggedOut,
		"read_relays":            status["read_relays"],
		"write_relays":           status["write_relays"],
		"runtime_dm_relays":      status["runtime_dm_relays"],
		"runtime_control_relays": status["runtime_ctrl_relays"],
	}
}

func subHealthToInfo(s nostruntime.SubHealthSnapshot) methods.SubHealthInfo {
	var lastEvtMS, lastReconnMS int64
	if !s.LastEventAt.IsZero() {
		lastEvtMS = s.LastEventAt.UnixMilli()
	}
	if !s.LastReconnectAt.IsZero() {
		lastReconnMS = s.LastReconnectAt.UnixMilli()
	}
	return methods.SubHealthInfo{
		Label:            s.Label,
		BoundRelays:      s.BoundRelays,
		LastEventAt:      lastEvtMS,
		LastReconnectAt:  lastReconnMS,
		LastClosedReason: s.LastClosedReason,
		ReplayWindowMS:   s.ReplayWindowMS,
		EventCount:       s.EventCount,
		ReconnectCount:   s.ReconnectCount,
	}
}

// ---------------------------------------------------------------------------
// Agent ID helpers (stateless — no receiver needed)
// ---------------------------------------------------------------------------

func defaultAgentID(id string) string {
	return methods.DefaultAgentID(id)
}

func isKnownAgentID(ctx context.Context, docsRepo *state.DocsRepository, id string) error {
	return methods.IsKnownAgentID(ctx, docsRepo, id)
}
