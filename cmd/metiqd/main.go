package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/admin"
	"metiq/internal/agent"
	"metiq/internal/autoreply"
	"metiq/internal/canvas"
	"metiq/internal/config"
	"metiq/internal/cron"
	"metiq/internal/gateway/channels"
	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/nodepending"
	gatewayprotocol "metiq/internal/gateway/protocol"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/grasp"
	hookspkg "metiq/internal/hooks"
	"metiq/internal/imagegen"
	mcppkg "metiq/internal/mcp"
	mediapkg "metiq/internal/media"
	"metiq/internal/memory"
	"metiq/internal/musicgen"
	"metiq/internal/nostr/dvm"
	"metiq/internal/nostr/nip38"
	"metiq/internal/nostr/nip51"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/nostr/secure"
	"metiq/internal/permissions"
	pluginhooks "metiq/internal/plugins/hooks"
	pluginmanager "metiq/internal/plugins/manager"
	pluginregistry "metiq/internal/plugins/registry"
	pluginruntime "metiq/internal/plugins/runtime"
	pluginservice "metiq/internal/plugins/service"
	"metiq/internal/policy"
	secretspkg "metiq/internal/secrets"
	"metiq/internal/social"
	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
	cfgTimeouts "metiq/internal/timeouts"
	ttspkg "metiq/internal/tts"
	"metiq/internal/update"
	"metiq/internal/videogen"
	"metiq/internal/workspace"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/agent/toolloop"
	ctxengine "metiq/internal/context"
	exportpkg "metiq/internal/export"
	metricspkg "metiq/internal/metrics"
	"metiq/internal/plugins/sdk"
	ratelimitpkg "metiq/internal/ratelimit"
	"metiq/internal/webui"

	// Built-in channel extensions — each init() registers a lightweight
	// constructor; no plugin instances are created until
	// extensions.RegisterConfigured() matches them against the live config.
	// Remove an import to exclude that extension from the binary entirely.
	"metiq/internal/extensions"
	_ "metiq/internal/extensions/bluebubbles"
	_ "metiq/internal/extensions/discord"
	_ "metiq/internal/extensions/email"
	_ "metiq/internal/extensions/feishu"
	_ "metiq/internal/extensions/googlechat"
	_ "metiq/internal/extensions/irc"
	_ "metiq/internal/extensions/line"
	_ "metiq/internal/extensions/matrix"
	_ "metiq/internal/extensions/mattermost"
	_ "metiq/internal/extensions/msteams"
	_ "metiq/internal/extensions/nextcloud"
	_ "metiq/internal/extensions/signal"
	_ "metiq/internal/extensions/slack"
	_ "metiq/internal/extensions/synology"
	_ "metiq/internal/extensions/telegram"
	_ "metiq/internal/extensions/twitch"
	_ "metiq/internal/extensions/whatsapp"
	_ "metiq/internal/extensions/zalo"
)

// version and commit are set at build time via -ldflags:
//
//	-X main.version=<tag> -X main.commit=<sha>
//
// They default to dev values for local builds.
var (
	version = "0.0.0-dev"
	commit  = "unknown"
)

var (
	controlAgentRuntime agent.Runtime
	controlAgentJobs    *agentJobRegistry
	// sessionDocUpdateLocks serializes in-process SessionDoc read/modify/write
	// cycles for a given session ID so concurrent DM hot paths do not lose
	// fields like active_turn, LastInboundAt, or LastReplyAt.
	sessionDocUpdateLocks [256]sync.Mutex
	// controlSessionTurns is the shared per-session turn-slot registry used by
	// live turns and destructive session lifecycle operations.
	controlSessionTurns         *autoreply.SessionTurns
	controlNodeInvocations      *nodeInvocationRegistry
	controlCronJobs             *cronRegistry
	controlSessionStore         *state.SessionStore
	controlSessionMemoryRuntime *sessionMemoryRuntime
	controlExecApprovals        *execApprovalsRegistry
	controlWizards              *wizardRegistry
	controlOps                  *operationsRegistry
	controlAgentRegistry        *agent.AgentRuntimeRegistry
	controlSessionRouter        *agent.AgentSessionRouter
	controlKeyer                nostr.Keyer                   // always set at startup; plain mode wraps key in a keyer
	controlPresenceHeartbeat38  *nip38.Heartbeat              // NIP-38 presence/status heartbeat; nil when disabled
	controlProfilePublisher     *nostruntime.ProfilePublisher // routine kind:0 profile publisher; nil when no profile configured
	// controlWsEmitter forwards typed events to connected WS clients.
	// Starts as NoopEmitter; upgraded to RuntimeEmitter once the WS gateway starts.
	controlWsEmitter       gatewayws.EventEmitter = gatewayws.NoopEmitter{}
	controlWsEmitterMu     sync.RWMutex
	controlHookInvoker     *pluginhooks.HookInvoker
	controlPairingConfigMu sync.Mutex

	// controlToolRegistry is the base tool registry used by agent runtimes.
	// Stored globally so the MethodAgent handler can build profile-filtered runtimes.
	controlToolRegistry *agent.ToolRegistry
	// controlRuntimeConfig is the live runtime config store used by shared
	// helper paths outside main() (for example agent.run fallbacks).
	controlRuntimeConfig *runtimeConfigStore
	// controlStateEnvelopeCodec switches relay-persisted state docs between
	// plaintext and NIP-44 self-encryption based on runtime config.
	controlStateEnvelopeCodec *secure.MutableSelfEnvelopeCodec
	// controlConfigFilePath is the runtime config file path used for durable
	// write-back on successful config mutations.
	controlConfigFilePath string

	// controlMCPOps manages operator-facing MCP list/get/put/remove/test/reconnect flows.
	controlMCPOps *mcpOpsController

	// controlSubagents tracks spawned child agent sessions and their ancestry.
	controlSubagents *SubagentRegistry

	// controlACPPeers is the ACP peer registry tracking known remote agent pubkeys.
	controlACPPeers *acppkg.PeerRegistry
	// controlACPDispatcher routes incoming ACP result DMs to waiting Dispatch() callers.
	controlACPDispatcher *acppkg.Dispatcher
	// controlTransportSelector is the FIPS-aware composite transport that routes
	// messages through FIPS mesh or relay transports based on the configured
	// preference. Nil when FIPS is not enabled.
	controlTransportSelector *nostruntime.TransportSelector

	// fipsHealthOpts holds the dependency-injected providers for FIPS health
	// reporting (fips_status tool and status.get). Nil when FIPS is not enabled.
	fipsHealthOpts *toolbuiltin.FIPSStatusOpts

	// controlContextEngine is the shared pluggable context engine used to ingest
	// and assemble conversation history for every agent session.
	controlContextEngine ctxengine.Engine

	// controlAutoCompactState tracks per-session circuit breaker state for
	// autocompact. After 3 consecutive failures, compaction is skipped to
	// avoid wasting API calls on irrecoverable contexts.
	controlAutoCompactState = ctxengine.NewAutoCompactState()

	// controlDMBus is the preferred outbound DM transport (NIP-17 first, then NIP-04).
	// Separate concrete bus pointers are kept so relay policy changes can rebind
	// every active inbound subscription, not just the preferred sender.
	controlDMBusMu  sync.RWMutex
	controlDMBus    nostruntime.DMTransport
	controlNIP04Bus *nostruntime.DMBus
	controlNIP17Bus *nostruntime.NIP17Bus

	// controlRPCCorrelator routes synchronous inter-agent RPC replies to
	// waiting nostr_agent_rpc tool calls instead of the normal agent pipeline.
	controlRPCCorrelator = newRPCCorrelator()
	controlRPCBus        *nostruntime.ControlRPCBus

	// controlRelaySelector is the global NIP-65 relay selector implementing the
	// outbox model. It caches per-pubkey relay lists and provides relay selection
	// for reading from / writing to specific pubkeys.
	controlRelaySelector *nostruntime.RelaySelector

	// controlHub is the shared NostrHub.  All channels, tools, and non-DM
	// subsystems share this hub's pool so WebSocket connections to the same
	// relay are deduplicated across the entire runtime.
	controlHub *nostruntime.NostrHub

	// watchRegistry is the global watch subscription registry, promoted to
	// package level so relay policy update functions can rebind active watches.
	watchRegistry *toolbuiltin.WatchRegistry

	// dvmHandler is the global DVM handler, promoted to package level so
	// relay policy updates and status introspection can reach it.
	dvmHandler *dvm.Handler

	// relaySetRegistry manages NIP-51 kind:30002 relay sets (nip29-relays,
	// chat-relays, nip28-relays, search-relays, dvm-relays, grasp-servers).
	// Promoted to package level so relay policy updates can re-publish sets.
	relaySetRegistry *nostruntime.RelaySetRegistry

	// relayHealthMonitor performs startup and periodic relay smoke-tests so
	// reachability issues are surfaced in logs before message delivery fails.
	relayHealthMonitor *nostruntime.RelayHealthMonitor
	relayHealthStateMu sync.Mutex
	relayHealthState   = map[string]bool{}

	// capabilityMonitor publishes the local kind:30317 capability descriptor and
	// subscribes to fleet peers' capability events for dynamic discovery.
	capabilityMonitor  *nostruntime.CapabilityMonitor
	capabilityRegistry *nostruntime.CapabilityRegistry

	// controlServices is the consolidated dependency struct for extracted handler
	// files. Initialized in main() after all components are started. Replaces
	// direct global access in main_relay_policy.go and other extracted files.
	controlServicesMu sync.RWMutex
	controlServices   *daemonServices

	// controlCronExecutor dispatches a gateway method from the cron scheduler.
	// Nil until startup completes; the scheduler goroutine checks for nil before calling.
	controlCronExecutorMu sync.RWMutex
	controlCronExecutor   func(ctx context.Context, method string, params json.RawMessage) (any, error)

	relayPolicyPublishMu    sync.Mutex
	relayPolicyPublishTimer *time.Timer
	relayPolicyPublishRead  []string
	relayPolicyPublishWrite []string
)

func conversationMessageFromContext(m ctxengine.Message) agent.ConversationMessage {
	cm := agent.ConversationMessage{
		Role:       m.Role,
		Content:    annotateConversationContentTimestamp(m),
		ToolCallID: m.ToolCallID,
	}
	for _, tc := range m.ToolCalls {
		cm.ToolCalls = append(cm.ToolCalls, agent.ToolCallRef{
			ID:       tc.ID,
			Name:     tc.Name,
			ArgsJSON: tc.ArgsJSON,
		})
	}
	return cm
}

func annotateConversationContentTimestamp(m ctxengine.Message) string {
	if m.Unix <= 0 {
		return m.Content
	}
	ts := time.Unix(m.Unix, 0).UTC().Format(time.RFC3339)
	return fmt.Sprintf("[message_time=%s unix=%d]\n%s", ts, m.Unix, m.Content)
}

// stripTimestampAnnotations removes [message_time=...] annotations from text.
// These annotations are added to conversation history for LLM temporal context,
// but should not appear in user-facing responses.
func stripTimestampAnnotations(text string) string {
	// Pattern: [message_time=2026-05-08T04:55:15Z unix=1778215935]
	// Match one or more timestamp lines at the start of the text.
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip lines that match the timestamp annotation pattern
		if strings.HasPrefix(trimmed, "[message_time=") && strings.Contains(trimmed, "unix=") && strings.HasSuffix(trimmed, "]") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

func defaultBootstrapWatchSpecs(sessionID, selfPubKey string, now time.Time) []toolbuiltin.WatchSpec {
	const defaultTTLSeconds = 365 * 24 * 60 * 60
	createdAt := now.Unix()
	deadline := now.Add(time.Duration(defaultTTLSeconds) * time.Second).Unix()
	// NOTE: gift-wrapped DMs (kind 1059) are intentionally NOT included here.
	// NIP17Bus already subscribes to kind 1059, properly unwraps the gift wrap
	// via NIP-59, and delivers the plaintext rumor through dmOnMessage with a
	// working reply function.  A watch spec for kind 1059 would deliver RAW
	// encrypted events the agent cannot decrypt, with no reply path, wasting
	// tokens on encrypted noise.
	return []toolbuiltin.WatchSpec{
		{
			Name:      "social-mentions",
			SessionID: sessionID,
			FilterRaw: map[string]any{
				"kinds": []any{float64(1), float64(7), float64(1111)},
				"tag_e": []any{selfPubKey},
			},
			TTLSec:    defaultTTLSeconds,
			MaxEvents: 0,
			CreatedAt: createdAt,
			Deadline:  deadline,
		},
		{
			Name:      "direct-mentions",
			SessionID: sessionID,
			FilterRaw: map[string]any{
				"kinds": []any{float64(1)},
				"tag_p": []any{selfPubKey},
			},
			TTLSec:    defaultTTLSeconds,
			MaxEvents: 0,
			CreatedAt: createdAt,
			Deadline:  deadline,
		},
	}
}

func loadOrDefaultWatchSpecs(raw json.RawMessage, sessionID, selfPubKey string, now time.Time) ([]toolbuiltin.WatchSpec, bool, error) {
	if len(raw) == 0 {
		return defaultBootstrapWatchSpecs(sessionID, selfPubKey, now), true, nil
	}
	var specs []toolbuiltin.WatchSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, false, err
	}
	return specs, false, nil
}

func prepopulateACPPeersFromConfig(reg *acppkg.PeerRegistry, cfg state.ConfigDoc) int {
	if reg == nil {
		return 0
	}
	added := 0
	for _, peer := range cfg.ACP.Peers {
		if strings.TrimSpace(peer.PubKey) == "" {
			continue
		}
		if err := reg.Register(acppkg.PeerEntry{PubKey: peer.PubKey, Alias: peer.Alias, Tags: peer.Tags}); err != nil {
			continue
		}
		added++
	}
	return added
}

func main() {
	log.Printf("metiqd starting version=%s commit=%s", version, commit)

	var bootstrapPath string
	var configFilePath string
	var adminAddr string
	var adminToken string
	var gatewayWSAddr string
	var gatewayWSToken string
	var gatewayWSPath string
	var gatewayWSAllowedOrigins string
	var gatewayWSTrustedProxies string
	var gatewayWSAllowInsecureControlUI bool
	var pidFile string
	flag.StringVar(&bootstrapPath, "bootstrap", "", "path to bootstrap config JSON")
	flag.StringVar(&configFilePath, "config", "", "path to live config JSON/JSON5/YAML file; watched for changes (default: ~/.metiq/config.json)")
	flag.StringVar(&adminAddr, "admin-addr", "", "optional admin API listen address, e.g. 127.0.0.1:8787")
	flag.StringVar(&adminToken, "admin-token", "", "optional bearer token for admin API")
	flag.StringVar(&gatewayWSAddr, "gateway-ws-addr", "", "optional gateway websocket listen address, e.g. 127.0.0.1:8788")
	flag.StringVar(&gatewayWSToken, "gateway-ws-token", "", "optional gateway websocket token")
	flag.StringVar(&gatewayWSPath, "gateway-ws-path", "", "optional gateway websocket path (default /ws)")
	flag.StringVar(&gatewayWSAllowedOrigins, "gateway-ws-allowed-origins", "", "optional comma-separated websocket Origin allowlist")
	flag.StringVar(&gatewayWSTrustedProxies, "gateway-ws-trusted-proxies", "", "optional comma-separated trusted proxy CIDRs/IPs for proxy-auth mode")
	flag.BoolVar(&gatewayWSAllowInsecureControlUI, "gateway-ws-allow-insecure-control-ui", false, "allow control-ui without device identity outside localhost")
	flag.StringVar(&pidFile, "pid-file", "", "write PID to this file on startup; removed on clean shutdown")
	flag.Parse()

	cfg, err := config.LoadBootstrap(bootstrapPath)
	if err != nil {
		log.Fatalf("load bootstrap config: %v", err)
	}

	// Register model context overrides from bootstrap config.
	for pattern, ctxWindow := range cfg.ModelContextOverrides {
		if ctxWindow > 0 {
			agent.RegisterModelContextPattern(pattern, agent.ProfileFromContextWindowTokens(ctxWindow))
			log.Printf("registered model context override: %q → %d tokens", pattern, ctxWindow)
		}
	}
	if config.IsBunkerURL(cfg) {
		log.Printf("signer: NIP-46 bunker detected, connecting…")
	}
	signerCtx, signerCancel := context.WithTimeout(context.Background(), 30*time.Second)
	kr, kErr := config.ResolveSigner(signerCtx, cfg, nil)
	signerCancel()
	if kErr != nil {
		log.Fatalf("resolve signer: %v", kErr)
	}
	controlKeyer = kr
	// Derive pubkey from the signer for identity tools and runtime wiring.
	pkCtx, pkCancel := context.WithTimeout(context.Background(), 10*time.Second)
	pk, pkErr := kr.GetPublicKey(pkCtx)
	pkCancel()
	if pkErr != nil {
		log.Fatalf("signer: get public key: %v", pkErr)
	}
	pubKeyHex := pk.Hex()
	log.Printf("signer ready pubkey=%s", pk.Hex())
	if adminAddr == "" {
		adminAddr = cfg.AdminListenAddr
	}
	if adminToken == "" {
		adminToken = cfg.AdminToken
	}
	if gatewayWSAddr == "" {
		gatewayWSAddr = cfg.GatewayWSListenAddr
	}
	if gatewayWSToken == "" {
		gatewayWSToken = cfg.GatewayWSToken
	}
	if gatewayWSPath == "" {
		gatewayWSPath = cfg.GatewayWSPath
	}
	allowedOrigins := normalizeCSVList(gatewayWSAllowedOrigins)
	if len(allowedOrigins) == 0 {
		allowedOrigins = normalizeStringList(cfg.GatewayWSAllowedOrigins)
	}
	trustedProxies := normalizeCSVList(gatewayWSTrustedProxies)
	if len(trustedProxies) == 0 {
		trustedProxies = normalizeStringList(cfg.GatewayWSTrustedProxies)
	}
	if !gatewayWSAllowInsecureControlUI {
		gatewayWSAllowInsecureControlUI = cfg.GatewayWSAllowInsecureControlUI
	}

	// Write PID file if requested; remove on clean shutdown.
	if pidFile != "" {
		if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
			log.Fatalf("create pid file directory %s: %v", filepath.Dir(pidFile), err)
		}
		if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
			log.Fatalf("write pid file %s: %v", pidFile, err)
		}
		defer func() {
			if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
				log.Printf("remove pid file %s: %v", pidFile, err)
			}
		}()
		log.Printf("pid file written: %s (pid=%d)", pidFile, os.Getpid())
	}

	startedAt := time.Now()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var agentRunWG sync.WaitGroup
	var agentRunMu sync.Mutex
	agentRunClosed := false

	shutdownEmitter := newRuntimeShutdownEmitter(emitControlWSEvent)

	restartCh := make(chan int, 4)

	// Restart scheduler: drains restartCh, emits EventShutdown, then stops the daemon.
	// The supervisor (systemd / launchd / Docker restart policy) is expected to re-launch it.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case delayMS := <-restartCh:
				if delayMS < 0 {
					delayMS = 0
				}
				shutdownEmitter.Emit("config change requires restart")
				if delayMS > 0 {
					time.Sleep(time.Duration(delayMS) * time.Millisecond)
				}
				log.Printf("scheduled restart executing (delay=%dms)", delayMS)
				stop() // cancel context → graceful shutdown
			}
		}
	}()

	store, err := state.NewNostrStore(controlKeyer, cfg.Relays)
	if err != nil {
		log.Fatalf("init state store: %v", err)
	}
	defer store.Close()

	pubkey := pubKeyHex

	codec, err := initEnvelopeCodec(controlKeyer)
	if err != nil {
		log.Fatalf("init envelope codec: %v", err)
	}
	controlStateEnvelopeCodec = codec

	docsRepo := state.NewDocsRepositoryWithCodec(store, pubkey, codec)
	taskStore := taskspkg.NewDocsStore(docsRepo)
	taskEvents := taskspkg.NewEventEmitter()
	taskService := taskspkg.NewService(taskStore, taskspkg.WithServiceEvents(taskEvents))
	taskLedger := taskService.Ledger()
	taskLedger.AddRetrospectiveObserver(docsRepo, taskspkg.RetroObserverConfig{})
	workflowStore := taskspkg.NewDocsWorkflowStore(docsRepo)
	transcriptRepo := state.NewTranscriptRepositoryWithCodec(store, pubkey, codec)
	memoryRepo := state.NewMemoryRepositoryWithCodec(store, pubkey, codec)

	sessionStore, ssErr := state.NewSessionStore(state.DefaultSessionStorePath())
	if ssErr != nil {
		log.Printf("session store init failed (non-fatal): %v", ssErr)
		sessionStore = nil
	}
	controlSessionStore = sessionStore
	sessionMemoryRuntime := newSessionMemoryRuntime(sessionStore, transcriptRepo)
	controlSessionMemoryRuntime = sessionMemoryRuntime
	baseMemoryIndex, err := memory.OpenIndex("")
	if err != nil {
		log.Fatalf("open memory index: %v", err)
	}
	defer func() {
		if err := baseMemoryIndex.Save(); err != nil {
			log.Printf("memory index save on shutdown failed: %v", err)
		}
	}()
	var memoryIndex memory.Store = baseMemoryIndex
	var sqliteMemoryBackend *memory.SQLiteBackend

	// Social planner: manages social action plans, rate limits, and history.
	socialPlanner := social.NewPlanner(social.DefaultRateLimitConfig())

	tools := agent.NewToolRegistry()
	controlToolRegistry = tools
	var configState *runtimeConfigStore
	tools.RegisterWithDef("memory_query", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryQueryTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryQueryDef)
	tools.RegisterWithDef("memory_stats", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryStatsTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryStatsDef)
	tools.RegisterWithDef("memory_health", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryHealthTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryHealthDef)
	tools.RegisterWithDef("memory_explain_query", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryExplainQueryTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryExplainQueryDef)
	tools.RegisterWithDef("memory_reflect", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryReflectTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryReflectDef)
	// memory_search is kept as a compatibility alias over memory_query.
	tools.RegisterWithDef("memory_search", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemorySearchCompatTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemorySearchDef)

	// acp.delegate — allows the agent to dispatch a sub-task to a peer agent
	// and wait for the result.  Uses the global DM transport + dispatcher.
	tools.Register("acp_delegate", func(ctx context.Context, args map[string]any) (string, error) {
		peerTarget := agent.ArgString(args, "peer_pubkey")
		instructions := agent.ArgString(args, "instructions")
		memoryScope := state.NormalizeAgentMemoryScope(agent.ArgString(args, "memory_scope"))
		timeoutMS := int64(agent.ArgInt(args, "timeout_ms", 60000))
		if peerTarget == "" || instructions == "" {
			return "", fmt.Errorf("acp.delegate: peer_pubkey and instructions are required")
		}
		cfg := state.ConfigDoc{}
		if configState != nil {
			cfg = configState.Get()
		}
		taskID := acppkg.GenerateTaskID()
		taskPayload := buildInheritedACPTaskPayload(ctx, cfg, docsRepo, sessionStore, acppkg.TaskPayload{
			Instructions: instructions,
			MemoryScope:  memoryScope,
			TimeoutMS:    timeoutMS,
		})
		bindACPTaskID(&taskPayload, taskID)
		recordACPDelegatedChild(sessionStore, taskPayload, taskID)
		req := buildACPTargetRequirements(cfg, turnToolConstraints{ToolProfile: taskPayload.ToolProfile, EnabledTools: taskPayload.EnabledTools})
		peerPubKey, _, err := resolveACPFleetTargetForConfigAndRequirements(peerTarget, cfg, req)
		if err != nil {
			return "", fmt.Errorf("acp.delegate: %w", err)
		}
		dmBus, dmScheme, err := resolveACPDMTransport(cfg, peerPubKey)
		if err != nil {
			return "", fmt.Errorf("acp.delegate: %w", err)
		}
		senderPubKey := dmBus.PublicKey()
		taskPayload.ReplyTo = senderPubKey
		acpMsg := acppkg.NewTask(taskID, senderPubKey, taskPayload)
		controlACPDispatcher.Register(taskID)
		payload, _ := json.Marshal(acpMsg)
		if err := sendACPDMWithTransport(ctx, dmBus, dmScheme, peerPubKey, string(payload)); err != nil {
			controlACPDispatcher.Cancel(taskID)
			return "", fmt.Errorf("acp.delegate: send: %w", err)
		}
		result, err := controlACPDispatcher.Wait(ctx, taskID, time.Duration(timeoutMS)*time.Millisecond)
		if err != nil {
			return "", fmt.Errorf("acp.delegate: %w", err)
		}
		if result.Error != "" {
			return "", fmt.Errorf("acp.delegate: peer error: %s", result.Error)
		}
		return result.Text, nil
	})
	tools.SetDefinition("acp_delegate", toolbuiltin.ACPDelegateDef)

	// Attach definition for inline memory.search (global cross-session search).
	tools.SetDefinition("memory_search", toolbuiltin.MemorySearchDef)

	// ── Built-in toolbuiltin tools ─────────────────────────────────────────
	// web_fetch: fetch and extract text from a URL (SSRF-guarded).
	tools.RegisterWithDef("web_fetch", toolbuiltin.WebFetchTool(toolbuiltin.WebFetchOpts{}), toolbuiltin.WebFetchDef)

	// web_search: search the web via Brave (BRAVE_SEARCH_API_KEY) or
	// Serper (SERPER_API_KEY).  Provider is auto-detected from env vars.
	tools.RegisterWithDef("web_search", toolbuiltin.WebSearchTool(toolbuiltin.WebSearchConfig{}), toolbuiltin.WebSearchDef)

	// Unified typed memory tools. Legacy memory_store/search/delete names remain as aliases.
	tools.RegisterWithDef("memory_write", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryWriteTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryWriteDef)
	tools.RegisterWithDef("memory_store", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryStoreCompatTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryStoreDef)
	tools.RegisterWithDef("memory_get", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryGetTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryGetDef)
	tools.RegisterWithDef("memory_update", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryUpdateTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryUpdateDef)
	tools.RegisterWithDef("memory_forget", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryForgetTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryForgetDef)
	tools.RegisterWithDef("memory_apply_reflection", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryApplyReflectionTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryApplyReflectionDef)
	tools.RegisterWithDef("memory_delete", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.MemoryForgetTool(memoryIndex)(ctx, args)
	}, toolbuiltin.MemoryDeleteDef)

	// memory_pin / memory_pinned: long-term agent knowledge surfaced in every system prompt.
	tools.RegisterWithDef("memory_pin", toolbuiltin.MemoryPinTool(memoryIndex), toolbuiltin.MemoryPinDef)
	tools.RegisterWithDef("memory_pinned", toolbuiltin.MemoryPinnedTool(memoryIndex), toolbuiltin.MemoryPinnedDef)

	// read_pdf: extract text from a local PDF file via pdftotext.
	// Allowed roots come from extra.tools.pdf.allowed_roots and default to
	// workspace-scoped directories when not configured.
	tools.Register("read_pdf", func(ctx context.Context, args map[string]any) (string, error) {
		if configState == nil {
			return "", fmt.Errorf("read_pdf: runtime config unavailable")
		}
		allowedRoots := configuredPDFAllowedRoots(configState.Get())
		return toolbuiltin.PDFTool(allowedRoots)(ctx, args)
	})
	tools.Register("add_reaction", toolbuiltin.AddReactionTool())
	tools.Register("remove_reaction", toolbuiltin.RemoveReactionTool())
	tools.Register("send_typing", toolbuiltin.SendTypingTool())
	tools.Register("send_in_thread", toolbuiltin.SendInThreadTool())
	tools.Register("edit_message", toolbuiltin.EditMessageTool())

	// ── Outbound content guard ───────────────────────────────────────────────
	// Scans all outbound Nostr event content for secrets, API keys, private keys,
	// and other sensitive data before publishing. Policy defaults to "block" which
	// rejects any event containing detected secrets.
	var publishGuardPolicy secure.PublishPolicy
	if configState != nil {
		if pgExtra, ok := configState.Get().Extra["publish_guard"].(map[string]any); ok {
			if p, ok := pgExtra["policy"].(string); ok {
				publishGuardPolicy = secure.ParsePublishPolicy(p)
			}
		}
	}
	if publishGuardPolicy == "" {
		publishGuardPolicy = secure.PublishPolicyBlock
	}
	publishGuard := secure.NewPublishGuard(publishGuardPolicy)
	log.Printf("publish guard: policy=%s patterns=%d", publishGuard.Policy(), publishGuard.PatternCount())

	// ── Nostr network tools ─────────────────────────────────────────────────
	// These give the agent first-class read/write/DM access to the Nostr network.
	nostrToolOpts := toolbuiltin.NostrToolOpts{
		Keyer:        controlKeyer,
		Relays:       cfg.Relays,
		HubFunc:      func() *nostruntime.NostrHub { return controlHub },
		PublishGuard: publishGuard,
	}
	tools.RegisterWithDef("nostr_fetch", toolbuiltin.NostrFetchTool(nostrToolOpts), toolbuiltin.NostrFetchDef)
	tools.RegisterWithDef("nostr_dm_decrypt", toolbuiltin.NostrDMDecryptTool(nostrToolOpts), toolbuiltin.NostrDMDecryptDef)
	tools.RegisterWithDef("nostr_publish", toolbuiltin.NostrPublishTool(toolbuiltin.NostrToolOpts{
		Keyer:        controlKeyer,
		Relays:       cfg.Relays,
		PublishGuard: publishGuard,
	}), toolbuiltin.NostrPublishDef)
	// nostr_send_dm uses controlDMBus which is assigned later; capture by reference via closure.
	// When the caller requests nip04 encryption explicitly, route to controlNIP04Bus so the
	// NIP-04 bus handles it directly (NIP-17 bus rejects nip04 scheme requests).
	tools.Register("nostr_send_dm", func(ctx context.Context, args map[string]any) (string, error) {
		controlDMBusMu.RLock()
		bus := controlDMBus
		controlDMBusMu.RUnlock()
		encryption := ""
		if v, ok := args["encryption"]; ok {
			if s, ok := v.(string); ok {
				encryption = strings.ToLower(strings.TrimSpace(s))
			}
		}
		if (encryption == "nip04" || encryption == "nip-04") && controlNIP04Bus != nil {
			bus = controlNIP04Bus
		}
		return toolbuiltin.NostrSendDMTool(toolbuiltin.NostrToolOpts{DMTransport: bus, PublishGuard: publishGuard})(ctx, args)
	})
	tools.SetDefinition("nostr_send_dm", toolbuiltin.NostrSendDMDef)

	// ── Fleet inter-agent tools ─────────────────────────────────────────────
	// fleet_agents: list known fleet agents from the NIP-51 directory.
	// nostr_agent_rpc: send DM to a fleet agent and wait for its reply.
	tools.RegisterWithDef("fleet_agents", toolbuiltin.FleetAgentsTool(fleetDirectory), toolbuiltin.FleetAgentsDef)
	tools.Register("nostr_agent_rpc", func(ctx context.Context, args map[string]any) (string, error) {
		controlDMBusMu.RLock()
		bus := controlDMBus
		controlDMBusMu.RUnlock()
		return toolbuiltin.NostrAgentRPCTool(
			toolbuiltin.NostrToolOpts{DMTransport: bus, PublishGuard: publishGuard},
			fleetDirectory,
			controlRPCCorrelator.WaiterFunc(),
		)(ctx, args)
	})
	tools.SetDefinition("nostr_agent_rpc", toolbuiltin.NostrAgentRPCDef)

	// nostr_agent_send: async (non-blocking) DM to a fleet agent.
	tools.Register("nostr_agent_send", func(ctx context.Context, args map[string]any) (string, error) {
		controlDMBusMu.RLock()
		bus := controlDMBus
		controlDMBusMu.RUnlock()
		return toolbuiltin.NostrAgentSendTool(
			toolbuiltin.NostrToolOpts{DMTransport: bus, PublishGuard: publishGuard},
			fleetDirectory,
		)(ctx, args)
	})
	tools.SetDefinition("nostr_agent_send", toolbuiltin.NostrAgentSendDef)

	// nostr_agent_inbox: poll for async replies from a fleet agent.
	tools.RegisterWithDef("nostr_agent_inbox", toolbuiltin.NostrAgentInboxTool(
		fleetDirectory,
		func(fromPubkeyHex string) []toolbuiltin.InboxMessage {
			raw := controlRPCCorrelator.DrainInbox(fromPubkeyHex)
			out := make([]toolbuiltin.InboxMessage, len(raw))
			for i, e := range raw {
				out[i] = toolbuiltin.InboxMessage{From: e.From, Text: e.Text, Unix: e.Unix}
			}
			return out
		},
	), toolbuiltin.NostrAgentInboxDef)

	tools.RegisterWithDef("nostr_profile", toolbuiltin.NostrProfileTool(nostrToolOpts), toolbuiltin.NostrProfileDef)
	tools.RegisterWithDef("nostr_profile_set", toolbuiltin.NostrProfileSetTool(nostrToolOpts), toolbuiltin.NostrProfileSetDef)
	tools.RegisterWithDef("nostr_resolve_nip05", toolbuiltin.NostrResolveNIP05Tool(), toolbuiltin.NostrResolveNIP05Def)
	tools.RegisterWithDef("relay_list", toolbuiltin.NostrRelayListTool(toolbuiltin.NostrRelayToolOpts{
		ReadRelays:  cfg.Relays,
		WriteRelays: cfg.Relays,
	}), toolbuiltin.NostrRelayListDef)
	tools.RegisterWithDef("relay_ping", toolbuiltin.NostrRelayPingTool(), toolbuiltin.NostrRelayPingDef)
	tools.RegisterWithDef("relay_info", toolbuiltin.NostrRelayInfoTool(), toolbuiltin.NostrRelayInfoDef)
	tools.RegisterWithDef("relay_score", toolbuiltin.NostrRelayScoreTool(), toolbuiltin.NostrRelayScoreDef)
	tools.RegisterWithDef("nostr_follows", toolbuiltin.NostrFollowsTool(nostrToolOpts), toolbuiltin.NostrFollowsDef)
	tools.RegisterWithDef("nostr_followers", toolbuiltin.NostrFollowersTool(nostrToolOpts), toolbuiltin.NostrFollowersDef)
	tools.RegisterWithDef("nostr_wot_distance", toolbuiltin.NostrWotDistanceTool(nostrToolOpts), toolbuiltin.NostrWotDistanceDef)
	tools.RegisterWithDef("nostr_relay_hints", toolbuiltin.NostrRelayHintsTool(nostrToolOpts), toolbuiltin.NostrRelayHintsDef)
	tools.RegisterWithDef("nostr_relay_list_set", toolbuiltin.NostrRelayListSetTool(nostrToolOpts), toolbuiltin.NostrRelayListSetDef)
	tools.RegisterWithDef("nostr_dvm_request", toolbuiltin.NostrDVMRequestTool(nostrToolOpts), toolbuiltin.NostrDVMRequestDef)
	tools.RegisterWithDef("nostr_publish_batch", toolbuiltin.NostrPublishBatchTool(nostrToolOpts), toolbuiltin.NostrPublishBatchDef)
	tools.RegisterWithDef("nostr_compose", toolbuiltin.NostrComposeTool(), toolbuiltin.NostrComposeDef)
	tools.RegisterWithDef("nostr_zap_send", toolbuiltin.NostrZapSendTool(nostrToolOpts), toolbuiltin.NostrZapSendDef)
	tools.RegisterWithDef("nostr_zap_list", toolbuiltin.NostrZapListTool(nostrToolOpts), toolbuiltin.NostrZapListDef)

	// NIP-51 list management tools (allowlists, blocklists, mute lists, etc.)
	listStore := nip51.NewListStore()
	listToolOpts := toolbuiltin.NostrListToolOpts{
		HubFunc: func() *nostruntime.NostrHub { return controlHub },
		Keyer:   controlKeyer,
		Relays:  cfg.Relays,
		Store:   listStore,
	}
	toolbuiltin.RegisterListTools(tools, listToolOpts)
	toolbuiltin.RegisterNostrListSemanticTools(tools, listToolOpts)

	// NIP-38 status tool — uses controlPresenceHeartbeat38 which is set after DM bus starts.
	// Wire via closure so it picks up the global after initialization.
	tools.RegisterWithDef("nostr_status_set", func(ctx context.Context, args map[string]any) (string, error) {
		return toolbuiltin.NostrStatusTool(toolbuiltin.NostrStatusToolOpts{
			Heartbeat: controlPresenceHeartbeat38,
		})(ctx, args)
	}, toolbuiltin.NostrStatusSetDef)

	// ── Additional NIP tools (NIP-09/22/23/25/50/78/94) ────────────────────
	toolbuiltin.RegisterNIPTools(tools, nostrToolOpts)

	// ── NIP-C7 Chat tools ──────────────────────────────────────────────────
	toolbuiltin.RegisterChatTools(tools, nostrToolOpts)

	// ── Relay-as-memory tools ───────────────────────────────────────────────
	toolbuiltin.RegisterRelayMemoryTools(tools, toolbuiltin.RelayMemoryToolOpts{
		HubFunc:      func() *nostruntime.NostrHub { return controlHub },
		Keyer:        controlKeyer,
		Relays:       cfg.Relays,
		PublishGuard: publishGuard,
	})

	// ── ContextVM tools ─────────────────────────────────────────────────────
	toolbuiltin.RegisterContextVMTools(tools, toolbuiltin.ContextVMToolOpts{
		HubFunc: func() *nostruntime.NostrHub { return controlHub },
		Keyer:   controlKeyer,
		Relays:  cfg.Relays,
	})

	// ── GRASP NIP-34 git repository tools ───────────────────────────────────
	toolbuiltin.RegisterGRASPTools(tools, toolbuiltin.GRASPToolOpts{
		HubFunc: func() *nostruntime.NostrHub { return controlHub },
		Keyer:   controlKeyer,
		Relays:  cfg.Relays,
	})

	// ── Loom compute marketplace tools ──────────────────────────────────────
	toolbuiltin.RegisterLoomTools(tools, toolbuiltin.LoomToolOpts{
		HubFunc: func() *nostruntime.NostrHub { return controlHub },
		Keyer:   controlKeyer,
		Relays:  cfg.Relays,
	})

	// ── Cashu NUT ecash tools ───────────────────────────────────────────────
	{
		var nutsDefaultMint string
		if configState != nil {
			if nutsExtra, ok := configState.Get().Extra["nuts"].(map[string]any); ok {
				nutsDefaultMint, _ = nutsExtra["mint_url"].(string)
			}
		}
		if nutsDefaultMint == "" {
			nutsDefaultMint = "https://legend.lnbits.com/cashu/api/v1/Ah9J3tb5bI0ZLI-e0iSZ0g" // well-known public mint
		}
		toolbuiltin.RegisterNutsTools(tools, toolbuiltin.NutsToolOpts{
			DefaultMintURL: nutsDefaultMint,
		})
		log.Printf("Cashu NUT tools active (default mint: %s)", nutsDefaultMint)
	}

	// ── NWC (NIP-47) Nostr Wallet Connect tools ────────────────────────────
	// Enabled when extra.nwc.uri is set in config. Allows agents to interact
	// with any NWC-compatible lightning wallet (Alby, LNbits NWC, etc.).
	{
		var nwcUri string
		if configState != nil {
			if nwcExtra, ok := configState.Get().Extra["nwc"].(map[string]any); ok {
				nwcUri, _ = nwcExtra["uri"].(string)
			}
		}
		toolbuiltin.RegisterNWCTools(tools, toolbuiltin.NWCToolOpts{
			HubFunc: func() *nostruntime.NostrHub { return controlHub },
			Keyer:   controlKeyer,
			NWCUri:  nwcUri,
			Relays:  cfg.Relays,
			Timeout: 30 * time.Second,
		})
		if nwcUri != "" {
			log.Printf("NWC tools active (wallet connected)")
		} else {
			log.Printf("NWC tools registered (no wallet configured — set extra.nwc.uri to enable)")
		}
	}

	// ── Blossom blob storage tools (BUD-01 through BUD-05) ──────────────────
	// Enabled by default; default server can be configured via extra.blossom.server.
	{
		var blossomServer string
		if configState != nil {
			if blossomExtra, ok := configState.Get().Extra["blossom"].(map[string]any); ok {
				blossomServer, _ = blossomExtra["server"].(string)
			}
		}
		if blossomServer == "" {
			blossomServer = "https://blossom.band" // community default
		}
		toolbuiltin.RegisterBlossomTools(tools, toolbuiltin.BlossomToolOpts{
			Keyer:         controlKeyer,
			DefaultServer: blossomServer,
		})
		log.Printf("Blossom tools active (default server: %s)", blossomServer)
	}

	// nostr_watch / nostr_unwatch / nostr_watch_list — persistent subscriptions.
	// Delivery fires back into the DM pipeline via dmRunAgentTurnRef which is
	// populated once dmRunAgentTurn is defined below.
	watchRegistry = toolbuiltin.NewWatchRegistry()
	watchRegistry.SetHubFunc(func() *nostruntime.NostrHub { return controlHub })
	watchDeliveryCtx, watchDeliveryCancel := context.WithCancel(ctx)
	defer watchDeliveryCancel()
	relayFilterInFlight := newEventInFlightRegistry()
	var dmRunAgentTurnRef func(ctx context.Context, sessionID, senderID, text, eventID string, createdAt int64, replyFn func(context.Context, string) error, overrideAgentID string, constraints turnToolConstraints)
	watchDeliver := func(sessionID, name string, event map[string]any) {
		if dmRunAgentTurnRef == nil {
			return
		}
		b, _ := json.Marshal(event)
		text := fmt.Sprintf("[watch:%s] %s", name, string(b))
		eventID, createdAt := nostrWatchDeliveryMeta(name, event)
		if eventID != "" && transcriptRepo != nil {
			exists, err := transcriptRepo.HasEntry(watchDeliveryCtx, sessionID, eventID)
			if err != nil {
				log.Printf("watch delivery dedupe check failed watch=%s event=%s err=%v", name, eventID, err)
			} else if exists {
				return
			}
		}
		dmRunAgentTurnRef(watchDeliveryCtx, sessionID, sessionID, text, eventID, createdAt, nil, "", turnToolConstraints{})
	}
	// saveWatches persists the active watch specs to the state store so they
	// survive daemon restarts.  Runs asynchronously to avoid blocking tool
	// calls on relay publish latency.
	saveWatches := func() {
		specs := watchRegistry.Specs()
		raw, err := json.Marshal(specs)
		if err != nil {
			log.Printf("watches save: marshal: %v", err)
			return
		}
		go func() {
			if _, err := docsRepo.PutWatches(ctx, raw); err != nil {
				log.Printf("watches save: put: %v", err)
			}
		}()
	}
	// Wrap nostr_watch to persist after creation.
	rawWatchTool := toolbuiltin.NostrWatchTool(nostrToolOpts, watchRegistry, watchDeliver)
	tools.RegisterWithDef("nostr_watch", func(toolCtx context.Context, args map[string]any) (string, error) {
		result, err := rawWatchTool(toolCtx, args)
		if err == nil {
			saveWatches()
		}
		return result, err
	}, toolbuiltin.NostrWatchDef)
	// Wrap nostr_unwatch to persist after removal.
	rawUnwatchTool := toolbuiltin.NostrUnwatchTool(watchRegistry)
	tools.RegisterWithDef("nostr_unwatch", func(toolCtx context.Context, args map[string]any) (string, error) {
		result, err := rawUnwatchTool(toolCtx, args)
		if err == nil {
			saveWatches()
		}
		return result, err
	}, toolbuiltin.NostrUnwatchDef)
	tools.RegisterWithDef("nostr_watch_list", toolbuiltin.NostrWatchListTool(watchRegistry), toolbuiltin.NostrWatchListDef)

	// file_watch_add / file_watch_remove / file_watch_list — filesystem change subscriptions.
	fileWatchRegistry := toolbuiltin.NewFileWatchRegistry()
	fileWatchDeliver := func(sessionID, name string, event map[string]any) {
		if dmRunAgentTurnRef == nil {
			return
		}
		b, _ := json.Marshal(event)
		text := fmt.Sprintf("[file_watch:%s] %s", name, string(b))
		eventID, createdAt := nostrWatchDeliveryMeta("file_watch:"+name, event)
		if eventID != "" && transcriptRepo != nil {
			exists, err := transcriptRepo.HasEntry(watchDeliveryCtx, sessionID, eventID)
			if err != nil {
				log.Printf("file watch delivery dedupe check failed watch=%s event=%s err=%v", name, eventID, err)
			} else if exists {
				return
			}
		}
		dmRunAgentTurnRef(watchDeliveryCtx, sessionID, sessionID, text, eventID, createdAt, nil, "", turnToolConstraints{})
	}
	tools.RegisterWithDef("file_watch_add", toolbuiltin.FileWatchAddTool(fileWatchRegistry, fileWatchDeliver), toolbuiltin.FileWatchAddDef)
	tools.RegisterWithDef("file_watch_remove", toolbuiltin.FileWatchRemoveTool(fileWatchRegistry), toolbuiltin.FileWatchRemoveDef)
	tools.RegisterWithDef("file_watch_list", toolbuiltin.FileWatchListTool(fileWatchRegistry), toolbuiltin.FileWatchListDef)

	// ── System / capability tools ───────────────────────────────────────────
	// current_time: returns UTC timestamp so the agent always knows "now".
	tools.RegisterWithDef("current_time", toolbuiltin.CurrentTimeTool, toolbuiltin.CurrentTimeDef)
	// nostr_sign: sign an event without publishing (returns signed JSON).
	tools.RegisterWithDef("nostr_sign", toolbuiltin.NostrSignTool(toolbuiltin.NostrSignOpts{
		Keyer: controlKeyer,
	}), toolbuiltin.NostrSignDef)
	// my_identity: agent self-awareness — name, nostr pubkey, model.
	toolbuiltin.SetIdentityInfo(toolbuiltin.IdentityInfo{
		Name:   "main",
		Pubkey: pubkey,
		NPub:   toolbuiltin.NostrNPubFromHex(pubkey),
		Model:  strings.TrimSpace(os.Getenv("METIQ_AGENT_PROVIDER")),
	})
	tools.RegisterWithDef("my_identity", toolbuiltin.MyIdentityTool, toolbuiltin.MyIdentityDef)
	tools.RegisterWithDef("runtime_observe", toolbuiltin.RuntimeObserveTool, toolbuiltin.RuntimeObserveDef)
	// bash_exec: shell command execution (gated by exec approval policy middleware).
	tools.RegisterWithDef("bash_exec", toolbuiltin.BashExecTool, toolbuiltin.BashExecDef)
	// Git tools: structured status, diff, log, and blame output.
	tools.RegisterWithDef("git_status", toolbuiltin.GitStatusTool, toolbuiltin.GitStatusDef)
	tools.RegisterWithDef("git_diff", toolbuiltin.GitDiffTool, toolbuiltin.GitDiffDef)
	tools.RegisterWithDef("git_log", toolbuiltin.GitLogTool, toolbuiltin.GitLogDef)
	tools.RegisterWithDef("git_blame", toolbuiltin.GitBlameTool, toolbuiltin.GitBlameDef)
	// Test runner: structured go test -json results.
	tools.RegisterWithDef("test_run", toolbuiltin.TestRunTool, toolbuiltin.TestRunDef)
	// Process handles: spawn/read/send/kill background processes.
	processReg := toolbuiltin.NewProcessRegistry()
	defer processReg.Shutdown()
	tools.RegisterWithDef("process_spawn", toolbuiltin.ProcessSpawnTool(processReg), toolbuiltin.ProcessSpawnDef)
	tools.RegisterWithDef("process_read", toolbuiltin.ProcessReadTool(processReg), toolbuiltin.ProcessReadDef)
	tools.RegisterWithDef("process_send", toolbuiltin.ProcessSendTool(processReg), toolbuiltin.ProcessSendDef)
	tools.RegisterWithDef("process_kill", toolbuiltin.ProcessKillTool(processReg), toolbuiltin.ProcessKillDef)
	tools.RegisterWithDef("process_list", toolbuiltin.ProcessListTool(processReg), toolbuiltin.ProcessListDef)
	tools.RegisterWithDef("process_exec", toolbuiltin.ProcessExecTool, toolbuiltin.ProcessExecDef)
	// Filesystem tools: read/write files, list and create directories.
	// Relative paths are resolved against the agent's workspace directory.
	fsOpts := toolbuiltin.FilesystemOpts{
		WorkspaceDir: func() string {
			return workspace.ResolveWorkspaceDir(configState.Get(), "")
		},
	}
	tools.RegisterWithDef("read_file", toolbuiltin.ReadFileTool(fsOpts), toolbuiltin.ReadFileDef)
	tools.RegisterWithDef("write_file", toolbuiltin.WriteFileTool(fsOpts), toolbuiltin.WriteFileDef)
	tools.RegisterWithDef("file_edit", toolbuiltin.FileEditTool(fsOpts), toolbuiltin.FileEditDef)
	tools.RegisterWithDef("list_dir", toolbuiltin.ListDirTool(fsOpts), toolbuiltin.ListDirDef)
	tools.RegisterWithDef("make_dir", toolbuiltin.MakeDirTool(fsOpts), toolbuiltin.MakeDirDef)
	tools.RegisterWithDef("file_tree", toolbuiltin.FileTreeTool(fsOpts), toolbuiltin.FileTreeDef)
	tools.RegisterWithDef("grep_search", toolbuiltin.GrepSearchTool(fsOpts), toolbuiltin.GrepSearchDef)
	tools.RegisterWithDef("diff_apply", toolbuiltin.DiffApplyTool(fsOpts), toolbuiltin.DiffApplyDef)
	// LSP code intelligence: definition, references, hover, diagnostics, symbols.
	lspReg := toolbuiltin.NewLSPRegistry()
	defer lspReg.Shutdown()
	tools.RegisterWithDef("lsp_query", toolbuiltin.LSPQueryTool(lspReg, fsOpts), toolbuiltin.LSPQueryDef)
	// Sandbox execution: compile & run code with isolation and resource limits.
	tools.RegisterWithDef("sandbox_exec", toolbuiltin.SandboxExecTool(), toolbuiltin.SandboxExecDef)
	// Taskfile execution: preferred backend for repeatable multi-step local workflows.
	if _, err := exec.LookPath("task"); err == nil {
		tools.RegisterTool("task", agent.ToolRegistration{
			Func: toolbuiltin.TaskTool(fsOpts),
			Descriptor: agent.ToolDescriptor{
				Name:            toolbuiltin.TaskDef.Name,
				Description:     toolbuiltin.TaskDef.Description,
				Parameters:      toolbuiltin.TaskDef.Parameters,
				InputJSONSchema: toolbuiltin.TaskDef.InputJSONSchema,
				ParamAliases:    toolbuiltin.TaskDef.ParamAliases,
				Origin:          agent.ToolOrigin{Kind: agent.ToolOriginKindBuiltin},
				Traits:          agent.ToolTraits{Destructive: true},
			},
			ProviderVisible: true,
			Traits: agent.ToolTraitResolvers{
				IsReadOnly:    toolbuiltin.TaskActionReadOnly,
				IsDestructive: toolbuiltin.TaskActionDestructive,
			},
		})
	} else {
		log.Printf("task tool disabled: go-task binary not found in PATH")
	}
	// Tool chains / macros: composable multi-tool workflows.
	chainReg := toolbuiltin.NewChainRegistry()
	tools.RegisterWithDef("chain_define", toolbuiltin.ChainDefineTool(chainReg), toolbuiltin.ChainDefineDef)
	tools.RegisterWithDef("chain_run", toolbuiltin.ChainRunTool(chainReg, tools), toolbuiltin.ChainRunDef)
	tools.RegisterWithDef("chain_list", toolbuiltin.ChainListTool(chainReg), toolbuiltin.ChainListDef)
	// task queue: persistent structured work-item management.
	{
		home, _ := os.UserHomeDir()
		taskPath := filepath.Join(home, ".metiq", "tasks.json")
		if err := toolbuiltin.InitTaskStore(taskPath); err != nil {
			log.Printf("task store init (non-fatal): %v", err)
		}
	}
	tools.RegisterWithDef("task_add", toolbuiltin.TaskAddTool, toolbuiltin.TaskAddDef)
	tools.RegisterWithDef("task_list", toolbuiltin.TaskListTool, toolbuiltin.TaskListDef)
	tools.RegisterWithDef("task_update", toolbuiltin.TaskUpdateTool, toolbuiltin.TaskUpdateDef)
	tools.RegisterWithDef("task_remove", toolbuiltin.TaskRemoveTool, toolbuiltin.TaskRemoveDef)

	agentRuntime, err := agent.NewRuntimeFromEnv(tools)
	if err != nil {
		log.Fatalf("init agent runtime: %v", err)
	}

	// image: analyse an image via the configured vision-capable model.
	tools.Register("image", toolbuiltin.ImageTool(agentRuntime, toolbuiltin.ImageOpts{}))

	// tts: convert text to speech — registered after ttsMgr is set up.
	// See the deferred registration below (after ttsMgr = ttspkg.NewManager()).
	validateRuntimeConfigDoc := normalizeAndValidateRuntimeConfigDoc
	var startupFileConfig *state.ConfigDoc
	startupFileDefinesControl := false
	cfgPath := strings.TrimSpace(configFilePath)
	if cfgPath == "" {
		if def, cfgErr := config.DefaultConfigPath(); cfgErr == nil {
			cfgPath = def
		}
	}
	if cfgPath != "" && config.ConfigFileExists(cfgPath) {
		startupFileDefinesControl = configFileDeclaresTopLevelKey(cfgPath, "control")
		if fileDoc, fileErr := config.LoadConfigFile(cfgPath); fileErr == nil {
			if fileDoc, validateErr := validateRuntimeConfigDoc(fileDoc); validateErr != nil {
				log.Printf("config: startup file rejected invalid config (%v); using persisted state", validateErr)
			} else {
				startupFileConfig = &fileDoc
				log.Printf("config: startup file candidate %s", cfgPath)
			}
		} else {
			log.Printf("config: startup file load failed (%v); using persisted state", fileErr)
		}
	}

	runtimeCfg, err := ensureRuntimeConfig(ctx, docsRepo, cfg.Relays, pubkey, startupFileConfig, startupFileDefinesControl)
	if err != nil {
		log.Fatalf("load runtime config: %v", err)
	}
	codec.SetEncrypt(runtimeCfg.StorageEncryptEnabled())
	configState = newRuntimeConfigStore(runtimeCfg)
	controlRuntimeConfig = configState
	setRuntimeIdentityInfo(runtimeCfg, pubkey)

	// ── gRPC tool provider integration ───────────────────────────────────
	grpcProviderCtl := &grpcProviderController{}
	defer grpcProviderCtl.close()
	grpcProviderCtl.reconcile(ctx, tools, runtimeCfg, "initialization")

	// ── MCP (Model Context Protocol) client integration ─────────────────
	// Load MCP config from extra.mcp and register discovered tools after the
	// secrets/auth controller is available.
	mcpManager := mcppkg.NewManager()
	toolbuiltin.RegisterMCPResourceTools(tools, toolbuiltin.MCPResourceToolOpts{
		Manager: func() *mcppkg.Manager { return mcpManager },
	})
	toolbuiltin.RegisterMCPPromptTools(tools, toolbuiltin.MCPPromptToolOpts{
		Manager: func() *mcppkg.Manager { return mcpManager },
	})
	defer func() {
		if mcpManager != nil {
			_ = mcpManager.Close()
		}
	}()

	// Resolve memory backend from live config (Extra["memory"]["backend"]).
	// SQLite is the preferred local retrieval/index layer; the JSON inverted
	// index remains as a compatibility fallback inside HybridIndex.
	{
		memoryBackendName := "sqlite"
		if mExtra, ok := configState.Get().Extra["memory"].(map[string]any); ok {
			if beName, ok2 := mExtra["backend"].(string); ok2 && strings.TrimSpace(beName) != "" {
				memoryBackendName = strings.TrimSpace(beName)
			}
		}
		memoryBackendPath := ""
		memoryExtra, _ := configState.Get().Extra["memory"].(map[string]any)
		if memoryExtra != nil {
			if p, _ := memoryExtra["path"].(string); strings.TrimSpace(p) != "" {
				memoryBackendPath = strings.TrimSpace(p)
			}
			qdrantURL, _ := memoryExtra["url"].(string)
			ollamaURL, _ := memoryExtra["ollama_url"].(string)
			collection, _ := memoryExtra["collection"].(string)
			// qdrant path format: "qdrantURL|ollamaURL|collection"
			if qdrantURL != "" && strings.EqualFold(memoryBackendName, "qdrant") {
				memoryBackendPath = qdrantURL + "|" + ollamaURL + "|" + collection
			}
		}
		var be memory.Backend
		var beErr error
		if strings.EqualFold(memoryBackendName, "sqlite") {
			backupEnabled := true
			if v, ok := memoryExtra["backup_enabled"].(bool); ok {
				backupEnabled = v
			}
			backupRetentionWeeks := 4
			switch v := memoryExtra["backup_retention_weeks"].(type) {
			case float64:
				if v > 0 {
					backupRetentionWeeks = int(v)
				}
			case int:
				if v > 0 {
					backupRetentionWeeks = v
				}
			}
			workspaceDir := workspace.ResolveWorkspaceDir(configState.Get(), "")
			rebuildRoots := []string{}
			if workspaceDir != "" {
				rebuildRoots = append(rebuildRoots,
					filepath.Join(workspaceDir, ".metiq", "agent-memory"),
					filepath.Join(workspaceDir, ".metiq", "agent-memory-local"),
				)
			}
			if home, homeErr := os.UserHomeDir(); homeErr == nil {
				rebuildRoots = append(rebuildRoots, filepath.Join(home, ".metiq", "agent-memory"))
			}
			sqliteOpts := memory.SQLiteRecoveryOptions{
				BackupEnabled:        backupEnabled,
				BackupEnabledSet:     true,
				BackupRetentionWeeks: backupRetentionWeeks,
				RebuildDurableRoots:  rebuildRoots,
				RebuildWorkspaceDir:  workspaceDir,
				Logf:                 log.Printf,
			}
			sqliteMemoryBackend, beErr = memory.OpenSQLiteBackendWithRecoveryOptions(memoryBackendPath, sqliteOpts)
			be = sqliteMemoryBackend
		} else {
			be, beErr = memory.OpenBackend(memoryBackendName, memoryBackendPath)
		}
		if beErr != nil {
			log.Printf("memory backend %q not available (%v); using json-fts", memoryBackendName, beErr)
		} else {
			log.Printf("memory backend: %s path=%q", memoryBackendName, memoryBackendPath)
			if migrator, ok := be.(interface{ MigrateFromJSONIndex(string) error }); ok {
				if jsonPath, pathErr := memory.DefaultIndexPath(); pathErr == nil {
					if migErr := migrator.MigrateFromJSONIndex(jsonPath); migErr != nil {
						log.Printf("memory sqlite migration warning: %v", migErr)
					}
				}
			}
			memoryIndex = memory.NewHybridIndex(baseMemoryIndex, be)
			if sqliteMemoryBackend != nil {
				backupCtx, stopWeeklyBackups := context.WithCancel(ctx)
				weeklyBackupDone := memory.StartWeeklySQLiteBackups(backupCtx, sqliteMemoryBackend)
				defer func() {
					stopWeeklyBackups()
					<-weeklyBackupDone
					if err := sqliteMemoryBackend.Close(); err != nil {
						log.Printf("memory sqlite clean shutdown warning: %v", err)
					}
				}()
			}
		}
	}
	startMemoryMaintenance(ctx, memoryIndex, configState.Get)

	var contextEngineName string
	// Initialise pluggable context engine from config (Extra["context_engine"]).
	// The engine ingests and assembles conversation history for every agent session.
	{
		engineName := "windowed"
		if ceVal, ok := configState.Get().Extra["context_engine"].(string); ok && strings.TrimSpace(ceVal) != "" {
			engineName = strings.TrimSpace(ceVal)
		}
		engineOpts := map[string]any{}
		if ceExtra, ok := configState.Get().Extra["context_engine_opts"].(map[string]any); ok {
			engineOpts = ceExtra
		}
		eng, engErr := ctxengine.NewEngine(engineName, "global", engineOpts)
		if engErr != nil {
			log.Printf("context engine %q unavailable (%v); falling back to 'windowed'", engineName, engErr)
			eng, _ = ctxengine.NewEngine("windowed", "global", engineOpts)
			engineName = "windowed"
		}
		controlContextEngine = eng
		contextEngineName = engineName
		log.Printf("context engine: %s", engineName)
	}

	// Resolve live config file path (for disk↔Nostr sync and hot-reload).
	if configFilePath == "" {
		if def, err2 := config.DefaultConfigPath(); err2 == nil {
			configFilePath = def
		}
	}
	if configFilePath != "" {
		validatedPath, err := config.ValidateConfigFilePath(configFilePath)
		if err != nil {
			log.Fatalf("invalid --config path: %v", err)
		}
		configFilePath = validatedPath
	}

	// Load Goja (JS) plugins from config and register their tools.
	pluginHost := pluginmanager.BuildHost(configState, agentRuntime)
	pluginMgr := pluginmanager.New(pluginHost)
	controlHookInvoker = pluginhooks.NewHookInvoker(nil, nil)
	if loadErr := pluginMgr.Load(ctx, configState.Get()); loadErr != nil {
		log.Printf("plugin manager load warning: %v", loadErr)
	}
	pluginMgr.RegisterTools(tools)

	// OpenClaw plugin host + service registry (Phase 6: background services).
	var pluginServiceMgr *pluginservice.ServiceManager
	var openClawHost *pluginruntime.OpenClawPluginHost
	var unifiedPlugins *pluginregistry.UnifiedRegistry
	{
		host, hostErr := pluginruntime.NewOpenClawPluginHost(ctx)
		if hostErr != nil {
			log.Printf("openclaw host disabled: %v", hostErr)
		} else {
			openClawHost = host
			unified := pluginregistry.NewUnifiedRegistry()
			unifiedPlugins = unified
			controlHookInvoker = pluginhooks.NewHookInvoker(unified.Hooks(), openClawHost)
			for _, installPath := range configuredOpenClawPluginPaths(configState.Get()) {
				result, err := openClawHost.LoadPluginResult(ctx, installPath, nil)
				if err != nil {
					log.Printf("openclaw plugin load failed (%s): %v", installPath, err)
					continue
				}
				if err := unified.RegisterOpenClawLoadResult(result); err != nil {
					log.Printf("openclaw registration failed (%s): %v", result.PluginID, err)
				}
			}
			pluginServiceMgr = pluginservice.NewManager(unified.Services(), openClawHost)
			if err := pluginServiceMgr.StartAll(ctx); err != nil {
				log.Printf("plugin service auto-start warning: %v", err)
			}
		}
	}

	// Media generation runtimes (Phase 8): built-in image providers plus OpenClaw plugin-backed providers.
	imageGenRegistry := imagegen.NewRegistry()
	_ = imageGenRegistry.Register(imagegen.NewOpenAIProvider())
	_ = imageGenRegistry.Register(imagegen.NewMidjourneyProvider())
	_ = imageGenRegistry.Register(imagegen.NewStableDiffusionProvider())
	videoGenRegistry := videogen.NewRegistry()
	_ = videoGenRegistry.Register(videogen.NewRunwayProvider())
	_ = videoGenRegistry.Register(videogen.NewPikaProvider())
	musicGenRegistry := musicgen.NewRegistry()
	_ = musicGenRegistry.Register(musicgen.NewSunoProvider())
	_ = musicGenRegistry.Register(musicgen.NewUdioProvider())
	if openClawHost != nil && unifiedPlugins != nil {
		for _, meta := range unifiedPlugins.ImageGenProviders().List() {
			_ = imageGenRegistry.Register(imagegen.NewPluginProvider(meta.ID, meta.Raw, openClawHost))
		}
		for _, meta := range unifiedPlugins.VideoGenProviders().List() {
			_ = videoGenRegistry.Register(videogen.NewPluginProvider(meta.ID, meta.Raw, openClawHost))
		}
		for _, meta := range unifiedPlugins.MusicGenProviders().List() {
			_ = musicGenRegistry.Register(musicgen.NewPluginProvider(meta.ID, meta.Raw, openClawHost))
		}
	}
	imageGenRuntime := imagegen.NewRuntime(imageGenRegistry, func() string { return mediaGenerationOutputDir(configState.Get(), "images") })
	videoGenRuntime := videogen.NewRuntime(
		videoGenRegistry,
		func() string { return mediaGenerationOutputDir(configState.Get(), "videos") },
		mediaGenerationDuration(configState.Get(), "video_poll_interval_ms", 2*time.Second),
		mediaGenerationDuration(configState.Get(), "video_max_wait_ms", 5*time.Minute),
	)
	musicGenRuntime := musicgen.NewRuntime(musicGenRegistry, func() string { return mediaGenerationOutputDir(configState.Get(), "music") })
	tools.RegisterWithDef("image_generate", imagegen.Tool(imageGenRuntime), toolbuiltin.ImageGenerateDef)
	tools.RegisterWithDef("video_generate", videogen.Tool(videoGenRuntime), toolbuiltin.VideoGenerateDef)
	tools.RegisterWithDef("music_generate", musicgen.Tool(musicgen.ToolOptions{Runtime: musicGenRuntime, MediaPrefix: toolbuiltin.MediaPrefix}), toolbuiltin.MusicGenerateDef)

	// ── Hooks system ─────────────────────────────────────────────────────────
	hooksMgr := hookspkg.NewManager()
	// Load bundled hooks from the bundled hooks directory.
	if bundledHooksDir := hookspkg.BundledHooksDir(); bundledHooksDir != "" {
		if bundledHooks, err := hookspkg.ScanDir(bundledHooksDir, hookspkg.SourceBundled); err == nil {
			for _, h := range bundledHooks {
				hooksMgr.Register(h)
			}
		}
	}
	// Load managed hooks from ~/.metiq/hooks/.
	if managedHooksDir := hookspkg.ManagedHooksDir(); managedHooksDir != "" {
		if managedHooks, err := hookspkg.ScanDir(managedHooksDir, hookspkg.SourceManaged); err == nil {
			for _, h := range managedHooks {
				hooksMgr.Register(h)
			}
		}
	}
	// Load workspace hooks from the agent's workspace hooks/ subdirectory.
	if wkspHooksDir := filepath.Join(workspace.ResolveWorkspaceDir(configState.Get(), ""), "hooks"); wkspHooksDir != "" {
		if wkspHooks, err := hookspkg.ScanDir(wkspHooksDir, hookspkg.SourceWorkspace); err == nil {
			for _, h := range wkspHooks {
				hooksMgr.Register(h)
			}
		}
	}
	// Wire bundled Go handlers.
	hookspkg.RegisterBundledHandlers(hooksMgr, hookspkg.BundledHandlerOpts{
		WorkspaceDir: func() string {
			return workspace.ResolveWorkspaceDir(configState.Get(), "")
		},
	})
	// Attach shell handlers for any managed/workspace hooks that have handler.sh
	// but no bundled Go implementation.
	hookspkg.AttachShellHandlers(hooksMgr)

	// ── Secrets store ─────────────────────────────────────────────────────────
	secretsStore := secretspkg.NewStore(nil) // uses ~/.metiq/.env by default
	if _, warns := secretsStore.Reload(); len(warns) > 0 {
		for _, w := range warns {
			log.Printf("secrets: %s", w)
		}
	}
	mcpAuthController := newMCPAuthController(&mcpManager, tools, secretsStore, func() state.ConfigDoc { return configState.Get() })
	controlMCPOps = newMCPOpsController(&mcpManager, tools, mcpAuthController, configState, docsRepo)
	mcpAuthController.InstallOnManager(mcpManager)
	{
		mcpCfg := resolveMCPConfigWithDefaults(configState.Get(), fsOpts.WorkspaceDir())
		applyMCPConfigAndReconcile(ctx, &mcpManager, tools, mcpCfg, "initialization")
	}

	// TTS manager — initialise before the server starts so method handlers have it.
	ttsMgr := ttspkg.NewManager()
	// Register the tts agent tool now that the manager is initialised.
	tools.Register("tts", toolbuiltin.TTSTool(ttsMgr))

	// Canvas host — shared store for agent-rendered UI content.
	canvasHost := canvas.NewHost()
	canvasHost.Subscribe(func(ev canvas.UpdateEvent) {
		emitControlWSEvent(gatewayws.EventCanvasUpdate, gatewayws.CanvasUpdatePayload{
			TS:          time.Now().UnixMilli(),
			CanvasID:    ev.CanvasID,
			ContentType: ev.ContentType,
			Data:        ev.Data,
		})
	})
	tools.RegisterWithDef("canvas_update", func(_ context.Context, args map[string]any) (string, error) {
		id := agent.ArgString(args, "canvas_id")
		contentType := agent.ArgString(args, "content_type")
		data := agent.ArgString(args, "data")
		if id == "" || contentType == "" {
			return "", fmt.Errorf("canvas_update: canvas_id and content_type are required")
		}
		if err := canvasHost.UpdateCanvas(id, contentType, data); err != nil {
			return "", fmt.Errorf("canvas_update: %w", err)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "canvas_id": id, "content_type": contentType})
		return string(b), nil
	}, toolbuiltin.CanvasUpdateDef)

	// Media transcriber — auto-selected from configured API keys, or a specific
	// backend from the config's media_understanding.transcriber field.
	// Priority: config override → OPENAI_API_KEY → GROQ_API_KEY → DEEPGRAM_API_KEY.
	var mediaTranscriber mediapkg.Transcriber
	if t := configuredTranscriber(configState.Get()); t != nil {
		mediaTranscriber = t
	} else {
		mediaTranscriber = mediapkg.DefaultTranscriber()
	}
	if mediaTranscriber != nil {
		log.Printf("media transcriber: configured (type=%T)", mediaTranscriber)
	} else {
		log.Printf("media transcriber: none configured (audio attachments will not be transcribed)")
	}

	checkpoint, err := ensureIngestCheckpoint(ctx, docsRepo)
	if err != nil {
		log.Fatalf("load ingest checkpoint: %v", err)
	}
	tracker := newIngestTracker(checkpoint)
	// Log checkpoint state at startup so future-dated or stale checkpoints are
	// immediately visible in logs.  A lastUnix significantly ahead of wall
	// clock means all inbound DMs will be silently dropped until the clock
	// catches up.
	{
		delta := checkpoint.LastUnix - time.Now().Unix()
		label := "behind"
		if delta > 0 {
			label = "AHEAD — all new DMs will be dropped until clock catches up!"
		} else {
			delta = -delta
		}
		log.Printf("dm checkpoint: last_unix=%d last_event=%s recent_ids=%d (%ds %s wall clock)",
			checkpoint.LastUnix, checkpoint.LastEvent, len(checkpoint.RecentEventIDs), delta, label)
	}
	memoryCheckpoint, err := ensureMemoryIndexCheckpoint(ctx, docsRepo)
	if err != nil {
		log.Fatalf("load memory index checkpoint: %v", err)
	}
	memoryTracker := newMemoryIndexTracker(memoryCheckpoint)
	controlCheckpoint, err := ensureControlCheckpoint(ctx, docsRepo)
	if err != nil {
		log.Fatalf("load control checkpoint: %v", err)
	}
	controlTracker := newControlTracker(controlCheckpoint)
	chatCancels := newChatAbortRegistry()
	agentJobs := newAgentJobRegistry()
	nodeInvocations := newNodeInvocationRegistry()
	nodePending := nodepending.New()
	cronJobs := newCronRegistry()
	execApprovals := newExecApprovalsRegistry()
	wizards := newWizardRegistry()
	ops := newOperationsRegistry()
	subagents := newSubagentRegistry()
	keyRings := agent.NewProviderKeyRingRegistry()
	acpPeers := acppkg.NewPeerRegistry()
	acpDispatcher := acppkg.NewDispatcher()
	controlACPPeers = acpPeers
	controlACPDispatcher = acpDispatcher
	if n := prepopulateACPPeersFromConfig(acpPeers, configState.Get()); n > 0 {
		log.Printf("acp peer registry pre-populated from config: %d peer(s)", n)
	}
	controlAgentRuntime = agentRuntime
	controlAgentJobs = agentJobs
	controlNodeInvocations = nodeInvocations
	controlCronJobs = cronJobs
	// Load persisted cron jobs from the state store so they survive restarts.
	if loadErr := cronJobs.Load(ctx, docsRepo); loadErr != nil {
		log.Printf("cron jobs load warning: %v", loadErr)
	} else {
		loaded := cronJobs.List(0)
		if len(loaded) > 0 {
			log.Printf("cron jobs restored from state store: %d jobs", len(loaded))
		}
	}
	controlExecApprovals = execApprovals
	controlWizards = wizards
	controlSubagents = subagents
	controlOps = ops
	ops.SyncHeartbeatConfig(configState.Get().Heartbeat)

	// ── Permission engine + exec approval middleware ─────────────────────────
	// The permission engine layers on top of tool profiles:
	// 1. tool_profile (minimal/coding/messaging/full) controls tool availability
	// 2. permissions section controls execution behavior (allow/ask/deny)
	// 3. approvals.tools is the legacy fallback when permissions isn't configured
	var permEngine *permissions.Engine
	{
		liveCfg := configState.Get()
		if liveCfg.Permissions.DefaultBehavior != "" || len(liveCfg.Permissions.Agents) > 0 || len(liveCfg.Permissions.Rules) > 0 {
			// Use new permission engine - audit logs go to current directory
			permBaseDir, _ := os.Getwd()
			var err error
			permEngine, err = permissions.NewEngineFromStateConfig(permBaseDir, liveCfg.Permissions)
			if err != nil {
				log.Printf("WARN: failed to initialize permission engine: %v; falling back to legacy approvals", err)
			} else {
				log.Printf("permission engine initialized with %d rules", permEngine.Stats().TotalRules)
			}
		}
	}

	// Hook the tool registry so that tools matching the configured approval list
	// pause execution, create an approval request, and wait for a human decision
	// before proceeding.  This implements OpenClaw parity for exec approval gating.
	{
		// Default tool names that require approval (legacy mode).
		// If Extra["approvals"]["tools"] is present (even empty), it REPLACES the defaults.
		// Set to [] for fully autonomous operation; omit the key to use defaults.
		// NOTE: If permissions section is configured, it takes precedence over this.
		defaultApprovalTools := []string{"bash", "shell", "exec", "run_command", "terminal", "sh", "bash_exec", "process_spawn", "process_send", "process_kill", "process_exec", "task", "git_status", "git_diff", "test_run"}
		approvalTools := make(map[string]bool)
		configOverride := false
		if aExtra, ok := configState.Get().Extra["approvals"].(map[string]any); ok {
			if _, hasKey := aExtra["tools"]; hasKey {
				configOverride = true
				switch v := aExtra["tools"].(type) {
				case string:
					for _, t := range strings.Split(v, ",") {
						if s := strings.TrimSpace(t); s != "" {
							approvalTools[s] = true
						}
					}
				case []any:
					for _, item := range v {
						if s, ok := item.(string); ok && s != "" {
							approvalTools[s] = true
						}
					}
				}
			}
		}
		if !configOverride {
			for _, t := range defaultApprovalTools {
				approvalTools[t] = true
			}
		}

		// Approval timeout defaults to 5 minutes; configurable via
		// Extra["approvals"]["timeout_ms"].
		approvalTimeoutMS := 5 * 60 * 1000 // 5 min in ms
		if aExtra, ok := configState.Get().Extra["approvals"].(map[string]any); ok {
			if ms, ok := aExtra["timeout_ms"].(float64); ok && ms > 0 {
				approvalTimeoutMS = int(ms)
			}
		}

		// Tool loop detection: per-session sliding-window history with three
		// detectors (generic repeat, no-progress polling, ping-pong) plus a
		// global circuit breaker. Ported from OpenClaw's tool-loop-detection.ts.
		loopRegistry := toolloop.NewRegistry()
		loopConfig := toolloop.DefaultConfig()
		tools.SetLoopDetection(loopRegistry, loopConfig)

		tools.SetMiddleware(func(ctx context.Context, call agent.ToolCall, next func(context.Context, agent.ToolCall) (string, error)) (string, error) {
			// Get agent ID from memory scope context (set during turn setup)
			agentID := ""
			if scope := agent.MemoryScopeFromContext(ctx); scope.AgentID != "" {
				agentID = scope.AgentID
			}

			// ── Check tool_profile first ────────────────────────────────
			// A "full" profile bypasses only the legacy approval list. Configured
			// permission rules still evaluate first because permissions layer on top
			// of profile-based tool availability and include critical safety denies.
			fullProfileBypassesLegacy := false
			if agentID != "" {
				liveCfg := configState.Get()
				foundAgent := false
				for _, ac := range liveCfg.Agents {
					if ac.ID == agentID {
						foundAgent = true
						if toolProfileBypassesApproval(ac.ToolProfile, permEngine != nil) {
							fullProfileBypassesLegacy = true
						}
						if ac.ToolProfile != "" {
							log.Printf("tool %s: agent %s has tool_profile=%q", call.Name, agentID, ac.ToolProfile)
						}
						break
					}
				}
				// Log when agent is not found in config (potential config mismatch)
				if !foundAgent && len(liveCfg.Agents) > 0 {
					var configAgentIDs []string
					for _, ac := range liveCfg.Agents {
						configAgentIDs = append(configAgentIDs, ac.ID)
					}
					log.Printf("tool %s: agent %q not found in config.agents (available: %v)", call.Name, agentID, configAgentIDs)
				}
			} else {
				// Log when agent ID is missing from context (context propagation issue)
				log.Printf("tool %s: no agent ID in context (memory scope may not be set)", call.Name)
			}

			// ── Permission engine check (new system) ────────────────────
			// If the permission engine is configured, use it instead of the legacy approval list.
			if permEngine != nil {
				// Build permission request - serialize args to string for content matching
				argsStr := ""
				if call.Args != nil {
					if argsBytes, err := json.Marshal(call.Args); err == nil {
						argsStr = string(argsBytes)
					}
				}
				req := permissions.NewToolRequest(call.Name, permissionCategoryForTool(tools, call.Name)).
					WithContent(argsStr).
					WithContext("", "", agentID, agent.SessionIDFromContext(ctx))
				if origin, originName := permissionOriginForTool(tools, call.Name); origin != "" || originName != "" {
					req = req.WithOrigin(origin, originName)
				}

				decision := permEngine.Evaluate(ctx, req)

				switch decision.Behavior {
				case permissions.BehaviorAllow:
					// Allowed - proceed without approval gate
					metricspkg.ToolCalls.Inc()
					return next(ctx, call)
				case permissions.BehaviorDeny:
					// Denied - block immediately
					metricspkg.ToolDenied.Inc()
					return "", fmt.Errorf("tool %q denied by permission rule: %s", call.Name, decision.Reason)
				case permissions.BehaviorAsk:
					// Fall through to approval gate below
				}
			}

			// ── Legacy approval gate ────────────────────────────────────
			// Re-read approval tool list from live config on every call so that
			// config hot-reload (SIGHUP or file change) takes effect immediately.
			// If Extra["approvals"]["tools"] is present it REPLACES the startup defaults.
			liveApprovalTools := approvalTools
			if aExtra, ok := configState.Get().Extra["approvals"].(map[string]any); ok {
				if _, hasKey := aExtra["tools"]; hasKey {
					live := make(map[string]bool)
					switch v := aExtra["tools"].(type) {
					case string:
						for _, t := range strings.Split(v, ",") {
							if s := strings.TrimSpace(t); s != "" {
								live[s] = true
							}
						}
					case []any:
						for _, item := range v {
							if s, ok := item.(string); ok && s != "" {
								live[s] = true
							}
						}
					}
					liveApprovalTools = live
				}
			}

			if fullProfileBypassesLegacy {
				log.Printf("tool %s allowed for agent %s (tool_profile=full)", call.Name, agentID)
				metricspkg.ToolCalls.Inc()
				return next(ctx, call)
			}

			// If permission engine is active and returned "ask", always go to approval gate.
			// Otherwise, only gate tools in the legacy approval list.
			needsApproval := permEngine != nil || liveApprovalTools[call.Name]
			if !needsApproval {
				metricspkg.ToolCalls.Inc()
				return next(ctx, call)
			}

			// Build an approval request.
			rec := execApprovals.Request(methods.ExecApprovalRequestRequest{
				Command:   call.Name,
				Args:      call.Args,
				TimeoutMS: approvalTimeoutMS,
			})

			// Emit a WS event so the UI / operator can see the pending request.
			emitControlWSEvent(gatewayws.EventExecApprovalRequested, gatewayws.ExecApprovalRequestedPayload{
				ID:     rec.ID,
				NodeID: rec.NodeID,
			})
			log.Printf("exec approval requested id=%s tool=%s", rec.ID, call.Name)

			// Block until decided, timed out, or context cancelled.
			decided, resolved, waitErr := execApprovals.WaitForDecision(ctx, rec.ID, approvalTimeoutMS)
			if waitErr != nil {
				return "", fmt.Errorf("exec approval wait error tool=%s: %w", call.Name, waitErr)
			}
			if !resolved || decided.Decision != "approve" {
				reason := decided.Reason
				if reason == "" {
					if !resolved {
						reason = "timed out waiting for approval"
					} else {
						reason = "denied"
					}
				}
				metricspkg.ToolDenied.Inc()
				log.Printf("exec approval denied id=%s tool=%s reason=%s", rec.ID, call.Name, reason)
				return "", fmt.Errorf("tool %q execution denied by approval gate: %s", call.Name, reason)
			}

			log.Printf("exec approval granted id=%s tool=%s", rec.ID, call.Name)
			metricspkg.ToolCalls.Inc()
			return next(ctx, call)
		})
	}

	// Initialise the version checker. update_check_url from config extra overrides the default.
	updateCheckURL := ""
	if u, ok := configState.Get().Extra["update_check_url"].(string); ok {
		updateCheckURL = strings.TrimSpace(u)
	}
	updateChecker := update.NewChecker(version, updateCheckURL)

	// Multi-agent runtime registry: maps agent IDs to their Runtime instances.
	// "main" / "" always resolves to agentRuntime (the default).
	agentRegistry := agent.NewAgentRuntimeRegistry(agentRuntime)
	sessionRouter := agent.NewAgentSessionRouter()
	controlAgentRegistry = agentRegistry
	controlSessionRouter = sessionRouter

	// Channel registry for NIP-29 group chat and future channel types.
	channelReg := channels.NewRegistry()
	defer channelReg.CloseAll()
	defer func() {
		if controlHub != nil {
			controlHub.Close()
		}
	}()

	// ── Shared control relay selector + hub (for channels/tools) ───────────────
	// Must be initialized before channel auto-join so startup channels share the
	// same hub pool and deduplicated relay connections.
	{
		liveCfg := configState.Get()
		if len(liveCfg.Relays.Read) == 0 {
			liveCfg.Relays.Read = cfg.Relays
		}
		if len(liveCfg.Relays.Write) == 0 {
			liveCfg.Relays.Write = cfg.Relays
		}

		if controlRelaySelector == nil {
			controlRelaySelector = nostruntime.NewRelaySelector(liveCfg.Relays.Read, liveCfg.Relays.Write)
			toolbuiltin.SetRelaySelector(controlRelaySelector)
		}

		if controlHub == nil && controlKeyer != nil {
			hub, hubErr := nostruntime.NewHub(ctx, controlKeyer, controlRelaySelector)
			if hubErr != nil {
				log.Printf("nostr hub: failed to create: %v (channels/tools will use dedicated pools)", hubErr)
			} else {
				controlHub = hub
			}
		}
	}

	// buildAutoJoinTurn assembles a Turn with context, history, and executor
	// for auto-joined channel sessions.  This mirrors the context assembly in
	// doChannelTurn (defined later) so auto-join channels get the same context
	// quality as manually-connected channels.
	buildAutoJoinTurn := func(turnCtx context.Context, sessionID, text string, turnTools []agent.ToolDefinition, turnExecutor agent.ToolExecutor) preparedAgentRunTurn {
		scopeCtx := resolveMemoryScopeContext(turnCtx, configState.Get(), docsRepo, sessionStore, sessionID, sessionRouter.Get(sessionID), "")
		turnCtx = contextWithMemoryScope(turnCtx, scopeCtx)
		turnContext, surfacedFileMemory, memoryRecallSample := buildDynamicMemoryRecallContext(turnCtx, memoryIndex, scopeCtx, sessionID, text, workspaceDirForAgent(configState.Get(), sessionRouter.Get(sessionID)), sessionStore, 0)
		// Inject structured task state for context rehydration.
		if taskCtx := buildTaskStateContextBlock(sessionStore, sessionID); taskCtx != "" {
			turnContext = joinPromptSections(turnContext, taskCtx)
		}
		staticSystemPrompt := assembleMemorySystemPrompt(memoryIndex, scopeCtx, workspaceDirForAgent(configState.Get(), sessionRouter.Get(sessionID)))
		var turnHistory []agent.ConversationMessage
		if controlContextEngine != nil {
			if assembled, asmErr := controlContextEngine.Assemble(turnCtx, sessionID, 100_000); asmErr == nil {
				if assembled.SystemPromptAddition != "" {
					turnContext = joinPromptSections(turnContext, assembled.SystemPromptAddition)
				}
				msgs := assembled.Messages
				// Deduplicate the current user message if context engine already has it.
				if n := len(msgs); n > 0 {
					if last := msgs[n-1]; last.Role == "user" && strings.TrimSpace(last.Content) == strings.TrimSpace(text) {
						msgs = msgs[:n-1]
					}
				}
				for _, m := range msgs {
					turnHistory = append(turnHistory, conversationMessageFromContext(m))
				}
			}
		}
		promptEnvelope := buildTurnPromptEnvelope(turnPromptBuilderParams{
			Config:             configState.Get(),
			SessionID:          sessionID,
			AgentID:            sessionRouter.Get(sessionID),
			Channel:            "nostr",
			SelfPubkey:         pubkey,
			SelfNPub:           toolbuiltin.NostrNPubFromHex(pubkey),
			StaticSystemPrompt: staticSystemPrompt,
			Context:            turnContext,
			Tools:              turnTools,
		})
		return preparedAgentRunTurn{
			Turn: agent.Turn{
				SessionID:           sessionID,
				TurnID:              nextDeterministicRecallTurnID(),
				UserText:            text,
				StaticSystemPrompt:  promptEnvelope.StaticSystemPrompt,
				Context:             promptEnvelope.Context,
				History:             turnHistory,
				Tools:               turnTools,
				Executor:            turnExecutor,
				ContextWindowTokens: promptEnvelope.ContextWindowTokens,
				HookInvoker:         controlHookInvoker,
			},
			TurnCtx:            turnCtx, // Pass the context with memory scope set
			SurfacedFileMemory: surfacedFileMemory,
			MemoryRecallSample: memoryRecallSample,
		}
	}

	if anyEnabledNIP34AutoReviewFollowedOnly(configState.Get()) {
		loadInitialRepoBookmarks(ctx, controlKeyer, configState.Get())
	}

	// Auto-join any NostrChannels declared in the config with enabled: true.
	// This provides OpenClaw parity: channels configured in the config file are
	// active immediately at startup without a manual channels.join RPC call.
	for chanName, chanCfg := range configState.Get().NostrChannels {
		if !chanCfg.Enabled {
			continue
		}
		switch chanCfg.Kind {
		case state.NostrChannelKindNIP29:
			if chanCfg.GroupAddress == "" {
				log.Printf("auto-join skip: nostr_channels.%s has no group_address", chanName)
				continue
			}
			localChanCfg := chanCfg // capture loop var
			localChanName := chanName
			ch, chErr := channels.NewNIP29GroupChannel(ctx, channels.NIP29GroupChannelOptions{
				GroupAddress: localChanCfg.GroupAddress,
				Hub:          controlHub,
				Keyer:        controlKeyer,
				OnMessage: func(msg channels.InboundMessage) {
					// Per-channel allow-from check.
					if dec := policy.EvaluateGroupMessage(msg.FromPubKey, localChanCfg.AllowFrom, configState.Get()); !dec.Allowed {
						log.Printf("nip29 group message rejected from=%s channel=%s reason=%s", msg.FromPubKey, msg.ChannelID, dec.Reason)
						return
					}
					// Per-sender session: each group member gets their own conversation.
					sessionID := "ch:" + msg.ChannelID + ":" + msg.FromPubKey
					activeAgentID, rt := resolveInboundChannelRuntime(localChanCfg.AgentID, msg.ChannelID)
					emitPluginMessageReceived(ctx, pluginhooks.MessageReceivedEvent{ChannelID: msg.ChannelID, SenderID: msg.FromPubKey, Text: msg.Text, EventID: msg.EventID, SessionID: sessionID, AgentID: activeAgentID, CreatedAt: msg.CreatedAt})
					turnCtx, release := chatCancels.Begin(sessionID, ctx)
					go func() {
						defer release()
						filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, rt, tools, turnToolConstraints{})
						prepared := buildAutoJoinTurn(turnCtx, sessionID, msg.Text, turnTools, turnExecutor)
						result, turnErr := filteredRuntime.ProcessTurn(prepared.TurnCtx, prepared.Turn)
						if turnErr != nil {
							log.Printf("auto-join channel agent turn error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, turnErr)
							return
						}
						commitMemoryRecallArtifacts(sessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
						replyText, sendOK := applyPluginMessageSending(turnCtx, pluginhooks.MessageSendingEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: result.Text, SessionID: sessionID, AgentID: activeAgentID})
						if !sendOK {
							return
						}
						if err := msg.Reply(turnCtx, replyText); err != nil {
							emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: replyText, SessionID: sessionID, AgentID: activeAgentID, Success: false, Error: err.Error()})
							log.Printf("auto-join channel reply error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, err)
							return
						}
						emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: replyText, SessionID: sessionID, AgentID: activeAgentID, Success: true})
					}()
				},
				OnError: func(err error) {
					log.Printf("auto-join nip29 error name=%s group=%s err=%v", localChanName, localChanCfg.GroupAddress, err)
				},
			})
			if chErr != nil {
				log.Printf("auto-join nip29 failed name=%s group=%s err=%v", localChanName, localChanCfg.GroupAddress, chErr)
				continue
			}
			if addErr := channelReg.Add(ch); addErr != nil {
				ch.Close()
				log.Printf("auto-join channel add failed name=%s err=%v", localChanName, addErr)
				continue
			}
			log.Printf("auto-join nip29 channel joined name=%s group=%s id=%s", localChanName, localChanCfg.GroupAddress, ch.ID())
		case state.NostrChannelKindNIP28:
			if chanCfg.ChannelID == "" {
				log.Printf("auto-join skip: nostr_channels.%s has no channel_id", chanName)
				continue
			}
			relays := chanCfg.Relays
			if len(relays) == 0 {
				relays = configState.Get().Relays.Read
			}
			if len(relays) == 0 {
				log.Printf("auto-join skip: nostr_channels.%s (nip28) has no relays configured", chanName)
				continue
			}
			localChanCfg := chanCfg
			localChanName := chanName
			ch28, chErr := channels.NewNIP28PublicChannel(ctx, channels.NIP28PublicChannelOptions{
				ChannelID: localChanCfg.ChannelID,
				Hub:       controlHub,
				Keyer:     controlKeyer,
				Relays:    relays,
				OnMessage: func(msg channels.InboundMessage) {
					// Per-channel allow-from check.
					if dec := policy.EvaluateGroupMessage(msg.FromPubKey, localChanCfg.AllowFrom, configState.Get()); !dec.Allowed {
						log.Printf("nip28 channel message rejected from=%s channel=%s reason=%s", msg.FromPubKey, msg.ChannelID, dec.Reason)
						return
					}
					// Per-sender session: each channel member gets their own conversation.
					sessionID := "ch:" + msg.ChannelID + ":" + msg.FromPubKey
					activeAgentID, rt := resolveInboundChannelRuntime(localChanCfg.AgentID, msg.ChannelID)
					emitPluginMessageReceived(ctx, pluginhooks.MessageReceivedEvent{ChannelID: msg.ChannelID, SenderID: msg.FromPubKey, Text: msg.Text, EventID: msg.EventID, SessionID: sessionID, AgentID: activeAgentID, CreatedAt: msg.CreatedAt})
					turnCtx, release := chatCancels.Begin(sessionID, ctx)
					go func() {
						defer release()
						filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, rt, tools, turnToolConstraints{})
						prepared := buildAutoJoinTurn(turnCtx, sessionID, msg.Text, turnTools, turnExecutor)
						result, turnErr := filteredRuntime.ProcessTurn(prepared.TurnCtx, prepared.Turn)
						if turnErr != nil {
							log.Printf("auto-join nip28 agent turn error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, turnErr)
							return
						}
						commitMemoryRecallArtifacts(sessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
						replyText, sendOK := applyPluginMessageSending(turnCtx, pluginhooks.MessageSendingEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: result.Text, SessionID: sessionID, AgentID: activeAgentID})
						if !sendOK {
							return
						}
						if err := msg.Reply(turnCtx, replyText); err != nil {
							emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: replyText, SessionID: sessionID, AgentID: activeAgentID, Success: false, Error: err.Error()})
							log.Printf("auto-join nip28 reply error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, err)
							return
						}
						emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: replyText, SessionID: sessionID, AgentID: activeAgentID, Success: true})
					}()
				},
				OnError: func(err error) {
					log.Printf("auto-join nip28 error name=%s channel=%s err=%v", localChanName, localChanCfg.ChannelID, err)
				},
			})
			if chErr != nil {
				log.Printf("auto-join nip28 failed name=%s channel=%s err=%v", localChanName, localChanCfg.ChannelID, chErr)
				continue
			}
			if addErr := channelReg.Add(ch28); addErr != nil {
				ch28.Close()
				log.Printf("auto-join nip28 channel add failed name=%s err=%v", localChanName, addErr)
				continue
			}
			log.Printf("auto-join nip28 channel joined name=%s channel_id=%s id=%s", localChanName, localChanCfg.ChannelID, ch28.ID())
		case state.NostrChannelKindChat:
			// NIP-C7 kind:9 chat channel.
			relays := chanCfg.Relays
			if len(relays) == 0 {
				relays = configState.Get().Relays.Read
			}
			if len(relays) == 0 {
				log.Printf("auto-join skip: nostr_channels.%s (chat) has no relays configured", chanName)
				continue
			}
			localChanCfg := chanCfg
			localChanName := chanName

			// Extract root_tag from channel config; defaults to "-" (relay root chat).
			rootTag := "-"
			if cfgMap := localChanCfg.Config; cfgMap != nil {
				if rt, ok := cfgMap["root_tag"].(string); ok && rt != "" {
					rootTag = rt
				}
			}

			chatCh, chErr := channels.NewChatChannel(ctx, channels.ChatChannelOptions{
				Hub:     controlHub,
				Keyer:   controlKeyer,
				Relays:  relays,
				RootTag: rootTag,
				OnMessage: func(msg channels.InboundMessage) {
					// Per-channel allow-from check.
					if dec := policy.EvaluateGroupMessage(msg.FromPubKey, localChanCfg.AllowFrom, configState.Get()); !dec.Allowed {
						log.Printf("chat channel message rejected from=%s channel=%s reason=%s", msg.FromPubKey, msg.ChannelID, dec.Reason)
						return
					}
					// Per-sender session: each chat participant gets their own conversation.
					sessionID := "ch:" + msg.ChannelID + ":" + msg.FromPubKey
					activeAgentID, rt := resolveInboundChannelRuntime(localChanCfg.AgentID, msg.ChannelID)
					emitPluginMessageReceived(ctx, pluginhooks.MessageReceivedEvent{ChannelID: msg.ChannelID, SenderID: msg.FromPubKey, Text: msg.Text, EventID: msg.EventID, SessionID: sessionID, AgentID: activeAgentID, CreatedAt: msg.CreatedAt})
					turnCtx, release := chatCancels.Begin(sessionID, ctx)
					go func() {
						defer release()
						filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, rt, tools, turnToolConstraints{})
						prepared := buildAutoJoinTurn(turnCtx, sessionID, msg.Text, turnTools, turnExecutor)
						result, turnErr := filteredRuntime.ProcessTurn(prepared.TurnCtx, prepared.Turn)
						if turnErr != nil {
							log.Printf("auto-join chat agent turn error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, turnErr)
							return
						}
						commitMemoryRecallArtifacts(sessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
						replyText, sendOK := applyPluginMessageSending(turnCtx, pluginhooks.MessageSendingEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: result.Text, SessionID: sessionID, AgentID: activeAgentID})
						if !sendOK {
							return
						}
						if err := msg.Reply(turnCtx, replyText); err != nil {
							emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: replyText, SessionID: sessionID, AgentID: activeAgentID, Success: false, Error: err.Error()})
							log.Printf("auto-join chat reply error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, err)
							return
						}
						emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: msg.ChannelID, SenderID: activeAgentID, Recipient: msg.FromPubKey, Text: replyText, SessionID: sessionID, AgentID: activeAgentID, Success: true})
					}()
				},
				OnError: func(err error) {
					log.Printf("auto-join chat error name=%s root_tag=%s err=%v", localChanName, rootTag, err)
				},
			})
			if chErr != nil {
				log.Printf("auto-join chat failed name=%s root_tag=%s err=%v", localChanName, rootTag, chErr)
				continue
			}
			if addErr := channelReg.Add(chatCh); addErr != nil {
				chatCh.Close()
				log.Printf("auto-join chat channel add failed name=%s err=%v", localChanName, addErr)
				continue
			}
			log.Printf("auto-join chat channel joined name=%s root_tag=%s relays=%d id=%s", localChanName, rootTag, len(relays), chatCh.ID())
		case state.NostrChannelKindRelayFilter, state.NostrChannelKindNIP34Inbox:
			relays := chanCfg.Relays
			if len(relays) == 0 {
				relays = configState.Get().Relays.Read
			}
			if len(relays) == 0 {
				log.Printf("auto-join skip: nostr_channels.%s (%s) has no relays configured", chanName, chanCfg.Kind)
				continue
			}
			filter, filterErr := buildRelayFilterFilter(chanCfg)
			if filterErr != nil {
				log.Printf("auto-join skip: nostr_channels.%s invalid relay filter err=%v", chanName, filterErr)
				continue
			}
			localChanCfg := chanCfg
			localChanName := chanName
			localChanID := "relay-filter:" + localChanName
			localMode := relayFilterMode(localChanCfg)
			rfCh, chErr := channels.NewRelayFilterChannel(ctx, channels.RelayFilterChannelOptions{
				ID:     localChanID,
				Hub:    controlHub,
				Keyer:  controlKeyer,
				Relays: relays,
				Filter: filter,
				OnEvent: func(msg channels.RelayFilterEvent) {
					if dmRunAgentTurnRef == nil {
						return
					}
					activeChanCfg := localChanCfg
					if liveChanCfg, ok := configState.Get().NostrChannels[localChanName]; ok {
						activeChanCfg = liveChanCfg
					}
					if !activeChanCfg.Enabled {
						return
					}
					if dec := policy.EvaluateGroupMessage(msg.FromPubKey, activeChanCfg.AllowFrom, configState.Get()); !dec.Allowed {
						log.Printf("relay-filter message rejected from=%s channel=%s reason=%s", msg.FromPubKey, msg.ChannelID, dec.Reason)
						return
					}
					mode := relayFilterMode(activeChanCfg)
					sessionID := relayFilterSessionID(msg.ChannelID, msg.FromPubKey)
					senderID := msg.FromPubKey
					text := renderRelayFilterInboxText(localChanName, msg.Event, msg.Relay)
					overrideAgentID := ""
					turnConstraints := turnToolConstraints{}
					if mode == relayFilterModeNIP34 {
						parsed, parseErr := grasp.ParseInboundEvent(&msg.Event)
						if parseErr != nil {
							log.Printf("auto-join nip34 parse error name=%s event=%s err=%v", localChanName, msg.Event.ID.Hex(), parseErr)
							return
						}
						sessionID = nip34InboxSessionID(localChanName, parsed)
						text = renderNIP34InboxText(localChanName, parsed, msg.Relay)
						autoReviewCfg, autoReviewEnabled := parseNIP34AutoReviewConfig(activeChanCfg)
						autoReviewMatched := autoReviewEnabled && shouldAutoReviewNIP34Event(autoReviewCfg, parsed, nip34RepoBookmarks)
						if transcriptRepo != nil {
							exists, err := transcriptRepo.HasEntry(watchDeliveryCtx, sessionID, msg.Event.ID.Hex())
							if err != nil {
								log.Printf("relay-filter delivery dedupe check failed channel=%s event=%s err=%v", localChanName, msg.Event.ID.Hex(), err)
							} else if exists {
								return
							}
						}
						if autoReviewMatched {
							text = renderNIP34AutoReviewText(localChanName, parsed, msg.Relay, autoReviewCfg)
							overrideAgentID = strings.TrimSpace(autoReviewCfg.AgentID)
							turnConstraints = turnToolConstraints{ToolProfile: autoReviewCfg.ToolProfile, EnabledTools: append([]string(nil), autoReviewCfg.EnabledTools...)}
						}
					} else if transcriptRepo != nil {
						exists, err := transcriptRepo.HasEntry(watchDeliveryCtx, sessionID, msg.Event.ID.Hex())
						if err != nil {
							log.Printf("relay-filter delivery dedupe check failed channel=%s event=%s err=%v", localChanName, msg.Event.ID.Hex(), err)
						} else if exists {
							return
						}
					}
					eventID := msg.Event.ID.Hex()
					inFlightKey := sessionID + "\x00" + eventID
					if !relayFilterInFlight.Begin(inFlightKey) {
						return
					}
					if baseAgentID := strings.TrimSpace(activeChanCfg.AgentID); baseAgentID != "" {
						sessionRouter.Assign(sessionID, baseAgentID)
					}
					createdAt := int64(msg.Event.CreatedAt)
					peerPubKey := msg.FromPubKey
					turnConstraintsCopy := turnToolConstraints{
						ToolProfile:  turnConstraints.ToolProfile,
						EnabledTools: append([]string(nil), turnConstraints.EnabledTools...),
					}
					go func() {
						defer relayFilterInFlight.End(inFlightKey)
						dmRunAgentTurnRef(watchDeliveryCtx, sessionID, senderID, text, eventID, createdAt, nil, overrideAgentID, turnConstraintsCopy)
						if sessionStore != nil {
							se := sessionStore.GetOrNew(sessionID)
							se.LastChannel = "nostr"
							se.LastTo = peerPubKey
							if putErr := sessionStore.Put(sessionID, se); putErr != nil {
								log.Printf("relay-filter session store put failed session=%s err=%v", sessionID, putErr)
							}
						}
						if err := updateSessionDoc(watchDeliveryCtx, docsRepo, sessionID, peerPubKey, func(session *state.SessionDoc) error {
							session.PeerPubKey = peerPubKey
							return nil
						}); err != nil {
							log.Printf("relay-filter session identity update failed session=%s err=%v", sessionID, err)
						}
					}()
				},
				OnError: func(err error) {
					log.Printf("auto-join %s error name=%s err=%v", localChanCfg.Kind, localChanName, err)
				},
			})
			if chErr != nil {
				log.Printf("auto-join %s failed name=%s err=%v", localChanCfg.Kind, localChanName, chErr)
				continue
			}
			if addErr := channelReg.Add(rfCh); addErr != nil {
				rfCh.Close()
				log.Printf("auto-join %s channel add failed name=%s err=%v", localChanCfg.Kind, localChanName, addErr)
				continue
			}
			log.Printf("auto-join %s channel joined name=%s relays=%d id=%s mode=%s", localChanCfg.Kind, localChanName, len(relays), rfCh.ID(), localMode)
		default:
			log.Printf("auto-join skip: nostr_channels.%s kind=%q not yet supported for auto-join", chanName, chanCfg.Kind)
		}
	}

	// Pre-load runtimes for any agents persisted from a previous run.
	// This is best-effort: failures are logged but don't block startup.
	if existingAgents, listErr := docsRepo.ListAgents(ctx, 200); listErr == nil {
		for _, agDoc := range existingAgents {
			if agDoc.Deleted || agDoc.AgentID == "" || agDoc.AgentID == "main" {
				continue
			}
			if rt, rtErr := agent.BuildRuntimeForModel(agDoc.Model, tools); rtErr == nil {
				agentRegistry.Set(agDoc.AgentID, rt)
				log.Printf("agent runtime loaded id=%s model=%q", agDoc.AgentID, agDoc.Model)
			} else {
				log.Printf("agent runtime build warning id=%s model=%q err=%v", agDoc.AgentID, agDoc.Model, rtErr)
			}
		}
	} else {
		log.Printf("pre-load agents warning: %v", listErr)
	}

	// Auto-provision agents declared in the typed Agents config section.
	// These complement persisted agents (loaded above from Nostr docs).
	// Config-declared agents take lower precedence: if an agent ID is already
	// in the registry from a Nostr doc, its runtime is preserved.
	// Provider overrides from cfg.Providers are applied when the agent names a provider.
	if configAgents := configState.Get().Agents; len(configAgents) > 0 {
		registeredIDs := make(map[string]bool)
		for _, id := range agentRegistry.Registered() {
			registeredIDs[id] = true
		}
		providers := configState.Get().Providers
		// Refresh multi-key rotation rings from provider config.
		refreshKeyRings(providers)
		for _, agCfg := range configAgents {
			agentID := strings.TrimSpace(agCfg.ID)
			if agentID == "" {
				continue
			}
			model := strings.TrimSpace(agCfg.Model)
			if model == "" {
				continue
			}
			// "main" is normally skipped because Get("main") returns the default
			// runtime.  However, when config explicitly declares a "main" agent with
			// a model/provider, we want that to become the new default.  We handle
			// it separately below by updating the registry default.
			isMain := agentID == "main"
			if !isMain && registeredIDs[agentID] {
				log.Printf("agent config: id=%s already loaded from Nostr docs, skipping auto-provision", agentID)
				continue
			}
			override := resolveModelProviderOverride(configState.Get(), agCfg, model)
			// Determine the effective Provider before building the Runtime.
			// Layer 1: FallbackChain wraps the primary + fallback ChatProviders.
			// Layer 2: RoutedProvider selects between primary and light model.
			var effectiveProvider agent.Provider
			hasFallbacks := len(agCfg.FallbackModels) > 0
			hasLightModel := strings.TrimSpace(agCfg.LightModel) != ""

			if hasFallbacks {
				fbOverrides := make(map[string]agent.ProviderOverride)
				for _, fbModel := range agCfg.FallbackModels {
					fbModel = strings.TrimSpace(fbModel)
					if fbModel == "" {
						continue
					}
					fbOverrides[fbModel] = resolveModelProviderOverride(configState.Get(), agCfg, fbModel)
				}
				fbProvider, fbErr := agent.NewFallbackChainProvider(
					model,
					override.APIKey,
					override.BaseURL,
					override.PromptCache,
					agCfg.FallbackModels,
					fbOverrides,
					override.SystemPrompt,
				)
				if fbErr == nil {
					effectiveProvider = fbProvider
					log.Printf("agent config: fallback chain enabled id=%s primary=%q fallbacks=%v", agentID, model, agCfg.FallbackModels)
				} else {
					log.Printf("agent config: fallback chain build warning id=%s err=%v — falling back to standard provider", agentID, fbErr)
				}
			}

			// Build the base runtime (used when no FallbackChain, or as
			// the primary for RoutedProvider).
			if effectiveProvider == nil {
				baseProv, basErr := buildProviderForAgentModel(configState.Get(), agCfg, model)
				if basErr != nil {
					log.Printf("agent config auto-provision warning id=%s model=%q provider=%q err=%v", agentID, model, agCfg.Provider, basErr)
					continue
				}
				effectiveProvider = baseProv
			}

			// Layer 2: Wrap with ModelRouter if light_model is configured.
			if hasLightModel {
				lightModel := strings.TrimSpace(agCfg.LightModel)
				threshold := agCfg.LightModelThreshold
				lightProv, lightErr := buildProviderForAgentModel(configState.Get(), agCfg, lightModel)
				if lightErr != nil {
					log.Printf("agent config: light model build warning id=%s light=%q err=%v — routing disabled", agentID, lightModel, lightErr)
				} else {
					routed := agent.NewRoutedProvider(effectiveProvider, model, lightProv, lightModel, threshold)
					effectiveProvider = routed
					log.Printf("agent config: model routing enabled id=%s primary=%q light=%q threshold=%.2f", agentID, model, lightModel, threshold)
				}
			}

			rt, rtErr := agent.NewProviderRuntime(effectiveProvider, tools)
			if rtErr != nil {
				log.Printf("agent config auto-provision warning id=%s model=%q provider=%q err=%v", agentID, model, agCfg.Provider, rtErr)
				continue
			}

			if isMain {
				// Update the registry default so all "main"/"" lookups use this runtime.
				agentRegistry.SetDefault(rt)
				controlAgentRuntime = rt
				if controlServices != nil {
					controlServices.session.agentRuntime = rt
				}
				log.Printf("agent config: default runtime updated id=main model=%q provider=%q", model, agCfg.Provider)
				if agCfg.ContextWindow == 0 {
					resolved := agent.ResolveModelContext(model)
					if resolved.ContextWindowTokens >= 200_000 {
						log.Printf("⚠️  agent \"main\": model %q is not in the context-window registry — defaulting to %dk tokens. "+
							"If this model has a smaller context window, set context_window in the agent config "+
							"(e.g. context_window = 8192) to enable proper budget allocation and compaction.",
							model, resolved.ContextWindowTokens/1000)
					}
				}
				continue
			}
			agentRegistry.Set(agentID, rt)
			log.Printf("agent config auto-provisioned id=%s model=%q provider=%q", agentID, model, agCfg.Provider)
			// Warn when the context window falls back to 200K for an
			// unrecognized model. This usually means the operator is
			// running a local GGUF/GGML model whose actual context
			// capacity is much smaller.
			if agCfg.ContextWindow == 0 {
				resolved := agent.ResolveModelContext(model)
				if resolved.ContextWindowTokens >= 200_000 {
					log.Printf("⚠️  agent %q: model %q is not in the context-window registry — defaulting to %dk tokens. "+
						"If this model has a smaller context window, set context_window in the agent config "+
						"(e.g. context_window = 8192) to enable proper budget allocation and compaction.",
						agentID, model, resolved.ContextWindowTokens/1000)
				}
			}
			// Pre-seed DM peer routing: each dm_peers pubkey is routed to this agent.
			for _, peerPubkey := range agCfg.DmPeers {
				peerPubkey = strings.TrimSpace(peerPubkey)
				if peerPubkey == "" {
					continue
				}
				sessionRouter.Assign(peerPubkey, agentID)
				preview := peerPubkey
				if len(preview) > 12 {
					preview = preview[:12] + "..."
				}
				log.Printf("agent config dm-peer routed peer=%s → agent=%s", preview, agentID)
			}
		}
	}

	// Auto-promote: when no "main" agent was declared in config but at
	// least one real agent was provisioned, promote the first (or sole)
	// agent as the default runtime.  This prevents the EchoProvider stub
	// from handling DMs meant for the configured model.
	if registered := agentRegistry.Registered(); len(registered) > 0 {
		if agent.IsEchoRuntime(agentRuntime) {
			promotedID := registered[0]
			promotedRT := agentRegistry.Get(promotedID)
			agentRegistry.SetDefault(promotedRT)
			agentRuntime = promotedRT
			controlAgentRuntime = promotedRT
			log.Printf("agent config: auto-promoted %q as default runtime (no \"main\" agent declared)", promotedID)
		}
	}

	// Pre-seed session→agent assignments from persisted session meta.
	// Any session with meta["agent_id"] set is re-routed to that agent.
	if existingSessions, sessErr := docsRepo.ListSessions(ctx, 5000); sessErr == nil {
		for _, sess := range existingSessions {
			if sess.Meta == nil {
				continue
			}
			if aid, ok := sess.Meta["agent_id"].(string); ok && aid != "" && aid != "main" {
				sessionID := strings.TrimSpace(sess.SessionID)
				if sessionID == "" {
					continue
				}
				sessionRouter.Assign(sessionID, aid)
			}
		}
	} else {
		log.Printf("pre-seed session routes warning: %v", sessErr)
	}
	newHeartbeatRunner(ops, func() state.ConfigDoc { return configState.Get() }).Start(ctx)
	usageState := newUsageTracker(startedAt)
	logBuffer := newRuntimeLogBuffer(2000)
	eventBuffer := newRuntimeEventBuffer(2000)
	toolbuiltin.SetRuntimeObserveProvider(toolbuiltin.RuntimeObserveProvider{
		Observe: func(obsCtx context.Context, req toolbuiltin.RuntimeObserveRequest) (map[string]any, error) {
			out, err := observeRuntimeActivity(obsCtx, eventBuffer, logBuffer, req)
			if err != nil {
				return nil, err
			}
			if snapshot := currentMCPTelemetry(configState.Get(), mcpManager); !snapshot.Empty() {
				out["mcp"] = snapshot
			}
			return out, nil
		},
		TailEvents: func(cursor int64, limit int, maxBytes int, filters toolbuiltin.RuntimeObserveFilters) map[string]any {
			return eventBuffer.Tail(cursor, limit, maxBytes, filters)
		},
		TailLogs: func(cursor int64, limit int, maxBytes int) map[string]any {
			return logBuffer.Tail(cursor, limit, maxBytes)
		},
	})

	// ── Rate limiter ──────────────────────────────────────────────────────────
	// Per-user and per-channel rate limits. Configurable via Extra["rate_limit"].
	// Defaults: user burst=10, rate=2/s; channel burst=20, rate=5/s.
	buildRateLimitCfg := func(key string, defBurst, defRate float64) ratelimitpkg.Config {
		cfg := ratelimitpkg.Config{Burst: defBurst, Rate: defRate, Enabled: true}
		if rlExtra, ok := configState.Get().Extra["rate_limit"].(map[string]any); ok {
			if section, ok := rlExtra[key].(map[string]any); ok {
				if v, ok := section["burst"].(float64); ok && v > 0 {
					cfg.Burst = v
				}
				if v, ok := section["rate"].(float64); ok && v > 0 {
					cfg.Rate = v
				}
				if v, ok := section["enabled"].(bool); ok {
					cfg.Enabled = v
				}
			}
		}
		return cfg
	}
	dmRateLimiter := ratelimitpkg.NewMultiLimiter(
		buildRateLimitCfg("user", 10, 2),
		buildRateLimitCfg("channel", 20, 5),
	)
	// Prune idle rate-limit buckets every 10 minutes.
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dmRateLimiter.Prune()
			}
		}
	}()
	channelState := newChannelRuntimeState()

	// Start background cleanup goroutines to prevent memory leaks
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				nodeInvocations.cleanup()
				cronJobs.cleanup()
				execApprovals.cleanup()
				wizards.cleanup()
			}
		}
	}()

	// Background context engine compaction: every 30 minutes, compact the
	// shared engine (which handles all sessions internally).
	go func() {
		compactInterval := 30 * time.Minute
		if v, ok := configState.Get().Extra["context_compact_interval_minutes"].(float64); ok && v > 0 {
			compactInterval = time.Duration(v) * time.Minute
		}
		ticker := time.NewTicker(compactInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if controlContextEngine == nil {
					continue
				}
				if cr, cErr := controlContextEngine.Compact(ctx, ""); cErr == nil && cr.Compacted {
					log.Printf("context engine background compact: %s", cr.Summary)
				}
			}
		}
	}()

	// Background memory index compaction: every 6 hours, trim the raw JSON-FTS
	// memory index to prevent unbounded disk/memory growth.
	// Default max is 50 000 entries; configurable via extra.memory.max_entries.
	go func() {
		const defaultMaxMemoryEntries = 50_000
		const compactCycle = 6 * time.Hour
		ticker := time.NewTicker(compactCycle)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if memoryIndex == nil {
					continue
				}
				maxEntries := defaultMaxMemoryEntries
				if extra, ok := configState.Get().Extra["memory"].(map[string]any); ok {
					if mv, ok := extra["max_entries"].(float64); ok && mv > 0 {
						maxEntries = int(mv)
					}
				}
				removed := memoryIndex.Compact(maxEntries)
				if removed > 0 {
					log.Printf("memory index compaction: removed %d oldest entries (max=%d)", removed, maxEntries)
					if saveErr := memoryIndex.Save(); saveErr != nil {
						log.Printf("memory index save after compaction failed: %v", saveErr)
					}
				}
			}
		}
	}()

	// wsEmitter pushes typed events to connected WebSocket clients.
	// It starts as a no-op and is upgraded to the real runtime emitter once the
	// WS gateway starts.  The dmOnMessage closure captures this variable.
	var wsEmitter gatewayws.EventEmitter = newObservedEventEmitter(gatewayws.NoopEmitter{}, eventBuffer)
	filteredMCPLifecycle := newFilteredMCPLifecycleTracker()
	setControlWSEmitter(wsEmitter)
	if mcpManager != nil {
		snapshots := mcpManager.SetStateObserverAndSnapshot(func(change mcppkg.StateChange) {
			emitControlWSEvent(gatewayws.EventMCPLifecycle, buildMCPLifecyclePayload(change, time.Now().UnixMilli()))
		})
		for _, snapshot := range snapshots {
			emitControlWSEvent(gatewayws.EventMCPLifecycle, buildMCPLifecyclePayload(mcppkg.StateChange{
				Server: snapshot,
				Reason: "startup.snapshot",
			}, time.Now().UnixMilli()))
		}
	}
	filteredMCPLifecycle.Emit(runtimeEventEmitterFunc(emitControlWSEvent), mcppkg.ResolveConfigDoc(configState.Get()), "startup.snapshot", time.Now().UnixMilli())
	heartbeatDone := startRuntimeHeartbeatLoop(ctx, startedAt, "metiqd", 30*time.Second, shutdownEmitter)

	// ── Slash command router ──────────────────────────────────────────────────
	// Registers built-in /commands that are intercepted before the message
	// reaches the agent runtime.
	slashRouter := autoreply.NewRouter()

	// slashAuthLevels defines the minimum AuthLevel required for each slash command.
	// Commands not listed default to AuthPublic (any allowed sender may run them).
	slashAuthLevels := map[string]policy.AuthLevel{
		// Owner-only: configuration and export.
		"set":    policy.AuthOwner,
		"unset":  policy.AuthOwner,
		"export": policy.AuthOwner,
		// Trusted+: session management and compaction.
		"compact": policy.AuthTrusted,
		"kill":    policy.AuthTrusted,
		"new":     policy.AuthTrusted,
		"reset":   policy.AuthTrusted,
		"session": policy.AuthTrusted,
		"fast":    policy.AuthTrusted,
		"restart": policy.AuthTrusted,
		// Trusted+: agent routing commands.
		"focus":   policy.AuthTrusted,
		"unfocus": policy.AuthTrusted,
		"spawn":   policy.AuthTrusted,
		// Public: informational commands (default — listed for documentation).
		"help":   policy.AuthPublic,
		"status": policy.AuthPublic,
		"info":   policy.AuthPublic,
		"agents": policy.AuthPublic,
		"model":  policy.AuthPublic,
		"usage":  policy.AuthPublic,
	}
	sessionTurns := autoreply.NewSessionTurns()
	controlSessionTurns = sessionTurns
	turnHandoffs := newSessionTurnHandoffRegistry()
	// dmQueues holds per-session pending-turn queues for DMs that arrive while
	// the session turn slot is busy.  Mirrors channelQueues for the DM path.
	dmQueues := autoreply.NewSessionQueueRegistry(10, autoreply.QueueDropSummarize)
	// steeringMailboxes holds exact queue-mode "steer" input for active turns.
	// Items are drained at agentic model boundaries, with post-turn fallback.
	steeringMailboxes := autoreply.NewSteeringMailboxRegistry(10, autoreply.QueueDropSummarize)
	activeTools := newActiveToolRegistry()

	// ── Session and node agent tools ─────────────────────────────────
	// Registered here so they can close over sessionTurns and configState.
	// Attach native function-calling definitions for session tools.
	tools.SetDefinition("sessions_list", toolbuiltin.SessionsListDef)
	tools.SetDefinition("session_spawn", toolbuiltin.SessionSpawnDef)
	tools.SetDefinition("session_send", toolbuiltin.SessionSendDef)

	// sessions_list: return all tracked session IDs.
	tools.Register("sessions_list", func(_ context.Context, _ map[string]any) (string, error) {
		sessions := sessionTurns.KnownSessions()
		b, _ := json.Marshal(map[string]any{"sessions": sessions, "count": len(sessions)})
		return string(b), nil
	})

	// session_spawn: run a fresh agent session and optionally wait for result.
	tools.Register("session_spawn", func(ctx context.Context, args map[string]any) (string, error) {
		instructions := agent.ArgString(args, "instructions")
		if instructions == "" {
			return "", fmt.Errorf("session_spawn: instructions is required")
		}
		waitFor := args["wait"] == true
		timeoutSec := agent.ArgInt(args, "timeout_seconds", 60)
		memoryScope := state.NormalizeAgentMemoryScope(agent.ArgString(args, "memory_scope"))
		spawnAgentID := agent.ArgString(args, "agent_id")

		sessionID := generateSessionID()
		sessionTurns.Track(sessionID, spawnAgentID)
		persistSessionMemoryScope(sessionStore, sessionID, spawnAgentID, memoryScope)

		runTurn := func(ctx context.Context) (string, error) {
			releaseTurn, acquired := sessionTurns.TryAcquire(sessionID)
			if !acquired {
				return "", fmt.Errorf("session_spawn: session %q is busy", sessionID)
			}
			defer releaseTurn()
			scopeCtx := resolveMemoryScopeContext(ctx, configState.Get(), docsRepo, sessionStore, sessionID, spawnAgentID, memoryScope)
			turnCtx := contextWithMemoryScope(ctx, scopeCtx)
			prepared := buildAgentRunTurn(turnCtx, methods.AgentRequest{SessionID: sessionID, Message: instructions}, memoryIndex, scopeCtx, workspaceDirForAgent(configState.Get(), spawnAgentID), sessionStore)
			filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, spawnAgentID, agentRuntime, tools, turnToolConstraints{})
			prepared.Turn.Tools = turnTools
			prepared.Turn.Executor = turnExecutor
			prepared.Turn.ToolEventSink = toolLifecyclePersistenceSink(controlServices.session.sessionStore, sessionID, toolLifecycleEmitter(runtimeEventEmitterFunc(emitControlWSEvent), spawnAgentID))
			prepared = applyPromptEnvelopeToPreparedTurn(prepared, turnPromptBuilderParams{Config: configState.Get(), SessionID: sessionID, AgentID: spawnAgentID, Channel: "nostr", SelfPubkey: pubkey, SelfNPub: toolbuiltin.NostrNPubFromHex(pubkey), StaticSystemPrompt: prepared.Turn.StaticSystemPrompt, Context: prepared.Turn.Context, Tools: turnTools})
			result, err := filteredRuntime.ProcessTurn(turnCtx, prepared.Turn)
			if err != nil {
				return "", err
			}
			commitMemoryRecallArtifacts(sessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
			return result.Text, nil
		}

		if !waitFor {
			go func() {
				tctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
				defer cancel()
				//nolint:errcheck
				runTurn(tctx)
			}()
			b, _ := json.Marshal(map[string]any{"session_id": sessionID, "running": true})
			return string(b), nil
		}

		tctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		defer cancel()
		text, err := runTurn(tctx)
		if err != nil {
			return "", fmt.Errorf("session_spawn: %w", err)
		}
		b, _ := json.Marshal(map[string]any{"session_id": sessionID, "result": text})
		return string(b), nil
	})

	// session_send: run a turn in an existing named session.
	tools.Register("session_send", func(ctx context.Context, args map[string]any) (string, error) {
		sessionID := agent.ArgString(args, "session_id")
		text := agent.ArgString(args, "text")
		if sessionID == "" || text == "" {
			return "", fmt.Errorf("session_send: session_id and text are required")
		}
		if !sessionTurns.IsKnown(sessionID) {
			return "", fmt.Errorf("session_send: unknown session %q", sessionID)
		}
		releaseTurn, acquired := sessionTurns.TryAcquire(sessionID)
		if !acquired {
			return "", fmt.Errorf("session_send: session %q is busy", sessionID)
		}
		defer releaseTurn()
		scopeCtx := resolveMemoryScopeContext(ctx, configState.Get(), docsRepo, sessionStore, sessionID, "", "")
		turnCtx := contextWithMemoryScope(ctx, scopeCtx)
		activeAgentID := ""
		if sessionRouter != nil {
			activeAgentID = sessionRouter.Get(sessionID)
		}
		prepared := buildAgentRunTurn(turnCtx, methods.AgentRequest{SessionID: sessionID, Message: text}, memoryIndex, scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID), sessionStore)
		filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, agentRuntime, tools, turnToolConstraints{})
		prepared.Turn.Tools = turnTools
		prepared.Turn.Executor = turnExecutor
		prepared = applyPromptEnvelopeToPreparedTurn(prepared, turnPromptBuilderParams{Config: configState.Get(), SessionID: sessionID, AgentID: activeAgentID, Channel: "nostr", SelfPubkey: pubkey, SelfNPub: toolbuiltin.NostrNPubFromHex(pubkey), StaticSystemPrompt: prepared.Turn.StaticSystemPrompt, Context: prepared.Turn.Context, Tools: turnTools})
		result, err := filteredRuntime.ProcessTurn(turnCtx, prepared.Turn)
		if err != nil {
			return "", fmt.Errorf("session_send: %w", err)
		}
		commitMemoryRecallArtifacts(sessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
		b, _ := json.Marshal(map[string]any{"session_id": sessionID, "result": result.Text})
		return string(b), nil
	})

	// node_invoke: send an ACP task DM to any metiq node pubkey and wait.
	tools.Register("node_invoke", func(ctx context.Context, args map[string]any) (string, error) {
		targetPubKey := agent.ArgString(args, "node_pubkey")
		instructions := agent.ArgString(args, "instructions")
		timeoutMS := int64(agent.ArgInt(args, "timeout_seconds", 30)) * 1000
		if targetPubKey == "" || instructions == "" {
			return "", fmt.Errorf("node_invoke: node_pubkey and instructions are required")
		}
		cfg := state.ConfigDoc{}
		if configState != nil {
			cfg = configState.Get()
		}
		dmBus, dmScheme, err := resolveACPDMTransport(cfg, targetPubKey)
		if err != nil {
			return "", fmt.Errorf("node_invoke: %w", err)
		}
		taskID := acppkg.GenerateTaskID()
		senderPubKey := dmBus.PublicKey()
		acpMsg := acppkg.NewTask(taskID, senderPubKey, acppkg.TaskPayload{
			Instructions: instructions,
			TimeoutMS:    timeoutMS,
			ReplyTo:      senderPubKey,
		})
		controlACPDispatcher.Register(taskID)
		payload, _ := json.Marshal(acpMsg)
		if err := sendACPDMWithTransport(ctx, dmBus, dmScheme, targetPubKey, string(payload)); err != nil {
			controlACPDispatcher.Cancel(taskID)
			return "", fmt.Errorf("node_invoke: send: %w", err)
		}
		result, err := controlACPDispatcher.Wait(ctx, taskID, time.Duration(timeoutMS)*time.Millisecond)
		if err != nil {
			return "", fmt.Errorf("node_invoke: %w", err)
		}
		if result.Error != "" {
			return "", fmt.Errorf("node_invoke: remote error: %s", result.Error)
		}
		return result.Text, nil
	})

	// node_ping: send ACP ping and wait for ACP pong from a node.
	tools.Register("node_ping", func(ctx context.Context, args map[string]any) (string, error) {
		targetPubKey := agent.ArgString(args, "node_pubkey")
		timeoutMS := int64(agent.ArgInt(args, "timeout_seconds", 10)) * 1000
		if targetPubKey == "" {
			return "", fmt.Errorf("node_ping: node_pubkey is required")
		}
		cfg := state.ConfigDoc{}
		if configState != nil {
			cfg = configState.Get()
		}
		dmBus, dmScheme, err := resolveACPDMTransport(cfg, targetPubKey)
		if err != nil {
			return "", fmt.Errorf("node_ping: %w", err)
		}
		taskID := acppkg.GenerateTaskID()
		senderPubKey := dmBus.PublicKey()
		pingMsg := acppkg.NewPing(taskID, senderPubKey, acppkg.PingPayload{Nonce: taskID})
		controlACPDispatcher.Register(taskID)
		started := time.Now()
		payload, _ := json.Marshal(pingMsg)
		if err := sendACPDMWithTransport(ctx, dmBus, dmScheme, targetPubKey, string(payload)); err != nil {
			controlACPDispatcher.Cancel(taskID)
			return "", fmt.Errorf("node_ping: send: %w", err)
		}
		result, err := controlACPDispatcher.Wait(ctx, taskID, time.Duration(timeoutMS)*time.Millisecond)
		if err != nil {
			return "", fmt.Errorf("node_ping: %w", err)
		}
		if result.Error != "" {
			return "", fmt.Errorf("node_ping: remote error: %s", result.Error)
		}
		if strings.TrimSpace(result.Text) != "pong" {
			return "", fmt.Errorf("node_ping: unexpected response %q", result.Text)
		}
		out, _ := json.Marshal(map[string]any{
			"node_pubkey": targetPubKey,
			"task_id":     taskID,
			"ok":          true,
			"latency_ms":  time.Since(started).Milliseconds(),
		})
		return string(out), nil
	})

	// node_list: return paired/known metiq nodes.
	tools.Register("node_list", func(_ context.Context, _ map[string]any) (string, error) {
		out, err := applyNodeList(configState, methods.NodeListRequest{Limit: 100})
		if err != nil {
			return "", fmt.Errorf("node_list: %w", err)
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})

	// Attach native function-calling definitions for cron tools.
	tools.SetDefinition("cron_add", toolbuiltin.CronAddDef)
	tools.SetDefinition("cron_list", toolbuiltin.CronListDef)
	tools.SetDefinition("cron_remove", toolbuiltin.CronRemoveDef)

	// cron_add: schedule a recurring agent task.
	tools.Register("cron_add", func(ctx context.Context, args map[string]any) (string, error) {
		schedule := agent.ArgString(args, "schedule")
		instructions := agent.ArgString(args, "instructions")
		if schedule == "" || instructions == "" {
			return "", fmt.Errorf("cron_add: schedule and instructions are required")
		}
		if _, parseErr := cron.Parse(schedule); parseErr != nil {
			return "", fmt.Errorf("cron_add: invalid schedule %q: %w", schedule, parseErr)
		}
		// Guard: cap agent-created cron jobs.
		const maxAgentCronJobs = 20
		existing := controlCronJobs.List(0)
		if len(existing) >= maxAgentCronJobs {
			return "", fmt.Errorf("cron_add: limit of %d cron jobs reached", maxAgentCronJobs)
		}
		agentID := agent.ArgString(args, "agent_id")
		label := agent.ArgString(args, "label")
		params, _ := json.Marshal(methods.ChatSendRequest{To: agentID, Text: instructions})
		req := methods.CronAddRequest{
			Schedule: schedule,
			Method:   methods.MethodChatSend,
			Params:   params,
		}
		if label != "" {
			// CronAddRequest has no label field; embed in ID prefix so it shows in list.
			req.ID = label
		}
		job := controlCronJobs.Add(req)
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron_add: save: %v", saveErr)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "job_id": job.ID, "schedule": job.Schedule})
		return string(b), nil
	})

	// cron_list: return scheduled cron jobs as JSON.
	tools.Register("cron_list", func(_ context.Context, _ map[string]any) (string, error) {
		jobs := controlCronJobs.List(100)
		b, _ := json.Marshal(map[string]any{"jobs": jobs, "count": len(jobs)})
		return string(b), nil
	})

	// cron_remove: remove a cron job by ID.
	tools.Register("cron_remove", func(ctx context.Context, args map[string]any) (string, error) {
		jobID := agent.ArgString(args, "job_id")
		if jobID == "" {
			return "", fmt.Errorf("cron_remove: job_id is required")
		}
		if err := controlCronJobs.Remove(jobID); err != nil {
			return "", fmt.Errorf("cron_remove: %w", err)
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron_remove: save: %v", saveErr)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "job_id": jobID, "removed": true})
		return string(b), nil
	})

	// ─── Social planning tools ──────────────────────────────────────────────────
	tools.RegisterWithDef("social_plan_add", toolbuiltin.SocialPlanAddTool(socialPlanner), toolbuiltin.SocialPlanAddDef)
	tools.RegisterWithDef("social_plan_list", toolbuiltin.SocialPlanListTool(socialPlanner), toolbuiltin.SocialPlanListDef)
	tools.RegisterWithDef("social_plan_remove", toolbuiltin.SocialPlanRemoveTool(socialPlanner), toolbuiltin.SocialPlanRemoveDef)
	tools.RegisterWithDef("social_history", toolbuiltin.SocialHistoryTool(socialPlanner), toolbuiltin.SocialHistoryDef)
	tools.RegisterWithDef("social_record", toolbuiltin.SocialRecordTool(socialPlanner), toolbuiltin.SocialRecordDef)

	slashRouter.Register("help", func(_ context.Context, cmd autoreply.Command) (string, error) {
		cmds := slashRouter.Registered()
		lines := make([]string, 0, len(cmds)+1)
		lines = append(lines, "Available slash commands:")
		for _, c := range cmds {
			lines = append(lines, "  /"+c)
		}
		return strings.Join(lines, "\n"), nil
	})

	slashRouter.Register("status", func(_ context.Context, cmd autoreply.Command) (string, error) {
		agentID := sessionRouter.Get(cmd.SessionID)
		if agentID == "" {
			agentID = "main"
		}
		preview := cmd.SessionID
		if len(preview) > 16 {
			preview = preview[:16] + "…"
		}
		lines := []string{
			fmt.Sprintf("Session: %s", preview),
			fmt.Sprintf("Agent:   %s", agentID),
		}
		if sessionStore != nil {
			if se, ok := sessionStore.Get(cmd.SessionID); ok {
				if se.ModelOverride != "" {
					lines = append(lines, fmt.Sprintf("Model:   %s", se.ModelOverride))
				}
				if se.ProviderOverride != "" {
					lines = append(lines, fmt.Sprintf("Provider: %s", se.ProviderOverride))
				}
				if se.TotalTokens > 0 {
					lines = append(lines, fmt.Sprintf("Tokens:  %d in / %d out / %d total",
						se.InputTokens, se.OutputTokens, se.TotalTokens))
				}
				if se.FallbackTo != "" {
					lines = append(lines, fmt.Sprintf("Fallback: %s → %s", fallbackText(se.FallbackFrom, "primary"), se.FallbackTo))
				}
				var flags []string
				if se.Verbose {
					flags = append(flags, "verbose")
				}
				if se.Thinking {
					flags = append(flags, "thinking")
				}
				if se.TTSAuto {
					flags = append(flags, "tts-auto")
				}
				if se.FastMode {
					flags = append(flags, "fast")
				}
				if len(flags) > 0 {
					lines = append(lines, "Flags:   "+strings.Join(flags, ", "))
				}
				if se.VerboseLevel != "" {
					lines = append(lines, fmt.Sprintf("Verbose: %s", se.VerboseLevel))
				}
				if se.ReasoningLevel != "" {
					lines = append(lines, fmt.Sprintf("Reason:  %s", se.ReasoningLevel))
				}
				if se.ThinkingLevel != "" {
					lines = append(lines, fmt.Sprintf("Think:   %s", se.ThinkingLevel))
				}
				if se.ResponseUsage != "" {
					lines = append(lines, fmt.Sprintf("Usage:   %s", se.ResponseUsage))
				}
				if se.QueueMode != "" || se.QueueCap > 0 || se.QueueDrop != "" {
					qCap := "default"
					if se.QueueCap > 0 {
						qCap = strconv.Itoa(se.QueueCap)
					}
					qDrop := se.QueueDrop
					if qDrop == "" {
						qDrop = "summarize"
					}
					qMode := se.QueueMode
					if qMode == "" {
						qMode = "collect"
					}
					lines = append(lines, fmt.Sprintf("Queue:   mode=%s cap=%s drop=%s", qMode, qCap, qDrop))
				}
			}
		}
		return strings.Join(lines, "\n"), nil
	})

	slashRouter.Register("model", func(_ context.Context, cmd autoreply.Command) (string, error) {
		if len(cmd.Args) == 0 {
			agentID := sessionRouter.Get(cmd.SessionID)
			if agentID == "" {
				agentID = "main"
			}
			return fmt.Sprintf("Current agent: %s\nUsage: /model <model-name>", agentID), nil
		}
		modelName := cmd.Args[0]
		rt, rtErr := agent.BuildRuntimeForModel(modelName, tools)
		if rtErr != nil {
			return fmt.Sprintf("⚠️  Unknown or unconfigured model %q: %v", modelName, rtErr), nil
		}
		// Register an ephemeral per-session agent so other sessions are unaffected.
		ephemeralID := "session-model:" + cmd.SessionID
		if len(ephemeralID) > 48 {
			ephemeralID = ephemeralID[:48]
		}
		agentRegistry.Set(ephemeralID, rt)
		sessionRouter.Assign(cmd.SessionID, ephemeralID)
		// Persist model override in session store.
		if sessionStore != nil {
			se := sessionStore.GetOrNew(cmd.SessionID)
			se.ModelOverride = modelName
			if putErr := sessionStore.Put(cmd.SessionID, se); putErr != nil {
				log.Printf("session store put failed session=%s: %v", cmd.SessionID, putErr)
			}
		}
		return fmt.Sprintf("✓ Switched to model %q for this session.", modelName), nil
	})

	// seenChannelSessions tracks which channel sessionIDs have had session_start
	// fired; must be declared here so rotateSession can clear it on reset.
	var seenChannelSessions sync.Map // key: sessionID string → struct{}

	// rotateSession clears transcript and session state, carrying over flags.
	// For ACP-bound sessions ("acp:*"), the sessionID is preserved (in-place reset).
	// It returns the response string for the command.
	rotateSession := func(cmdCtx context.Context, sessionID, reason string) string {
		isACP := strings.HasPrefix(sessionID, "acp:")
		if err := rotateSessionCoordinated(cmdCtx, sessionID, reason, isACP, chatCancels, steeringMailboxes, sessionRouter, &seenChannelSessions, hooksMgr, transcriptRepo, sessionStore, configState.Get()); err != nil {
			log.Printf("session rotation warning session=%s reason=%s err=%v", sessionID, reason, err)
		}

		if isACP {
			return "🔄 ACP session reset in-place. Conversation history cleared."
		}
		return "🔄 Session reset. Conversation history cleared — starting fresh."
	}

	slashRouter.Register("reset", func(cmdCtx context.Context, cmd autoreply.Command) (string, error) {
		return rotateSession(cmdCtx, cmd.SessionID, "slash:/reset"), nil
	})

	slashRouter.Register("new", func(cmdCtx context.Context, cmd autoreply.Command) (string, error) {
		return rotateSession(cmdCtx, cmd.SessionID, "slash:/new"), nil
	})

	slashRouter.Register("kill", func(_ context.Context, cmd autoreply.Command) (string, error) {
		chatCancels.Abort(cmd.SessionID)
		return "🛑 Aborted in-flight turn.", nil
	})

	// /stop — alias for /kill.
	slashRouter.Register("stop", func(_ context.Context, cmd autoreply.Command) (string, error) {
		chatCancels.Abort(cmd.SessionID)
		return "🛑 Aborted in-flight turn.", nil
	})

	// /set <flag> [value] — persist a per-session flag.
	slashRouter.Register("set", func(_ context.Context, cmd autoreply.Command) (string, error) {
		if len(cmd.Args) == 0 {
			return "Usage: /set <flag> [value]\nFlags: verbose, thinking, tts, fast, model <name>, provider <name>, label <text>, thinking-level <off|minimal|low|medium|high|xhigh>, reasoning <low|medium|high>, verbose-level <quiet|normal|debug>, response-usage <off|on|tokens|full>, queue-cap <n>, queue-drop <summarize|oldest|newest>, queue-mode <collect|followup|queue>", nil
		}
		flag := strings.ToLower(cmd.Args[0])
		value := ""
		if len(cmd.Args) > 1 {
			value = strings.Join(cmd.Args[1:], " ")
		}
		boolVal := value == "" || value == "on" || value == "true" || value == "1"
		if sessionStore == nil {
			return "⚠️  Session store unavailable.", nil
		}
		se := sessionStore.GetOrNew(cmd.SessionID)
		switch flag {
		case "verbose":
			se.Verbose = boolVal
		case "thinking":
			se.Thinking = boolVal
		case "tts", "tts-auto":
			se.TTSAuto = boolVal
		case "fast", "fast-mode":
			se.FastMode = boolVal
		case "thinking-level":
			lvl := normalizeThinkingLevel(value)
			if lvl == "" {
				return "Usage: /set thinking-level <off|minimal|low|medium|high|xhigh>", nil
			}
			se.ThinkingLevel = lvl
		case "reasoning":
			lvl := normalizeReasoningLevel(value)
			if lvl == "" {
				return "Usage: /set reasoning <low|medium|high>", nil
			}
			se.ReasoningLevel = lvl
		case "verbose-level":
			lvl := normalizeVerboseLevel(value)
			if lvl == "" {
				return "Usage: /set verbose-level <quiet|normal|debug>", nil
			}
			se.VerboseLevel = lvl
		case "response-usage":
			mode := normalizeResponseUsage(value)
			if mode == "" {
				return "Usage: /set response-usage <off|on|tokens|full>", nil
			}
			se.ResponseUsage = mode
		case "queue-cap":
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n <= 0 {
				return "Usage: /set queue-cap <positive-int>", nil
			}
			se.QueueCap = n
		case "queue-drop":
			d := normalizeQueueDrop(value)
			if d == "" {
				return "Usage: /set queue-drop <summarize|oldest|newest>", nil
			}
			se.QueueDrop = d
		case "queue-mode":
			m := normalizeQueueMode(value)
			if m == "" {
				return "Usage: /set queue-mode <collect|followup|queue>", nil
			}
			se.QueueMode = m
		case "provider":
			if strings.TrimSpace(value) == "" {
				return "Usage: /set provider <name>", nil
			}
			se.ProviderOverride = strings.TrimSpace(value)
		case "model":
			if value == "" {
				return "Usage: /set model <model-name>", nil
			}
			se.ModelOverride = value
			rt, rtErr := agent.BuildRuntimeForModel(value, tools)
			if rtErr != nil {
				return fmt.Sprintf("⚠️  Unknown or unconfigured model %q: %v", value, rtErr), nil
			}
			ephemeralID := "session-model:" + cmd.SessionID
			if len(ephemeralID) > 48 {
				ephemeralID = ephemeralID[:48]
			}
			agentRegistry.Set(ephemeralID, rt)
			sessionRouter.Assign(cmd.SessionID, ephemeralID)
		case "label":
			if value == "" {
				return "Usage: /set label <text>", nil
			}
			se.Label = value
		default:
			return fmt.Sprintf("⚠️  Unknown flag %q.", flag), nil
		}
		if err := sessionStore.Put(cmd.SessionID, se); err != nil {
			return fmt.Sprintf("⚠️  Failed to persist: %v", err), nil
		}
		return fmt.Sprintf("✓ Set %s.", flag), nil
	})

	// /unset <flag> — clear a per-session flag.
	slashRouter.Register("unset", func(_ context.Context, cmd autoreply.Command) (string, error) {
		if len(cmd.Args) == 0 {
			return "Usage: /unset <flag>\nFlags: verbose, thinking, tts, fast, thinking-level, reasoning, verbose-level, response-usage, queue-cap, queue-drop, queue-mode, provider, model, label", nil
		}
		flag := strings.ToLower(cmd.Args[0])
		if sessionStore == nil {
			return "⚠️  Session store unavailable.", nil
		}
		se := sessionStore.GetOrNew(cmd.SessionID)
		switch flag {
		case "verbose":
			se.Verbose = false
		case "thinking":
			se.Thinking = false
		case "tts", "tts-auto":
			se.TTSAuto = false
		case "fast", "fast-mode":
			se.FastMode = false
		case "thinking-level":
			se.ThinkingLevel = ""
		case "reasoning":
			se.ReasoningLevel = ""
		case "verbose-level":
			se.VerboseLevel = ""
		case "response-usage":
			se.ResponseUsage = ""
		case "queue-cap":
			se.QueueCap = 0
		case "queue-drop":
			se.QueueDrop = ""
		case "queue-mode":
			se.QueueMode = ""
		case "provider":
			se.ProviderOverride = ""
		case "model":
			se.ModelOverride = ""
			sessionRouter.Assign(cmd.SessionID, "")
		case "label":
			se.Label = ""
		default:
			return fmt.Sprintf("⚠️  Unknown flag %q.", flag), nil
		}
		if err := sessionStore.Put(cmd.SessionID, se); err != nil {
			return fmt.Sprintf("⚠️  Failed to persist: %v", err), nil
		}
		return fmt.Sprintf("✓ Unset %s.", flag), nil
	})

	// /fast on|off — convenience toggle for fast-mode.
	slashRouter.Register("fast", func(_ context.Context, cmd autoreply.Command) (string, error) {
		return applyFastSlash(sessionStore, cmd.SessionID, cmd.Args), nil
	})

	// /usage [off|on|tokens|full] — show or set per-session usage reporting mode.
	slashRouter.Register("usage", func(_ context.Context, cmd autoreply.Command) (string, error) {
		return applyUsageSlash(sessionStore, cmd.SessionID, cmd.Args), nil
	})

	// /session [show|list|reset|delete] — session management controls.
	slashRouter.Register("session", func(cmdCtx context.Context, cmd autoreply.Command) (string, error) {
		if sessionStore == nil {
			return "⚠️  Session store unavailable.", nil
		}
		sub := "show"
		if len(cmd.Args) > 0 {
			sub = strings.ToLower(strings.TrimSpace(cmd.Args[0]))
		}
		switch sub {
		case "show":
			sessionID := cmd.SessionID
			if len(cmd.Args) > 1 && strings.TrimSpace(cmd.Args[1]) != "" {
				sessionID = strings.TrimSpace(cmd.Args[1])
			}
			se, ok := sessionStore.Get(sessionID)
			if !ok {
				return fmt.Sprintf("Session %q not found.", sessionID), nil
			}
			mode := se.ResponseUsage
			if mode == "" {
				mode = "off"
			}
			lines := []string{
				fmt.Sprintf("Session: %s", sessionID),
				fmt.Sprintf("Label:   %s", fallbackText(se.Label, "-")),
				fmt.Sprintf("Model:   %s", fallbackText(se.ModelOverride, "-")),
				fmt.Sprintf("Provider:%s", prefixIfNeeded(fallbackText(se.ProviderOverride, "-"), " ")),
				fmt.Sprintf("Fast:    %t", se.FastMode),
				fmt.Sprintf("Usage:   %s", mode),
				fmt.Sprintf("Tokens:  %d in / %d out / %d total", se.InputTokens, se.OutputTokens, se.TotalTokens),
				fmt.Sprintf("Queue:   mode=%s cap=%d drop=%s", fallbackText(se.QueueMode, "collect"), se.QueueCap, fallbackText(se.QueueDrop, "summarize")),
			}
			if se.FallbackTo != "" {
				lines = append(lines, fmt.Sprintf("Fallback:%s → %s", prefixIfNeeded(fallbackText(se.FallbackFrom, "primary"), " "), se.FallbackTo))
			}
			return strings.Join(lines, "\n"), nil
		case "list":
			all := sessionStore.List()
			type row struct {
				key   string
				entry state.SessionEntry
			}
			rows := make([]row, 0, len(all))
			for key, entry := range all {
				rows = append(rows, row{key: key, entry: entry})
			}
			sort.Slice(rows, func(i, j int) bool {
				return rows[i].entry.UpdatedAt.After(rows[j].entry.UpdatedAt)
			})
			if len(rows) == 0 {
				return "No sessions.", nil
			}
			limit := 20
			if len(cmd.Args) > 1 {
				if n, err := strconv.Atoi(strings.TrimSpace(cmd.Args[1])); err == nil && n > 0 {
					limit = n
				}
			}
			if len(rows) > limit {
				rows = rows[:limit]
			}
			lines := []string{fmt.Sprintf("Sessions (%d shown):", len(rows))}
			for _, row := range rows {
				lines = append(lines, fmt.Sprintf("- %s  label=%s  updated=%s",
					row.key,
					fallbackText(row.entry.Label, "-"),
					row.entry.UpdatedAt.Format(time.RFC3339),
				))
			}
			return strings.Join(lines, "\n"), nil
		case "reset":
			target := cmd.SessionID
			if len(cmd.Args) > 1 && strings.TrimSpace(cmd.Args[1]) != "" {
				target = strings.TrimSpace(cmd.Args[1])
			}
			return rotateSession(cmdCtx, target, "slash:/session reset"), nil
		case "delete":
			if len(cmd.Args) < 2 || strings.TrimSpace(cmd.Args[1]) == "" {
				return "Usage: /session delete <session-id>", nil
			}
			target := strings.TrimSpace(cmd.Args[1])
			if err := deleteSessionCoordinated(cmdCtx, target, chatCancels, steeringMailboxes, sessionRouter, &seenChannelSessions, docsRepo, transcriptRepo, sessionStore); err != nil {
				return fmt.Sprintf("⚠️  Failed to delete session: %v", err), nil
			}
			return fmt.Sprintf("🗑️ Session %s deleted.", target), nil
		default:
			return "Usage: /session [show|list|reset|delete]", nil
		}
	})

	// /restart — restart the current conversational session (alias to reset/new).
	slashRouter.Register("restart", func(cmdCtx context.Context, cmd autoreply.Command) (string, error) {
		return rotateSession(cmdCtx, cmd.SessionID, "slash:/restart"), nil
	})

	// /send on|off — enable or suppress reply delivery for this session.
	slashRouter.Register("send", func(_ context.Context, cmd autoreply.Command) (string, error) {
		arg := ""
		if len(cmd.Args) > 0 {
			arg = strings.ToLower(cmd.Args[0])
		}
		if arg == "" {
			return "Usage: /send on|off", nil
		}
		suppress := arg == "off" || arg == "false" || arg == "0"
		if sessionStore == nil {
			return "⚠️  Session store unavailable.", nil
		}
		se := sessionStore.GetOrNew(cmd.SessionID)
		se.SendSuppressed = suppress
		if err := sessionStore.Put(cmd.SessionID, se); err != nil {
			return fmt.Sprintf("⚠️  Failed to persist: %v", err), nil
		}
		if suppress {
			return "🔇 Reply delivery suppressed. Use /send on to re-enable.", nil
		}
		return "🔊 Reply delivery enabled.", nil
	})

	// /context list — list workspace bootstrap files present on disk.
	slashRouter.Register("context", func(_ context.Context, cmd autoreply.Command) (string, error) {
		sub := ""
		if len(cmd.Args) > 0 {
			sub = strings.ToLower(cmd.Args[0])
		}
		if sub == "" || sub == "list" || sub == "detail" {
			cfg := configState.Get()
			activeAgentID := sessionRouter.Get(cmd.SessionID)
			if activeAgentID == "" {
				activeAgentID = "main"
			}
			wsDir := workspace.ResolveWorkspaceDir(cfg, activeAgentID)
			candidates := []string{
				"AGENTS.md", "SOUL.md", "USER.md", "IDENTITY.md",
				"TOOLS.md", "HEARTBEAT.md", "BOOT.md", "BOOTSTRAP.md", "MEMORY.md",
			}
			lines := []string{fmt.Sprintf("Workspace: %s", wsDir)}
			for _, f := range candidates {
				fp := filepath.Join(wsDir, f)
				info, err := os.Stat(fp)
				if err == nil {
					lines = append(lines, fmt.Sprintf("  ✓ %s (%d bytes)", f, info.Size()))
				} else {
					lines = append(lines, fmt.Sprintf("  · %s (missing)", f))
				}
			}
			return strings.Join(lines, "\n"), nil
		}
		return fmt.Sprintf("Unknown subcommand %q. Usage: /context list", sub), nil
	})

	// /info — agent / node info.
	slashRouter.Register("info", func(_ context.Context, cmd autoreply.Command) (string, error) {
		agentID := sessionRouter.Get(cmd.SessionID)
		if agentID == "" {
			agentID = "main"
		}
		lines := []string{
			fmt.Sprintf("Metiq v%s", version),
			fmt.Sprintf("Pubkey: %s", pubkey),
			fmt.Sprintf("Agent:  %s", agentID),
		}
		if sessionStore != nil {
			if se, ok := sessionStore.Get(cmd.SessionID); ok {
				if se.ModelOverride != "" {
					lines = append(lines, fmt.Sprintf("Model override: %s", se.ModelOverride))
				}
				if se.ProviderOverride != "" {
					lines = append(lines, fmt.Sprintf("Provider override: %s", se.ProviderOverride))
				}
				if se.Label != "" {
					lines = append(lines, fmt.Sprintf("Label: %s", se.Label))
				}
				if se.FallbackTo != "" {
					lines = append(lines, fmt.Sprintf("Fallback: %s → %s", fallbackText(se.FallbackFrom, "primary"), se.FallbackTo))
					if se.FallbackReason != "" {
						lines = append(lines, fmt.Sprintf("Fallback reason: %s", truncateRunes(se.FallbackReason, 160)))
					}
				}
			}
		}
		return strings.Join(lines, "\n"), nil
	})

	// /summary — force the maintained session-memory artifact to be current.
	slashRouter.Register("summary", func(cmdCtx context.Context, cmd autoreply.Command) (string, error) {
		outcome, err := ensureSessionMemoryCurrent(cmdCtx, configState.Get(), cmd.SessionID, sessionStore)
		if err != nil {
			return fmt.Sprintf("⚠️  Session memory update failed: %v", err), nil
		}
		if strings.TrimSpace(outcome.Path) == "" {
			return "⚠️  Session memory is not available for this session.", nil
		}
		if outcome.Updated {
			return fmt.Sprintf("✓ Session memory updated: %s", outcome.Path), nil
		}
		return fmt.Sprintf("✓ Session memory already current: %s", outcome.Path), nil
	})

	// /compact — compact conversation history via the context engine.
	// Tries LLM-free session memory compaction first; falls back to regular compact.
	slashRouter.Register("compact", func(cmdCtx context.Context, cmd autoreply.Command) (string, error) {
		if controlContextEngine == nil {
			return "⚠️  No context engine active.", nil
		}
		flushOutcome, err := ensureSessionMemoryCurrent(cmdCtx, configState.Get(), cmd.SessionID, sessionStore)
		if err != nil {
			return fmt.Sprintf("⚠️  Session memory flush failed: %v", err), nil
		}
		// Try LLM-free session memory compaction first.
		if smCR, smOK := trySessionMemoryCompact(cmdCtx, controlContextEngine, cmd.SessionID, flushOutcome.Path, sessionMemoryLastEntryID(sessionStore, cmd.SessionID)); smOK {
			recordSessionCompaction(sessionStore, cmd.SessionID, true, time.Now())
			runPostCompactCleanup(cmd.SessionID)
			saved := smCR.TokensBefore - smCR.TokensAfter
			if saved > 0 {
				return fmt.Sprintf("✓ Compacted (session memory). %d tokens freed.", saved), nil
			}
			return "✓ Compacted (session memory).", nil
		}
		// Fall back to regular compaction.
		cr, cErr := controlContextEngine.Compact(cmdCtx, cmd.SessionID)
		if cErr != nil {
			return fmt.Sprintf("⚠️  Compact failed: %v", cErr), nil
		}
		recordSessionCompaction(sessionStore, cmd.SessionID, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
		if !cr.Compacted {
			return "Nothing to compact yet.", nil
		}
		runPostCompactCleanup(cmd.SessionID)
		saved := cr.TokensBefore - cr.TokensAfter
		if saved > 0 {
			return fmt.Sprintf("✓ Compacted. %d tokens freed.", saved), nil
		}
		return "✓ Compacted.", nil
	})

	// /export — export session transcript as HTML and return a summary.
	slashRouter.Register("export", func(cmdCtx context.Context, cmd autoreply.Command) (string, error) {
		entries, lErr := transcriptRepo.ListSessionAll(cmdCtx, cmd.SessionID)
		if lErr != nil {
			return fmt.Sprintf("⚠️  Export failed: %v", lErr), nil
		}
		msgs := make([]exportpkg.Message, 0, len(entries))
		for _, e := range entries {
			if e.Role == "deleted" || e.Role == "" {
				continue
			}
			msgs = append(msgs, exportpkg.Message{
				Role:      e.Role,
				Content:   e.Text,
				Timestamp: e.Unix,
				ID:        e.EntryID,
			})
		}
		if len(msgs) == 0 {
			return "Nothing to export yet.", nil
		}
		agentName := ""
		if agDoc, err2 := docsRepo.GetAgent(cmdCtx, "main"); err2 == nil {
			agentName = agDoc.Name
		}
		_, exportErr := exportpkg.SessionToHTML(exportpkg.SessionHTMLOptions{
			SessionID:  cmd.SessionID,
			AgentID:    "main",
			AgentName:  agentName,
			PubKey:     cmd.FromPubKey,
			Messages:   msgs,
			ExportedAt: time.Now(),
		})
		if exportErr != nil {
			return fmt.Sprintf("⚠️  Export render failed: %v", exportErr), nil
		}
		return fmt.Sprintf("✓ Exported %d messages. (Full HTML available via the gateway sessions.export method.)", len(msgs)), nil
	})

	slashRouter.Register("agents", func(_ context.Context, cmd autoreply.Command) (string, error) {
		ids := agentRegistry.Registered()
		if len(ids) == 0 {
			return "No agents registered.", nil
		}
		lines := make([]string, 0, len(ids)+1)
		lines = append(lines, "Registered agents:")
		for _, id := range ids {
			lines = append(lines, "  "+id)
		}
		return strings.Join(lines, "\n"), nil
	})

	// /focus <agent-name> — route this session to a named agent.
	slashRouter.Register("focus", func(_ context.Context, cmd autoreply.Command) (string, error) {
		if len(cmd.Args) == 0 {
			current := sessionRouter.Get(cmd.SessionID)
			if current == "" {
				current = "main"
			}
			return fmt.Sprintf("Currently focused on: %s\nUsage: /focus <agent-name>", current), nil
		}
		agentName := cmd.Args[0]
		if agentRegistry.Get(agentName) == nil {
			return fmt.Sprintf("⚠️  Agent %q not found. Use /agents to list available agents.", agentName), nil
		}
		sessionRouter.Assign(cmd.SessionID, agentName)
		return fmt.Sprintf("✓ Session now focused on agent: %s", agentName), nil
	})

	// /unfocus — reset session routing to the default agent.
	slashRouter.Register("unfocus", func(_ context.Context, cmd autoreply.Command) (string, error) {
		sessionRouter.Assign(cmd.SessionID, "")
		return "✓ Unfocused — session reset to default agent.", nil
	})

	// /spawn <agent-name> [instructions...] — spawn and focus a subagent session.
	slashRouter.Register("spawn", func(cmdCtx context.Context, cmd autoreply.Command) (string, error) {
		if len(cmd.Args) == 0 {
			return "Usage: /spawn <agent-name> [initial instructions]", nil
		}
		agentName := cmd.Args[0]
		if agentRegistry.Get(agentName) == nil {
			return fmt.Sprintf("⚠️  Agent %q not found. Use /agents to list available agents.", agentName), nil
		}
		// Route session to the named agent.
		sessionRouter.Assign(cmd.SessionID, agentName)
		instructions := ""
		if len(cmd.Args) > 1 {
			instructions = strings.Join(cmd.Args[1:], " ")
		}
		if instructions != "" {
			return fmt.Sprintf("✓ Spawned and focused on agent: %s\nFirst message: %q", agentName, instructions), nil
		}
		return fmt.Sprintf("✓ Spawned and focused on agent: %s", agentName), nil
	})
	// ─────────────────────────────────────────────────────────────────────────

	// dmDebounceWindow is read from Extra["messages"]["inbound"]["debounce_ms"].
	// 0ms (default) = no debounce; each DM is processed immediately.
	dmDebounceWindow := time.Duration(0)
	if mExtra, ok := configState.Get().Extra["messages"].(map[string]any); ok {
		if inExtra, ok := mExtra["inbound"].(map[string]any); ok {
			if ms, ok := inExtra["debounce_ms"].(float64); ok && ms > 0 {
				dmDebounceWindow = time.Duration(ms) * time.Millisecond
			}
		}
	}

	// dmReplyFns stores the most recent reply function per fromPubKey so the
	// DM debouncer can reply with the combined message via the correct channel.
	var dmReplyFnsMu sync.Mutex
	dmReplyFns := make(map[string]func(context.Context, string) error)

	// dmRunAgentTurn is the core DM agent-dispatch logic, called either directly
	// from dmOnMessage (no debounce) or from the dmDebouncer flush.
	var runInboundTurn func(
		ctx context.Context,
		sessionID, senderID, combinedText, eventID string,
		createdAt int64,
		replyFn func(context.Context, string) error,
		overrideAgentID string,
		constraints turnToolConstraints,
		handoffToken uint64,
	)
	runInboundTurn = func(
		ctx context.Context,
		sessionID, senderID, combinedText, eventID string,
		createdAt int64,
		replyFn func(context.Context, string) error,
		overrideAgentID string,
		constraints turnToolConstraints,
		handoffToken uint64,
	) {
		sessionID = strings.TrimSpace(sessionID)
		senderID = strings.TrimSpace(senderID)
		if sessionID == "" {
			sessionID = senderID
		}
		if sessionStore != nil {
			se := sessionStore.GetOrNew(sessionID)
			se.LastChannel = "nostr"
			se.LastTo = senderID
			if putErr := sessionStore.Put(sessionID, se); putErr != nil {
				log.Printf("session store put failed session=%s: %v", sessionID, putErr)
			}
		}

		// Per-session turn serialisation.  If the slot is busy, enqueue the
		// message so it is processed when the current turn finishes.  The queue
		// drain runs in the defer registered below (LIFO: it executes after
		// releaseTurnSlot fires, so the slot is free when the goroutine runs).
		sessionDMQ := dmQueues.Get(sessionID)
		var sessionEntry *state.SessionEntry
		if sessionStore != nil {
			if se, ok := sessionStore.Get(sessionID); ok {
				tmp := se
				sessionEntry = &tmp
			}
		}
		queueSettings := resolveQueueRuntimeSettings(configState.Get(), sessionEntry, "", 10)
		sessionDMQ.Configure(queueSettings.Cap, queueSettings.Drop)

		acquireTurnSlot := func() (func(), bool) {
			if handoffToken == 0 && turnHandoffs.Has(sessionID) {
				return nil, false
			}
			release, ok := sessionTurns.TryAcquire(sessionID)
			if !ok {
				return nil, false
			}
			if handoffToken != 0 {
				if !turnHandoffs.ConsumeIfMatch(sessionID, handoffToken) {
					return nil, false
				}
				return release, true
			}
			if turnHandoffs.Has(sessionID) {
				release()
				return nil, false
			}
			return release, true
		}
		releaseTurnSlot, acquired := acquireTurnSlot()
		if !acquired {
			switch queueSettings.Mode {
			case "steer":
				handleBusySteer(steeringMailboxes, sessionDMQ, queueSettings, activeRunSteeringInput{
					SessionID:    sessionID,
					Text:         combinedText,
					EventID:      eventID,
					SenderID:     senderID,
					Source:       "dm",
					AgentID:      strings.TrimSpace(overrideAgentID),
					ToolProfile:  constraints.ToolProfile,
					EnabledTools: append([]string(nil), constraints.EnabledTools...),
					CreatedAt:    createdAt,
					Priority:     autoreply.SteeringPriorityNormal,
				})
				return
			case "interrupt":
				if handleBusyInterrupt(chatCancels, activeTools, steeringMailboxes, sessionDMQ, queueSettings, activeRunSteeringInput{
					SessionID:    sessionID,
					Text:         combinedText,
					EventID:      eventID,
					SenderID:     senderID,
					Source:       "dm",
					AgentID:      strings.TrimSpace(overrideAgentID),
					ToolProfile:  constraints.ToolProfile,
					EnabledTools: append([]string(nil), constraints.EnabledTools...),
					CreatedAt:    createdAt,
				}) {
					return
				}
			}
			sessionDMQ.Enqueue(autoreply.PendingTurn{
				Text:         combinedText,
				EventID:      eventID,
				SenderID:     senderID,
				AgentID:      strings.TrimSpace(overrideAgentID),
				ToolProfile:  constraints.ToolProfile,
				EnabledTools: append([]string(nil), constraints.EnabledTools...),
				CreatedAt:    createdAt,
			})
			log.Printf("dm session busy, queued: session=%s mode=%s queue_len=%d", sessionID, queueSettings.Mode, sessionDMQ.Len())
			return
		}

		// Queue-drain defer — registered BEFORE releaseTurnSlot so it runs
		// AFTER the slot is released (Go defers are LIFO).  Any DMs that
		// arrived while this turn was processing are dispatched here.
		var nextHandoffToken uint64
		defer func() {
			steeringPending := drainSteeringAsPending(steeringMailboxes, sessionID)
			pending := sessionDMQ.Dequeue()
			if len(steeringPending) > 0 {
				combinedPending := append(append([]autoreply.PendingTurn(nil), steeringPending...), pending...)
				first := combinedPending[0]
				enqueuePendingTurns(sessionDMQ, combinedPending[1:])
				log.Printf("dm active-run steering fallback dispatch: session=%s remaining=%d", sessionID, len(combinedPending)-1)
				go runInboundTurn(ctx, sessionID, first.SenderID, first.Text, first.EventID, pendingTurnCreatedAt(first), replyFn, first.AgentID, turnToolConstraints{ToolProfile: first.ToolProfile, EnabledTools: append([]string(nil), first.EnabledTools...)}, nextHandoffToken)
				return
			}
			if len(pending) == 0 {
				return
			}
			mode := queueSettings.Mode
			if sessionStore != nil {
				if se, ok := sessionStore.Get(sessionID); ok {
					mode = resolveQueueRuntimeSettings(configState.Get(), &se, "", 10).Mode
				}
			}
			if queueModeCollect(mode) {
				if !pendingTurnsShareExecutionContext(pending) {
					log.Printf("dm queue drain collect->sequential fallback: session=%s items=%d", sessionID, len(pending))
					first := pending[0]
					for _, pt := range pending[1:] {
						sessionDMQ.Enqueue(pt)
					}
					go runInboundTurn(ctx, sessionID, first.SenderID, first.Text, first.EventID, pendingTurnCreatedAt(first), replyFn, first.AgentID, turnToolConstraints{ToolProfile: first.ToolProfile, EnabledTools: append([]string(nil), first.EnabledTools...)}, nextHandoffToken)
					return
				}
				var texts []string
				var latestEventID string
				var latestCreatedAt int64
				for _, pt := range pending {
					texts = append(texts, pt.Text)
					if pt.EventID != "" {
						latestEventID = pt.EventID
					}
					if pendingTurnCreatedAt(pt) > latestCreatedAt {
						latestCreatedAt = pendingTurnCreatedAt(pt)
					}
				}
				combined := strings.Join(texts, "\n\n")
				if len(pending) > 1 {
					combined = fmt.Sprintf("[%d messages received while agent was busy]\n\n%s", len(pending), combined)
				}
				log.Printf("dm queue drain: session=%s items=%d mode=%s", sessionID, len(pending), mode)
				latest := pending[len(pending)-1]
				go runInboundTurn(ctx, sessionID, latest.SenderID, combined, latestEventID, latestCreatedAt, replyFn, latest.AgentID, turnToolConstraints{ToolProfile: latest.ToolProfile, EnabledTools: append([]string(nil), latest.EnabledTools...)}, nextHandoffToken)
				return
			}

			if queueModeSequential(mode) {
				log.Printf("dm queue drain sequential: session=%s items=%d mode=%s", sessionID, len(pending), mode)
				first := pending[0]
				for _, pt := range pending[1:] {
					sessionDMQ.Enqueue(pt)
				}
				go runInboundTurn(ctx, sessionID, first.SenderID, first.Text, first.EventID, pendingTurnCreatedAt(first), replyFn, first.AgentID, turnToolConstraints{ToolProfile: first.ToolProfile, EnabledTools: append([]string(nil), first.EnabledTools...)}, nextHandoffToken)
				return
			}

			// Steer/interrupt fallback after drain: run newest only.
			latest := pending[len(pending)-1]
			go runInboundTurn(ctx, sessionID, latest.SenderID, latest.Text, latest.EventID, pendingTurnCreatedAt(latest), replyFn, latest.AgentID, turnToolConstraints{ToolProfile: latest.ToolProfile, EnabledTools: append([]string(nil), latest.EnabledTools...)}, nextHandoffToken)
		}()

		defer releaseTurnSlot()
		defer func() {
			if sessionDMQ.Len() > 0 || steeringMailboxLen(steeringMailboxes, sessionID) > 0 {
				nextHandoffToken = turnHandoffs.Reserve(sessionID)
			}
		}()
		defer func() {
			clearCtx, clearCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer clearCancel()
			setSessionActiveTurn(clearCtx, docsRepo, sessionID, senderID, false)
		}()
		setSessionActiveTurn(ctx, docsRepo, sessionID, senderID, true)

		entryID := eventID
		if entryID == "" {
			// Synthesise an entry ID for messages without a Nostr event ID
			// (e.g. watch delivery, queue drain).  We hash stable message
			// fields so the ID remains deterministic and content-derived.
			entryID = synthesizeInboundEventID(senderID, combinedText, createdAt)
		}
		emitPluginMessageReceived(ctx, pluginhooks.MessageReceivedEvent{ChannelID: "nostr", SenderID: senderID, Text: combinedText, EventID: entryID, SessionID: sessionID, AgentID: defaultAgentID(overrideAgentID), CreatedAt: createdAt})
		if err := persistInbound(ctx, docsRepo, transcriptRepo, sessionID, nostruntime.InboundDM{
			EventID:    entryID,
			FromPubKey: senderID,
			Text:       combinedText,
			CreatedAt:  createdAt,
		}); err != nil {
			log.Printf("persist inbound text failed event=%s err=%v", entryID, err)
		}
		// Resolve agent ID and create the per-turn timeout context before any
		// potentially blocking memory embedding/search work.
		activeAgentID := defaultAgentID(overrideAgentID)
		if strings.TrimSpace(overrideAgentID) == "" {
			activeAgentID = defaultAgentID(sessionRouter.Get(sessionID))
		}
		turnCtxBase, releaseTurn := chatCancels.Begin(sessionID, ctx)
		// Default turn timeout matches OpenClaw's generous 48-hour default.
		// This is important for users running large local models with long context windows.
		const defaultTurnTimeoutSecs = 172800 // 48 hours
		const turnTimeoutGraceSecs = 1        // Grace period before timing out
		turnTimeoutSecs := defaultTurnTimeoutSecs
		for _, ac := range configState.Get().Agents {
			if ac.ID == activeAgentID && ac.TurnTimeoutSecs != 0 {
				turnTimeoutSecs = ac.TurnTimeoutSecs
				break
			}
		}
		var turnCtx context.Context
		var cancelTurnTimeout context.CancelFunc
		if turnTimeoutSecs > 0 {
			// Apply grace period to the timeout (matches OpenClaw behavior)
			actualTimeoutSecs := turnTimeoutSecs + turnTimeoutGraceSecs
			turnCtx, cancelTurnTimeout = context.WithTimeout(turnCtxBase, time.Duration(actualTimeoutSecs)*time.Second)
		} else {
			turnCtx = turnCtxBase
			cancelTurnTimeout = func() {}
		}
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in agent process session=%s panic=%v", sessionID, r)
			}
			cancelTurnTimeout()
			activeTools.ClearSession(sessionID)
			releaseTurn()
		}()

		// Attach configurable timeouts to turn context so tools can read them.
		turnCtx = toolbuiltin.WithTimeoutsConfig(turnCtx, configState.Get().Timeouts)

		scopeCtx := resolveMemoryScopeContext(turnCtx, configState.Get(), docsRepo, sessionStore, sessionID, activeAgentID, "")
		turnCtx = contextWithMemoryScope(turnCtx, scopeCtx)

		userMemoryDocs := scopedMemoryDocs(memory.ExtractFromTurn(sessionID, "user", entryID, combinedText, createdAt), scopeCtx)
		if len(userMemoryDocs) > 0 {
			go func(docs []state.MemoryDoc) {
				persistCtx, cancel := context.WithTimeout(context.Background(), cfgTimeouts.MemoryPersist(configState.Get().Timeouts))
				defer cancel()
				persistMemories(persistCtx, docsRepo, memoryRepo, memoryIndex, memoryTracker, docs)
			}(userMemoryDocs)
		}

		if controlContextEngine != nil {
			if _, ingErr := controlContextEngine.Ingest(ctx, sessionID, ctxengine.Message{
				Role:    "user",
				Content: combinedText,
				ID:      entryID,
				Unix:    createdAt,
			}); ingErr != nil {
				log.Printf("context engine ingest user session=%s err=%v", sessionID, ingErr)
			}
		}

		turnContext, surfacedFileMemory, memoryRecallSample := buildDynamicMemoryRecallContext(turnCtx, memoryIndex, scopeCtx, sessionID, combinedText, workspaceDirForAgent(configState.Get(), activeAgentID), sessionStore, 0)
		// Inject structured task state for context rehydration.
		if taskCtx := buildTaskStateContextBlock(sessionStore, sessionID); taskCtx != "" {
			turnContext = joinPromptSections(turnContext, taskCtx)
		}
		staticSystemPrompt := assembleMemorySystemPrompt(memoryIndex, scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID))
		// turnHistory carries prior conversation turns for multi-turn LLM context.
		var turnHistory []agent.ConversationMessage
		if controlContextEngine != nil {
			maxCtxTokens := 100_000
			if activeAgentID != "" {
				for _, ac := range configState.Get().Agents {
					if ac.ID == activeAgentID && ac.MaxContextTokens > 0 {
						maxCtxTokens = ac.MaxContextTokens
						break
					}
				}
			}
			assembled, asmErr := controlContextEngine.Assemble(turnCtx, sessionID, maxCtxTokens)
			if asmErr == nil {
				threshold := int(float64(maxCtxTokens) * 0.80)
				if assembled.EstimatedTokens > 0 && threshold > 0 && assembled.EstimatedTokens > threshold {
					// Circuit breaker: skip compaction after too many consecutive failures.
					if controlAutoCompactState.ShouldSkipCompaction(sessionID) {
						log.Printf("context engine auto-compact circuit-breaker open session=%s failures=%d", sessionID, controlAutoCompactState.ConsecutiveFailures(sessionID))
					} else {
						compacted := false
						flushOutcome, flushErr := ensureSessionMemoryCurrent(turnCtx, configState.Get(), sessionID, sessionStore)
						if flushErr != nil {
							log.Printf("context engine auto-compact skipped session=%s session_memory_err=%v", sessionID, flushErr)
							controlAutoCompactState.RecordFailure(sessionID)
						} else {
							// Try LLM-free session memory compaction first.
							smCR, smOK := trySessionMemoryCompact(turnCtx, controlContextEngine, sessionID, flushOutcome.Path, sessionMemoryLastEntryID(sessionStore, sessionID))
							if smOK {
								recordSessionCompaction(sessionStore, sessionID, true, time.Now())
								log.Printf("context engine auto-compact (session-memory) session=%s tokens_before=%d tokens_after=%d", sessionID, smCR.TokensBefore, smCR.TokensAfter)
								assembled, _ = controlContextEngine.Assemble(turnCtx, sessionID, maxCtxTokens)
								compacted = true
								runPostCompactCleanup(sessionID)
							} else if cr, cErr := controlContextEngine.Compact(turnCtx, sessionID); cErr == nil && cr.Compacted {
								// Fall back to regular compaction.
								recordSessionCompaction(sessionStore, sessionID, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
								log.Printf("context engine auto-compact session=%s tokens_before=%d tokens_after=%d", sessionID, cr.TokensBefore, cr.TokensAfter)
								assembled, _ = controlContextEngine.Assemble(turnCtx, sessionID, maxCtxTokens)
								compacted = true
								runPostCompactCleanup(sessionID)
							}
						}
						if compacted {
							controlAutoCompactState.RecordSuccess(sessionID)
						} else if flushErr == nil {
							controlAutoCompactState.RecordFailure(sessionID)
						}
					}
				}
				if assembled.SystemPromptAddition != "" {
					turnContext = joinPromptSections(turnContext, assembled.SystemPromptAddition)
				}
				// Convert assembled.Messages → turn.History.
				// Exclude the last message if it is the current user turn (just ingested)
				// to avoid duplication with turn.UserText.
				msgs := assembled.Messages
				if n := len(msgs); n > 0 {
					last := msgs[n-1]
					if last.Role == "user" && strings.TrimSpace(last.Content) == strings.TrimSpace(combinedText) {
						msgs = msgs[:n-1]
					}
				}
				for _, m := range msgs {
					cm := conversationMessageFromContext(m)
					turnHistory = append(turnHistory, cm)
				}
				if len(turnHistory) > 0 {
					log.Printf("context engine history session=%s messages=%d", sessionID, len(turnHistory))
				}
			} else {
				log.Printf("context engine assemble session=%s err=%v", sessionID, asmErr)
			}
		}

		// Refresh routed agent only for normal session-routed turns.
		if strings.TrimSpace(overrideAgentID) == "" {
			if resolved := sessionRouter.Get(sessionID); resolved != "" {
				activeAgentID = defaultAgentID(resolved)
			}
		}
		activeRuntime := agentRegistry.Get(activeAgentID)
		activeRuntime, turnExecutor, baseTurnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, activeRuntime, tools, constraints)

		sessionThinkingLevel := ""
		if sessionStore != nil {
			if se, ok := sessionStore.Get(sessionID); ok {
				sessionThinkingLevel = strings.TrimSpace(se.ThinkingLevel)
			}
		}
		promptEnvelope := buildTurnPromptEnvelope(turnPromptBuilderParams{
			Config:               configState.Get(),
			SessionID:            sessionID,
			AgentID:              activeAgentID,
			Channel:              "nostr",
			SelfPubkey:           pubkey,
			SelfNPub:             toolbuiltin.NostrNPubFromHex(pubkey),
			StaticSystemPrompt:   staticSystemPrompt,
			Context:              turnContext,
			Tools:                baseTurnTools,
			SessionThinkingLevel: sessionThinkingLevel,
		})
		staticSystemPrompt = promptEnvelope.StaticSystemPrompt
		turnContext = promptEnvelope.Context

		lastActivityAt := time.Now().UnixMilli()
		wsEmitter.Emit(gatewayws.EventAgentStatus, gatewayws.AgentStatusPayload{
			TS:             lastActivityAt,
			AgentID:        activeAgentID,
			Status:         "thinking",
			Session:        sessionID,
			LastActivityAt: lastActivityAt,
		})
		// NIP-38: signal to Nostr network that the agent is composing a response.
		if controlPresenceHeartbeat38 != nil {
			controlPresenceHeartbeat38.SetTyping(turnCtx, "processing request…")
		}

		heartbeatDone := make(chan struct{})
		var heartbeatOnce sync.Once
		stopHeartbeat := func() { heartbeatOnce.Do(func() { close(heartbeatDone) }) }
		go func() {
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-heartbeatDone:
					return
				case <-turnCtx.Done():
					return
				case t := <-ticker.C:
					wsEmitter.Emit(gatewayws.EventAgentStatus, gatewayws.AgentStatusPayload{
						TS:             t.UnixMilli(),
						AgentID:        activeAgentID,
						Status:         "busy",
						Session:        sessionID,
						LastActivityAt: lastActivityAt,
					})
				}
			}
		}()

		var turnResult agent.TurnResult
		var turnErr error
		turnStartedAt := time.Now()
		// Resolve thinking budget: per-agent ThinkingLevel takes precedence,
		// then the session-level Thinking bool (defaults to medium: 10 000 tokens).
		var thinkingBudget int
		if sessionStore != nil {
			if se, ok := sessionStore.Get(sessionID); ok {
				if se.ThinkingLevel != "" {
					thinkingBudget = thinkingLevelToBudget(se.ThinkingLevel)
				} else if se.Thinking {
					thinkingBudget = thinkingLevelToBudget("medium")
				}
			}
		}
		var maxAgenticIterations int
		for _, ac := range configState.Get().Agents {
			if ac.ID == activeAgentID {
				if ac.ThinkingLevel != "" {
					thinkingBudget = thinkingLevelToBudget(ac.ThinkingLevel)
				}
				maxAgenticIterations = ac.MaxAgenticIterations
				break
			}
		}

		// Derive the last assistant time from the session store's UpdatedAt.
		// This approximates the last assistant message time for the time-based
		// microcompact trigger — when the gap exceeds the cache TTL, the
		// prompt cache has expired and clearing old tool results is free.
		var lastAssistantTime time.Time
		if sessionStore != nil {
			if sessEntry, ok := sessionStore.Get(sessionID); ok {
				lastAssistantTime = sessEntry.UpdatedAt
			}
		}

		// Partition tools into inline and deferred sets. Deferred tools are
		// not sent with every API request — the model discovers them via
		// tool_search when needed, saving context budget.
		inlineTools, deferredTools := partitionTurnTools(turnExecutor, promptEnvelope.ContextWindowTokens)
		if deferredTools != nil && deferredTools.Count() > 0 {
			log.Printf("turn tools: %d inline, %d deferred (session=%s)",
				len(inlineTools), deferredTools.Count(), sessionID)
		}

		drainedSteering := &activeRunSteeringDrainTracker{}
		steeringDrainCommitted := false
		defer func() {
			if !steeringDrainCommitted && shouldRestoreDrainedSteering(turnErr) {
				restoreDrainedSteering(steeringMailboxes, queueSettings, sessionID, drainedSteering.Snapshot())
			}
		}()

		baseTurn := agent.Turn{
			SessionID:            sessionID,
			TurnID:               eventID,
			UserText:             combinedText,
			StaticSystemPrompt:   staticSystemPrompt,
			Context:              turnContext,
			History:              turnHistory,
			Tools:                inlineTools,
			Executor:             turnExecutor, // canonical filtered turn tool pool
			ThinkingBudget:       thinkingBudget,
			MaxAgenticIterations: maxAgenticIterations,
			ToolEventSink:        toolLifecycleSinkWithActiveTools(activeTools, toolLifecyclePersistenceSink(sessionStore, sessionID, toolLifecycleEmitter(wsEmitter, activeAgentID))),
			ContextWindowTokens:  promptEnvelope.ContextWindowTokens,
			HookInvoker:          controlHookInvoker,
			SteeringDrain:        makeActiveRunSteeringDrain(steeringMailboxes, sessionID, drainedSteering.Record),
			LastAssistantTime:    lastAssistantTime,
			DeferredTools:        deferredTools,
		}
		if sr, ok := activeRuntime.(agent.StreamingRuntime); ok {
			turnResult, turnErr = sr.ProcessTurnStreaming(turnCtx, baseTurn, func(chunk string) {
				wsEmitter.Emit(gatewayws.EventChatChunk, gatewayws.ChatChunkPayload{
					TS:        time.Now().UnixMilli(),
					AgentID:   activeAgentID,
					SessionID: sessionID,
					Text:      chunk,
				})
			})
		} else {
			turnResult, turnErr = activeRuntime.ProcessTurn(turnCtx, baseTurn)
		}
		if turnErr != nil {
			stopHeartbeat()
			if controlPresenceHeartbeat38 != nil {
				controlPresenceHeartbeat38.SetIdle(ctx)
			}
			// Persist any completed tool work from the failed turn so future
			// turns know what was attempted and what succeeded before the error.
			if partial, ok := agent.PartialTurnResult(turnErr); ok {
				if len(partial.ToolTraces) > 0 {
					if err := persistToolTraces(ctx, transcriptRepo, sessionID, eventID, partial.ToolTraces); err != nil {
						log.Printf("persist partial tool traces session=%s err=%v", sessionID, err)
					}
				}
				persistAndIngestTurnHistory(ctx, transcriptRepo, controlContextEngine, sessionID, eventID, partial.HistoryDelta, turnResultMetadataPtr(turnResult, turnErr))
				sessionMemoryRuntime.ObserveTurn(configState.Get(), runtimeSessionMemoryGenerator{runtime: activeRuntime}, sessionID, activeAgentID, sessionMemoryWorkspaceDir(scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID)), resolveAgentContextWindow(configState.Get(), activeAgentID), partial.HistoryDelta)
				// Distill structured episodic memory from the partial turn.
				if turnStateDocs := scopedMemoryDocs(distillTurnState(sessionID, eventID, partial.ToolTraces, partial.HistoryDelta, true), scopeCtx); len(turnStateDocs) > 0 {
					go func(docs []state.MemoryDoc) {
						pCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						persistMemories(pCtx, docsRepo, memoryRepo, memoryIndex, memoryTracker, docs)
					}(turnStateDocs)
				}
				updateSessionTaskState(sessionStore, sessionID, partial.ToolTraces, partial.HistoryDelta, true)
			}
			switch {
			case errors.Is(turnErr, context.DeadlineExceeded):
				log.Printf("agent turn timed out session=%s timeout_secs=%d", sessionID, turnTimeoutSecs)
				if replyFn != nil {
					_ = replyFn(ctx, fmt.Sprintf("⏱️ Turn timed out after %d seconds. The request may have been too complex or the AI service is slow. Please try again.", turnTimeoutSecs))
				}
			case errors.Is(turnErr, context.Canceled):
				log.Printf("agent process aborted session=%s", sessionID)
			default:
				log.Printf("agent process failed session=%s err=%v", sessionID, turnErr)
			}
			turnTelemetry := buildTurnTelemetry(eventID, turnStartedAt, time.Now(), turnResult, turnErr, false, "", "", "")
			persistTurnTelemetry(sessionStore, sessionID, turnTelemetry)
			emitTurnTelemetry(wsEmitter, activeAgentID, sessionID, turnTelemetry)
			// Do NOT mark as processed on failure — the agent will retry
			// this message on recovery (within the replay/since window).
			// This ensures transient LLM errors don't permanently drop
			// user messages.
			if eventID != "" && !strings.HasPrefix(eventID, "watch:") {
				log.Printf("dm turn failed, event NOT marked processed (will retry on recovery) event=%s session=%s", eventID, sessionID)
			}
			return
		}
		stopHeartbeat()
		// NIP-38: return to idle once the agent turn is complete.
		if controlPresenceHeartbeat38 != nil {
			controlPresenceHeartbeat38.SetIdle(ctx)
		}

		inlineSteering := drainedSteering.Snapshot()
		for _, steered := range inlineSteering {
			if strings.ToLower(strings.TrimSpace(steered.Source)) != "dm" {
				continue
			}
			steeredSender := strings.TrimSpace(steered.SenderID)
			if steeredSender == "" {
				steeredSender = senderID
			}
			steeredCreatedAt := steered.CreatedAt
			if steeredCreatedAt <= 0 {
				steeredCreatedAt = time.Now().Unix()
			}
			steeredEntryID := strings.TrimSpace(steered.EventID)
			if steeredEntryID == "" {
				steeredEntryID = synthesizeInboundEventID(steeredSender, steered.Text, steeredCreatedAt)
			}
			if err := persistInbound(ctx, docsRepo, transcriptRepo, sessionID, nostruntime.InboundDM{
				EventID:    steeredEntryID,
				FromPubKey: steeredSender,
				Text:       steered.Text,
				CreatedAt:  steeredCreatedAt,
			}); err != nil {
				log.Printf("persist inline steering failed event=%s err=%v", steeredEntryID, err)
			}
			if controlContextEngine != nil {
				if _, ingErr := controlContextEngine.Ingest(ctx, sessionID, ctxengine.Message{Role: "user", Content: steered.Text, ID: steeredEntryID, Unix: steeredCreatedAt}); ingErr != nil {
					log.Printf("context engine ingest inline steering session=%s err=%v", sessionID, ingErr)
				}
			}
		}

		if err := persistToolTraces(ctx, transcriptRepo, sessionID, eventID, turnResult.ToolTraces); err != nil {
			log.Printf("persist tool traces failed session=%s err=%v", sessionID, err)
		}
		// Persist the full tool-call/tool-result history so future turns can
		// see prior tool usage — fixes the "announce and forget" behaviour.
		persistAndIngestTurnHistory(ctx, transcriptRepo, controlContextEngine, sessionID, eventID, turnResult.HistoryDelta, turnResultMetadataPtr(turnResult, nil))
		sessionMemoryRuntime.ObserveTurn(configState.Get(), runtimeSessionMemoryGenerator{runtime: activeRuntime}, sessionID, activeAgentID, sessionMemoryWorkspaceDir(scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID)), resolveAgentContextWindow(configState.Get(), activeAgentID), turnResult.HistoryDelta)
		// Distill structured episodic memory from the completed turn.
		if turnStateDocs := scopedMemoryDocs(distillTurnState(sessionID, eventID, turnResult.ToolTraces, turnResult.HistoryDelta, false), scopeCtx); len(turnStateDocs) > 0 {
			go func(docs []state.MemoryDoc) {
				pCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				persistMemories(pCtx, docsRepo, memoryRepo, memoryIndex, memoryTracker, docs)
			}(turnStateDocs)
		}
		updateSessionTaskState(sessionStore, sessionID, turnResult.ToolTraces, turnResult.HistoryDelta, false)
		commitMemoryRecallArtifacts(sessionStore, sessionID, eventID, memoryRecallSample, surfacedFileMemory)
		wsEmitter.Emit(gatewayws.EventAgentStatus, gatewayws.AgentStatusPayload{
			TS:      time.Now().UnixMilli(),
			AgentID: activeAgentID,
			Status:  "idle",
			Session: sessionID,
		})
		wsEmitter.Emit(gatewayws.EventChatChunk, gatewayws.ChatChunkPayload{
			TS:        time.Now().UnixMilli(),
			AgentID:   activeAgentID,
			SessionID: sessionID,
			Done:      true,
		})
		turnTelemetry := buildTurnTelemetry(eventID, turnStartedAt, time.Now(), turnResult, nil, false, "", "", "")
		persistTurnTelemetry(sessionStore, sessionID, turnTelemetry)
		emitTurnTelemetry(wsEmitter, activeAgentID, sessionID, turnTelemetry)

		if replyFn != nil {
			sendSuppressed := false
			var seForUsage *state.SessionEntry
			if sessionStore != nil {
				if se, ok := sessionStore.Get(sessionID); ok {
					sendSuppressed = se.SendSuppressed
					copy := se
					seForUsage = &copy
				}
			}
			outboundText := renderResponseWithUsage(turnResult.Text, turnResult.Usage, seForUsage)
			// Strip timestamp annotations that LLM may have copied from conversation history
			outboundText = stripTimestampAnnotations(outboundText)
			if !sendSuppressed {
				var sendOK bool
				outboundText, sendOK = applyPluginMessageSending(ctx, pluginhooks.MessageSendingEvent{ChannelID: "nostr", SenderID: activeAgentID, Recipient: senderID, Text: outboundText, SessionID: sessionID, AgentID: activeAgentID})
				if !sendOK {
					log.Printf("dm reply rejected by hook event=%s reason=%s", eventID, outboundText)
					return
				}
				if err := replyFn(ctx, outboundText); err != nil {
					emitPluginMessageSent(ctx, pluginhooks.MessageSentEvent{ChannelID: "nostr", SenderID: activeAgentID, Recipient: senderID, Text: outboundText, SessionID: sessionID, AgentID: activeAgentID, EventID: eventID, Success: false, Error: err.Error()})
					log.Printf("reply failed event=%s err=%v", eventID, err)
					logBuffer.Append("error", fmt.Sprintf("dm reply failed event=%s err=%v", eventID, err))
					return
				}
				emitPluginMessageSent(ctx, pluginhooks.MessageSentEvent{ChannelID: "nostr", SenderID: activeAgentID, Recipient: senderID, Text: outboundText, SessionID: sessionID, AgentID: activeAgentID, EventID: eventID, Success: true})
			} else {
				log.Printf("reply suppressed (send off) session=%s event=%s", sessionID, eventID)
			}
			turnResult.Text = outboundText
		}

		wsEmitter.Emit(gatewayws.EventChatMessage, gatewayws.ChatMessagePayload{
			TS:        time.Now().UnixMilli(),
			AgentID:   activeAgentID,
			SessionID: sessionID,
			Direction: "outbound",
			EventID:   eventID,
		})
		usageState.RecordOutbound(turnResult.Text)
		metricspkg.MessagesOutbound.Inc()
		logBuffer.Append("info", fmt.Sprintf("dm reply sent to=%s event=%s", senderID, eventID))
		if err := persistAssistant(ctx, docsRepo, transcriptRepo, sessionID, turnResult.Text, eventID); err != nil {
			log.Printf("persist assistant failed session=%s err=%v", sessionID, err)
		}
		// Also extract assistant reply into memory so both sides of the
		// conversation are searchable — not just user messages.
		assistantMemoryDocs := scopedMemoryDocs(memory.ExtractFromTurn(sessionID, "assistant", eventID, turnResult.Text, time.Now().Unix()), scopeCtx)
		if len(assistantMemoryDocs) > 0 {
			go func(docs []state.MemoryDoc) {
				persistCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				persistMemories(persistCtx, docsRepo, memoryRepo, memoryIndex, memoryTracker, docs)
			}(assistantMemoryDocs)
		}
		if sessionStore != nil && (turnResult.Usage.InputTokens > 0 || turnResult.Usage.OutputTokens > 0) {
			_ = sessionStore.AddTokens(sessionID, turnResult.Usage.InputTokens, turnResult.Usage.OutputTokens, turnResult.Usage.CacheReadTokens, turnResult.Usage.CacheCreationTokens)
		}
		// Note: assistant text is already ingested via persistAndIngestTurnHistory
		// above (as part of HistoryDelta), so we don't duplicate it here.
		if eventID != "" && createdAt > 0 && !strings.HasPrefix(eventID, "watch:") {
			if err := tracker.MarkProcessed(ctx, docsRepo, eventID, createdAt); err != nil {
				log.Printf("checkpoint update failed event=%s err=%v", eventID, err)
			}
		}
		for _, steered := range inlineSteering {
			if strings.ToLower(strings.TrimSpace(steered.Source)) != "dm" || strings.TrimSpace(steered.EventID) == "" {
				continue
			}
			steeredCreatedAt := steered.CreatedAt
			if steeredCreatedAt <= 0 {
				steeredCreatedAt = time.Now().Unix()
			}
			if err := tracker.MarkProcessed(ctx, docsRepo, steered.EventID, steeredCreatedAt); err != nil {
				log.Printf("checkpoint update failed inline steering event=%s err=%v", steered.EventID, err)
			}
		}
		steeringDrainCommitted = true
		drainedSteering.Clear()
	}

	dmRunAgentTurn := func(
		ctx context.Context,
		fromPubKey, combinedText, eventID string,
		createdAt int64,
		replyFn func(context.Context, string) error,
	) {
		runInboundTurn(ctx, fromPubKey, fromPubKey, combinedText, eventID, createdAt, replyFn, "", turnToolConstraints{}, 0)
	}

	// Wire dmRunAgentTurn into the watch delivery closure.
	dmRunAgentTurnRef = func(ctx context.Context, sessionID, senderID, text, eventID string, createdAt int64, replyFn func(context.Context, string) error, overrideAgentID string, constraints turnToolConstraints) {
		runInboundTurn(ctx, sessionID, senderID, text, eventID, createdAt, replyFn, overrideAgentID, constraints, 0)
	}

	// Restore persisted watch subscriptions from the state store.
	if raw, loadErr := docsRepo.GetWatches(ctx); loadErr != nil {
		log.Printf("watches load warning: %v", loadErr)
	} else {
		specs, bootstrapped, specErr := loadOrDefaultWatchSpecs(raw, pubKeyHex, pubKeyHex, time.Now())
		if specErr != nil {
			log.Printf("watches load unmarshal warning: %v", specErr)
		} else if n := watchRegistry.Restore(watchDeliveryCtx, nostrToolOpts, specs, watchDeliver); n > 0 {
			if bootstrapped {
				log.Printf("watches bootstrapped with defaults: %d subscriptions", n)
				if rawSpecs, err := json.Marshal(specs); err != nil {
					log.Printf("watches bootstrap marshal warning: %v", err)
				} else if _, err := docsRepo.PutWatches(ctx, rawSpecs); err != nil {
					log.Printf("watches bootstrap persist warning: %v", err)
				}
			} else {
				log.Printf("watches restored from state store: %d subscriptions", n)
			}
		}
	}

	// dmDebouncer coalesces rapid DM messages per sender.
	// Only created when dmDebounceWindow > 0.
	var dmDebouncer *channels.Debouncer
	type dmEventMeta struct {
		ID        string
		CreatedAt int64
	}
	var dmEventIDsMu sync.Mutex
	dmEventIDs := make(map[string]dmEventMeta) // pubkey → latest event metadata

	if dmDebounceWindow > 0 {
		dmDebouncer = channels.NewDebouncer(dmDebounceWindow, func(pubkey string, msgs []string) {
			combined := strings.Join(msgs, "\n")

			dmEventIDsMu.Lock()
			ev := dmEventIDs[pubkey]
			delete(dmEventIDs, pubkey)
			dmEventIDsMu.Unlock()

			dmReplyFnsMu.Lock()
			replyFn := dmReplyFns[pubkey]
			dmReplyFnsMu.Unlock()

			if replyFn == nil {
				return
			}
			dmRunAgentTurn(ctx, pubkey, combined, ev.ID, ev.CreatedAt, replyFn)
		})
		defer dmDebouncer.FlushAll()
	}

	// Shared inbound DM handler used by both NIP-04 and NIP-17 buses.
	dmOnMessage := func(ctx context.Context, msg nostruntime.InboundDM) error {
		msg = wrapInboundDMReply(func() state.ConfigDoc {
			if configState != nil {
				return configState.Get()
			}
			return state.ConfigDoc{}
		}, msg)
		log.Printf("dm received scheme=%s from=%s event=%s created_at=%d",
			msg.Scheme, msg.FromPubKey, msg.EventID, msg.CreatedAt)
		if tracker.AlreadyProcessed(msg.EventID, msg.CreatedAt) {
			return nil
		}
		usageState.RecordInbound(msg.Text)
		metricspkg.MessagesInbound.Inc()
		logBuffer.Append("info", fmt.Sprintf("dm inbound from=%s event=%s", msg.FromPubKey, msg.EventID))

		// Emit inbound chat event.
		wsEmitter.Emit(gatewayws.EventChatMessage, gatewayws.ChatMessagePayload{
			TS:        time.Now().UnixMilli(),
			SessionID: msg.FromPubKey,
			Direction: "inbound",
			EventID:   msg.EventID,
		})

		decision := policy.EvaluateIncomingDM(msg.FromPubKey, configState.Get())
		if !decision.Allowed {
			// Check NIP-51 dynamic allowlist before rejecting.
			if isInDynamicAllowlist(msg.FromPubKey) {
				decision = policy.DMDecision{Allowed: true, Level: policy.AuthPublic}
				log.Printf("dm allowed via nip51 list from=%s", msg.FromPubKey)
			} else {
				log.Printf("dm rejected from=%s reason=%s", msg.FromPubKey, decision.Reason)
				if decision.RequiresPairing {
					_ = msg.Reply(ctx, "Your message was received, but this node requires pairing approval before processing DMs.")
				}
				return nil
			}
		}

		// Rate limit check: per-user and per-channel (using pubkey as channel for DMs).
		if !dmRateLimiter.Allow(msg.FromPubKey, "dm:"+msg.FromPubKey) {
			log.Printf("dm rate-limited from=%s", msg.FromPubKey)
			return nil // silently drop rate-limited messages
		}

		// Session ID for DM conversations is the sender's pubkey (peer-to-peer session).
		sessionID := msg.FromPubKey
		if sessionStore != nil {
			if entry, ok := sessionStore.Get(sessionID); ok {
				pol := resolveSessionFreshnessPolicy(configState.Get(), "direct", "")
				if shouldAutoRotateSession(entry, time.Now(), pol) {
					_ = rotateSession(ctx, sessionID, "stale:dm")
					log.Printf("auto session reset (dm) session=%s idle_minutes=%d daily=%v", sessionID, pol.IdleMinutes, pol.DailyReset)
				}
			}
		}

		// ── ACP fast-path ────────────────────────────────────────────────
		// Route ACP messages through the ACP handler when either:
		//   1) the sender is a registered ACP peer, or
		//   2) the message is a reply for a currently pending ACP dispatch.
		// This lets pong/result replies complete even when the sender was not
		// manually pre-registered as a peer.
		if acppkg.IsACPMessage([]byte(msg.Text)) {
			if acpMsg, acpErr := acppkg.Parse([]byte(msg.Text)); acpErr == nil {
				registeredPeer := controlACPPeers != nil && controlACPPeers.IsPeer(msg.FromPubKey)
				pendingReply := false
				if controlServices.relay.acpDispatcher != nil {
					switch acpMsg.ACPType {
					case "result", "pong":
						pendingReply = controlServices.relay.acpDispatcher.HasPending(acpMsg.TaskID)
					}
				}
				if registeredPeer || pendingReply {
					if err := handleACPMessage(ctx, acpMsg, msg.FromPubKey, msg, agentRegistry, sessionRouter, tools, docsRepo, transcriptRepo); err != nil {
						log.Printf("acp message handler error from=%s task_id=%s err=%v", msg.FromPubKey, acpMsg.TaskID, err)
						return err
					}
					if err := tracker.MarkProcessed(ctx, docsRepo, msg.EventID, msg.CreatedAt); err != nil {
						log.Printf("checkpoint update (acp) failed event=%s err=%v", msg.EventID, err)
					}
					return nil
				}
			}
		}
		// ─────────────────────────────────────────────────────────────────

		// ── Inter-agent RPC fast-path ─────────────────────────────────────
		// If a nostr_agent_rpc tool call is pending a reply from this sender,
		// deliver directly to the waiting goroutine and skip the normal
		// agent-turn pipeline.  The tool call already holds the conversation
		// context; we don't need a separate session turn.
		if controlRPCCorrelator.Deliver(msg.FromPubKey, msg.Text) {
			log.Printf("dm rpc-reply delivered from=%s event=%s", msg.FromPubKey, msg.EventID)
			if err := tracker.MarkProcessed(ctx, docsRepo, msg.EventID, msg.CreatedAt); err != nil {
				log.Printf("checkpoint update (rpc-reply) failed event=%s err=%v", msg.EventID, err)
			}
			return nil
		}
		// Store in async inbox for nostr_agent_inbox polling.
		controlRPCCorrelator.StoreInbox(msg.FromPubKey, msg.Text)
		// ─────────────────────────────────────────────────────────────────

		// ── Reset trigger detection ───────────────────────────────────────
		// Detect /new or /reset at the start of the message body (before
		// slash routing) so that "  /new hello" resets the session and
		// then passes "hello" as the first message of the fresh session.
		if trigger, remainder := parseResetTrigger(msg.Text); trigger != "" {
			_ = rotateSession(ctx, sessionID, "trigger:"+trigger)
			reply := "🔄 Session reset. Starting fresh."
			if replyErr := msg.Reply(ctx, reply); replyErr != nil {
				log.Printf("reset trigger reply failed event=%s err=%v", msg.EventID, replyErr)
			}
			if err := tracker.MarkProcessed(ctx, docsRepo, msg.EventID, msg.CreatedAt); err != nil {
				log.Printf("checkpoint update (reset-trigger) failed event=%s err=%v", msg.EventID, err)
			}
			if remainder == "" {
				return nil
			}
			// Re-inject the remainder as a new virtual message.
			msg.Text = remainder
		}
		// ─────────────────────────────────────────────────────────────────

		// ── Slash command fast-path ───────────────────────────────────────
		// Slash commands are handled before agent processing and do NOT
		// consume a turn slot (they return synchronously).
		if slashCmd := autoreply.Parse(msg.Text); slashCmd != nil {
			slashCmd.SessionID = sessionID
			slashCmd.FromPubKey = msg.FromPubKey

			// Auth level check: compare sender's level against command requirement.
			senderDecision := policy.EvaluateIncomingDM(msg.FromPubKey, configState.Get())
			requiredLevel := slashAuthLevels[slashCmd.Name] // zero value = AuthDenied treated as AuthPublic
			if requiredLevel == 0 {
				requiredLevel = policy.AuthPublic
			}
			if senderDecision.Level < requiredLevel {
				// Reply with permission denied, mark processed, and return.
				denyMsg := fmt.Sprintf("⛔ /%s requires %s access (you have %s).", slashCmd.Name, requiredLevel, senderDecision.Level)
				if replyErr := msg.Reply(ctx, denyMsg); replyErr != nil {
					log.Printf("auth deny reply failed event=%s err=%v", msg.EventID, replyErr)
				}
				if err := tracker.MarkProcessed(ctx, docsRepo, msg.EventID, msg.CreatedAt); err != nil {
					log.Printf("checkpoint update (auth-deny) failed event=%s err=%v", msg.EventID, err)
				}
				return nil
			}

			reply, handled, slashErr := slashRouter.Dispatch(ctx, slashCmd)
			if handled {
				if slashErr != nil {
					log.Printf("slash command error cmd=%s session=%s err=%v", slashCmd.Name, sessionID, slashErr)
					reply = fmt.Sprintf("⚠️  Error running /%s: %v", slashCmd.Name, slashErr)
				}
				if reply != "" {
					if replyErr := msg.Reply(ctx, reply); replyErr != nil {
						log.Printf("slash reply failed event=%s err=%v", msg.EventID, replyErr)
					}
				}
				if err := tracker.MarkProcessed(ctx, docsRepo, msg.EventID, msg.CreatedAt); err != nil {
					log.Printf("checkpoint update (slash) failed event=%s err=%v", msg.EventID, err)
				}
				return nil
			}
			// Unknown slash command — fall through to agent so it can answer.
		}
		// ─────────────────────────────────────────────────────────────────

		// ── DM debounce ───────────────────────────────────────────────────
		// If a debounce window is configured, coalesce rapid messages from
		// the same sender and defer agent dispatch until silence.
		if dmDebouncer != nil {
			dmReplyFnsMu.Lock()
			dmReplyFns[msg.FromPubKey] = msg.Reply
			dmReplyFnsMu.Unlock()

			dmEventIDsMu.Lock()
			if msg.EventID != "" {
				dmEventIDs[msg.FromPubKey] = dmEventMeta{ID: msg.EventID, CreatedAt: msg.CreatedAt}
			}
			dmEventIDsMu.Unlock()

			dmDebouncer.Submit(msg.FromPubKey, msg.Text)
			return nil
		}
		// ─────────────────────────────────────────────────────────────────

		// Direct (non-debounced) DM turn execution via shared helper.
		dmRunAgentTurn(ctx, msg.FromPubKey, msg.Text, msg.EventID, msg.CreatedAt, msg.Reply)
		log.Printf("dm accepted from=%s relay=%s event=%s text=%q", msg.FromPubKey, msg.RelayURL, msg.EventID, msg.Text)
		return nil
	}

	dmOnError := func(err error) {
		log.Printf("nostr runtime error: %v", err)
	}

	// Start DM transport: NIP-17 (gift-wrapped) + NIP-04 (legacy) in parallel.
	// Both buses share the same dmOnMessage handler so any client protocol works.
	if controlRelaySelector == nil {
		controlRelaySelector = nostruntime.NewRelaySelector(cfg.Relays, cfg.Relays)
		toolbuiltin.SetRelaySelector(controlRelaySelector)
	}
	if controlHub == nil && controlKeyer != nil {
		hub, hubErr := nostruntime.NewHub(ctx, controlKeyer, controlRelaySelector)
		if hubErr != nil {
			log.Printf("nostr hub: failed to create before DM/control startup: %v", hubErr)
		} else {
			controlHub = hub
		}
	}

	nip17bus, nip17err := nostruntime.StartNIP17Bus(ctx, nostruntime.NIP17BusOptions{
		Keyer:     controlKeyer,
		Hub:       controlHub,
		Relays:    cfg.Relays,
		SinceUnix: checkpointSinceUnix(checkpoint.LastUnix),
		OnMessage: dmOnMessage,
		OnError:   dmOnError,
	})
	if nip17err != nil {
		log.Printf("dm transport: NIP-17 unavailable (%v); NIP-04 only", nip17err)
	} else {
		controlNIP17Bus = nip17bus
		log.Printf("dm transport: NIP-17 (gift-wrapped) active")
		defer nip17bus.Close()
	}
	nip04bus, nip04err := nostruntime.StartDMBus(ctx, nostruntime.DMBusOptions{
		Keyer:     controlKeyer,
		Hub:       controlHub,
		Relays:    cfg.Relays,
		SinceUnix: checkpointSinceUnix(checkpoint.LastUnix),
		OnMessage: dmOnMessage,
		OnError:   dmOnError,
	})
	if nip04err != nil {
		log.Printf("dm transport: NIP-04 unavailable (%v)", nip04err)
	} else {
		controlNIP04Bus = nip04bus
		log.Printf("dm transport: NIP-04 (legacy) active")
		defer nip04bus.Close()
	}
	// Expose whichever bus is available for outbound sends (prefer NIP-17).
	var bus nostruntime.DMTransport
	if nip17err == nil {
		bus = nip17bus
	} else if nip04err == nil {
		bus = nip04bus
	} else {
		log.Fatalf("dm transport: no transport available (NIP-17: %v, NIP-04: %v)", nip17err, nip04err)
	}

	// Expose the DM bus globally so node-pairing and node.invoke handlers
	// can send NIP-17/NIP-04 DMs without the bus being threaded into every function.
	controlDMBusMu.Lock()
	controlDMBus = bus
	controlDMBusMu.Unlock()

	dmHealthReporters := make([]dmHealthReporter, 0, 2)
	if controlNIP17Bus != nil {
		dmHealthReporters = append(dmHealthReporters, controlNIP17Bus)
	}
	if controlNIP04Bus != nil {
		dmHealthReporters = append(dmHealthReporters, controlNIP04Bus)
	}
	if len(dmHealthReporters) > 0 {
		dmHealthObserver := newDMHealthObserverFunc(emitControlWSEvent)
		dmHealthObserver.EmitStartup(dmHealthReporters...)
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					dmHealthObserver.EmitTick(dmHealthReporters...)
				}
			}
		}()
	}

	// ── Initialize daemonServices ──────────────────────────────────────────────
	// Consolidates commonly-accessed globals into a dependency struct so that
	// extracted handler files can receive it instead of reading globals directly.
	controlServices = &daemonServices{
		relay: relayPolicyServices{
			nip17Bus:            controlNIP17Bus,
			nip04Bus:            controlNIP04Bus,
			dmBusMu:             &controlDMBusMu,
			dmBus:               &controlDMBus,
			controlBus:          controlRPCBus,
			relaySelector:       controlRelaySelector,
			keyer:               controlKeyer,
			watchRegistry:       watchRegistry,
			dvmHandler:          dvmHandler,
			healthMonitor:       &relayHealthMonitor,
			healthState:         map[string]bool{},
			transportSelector:   controlTransportSelector,
			acpPeers:            controlACPPeers,
			acpDispatcher:       controlACPDispatcher,
			hub:                 controlHub,
			channels:            channelReg,
			presenceHeartbeat38: controlPresenceHeartbeat38,
			rpcCorrelator:       controlRPCCorrelator,
		},
		emitter:   controlWsEmitter,
		emitterMu: &controlWsEmitterMu,
		session: sessionServices{
			sessionTurns:      controlSessionTurns,
			chatCancels:       chatCancels,
			steeringMailboxes: steeringMailboxes,
			agentRuntime:      controlAgentRuntime,
			agentRegistry:     controlAgentRegistry,
			sessionMemRuntime: controlSessionMemoryRuntime,
			sessionRouter:     controlSessionRouter,
			toolRegistry:      controlToolRegistry,
			memoryStore:       memoryIndex,
			contextEngine:     controlContextEngine,
			contextEngineName: contextEngineName,
			sessionStore:      controlSessionStore,
			agentJobs:         controlAgentJobs,
			subagents:         controlSubagents,
			ops:               controlOps,
			cronJobs:          controlCronJobs,
			execApprovals:     controlExecApprovals,
			wizards:           controlWizards,
			nodeInvocations:   controlNodeInvocations,
			nodePending:       nodePending,
		},
		handlers: handlerServices{
			ttsManager:         ttsMgr,
			updateChecker:      updateChecker,
			secretsStore:       secretsStore,
			pairingConfigMu:    &sync.Mutex{},
			hooksMgr:           hooksMgr,
			pluginMgr:          pluginMgr,
			pluginServiceMgr:   pluginServiceMgr,
			mcpOps:             controlMCPOps,
			mcpAuth:            mcpAuthController,
			canvasHost:         canvasHost,
			mediaTranscriber:   mediaTranscriber,
			keyRings:           keyRings,
			stateEnvelopeCodec: controlStateEnvelopeCodec,
			bootstrapPath:      bootstrapPath,
			configFilePath:     controlConfigFilePath,
			cronExecutorMu:     &controlCronExecutorMu,
		},
		runtimeConfig:  controlRuntimeConfig,
		docsRepo:       docsRepo,
		transcriptRepo: transcriptRepo,
		tasks: taskRuntimeServices{
			store:         taskStore,
			ledger:        taskLedger,
			service:       taskService,
			events:        taskEvents,
			workflowStore: workflowStore,
		},
		pubKeyHex:      pubKeyHex,
		restartCh:      restartCh,
		lifecycleCtx:   ctx,
		agentRunWG:     &agentRunWG,
		agentRunMu:     &agentRunMu,
		agentRunClosed: &agentRunClosed,
	}
	if runner := newTaskRunner(controlServices); runner != nil {
		controlServices.tasks.runner = runner
		taskLedger.AddObserver(runner)
	}
	if workflowExec := newWorkflowExecutor(controlServices); workflowExec != nil {
		workflowExec.gatewayCall = func(callCtx context.Context, method string, params json.RawMessage) (map[string]any, error) {
			var workflowDMBus nostruntime.DMTransport
			if controlServices.relay.dmBus != nil {
				workflowDMBus = *controlServices.relay.dmBus
			}
			result, err := handleControlRPCRequest(callCtx, nostruntime.ControlRPCInbound{
				Method:        method,
				Params:        params,
				FromPubKey:    pubKeyHex,
				Internal:      true,
				Authenticated: true,
			}, workflowDMBus, controlServices.relay.controlBus, controlServices.session.chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
			if err != nil {
				return nil, err
			}
			if out, ok := result.Result.(map[string]any); ok {
				return out, nil
			}
			raw, err := json.Marshal(result.Result)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, err
			}
			return out, nil
		}
		controlServices.tasks.workflowExecutor = workflowExec
		taskLedger.AddObserver(workflowExec)
		if orchestrator, err := taskspkg.NewWorkflowOrchestrator(taskspkg.OrchestratorConfig{
			Store:    workflowStore,
			Ledger:   taskLedger,
			Emitter:  taskEvents,
			Executor: workflowExec,
		}); err != nil {
			log.Printf("workflow orchestrator init failed: %v", err)
		} else {
			controlServices.tasks.workflowOrchestrator = orchestrator
			if err := orchestrator.RecoverNonTerminalRuns(context.Background()); err != nil {
				log.Printf("workflow recovery failed: %v", err)
			}
		}
	}

	// ── NIP-51 allowlist watcher + agent list sync ─────────────────────────────
	// Create a dedicated pool for NIP-51 list fetch/subscribe operations so the
	// DM buses are not disturbed.
	{
		nip51Pool := nostr.NewPool(nostruntime.PoolOptsNIP42(controlServices.relay.keyer))
		liveCfg := configState.Get()

		// When the runtime config has no explicit relays, fall back to bootstrap relays.
		if len(liveCfg.Relays.Read) == 0 {
			liveCfg.Relays.Read = cfg.Relays
		}
		if len(liveCfg.Relays.Write) == 0 {
			liveCfg.Relays.Write = cfg.Relays
		}
		controlServices.startRelayHealthMonitor(ctx, nostruntime.MergeRelayLists(liveCfg.Relays.Read, liveCfg.Relays.Write))

		// ── NIP-65 Relay Selector (outbox model) ────────────────────────────
		// Initialize once; keep a single shared selector/hub instance so existing
		// channels and tools continue to share the same pooled connections.
		if controlServices.relay.relaySelector == nil {
			controlRelaySelector = nostruntime.NewRelaySelector(liveCfg.Relays.Read, liveCfg.Relays.Write)
			controlServices.relay.relaySelector = controlRelaySelector
			toolbuiltin.SetRelaySelector(controlServices.relay.relaySelector)
		}

		// ── Shared NostrHub ──────────────────────────────────────────────────
		if controlServices.relay.hub == nil && controlServices.relay.keyer != nil {
			hub, hubErr := nostruntime.NewHub(ctx, controlServices.relay.keyer, controlServices.relay.relaySelector)
			if hubErr != nil {
				log.Printf("nostr hub: failed to create: %v (channels/tools will use dedicated pools)", hubErr)
			} else {
				controlHub = hub
				controlServices.relay.hub = hub
			}
		}

		if controlServices.tasks.events != nil && controlServices.relay.keyer != nil {
			lifecyclePublisher, lifecycleErr := taskspkg.NewLifecyclePublisher(ctx, taskspkg.LifecyclePublisherOptions{
				Keyer: controlServices.relay.keyer,
				Pool:  nip51Pool,
				RelayProvider: func() []string {
					relays := currentCapabilityPublishRelays(configState.Get())
					if len(relays) == 0 {
						relays = append([]string{}, cfg.Relays...)
					}
					return relays
				},
			})
			if lifecycleErr != nil {
				log.Printf("task lifecycle publisher init failed: %v", lifecycleErr)
			} else {
				controlServices.tasks.lifecyclePublisher = lifecyclePublisher
				lifecyclePublisher.Subscribe(controlServices.tasks.events)
				defer lifecyclePublisher.Stop()
				log.Printf("task lifecycle publisher active (kind=30316)")
			}
		}

		// Publish startup lists (NIP-65 relay list, NIP-02 contacts) if they
		// don't already exist. Run in background to not block startup.
		go func() {
			// Build NIP-02 contacts from allow_from list + fleet entries.
			var contacts []nostruntime.NIP02Contact
			for _, pk := range liveCfg.DM.AllowFrom {
				contacts = append(contacts, nostruntime.NIP02Contact{PubKey: pk})
			}
			// Also include agent_list peers if configured.
			if liveCfg.AgentList != nil && liveCfg.AgentList.DTag != "" {
				for _, fe := range fleetDirectory() {
					contacts = append(contacts, nostruntime.NIP02Contact{
						PubKey:  fe.Pubkey,
						Relay:   fe.Relay,
						Petname: fe.Name,
					})
				}
			}

			allRelays := nostruntime.MergeRelayLists(liveCfg.Relays.Read, liveCfg.Relays.Write)

			if err := nostruntime.PublishStartupLists(ctx, nostruntime.StartupListPublishOptions{
				Keyer:         controlServices.relay.keyer,
				Pool:          nip51Pool,
				PublishRelays: allRelays,
				ReadRelays:    liveCfg.Relays.Read,
				WriteRelays:   liveCfg.Relays.Write,
				Contacts:      contacts,
			}); err != nil {
				log.Printf("nip65: startup list publish: %v", err)
			}
		}()

		// Subscribe to our own NIP-65 relay list for bidirectional sync.
		// When an external client publishes a new kind:10002 for our pubkey,
		// apply the relay changes to the live runtime.
		if err := nostruntime.NIP65SelfSync(ctx, nostruntime.NIP65SyncOptions{
			Keyer:  controlServices.relay.keyer,
			Pool:   nip51Pool,
			Relays: nostruntime.MergeRelayLists(liveCfg.Relays.Read, liveCfg.Relays.Write),
			OnRelayUpdate: func(read, write []string) {
				log.Printf("nip65: applying remote relay list update (read=%d, write=%d)", len(read), len(write))

				// Durably update the runtime config before applying relay side effects.
				if configState != nil {
					commit, err := commitRuntimeConfigMutation(ctx, docsRepo, configState, configMutationCommitRequest{
						SkipIfUnchanged: true,
						BuildNext: func(current state.ConfigDoc) (state.ConfigDoc, error) {
							current.Relays.Read = read
							current.Relays.Write = write
							return current, nil
						},
					})
					if err != nil {
						log.Printf("nip65: durable config update failed: %v", err)
						return
					}
					read = append([]string{}, commit.Next.Relays.Read...)
					write = append([]string{}, commit.Next.Relays.Write...)
				}

				// Update relay runtime side effects only after durable config convergence.
				controlServices.relay.relaySelector.SetFallbacks(read, write)
				allRelays := nostruntime.MergeRelayLists(read, write)
				controlServices.applyDMRelayPolicy(allRelays)
				controlServices.applyControlRelayPolicy(allRelays)
				watchRegistry.RebindRelays(allRelays)
			},
		}); err != nil {
			log.Printf("nip65: self-sync init: %v", err)
		}

		// ── NIP-51 kind:30002 Relay Sets ───────────────────────────────────
		// Create the relay set registry and seed it with relays from channel
		// configs.  Then subscribe to our own kind:30002 events so changes
		// made by external clients are applied at runtime.
		{
			relaySetRegistry = nostruntime.NewRelaySetRegistry()

			// Seed relay sets from nostr_channels config.
			nip29Relays := map[string]struct{}{}
			nip28Relays := map[string]struct{}{}
			chatRelays := map[string]struct{}{}
			for _, chanCfg := range liveCfg.NostrChannels {
				switch state.NostrChannelKind(chanCfg.Kind) {
				case state.NostrChannelKindNIP29:
					for _, r := range chanCfg.Relays {
						nip29Relays[r] = struct{}{}
					}
				case state.NostrChannelKindNIP28:
					for _, r := range chanCfg.Relays {
						nip28Relays[r] = struct{}{}
					}
				case state.NostrChannelKindChat:
					for _, r := range chanCfg.Relays {
						chatRelays[r] = struct{}{}
					}
				}
			}
			seedSet := func(dtag string, m map[string]struct{}) {
				if len(m) > 0 {
					rs := make([]string, 0, len(m))
					for r := range m {
						rs = append(rs, r)
					}
					relaySetRegistry.Set(dtag, rs)
				}
			}
			seedSet(nip51.RelaySetNIP29, nip29Relays)
			seedSet(nip51.RelaySetNIP28, nip28Relays)
			seedSet(nip51.RelaySetChat, chatRelays)

			// Seed DVM relays from config if DVM is configured.
			if dvmHandler != nil {
				relaySetRegistry.Set(nip51.RelaySetDVM, cfg.Relays)
			}

			// Seed search relays from extra config if present.
			if searchExtra, ok := liveCfg.Extra["search"].(map[string]any); ok {
				if rawRelays, ok := searchExtra["relays"].([]any); ok {
					var sr []string
					for _, r := range rawRelays {
						if s, ok := r.(string); ok {
							sr = append(sr, s)
						}
					}
					if len(sr) > 0 {
						relaySetRegistry.Set(nip51.RelaySetSearch, sr)
					}
				}
			}

			// Seed grasp servers from extra config if present.
			if graspExtra, ok := liveCfg.Extra["grasp"].(map[string]any); ok {
				if rawServers, ok := graspExtra["servers"].([]any); ok {
					var gs []string
					for _, s := range rawServers {
						if str, ok := s.(string); ok {
							gs = append(gs, str)
						}
					}
					if len(gs) > 0 {
						relaySetRegistry.Set(nip51.RelaySetGrasp, gs)
					}
				}
			}

			// Register change callback to rebind affected subscriptions.
			relaySetRegistry.OnChange(func(dtag string, relays []string) {
				log.Printf("relay-set: %q updated → %v", dtag, relays)
				switch dtag {
				case nip51.RelaySetDVM:
					if dvmHandler != nil {
						dvmHandler.SetRelays(relays)
					}
				}
				// NIP-29/NIP-28/chat channel rebinding is handled by the
				// channel registry — channels read relay sets at join time.
			})

			// Subscribe to our own kind:30002 relay set events.
			allRelays := nostruntime.MergeRelayLists(liveCfg.Relays.Read, liveCfg.Relays.Write)
			if syncErr := nostruntime.RelaySetSelfSync(ctx, nostruntime.RelaySetSyncOptions{
				Keyer:    controlServices.relay.keyer,
				Pool:     nip51Pool,
				Relays:   allRelays,
				Registry: relaySetRegistry,
				WatchDTags: []string{
					nip51.RelaySetDMInbox,
					nip51.RelaySetNIP29,
					nip51.RelaySetChat,
					nip51.RelaySetNIP28,
					nip51.RelaySetSearch,
					nip51.RelaySetDVM,
					nip51.RelaySetGrasp,
				},
			}); syncErr != nil {
				log.Printf("relay-set-sync: init failed: %v", syncErr)
			}

			// Publish current relay sets in the background.
			go func() {
				sets := relaySetRegistry.All()
				for dtag, entry := range sets {
					if len(entry.Relays) == 0 {
						continue
					}
					if _, err := nostruntime.PublishRelaySet(ctx, nip51Pool, controlServices.relay.keyer, allRelays, dtag, entry.Relays); err != nil {
						log.Printf("relay-set: publish %q failed: %v", dtag, err)
					} else {
						log.Printf("relay-set: published %q (%d relays)", dtag, len(entry.Relays))
					}
				}
			}()
		}

		// Start watchers for each allow_from_lists entry.
		log.Printf("nip51: starting watcher for %d allow_from_lists entries", len(liveCfg.DM.AllowFromLists))
		startNIP51AllowlistWatcher(ctx, nip51Pool, liveCfg)
		startRepoBookmarkWatcher(ctx, nip51Pool, controlServices.relay.keyer, liveCfg)

		// Publish local kind:30317 capabilities and subscribe to fleet peers'
		// capability advertisements for dynamic discovery.
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.OnChange(func(pubkey string, cap nostruntime.CapabilityAnnouncement) {
			log.Printf("capability-sync: peer=%s runtime=%s version=%s tools=%d", pubkey, cap.Runtime, cap.RuntimeVersion, len(cap.Tools))
			writeFleetMD(getFleetWorkspaceDir())
		})
		capabilityMonitor = nostruntime.NewCapabilityMonitor(nostruntime.CapabilityMonitorOptions{
			Pool:            nip51Pool,
			Keyer:           controlServices.relay.keyer,
			Registry:        capabilityRegistry,
			PublishRelays:   currentCapabilityPublishRelays(liveCfg),
			SubscribeRelays: currentCapabilitySubscriptionRelays(liveCfg),
			Peers:           fleetPeerPubkeys(),
			Local:           buildLocalCapabilityAnnouncement(context.Background(), liveCfg, docsRepo),
			OnPublished:     func(eventID string) { log.Printf("capability-sync: published local capability event=%s", eventID) },
		})
		capabilityMonitor.Start(ctx)
		refreshCapabilityPeerWatch()
		capabilityMonitor.TriggerPublish()

		// Publish/update Strand's own kind:30000 agent list if auto_sync is enabled.
		// Run in background so startup is not blocked on relay I/O.
		if liveCfg.AgentList != nil && liveCfg.AgentList.AutoSync {
			go syncAgentList(ctx, nip51Pool, liveCfg)
		}
	}

	// ── NIP-38 presence/status ──────────────────────────────────────────────────
	// Publishes kind 30315 status events so other Nostr clients can see whether
	// the agent is idle, typing, or running tools.
	{
		hbEnabled := true // default on
		hbInterval := 5 * time.Minute
		var hbDefaultContent string
		hbExtra, _ := configState.Get().Extra["status"].(map[string]any)
		if hbExtra == nil {
			hbExtra, _ = configState.Get().Extra["heartbeat"].(map[string]any)
		}
		if hbExtra != nil {
			if v, ok := hbExtra["enabled"].(bool); ok {
				hbEnabled = v
			}
			if v, ok := hbExtra["interval_seconds"].(float64); ok && v > 0 {
				hbInterval = time.Duration(v) * time.Second
			}
			if v, ok := hbExtra["content"].(string); ok {
				hbDefaultContent = v
			}
		}
		hbKeyer := controlServices.relay.keyer
		if hbKeyer != nil && hbEnabled {
			hb, hbErr := nip38.NewHeartbeat(ctx, nip38.HeartbeatOptions{
				Keyer:          hbKeyer,
				Relays:         cfg.Relays,
				IdleInterval:   hbInterval,
				DefaultContent: hbDefaultContent,
				Enabled:        true,
			})
			if hbErr != nil {
				log.Printf("warn: NIP-38 presence/status init failed: %v", hbErr)
			} else {
				controlPresenceHeartbeat38 = hb
				if controlServices != nil {
					controlServices.relay.presenceHeartbeat38 = hb
				}
				defer hb.Stop()
				log.Printf("NIP-38 presence/status active (interval=%s)", hbInterval)
			}
		} else if !hbEnabled {
			log.Printf("NIP-38 presence/status disabled by config")
		} else {
			log.Printf("NIP-38 presence/status skipped: no signing key available")
		}
		_ = controlServices.relay.presenceHeartbeat38 // referenced in dmRunAgentTurn closure
	}

	// ── Profile Publisher (kind:0) ──────────────────────────────────────────────
	// Reads extra.profile from config and publishes kind:0 metadata at startup,
	// on config change, and periodically (default 6h) to keep relays in sync.
	{
		currentCfg := configState.Get()
		profileData := nostruntime.ExtractProfileFromExtra(currentCfg.Extra)
		profileKeyer := controlServices.relay.keyer
		allRelays := nostruntime.MergeRelayLists(currentCfg.Relays.Read, currentCfg.Relays.Write)
		if len(allRelays) == 0 {
			allRelays = cfg.Relays // fall back to bootstrap relays
		}
		if profileKeyer != nil && len(profileData) > 0 && len(allRelays) > 0 {
			pp, ppErr := nostruntime.NewProfilePublisher(nostruntime.ProfilePublisherOptions{
				Keyer:   profileKeyer,
				Relays:  allRelays,
				Profile: profileData,
				OnPublished: func(eventID string, relayCount int) {
					log.Printf("profile-publisher: kind:0 published event=%s relays=%d", eventID, relayCount)
				},
			})
			if ppErr != nil {
				log.Printf("warn: profile publisher init failed: %v", ppErr)
			} else {
				controlProfilePublisher = pp
				pp.Start(ctx)
				defer pp.Stop()
				log.Printf("profile-publisher: active (fields=%d, relays=%d)", len(profileData), len(allRelays))
			}
		} else if len(profileData) == 0 {
			log.Printf("profile-publisher: skipped (no extra.profile in config)")
		} else {
			log.Printf("profile-publisher: skipped (no signing key or relays)")
		}
	}

	// ── NIP-90 DVM handler ─────────────────────────────────────────────────────
	// Enabled when extra.dvm.enabled = true in config.
	if dvmExtra, ok := configState.Get().Extra["dvm"].(map[string]any); ok {
		if enabled, _ := dvmExtra["enabled"].(bool); enabled {
			// Collect accepted kinds from extra.dvm.kinds (e.g. [5000, 5001]).
			var acceptedKinds []int
			if rawKinds, ok := dvmExtra["kinds"].([]any); ok {
				for _, k := range rawKinds {
					if f, ok := k.(float64); ok {
						acceptedKinds = append(acceptedKinds, int(f))
					}
				}
			}
			var dvmErr error
			dvmHandler, dvmErr = dvm.Start(ctx, dvm.HandlerOpts{
				Keyer:         controlServices.relay.keyer,
				Relays:        cfg.Relays,
				AcceptedKinds: acceptedKinds,
				OnJob: func(jobCtx context.Context, jobID string, kind int, input string) (string, error) {
					filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(jobCtx, configState.Get(), docsRepo, "dvm:"+jobID, "", agentRuntime, tools, turnToolConstraints{})
					scopeCtx := resolveMemoryScopeContext(jobCtx, configState.Get(), docsRepo, sessionStore, "dvm:"+jobID, "", "")
					jobCtx = contextWithMemoryScope(jobCtx, scopeCtx)
					result, err := filteredRuntime.ProcessTurn(jobCtx, agent.Turn{
						SessionID:           "dvm:" + jobID,
						UserText:            input,
						Tools:               turnTools,
						Executor:            turnExecutor,
						ContextWindowTokens: maxContextTokensForAgent(configState.Get(), ""),
						HookInvoker:         controlHookInvoker,
					})
					if err != nil {
						return "", err
					}
					return result.Text, nil
				},
			})
			if dvmErr != nil {
				log.Printf("warn: DVM handler start failed: %v", dvmErr)
			} else {
				defer dvmHandler.Stop()
				log.Printf("NIP-90 DVM handler active (kinds=%v)", acceptedKinds)
			}
		}
	}

	// ── Start built-in channel extensions ──────────────────────────────────────
	// ConnectExtensions scans nostr_channels for entries whose "kind" matches
	// a registered ChannelPlugin (telegram, discord, etc.).
	//
	// All inbound channel messages are run through a per-(channel,sender)
	// debouncer (500 ms window) before reaching the agent pipeline.  This
	// prevents duplicate or fragmented agent responses when a user types
	// several messages in quick succession (matching OpenClaw behaviour).

	// channelDebounceWindow is read from Extra["channels"]["debounce_ms"] if set.
	channelDebounceWindow := 500 * time.Millisecond
	if cExtra, ok := configState.Get().Extra["channels"].(map[string]any); ok {
		if ms, ok := cExtra["debounce_ms"].(float64); ok && ms > 0 {
			channelDebounceWindow = time.Duration(ms) * time.Millisecond
		}
	}

	// channelHandles maps channelID → Channel handle, populated after
	// ConnectExtensions returns.  The debounce callback (which fires ≥500ms later)
	// can safely read from it by then.
	var channelHandlesMu sync.RWMutex
	channelHandles := map[string]channels.Channel{}
	// channelRawHandles maps channelID → raw sdk.ChannelHandle so the debounce
	// flush can do interface assertions for optional features (e.g. TypingHandle).
	channelRawHandles := map[string]sdk.ChannelHandle{}

	// channelEventIDs tracks the most-recent inbound EventID per debounce key
	// so ack reactions can target the right message after debouncing.
	var channelEventIDsMu sync.Mutex
	channelEventIDs := map[string]string{}

	// channelQueues holds per-session pending-turn queues for when a session
	// is busy. Items are drained immediately after each turn completes.
	channelQueues := autoreply.NewSessionQueueRegistry(20, autoreply.QueueDropSummarize)

	// doChannelTurn runs one agent turn for a channel session and delivers the
	// reply. It is called both from the initial debounce flush and from the
	// post-turn queue drain loop. turnCtx must already be set up by the caller.
	doChannelTurn := func(
		turnCtx context.Context,
		chID, senderID, sessionID, text, eventID string,
		handle channels.Channel,
		rawHandle sdk.ChannelHandle,
		queueSettings queueRuntimeSettings,
	) (turnErr error) {
		// ── Session start hook (fires once per session) ───────────────────
		if _, seen := seenChannelSessions.LoadOrStore(sessionID, struct{}{}); !seen {
			fireHookEvent(controlServices.handlers.hooksMgr, "session:start", sessionID, map[string]any{
				"channel_id": chID,
				"sender_id":  senderID,
			})
		}

		// ── Status reaction controller ────────────────────────────────────
		var statusCtrl *channels.StatusReactionController
		if rh, ok := rawHandle.(sdk.ReactionHandle); ok && eventID != "" {
			statusCtrl = channels.NewStatusReactionController(turnCtx, rh, eventID)
			statusCtrl.SetQueued()
		}
		closeStatus := func(success bool) {
			if statusCtrl == nil {
				return
			}
			if success {
				statusCtrl.SetDone()
			} else {
				statusCtrl.SetError()
			}
			statusCtrl.Close()
		}

		// ── Typing keepalive ──────────────────────────────────────────────
		var typingKA *channels.TypingKeepalive
		if typingH, ok := rawHandle.(sdk.TypingHandle); ok {
			typingKA = channels.NewTypingKeepalive(typingH.SendTyping, 3*time.Second, 60*time.Second, 2)
			typingKA.Start(turnCtx)
		}
		stopTyping := func() {
			if typingKA != nil {
				typingKA.Stop()
			}
		}

		// ── Agent routing ─────────────────────────────────────────────────
		activeAgentID := sessionRouter.Get(sessionID)
		if activeAgentID == "" {
			activeAgentID = "main"
		}
		activeRuntime := agentRegistry.Get(activeAgentID)
		if activeRuntime == nil {
			stopTyping()
			log.Printf("channel dispatch: no runtime for agent=%s session=%s", activeAgentID, sessionID)
			return fmt.Errorf("no runtime for agent %s", activeAgentID)
		}
		activeRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, activeRuntime, tools, turnToolConstraints{})

		// Attach configurable timeouts to channel turn context.
		turnCtx = toolbuiltin.WithTimeoutsConfig(turnCtx, configState.Get().Timeouts)

		scopeCtx := resolveMemoryScopeContext(turnCtx, configState.Get(), docsRepo, sessionStore, sessionID, activeAgentID, "")
		turnCtx = contextWithMemoryScope(turnCtx, scopeCtx)
		turnContext, surfacedFileMemory, memoryRecallSample := buildDynamicMemoryRecallContext(turnCtx, memoryIndex, scopeCtx, sessionID, text, workspaceDirForAgent(configState.Get(), activeAgentID), sessionStore, 0)
		// Inject structured task state for context rehydration.
		if taskCtx := buildTaskStateContextBlock(sessionStore, sessionID); taskCtx != "" {
			turnContext = joinPromptSections(turnContext, taskCtx)
		}
		turnContext = joinPromptSections(buildExternalChannelMetadataContext(configState.Get(), chID, senderID, sessionID), turnContext)
		staticSystemPrompt := assembleMemorySystemPrompt(memoryIndex, scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID))
		var chTurnHistory []agent.ConversationMessage
		if controlServices.session.contextEngine != nil {
			if assembled, asmErr := controlServices.session.contextEngine.Assemble(turnCtx, sessionID, 100_000); asmErr == nil {
				if assembled.SystemPromptAddition != "" {
					turnContext = joinPromptSections(turnContext, assembled.SystemPromptAddition)
				}
				msgs := assembled.Messages
				if n := len(msgs); n > 0 {
					if last := msgs[n-1]; last.Role == "user" && strings.TrimSpace(last.Content) == strings.TrimSpace(text) {
						msgs = msgs[:n-1]
					}
				}
				for _, m := range msgs {
					chTurnHistory = append(chTurnHistory, conversationMessageFromContext(m))
				}
			}
		}

		wsEmitter.Emit(gatewayws.EventAgentStatus, gatewayws.AgentStatusPayload{
			TS:      time.Now().UnixMilli(),
			AgentID: activeAgentID,
			Status:  "thinking",
			Session: sessionID,
		})
		if statusCtrl != nil {
			statusCtrl.SetThinking()
		}

		// Inject channel handle so channel-action tools work during execution.
		if rawHandle != nil {
			turnCtx = toolbuiltin.WithChannelHandle(turnCtx, rawHandle)
		}

		// ── Run agent turn ──────────────────────────────────────────
		promptEnvelope := buildTurnPromptEnvelope(turnPromptBuilderParams{
			Config:             configState.Get(),
			SessionID:          sessionID,
			AgentID:            activeAgentID,
			Channel:            "nostr",
			SelfPubkey:         pubkey,
			SelfNPub:           toolbuiltin.NostrNPubFromHex(pubkey),
			StaticSystemPrompt: staticSystemPrompt,
			Context:            turnContext,
			Tools:              turnTools,
		})
		var chMaxAgenticIterations int
		for _, ac := range configState.Get().Agents {
			if ac.ID == activeAgentID {
				chMaxAgenticIterations = ac.MaxAgenticIterations
				break
			}
		}
		// Derive last assistant time for time-based microcompact trigger.
		var chLastAssistantTime time.Time
		if sessionStore != nil {
			if sessEntry, ok := sessionStore.Get(sessionID); ok {
				chLastAssistantTime = sessEntry.UpdatedAt
			}
		}

		// Partition tools into inline and deferred sets.
		chInlineTools, chDeferredTools := partitionTurnTools(turnExecutor, promptEnvelope.ContextWindowTokens)
		if chDeferredTools != nil && chDeferredTools.Count() > 0 {
			log.Printf("channel turn tools: %d inline, %d deferred (session=%s)",
				len(chInlineTools), chDeferredTools.Count(), sessionID)
		}

		drainedSteering := &activeRunSteeringDrainTracker{}
		steeringDrainCommitted := false
		defer func() {
			if !steeringDrainCommitted && shouldRestoreDrainedSteering(turnErr) {
				restoreDrainedSteering(steeringMailboxes, queueSettings, sessionID, drainedSteering.Snapshot())
			}
		}()

		chBaseTurn := agent.Turn{
			SessionID:            sessionID,
			TurnID:               eventID,
			UserText:             text,
			StaticSystemPrompt:   promptEnvelope.StaticSystemPrompt,
			Context:              promptEnvelope.Context,
			History:              chTurnHistory,
			Tools:                chInlineTools,
			Executor:             turnExecutor,
			MaxAgenticIterations: chMaxAgenticIterations,
			ToolEventSink:        toolLifecycleSinkWithActiveTools(activeTools, toolLifecyclePersistenceSink(sessionStore, sessionID, toolLifecycleEmitter(wsEmitter, activeAgentID))),
			ContextWindowTokens:  promptEnvelope.ContextWindowTokens,
			HookInvoker:          controlHookInvoker,
			SteeringDrain:        makeActiveRunSteeringDrain(steeringMailboxes, sessionID, drainedSteering.Record),
			LastAssistantTime:    chLastAssistantTime,
			DeferredTools:        chDeferredTools,
		}
		var turnResult agent.TurnResult
		turnStartedAt := time.Now()
		if sr, ok := activeRuntime.(agent.StreamingRuntime); ok {
			turnResult, turnErr = sr.ProcessTurnStreaming(turnCtx, chBaseTurn, func(chunk string) {
				wsEmitter.Emit(gatewayws.EventChatChunk, gatewayws.ChatChunkPayload{
					TS:        time.Now().UnixMilli(),
					AgentID:   activeAgentID,
					SessionID: sessionID,
					Text:      chunk,
				})
			})
		} else {
			turnResult, turnErr = activeRuntime.ProcessTurn(turnCtx, chBaseTurn)
		}

		stopTyping()

		wsEmitter.Emit(gatewayws.EventAgentStatus, gatewayws.AgentStatusPayload{
			TS:      time.Now().UnixMilli(),
			AgentID: activeAgentID,
			Status:  "idle",
			Session: sessionID,
		})

		// ── Status reaction: done or error ───────────────────────────────
		success := turnErr == nil || errors.Is(turnErr, context.Canceled)
		closeStatus(success)

		if turnErr != nil {
			// Persist completed tool work from the failed channel turn.
			if partial, ok := agent.PartialTurnResult(turnErr); ok {
				if len(partial.ToolTraces) > 0 {
					if err := persistToolTraces(ctx, transcriptRepo, sessionID, eventID, partial.ToolTraces); err != nil {
						log.Printf("persist partial tool traces (channel) session=%s err=%v", sessionID, err)
					}
				}
				persistAndIngestTurnHistory(ctx, transcriptRepo, controlServices.session.contextEngine, sessionID, eventID, partial.HistoryDelta, turnResultMetadataPtr(turnResult, turnErr))
				sessionMemoryRuntime.ObserveTurn(configState.Get(), runtimeSessionMemoryGenerator{runtime: activeRuntime}, sessionID, activeAgentID, sessionMemoryWorkspaceDir(scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID)), resolveAgentContextWindow(configState.Get(), activeAgentID), partial.HistoryDelta)
				// Distill structured episodic memory from the partial channel turn.
				if turnStateDocs := scopedMemoryDocs(distillTurnState(sessionID, eventID, partial.ToolTraces, partial.HistoryDelta, true), scopeCtx); len(turnStateDocs) > 0 {
					go func(docs []state.MemoryDoc) {
						pCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						persistMemories(pCtx, docsRepo, memoryRepo, memoryIndex, memoryTracker, docs)
					}(turnStateDocs)
				}
				updateSessionTaskState(sessionStore, sessionID, partial.ToolTraces, partial.HistoryDelta, true)
			}
			if errors.Is(turnErr, context.Canceled) {
				log.Printf("channel agent aborted session=%s", sessionID)
			} else {
				log.Printf("channel agent error session=%s err=%v", sessionID, turnErr)
			}
			turnTelemetry := buildTurnTelemetry(eventID, turnStartedAt, time.Now(), turnResult, turnErr, false, "", "", "")
			persistTurnTelemetry(sessionStore, sessionID, turnTelemetry)
			emitTurnTelemetry(wsEmitter, activeAgentID, sessionID, turnTelemetry)
			return turnErr
		}

		wsEmitter.Emit(gatewayws.EventChatChunk, gatewayws.ChatChunkPayload{
			TS:        time.Now().UnixMilli(),
			AgentID:   activeAgentID,
			SessionID: sessionID,
			Done:      true,
		})

		inlineSteering := drainedSteering.Snapshot()
		persistAndIngestInlineChannelSteering(ctx, docsRepo, transcriptRepo, controlServices.session.contextEngine, sessionID, chID, threadIDFromSessionID(sessionID), senderID, inlineSteering)

		if err := persistToolTraces(ctx, transcriptRepo, sessionID, eventID, turnResult.ToolTraces); err != nil {
			log.Printf("persist tool traces (channel) failed session=%s err=%v", sessionID, err)
		}
		persistAndIngestTurnHistory(ctx, transcriptRepo, controlServices.session.contextEngine, sessionID, eventID, turnResult.HistoryDelta, turnResultMetadataPtr(turnResult, nil))
		sessionMemoryRuntime.ObserveTurn(configState.Get(), runtimeSessionMemoryGenerator{runtime: activeRuntime}, sessionID, activeAgentID, sessionMemoryWorkspaceDir(scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID)), resolveAgentContextWindow(configState.Get(), activeAgentID), turnResult.HistoryDelta)
		// Distill structured episodic memory from the completed channel turn.
		if turnStateDocs := scopedMemoryDocs(distillTurnState(sessionID, eventID, turnResult.ToolTraces, turnResult.HistoryDelta, false), scopeCtx); len(turnStateDocs) > 0 {
			go func(docs []state.MemoryDoc) {
				pCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				persistMemories(pCtx, docsRepo, memoryRepo, memoryIndex, memoryTracker, docs)
			}(turnStateDocs)
		}
		updateSessionTaskState(sessionStore, sessionID, turnResult.ToolTraces, turnResult.HistoryDelta, false)
		commitMemoryRecallArtifacts(sessionStore, sessionID, eventID, memoryRecallSample, surfacedFileMemory)

		// ── Deliver reply ─────────────────────────────────────────────────
		outboundText := turnResult.Text
		audioSent := false
		audioPath, isMedia := extractMediaOutputPath(turnResult.Text)
		if isMedia {
			if ah, ok := rawHandle.(sdk.AudioHandle); ok {
				audioData, readErr := os.ReadFile(filepath.FromSlash(audioPath))
				if readErr != nil {
					log.Printf("channel audio read error channel=%s path=%s err=%v", chID, audioPath, readErr)
				} else {
					format := strings.TrimPrefix(strings.ToLower(filepath.Ext(audioPath)), ".")
					if format == "" {
						format = "mp3"
					}
					if sendErr := ah.SendAudio(turnCtx, audioData, format); sendErr != nil {
						log.Printf("channel audio send error channel=%s session=%s err=%v", chID, sessionID, sendErr)
					} else {
						audioSent = true
						metricspkg.MessagesOutbound.Inc()
						wsEmitter.Emit(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
							TS:        time.Now().UnixMilli(),
							ChannelID: chID,
							Direction: "outbound",
							From:      activeAgentID,
							Text:      "[audio]",
						})
						logBuffer.Append("info", fmt.Sprintf("channel audio reply sent channel=%s session=%s", chID, sessionID))
					}
				}
			}
			if !audioSent {
				outboundText = fmt.Sprintf("[audio generated] %s", audioPath)
			}
		}
		if !audioSent {
			var seForUsage *state.SessionEntry
			if sessionStore != nil {
				if se, ok := sessionStore.Get(sessionID); ok {
					copy := se
					seForUsage = &copy
				}
			}
			outboundText = renderResponseWithUsage(outboundText, turnResult.Usage, seForUsage)
			// Strip timestamp annotations that LLM may have copied from conversation history
			outboundText = stripTimestampAnnotations(outboundText)
		}
		if !audioSent && handle != nil && outboundText != "" {
			var sendOK bool
			outboundText, sendOK = applyPluginMessageSending(turnCtx, pluginhooks.MessageSendingEvent{ChannelID: chID, SenderID: activeAgentID, Recipient: senderID, Text: outboundText, SessionID: sessionID, AgentID: activeAgentID})
			if !sendOK {
				log.Printf("channel reply rejected by hook channel=%s session=%s reason=%s", chID, sessionID, outboundText)
				return nil
			}
			if sendErr := handle.Send(turnCtx, outboundText); sendErr != nil {
				emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: chID, SenderID: activeAgentID, Recipient: senderID, Text: outboundText, SessionID: sessionID, AgentID: activeAgentID, Success: false, Error: sendErr.Error()})
				log.Printf("channel reply error channel=%s session=%s err=%v", chID, sessionID, sendErr)
			} else {
				emitPluginMessageSent(turnCtx, pluginhooks.MessageSentEvent{ChannelID: chID, SenderID: activeAgentID, Recipient: senderID, Text: outboundText, SessionID: sessionID, AgentID: activeAgentID, Success: true})
				metricspkg.MessagesOutbound.Inc()
				wsEmitter.Emit(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
					TS:        time.Now().UnixMilli(),
					ChannelID: chID,
					Direction: "outbound",
					From:      activeAgentID,
					Text:      outboundText,
				})
				logBuffer.Append("info", fmt.Sprintf("channel reply sent channel=%s session=%s", chID, sessionID))
			}
		}
		// Accumulate token usage into session state.
		if sessionStore != nil && (turnResult.Usage.InputTokens > 0 || turnResult.Usage.OutputTokens > 0) {
			_ = sessionStore.AddTokens(sessionID, turnResult.Usage.InputTokens, turnResult.Usage.OutputTokens, turnResult.Usage.CacheReadTokens, turnResult.Usage.CacheCreationTokens)
		}
		turnTelemetry := buildTurnTelemetry(eventID, turnStartedAt, time.Now(), turnResult, nil, false, "", "", "")
		persistTurnTelemetry(sessionStore, sessionID, turnTelemetry)
		emitTurnTelemetry(wsEmitter, activeAgentID, sessionID, turnTelemetry)
		steeringDrainCommitted = true
		drainedSteering.Clear()
		return nil
	}

	channelDebouncer := channels.NewDebouncer(channelDebounceWindow, func(key string, msgs []string) {
		// key format: "channelID:senderID" or "channelID:senderID:thread:threadID"
		// Parse out the three components.
		parts := strings.SplitN(key, ":", 2)
		chID, senderID, threadID := "", "", ""
		if len(parts) == 2 {
			chID = parts[0]
			rest := parts[1]
			// Check for thread suffix ":thread:<threadID>"
			if idx := strings.Index(rest, ":thread:"); idx >= 0 {
				senderID = rest[:idx]
				threadID = rest[idx+len(":thread:"):]
			} else {
				senderID = rest
			}
		}
		combined := channels.JoinMessages(msgs)

		// Retrieve (and clear) the latest EventID tracked for this key.
		channelEventIDsMu.Lock()
		eventID := channelEventIDs[key]
		delete(channelEventIDs, key)

		preview := combined
		if len(preview) > 120 {
			preview = preview[:117] + "..."
		}
		log.Printf("channel dispatch channel=%s sender=%s msgs=%d text=%q",
			chID, senderID, len(msgs), preview)

		channelHandlesMu.RLock()
		handle := channelHandles[chID]
		rawHandle := channelRawHandles[chID]
		channelHandlesMu.RUnlock()

		sessionID := channels.SessionIDForMessage(chID, senderID, threadID)
		if sessionStore != nil {
			if entry, ok := sessionStore.Get(sessionID); ok {
				sType := "group"
				if strings.TrimSpace(threadID) != "" {
					sType = "thread"
				}
				pol := resolveSessionFreshnessPolicy(configState.Get(), sType, chID)
				if shouldAutoRotateSession(entry, time.Now(), pol) {
					_ = rotateSession(ctx, sessionID, "stale:channel")
					log.Printf("auto session reset (channel) session=%s type=%s channel=%s idle_minutes=%d daily=%v", sessionID, sType, chID, pol.IdleMinutes, pol.DailyReset)
				}
			}
		}

		emitPluginMessageReceived(ctx, pluginhooks.MessageReceivedEvent{ChannelID: chID, SenderID: senderID, Text: combined, EventID: eventID, ThreadID: threadID, SessionID: sessionID, AgentID: sessionRouter.Get(sessionID)})

		// Slash command fast-path: route /commands before hitting the agent.
		if slashCmd := autoreply.Parse(combined); slashCmd != nil {
			slashCmd.SessionID = sessionID
			slashCmd.FromPubKey = senderID
			reply, handled, slashErr := slashRouter.Dispatch(ctx, slashCmd)
			if slashErr != nil {
				reply = fmt.Sprintf("error: %v", slashErr)
				handled = true
			}
			if handled && reply != "" && handle != nil {
				replyCtx := sdk.WithChannelReplyTarget(ctx, senderID)
				if sendErr := handle.Send(replyCtx, reply); sendErr != nil {
					log.Printf("channel slash reply error channel=%s err=%v", chID, sendErr)
				}
			}
			metricspkg.MessagesInbound.Inc()
			if handled {
				metricspkg.MessagesOutbound.Inc()
				return
			}
		}

		// ── Channel agent pipeline ────────────────────────────────────────────
		// sessionID is per-(channel, sender) so each user gets their own
		// conversation context.
		metricspkg.MessagesInbound.Inc()
		sessionQ := channelQueues.Get(sessionID)
		var channelSessionEntry *state.SessionEntry
		if sessionStore != nil {
			if se, ok := sessionStore.Get(sessionID); ok {
				tmp := se
				channelSessionEntry = &tmp
			}
		}
		queueSettings := resolveQueueRuntimeSettings(configState.Get(), channelSessionEntry, chID, 20)
		sessionQ.Configure(queueSettings.Cap, queueSettings.Drop)

		// Per-session turn serialisation. If busy, queue and return;
		// the turn loop below drains the queue after each turn.
		releaseTurnSlot, acquired := sessionTurns.TryAcquire(sessionID)
		if !acquired {
			switch queueSettings.Mode {
			case "steer":
				accepted := handleBusySteer(steeringMailboxes, sessionQ, queueSettings, activeRunSteeringInput{
					SessionID: sessionID,
					Text:      combined,
					EventID:   eventID,
					SenderID:  senderID,
					ChannelID: chID,
					ThreadID:  threadID,
					Source:    "channel",
					CreatedAt: time.Now().Unix(),
					Priority:  autoreply.SteeringPriorityNormal,
				})
				if accepted {
					if rh, ok := rawHandle.(sdk.ReactionHandle); ok && eventID != "" {
						if rErr := rh.AddReaction(ctx, eventID, "👀"); rErr != nil {
							log.Printf("channel steer ack reaction error channel=%s err=%v", chID, rErr)
						}
					}
				}
				return
			case "interrupt":
				deferred := handleBusyInterrupt(chatCancels, activeTools, steeringMailboxes, sessionQ, queueSettings, activeRunSteeringInput{
					SessionID: sessionID,
					Text:      combined,
					EventID:   eventID,
					SenderID:  senderID,
					ChannelID: chID,
					ThreadID:  threadID,
					Source:    "channel",
					CreatedAt: time.Now().Unix(),
				})
				if deferred {
					if rh, ok := rawHandle.(sdk.ReactionHandle); ok && eventID != "" {
						if rErr := rh.AddReaction(ctx, eventID, "👀"); rErr != nil {
							log.Printf("channel interrupt defer ack reaction error channel=%s err=%v", chID, rErr)
						}
					}
					return
				}
			}
			// Enqueue for processing after the current turn finishes.
			sessionQ.Enqueue(autoreply.PendingTurn{
				Text:     combined,
				EventID:  eventID,
				SenderID: senderID,
			})
			// Ack with 👀 to confirm receipt while busy.
			if rh, ok := rawHandle.(sdk.ReactionHandle); ok && eventID != "" {
				if rErr := rh.AddReaction(ctx, eventID, "👀"); rErr != nil {
					log.Printf("channel queue ack reaction error channel=%s err=%v", chID, rErr)
				}
			}
			log.Printf("channel session busy, queued: session=%s mode=%s queue_len=%d", sessionID, queueSettings.Mode, sessionQ.Len())
			return
		}

		// We hold the turn slot. Run the current turn, then drain the queue.
		turnCtx, releaseTurn := chatCancels.Begin(sessionID, ctx)
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in channel agent session=%s panic=%v", sessionID, r)
			}
			activeTools.ClearSession(sessionID)
			releaseTurn()
			releaseTurnSlot()
			if sessionQ.Len() == 0 {
				channelQueues.Delete(sessionID)
			}
		}()

		replyCtx := sdk.WithChannelReplyTarget(turnCtx, senderID)

		// Run the initial turn.
		_ = doChannelTurn(replyCtx, chID, senderID, sessionID, combined, eventID, handle, rawHandle, queueSettings)

		// First, run residual active-run steering that did not reach a model boundary.
		for {
			steeringPending := drainSteeringAsPending(steeringMailboxes, sessionID)
			if len(steeringPending) == 0 {
				break
			}
			interrupted := false
			for i, pt := range steeringPending {
				queuedCtx := sdk.WithChannelReplyTarget(turnCtx, pt.SenderID)
				if err := doChannelTurn(queuedCtx, chID, pt.SenderID, sessionID, pt.Text, pt.EventID, handle, rawHandle, queueSettings); err != nil {
					enqueuePendingTurns(sessionQ, steeringPending[i+1:])
					interrupted = true
					break
				}
			}
			if interrupted {
				return
			}
		}

		// Drain any messages that arrived while we were running.
		for {
			pending := sessionQ.Dequeue()
			if len(pending) == 0 {
				break
			}
			mode := queueSettings.Mode
			if sessionStore != nil {
				if se, ok := sessionStore.Get(sessionID); ok {
					mode = resolveQueueRuntimeSettings(configState.Get(), &se, chID, 20).Mode
				}
			}
			if queueModeCollect(mode) {
				// Collect all queued items into one combined prompt.
				var texts []string
				var latestEventID string
				for _, pt := range pending {
					texts = append(texts, pt.Text)
					if pt.EventID != "" {
						latestEventID = pt.EventID
					}
				}
				queuedText := channels.JoinMessages(texts)
				if len(pending) > 1 {
					queuedText = fmt.Sprintf("[%d queued messages while agent was busy]\n\n%s", len(pending), queuedText)
				}
				queuedCtx := sdk.WithChannelReplyTarget(turnCtx, senderID)
				_ = doChannelTurn(queuedCtx, chID, senderID, sessionID, queuedText, latestEventID, handle, rawHandle, queueSettings)
				continue
			}
			if queueModeSequential(mode) {
				for _, pt := range pending {
					queuedCtx := sdk.WithChannelReplyTarget(turnCtx, senderID)
					_ = doChannelTurn(queuedCtx, chID, senderID, sessionID, pt.Text, pt.EventID, handle, rawHandle, queueSettings)
				}
				continue
			}
			latest := pending[len(pending)-1]
			queuedCtx := sdk.WithChannelReplyTarget(turnCtx, senderID)
			_ = doChannelTurn(queuedCtx, chID, senderID, sessionID, latest.Text, latest.EventID, handle, rawHandle, queueSettings)
		}

	})

	// channelPlatforms maps channelID → plugin kind (e.g. "slack", "telegram")
	// for per-platform inbound text normalization.
	channelPlatforms := make(map[string]string)
	for chID, chanCfg := range configState.Get().NostrChannels {
		if chanCfg.Kind != "" {
			channelPlatforms[chID] = chanCfg.Kind
		}
	}

	// Register only the channel plugins that match configured nostr_channels entries.
	availableKinds := extensions.AvailableKinds()
	n := extensions.RegisterConfigured(configState.Get())
	log.Printf("channel extensions: %d available, %d registered from config", len(availableKinds), n)

	extensionResults, err := channels.ConnectExtensions(ctx, configState.Get(), func(msg sdk.InboundChannelMessage) {
		// Per-channel allow-from check for extension channels.
		if chanCfgExt, ok := configState.Get().NostrChannels[msg.ChannelID]; ok {
			if dec := policy.EvaluateGroupMessage(msg.SenderID, chanCfgExt.AllowFrom, configState.Get()); !dec.Allowed {
				log.Printf("extension channel message rejected from=%s channel=%s reason=%s", msg.SenderID, msg.ChannelID, dec.Reason)
				return
			}
		}

		// Normalize inbound text: strip platform-specific bot mention prefixes.
		text := msg.Text
		if platform, ok := channelPlatforms[msg.ChannelID]; ok {
			text = channels.NormalizeInbound(platform, text, "")
			msg.Text = text
		}

		// Compute debounce key (includes thread ID when present for separate
		// thread-scoped queues).
		key := channels.DebounceKeyWithThread(msg.ChannelID, msg.SenderID, msg.ThreadID)
		if msg.EventID != "" {
			channelEventIDsMu.Lock()
			channelEventIDs[key] = msg.EventID
			channelEventIDsMu.Unlock()
		}
		// Submit to the debouncer; it will coalesce rapid messages from the same
		// sender (and thread) and fire after channelDebounceWindow of silence.
		channelDebouncer.Submit(key, msg.Text)
	})
	if err != nil {
		log.Printf("extension channel startup error: %v", err)
	}
	for _, r := range extensionResults {
		log.Printf("extension channel connected: %s (plugin=%s caps=%+v)", r.ChannelID, r.PluginID, r.Capabilities)
		defer r.Handle.Close()
		// Register handles so the debounce flush callback can send replies and
		// use optional channel features via interface assertions.
		channelHandlesMu.Lock()
		channelHandles[r.ChannelID] = r.Handle
		if r.RawHandle != nil {
			channelRawHandles[r.ChannelID] = r.RawHandle
		}
		channelHandlesMu.Unlock()
	}
	// Flush any in-flight debounced messages on shutdown.
	defer channelDebouncer.FlushAll()

	var controlBus *nostruntime.ControlRPCBus
	controlBus, err = nostruntime.StartControlRPCBus(ctx, nostruntime.ControlRPCBusOptions{
		Keyer:             controlServices.relay.keyer,
		Hub:               controlServices.relay.hub,
		Relays:            cfg.Relays,
		SinceUnix:         checkpointSinceUnix(controlCheckpoint.LastUnix),
		MaxRequestAge:     2 * time.Minute,
		MinCallerInterval: 100 * time.Millisecond,
		CachedLookup:      controlTracker.LookupResponse,
		OnRequest: func(ctx context.Context, in nostruntime.ControlRPCInbound) (nostruntime.ControlRPCResult, error) {
			if controlTracker.AlreadyProcessed(in.EventID, in.CreatedAt) {
				return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "duplicate": true}}, nil
			}
			return handleControlRPCRequest(ctx, in, bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
		},
		OnHandled: func(ctx context.Context, handled nostruntime.ControlRPCHandled) {
			if err := controlTracker.MarkHandled(ctx, docsRepo, handled); err != nil {
				log.Printf("control checkpoint update failed event=%s req=%s err=%v", handled.EventID, handled.RequestID, err)
			}
		},
		OnError: func(err error) {
			log.Printf("control rpc runtime error: %v", err)
		},
	})
	if err != nil {
		log.Fatalf("start control rpc bus: %v", err)
	}
	controlRPCBus = controlBus
	controlServices.relay.controlBus = controlBus
	defer controlBus.Close()

	if gatewayWSAddr != "" {
		wsMethods := append([]string{}, supportedMethods(configState.Get())...)
		wsMethods = append(wsMethods, gatewayws.MethodEventsList, gatewayws.MethodEventsSubscribe, gatewayws.MethodEventsUnsubscribe)
		wsPath := strings.TrimSpace(gatewayWSPath)
		if wsPath == "" {
			wsPath = "/ws"
		}
		wsRuntime, err := gatewayws.Start(ctx, gatewayws.RuntimeOptions{
			Addr:                   gatewayWSAddr,
			Path:                   wsPath,
			Token:                  gatewayWSToken,
			Methods:                wsMethods,
			Events:                 gatewayws.AllPushEvents,
			Version:                "metiqd",
			HandshakeTTL:           10 * time.Second,
			AuthRateLimitPerMin:    120,
			UnauthorizedBurstMax:   8,
			AllowedOrigins:         allowedOrigins,
			TrustedProxies:         trustedProxies,
			AllowInsecureControlUI: gatewayWSAllowInsecureControlUI,
			StaticHandler:          webui.Handler(wsPath, gatewayWSToken),
			HandleRequest: func(ctx context.Context, req gatewayprotocol.RequestFrame) (any, *gatewayprotocol.ErrorShape) {
				principal, _ := gatewayws.PrincipalFromContext(ctx)
				res, err := handleControlRPCRequest(ctx, gatewayControlRPCInbound(principal, req), bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
				if err != nil {
					return nil, mapGatewayWSError(err)
				}
				if strings.TrimSpace(res.Error) != "" {
					return nil, gatewayprotocol.NewError(gatewayprotocol.ErrorCodeUnavailable, res.Error, nil)
				}
				return res.Result, nil
			},
		})
		if err != nil {
			log.Fatalf("start gateway ws runtime: %v", err)
		}
		wsEmitter = newObservedEventEmitter(gatewayws.NewRuntimeEmitter(wsRuntime), eventBuffer)
		setControlWSEmitter(wsEmitter)
	}

	controlConfigFilePath = configFilePath
	controlServices.handlers.configFilePath = configFilePath

	// configState.Set hook: apply live runtime side effects + WS event on every
	// config mutation. Disk persistence is handled before successful Set() calls
	// in mutation paths so callers do not observe success when write-back fails.
	configState.SetOnChange(func(doc state.ConfigDoc) {
		// Bump prompt section cache generation so next prompt build recomputes.
		bumpPromptConfigGeneration()
		setRuntimeIdentityInfo(doc, pubkey)
		applyRuntimeConfigSideEffects(doc)
		grpcProviderCtl.reconcile(ctx, tools, doc, "live config apply")
		resolvedMCP := resolveMCPConfigWithDefaults(doc, fsOpts.WorkspaceDir())
		applyMCPConfigAndReconcile(ctx, &mcpManager, tools, resolvedMCP, "live config apply")
		filteredMCPLifecycle.Emit(runtimeEventEmitterFunc(emitControlWSEvent), resolvedMCP, "config.snapshot", time.Now().UnixMilli())
		wsEmitter.Emit(gatewayws.EventConfigUpdated, gatewayws.ConfigUpdatedPayload{
			TS: time.Now().UnixMilli(),
		})
	})

	// Start config file watcher for hot-reload (disk → runtimeConfigStore → relay).
	// The SyncEngine debounces rapid edits and calls our onChange callback on
	// each successful read, allowing the runtime to apply changes live.
	if configFilePath != "" && config.ConfigFileExists(configFilePath) {
		syncEngine, syncErr := config.NewSyncEngine(configFilePath, docsRepo,
			config.WithOnChange(func(doc state.ConfigDoc) error {
				doc, err := validateRuntimeConfigDoc(doc)
				if err != nil {
					return fmt.Errorf("path=%s err=%w", configFilePath, err)
				}
				if !applyRuntimeConfigReloadIfChanged(configState, doc, func(doc state.ConfigDoc) {
					log.Printf("config file changed: applying live reload path=%s", configFilePath)
					bumpPromptConfigGeneration()
					// Use the internal field directly to avoid triggering disk write-back
					// (the file already has the new content).
					configState.mu.Lock()
					configState.cfg = doc
					configState.mu.Unlock()
					setRuntimeIdentityInfo(doc, pubkey)
					applyRuntimeConfigSideEffects(doc)
					grpcProviderCtl.reconcile(ctx, tools, doc, "live file-reload apply")
					resolvedMCP := resolveMCPConfigWithDefaults(doc, fsOpts.WorkspaceDir())
					applyMCPConfigAndReconcile(ctx, &mcpManager, tools, resolvedMCP, "live file-reload apply")
					filteredMCPLifecycle.Emit(runtimeEventEmitterFunc(emitControlWSEvent), resolvedMCP, "file-reload.snapshot", time.Now().UnixMilli())
					wsEmitter.Emit(gatewayws.EventConfigUpdated, gatewayws.ConfigUpdatedPayload{
						TS: time.Now().UnixMilli(),
					})
				}) {
					log.Printf("config file changed: no live reload needed path=%s", configFilePath)
				}
				return nil
			}),
		)
		if syncErr != nil {
			log.Printf("config sync engine init warning: %v", syncErr)
		} else {
			if err := syncEngine.Start(ctx); err != nil {
				log.Printf("config sync engine start warning: %v", err)
			} else {
				defer syncEngine.Stop()
				log.Printf("config file hot-reload active path=%s", configFilePath)
			}
		}
	}

	// ── SIGHUP config hot-reload ─────────────────────────────────────────────
	// Receiving SIGHUP triggers an immediate re-read of configFilePath and
	// applies the new config to the running daemon — same as OpenClaw's
	// SIGHUP handler.  The fsnotify SyncEngine watches for file changes
	// automatically; SIGHUP is a manual override (useful for scripts and
	// systemd/launchd ExecReload= directives).
	if configFilePath != "" {
		sighupCh := make(chan os.Signal, 1)
		signal.Notify(sighupCh, syscall.SIGHUP)
		go func() {
			for {
				select {
				case <-ctx.Done():
					signal.Stop(sighupCh)
					return
				case <-sighupCh:
					log.Printf("SIGHUP received: reloading config from %s", configFilePath)
					raw, readErr := os.ReadFile(configFilePath)
					if readErr != nil {
						log.Printf("SIGHUP reload: read config failed path=%s err=%v", configFilePath, readErr)
						continue
					}
					newDoc, parseErr := config.ParseConfigBytes(raw, configFilePath)
					if parseErr != nil {
						log.Printf("SIGHUP reload: parse config failed err=%v", parseErr)
						continue
					}
					newDoc, validateErr := validateRuntimeConfigDoc(newDoc)
					if validateErr != nil {
						log.Printf("SIGHUP reload: invalid config err=%v", validateErr)
						continue
					}
					commit, commitErr := commitRuntimeConfigMutation(ctx, docsRepo, configState, configMutationCommitRequest{
						BuildNext: func(current state.ConfigDoc) (state.ConfigDoc, error) {
							return newDoc, nil
						},
					})
					if commitErr != nil {
						log.Printf("SIGHUP reload: durable config apply failed err=%v", commitErr)
						continue
					}
					if commit.RuntimeApplied {
						log.Printf("SIGHUP reload: config applied successfully agents=%d relays=%d",
							len(commit.Next.Agents), len(commit.Next.Relays.Read))
					} else {
						log.Printf("SIGHUP reload: durable config refreshed; live apply unchanged")
					}
				}
			}
		}()
		log.Printf("SIGHUP handler registered (config reload on SIGHUP)")
	}

	if adminAddr != "" {
		go func() {
			err := admin.Start(ctx, admin.ServerOptions{
				Addr:  adminAddr,
				Token: adminToken,
				Status: admin.StatusProvider{
					PubKey:   bus.PublicKey(),
					Relays:   cfg.Relays,
					DMPolicy: configState.Get().DM.Policy,
					Started:  startedAt,
				},
				StatusDMPolicy: func() string {
					return configState.Get().DM.Policy
				},
				StatusRelays: func() []string {
					current := configState.Get()
					return append([]string{}, current.Relays.Read...)
				},
				StatusMCP: func() *mcppkg.TelemetrySnapshot {
					if controlServices.handlers.mcpOps == nil {
						return nil
					}
					return controlServices.handlers.mcpOps.telemetrySnapshotPtr()
				},
				Metrics: func(_ context.Context) string {
					// Update live gauges before rendering.
					metricspkg.UptimeSeconds.Set(time.Since(startedAt).Seconds())
					metricspkg.RelayConnected.Set(float64(len(cfg.Relays)))
					if controlServices.session.execApprovals != nil {
						pending := controlServices.session.execApprovals.GetGlobal()
						metricspkg.ApprovalQueueSize.Set(float64(len(pending)))
					}
					return metricspkg.Default.Exposition()
				},
				HealthExtra: func(_ context.Context) map[string]any {
					body := map[string]any{
						"uptime_seconds": int(time.Since(startedAt).Seconds()),
						"version":        version,
					}
					if controlServices.handlers.mcpOps != nil {
						if snapshot := controlServices.handlers.mcpOps.telemetrySnapshotPtr(); snapshot != nil {
							body["mcp"] = map[string]any{
								"enabled": snapshot.Enabled,
								"summary": snapshot.Summary,
							}
						}
					}
					return body
				},
				SearchMemory: func(query string, limit int) []memory.IndexedMemory {
					return memoryIndex.Search(query, limit)
				},
				MemoryStats: func() (int, int) {
					return memoryIndex.Count(), memoryIndex.SessionCount()
				},
				GetCheckpoint: func(ctx context.Context, name string) (state.CheckpointDoc, error) {
					return docsRepo.GetCheckpoint(ctx, name)
				},
				StartAgent: func(ctx context.Context, req methods.AgentRequest) (map[string]any, error) {
					// Default session ID for agent runs is daemon's pubkey (server-side session)
					if req.SessionID == "" {
						req.SessionID = bus.PublicKey()
					}
					rt := controlAgentRuntime
					if rt == nil {
						return nil, fmt.Errorf("agent runtime not configured")
					}
					runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
					snapshot := agentJobs.Begin(runID, req.SessionID)
					go executeAgentRun(runID, req, rt, memoryIndex, agentJobs)
					return map[string]any{"run_id": runID, "status": "accepted", "accepted_at": snapshot.StartedAt}, nil
				},
				WaitAgent: func(ctx context.Context, req methods.AgentWaitRequest) (map[string]any, error) {
					snap, ok := agentJobs.Wait(ctx, req.RunID, time.Duration(req.TimeoutMS)*time.Millisecond)
					if !ok {
						return nil, fmt.Errorf("run not found")
					}
					if snap.Status == "pending" {
						return map[string]any{"run_id": req.RunID, "status": "timeout"}, nil
					}
					out := map[string]any{"run_id": req.RunID, "status": snap.Status, "started_at": snap.StartedAt, "ended_at": snap.EndedAt}
					if snap.Err != "" {
						out["error"] = snap.Err
					}
					if snap.Result != "" {
						out["result"] = snap.Result
					}
					if snap.FallbackUsed {
						out["fallback_used"] = true
						out["fallback_from"] = snap.FallbackFrom
						out["fallback_to"] = snap.FallbackTo
						if snap.FallbackReason != "" {
							out["fallback_reason"] = truncateRunes(snap.FallbackReason, 200)
						}
					}
					return out, nil
				},
				AgentIdentity: func(ctx context.Context, req methods.AgentIdentityRequest) (map[string]any, error) {
					agentID := strings.TrimSpace(req.AgentID)
					if agentID == "" {
						agentID = "main"
					}
					displayName := "Metiq Agent"
					if doc, err := docsRepo.GetAgent(ctx, agentID); err == nil && doc.Name != "" {
						displayName = doc.Name
					}
					return map[string]any{"agent_id": agentID, "display_name": displayName, "session_id": req.SessionID, "pubkey": bus.PublicKey()}, nil
				},
				GatewayIdentity: func(_ context.Context) (map[string]any, error) {
					pk := bus.PublicKey()
					deviceID := pk
					if len(deviceID) > 24 {
						deviceID = deviceID[:24]
					}
					return map[string]any{"deviceId": deviceID, "publicKey": pk, "pubkey": pk}, nil
				},
				SendDM: func(ctx context.Context, to string, text string) error {
					sendCtx, release := chatCancels.Begin(to, ctx)
					defer release()
					return bus.SendDM(sendCtx, to, text)
				},
				AbortChat: func(_ context.Context, sessionID string) (int, error) {
					aborted := 0
					if strings.TrimSpace(sessionID) == "" {
						aborted = chatCancels.AbortAll()
					} else if chatCancels.Abort(sessionID) {
						aborted = 1
					}
					usageState.RecordAbort(aborted)
					return aborted, nil
				},
				GetSession: func(ctx context.Context, sessionID string) (state.SessionDoc, error) {
					return docsRepo.GetSession(ctx, sessionID)
				},
				PutSession: func(ctx context.Context, sessionID string, doc state.SessionDoc) error {
					return replaceSessionDoc(ctx, docsRepo, sessionID, doc)
				},
				ListSessions: func(ctx context.Context, limit int) ([]state.SessionDoc, error) {
					return docsRepo.ListSessions(ctx, limit)
				},
				SessionStore: sessionStore,
				ListTranscript: func(ctx context.Context, sessionID string, limit int) ([]state.TranscriptEntryDoc, error) {
					return transcriptRepo.ListSession(ctx, sessionID, limit)
				},
				SessionsPrune: func(ctx context.Context, req methods.SessionsPruneRequest) (map[string]any, error) {
					return runSessionsPrune(ctx, docsRepo, transcriptRepo, req, "manual")
				},
				TailLogs: func(_ context.Context, cursor int64, limit int, maxBytes int) (map[string]any, error) {
					return logBuffer.Tail(cursor, limit, maxBytes), nil
				},
				ObserveRuntime: func(obsCtx context.Context, req methods.RuntimeObserveRequest) (map[string]any, error) {
					return toolbuiltin.ObserveRuntime(obsCtx, runtimeObserveToolRequest(req))
				},
				ChannelsStatus: func(_ context.Context, _ methods.ChannelsStatusRequest) (map[string]any, error) {
					current := configState.Get()
					status := channelState.Status(bus, controlBus, current)
					return map[string]any{"channels": []map[string]any{buildNostrChannelStatusRow(status, "")}}, nil
				},
				ChannelsLogout: func(_ context.Context, channel string) (map[string]any, error) {
					return channelState.Logout(channel)
				},
				UsageStatus: func(_ context.Context) (map[string]any, error) {
					return map[string]any{"ok": true, "totals": usageState.Status()}, nil
				},
				UsageCost: func(_ context.Context, req methods.UsageCostRequest) (map[string]any, error) {
					if req.StartDate != "" || req.EndDate != "" || req.Days > 0 {
						return nil, fmt.Errorf("usage.cost: date filtering is not supported")
					}
					cost := usageState.Cost()
					return map[string]any{"ok": true, "total_usd": cost["total_usd"], "estimate": cost, "filtered": false}, nil
				},
				GetList: func(ctx context.Context, name string) (state.ListDoc, error) {
					return docsRepo.GetList(ctx, strings.ToLower(strings.TrimSpace(name)))
				},
				GetListWithEvent: func(ctx context.Context, name string) (state.ListDoc, state.Event, error) {
					return docsRepo.GetListWithEvent(ctx, strings.ToLower(strings.TrimSpace(name)))
				},
				PutList: func(ctx context.Context, name string, doc state.ListDoc) error {
					name = strings.ToLower(strings.TrimSpace(name))
					if name == "" {
						return fmt.Errorf("name is required")
					}
					doc.Name = name
					if doc.Version == 0 {
						doc.Version = 1
					}
					_, err := docsRepo.PutList(ctx, name, doc)
					return err
				},
				ListAgents: func(ctx context.Context, req methods.AgentsListRequest) (map[string]any, error) {
					agents, err := docsRepo.ListAgents(ctx, req.Limit)
					if err != nil {
						return nil, err
					}
					return map[string]any{"agents": agents}, nil
				},
				CreateAgent: func(ctx context.Context, req methods.AgentsCreateRequest) (map[string]any, error) {
					if _, err := docsRepo.GetAgent(ctx, req.AgentID); err == nil {
						return nil, fmt.Errorf("agent %q already exists", req.AgentID)
					} else if !errors.Is(err, state.ErrNotFound) {
						return nil, err
					}
					doc := state.AgentDoc{Version: 1, AgentID: req.AgentID, Name: req.Name, Workspace: req.Workspace, Model: req.Model, Meta: req.Meta}
					_, err := docsRepo.PutAgent(ctx, req.AgentID, doc)
					if err != nil {
						return nil, err
					}
					return map[string]any{"ok": true, "agent": doc}, nil
				},
				UpdateAgent: func(ctx context.Context, req methods.AgentsUpdateRequest) (map[string]any, error) {
					doc, err := docsRepo.GetAgent(ctx, req.AgentID)
					if err != nil {
						return nil, err
					}
					if req.Name != "" {
						doc.Name = req.Name
					}
					if req.Workspace != "" {
						doc.Workspace = req.Workspace
					}
					if req.Model != "" {
						doc.Model = req.Model
					}
					doc.Meta = mergeSessionMeta(doc.Meta, req.Meta)
					if doc.Version == 0 {
						doc.Version = 1
					}
					_, err = docsRepo.PutAgent(ctx, req.AgentID, doc)
					if err != nil {
						return nil, err
					}
					return map[string]any{"ok": true, "agent": doc}, nil
				},
				DeleteAgent: func(ctx context.Context, req methods.AgentsDeleteRequest) (map[string]any, error) {
					doc, err := docsRepo.GetAgent(ctx, req.AgentID)
					if err != nil {
						return nil, err
					}
					doc.Deleted = true
					doc.Meta = mergeSessionMeta(doc.Meta, map[string]any{"deleted_at": time.Now().Unix()})
					if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
						return nil, err
					}
					return map[string]any{"ok": true, "agent_id": req.AgentID, "deleted": true}, nil
				},
				ListAgentFiles: func(ctx context.Context, req methods.AgentsFilesListRequest) (map[string]any, error) {
					files, err := docsRepo.ListAgentFiles(ctx, req.AgentID, req.Limit)
					if err != nil {
						return nil, err
					}
					out := make([]map[string]any, 0, len(files))
					for _, file := range files {
						out = append(out, map[string]any{"name": file.Name, "size": len(file.Content)})
					}
					return map[string]any{"agent_id": req.AgentID, "files": out}, nil
				},
				GetAgentFile: func(ctx context.Context, req methods.AgentsFilesGetRequest) (map[string]any, error) {
					file, err := docsRepo.GetAgentFile(ctx, req.AgentID, req.Name)
					if err != nil {
						if errors.Is(err, state.ErrNotFound) {
							return map[string]any{"agent_id": req.AgentID, "file": map[string]any{"name": req.Name, "missing": true}}, nil
						}
						return nil, err
					}
					return map[string]any{"agent_id": req.AgentID, "file": map[string]any{"name": file.Name, "missing": false, "content": file.Content}}, nil
				},
				SetAgentFile: func(ctx context.Context, req methods.AgentsFilesSetRequest) (map[string]any, error) {
					doc := state.AgentFileDoc{Version: 1, AgentID: req.AgentID, Name: req.Name, Content: req.Content}
					if _, err := docsRepo.PutAgentFile(ctx, req.AgentID, req.Name, doc); err != nil {
						return nil, err
					}
					return map[string]any{"ok": true, "agent_id": req.AgentID, "file": map[string]any{"name": req.Name, "missing": false, "content": req.Content}}, nil
				},
				ListModels: func(_ context.Context, _ methods.ModelsListRequest) (map[string]any, error) {
					return map[string]any{"models": defaultModelsCatalog(configState.Get().Providers)}, nil
				},
				ToolsCatalog: func(ctx context.Context, req methods.ToolsCatalogRequest) (map[string]any, error) {
					if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
						return nil, err
					}
					cfg := configState.Get()
					agentID := defaultAgentID(req.AgentID)
					groups := buildToolCatalogGroups(cfg, tools, req.IncludePlugins, pluginMgr)
					if req.Profile != nil && *req.Profile != "" {
						profileID := strings.TrimSpace(strings.ToLower(*req.Profile))
						if agent.LookupProfile(profileID) == nil {
							return nil, fmt.Errorf("unknown profile %q; valid: %s", profileID, strings.Join(agent.ProfileListSorted(), ", "))
						}
						groups = agent.FilterCatalogByProfile(groups, profileID)
					}
					return map[string]any{"agentId": agentID, "profiles": defaultToolProfiles(), "groups": groups}, nil
				},
				ToolsProfileGet: func(ctx context.Context, req methods.ToolsProfileGetRequest) (map[string]any, error) {
					if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
						return nil, err
					}
					agentID := defaultAgentID(req.AgentID)
					doc, _ := docsRepo.GetAgent(ctx, agentID)
					profileID := agent.DefaultProfile
					if p, ok := doc.Meta[agent.AgentProfileKey].(string); ok && p != "" {
						profileID = p
					}
					return map[string]any{"agentId": agentID, "profile": profileID}, nil
				},
				ToolsProfileSet: func(ctx context.Context, req methods.ToolsProfileSetRequest) (map[string]any, error) {
					if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
						return nil, err
					}
					if agent.LookupProfile(req.Profile) == nil {
						return nil, fmt.Errorf("unknown profile %q; valid: %s", req.Profile, strings.Join(agent.ProfileListSorted(), ", "))
					}
					agentID := defaultAgentID(req.AgentID)
					doc, _ := docsRepo.GetAgent(ctx, agentID)
					if doc.Meta == nil {
						doc.Meta = map[string]any{}
					}
					if doc.AgentID == "" {
						doc = state.AgentDoc{Version: 1, AgentID: agentID, Meta: map[string]any{}}
					}
					doc.Meta[agent.AgentProfileKey] = req.Profile
					if _, err := docsRepo.PutAgent(ctx, agentID, doc); err != nil {
						return nil, err
					}
					return map[string]any{"agentId": agentID, "profile": req.Profile}, nil
				},
				SkillsStatus: func(ctx context.Context, req methods.SkillsStatusRequest) (map[string]any, error) {
					if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
						return nil, err
					}
					return buildSkillsStatusReport(configState.Get(), defaultAgentID(req.AgentID)), nil
				},
				SkillsBins: func(_ context.Context, _ methods.SkillsBinsRequest) (map[string]any, error) {
					return applySkillsBins(configState.Get()), nil
				},
				SkillsInstall: func(ctx context.Context, req methods.SkillsInstallRequest) (map[string]any, error) {
					if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
						return nil, err
					}
					_, result, err := applySkillInstall(ctx, docsRepo, configState, req)
					if err != nil {
						return nil, err
					}
					return result, nil
				},
				SkillsUpdate: func(ctx context.Context, req methods.SkillsUpdateRequest) (map[string]any, error) {
					_, entry, err := applySkillUpdate(ctx, docsRepo, configState, req)
					if err != nil {
						return nil, err
					}
					return map[string]any{"ok": true, "skillKey": strings.ToLower(strings.TrimSpace(req.SkillKey)), "config": entry}, nil
				},
				PluginsInstall: func(ctx context.Context, req methods.PluginsInstallRequest) (map[string]any, error) {
					return applyPluginInstallRuntime(ctx, docsRepo, configState, req)
				},
				PluginsUninstall: func(ctx context.Context, req methods.PluginsUninstallRequest) (map[string]any, error) {
					return applyPluginUninstallRuntime(ctx, docsRepo, configState, req)
				},
				PluginsUpdate: func(ctx context.Context, req methods.PluginsUpdateRequest) (map[string]any, error) {
					return applyPluginUpdateRuntime(ctx, docsRepo, configState, req)
				},
				PluginsRegistryList: func(ctx context.Context, req methods.PluginsRegistryListRequest) (map[string]any, error) {
					return handlePluginsRegistryList(ctx, configState, req)
				},
				PluginsRegistryGet: func(ctx context.Context, req methods.PluginsRegistryGetRequest) (map[string]any, error) {
					return handlePluginsRegistryGet(ctx, configState, req)
				},
				PluginsRegistrySearch: func(ctx context.Context, req methods.PluginsRegistrySearchRequest) (map[string]any, error) {
					return handlePluginsRegistrySearch(ctx, configState, req)
				},
				NodePairRequest: func(ctx context.Context, req methods.NodePairRequest) (map[string]any, error) {
					return applyNodePairRequest(ctx, docsRepo, configState, req)
				},
				NodePairList: func(ctx context.Context, req methods.NodePairListRequest) (map[string]any, error) {
					return applyNodePairList(ctx, configState, req)
				},
				NodePairApprove: func(ctx context.Context, req methods.NodePairApproveRequest) (map[string]any, error) {
					return applyNodePairApprove(ctx, docsRepo, configState, req)
				},
				NodePairReject: func(ctx context.Context, req methods.NodePairRejectRequest) (map[string]any, error) {
					return applyNodePairReject(ctx, docsRepo, configState, req)
				},
				NodePairVerify: func(ctx context.Context, req methods.NodePairVerifyRequest) (map[string]any, error) {
					return applyNodePairVerify(ctx, configState, req)
				},
				DevicePairList: func(ctx context.Context, req methods.DevicePairListRequest) (map[string]any, error) {
					return applyDevicePairList(ctx, configState, req)
				},
				DevicePairApprove: func(ctx context.Context, req methods.DevicePairApproveRequest) (map[string]any, error) {
					return applyDevicePairApprove(ctx, docsRepo, configState, req)
				},
				DevicePairReject: func(ctx context.Context, req methods.DevicePairRejectRequest) (map[string]any, error) {
					return applyDevicePairReject(ctx, docsRepo, configState, req)
				},
				DevicePairRemove: func(ctx context.Context, req methods.DevicePairRemoveRequest) (map[string]any, error) {
					return applyDevicePairRemove(ctx, docsRepo, configState, req)
				},
				DeviceTokenRotate: func(ctx context.Context, req methods.DeviceTokenRotateRequest) (map[string]any, error) {
					return applyDeviceTokenRotate(ctx, docsRepo, configState, req)
				},
				DeviceTokenRevoke: func(ctx context.Context, req methods.DeviceTokenRevokeRequest) (map[string]any, error) {
					return applyDeviceTokenRevoke(ctx, docsRepo, configState, req)
				},
				NodeList: func(_ context.Context, req methods.NodeListRequest) (map[string]any, error) {
					return applyNodeList(configState, req)
				},
				NodeDescribe: func(_ context.Context, req methods.NodeDescribeRequest) (map[string]any, error) {
					return applyNodeDescribe(configState, req)
				},
				NodeRename: func(ctx context.Context, req methods.NodeRenameRequest) (map[string]any, error) {
					return applyNodeRename(ctx, docsRepo, configState, req)
				},
				NodeCanvasCapabilityRefresh: func(_ context.Context, req methods.NodeCanvasCapabilityRefreshRequest) (map[string]any, error) {
					return applyNodeCanvasCapabilityRefresh(configState, req)
				},
				NodeInvoke: func(_ context.Context, req methods.NodeInvokeRequest) (map[string]any, error) {
					return applyNodeInvoke(nodeInvocations, req)
				},
				NodeEvent: func(_ context.Context, req methods.NodeEventRequest) (map[string]any, error) {
					return applyNodeEvent(nodeInvocations, req)
				},
				NodeResult: func(_ context.Context, req methods.NodeResultRequest) (map[string]any, error) {
					return applyNodeResult(nodeInvocations, req)
				},
				NodePendingEnqueue: func(_ context.Context, req methods.NodePendingEnqueueRequest) (map[string]any, error) {
					return nodePending.Enqueue(nodepending.EnqueueRequest{NodeID: req.NodeID, Command: req.Command, Args: req.Args, IdempotencyKey: req.IdempotencyKey, TTLMS: req.TTLMS})
				},
				NodePendingPull: func(_ context.Context, req methods.NodePendingPullRequest) (map[string]any, error) {
					return nodePending.Pull(req.NodeID)
				},
				NodePendingAck: func(_ context.Context, req methods.NodePendingAckRequest) (map[string]any, error) {
					return nodePending.Ack(nodepending.AckRequest{NodeID: req.NodeID, IDs: req.IDs})
				},
				NodePendingDrain: func(_ context.Context, req methods.NodePendingDrainRequest) (map[string]any, error) {
					return nodePending.Drain(nodepending.DrainRequest{NodeID: req.NodeID, MaxItems: req.MaxItems})
				},
				CronList: func(_ context.Context, req methods.CronListRequest) (map[string]any, error) {
					return applyCronList(cronJobs, req)
				},
				CronStatus: func(_ context.Context, req methods.CronStatusRequest) (map[string]any, error) {
					return applyCronStatus(cronJobs, req)
				},
				CronAdd: func(_ context.Context, req methods.CronAddRequest) (map[string]any, error) {
					return applyCronAdd(cronJobs, req)
				},
				CronUpdate: func(_ context.Context, req methods.CronUpdateRequest) (map[string]any, error) {
					return applyCronUpdate(cronJobs, req)
				},
				CronRemove: func(_ context.Context, req methods.CronRemoveRequest) (map[string]any, error) {
					return applyCronRemove(cronJobs, req)
				},
				CronRun: func(_ context.Context, req methods.CronRunRequest) (map[string]any, error) {
					return applyCronRun(cronJobs, req)
				},
				CronRuns: func(_ context.Context, req methods.CronRunsRequest) (map[string]any, error) {
					return applyCronRuns(cronJobs, req)
				},
				ExecApprovalsGet: func(_ context.Context, req methods.ExecApprovalsGetRequest) (map[string]any, error) {
					return applyExecApprovalsGet(execApprovals, req)
				},
				ExecApprovalsSet: func(_ context.Context, req methods.ExecApprovalsSetRequest) (map[string]any, error) {
					return applyExecApprovalsSet(execApprovals, req)
				},
				ExecApprovalsNodeGet: func(_ context.Context, req methods.ExecApprovalsNodeGetRequest) (map[string]any, error) {
					return applyExecApprovalsNodeGet(execApprovals, req)
				},
				ExecApprovalsNodeSet: func(_ context.Context, req methods.ExecApprovalsNodeSetRequest) (map[string]any, error) {
					return applyExecApprovalsNodeSet(execApprovals, req)
				},
				ExecApprovalRequest: func(_ context.Context, req methods.ExecApprovalRequestRequest) (map[string]any, error) {
					return applyExecApprovalRequest(execApprovals, req)
				},
				ExecApprovalWaitDecision: func(ctx context.Context, req methods.ExecApprovalWaitDecisionRequest) (map[string]any, error) {
					return applyExecApprovalWaitDecision(ctx, execApprovals, req)
				},
				ExecApprovalResolve: func(_ context.Context, req methods.ExecApprovalResolveRequest) (map[string]any, error) {
					return applyExecApprovalResolve(execApprovals, req)
				},
				SandboxRun: func(ctx context.Context, req methods.SandboxRunRequest) (map[string]any, error) {
					return applySandboxRun(ctx, configState, req)
				},
				MCPList: func(ctx context.Context, req methods.MCPListRequest) (map[string]any, error) {
					return controlServices.handlers.mcpOps.applyList(ctx, req)
				},
				MCPGet: func(ctx context.Context, req methods.MCPGetRequest) (map[string]any, error) {
					return controlServices.handlers.mcpOps.applyGet(ctx, req)
				},
				MCPPut: func(ctx context.Context, req methods.MCPPutRequest) (map[string]any, error) {
					return controlServices.handlers.mcpOps.applyPut(ctx, req)
				},
				MCPRemove: func(ctx context.Context, req methods.MCPRemoveRequest) (map[string]any, error) {
					return controlServices.handlers.mcpOps.applyRemove(ctx, req)
				},
				MCPTest: func(ctx context.Context, req methods.MCPTestRequest) (map[string]any, error) {
					return controlServices.handlers.mcpOps.applyTest(ctx, req)
				},
				MCPReconnect: func(ctx context.Context, req methods.MCPReconnectRequest) (map[string]any, error) {
					return controlServices.handlers.mcpOps.applyReconnect(ctx, req)
				},
				MCPAuthStart: func(ctx context.Context, req methods.MCPAuthStartRequest) (map[string]any, error) {
					return mcpAuthController.applyStart(ctx, req)
				},
				MCPAuthRefresh: func(ctx context.Context, req methods.MCPAuthRefreshRequest) (map[string]any, error) {
					return mcpAuthController.applyRefresh(ctx, req)
				},
				MCPAuthClear: func(ctx context.Context, req methods.MCPAuthClearRequest) (map[string]any, error) {
					return mcpAuthController.applyClear(ctx, req)
				},
				SecretsReload: func(_ context.Context, req methods.SecretsReloadRequest) (map[string]any, error) {
					return applySecretsReload(req)
				},
				SecretsResolve: func(_ context.Context, req methods.SecretsResolveRequest) (map[string]any, error) {
					return applySecretsResolve(req)
				},
				WizardStart: func(_ context.Context, req methods.WizardStartRequest) (map[string]any, error) {
					return applyWizardStart(wizards, req)
				},
				WizardNext: func(_ context.Context, req methods.WizardNextRequest) (map[string]any, error) {
					return applyWizardNext(wizards, req)
				},
				WizardCancel: func(_ context.Context, req methods.WizardCancelRequest) (map[string]any, error) {
					return applyWizardCancel(wizards, req)
				},
				WizardStatus: func(_ context.Context, req methods.WizardStatusRequest) (map[string]any, error) {
					return applyWizardStatus(wizards, req)
				},
				UpdateRun: func(_ context.Context, req methods.UpdateRunRequest) (map[string]any, error) {
					return applyUpdateRun(ops, req)
				},
				TalkConfig: func(_ context.Context, req methods.TalkConfigRequest) (map[string]any, error) {
					return applyTalkConfig(configState.Get(), ops, req)
				},
				TalkMode: func(_ context.Context, req methods.TalkModeRequest) (map[string]any, error) {
					return applyTalkMode(ops, req)
				},
				LastHeartbeat: func(_ context.Context, req methods.LastHeartbeatRequest) (map[string]any, error) {
					return applyLastHeartbeat(ops, req)
				},
				SetHeartbeats: func(_ context.Context, req methods.SetHeartbeatsRequest) (map[string]any, error) {
					return applySetHeartbeats(ops, req)
				},
				Wake: func(_ context.Context, req methods.WakeRequest) (map[string]any, error) {
					return applyWake(ops, req)
				},
				SystemPresence: func(_ context.Context, req methods.SystemPresenceRequest) ([]map[string]any, error) {
					return applySystemPresence(ops, req)
				},
				SystemEvent: func(_ context.Context, req methods.SystemEventRequest) (map[string]any, error) {
					return applySystemEvent(ops, req)
				},
				Send: func(ctx context.Context, req methods.SendRequest) (map[string]any, error) {
					return applySend(ctx, bus, req)
				},
				BrowserRequest: func(_ context.Context, req methods.BrowserRequestRequest) (map[string]any, error) {
					return applyBrowserRequest(req)
				},
				VoicewakeGet: func(_ context.Context, req methods.VoicewakeGetRequest) (map[string]any, error) {
					return applyVoicewakeGet(ops, req)
				},
				VoicewakeSet: func(_ context.Context, req methods.VoicewakeSetRequest) (map[string]any, error) {
					return applyVoicewakeSet(ops, req)
				},
				TTSStatus: func(_ context.Context, req methods.TTSStatusRequest) (map[string]any, error) {
					return applyTTSStatus(ops, req)
				},
				TTSProviders: func(_ context.Context, req methods.TTSProvidersRequest) (map[string]any, error) {
					return applyTTSProviders(ops, req)
				},
				TTSSetProvider: func(_ context.Context, req methods.TTSSetProviderRequest) (map[string]any, error) {
					return applyTTSSetProvider(ops, req)
				},
				TTSEnable: func(_ context.Context, req methods.TTSEnableRequest) (map[string]any, error) {
					return applyTTSEnable(ops, req)
				},
				TTSDisable: func(_ context.Context, req methods.TTSDisableRequest) (map[string]any, error) {
					return applyTTSDisable(ops, req)
				},
				TTSConvert: func(ctx context.Context, req methods.TTSConvertRequest) (map[string]any, error) {
					return applyTTSConvert(ctx, ops, req)
				},
				GetConfig: func(ctx context.Context) (state.ConfigDoc, error) {
					return docsRepo.GetConfig(ctx)
				},
				GetConfigWithEvent: func(ctx context.Context) (state.ConfigDoc, state.Event, error) {
					return docsRepo.GetConfigWithEvent(ctx)
				},
				SupportedMethods: func(_ context.Context) ([]string, error) {
					return supportedMethods(configState.Get()), nil
				},
				DelegateControlCall: func(ctx context.Context, method string, params json.RawMessage) (any, int, error) {
					return dispatchAdminDelegatedControlCall(ctx, admin.CallerPubKeyFromContext(ctx), method, params, bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
				},
				GetRelayPolicy: func(context.Context) (methods.RelayPolicyResponse, error) {
					current := configState.Get()
					return methods.RelayPolicyResponse{
						ReadRelays:           append([]string{}, current.Relays.Read...),
						WriteRelays:          append([]string{}, current.Relays.Write...),
						RuntimeDMRelays:      bus.Relays(),
						RuntimeControlRelays: controlBus.Relays(),
					}, nil
				},
				PutConfig: func(ctx context.Context, newCfg state.ConfigDoc) error {
					_, err := commitRuntimeConfigMutation(ctx, docsRepo, configState, configMutationCommitRequest{
						BuildNext: func(current state.ConfigDoc) (state.ConfigDoc, error) {
							return newCfg, nil
						},
					})
					return err
				},
				ConfigSet: func(ctx context.Context, req methods.ConfigSetRequest) (map[string]any, int, error) {
					return dispatchAdminControlConfigMutation(ctx, adminControlMutationCaller(ctx, bus.PublicKey()), methods.MethodConfigSet, req, bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
				},
				ConfigApply: func(ctx context.Context, req methods.ConfigApplyRequest) (map[string]any, int, error) {
					return dispatchAdminControlConfigMutation(ctx, adminControlMutationCaller(ctx, bus.PublicKey()), methods.MethodConfigApply, req, bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
				},
				ConfigPatch: func(ctx context.Context, req methods.ConfigPatchRequest) (map[string]any, int, error) {
					return dispatchAdminControlConfigMutation(ctx, adminControlMutationCaller(ctx, bus.PublicKey()), methods.MethodConfigPatch, req, bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
				},
			})
			if err != nil {
				log.Printf("admin API error: %v", err)
			}
		}()
	}

	// ── Cron auto-scheduler ─────────────────────────────────────────────────────
	// Register the daemon-internal RPC executor so the scheduler can fire jobs.
	daemonPubKey := bus.PublicKey()
	controlServices.handlers.cronExecutorMu.Lock()
	controlCronExecutor = func(execCtx context.Context, method string, params json.RawMessage) (any, error) {
		res, err := handleControlRPCRequest(execCtx,
			nostruntime.ControlRPCInbound{
				FromPubKey: daemonPubKey,
				Method:     method,
				Params:     params,
				Internal:   true,
			},
			bus, controlBus, chatCancels, usageState, logBuffer, channelState,
			docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt,
		)
		if err != nil {
			return nil, err
		}
		return res.Result, nil
	}
	controlServices.handlers.cronExecutorMu.Unlock()

	// Scheduler goroutine: ticks every minute and fires enabled cron jobs.
	go func() {
		for {
			now := time.Now()
			next := now.Truncate(time.Minute).Add(time.Minute)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
			}
			tickTime := time.Now().Truncate(time.Minute)
			jobs := controlServices.session.cronJobs.List(1000)
			for _, job := range jobs {
				if !job.Enabled {
					continue
				}
				sched, parseErr := cron.Parse(job.Schedule)
				if parseErr != nil {
					log.Printf("cron scheduler: job %s has invalid schedule %q: %v", job.ID, job.Schedule, parseErr)
					continue
				}
				if !sched.Matches(tickTime) {
					continue
				}
				jobCopy := job
				go func() {
					emitControlWSEvent(gatewayws.EventCronTick, gatewayws.CronTickPayload{
						TS:    tickTime.UnixMilli(),
						JobID: jobCopy.ID,
					})
					started := time.Now()
					jobCtx, cancel := context.WithTimeout(ctx, cfgTimeouts.CronJobExec(configState.Get().CronCfg))
					defer cancel()
					_, execErr := func() (any, error) {
						controlServices.handlers.cronExecutorMu.RLock()
						exec := controlCronExecutor
						controlServices.handlers.cronExecutorMu.RUnlock()
						if exec == nil {
							return nil, fmt.Errorf("cron executor not ready")
						}
						return exec(jobCtx, jobCopy.Method, jobCopy.Params)
					}()
					status := "ok"
					if execErr != nil {
						status = "error"
						log.Printf("cron job %s (%s) failed: %v", jobCopy.ID, jobCopy.Method, execErr)
					}
					controlServices.session.cronJobs.RecordRun(jobCopy.ID, status, time.Since(started).Milliseconds())
					emitControlWSEvent(gatewayws.EventCronResult, gatewayws.CronResultPayload{
						TS:         time.Now().UnixMilli(),
						JobID:      jobCopy.ID,
						Succeeded:  status == "ok",
						DurationMS: time.Since(started).Milliseconds(),
					})
				}()
			}
		}
	}()

	fmt.Printf("metiqd running pubkey=%s relays=%d state_store=nostr dm_policy=%s admin=%s\n",
		bus.PublicKey(), len(cfg.Relays), configState.Get().DM.Policy, adminAddr)

	// Fire gateway:startup hook now that all channels and goroutines are ready.
	if controlServices.handlers.hooksMgr != nil {
		go controlServices.handlers.hooksMgr.Fire("gateway:startup", "", map[string]any{})
	}

	// Boot-time session pruning honors the configured age- and idle-based
	// policies when PruneOnBoot is set in the session config.
	if configState != nil {
		sessCfg := configState.Get().Session
		if sessCfg.PruneOnBoot && sessCfg.PruneAfterDays > 0 {
			go func() {
				pruneSessions(ctx, docsRepo, transcriptRepo, sessCfg.PruneAfterDays)
			}()
		}
		if sessCfg.PruneOnBoot && sessCfg.PruneIdleAfterDays > 0 {
			go func() {
				pruneIdleSessions(ctx, docsRepo, transcriptRepo, sessCfg.PruneIdleAfterDays)
			}()
		}
	}

	<-ctx.Done()
	if controlServices != nil && controlServices.tasks.runner != nil {
		shutdownCtx, cancelTaskRuns := context.WithTimeout(context.Background(), 30*time.Second)
		controlServices.tasks.runner.Shutdown(shutdownCtx)
		cancelTaskRuns()
	}
	agentRunMu.Lock()
	agentRunClosed = true
	agentRunMu.Unlock()
	agentRunsDone := make(chan struct{})
	go func() {
		agentRunWG.Wait()
		close(agentRunsDone)
	}()
	select {
	case <-agentRunsDone:
	case <-time.After(30 * time.Second):
		log.Printf("daemon shutdown: still waiting for agent runs to stop after context cancellation")
		<-agentRunsDone
	}
	if pluginServiceMgr != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = pluginServiceMgr.StopAll(stopCtx)
		cancel()
	}
	if openClawHost != nil {
		if err := openClawHost.Close(); err != nil {
			log.Printf("openclaw host close warning: %v", err)
		}
	}
	shutdownEmitter.Emit("daemon stopping")
	select {
	case <-heartbeatDone:
	case <-time.After(250 * time.Millisecond):
	}
	log.Println("metiqd shutting down")
}

func configuredOpenClawPluginPaths(cfg state.ConfigDoc) []string {
	rawExt, _ := cfg.Extra["extensions"].(map[string]any)
	rawEntries, _ := rawExt["entries"].(map[string]any)
	if len(rawEntries) == 0 {
		return nil
	}
	paths := make([]string, 0, len(rawEntries))
	for _, raw := range rawEntries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if enabled, ok := entry["enabled"].(bool); ok && !enabled {
			continue
		}
		pluginType, _ := entry["plugin_type"].(string)
		if !strings.EqualFold(strings.TrimSpace(pluginType), "openclaw") {
			continue
		}
		installPath, _ := entry["install_path"].(string)
		if trimmed := strings.TrimSpace(installPath); trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	sort.Strings(paths)
	return paths
}

func initEnvelopeCodec(signer nostr.Keyer) (*secure.MutableSelfEnvelopeCodec, error) {
	return secure.NewMutableSelfEnvelopeCodec(signer, true)
}

func ensureRuntimeConfig(ctx context.Context, repo *state.DocsRepository, relays []string, adminPubKey string, preferred *state.ConfigDoc, preferredDefinesControl bool) (state.ConfigDoc, error) {
	if preferred != nil {
		next, err := normalizeAndValidateRuntimeConfigDoc(*preferred)
		if err != nil {
			return state.ConfigDoc{}, err
		}
		current, currentErr := repo.GetConfig(ctx)
		if currentErr == nil {
			if current, err := normalizeAndValidateRuntimeConfigDoc(current); err == nil && current.Hash() == next.Hash() {
				return next, nil
			}
		} else if errors.Is(currentErr, state.ErrNotFound) {
			if !preferredDefinesControl {
				seed := defaultRuntimeConfigDoc(relays, adminPubKey)
				next.Control = seed.Control
				next = policy.NormalizeConfig(next)
			}
		} else {
			return state.ConfigDoc{}, currentErr
		}
		if _, err := repo.PutConfig(ctx, next); err != nil {
			return state.ConfigDoc{}, err
		}
		return next, nil
	}

	doc, err := repo.GetConfig(ctx)
	if err == nil {
		return normalizeAndValidateRuntimeConfigDoc(doc)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.ConfigDoc{}, err
	}

	fallback := defaultRuntimeConfigDoc(relays, adminPubKey)
	if _, err := repo.PutConfig(ctx, fallback); err != nil {
		return state.ConfigDoc{}, err
	}
	return normalizeAndValidateRuntimeConfigDoc(fallback)
}

func defaultRuntimeConfigDoc(relays []string, adminPubKey string) state.ConfigDoc {
	return state.ConfigDoc{
		Version: 1,
		DM: state.DMPolicy{
			Policy: policy.DMPolicyPairing,
		},
		Relays:  state.RelayPolicy{Read: relays, Write: relays},
		Storage: state.StorageConfig{Encrypt: state.BoolPtr(true)},
		Control: state.ControlPolicy{
			RequireAuth:        true,
			AllowUnauthMethods: []string{"supportedmethods"},
			Admins: []state.ControlAdmin{{
				PubKey:  adminPubKey,
				Methods: []string{"*"},
			}},
		},
	}
}

func configFileDeclaresTopLevelKey(path string, key string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return false
	}
	_, ok := top[strings.TrimSpace(key)]
	return ok
}

func normalizeAndValidateRuntimeConfigDoc(doc state.ConfigDoc) (state.ConfigDoc, error) {
	doc = policy.NormalizeConfig(doc)
	if errs := config.ValidateConfigDoc(doc); len(errs) > 0 {
		return state.ConfigDoc{}, errs[0]
	}
	if err := policy.ValidateConfig(doc); err != nil {
		return state.ConfigDoc{}, err
	}
	return doc, nil
}

func ensureIngestCheckpoint(ctx context.Context, repo *state.DocsRepository) (state.CheckpointDoc, error) {
	doc, err := repo.GetCheckpoint(ctx, "dm_ingest")
	if err == nil {
		if doc.Name == "" {
			doc.Name = "dm_ingest"
		}
		return doc, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.CheckpointDoc{}, err
	}
	fallback := state.CheckpointDoc{Version: 1, Name: "dm_ingest"}
	if _, err := repo.PutCheckpoint(ctx, "dm_ingest", fallback); err != nil {
		return state.CheckpointDoc{}, err
	}
	return fallback, nil
}

func adminControlMutationCaller(ctx context.Context, fallback string) string {
	if caller := admin.CallerPubKeyFromContext(ctx); caller != "" {
		return caller
	}
	return strings.TrimSpace(fallback)
}

func dispatchAdminDelegatedControlCall(
	ctx context.Context,
	fromPubKey string,
	method string,
	params json.RawMessage,
	dmBus nostruntime.DMTransport,
	controlBus *nostruntime.ControlRPCBus,
	chatCancels *chatAbortRegistry,
	usageState *usageTracker,
	logBuffer *runtimeLogBuffer,
	channelState *channelRuntimeState,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	memoryIndex memory.Store,
	configState *runtimeConfigStore,
	tools *agent.ToolRegistry,
	pluginMgr *pluginmanager.GojaPluginManager,
	startedAt time.Time,
) (any, int, error) {
	res, err := handleControlRPCRequest(ctx, nostruntime.ControlRPCInbound{
		FromPubKey:    fromPubKey,
		Method:        method,
		Params:        params,
		Authenticated: true,
	}, dmBus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
	if err != nil {
		return nil, controlAdminMethodStatus(err), err
	}
	return res.Result, http.StatusOK, nil
}

func dispatchAdminControlConfigMutation(
	ctx context.Context,
	fromPubKey string,
	method string,
	params any,
	dmBus nostruntime.DMTransport,
	controlBus *nostruntime.ControlRPCBus,
	chatCancels *chatAbortRegistry,
	usageState *usageTracker,
	logBuffer *runtimeLogBuffer,
	channelState *channelRuntimeState,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	memoryIndex memory.Store,
	configState *runtimeConfigStore,
	tools *agent.ToolRegistry,
	pluginMgr *pluginmanager.GojaPluginManager,
	startedAt time.Time,
) (map[string]any, int, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	res, err := handleControlRPCRequest(ctx, nostruntime.ControlRPCInbound{
		FromPubKey:    fromPubKey,
		Method:        method,
		Params:        raw,
		Authenticated: true,
	}, dmBus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
	if err != nil {
		return nil, controlConfigMutationStatus(err), err
	}
	out, ok := res.Result.(map[string]any)
	if ok {
		return out, http.StatusOK, nil
	}
	encoded, err := json.Marshal(res.Result)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("marshal %s admin parity result: %w", method, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("decode %s admin parity result: %w", method, err)
	}
	return decoded, http.StatusOK, nil
}

func controlAdminMethodStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, state.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, methods.ErrConfigConflict) {
		return http.StatusConflict
	}
	var precondition *methods.PreconditionConflictError
	if errors.As(err, &precondition) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func controlConfigMutationStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, methods.ErrConfigConflict) {
		return http.StatusConflict
	}
	var precondition *methods.PreconditionConflictError
	if errors.As(err, &precondition) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func checkpointSinceUnix(lastUnix int64) int64 {
	// Always look back at least DMReplayWindowDefault (30min) so that agents
	// reconstruct recent conversation context after a restart, even if the
	// checkpoint is current.  The AlreadyProcessed gate prevents re-replies
	// to messages that were handled before the restart.
	//
	// See runtime/replay.go for the full replay policy documentation.
	floor := nostruntime.ResubscribeSince(nostruntime.DMReplayWindowDefault)
	if lastUnix <= 0 {
		return floor
	}
	since := lastUnix - 120
	if since > floor {
		since = floor
	}
	if since < 0 {
		return 0
	}
	return since
}

func handleControlRPCRequest(
	ctx context.Context,
	in nostruntime.ControlRPCInbound,
	dmBus nostruntime.DMTransport,
	controlBus *nostruntime.ControlRPCBus,
	chatCancels *chatAbortRegistry,
	usageState *usageTracker,
	logBuffer *runtimeLogBuffer,
	channelState *channelRuntimeState,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	memoryIndex memory.Store,
	configState *runtimeConfigStore,
	tools *agent.ToolRegistry,
	pluginMgr *pluginmanager.GojaPluginManager,
	startedAt time.Time,
) (nostruntime.ControlRPCResult, error) {
	// Ensure controlServices is safe for field access (may be nil in tests).
	svc := controlServices
	if svc == nil {
		svc = &daemonServices{
			emitter:   controlWsEmitter,
			emitterMu: &controlWsEmitterMu,
			session: sessionServices{
				sessionStore:      controlSessionStore,
				toolRegistry:      controlToolRegistry,
				agentJobs:         controlAgentJobs,
				sessionRouter:     controlSessionRouter,
				agentRegistry:     controlAgentRegistry,
				agentRuntime:      controlAgentRuntime,
				subagents:         controlSubagents,
				sessionTurns:      controlSessionTurns,
				ops:               controlOps,
				cronJobs:          controlCronJobs,
				execApprovals:     controlExecApprovals,
				wizards:           controlWizards,
				nodeInvocations:   controlNodeInvocations,
				sessionMemRuntime: controlSessionMemoryRuntime,
			},
			relay: relayPolicyServices{
				acpPeers:      controlACPPeers,
				acpDispatcher: controlACPDispatcher,
			},
			handlers: handlerServices{
				mcpOps:          controlMCPOps,
				pairingConfigMu: &controlPairingConfigMu,
			},
			runtimeConfig: controlRuntimeConfig,
		}
	}
	deps := controlRPCDeps{
		dmBus:             dmBus,
		controlBus:        controlBus,
		chatCancels:       chatCancels,
		steeringMailboxes: svc.session.steeringMailboxes,
		usageState:        usageState,
		logBuffer:         logBuffer,
		channelState:      channelState,
		docsRepo:          docsRepo,
		taskService:       svc.tasks.service,
		transcriptRepo:    transcriptRepo,
		memoryIndex:       memoryIndex,
		configState:       configState,
		tools:             tools,
		pluginMgr:         pluginMgr,
		startedAt:         startedAt,
		bootstrapPath:     svc.handlers.bootstrapPath,

		sessionStore:     svc.session.sessionStore,
		mediaTranscriber: svc.handlers.mediaTranscriber,
		toolRegistry:     svc.session.toolRegistry,
		agentJobs:        svc.session.agentJobs,
		sessionRouter:    svc.session.sessionRouter,
		agentRegistry:    svc.session.agentRegistry,
		agentRuntime:     svc.session.agentRuntime,

		sessionMemoryRuntime: svc.session.sessionMemRuntime,
		acpPeers:             svc.relay.acpPeers,
		acpDispatcher:        svc.relay.acpDispatcher,

		services: controlServices,

		ops:             svc.session.ops,
		cronJobs:        svc.session.cronJobs,
		execApprovals:   svc.session.execApprovals,
		wizards:         svc.session.wizards,
		contextEngine:   svc.session.contextEngine,
		mcpOps:          svc.handlers.mcpOps,
		mcpAuth:         svc.handlers.mcpAuth,
		nodeInvocations: svc.session.nodeInvocations,
		nodePending:     svc.session.nodePending,
		canvasHost:      svc.handlers.canvasHost,
		channels:        svc.relay.channels,
		nostrHub:        svc.relay.hub,
		keyer:           svc.relay.keyer,
	}
	if svc.handlers.hooksMgr != nil {
		deps.hooksMgr = svc.handlers.hooksMgr
		deps.hooksMgrFull = svc.handlers.hooksMgr
	}
	return newControlRPCHandler(deps).Handle(ctx, in)
}

// handleACPMessage processes an incoming ACP control message from a registered peer.
// For task messages, it runs the agent and sends a result DM back to the sender.
func handleACPMessage(
	ctx context.Context,
	msg acppkg.Message,
	fromPubKey string,
	dm nostruntime.InboundDM,
	agentReg *agent.AgentRuntimeRegistry,
	sessRouter *agent.AgentSessionRouter,
	tools *agent.ToolRegistry,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
) error {
	switch msg.ACPType {
	case "task":
		sendResult := func(resultMsg acppkg.Message) error {
			payload, marshalErr := json.Marshal(resultMsg)
			if marshalErr != nil {
				return fmt.Errorf("acp result marshal: %w", marshalErr)
			}
			replyTo := fromPubKey
			if msg.Payload != nil {
				if v, ok := msg.Payload["reply_to"].(string); ok && strings.TrimSpace(v) != "" {
					replyTo = strings.TrimSpace(v)
				}
			}
			if replyTo != "" && replyTo != fromPubKey {
				cfg := state.ConfigDoc{}
				if controlServices.runtimeConfig != nil {
					cfg = controlServices.runtimeConfig.Get()
				}
				bus, scheme, transportErr := resolveACPDMTransport(cfg, replyTo)
				if transportErr != nil {
					return fmt.Errorf("acp result send failed to=%s task_id=%s: %w", replyTo, msg.TaskID, transportErr)
				}
				if sendErr := sendACPDMWithTransport(ctx, bus, scheme, replyTo, string(payload)); sendErr != nil {
					return fmt.Errorf("acp result send failed to=%s task_id=%s: %w", replyTo, msg.TaskID, sendErr)
				}
				return nil
			}
			if sendErr := dm.Reply(ctx, string(payload)); sendErr != nil {
				return fmt.Errorf("acp result send failed to=%s task_id=%s: %w", replyTo, msg.TaskID, sendErr)
			}
			return nil
		}
		taskPayload, err := acppkg.DecodeTaskPayload(msg.Payload)
		if err != nil {
			return sendResult(acppkg.NewResult(msg.TaskID, "", acppkg.ResultPayload{Error: fmt.Sprintf("invalid task payload: %v", err)}))
		}
		instructions := strings.TrimSpace(taskPayload.Instructions)
		if instructions == "" {
			log.Printf("acp task from=%s task_id=%s: missing instructions", fromPubKey, msg.TaskID)
			return sendResult(acppkg.NewResult(msg.TaskID, "", acppkg.ResultPayload{Error: "instructions are required"}))
		}
		log.Printf("acp task from=%s task_id=%s instructions=%q", fromPubKey, msg.TaskID, instructions)

		// Route to the assigned agent for this peer, falling back to "main".
		agentID := ""
		if sessRouter != nil {
			agentID = sessRouter.Get(fromPubKey)
		}
		agentID = defaultAgentID(agentID)
		rt := agentReg.Get(agentID)

		sessionID := "acp:" + fromPubKey
		turnStartedAt := time.Now()
		cfg := state.ConfigDoc{}
		if controlServices.runtimeConfig != nil {
			cfg = controlServices.runtimeConfig.Get()
		}
		taskTimeout := 60 * time.Second
		if taskPayload.TimeoutMS > 0 {
			taskTimeout = time.Duration(taskPayload.TimeoutMS) * time.Millisecond
		}
		processCtx, cancel := context.WithTimeout(ctx, taskTimeout)
		defer cancel()
		var result agent.TurnResult
		var historyEntryIDs []string
		var workerTask state.TaskSpec
		var workerRun state.TaskRun
		var cleanupWorkerTask func()
		procErr := withExclusiveSessionTurn(processCtx, sessionID, taskTimeout, func() (err error) {
			scopeCtx := resolveMemoryScopeContext(processCtx, cfg, docsRepo, controlServices.session.sessionStore, sessionID, agentID, taskPayload.MemoryScope)
			persistSessionMemoryScope(controlServices.session.sessionStore, sessionID, agentID, scopeCtx.Scope)
			workerTask, workerRun, cleanupWorkerTask, err = beginACPWorkerTask(processCtx, docsRepo, sessionID, fromPubKey, agentID, msg.TaskID, taskPayload, turnStartedAt)
			if err != nil {
				return fmt.Errorf("acp worker task start: %w", err)
			}
			defer cleanupWorkerTask()
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("acp worker panic: %v", r)
				}
			}()
			turnCtx := contextWithMemoryScope(processCtx, scopeCtx)
			turnCtx = contextWithACPTaskPayload(turnCtx, taskPayload)
			filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(
				turnCtx,
				cfg,
				docsRepo,
				sessionID,
				agentID,
				rt,
				tools,
				turnToolConstraints{ToolProfile: taskPayload.ToolProfile, EnabledTools: taskPayload.EnabledTools},
			)
			seedHistory := decodeACPConversationMessages(taskPayload.ContextMessages)
			historyEntryIDs = append(historyEntryIDs, persistACPContextHistory(processCtx, transcriptRepo, controlServices.session.contextEngine, sessionID, msg.TaskID, fromPubKey, taskPayload.ParentContext, seedHistory)...)
			prepared := buildAgentRunTurn(turnCtx, methods.AgentRequest{
				SessionID: sessionID,
				Message:   instructions,
			}, controlServices.session.memoryStore, scopeCtx, workspaceDirForAgent(cfg, agentID), controlServices.session.sessionStore)
			prepared.Turn.Tools = turnTools
			prepared.Turn.Executor = turnExecutor
			prepared.Turn.ToolEventSink = toolLifecyclePersistenceSink(controlServices.session.sessionStore, sessionID, toolLifecycleEmitter(runtimeEventEmitterFunc(emitControlWSEvent), agentID))
			prepared = applyPromptEnvelopeToPreparedTurn(prepared, turnPromptBuilderParams{Config: cfg, SessionID: sessionID, AgentID: agentID, Channel: "nostr", StaticSystemPrompt: prepared.Turn.StaticSystemPrompt, Context: prepared.Turn.Context, Tools: turnTools})
			prepared.Turn.TurnID = msg.TaskID
			if len(seedHistory) > 0 {
				mergedHistory := make([]agent.ConversationMessage, 0, len(prepared.Turn.History)+len(seedHistory))
				mergedHistory = append(mergedHistory, prepared.Turn.History...)
				mergedHistory = append(mergedHistory, seedHistory...)
				prepared.Turn.History = mergedHistory
			}
			result, err = filteredRuntime.ProcessTurn(turnCtx, prepared.Turn)
			if err != nil {
				if partial, ok := agent.PartialTurnResult(err); ok {
					historyEntryIDs = append(historyEntryIDs, persistACPTurnHistory(processCtx, transcriptRepo, controlServices.session.contextEngine, sessionID, msg.TaskID, fromPubKey, taskPayload.ParentContext, partial.HistoryDelta, turnResultMetadataPtr(result, err))...)
				}
				return err
			}
			if prepared.MemoryRecallSample != nil {
				prepared.MemoryRecallSample.TaskID = firstNonEmptyTrimmed(workerTask.TaskID, msg.TaskID)
				prepared.MemoryRecallSample.RunID = strings.TrimSpace(workerRun.RunID)
				prepared.MemoryRecallSample.GoalID = strings.TrimSpace(workerTask.GoalID)
			}
			commitMemoryRecallArtifacts(controlServices.session.sessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
			delta := result.HistoryDelta
			if len(delta) == 0 && strings.TrimSpace(result.Text) != "" {
				delta = []agent.ConversationMessage{{Role: "assistant", Content: strings.TrimSpace(result.Text)}}
			}
			historyEntryIDs = append(historyEntryIDs, persistACPTurnHistory(processCtx, transcriptRepo, controlServices.session.contextEngine, sessionID, msg.TaskID, fromPubKey, taskPayload.ParentContext, delta, turnResultMetadataPtr(result, nil))...)
			return err
		})
		if controlServices.session.sessionStore != nil && (result.Usage.InputTokens > 0 || result.Usage.OutputTokens > 0) {
			_ = controlServices.session.sessionStore.AddTokens(sessionID, result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheReadTokens, result.Usage.CacheCreationTokens)
		}
		turnTelemetry := buildTurnTelemetry(msg.TaskID, turnStartedAt, time.Now(), result, procErr, false, "", "", "")
		turnTelemetry.Trace = agent.TraceContext{
			GoalID:       strings.TrimSpace(workerTask.GoalID),
			TaskID:       firstNonEmptyTrimmed(workerTask.TaskID, msg.TaskID),
			RunID:        strings.TrimSpace(workerRun.RunID),
			ParentTaskID: strings.TrimSpace(workerTask.ParentTaskID),
			ParentRunID:  strings.TrimSpace(workerRun.ParentRunID),
		}

		var parentContext *acppkg.ParentContext
		if taskPayload.ParentContext != nil {
			parentContext = &acppkg.ParentContext{
				SessionID: strings.TrimSpace(taskPayload.ParentContext.SessionID),
				AgentID:   strings.TrimSpace(taskPayload.ParentContext.AgentID),
			}
		}
		resultRef := state.TaskResultRef{Kind: "acp_result", ID: msg.TaskID}
		if len(historyEntryIDs) > 0 {
			resultRef = state.TaskResultRef{Kind: "transcript_entry", ID: historyEntryIDs[len(historyEntryIDs)-1]}
		}
		if controlServices.session.sessionStore != nil {
			if err := controlServices.session.sessionStore.RecordTurn(sessionID, state.TurnTelemetry{
				TurnID:         turnTelemetry.TurnID,
				TaskID:         firstNonEmptyTrimmed(workerTask.TaskID, msg.TaskID),
				RunID:          strings.TrimSpace(workerRun.RunID),
				ParentTaskID:   strings.TrimSpace(workerTask.ParentTaskID),
				ParentRunID:    strings.TrimSpace(workerRun.ParentRunID),
				StartedAtMS:    turnTelemetry.StartedAtMS,
				EndedAtMS:      turnTelemetry.EndedAtMS,
				DurationMS:     turnTelemetry.DurationMS,
				Outcome:        string(turnTelemetry.Outcome),
				StopReason:     string(turnTelemetry.StopReason),
				LoopBlocked:    turnTelemetry.LoopBlocked,
				Error:          turnTelemetry.Error,
				FallbackUsed:   turnTelemetry.FallbackUsed,
				FallbackFrom:   turnTelemetry.FallbackFrom,
				FallbackTo:     turnTelemetry.FallbackTo,
				FallbackReason: turnTelemetry.FallbackReason,
				InputTokens:    turnTelemetry.Usage.InputTokens,
				OutputTokens:   turnTelemetry.Usage.OutputTokens,
				Result:         resultRef,
			}); err != nil {
				log.Printf("session store turn telemetry failed session=%s: %v", sessionID, err)
			}
		}
		emitControlWSEvent(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, sessionID, turnTelemetry))
		if strings.TrimSpace(workerTask.TaskID) != "" && strings.TrimSpace(workerRun.RunID) != "" {
			persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := finishACPWorkerTaskDocs(persistCtx, docsRepo, sessionID, workerTask, workerRun, resultRef, turnResultMetadataPtr(result, procErr), procErr, historyEntryIDs); err != nil {
				log.Printf("acp worker task completion persist failed session=%s task_id=%s run_id=%s err=%v", sessionID, workerTask.TaskID, workerRun.RunID, err)
			}
			persistCancel()
		}
		worker := &acppkg.WorkerMetadata{
			TaskID:          firstNonEmptyTrimmed(workerTask.TaskID, msg.TaskID),
			RunID:           workerRun.RunID,
			SessionID:       sessionID,
			AgentID:         agentID,
			ParentTaskID:    workerTask.ParentTaskID,
			ParentRunID:     workerRun.ParentRunID,
			ParentContext:   parentContext,
			HistoryEntryIDs: cloneACPStringSlice(historyEntryIDs),
			Result:          resultRef,
			TurnResult:      turnResultMetadataPtr(result, procErr),
		}
		senderPubKey := ""
		if controlServices != nil && controlServices.relay.dmBusMu != nil {
			controlServices.relay.dmBusMu.RLock()
			if controlServices.relay.dmBus != nil && *controlServices.relay.dmBus != nil {
				senderPubKey = (*controlServices.relay.dmBus).PublicKey()
			}
			controlServices.relay.dmBusMu.RUnlock()
		}

		// Build and send result DM back to the sender.
		var resultMsg acppkg.Message
		if procErr != nil {
			resultMsg = acppkg.NewResult(msg.TaskID, senderPubKey, acppkg.ResultPayload{
				Error:       procErr.Error(),
				CompletedAt: time.Now().Unix(),
				Worker:      worker,
			})
		} else {
			resultMsg = acppkg.NewResult(msg.TaskID, senderPubKey, acppkg.ResultPayload{
				Text:        result.Text,
				TokensUsed:  int(result.Usage.InputTokens + result.Usage.OutputTokens),
				CompletedAt: time.Now().Unix(),
				Worker:      worker,
			})
		}

		return sendResult(resultMsg)

	case "result":
		// Incoming result from a peer for a previously dispatched task.
		taskID := msg.TaskID
		resultPayload, err := acppkg.DecodeResultPayload(msg.Payload)
		if err != nil {
			return fmt.Errorf("acp result decode task_id=%s: %w", taskID, err)
		}
		text := resultPayload.Text
		errStr := resultPayload.Error
		log.Printf("acp result from=%s task_id=%s ok=%v text=%q err=%q", fromPubKey, taskID, errStr == "", text, errStr)
		// Deliver to any waiting Dispatch() caller.
		if controlServices.relay.acpDispatcher != nil {
			controlServices.relay.acpDispatcher.Deliver(acppkg.TaskResult{
				TaskID:       taskID,
				Text:         text,
				Error:        errStr,
				SenderPubKey: strings.TrimSpace(msg.SenderPubKey),
				Worker:       cloneACPWorkerMetadata(resultPayload.Worker),
				TokensUsed:   resultPayload.TokensUsed,
				CompletedAt:  resultPayload.CompletedAt,
			})
		}
		return nil

	case "ping":
		// Liveness probe: respond with a pong.
		pingPayload, err := acppkg.DecodePingPayload(msg.Payload)
		if err != nil {
			return fmt.Errorf("acp ping decode task_id=%s: %w", msg.TaskID, err)
		}
		pong := acppkg.NewPong(msg.TaskID, "", acppkg.PongPayload{Nonce: pingPayload.Nonce})
		payload, _ := json.Marshal(pong)
		if sendErr := dm.Reply(ctx, string(payload)); sendErr != nil {
			log.Printf("acp pong send failed to=%s err=%v", fromPubKey, sendErr)
		}
		return nil

	case "pong":
		pongPayload, err := acppkg.DecodePongPayload(msg.Payload)
		if err != nil {
			return fmt.Errorf("acp pong decode task_id=%s: %w", msg.TaskID, err)
		}
		log.Printf("acp pong from=%s task_id=%s nonce=%q", fromPubKey, msg.TaskID, pongPayload.Nonce)
		if controlServices.relay.acpDispatcher != nil {
			controlServices.relay.acpDispatcher.Deliver(acppkg.TaskResult{
				TaskID:       msg.TaskID,
				Text:         "pong",
				SenderPubKey: strings.TrimSpace(msg.SenderPubKey),
				CompletedAt:  time.Now().Unix(),
			})
		}
		return nil

	default:
		log.Printf("acp unknown message type=%q from=%s", msg.ACPType, fromPubKey)
		return nil
	}
}

// applyAgentProfileFilter resolves the runtime tool contract for an
// agent/session and returns a filtered Runtime. Configured enabled_tools are
// intersected with the profile-derived allowlist through the shared assembly
// path.
func applyAgentProfileFilter(ctx context.Context, rt agent.Runtime, sessionID string, cfg state.ConfigDoc, docsRepo *state.DocsRepository) agent.Runtime {
	agentID := ""
	if controlServices != nil && controlServices.session.sessionRouter != nil {
		agentID = controlServices.session.sessionRouter.Get(sessionID)
	}
	return applyAgentProfileFilterForAgent(ctx, rt, agentID, cfg, docsRepo)
}

// scheduleRestartIfNeeded compares old and next ConfigDoc.  If the change
// requires a daemon restart (model, providers, plugins changed) it sends a
// signal to the restart scheduler goroutine and returns true.
// delayMS is the caller-requested delay before restart; defaults to 500ms if zero.
func scheduleRestartIfNeeded(old, next state.ConfigDoc, delayMS int) (pending bool) {
	if controlServices == nil || !policy.ConfigChangedNeedsRestart(old, next) {
		return false
	}
	if delayMS <= 0 {
		delayMS = 500 // default grace period
	}
	select {
	case controlServices.restartCh <- delayMS:
	default:
		// already queued; ignore duplicate
	}
	return true
}

func setControlWSEmitter(emitter gatewayws.EventEmitter) {
	if emitter == nil {
		emitter = gatewayws.NoopEmitter{}
	}
	if controlServices != nil {
		controlServices.emitterMu.Lock()
		controlServices.emitter = emitter
		controlServices.emitterMu.Unlock()
	}
	// Always update the package-level globals so tests (and the fallback svc
	// built in handleControlRPCRequest) see the emitter even when
	// controlServices has not been initialised yet.
	controlWsEmitterMu.Lock()
	controlWsEmitter = emitter
	controlWsEmitterMu.Unlock()
}

// autoResolveProviderOverride infers a ProviderOverride from the model name and
// the configured providers map.  It enables zero-config usage: a model named
// "claude-3-5-sonnet-20241022" will automatically pick up an "anthropic" entry
// from providers[], or fall back to env vars (handled by BuildRuntimeForModel).
// refreshKeyRings rebuilds the ProviderKeyRingRegistry from the current
// provider config.  It should be called whenever the config changes.
func refreshKeyRings(providers map[string]state.ProviderEntry) {
	if controlServices == nil || controlServices.handlers.keyRings == nil {
		return
	}
	rings := make(map[string]*agent.KeyRing, len(providers))
	for providerID, pe := range providers {
		// Build the full key pool: APIKeys list + single APIKey if non-empty.
		keys := make([]string, 0, len(pe.APIKeys)+1)
		keys = append(keys, pe.APIKeys...)
		if pe.APIKey != "" {
			keys = append(keys, pe.APIKey)
		}
		if len(keys) > 0 {
			rings[providerID] = agent.NewKeyRing(keys)
		}
	}
	controlServices.handlers.keyRings.Replace(rings)
}

func applyRuntimeConfigSideEffects(cfg state.ConfigDoc) {
	if controlServices == nil {
		// Fallback: apply envelope codec side effect using the global.
		if controlStateEnvelopeCodec != nil {
			controlStateEnvelopeCodec.SetEncrypt(cfg.StorageEncryptEnabled())
		}
		return
	}
	if controlServices.handlers.stateEnvelopeCodec != nil {
		controlServices.handlers.stateEnvelopeCodec.SetEncrypt(cfg.StorageEncryptEnabled())
	}
	refreshKeyRings(cfg.Providers)
	controlServices.applyRuntimeRelayPolicy(nil, nil, cfg)
	applyCapabilityRuntimeState(cfg)
	if controlServices.session.ops != nil {
		controlServices.session.ops.SyncHeartbeatConfig(cfg.Heartbeat)
	}
	// Update profile publisher with any changes to extra.profile.
	if controlProfilePublisher != nil {
		if newProfile := nostruntime.ExtractProfileFromExtra(cfg.Extra); newProfile != nil {
			controlProfilePublisher.UpdateProfile(newProfile)
		}
		// Also update relay list in case relays changed.
		allRelays := nostruntime.MergeRelayLists(cfg.Relays.Read, cfg.Relays.Write)
		if len(allRelays) > 0 {
			controlProfilePublisher.UpdateRelays(allRelays)
		}
	}
}

var writeRuntimeConfigFile = config.WriteConfigFile

func persistRuntimeConfigFile(doc state.ConfigDoc) error {
	if controlServices == nil || strings.TrimSpace(controlServices.handlers.configFilePath) == "" {
		return nil
	}
	return writeRuntimeConfigFile(controlServices.handlers.configFilePath, doc)
}

func providerOverrideForEntry(name string, pe state.ProviderEntry) agent.ProviderOverride {
	apiKey := pe.APIKey
	if controlServices != nil && controlServices.handlers.keyRings != nil {
		if ring := controlServices.handlers.keyRings.Get(name); ring != nil && ring.Len() > 0 {
			if picked, ok := ring.Pick(); ok && picked != "" {
				apiKey = picked
			}
		}
	}
	return agent.ProviderOverride{BaseURL: pe.BaseURL, APIKey: apiKey, Model: pe.Model, PromptCache: pe.PromptCache}
}

func autoResolveProviderOverride(model string, providers map[string]state.ProviderEntry) agent.ProviderOverride {
	if providers == nil {
		return agent.ProviderOverride{}
	}
	norm := strings.ToLower(strings.TrimSpace(model))

	// Determine which provider family the model belongs to.
	var family string
	switch {
	case norm == "anthropic" || strings.HasPrefix(norm, "claude-"):
		family = "anthropic"
	case norm == "openai" || strings.HasPrefix(norm, "gpt-") || strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-"):
		family = "openai"
	case norm == "gemini" || strings.HasPrefix(norm, "gemini-"):
		family = "google"
	case norm == "xai" || strings.HasPrefix(norm, "grok-"):
		family = "xai"
	case norm == "groq" || strings.HasPrefix(norm, "groq/"):
		family = "groq"
	case norm == "mistral" || strings.HasPrefix(norm, "mistral-"):
		family = "mistral"
	case norm == "together" || strings.HasPrefix(norm, "together/"):
		family = "together"
	case norm == "openrouter" || strings.HasPrefix(norm, "openrouter/"):
		family = "openrouter"
	default:
		// Not a well-known provider family — try to extract a custom
		// provider prefix from the model string (e.g. "lemmy-local/model.gguf"
		// → prefix "lemmy-local") and look it up in the providers map.
		if idx := strings.Index(norm, "/"); idx > 0 {
			prefix := norm[:idx]
			if pe, ok := providers[prefix]; ok {
				return providerOverrideForEntry(prefix, pe)
			}
			// Case-insensitive scan for provider keys matching the prefix.
			for key, pe := range providers {
				if strings.EqualFold(key, prefix) {
					return providerOverrideForEntry(key, pe)
				}
			}
		}
		return agent.ProviderOverride{}
	}

	// Look for an exact match first (e.g. providers["anthropic"]).
	if pe, ok := providers[family]; ok {
		return providerOverrideForEntry(family, pe)
	}
	// Also scan for any entry with a matching family prefix in its key.
	for key, pe := range providers {
		if strings.HasPrefix(strings.ToLower(key), family) {
			return providerOverrideForEntry(key, pe)
		}
	}
	return agent.ProviderOverride{}
}

type auxiliaryModelUseCase string

const (
	auxiliaryModelUseCaseHeartbeat  auxiliaryModelUseCase = "heartbeat"
	auxiliaryModelUseCaseCompaction auxiliaryModelUseCase = "compaction"
)

func resolveAgentConfigByID(cfg state.ConfigDoc, agentID string) (state.AgentConfig, bool) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = "main"
	}
	for _, agCfg := range cfg.Agents {
		if strings.TrimSpace(agCfg.ID) == agentID {
			return agCfg, true
		}
	}
	return state.AgentConfig{}, false
}

func resolveModelProviderOverride(cfg state.ConfigDoc, agCfg state.AgentConfig, model string) agent.ProviderOverride {
	model = strings.TrimSpace(model)
	var override agent.ProviderOverride
	if strings.Contains(model, "/") {
		override = autoResolveProviderOverride(model, cfg.Providers)
	} else if provName := strings.TrimSpace(agCfg.Provider); provName != "" {
		if pe, ok := cfg.Providers[provName]; ok {
			override = providerOverrideForEntry(provName, pe)
		}
	}
	if override.BaseURL == "" && override.APIKey == "" && override.Model == "" {
		override = autoResolveProviderOverride(model, cfg.Providers)
	}
	// Final fallback: if auto-resolution still returned nothing but the agent
	// config explicitly names a provider, use that provider entry directly.
	// This handles models with slashes (e.g. "lemmy-local/model.gguf") where
	// the prefix doesn't match a well-known family but agCfg.Provider is set.
	if override.BaseURL == "" && override.APIKey == "" && override.Model == "" {
		if provName := strings.TrimSpace(agCfg.Provider); provName != "" {
			if pe, ok := cfg.Providers[provName]; ok {
				override = providerOverrideForEntry(provName, pe)
			}
		}
	}
	override.SystemPrompt = strings.TrimSpace(agCfg.SystemPrompt)
	return override
}

func buildProviderForAgentModel(cfg state.ConfigDoc, agCfg state.AgentConfig, model string) (agent.Provider, error) {
	// Early check: if the agent explicitly names a provider, it must exist in config.
	if provName := strings.TrimSpace(agCfg.Provider); provName != "" {
		if _, ok := cfg.Providers[provName]; !ok {
			return nil, fmt.Errorf("provider %q not found in providers config — add a [providers.%s] entry with base_url pointing to your inference server", provName, provName)
		}
	}
	override := resolveModelProviderOverride(cfg, agCfg, model)
	return agent.BuildProviderWithOverride(strings.TrimSpace(model), override)
}

func buildRuntimeForAgentModel(cfg state.ConfigDoc, agCfg state.AgentConfig, model string, tools agent.ToolExecutor) (agent.Runtime, error) {
	override := resolveModelProviderOverride(cfg, agCfg, model)
	return agent.BuildRuntimeWithOverride(strings.TrimSpace(model), override, tools)
}

func resolveAuxiliaryModelForAgent(agCfg state.AgentConfig, useCase auxiliaryModelUseCase) string {
	switch useCase {
	case auxiliaryModelUseCaseHeartbeat:
		return strings.TrimSpace(agCfg.Heartbeat.Model)
	case auxiliaryModelUseCaseCompaction:
		return strings.TrimSpace(agCfg.LightModel)
	default:
		return ""
	}
}

func emitControlWSEvent(event string, payload any) {
	controlServicesMu.RLock()
	svc := controlServices
	controlServicesMu.RUnlock()
	if svc != nil && svc.emitterMu != nil {
		svc.emitterMu.RLock()
		emitter := svc.emitter
		svc.emitterMu.RUnlock()
		if emitter != nil {
			emitter.Emit(event, payload)
			return
		}
	}
	// Fallback to package-level globals (used in tests).
	controlWsEmitterMu.RLock()
	emitter := controlWsEmitter
	controlWsEmitterMu.RUnlock()
	if emitter != nil {
		emitter.Emit(event, payload)
	}
}

// preprocessAttachments processes media attachments from a chat.send request.
//   - Audio attachments are transcribed via Whisper and their transcripts are
//     appended to text as "[Transcription]: ...".
//   - PDF attachments are text-extracted via pdftotext and appended similarly.
//   - Image attachments are resolved to agent.ImageRef for multi-modal providers;
//     when a DM must be used (text-only channel), a URL reference or filename hint
//     is appended to text instead.
//
// Returns the augmented text and image refs (may be empty if no images).
func preprocessAttachments(ctx context.Context, text string, atts []methods.AttachmentInput, transcriber mediapkg.Transcriber) (string, []agent.ImageRef, error) {
	var images []agent.ImageRef
	for _, att := range atts {
		ma := mediapkg.MediaAttachment{
			Type:     att.Type,
			URL:      att.URL,
			Base64:   att.Base64,
			MimeType: att.MimeType,
			Filename: att.Filename,
		}
		switch {
		case ma.IsAudio():
			audioBytes, mimeType, err := mediapkg.FetchAudioBytes(ctx, ma)
			if err != nil {
				log.Printf("chat.send: audio fetch error: %v", err)
				text += "\n[Audio attachment: could not fetch]"
				continue
			}
			if transcriber == nil || !transcriber.Configured() {
				text += "\n[Audio attachment: transcription not configured]"
				continue
			}
			transcript, err := transcriber.Transcribe(ctx, audioBytes, mimeType)
			if err != nil {
				log.Printf("chat.send: audio transcription error: %v", err)
				text += "\n[Audio attachment: transcription failed]"
				continue
			}
			if transcript != "" {
				text += "\n[Transcription]: " + transcript
			}

		case ma.IsPDF():
			pdfBytes, err := mediapkg.FetchPDFBytes(ctx, ma)
			if err != nil {
				log.Printf("chat.send: pdf fetch error: %v", err)
				text += "\n[PDF attachment: could not fetch]"
				continue
			}
			extracted, err := mediapkg.ExtractPDFText(ctx, pdfBytes)
			if err != nil {
				// pdftotext may not be installed; fall back to a note.
				log.Printf("chat.send: pdf extract error: %v", err)
				text += "\n[PDF attachment: text extraction failed]"
				continue
			}
			if extracted != "" {
				text += "\n[PDF content]:\n" + extracted
			}

		case ma.IsImage():
			ref, err := mediapkg.ResolveImage(ma)
			if err != nil {
				log.Printf("chat.send: image resolve error: %v", err)
				text += "\n" + mediapkg.InlineImageText(ma)
				continue
			}
			images = append(images, agent.ImageRef{
				URL:      ref.URL,
				Base64:   ref.Base64,
				MimeType: ref.MimeType,
			})
			// Also inline a text hint so text-only DM recipients get context.
			text += "\n" + mediapkg.InlineImageText(ma)
		}
	}
	return strings.TrimSpace(text), images, nil
}

// sendControlDM sends a DM via the active DM transport (NIP-17 or NIP-04).
// It is best-effort: errors are logged, not returned.
func sendControlDM(ctx context.Context, toPubKey, text string) {
	if controlServices == nil || controlServices.relay.dmBusMu == nil || controlServices.relay.dmBus == nil {
		return
	}
	controlServices.relay.dmBusMu.RLock()
	bus := *controlServices.relay.dmBus
	controlServices.relay.dmBusMu.RUnlock()
	if bus == nil {
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := bus.SendDM(sendCtx, toPubKey, text); err != nil {
		log.Printf("sendControlDM to=%s err=%v", toPubKey, err)
	}
}

func mapGatewayWSError(err error) *gatewayprotocol.ErrorShape {
	if err == nil {
		return nil
	}
	var conflict *methods.PreconditionConflictError
	if errors.As(err, &conflict) {
		return gatewayprotocol.NewError(gatewayprotocol.ErrorCodeInvalidRequest, err.Error(), map[string]any{
			"resource":         conflict.Resource,
			"expected_version": conflict.ExpectedVersion,
			"current_version":  conflict.CurrentVersion,
			"expected_event":   conflict.ExpectedEvent,
			"current_event":    conflict.CurrentEvent,
		})
	}
	if errors.Is(err, state.ErrNotFound) {
		return gatewayprotocol.NewError(gatewayprotocol.ErrorCodeInvalidRequest, "not found", nil)
	}
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	if strings.HasPrefix(lower, "unknown method") || strings.Contains(lower, "unknown agent") || strings.Contains(lower, "not found") || strings.Contains(lower, "invalid") || strings.Contains(lower, "required") {
		return gatewayprotocol.NewError(gatewayprotocol.ErrorCodeInvalidRequest, msg, nil)
	}
	if strings.Contains(lower, "forbidden") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "not linked") {
		return gatewayprotocol.NewError(gatewayprotocol.ErrorCodeNotLinked, msg, nil)
	}
	return gatewayprotocol.NewError(gatewayprotocol.ErrorCodeUnavailable, msg, nil)
}

func joinPromptSections(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return strings.Join(filtered, "\n\n")
}

func persistToolTraces(
	ctx context.Context,
	transcriptRepo *state.TranscriptRepository,
	sessionID string,
	requestEventID string,
	traces []agent.ToolTrace,
) error {
	if len(traces) == 0 {
		return nil
	}
	const maxMetaFieldRunes = 4096
	nowUnix := time.Now().Unix()
	var firstErr error
	for i, trace := range traces {
		argsJSON, err := json.Marshal(trace.Call.Args)
		if err != nil {
			argsJSON = []byte(`"<unmarshalable>"`)
		}
		resultPreview := truncateRunes(trace.Result, maxMetaFieldRunes)
		errorPreview := truncateRunes(trace.Error, maxMetaFieldRunes)
		meta := map[string]any{
			"request_event_id": requestEventID,
			"tool_name":        trace.Call.Name,
			"tool_args":        truncateRunes(string(argsJSON), maxMetaFieldRunes),
			"tool_result":      resultPreview,
			"tool_error":       errorPreview,
			"trace_index":      i,
		}
		text := fmt.Sprintf("tool=%s", trace.Call.Name)
		if errorPreview != "" {
			text = fmt.Sprintf("tool=%s error=%s", trace.Call.Name, truncateRunes(errorPreview, 300))
		} else if resultPreview != "" {
			text = fmt.Sprintf("tool=%s result=%s", trace.Call.Name, truncateRunes(resultPreview, 300))
		}
		safeToolName := strings.ReplaceAll(trace.Call.Name, ":", "_")
		entry := state.TranscriptEntryDoc{
			Version:   1,
			SessionID: sessionID,
			EntryID:   fmt.Sprintf("tool:%s:%02d:%s", requestEventID, i, safeToolName),
			Role:      "system",
			Text:      text,
			Unix:      nowUnix,
			Meta:      meta,
		}
		if _, err := transcriptRepo.PutEntry(ctx, entry); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func mediaGenerationOutputDir(cfg state.ConfigDoc, kind string) string {
	workspaceDir := workspace.ResolveWorkspaceDir(cfg, "")
	root := ""
	if extra, ok := cfg.Extra["media_generation"].(map[string]any); ok {
		if v, ok := extra["output_dir"].(string); ok {
			root = strings.TrimSpace(v)
		}
	}
	if root == "" {
		root = filepath.Join(workspaceDir, "generated-media")
	} else if !filepath.IsAbs(root) {
		root = filepath.Join(workspaceDir, root)
	}
	return filepath.Join(root, kind)
}

func mediaGenerationDuration(cfg state.ConfigDoc, key string, fallback time.Duration) time.Duration {
	if extra, ok := cfg.Extra["media_generation"].(map[string]any); ok {
		if v, ok := extra[key]; ok {
			switch t := v.(type) {
			case int:
				if t > 0 {
					return time.Duration(t) * time.Millisecond
				}
			case int64:
				if t > 0 {
					return time.Duration(t) * time.Millisecond
				}
			case float64:
				if t > 0 {
					return time.Duration(t) * time.Millisecond
				}
			case string:
				if d, err := time.ParseDuration(t); err == nil && d > 0 {
					return d
				}
				if ms, err := strconv.Atoi(t); err == nil && ms > 0 {
					return time.Duration(ms) * time.Millisecond
				}
			}
		}
	}
	return fallback
}
