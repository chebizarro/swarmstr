package main

// daemon_services.go — Dependency injection struct that replaces direct
// access to package-level globals from extracted handler files.
//
// Initialized once in main() after all components are started. The extracted
// files (main_relay_policy.go, main_session_ops.go, main_handlers.go, etc.)
// receive a *daemonServices and use its fields instead of reading globals.

import (
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/agent"
	"metiq/internal/autoreply"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/nostr/dvm"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/agent/toolbuiltin"
	ttspkg "metiq/internal/tts"
	"metiq/internal/update"
)

// daemonServices owns the dependencies needed by extracted handler files.
// Constructed once in main() after all components are started.
type daemonServices struct {
	relay    relayPolicyServices
	emitter  gatewayws.EventEmitter
	session  sessionServices
	handlers handlerServices
}

// emitWSEvent emits a typed event to connected WS clients.
// This replaces direct calls to the package-level emitControlWSEvent bridge.
func (s *daemonServices) emitWSEvent(event string, payload any) {
	if s == nil || s.emitter == nil {
		return
	}
	s.emitter.Emit(event, payload)
}

// ---------------------------------------------------------------------------
// Relay policy sub-services
// ---------------------------------------------------------------------------

// relayPolicyServices groups all relay-policy-related runtime dependencies.
type relayPolicyServices struct {
	nip17Bus      *nostruntime.NIP17Bus
	nip04Bus      *nostruntime.DMBus
	dmBusMu       *sync.RWMutex
	dmBus         *nostruntime.DMTransport // pointer so relay policy can read the current value
	controlBus    *nostruntime.ControlRPCBus
	relaySelector *nostruntime.RelaySelector
	keyer         nostr.Keyer
	watchRegistry *toolbuiltin.WatchRegistry
	dvmHandler    *dvm.Handler
	healthMonitor **nostruntime.RelayHealthMonitor // pointer-to-pointer so startRelayHealthMonitor can assign
	healthStateMu sync.Mutex
	healthState   map[string]bool
	publish       relayPublishDebounce
}

// relayPublishDebounce holds the debounce state for relay list publishing.
type relayPublishDebounce struct {
	mu    sync.Mutex
	timer *time.Timer
	read  []string
	write []string
}

// ---------------------------------------------------------------------------
// Session sub-services
// ---------------------------------------------------------------------------

// sessionServices groups session-related runtime dependencies.
type sessionServices struct {
	sessionTurns      *autoreply.SessionTurns
	agentRuntime      agent.Runtime
	agentRegistry     *agent.AgentRuntimeRegistry
	sessionMemRuntime *sessionMemoryRuntime
}

// ---------------------------------------------------------------------------
// Handler sub-services
// ---------------------------------------------------------------------------

// handlerServices groups dependencies for misc RPC handler functions.
type handlerServices struct {
	ttsManager    *ttspkg.Manager
	updateChecker *update.Checker
}
