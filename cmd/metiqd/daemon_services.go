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

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/autoreply"
	"metiq/internal/canvas"
	ctxengine "metiq/internal/context"
	"metiq/internal/gateway/channels"
	gatewayws "metiq/internal/gateway/ws"
	hookspkg "metiq/internal/hooks"
	mediapkg "metiq/internal/media"
	"metiq/internal/memory"
	"metiq/internal/nostr/dvm"
	"metiq/internal/nostr/nip38"
	"metiq/internal/gateway/nodepending"
	nostruntime "metiq/internal/nostr/runtime"
	pluginmanager "metiq/internal/plugins/manager"
	"metiq/internal/nostr/secure"
	secretspkg "metiq/internal/secrets"
	"metiq/internal/store/state"
	ttspkg "metiq/internal/tts"
	"metiq/internal/update"
)

// daemonServices owns the dependencies needed by extracted handler files.
// Constructed once in main() after all components are started.
type daemonServices struct {
	relay         relayPolicyServices
	emitter       gatewayws.EventEmitter
	emitterMu     *sync.RWMutex
	session       sessionServices
	handlers      handlerServices
	runtimeConfig *runtimeConfigStore
	docsRepo      *state.DocsRepository
	pubKeyHex     string
	restartCh     chan int
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
	nip17Bus            *nostruntime.NIP17Bus
	nip04Bus            *nostruntime.DMBus
	dmBusMu             *sync.RWMutex
	dmBus               *nostruntime.DMTransport // pointer so relay policy can read the current value
	controlBus          *nostruntime.ControlRPCBus
	relaySelector       *nostruntime.RelaySelector
	keyer               nostr.Keyer
	watchRegistry       *toolbuiltin.WatchRegistry
	dvmHandler          *dvm.Handler
	healthMonitor       **nostruntime.RelayHealthMonitor // pointer-to-pointer so startRelayHealthMonitor can assign
	healthStateMu       sync.Mutex
	healthState         map[string]bool
	publish             relayPublishDebounce
	transportSelector   *nostruntime.TransportSelector
	acpPeers            *acppkg.PeerRegistry
	acpDispatcher       *acppkg.Dispatcher
	hub                 *nostruntime.NostrHub
	channels            *channels.Registry
	presenceHeartbeat38 *nip38.Heartbeat
	rpcCorrelator       *RPCCorrelator
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
	sessionRouter     *agent.AgentSessionRouter
	toolRegistry      *agent.ToolRegistry
	memoryStore       memory.Store
	contextEngine     ctxengine.Engine
	contextEngineName string
	sessionStore      *state.SessionStore
	agentJobs         *agentJobRegistry
	subagents         *SubagentRegistry

	// Operation registries
	ops           *operationsRegistry
	cronJobs      *cronRegistry
	execApprovals *execApprovalsRegistry
	wizards       *wizardRegistry
	nodeInvocations *nodeInvocationRegistry
	nodePending     *nodepending.Store
}

// ---------------------------------------------------------------------------
// Handler sub-services
// ---------------------------------------------------------------------------

// handlerServices groups dependencies for misc RPC handler functions.
type handlerServices struct {
	ttsManager         *ttspkg.Manager
	updateChecker      *update.Checker
	secretsStore       *secretspkg.Store
	pairingConfigMu    *sync.Mutex
	hooksMgr           *hookspkg.Manager
	pluginMgr          *pluginmanager.GojaPluginManager
	mcpOps             *mcpOpsController
	mcpAuth            *mcpAuthController
	canvasHost         *canvas.Host
	mediaTranscriber   mediapkg.Transcriber
	keyRings           *agent.ProviderKeyRingRegistry
	stateEnvelopeCodec *secure.MutableSelfEnvelopeCodec
	configFilePath     string
	cronExecutorMu     *sync.RWMutex
}
