package main

// main_relay_policy.go — Relay policy application, health monitoring, and
// Nostr channel status helpers.
//
// Extracted from main.go to reduce god-file size. All functions remain in
// package main and reference the same globals/helpers as before.

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

func applyDMRelayPolicy(relays []string) {
	if controlNIP17Bus != nil {
		if err := controlNIP17Bus.SetRelays(relays); err != nil {
			log.Printf("nip17 relay policy update failed: %v", err)
		}
	}
	if controlNIP04Bus != nil {
		if err := controlNIP04Bus.SetRelays(relays); err != nil {
			log.Printf("nip04 relay policy update failed: %v", err)
		}
	}
	if controlNIP17Bus == nil && controlNIP04Bus == nil {
		controlDMBusMu.RLock()
		dmBus := controlDMBus
		controlDMBusMu.RUnlock()
		if dmBus != nil {
			if err := dmBus.SetRelays(relays); err != nil {
				log.Printf("dm relay policy update failed: %v", err)
			}
		}
	}
}

func applyControlRelayPolicy(relays []string) {
	if controlRPCBus != nil {
		if err := controlRPCBus.SetRelays(relays); err != nil {
			log.Printf("control relay policy update failed: %v", err)
		}
	}
}

func applyRuntimeRelayPolicy(_ nostruntime.DMTransport, _ *nostruntime.ControlRPCBus, cfg state.ConfigDoc) {
	relays := nostruntime.MergeRelayLists(cfg.Relays.Read, cfg.Relays.Write)
	applyDMRelayPolicy(relays)
	applyControlRelayPolicy(relays)
	if relayHealthMonitor != nil {
		relayHealthMonitor.UpdateRelays(relays)
		relayHealthMonitor.Trigger()
	}
	if watchRegistry != nil {
		watchRegistry.RebindRelays(relays)
	}
	if dvmHandler != nil {
		dvmHandler.SetRelays(relays)
	}

	// Update the NIP-65 relay selector fallbacks when relay config changes.
	if controlRelaySelector != nil {
		controlRelaySelector.SetFallbacks(cfg.Relays.Read, cfg.Relays.Write)
	}

	// Publish updated NIP-65 relay list and kind:10050 DM relay list to
	// reflect local config changes.  Uses categoriseRelays to ensure proper
	// read/write/both tagging per NIP-65.
	if controlKeyer != nil && (len(cfg.Relays.Read) > 0 || len(cfg.Relays.Write) > 0) {
		scheduleRelayPolicyPublish(cfg.Relays.Read, cfg.Relays.Write)
	}
}

// ---------------------------------------------------------------------------
// Relay list publishing (debounced)
// ---------------------------------------------------------------------------

func scheduleRelayPolicyPublish(readRelays, writeRelays []string) {
	relayPolicyPublishMu.Lock()
	defer relayPolicyPublishMu.Unlock()

	relayPolicyPublishRead = append([]string{}, readRelays...)
	relayPolicyPublishWrite = append([]string{}, writeRelays...)

	if relayPolicyPublishTimer != nil {
		relayPolicyPublishTimer.Stop()
	}
	relayPolicyPublishTimer = time.AfterFunc(750*time.Millisecond, func() {
		relayPolicyPublishMu.Lock()
		read := append([]string{}, relayPolicyPublishRead...)
		write := append([]string{}, relayPolicyPublishWrite...)
		relayPolicyPublishMu.Unlock()

		publishRelayPolicyLists(read, write)
	})
}

func publishRelayPolicyLists(readRelays, writeRelays []string) {
	if controlKeyer == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := nostr.NewPool(nostruntime.PoolOptsNIP42(controlKeyer))
	defer pool.Close("relay policy publish")
	publishRelays := nostruntime.MergeRelayLists(readRelays, writeRelays)
	if err := nostruntime.PublishStartupLists(ctx, nostruntime.StartupListPublishOptions{
		Keyer:         controlKeyer,
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

func startRelayHealthMonitor(ctx context.Context, relays []string) {
	if relayHealthMonitor != nil {
		relayHealthMonitor.UpdateRelays(relays)
		relayHealthMonitor.Trigger()
		return
	}
	relayHealthMonitor = nostruntime.NewRelayHealthMonitor(relays, nostruntime.RelayHealthMonitorOptions{
		Timeout:  8 * time.Second,
		Interval: 15 * time.Minute,
		OnResults: func(initial bool, results []nostruntime.RelayHealthResult) {
			logRelayHealthResults(initial, results)
		},
	})
	relayHealthMonitor.Start(ctx)
}

func logRelayHealthResults(initial bool, results []nostruntime.RelayHealthResult) {
	relayHealthStateMu.Lock()
	defer relayHealthStateMu.Unlock()

	if initial {
		failures := 0
		for _, res := range results {
			relayHealthState[res.URL] = res.Reachable
			emitControlWSEvent(gatewayws.EventRelayHealth, gatewayws.RelayHealthPayload{
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
		prev, seen := relayHealthState[res.URL]
		relayHealthState[res.URL] = res.Reachable
		emitControlWSEvent(gatewayws.EventRelayHealth, gatewayws.RelayHealthPayload{
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

func relayHealthErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// Nostr channel status
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
// Agent ID helpers
// ---------------------------------------------------------------------------

func defaultAgentID(id string) string {
	return methods.DefaultAgentID(id)
}

func isKnownAgentID(ctx context.Context, docsRepo *state.DocsRepository, id string) error {
	return methods.IsKnownAgentID(ctx, docsRepo, id)
}
