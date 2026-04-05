package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
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
	browserpkg "metiq/internal/browser"
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
	mcppkg "metiq/internal/mcp"
	mediapkg "metiq/internal/media"
	"metiq/internal/memory"
	"metiq/internal/nostr/dvm"
	"metiq/internal/nostr/events"
	"metiq/internal/nostr/nip38"
	"metiq/internal/nostr/nip51"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/nostr/secure"
	"metiq/internal/plugins/installer"
	pluginmanager "metiq/internal/plugins/manager"
	"metiq/internal/policy"
	secretspkg "metiq/internal/secrets"
	skillspkg "metiq/internal/skills"
	"metiq/internal/social"
	"metiq/internal/store/state"
	ttspkg "metiq/internal/tts"
	"metiq/internal/update"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/agent/toolloop"
	ctxengine "metiq/internal/context"
	exportpkg "metiq/internal/export"
	metricspkg "metiq/internal/metrics"
	"metiq/internal/plugins/sdk"
	ratelimitpkg "metiq/internal/ratelimit"
	"metiq/internal/sandbox"
	securitypkg "metiq/internal/security"
	"metiq/internal/webui"

	// Built-in channel extensions. Importing these packages registers their
	// ChannelPlugin implementations with the global channel plugin registry.
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

// version is set at build time via -ldflags "-X main.version=<tag>".
// It defaults to "0.0.0-dev" for local builds.
var version = "0.0.0-dev"

// controlUpdateChecker is the shared version checker; initialised in main().
var controlUpdateChecker *update.Checker

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
	controlNodePending          *nodepending.Store
	controlCronJobs             *cronRegistry
	controlSessionStore         *state.SessionStore
	controlSessionMemoryRuntime *sessionMemoryRuntime
	controlDocsRepo             *state.DocsRepository
	controlMemoryStore          memory.Store
	controlExecApprovals        *execApprovalsRegistry
	controlWizards              *wizardRegistry
	controlOps                  *operationsRegistry
	controlAgentRegistry        *agent.AgentRuntimeRegistry
	controlSessionRouter        *agent.AgentSessionRouter
	controlChannels             *channels.Registry
	controlPrivateKey           string
	controlKeyer                nostr.Keyer      // always set at startup; plain mode wraps key in a keyer
	controlPresenceHeartbeat38  *nip38.Heartbeat // NIP-38 presence/status heartbeat; nil when disabled
	// controlWsEmitter forwards typed events to connected WS clients.
	// Starts as NoopEmitter; upgraded to RuntimeEmitter once the WS gateway starts.
	controlWsEmitter   gatewayws.EventEmitter = gatewayws.NoopEmitter{}
	controlWsEmitterMu sync.RWMutex

	// controlRestartCh receives a restart-delay-ms value when a config mutation
	// requires a daemon restart.  The restart goroutine drains this channel,
	// emits EventShutdown, waits for the specified delay, then cancels the main context.
	controlRestartCh = make(chan int, 4)

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
	// controlPairingConfigMu serializes pairing/device/node mutations that do
	// read-modify-write updates against the live ConfigDoc.
	controlPairingConfigMu sync.Mutex

	// controlPluginMgr is the live Goja plugin manager; nil if no plugins are loaded.
	controlPluginMgr *pluginmanager.GojaPluginManager

	// controlHooksMgr manages bundled and managed hook event handlers.
	controlHooksMgr *hookspkg.Manager

	// controlSecrets is the runtime secrets store (dotenv + env passthrough).
	controlSecrets *secretspkg.Store
	// controlMCPAuth manages remote MCP OAuth lifecycle against the live runtime.
	controlMCPAuth *mcpAuthController
	// controlMCPOps manages operator-facing MCP list/get/put/remove/test/reconnect flows.
	controlMCPOps *mcpOpsController

	// controlTTSMgr is the TTS provider manager (OpenAI, Kokoro, …).
	controlTTSMgr *ttspkg.Manager

	// controlCanvas is the canvas host that stores agent-rendered UI content.
	controlCanvas *canvas.Host

	// controlMediaTranscriber is the audio transcription provider (Whisper by default).
	controlMediaTranscriber mediapkg.Transcriber

	// controlSubagents tracks spawned child agent sessions and their ancestry.
	controlSubagents *SubagentRegistry

	// controlACPPeers is the ACP peer registry tracking known remote agent pubkeys.
	controlACPPeers *acppkg.PeerRegistry
	// controlACPDispatcher routes incoming ACP result DMs to waiting Dispatch() callers.
	controlACPDispatcher *acppkg.Dispatcher

	// controlContextEngine is the shared pluggable context engine used to ingest
	// and assemble conversation history for every agent session.
	controlContextEngine ctxengine.Engine

	// controlContextEngineName tracks which engine is active (for gateway method responses).
	controlContextEngineName string

	// controlKeyRings manages multi-key rotation pools for each provider.
	controlKeyRings *agent.ProviderKeyRingRegistry

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

func defaultBootstrapWatchSpecs(sessionID, selfPubKey string, now time.Time) []toolbuiltin.WatchSpec {
	const defaultTTLSeconds = 365 * 24 * 60 * 60
	createdAt := now.Unix()
	deadline := now.Add(time.Duration(defaultTTLSeconds) * time.Second).Unix()
	return []toolbuiltin.WatchSpec{
		{
			Name:      "gift-wrapped-dms",
			SessionID: sessionID,
			FilterRaw: map[string]any{
				"kinds": []any{float64(events.KindGiftWrap)},
				"tag_p": []any{selfPubKey},
			},
			TTLSec:    defaultTTLSeconds,
			MaxEvents: 0,
			CreatedAt: createdAt,
			Deadline:  deadline,
		},
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

func main() {
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
	controlPrivateKey = pk.Hex() // always store pubkey only
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

	shutdownEmitter := newRuntimeShutdownEmitter(emitControlWSEvent)

	// Restart scheduler: drains controlRestartCh, emits EventShutdown, then stops the daemon.
	// The supervisor (systemd / launchd / Docker restart policy) is expected to re-launch it.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case delayMS := <-controlRestartCh:
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

	pubkey := controlPrivateKey

	codec, err := initEnvelopeCodec(controlKeyer)
	if err != nil {
		log.Fatalf("init envelope codec: %v", err)
	}
	controlStateEnvelopeCodec = codec

	docsRepo := state.NewDocsRepositoryWithCodec(store, pubkey, codec)
	controlDocsRepo = docsRepo
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
	controlMemoryStore = memoryIndex

	// Social planner: manages social action plans, rate limits, and history.
	socialPlanner := social.NewPlanner(social.DefaultRateLimitConfig())

	tools := agent.NewToolRegistry()
	controlToolRegistry = tools
	var configState *runtimeConfigStore
	tools.Register("memory_search", func(ctx context.Context, args map[string]any) (string, error) {
		query := agent.ArgString(args, "query")
		if query == "" {
			return "", fmt.Errorf("memory.search requires query")
		}
		limit := agent.ArgInt(args, "limit", 5)
		if limit <= 0 {
			limit = 5
		}
		if limit > 50 {
			limit = 50
		}
		scope := memory.ScopedContextFromAgent(agent.MemoryScopeFromContext(ctx))
		results := memory.FilterByScope(memory.SearchDocs(ctx, memoryIndex, query, limit), scope)
		b, err := json.Marshal(results)
		if err != nil {
			return "", err
		}
		return string(b), nil
	})

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

	// memory_store / memory_delete: write and remove memory entries.
	tools.RegisterWithDef("memory_store", toolbuiltin.MemoryStoreTool(memoryIndex), toolbuiltin.MemoryStoreDef)
	tools.RegisterWithDef("memory_delete", toolbuiltin.MemoryDeleteTool(memoryIndex), toolbuiltin.MemoryDeleteDef)

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
	// Git tools: structured status and diff output.
	tools.RegisterWithDef("git_status", toolbuiltin.GitStatusTool, toolbuiltin.GitStatusDef)
	tools.RegisterWithDef("git_diff", toolbuiltin.GitDiffTool, toolbuiltin.GitDiffDef)
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
			cfg := configState.Get()
			if cfg.Extra != nil {
				if ws, ok := cfg.Extra["workspace"].(map[string]any); ok {
					if d, ok := ws["dir"].(string); ok && d != "" {
						return d
					}
				}
			}
			if d := os.Getenv("METIQ_WORKSPACE"); d != "" {
				return d
			}
			home, _ := os.UserHomeDir()
			return filepath.Join(home, ".metiq", "workspace")
		},
	}
	tools.RegisterWithDef("read_file", toolbuiltin.ReadFileTool(fsOpts), toolbuiltin.ReadFileDef)
	tools.RegisterWithDef("write_file", toolbuiltin.WriteFileTool(fsOpts), toolbuiltin.WriteFileDef)
	tools.RegisterWithDef("list_dir", toolbuiltin.ListDirTool(fsOpts), toolbuiltin.ListDirDef)
	tools.RegisterWithDef("make_dir", toolbuiltin.MakeDirTool(fsOpts), toolbuiltin.MakeDirDef)
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

	// tts: convert text to speech — registered after controlTTSMgr is set up.
	// See the deferred registration below (after controlTTSMgr = ttspkg.NewManager()).
	runtimeCfg, err := ensureRuntimeConfig(ctx, docsRepo, cfg.Relays, pubkey)
	if err != nil {
		log.Fatalf("load runtime config: %v", err)
	}
	codec.SetEncrypt(runtimeCfg.StorageEncryptEnabled())
	configState = newRuntimeConfigStore(runtimeCfg)
	controlRuntimeConfig = configState
	{
		identityName := "main"
		identityModel := strings.TrimSpace(os.Getenv("METIQ_AGENT_PROVIDER"))
		for _, ag := range runtimeCfg.Agents {
			id := strings.TrimSpace(ag.ID)
			if id != "" && id != "main" {
				continue
			}
			if name := strings.TrimSpace(ag.Name); name != "" {
				identityName = name
			}
			if model := strings.TrimSpace(ag.Model); model != "" {
				identityModel = model
			}
			break
		}
		toolbuiltin.SetIdentityInfo(toolbuiltin.IdentityInfo{
			Name:   identityName,
			Pubkey: pubkey,
			NPub:   toolbuiltin.NostrNPubFromHex(pubkey),
			Model:  identityModel,
		})
	}

	// ── Early config file sync ──────────────────────────────────────────────
	// Load config.json synchronously at startup so that configState reflects
	// file-based settings (e.g. memory.backend) before the backend is initialized.
	// The file watcher is started later and handles subsequent hot-reloads.
	if cfgPath, cfgErr := config.DefaultConfigPath(); cfgErr == nil {
		if config.ConfigFileExists(cfgPath) {
			if earlyDoc, earlyErr := config.LoadConfigFile(cfgPath); earlyErr == nil {
				configState.Set(earlyDoc)
				codec.SetEncrypt(earlyDoc.StorageEncryptEnabled())
				log.Printf("config: early sync from %s", cfgPath)
			} else {
				log.Printf("config: early sync failed (%v); using Nostr state", earlyErr)
			}
		}
	}

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
	// The backend abstraction is used to future-proof swappable storage; the
	// default "memory" backend wraps the in-process JSON inverted index.
	{
		memoryBackendName := "memory"
		if mExtra, ok := configState.Get().Extra["memory"].(map[string]any); ok {
			if beName, ok2 := mExtra["backend"].(string); ok2 && strings.TrimSpace(beName) != "" {
				memoryBackendName = strings.TrimSpace(beName)
			}
		}
		memoryBackendPath := ""
		if mExtra2, ok2 := configState.Get().Extra["memory"].(map[string]any); ok2 {
			qdrantURL, _ := mExtra2["url"].(string)
			ollamaURL, _ := mExtra2["ollama_url"].(string)
			collection, _ := mExtra2["collection"].(string)
			// path format: "qdrantURL|ollamaURL|collection"
			if qdrantURL != "" {
				memoryBackendPath = qdrantURL + "|" + ollamaURL + "|" + collection
			}
		}
		if be, beErr := memory.OpenBackend(memoryBackendName, memoryBackendPath); beErr != nil {
			log.Printf("memory backend %q not available (%v); using json-fts", memoryBackendName, beErr)
		} else {
			log.Printf("memory backend: %s path=%q", memoryBackendName, memoryBackendPath)
			memoryIndex = memory.NewHybridIndex(baseMemoryIndex, be)
			controlMemoryStore = memoryIndex
		}
	}

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
		controlContextEngineName = engineName
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
	controlPluginMgr = pluginMgr
	if loadErr := pluginMgr.Load(ctx, configState.Get()); loadErr != nil {
		log.Printf("plugin manager load warning: %v", loadErr)
	}
	pluginMgr.RegisterTools(tools)

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
	if wkspHooksDir := func() string {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".metiq", "workspace", "hooks")
	}(); wkspHooksDir != "" {
		if wkspHooks, err := hookspkg.ScanDir(wkspHooksDir, hookspkg.SourceWorkspace); err == nil {
			for _, h := range wkspHooks {
				hooksMgr.Register(h)
			}
		}
	}
	// Wire bundled Go handlers.
	hookspkg.RegisterBundledHandlers(hooksMgr, hookspkg.BundledHandlerOpts{
		WorkspaceDir: func() string {
			cfg := configState.Get()
			if cfg.Extra != nil {
				if ws, ok := cfg.Extra["workspace"].(map[string]any); ok {
					if d, ok := ws["dir"].(string); ok && d != "" {
						return d
					}
				}
			}
			if d := os.Getenv("METIQ_WORKSPACE"); d != "" {
				return d
			}
			home, _ := os.UserHomeDir()
			return filepath.Join(home, ".metiq", "workspace")
		},
	})
	// Attach shell handlers for any managed/workspace hooks that have handler.sh
	// but no bundled Go implementation.
	hookspkg.AttachShellHandlers(hooksMgr)
	controlHooksMgr = hooksMgr

	// ── Secrets store ─────────────────────────────────────────────────────────
	secretsStore := secretspkg.NewStore(nil) // uses ~/.metiq/.env by default
	if _, warns := secretsStore.Reload(); len(warns) > 0 {
		for _, w := range warns {
			log.Printf("secrets: %s", w)
		}
	}
	controlSecrets = secretsStore
	mcpAuthController := newMCPAuthController(&mcpManager, tools, secretsStore, func() state.ConfigDoc { return configState.Get() })
	controlMCPAuth = mcpAuthController
	controlMCPOps = newMCPOpsController(&mcpManager, tools, mcpAuthController, configState, docsRepo)
	mcpAuthController.InstallOnManager(mcpManager)
	{
		mcpCfg := mcppkg.ResolveConfigDoc(configState.Get())
		applyMCPConfigAndReconcile(ctx, &mcpManager, tools, mcpCfg, "initialization")
	}

	// TTS manager — initialise before the server starts so method handlers have it.
	controlTTSMgr = ttspkg.NewManager()
	// Register the tts agent tool now that the manager is initialised.
	tools.Register("tts", toolbuiltin.TTSTool(controlTTSMgr))

	// Canvas host — shared store for agent-rendered UI content.
	controlCanvas = canvas.NewHost()
	controlCanvas.Subscribe(func(ev canvas.UpdateEvent) {
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
		if err := controlCanvas.UpdateCanvas(id, contentType, data); err != nil {
			return "", fmt.Errorf("canvas_update: %w", err)
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "canvas_id": id, "content_type": contentType})
		return string(b), nil
	}, toolbuiltin.CanvasUpdateDef)

	// Media transcriber — auto-selected from configured API keys, or a specific
	// backend from the config's media_understanding.transcriber field.
	// Priority: config override → OPENAI_API_KEY → GROQ_API_KEY → DEEPGRAM_API_KEY.
	if t := configuredTranscriber(configState.Get()); t != nil {
		controlMediaTranscriber = t
	} else {
		controlMediaTranscriber = mediapkg.DefaultTranscriber()
	}
	if controlMediaTranscriber != nil {
		log.Printf("media transcriber: configured (type=%T)", controlMediaTranscriber)
	} else {
		log.Printf("media transcriber: none configured (audio attachments will not be transcribed)")
	}

	checkpoint, err := ensureIngestCheckpoint(ctx, docsRepo)
	if err != nil {
		log.Fatalf("load ingest checkpoint: %v", err)
	}
	tracker := newIngestTracker(checkpoint)
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
	controlAgentRuntime = agentRuntime
	controlAgentJobs = agentJobs
	controlNodeInvocations = nodeInvocations
	controlNodePending = nodePending
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
	controlKeyRings = keyRings
	controlOps = ops
	ops.SyncHeartbeatConfig(configState.Get().Heartbeat)

	// ── Exec approval middleware ──────────────────────────────────────────────
	// Hook the tool registry so that tools matching the configured approval list
	// pause execution, create an approval request, and wait for a human decision
	// before proceeding.  This implements OpenClaw parity for exec approval gating.
	{
		// Default tool names that require approval.
		// If Extra["approvals"]["tools"] is present (even empty), it REPLACES the defaults.
		// Set to [] for fully autonomous operation; omit the key to use defaults.
		defaultApprovalTools := []string{"bash", "shell", "exec", "run_command", "terminal", "sh", "bash_exec", "process_spawn", "process_send", "process_kill", "process_exec", "git_status", "git_diff", "test_run"}
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
			// ── Approval gate ───────────────────────────────────────────
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
			if !liveApprovalTools[call.Name] {
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
	controlUpdateChecker = update.NewChecker(version, updateCheckURL)

	// Multi-agent runtime registry: maps agent IDs to their Runtime instances.
	// "main" / "" always resolves to agentRuntime (the default).
	agentRegistry := agent.NewAgentRuntimeRegistry(agentRuntime)
	sessionRouter := agent.NewAgentSessionRouter()
	controlAgentRegistry = agentRegistry
	controlSessionRouter = sessionRouter

	// Channel registry for NIP-29 group chat and future channel types.
	channelReg := channels.NewRegistry()
	controlChannels = channelReg
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
		turnContext, surfacedFileMemory, memoryRecallSample := buildDynamicMemoryRecallContext(turnCtx, memoryIndex, scopeCtx, sessionID, text, workspaceDirForAgent(configState.Get(), sessionRouter.Get(sessionID)), sessionStore)
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
		return preparedAgentRunTurn{
			Turn: agent.Turn{
				SessionID:          sessionID,
				TurnID:             nextDeterministicRecallTurnID(),
				UserText:           text,
				StaticSystemPrompt: staticSystemPrompt,
				Context:            turnContext,
				History:            turnHistory,
				Tools:              turnTools,
				Executor:           turnExecutor,
			},
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
					turnCtx, release := chatCancels.Begin(sessionID, ctx)
					go func() {
						defer release()
						filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, rt, tools, turnToolConstraints{})
						prepared := buildAutoJoinTurn(turnCtx, sessionID, msg.Text, turnTools, turnExecutor)
						result, turnErr := filteredRuntime.ProcessTurn(turnCtx, prepared.Turn)
						if turnErr != nil {
							log.Printf("auto-join channel agent turn error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, turnErr)
							return
						}
						commitMemoryRecallArtifacts(sessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
						if err := msg.Reply(turnCtx, result.Text); err != nil {
							log.Printf("auto-join channel reply error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, err)
						}
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
					turnCtx, release := chatCancels.Begin(sessionID, ctx)
					go func() {
						defer release()
						filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, rt, tools, turnToolConstraints{})
						prepared := buildAutoJoinTurn(turnCtx, sessionID, msg.Text, turnTools, turnExecutor)
						result, turnErr := filteredRuntime.ProcessTurn(turnCtx, prepared.Turn)
						if turnErr != nil {
							log.Printf("auto-join nip28 agent turn error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, turnErr)
							return
						}
						commitMemoryRecallArtifacts(sessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
						if err := msg.Reply(turnCtx, result.Text); err != nil {
							log.Printf("auto-join nip28 reply error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, err)
						}
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
					turnCtx, release := chatCancels.Begin(sessionID, ctx)
					go func() {
						defer release()
						filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, sessionID, activeAgentID, rt, tools, turnToolConstraints{})
						prepared := buildAutoJoinTurn(turnCtx, sessionID, msg.Text, turnTools, turnExecutor)
						result, turnErr := filteredRuntime.ProcessTurn(turnCtx, prepared.Turn)
						if turnErr != nil {
							log.Printf("auto-join chat agent turn error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, turnErr)
							return
						}
						commitMemoryRecallArtifacts(sessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
						if err := msg.Reply(turnCtx, result.Text); err != nil {
							log.Printf("auto-join chat reply error channel=%s agent=%s err=%v", msg.ChannelID, activeAgentID, err)
						}
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
				log.Printf("agent config: default runtime updated id=main model=%q provider=%q", model, agCfg.Provider)
				continue
			}
			agentRegistry.Set(agentID, rt)
			log.Printf("agent config auto-provisioned id=%s model=%q provider=%q", agentID, model, agCfg.Provider)
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
		if err := rotateSessionCoordinated(cmdCtx, sessionID, reason, isACP, chatCancels, sessionRouter, &seenChannelSessions, controlHooksMgr, transcriptRepo, sessionStore, configState.Get()); err != nil {
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
			if err := deleteSessionCoordinated(cmdCtx, target, chatCancels, sessionRouter, &seenChannelSessions, docsRepo, transcriptRepo, sessionStore); err != nil {
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
			wsDir := skillspkg.WorkspaceDir(cfg.Extra, activeAgentID)
			if wsDir == "" {
				home, _ := os.UserHomeDir()
				wsDir = filepath.Join(home, ".metiq", "workspace")
			}
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
	slashRouter.Register("compact", func(cmdCtx context.Context, cmd autoreply.Command) (string, error) {
		if controlContextEngine == nil {
			return "⚠️  No context engine active.", nil
		}
		flushOutcome, err := ensureSessionMemoryCurrent(cmdCtx, configState.Get(), cmd.SessionID, sessionStore)
		if err != nil {
			return fmt.Sprintf("⚠️  Session memory flush failed: %v", err), nil
		}
		cr, cErr := controlContextEngine.Compact(cmdCtx, cmd.SessionID)
		if cErr != nil {
			return fmt.Sprintf("⚠️  Compact failed: %v", cErr), nil
		}
		recordSessionCompaction(sessionStore, cmd.SessionID, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
		if !cr.Compacted {
			return "Nothing to compact yet.", nil
		}
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
				log.Printf("dm session busy, dropped by steer mode: session=%s", sessionID)
				return
			case "interrupt":
				chatCancels.Abort(sessionID)
				_ = sessionDMQ.Dequeue() // clear backlog before enqueueing latest
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
			pending := sessionDMQ.Dequeue()
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
			if sessionDMQ.Len() > 0 {
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
		const defaultTurnTimeoutSecs = 180
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
			turnCtx, cancelTurnTimeout = context.WithTimeout(turnCtxBase, time.Duration(turnTimeoutSecs)*time.Second)
		} else {
			turnCtx = turnCtxBase
			cancelTurnTimeout = func() {}
		}
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in agent process session=%s panic=%v", sessionID, r)
			}
			cancelTurnTimeout()
			releaseTurn()
		}()

		userMemoryDocs := memory.ExtractFromTurn(sessionID, "user", entryID, combinedText, createdAt)
		if len(userMemoryDocs) > 0 {
			go func(docs []state.MemoryDoc) {
				persistCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

		scopeCtx := resolveMemoryScopeContext(turnCtx, configState.Get(), docsRepo, sessionStore, sessionID, activeAgentID, "")
		turnCtx = contextWithMemoryScope(turnCtx, scopeCtx)
		turnContext, surfacedFileMemory, memoryRecallSample := buildDynamicMemoryRecallContext(turnCtx, memoryIndex, scopeCtx, sessionID, combinedText, workspaceDirForAgent(configState.Get(), activeAgentID), sessionStore)
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
					flushOutcome, flushErr := ensureSessionMemoryCurrent(turnCtx, configState.Get(), sessionID, sessionStore)
					if flushErr != nil {
						log.Printf("context engine auto-compact skipped session=%s session_memory_err=%v", sessionID, flushErr)
					} else if cr, cErr := controlContextEngine.Compact(turnCtx, sessionID); cErr == nil && cr.Compacted {
						recordSessionCompaction(sessionStore, sessionID, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
						log.Printf("context engine auto-compact session=%s tokens_before=%d tokens_after=%d", sessionID, cr.TokensBefore, cr.TokensAfter)
						assembled, _ = controlContextEngine.Assemble(turnCtx, sessionID, maxCtxTokens)
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

		// Inject workspace identity files + system_prompt into turnContext.
		// Loads SOUL.md, IDENTITY.md, USER.md from the agent workspace dir,
		// then prepends the config system_prompt. Runs at turn time so
		// hot-reloaded config and edited files are always reflected.
		{
			liveAgents := configState.Get().Agents
			log.Printf("DEBUG turn agent=%s live_agents=%d context_before=%d", activeAgentID, len(liveAgents), len(turnContext))

			wsDir := ""
			var agentSystemPrompt string
			for _, ac := range liveAgents {
				if ac.ID == activeAgentID {
					agentSystemPrompt = strings.TrimSpace(ac.SystemPrompt)
					wsDir = strings.TrimSpace(ac.WorkspaceDir)
					break
				}
			}
			if wsDir == "" {
				if home, herr := os.UserHomeDir(); herr == nil {
					wsDir = filepath.Join(home, ".metiq", "workspace")
				}
			}

			var identityParts []string
			// BOOTSTRAP.md is loaded first so it frames the agent's first session.
			// It is expected to be deleted by the agent after identity is established —
			// mirroring OpenClaw's behaviour.  All other identity files are always loaded
			// when present; the os.ReadFile check is a no-op if the file doesn't exist.
			for _, fname := range []string{"BOOTSTRAP.md", "SOUL.md", "IDENTITY.md", "USER.md", "AGENTS.md"} {
				fpath := filepath.Join(wsDir, fname)
				if raw, ferr := os.ReadFile(fpath); ferr == nil && len(raw) > 0 {
					identityParts = append(identityParts, strings.TrimSpace(string(raw)))
				}
			}

			// Fire agent:bootstrap to allow extra file injection via the
			// bootstrap-extra-files hook.  Extra file patterns are read from
			// extra.bootstrap_extra_files.paths in the runtime config.
			if controlHooksMgr != nil {
				liveCfg := configState.Get()
				var extraPaths []string
				if befRaw, ok := liveCfg.Extra["bootstrap_extra_files"]; ok {
					if befMap, ok := befRaw.(map[string]any); ok {
						for _, key := range []string{"paths", "patterns", "files"} {
							if v, ok2 := befMap[key]; ok2 {
								switch tv := v.(type) {
								case []any:
									for _, p := range tv {
										if s, ok3 := p.(string); ok3 {
											extraPaths = append(extraPaths, s)
										}
									}
								case []string:
									extraPaths = append(extraPaths, tv...)
								}
							}
						}
					}
				}
				if len(extraPaths) > 0 {
					evCtx := map[string]any{"paths": extraPaths}
					errs := controlHooksMgr.Fire("agent:bootstrap", sessionID, evCtx)
					for _, herr := range errs {
						log.Printf("agent:bootstrap hook: %v", herr)
					}
					if injected, ok := evCtx["injectedFiles"].([]string); ok {
						identityParts = append(identityParts, injected...)
					}
				}
			}

			var contextParts []string
			if len(identityParts) > 0 {
				contextParts = append(contextParts, strings.Join(identityParts, "\n\n---\n\n"))
			}
			if agentSystemPrompt != "" {
				contextParts = append(contextParts, agentSystemPrompt)
				log.Printf("DEBUG system_prompt injected agent=%s len=%d", activeAgentID, len(agentSystemPrompt))
			}
			if len(contextParts) > 0 {
				prefix := strings.Join(contextParts, "\n\n")
				staticSystemPrompt = joinPromptSections(staticSystemPrompt, prefix)
				log.Printf("DEBUG workspace_context agent=%s identity_files=%d total_len=%d", activeAgentID, len(identityParts), len(staticSystemPrompt))
			}
		}

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
		for _, ac := range configState.Get().Agents {
			if ac.ID == activeAgentID && ac.ThinkingLevel != "" {
				thinkingBudget = thinkingLevelToBudget(ac.ThinkingLevel)
				break
			}
		}
		// ── Inject runtime context (OpenClaw parity) ───────────────────────
		// Build supplementary system prompt sections: runtime info, time, tool
		// summaries, model aliases, TTS, reactions, sandbox, skills, docs.
		{
			liveCfg := configState.Get()
			agentModel := ""
			agentThinkingLevel := ""
			var agentCapabilities []string
			for _, ac := range liveCfg.Agents {
				if ac.ID == activeAgentID {
					agentModel = ac.Model
					agentThinkingLevel = ac.ThinkingLevel
					break
				}
			}
			if agentModel == "" {
				agentModel = liveCfg.Agent.DefaultModel
			}
			if agentThinkingLevel == "" {
				if sessionStore != nil {
					if se, ok := sessionStore.Get(sessionID); ok && se.ThinkingLevel != "" {
						agentThinkingLevel = se.ThinkingLevel
					}
				}
			}

			// Resolve channel capabilities from config if available.
			if capsRaw, ok := liveCfg.Extra["capabilities"]; ok {
				switch v := capsRaw.(type) {
				case []any:
					for _, c := range v {
						if s, ok := c.(string); ok {
							agentCapabilities = append(agentCapabilities, s)
						}
					}
				case []string:
					agentCapabilities = v
				}
			}

			skillsPromptStr := buildSkillsPromptCached(liveCfg, activeAgentID)

			wsDir := ""
			for _, ac := range liveCfg.Agents {
				if ac.ID == activeAgentID {
					wsDir = strings.TrimSpace(ac.WorkspaceDir)
					break
				}
			}
			if wsDir == "" {
				if home, herr := os.UserHomeDir(); herr == nil {
					wsDir = filepath.Join(home, ".metiq", "workspace")
				}
			}

			runtimeParams := turnRuntimeParams{
				AgentID:       activeAgentID,
				SelfPubkey:    pubkey,
				SelfNPub:      toolbuiltin.NostrNPubFromHex(pubkey),
				Model:         agentModel,
				Channel:       "nostr",
				Capabilities:  agentCapabilities,
				Tools:         baseTurnTools,
				Config:        liveCfg,
				WorkspaceDir:  wsDir,
				ThinkingLevel: agentThinkingLevel,
				SkillsPrompt:  skillsPromptStr,
			}
			staticSystemPrompt = joinPromptSections(staticSystemPrompt, buildTurnRuntimeStaticContext(runtimeParams))
			turnContext = joinPromptSections(turnContext, buildTurnRuntimeDynamicContext())
		}

		baseTurn := agent.Turn{
			SessionID:          sessionID,
			TurnID:             eventID,
			UserText:           combinedText,
			StaticSystemPrompt: staticSystemPrompt,
			Context:            turnContext,
			History:            turnHistory,
			Tools:              baseTurnTools,
			Executor:           turnExecutor, // canonical filtered turn tool pool
			ThinkingBudget:     thinkingBudget,
			ToolEventSink:      toolLifecycleEmitter(wsEmitter, activeAgentID),
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
				sessionMemoryRuntime.ObserveTurn(configState.Get(), runtimeSessionMemoryGenerator{runtime: activeRuntime}, sessionID, sessionMemoryWorkspaceDir(scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID)), partial.HistoryDelta)
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
			return
		}
		stopHeartbeat()
		// NIP-38: return to idle once the agent turn is complete.
		if controlPresenceHeartbeat38 != nil {
			controlPresenceHeartbeat38.SetIdle(ctx)
		}

		if err := persistToolTraces(ctx, transcriptRepo, sessionID, eventID, turnResult.ToolTraces); err != nil {
			log.Printf("persist tool traces failed session=%s err=%v", sessionID, err)
		}
		// Persist the full tool-call/tool-result history so future turns can
		// see prior tool usage — fixes the "announce and forget" behaviour.
		persistAndIngestTurnHistory(ctx, transcriptRepo, controlContextEngine, sessionID, eventID, turnResult.HistoryDelta, turnResultMetadataPtr(turnResult, nil))
		sessionMemoryRuntime.ObserveTurn(configState.Get(), runtimeSessionMemoryGenerator{runtime: activeRuntime}, sessionID, sessionMemoryWorkspaceDir(scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID)), turnResult.HistoryDelta)
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
			if !sendSuppressed {
				if err := replyFn(ctx, outboundText); err != nil {
					log.Printf("reply failed event=%s err=%v", eventID, err)
					logBuffer.Append("error", fmt.Sprintf("dm reply failed event=%s err=%v", eventID, err))
					return
				}
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
		assistantMemoryDocs := memory.ExtractFromTurn(sessionID, "assistant", eventID, turnResult.Text, time.Now().Unix())
		if len(assistantMemoryDocs) > 0 {
			go func(docs []state.MemoryDoc) {
				persistCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				persistMemories(persistCtx, docsRepo, memoryRepo, memoryIndex, memoryTracker, docs)
			}(assistantMemoryDocs)
		}
		if sessionStore != nil && (turnResult.Usage.InputTokens > 0 || turnResult.Usage.OutputTokens > 0) {
			_ = sessionStore.AddTokens(sessionID, turnResult.Usage.InputTokens, turnResult.Usage.OutputTokens)
		}
		// Note: assistant text is already ingested via persistAndIngestTurnHistory
		// above (as part of HistoryDelta), so we don't duplicate it here.
		if eventID != "" && createdAt > 0 && !strings.HasPrefix(eventID, "watch:") {
			if err := tracker.MarkProcessed(ctx, docsRepo, eventID, createdAt); err != nil {
				log.Printf("checkpoint update failed event=%s err=%v", eventID, err)
			}
		}
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
		specs, bootstrapped, specErr := loadOrDefaultWatchSpecs(raw, controlPrivateKey, controlPrivateKey, time.Now())
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
		// If the sender is a registered ACP peer and the message is a
		// valid ACP JSON payload, dispatch through the ACP handler instead
		// of the user-facing agent pipeline.
		if controlACPPeers != nil && controlACPPeers.IsPeer(msg.FromPubKey) && acppkg.IsACPMessage([]byte(msg.Text)) {
			if acpMsg, acpErr := acppkg.Parse([]byte(msg.Text)); acpErr == nil {
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

	// ── NIP-51 allowlist watcher + agent list sync ─────────────────────────────
	// Create a dedicated pool for NIP-51 list fetch/subscribe operations so the
	// DM buses are not disturbed.
	{
		nip51Pool := nostr.NewPool(nostruntime.PoolOptsNIP42(controlKeyer))
		liveCfg := configState.Get()

		// When the runtime config has no explicit relays, fall back to bootstrap relays.
		if len(liveCfg.Relays.Read) == 0 {
			liveCfg.Relays.Read = cfg.Relays
		}
		if len(liveCfg.Relays.Write) == 0 {
			liveCfg.Relays.Write = cfg.Relays
		}
		startRelayHealthMonitor(ctx, nostruntime.MergeRelayLists(liveCfg.Relays.Read, liveCfg.Relays.Write))

		// ── NIP-65 Relay Selector (outbox model) ────────────────────────────
		// Initialize once; keep a single shared selector/hub instance so existing
		// channels and tools continue to share the same pooled connections.
		if controlRelaySelector == nil {
			controlRelaySelector = nostruntime.NewRelaySelector(liveCfg.Relays.Read, liveCfg.Relays.Write)
			toolbuiltin.SetRelaySelector(controlRelaySelector)
		}

		// ── Shared NostrHub ──────────────────────────────────────────────────
		if controlHub == nil && controlKeyer != nil {
			hub, hubErr := nostruntime.NewHub(ctx, controlKeyer, controlRelaySelector)
			if hubErr != nil {
				log.Printf("nostr hub: failed to create: %v (channels/tools will use dedicated pools)", hubErr)
			} else {
				controlHub = hub
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
				Keyer:         controlKeyer,
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
			Keyer:  controlKeyer,
			Pool:   nip51Pool,
			Relays: nostruntime.MergeRelayLists(liveCfg.Relays.Read, liveCfg.Relays.Write),
			OnRelayUpdate: func(read, write []string) {
				log.Printf("nip65: applying remote relay list update (read=%d, write=%d)", len(read), len(write))

				// Update the relay selector fallbacks.
				controlRelaySelector.SetFallbacks(read, write)

				// Update the runtime config.
				if configState != nil {
					current := configState.Get()
					current.Relays.Read = read
					current.Relays.Write = write
					if err := persistRuntimeConfigFile(current); err != nil {
						log.Printf("nip65: config write-back failed: %v", err)
					} else {
						configState.Set(current)
					}
				}

				allRelays := nostruntime.MergeRelayLists(read, write)
				applyDMRelayPolicy(allRelays)
				applyControlRelayPolicy(allRelays)
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
				Keyer:    controlKeyer,
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
					if _, err := nostruntime.PublishRelaySet(ctx, nip51Pool, controlKeyer, allRelays, dtag, entry.Relays); err != nil {
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
		startRepoBookmarkWatcher(ctx, nip51Pool, controlKeyer, liveCfg)

		// Publish local kind:30317 capabilities and subscribe to fleet peers'
		// capability advertisements for dynamic discovery.
		capabilityRegistry = nostruntime.NewCapabilityRegistry()
		capabilityRegistry.OnChange(func(pubkey string, cap nostruntime.CapabilityAnnouncement) {
			log.Printf("capability-sync: peer=%s runtime=%s version=%s tools=%d", pubkey, cap.Runtime, cap.RuntimeVersion, len(cap.Tools))
			writeFleetMD(getFleetWorkspaceDir())
		})
		capabilityMonitor = nostruntime.NewCapabilityMonitor(nostruntime.CapabilityMonitorOptions{
			Pool:            nip51Pool,
			Keyer:           controlKeyer,
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
		hbKeyer := controlKeyer
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
				defer controlPresenceHeartbeat38.Stop()
				log.Printf("NIP-38 presence/status active (interval=%s)", hbInterval)
			}
		} else if !hbEnabled {
			log.Printf("NIP-38 presence/status disabled by config")
		} else {
			log.Printf("NIP-38 presence/status skipped: no signing key available")
		}
		_ = controlPresenceHeartbeat38 // referenced in dmRunAgentTurn closure
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
				Keyer:         controlKeyer,
				Relays:        cfg.Relays,
				AcceptedKinds: acceptedKinds,
				OnJob: func(jobCtx context.Context, jobID string, kind int, input string) (string, error) {
					filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(jobCtx, configState.Get(), docsRepo, "dvm:"+jobID, "", agentRuntime, tools, turnToolConstraints{})
					result, err := filteredRuntime.ProcessTurn(jobCtx, agent.Turn{
						SessionID: "dvm:" + jobID,
						UserText:  input,
						Tools:     turnTools,
						Executor:  turnExecutor,
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
	) (turnErr error) {
		// ── Session start hook (fires once per session) ───────────────────
		if _, seen := seenChannelSessions.LoadOrStore(sessionID, struct{}{}); !seen {
			fireHookEvent(controlHooksMgr, "session:start", sessionID, map[string]any{
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

		scopeCtx := resolveMemoryScopeContext(turnCtx, configState.Get(), docsRepo, sessionStore, sessionID, activeAgentID, "")
		turnCtx = contextWithMemoryScope(turnCtx, scopeCtx)
		turnContext, surfacedFileMemory, memoryRecallSample := buildDynamicMemoryRecallContext(turnCtx, memoryIndex, scopeCtx, sessionID, text, workspaceDirForAgent(configState.Get(), activeAgentID), sessionStore)
		staticSystemPrompt := assembleMemorySystemPrompt(memoryIndex, scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID))
		var chTurnHistory []agent.ConversationMessage
		if controlContextEngine != nil {
			if assembled, asmErr := controlContextEngine.Assemble(turnCtx, sessionID, 100_000); asmErr == nil {
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
		chBaseTurn := agent.Turn{
			SessionID:          sessionID,
			TurnID:             eventID,
			UserText:           text,
			StaticSystemPrompt: staticSystemPrompt,
			Context:            turnContext,
			History:            chTurnHistory,
			Tools:              turnTools,
			Executor:           turnExecutor,
			ToolEventSink:      toolLifecycleEmitter(wsEmitter, activeAgentID),
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
				persistAndIngestTurnHistory(ctx, transcriptRepo, controlContextEngine, sessionID, eventID, partial.HistoryDelta, turnResultMetadataPtr(turnResult, turnErr))
				sessionMemoryRuntime.ObserveTurn(configState.Get(), runtimeSessionMemoryGenerator{runtime: activeRuntime}, sessionID, sessionMemoryWorkspaceDir(scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID)), partial.HistoryDelta)
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

		if err := persistToolTraces(ctx, transcriptRepo, sessionID, eventID, turnResult.ToolTraces); err != nil {
			log.Printf("persist tool traces (channel) failed session=%s err=%v", sessionID, err)
		}
		persistAndIngestTurnHistory(ctx, transcriptRepo, controlContextEngine, sessionID, eventID, turnResult.HistoryDelta, turnResultMetadataPtr(turnResult, nil))
		sessionMemoryRuntime.ObserveTurn(configState.Get(), runtimeSessionMemoryGenerator{runtime: activeRuntime}, sessionID, sessionMemoryWorkspaceDir(scopeCtx, workspaceDirForAgent(configState.Get(), activeAgentID)), turnResult.HistoryDelta)
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
		}
		if !audioSent && handle != nil && outboundText != "" {
			if sendErr := handle.Send(turnCtx, outboundText); sendErr != nil {
				log.Printf("channel reply error channel=%s session=%s err=%v", chID, sessionID, sendErr)
			} else {
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
			_ = sessionStore.AddTokens(sessionID, turnResult.Usage.InputTokens, turnResult.Usage.OutputTokens)
		}
		turnTelemetry := buildTurnTelemetry(eventID, turnStartedAt, time.Now(), turnResult, nil, false, "", "", "")
		persistTurnTelemetry(sessionStore, sessionID, turnTelemetry)
		emitTurnTelemetry(wsEmitter, activeAgentID, sessionID, turnTelemetry)
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
				log.Printf("channel session busy, dropped by steer mode: session=%s", sessionID)
				return
			case "interrupt":
				chatCancels.Abort(sessionID)
				_ = sessionQ.Dequeue()
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
			releaseTurn()
			releaseTurnSlot()
			if sessionQ.Len() == 0 {
				channelQueues.Delete(sessionID)
			}
		}()

		replyCtx := sdk.WithChannelReplyTarget(turnCtx, senderID)

		// Run the initial turn.
		_ = doChannelTurn(replyCtx, chID, senderID, sessionID, combined, eventID, handle, rawHandle)

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
				_ = doChannelTurn(queuedCtx, chID, senderID, sessionID, queuedText, latestEventID, handle, rawHandle)
				continue
			}
			if queueModeSequential(mode) {
				for _, pt := range pending {
					queuedCtx := sdk.WithChannelReplyTarget(turnCtx, senderID)
					_ = doChannelTurn(queuedCtx, chID, senderID, sessionID, pt.Text, pt.EventID, handle, rawHandle)
				}
				continue
			}
			latest := pending[len(pending)-1]
			queuedCtx := sdk.WithChannelReplyTarget(turnCtx, senderID)
			_ = doChannelTurn(queuedCtx, chID, senderID, sessionID, latest.Text, latest.EventID, handle, rawHandle)
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
		Keyer:             controlKeyer,
		Hub:               controlHub,
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
				res, err := handleControlRPCRequest(ctx, nostruntime.ControlRPCInbound{
					FromPubKey: bus.PublicKey(),
					Method:     strings.TrimSpace(req.Method),
					Params:     req.Params,
				}, bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
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

	// configState.Set hook: apply live runtime side effects + WS event on every
	// config mutation. Disk persistence is handled before successful Set() calls
	// in mutation paths so callers do not observe success when write-back fails.
	configState.SetOnChange(func(doc state.ConfigDoc) {
		applyRuntimeConfigSideEffects(doc)
		resolvedMCP := mcppkg.ResolveConfigDoc(doc)
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
			config.WithOnChange(func(doc state.ConfigDoc) {
				doc = policy.NormalizeConfig(doc)
				log.Printf("config file changed: applying live reload path=%s", configFilePath)
				// Use the internal field directly to avoid triggering disk write-back
				// (the file already has the new content).
				configState.mu.Lock()
				configState.cfg = doc
				configState.mu.Unlock()
				applyRuntimeConfigSideEffects(doc)
				resolvedMCP := mcppkg.ResolveConfigDoc(doc)
				applyMCPConfigAndReconcile(ctx, &mcpManager, tools, resolvedMCP, "live file-reload apply")
				filteredMCPLifecycle.Emit(runtimeEventEmitterFunc(emitControlWSEvent), resolvedMCP, "file-reload.snapshot", time.Now().UnixMilli())
				wsEmitter.Emit(gatewayws.EventConfigUpdated, gatewayws.ConfigUpdatedPayload{
					TS: time.Now().UnixMilli(),
				})
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
					configState.Set(newDoc)
					log.Printf("SIGHUP reload: config applied successfully agents=%d relays=%d",
						len(newDoc.Agents), len(newDoc.Relays.Read))
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
					if controlMCPOps == nil {
						return nil
					}
					return controlMCPOps.telemetrySnapshotPtr()
				},
				Metrics: func(_ context.Context) string {
					// Update live gauges before rendering.
					metricspkg.UptimeSeconds.Set(time.Since(startedAt).Seconds())
					metricspkg.RelayConnected.Set(float64(len(cfg.Relays)))
					if controlExecApprovals != nil {
						pending := controlExecApprovals.GetGlobal()
						metricspkg.ApprovalQueueSize.Set(float64(len(pending)))
					}
					return metricspkg.Default.Exposition()
				},
				HealthExtra: func(_ context.Context) map[string]any {
					body := map[string]any{
						"uptime_seconds": int(time.Since(startedAt).Seconds()),
						"version":        version,
					}
					if controlMCPOps != nil {
						if snapshot := controlMCPOps.telemetrySnapshotPtr(); snapshot != nil {
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
					if agentRuntime == nil {
						return nil, fmt.Errorf("agent runtime not configured")
					}
					runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
					snapshot := agentJobs.Begin(runID, req.SessionID)
					go executeAgentRun(runID, req, agentRuntime, memoryIndex, agentJobs)
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
					return controlMCPOps.applyList(ctx, req)
				},
				MCPGet: func(ctx context.Context, req methods.MCPGetRequest) (map[string]any, error) {
					return controlMCPOps.applyGet(ctx, req)
				},
				MCPPut: func(ctx context.Context, req methods.MCPPutRequest) (map[string]any, error) {
					return controlMCPOps.applyPut(ctx, req)
				},
				MCPRemove: func(ctx context.Context, req methods.MCPRemoveRequest) (map[string]any, error) {
					return controlMCPOps.applyRemove(ctx, req)
				},
				MCPTest: func(ctx context.Context, req methods.MCPTestRequest) (map[string]any, error) {
					return controlMCPOps.applyTest(ctx, req)
				},
				MCPReconnect: func(ctx context.Context, req methods.MCPReconnectRequest) (map[string]any, error) {
					return controlMCPOps.applyReconnect(ctx, req)
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
					newCfg = policy.NormalizeConfig(newCfg)
					if err := policy.ValidateConfig(newCfg); err != nil {
						return err
					}
					if err := persistRuntimeConfigFile(newCfg); err != nil {
						return err
					}
					if _, err := docsRepo.PutConfig(ctx, newCfg); err != nil {
						return err
					}
					configState.Set(newCfg)
					return nil
				},
				ConfigSet: func(ctx context.Context, req methods.ConfigSetRequest) (map[string]any, int, error) {
					return dispatchAdminControlConfigMutation(ctx, bus.PublicKey(), methods.MethodConfigSet, req, bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
				},
				ConfigApply: func(ctx context.Context, req methods.ConfigApplyRequest) (map[string]any, int, error) {
					return dispatchAdminControlConfigMutation(ctx, bus.PublicKey(), methods.MethodConfigApply, req, bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
				},
				ConfigPatch: func(ctx context.Context, req methods.ConfigPatchRequest) (map[string]any, int, error) {
					return dispatchAdminControlConfigMutation(ctx, bus.PublicKey(), methods.MethodConfigPatch, req, bus, controlBus, chatCancels, usageState, logBuffer, channelState, docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt)
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
	controlCronExecutorMu.Lock()
	controlCronExecutor = func(execCtx context.Context, method string, params json.RawMessage) (any, error) {
		res, err := handleControlRPCRequest(execCtx,
			nostruntime.ControlRPCInbound{
				FromPubKey: daemonPubKey,
				Method:     method,
				Params:     params,
			},
			bus, controlBus, chatCancels, usageState, logBuffer, channelState,
			docsRepo, transcriptRepo, memoryIndex, configState, tools, pluginMgr, startedAt,
		)
		if err != nil {
			return nil, err
		}
		return res.Result, nil
	}
	controlCronExecutorMu.Unlock()

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
			jobs := controlCronJobs.List(1000)
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
					jobCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
					defer cancel()
					_, execErr := func() (any, error) {
						controlCronExecutorMu.RLock()
						exec := controlCronExecutor
						controlCronExecutorMu.RUnlock()
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
					controlCronJobs.RecordRun(jobCopy.ID, status, time.Since(started).Milliseconds())
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
	if controlHooksMgr != nil {
		go controlHooksMgr.Fire("gateway:startup", "", map[string]any{})
	}

	// Boot-time session pruning: delete sessions older than PruneAfterDays if
	// PruneOnBoot is set in the session config.
	if configState != nil {
		sessCfg := configState.Get().Session
		if sessCfg.PruneOnBoot && sessCfg.PruneAfterDays > 0 {
			go func() {
				pruneSessions(ctx, docsRepo, transcriptRepo, sessCfg.PruneAfterDays)
			}()
		}
	}

	<-ctx.Done()
	shutdownEmitter.Emit("daemon stopping")
	select {
	case <-heartbeatDone:
	case <-time.After(250 * time.Millisecond):
	}
	log.Println("metiqd shutting down")
}

func initEnvelopeCodec(signer nostr.Keyer) (*secure.MutableSelfEnvelopeCodec, error) {
	return secure.NewMutableSelfEnvelopeCodec(signer, true)
}

func ensureRuntimeConfig(ctx context.Context, repo *state.DocsRepository, relays []string, adminPubKey string) (state.ConfigDoc, error) {
	doc, err := repo.GetConfig(ctx)
	if err == nil {
		return policy.NormalizeConfig(doc), nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.ConfigDoc{}, err
	}

	fallback := state.ConfigDoc{
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
	if _, err := repo.PutConfig(ctx, fallback); err != nil {
		return state.ConfigDoc{}, err
	}
	return policy.NormalizeConfig(fallback), nil
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
		FromPubKey: fromPubKey,
		Method:     method,
		Params:     raw,
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

func ensureMemoryIndexCheckpoint(ctx context.Context, repo *state.DocsRepository) (state.CheckpointDoc, error) {
	doc, err := repo.GetCheckpoint(ctx, "memory_index")
	if err == nil {
		if doc.Name == "" {
			doc.Name = "memory_index"
		}
		return doc, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.CheckpointDoc{}, err
	}
	fallback := state.CheckpointDoc{Version: 1, Name: "memory_index"}
	if _, err := repo.PutCheckpoint(ctx, "memory_index", fallback); err != nil {
		return state.CheckpointDoc{}, err
	}
	return fallback, nil
}

type runtimeConfigStore struct {
	mu       sync.RWMutex
	cfg      state.ConfigDoc
	onChange func(state.ConfigDoc) // optional: called after each Set
}

type ingestTracker struct {
	mu             sync.Mutex
	lastEvent      string
	lastUnix       int64
	recentEventIDs []string
}

type memoryIndexTracker struct {
	mu        sync.Mutex
	lastEvent string
	lastUnix  int64
}

type controlTracker struct {
	mu             sync.Mutex
	lastEvent      string
	lastUnix       int64
	recentEventIDs []string
	responses      map[string]state.ControlResponseCacheDoc
	responseOrder  []string
}

type chatAbortHandle struct {
	id     uint64
	cancel context.CancelFunc
}

type chatAbortRegistry struct {
	mu       sync.Mutex
	nextID   uint64
	inFlight map[string]chatAbortHandle
}

func newChatAbortRegistry() *chatAbortRegistry {
	return &chatAbortRegistry{inFlight: map[string]chatAbortHandle{}}
}

func (r *chatAbortRegistry) Begin(sessionID string, parent context.Context) (context.Context, func()) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	var previous context.CancelFunc
	r.mu.Lock()
	r.nextID++
	h := chatAbortHandle{id: r.nextID, cancel: cancel}
	if prior, ok := r.inFlight[sessionID]; ok {
		previous = prior.cancel
	}
	r.inFlight[sessionID] = h
	r.mu.Unlock()
	if previous != nil {
		previous()
	}
	return ctx, func() {
		r.mu.Lock()
		current, ok := r.inFlight[sessionID]
		if ok && current.id == h.id {
			delete(r.inFlight, sessionID)
		}
		r.mu.Unlock()
	}
}

func (r *chatAbortRegistry) Abort(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	r.mu.Lock()
	h, ok := r.inFlight[sessionID]
	if ok {
		delete(r.inFlight, sessionID)
	}
	r.mu.Unlock()
	if ok {
		h.cancel()
	}
	return ok
}

func (r *chatAbortRegistry) AbortAll() int {
	r.mu.Lock()
	handles := make([]chatAbortHandle, 0, len(r.inFlight))
	for key, h := range r.inFlight {
		handles = append(handles, h)
		delete(r.inFlight, key)
	}
	r.mu.Unlock()
	for _, h := range handles {
		h.cancel()
	}
	return len(handles)
}

func newRuntimeConfigStore(cfg state.ConfigDoc) *runtimeConfigStore {
	return &runtimeConfigStore{cfg: policy.NormalizeConfig(cfg)}
}

func (s *runtimeConfigStore) Get() state.ConfigDoc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *runtimeConfigStore) Set(cfg state.ConfigDoc) {
	cfg = policy.NormalizeConfig(cfg)
	s.mu.Lock()
	s.cfg = cfg
	onChange := s.onChange
	s.mu.Unlock()
	if onChange != nil {
		onChange(cfg)
	}
}

// SetOnChange registers a callback invoked after every Set.
func (s *runtimeConfigStore) SetOnChange(fn func(state.ConfigDoc)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

func newIngestTracker(doc state.CheckpointDoc) *ingestTracker {
	return &ingestTracker{
		lastEvent:      doc.LastEvent,
		lastUnix:       doc.LastUnix,
		recentEventIDs: normalizeCheckpointEventIDs(doc.RecentEventIDs),
	}
}

func newMemoryIndexTracker(doc state.CheckpointDoc) *memoryIndexTracker {
	return &memoryIndexTracker{lastEvent: doc.LastEvent, lastUnix: doc.LastUnix}
}

const (
	controlResponseCheckpointCap = 256
	controlResponseCheckpointTTL = 30 * time.Minute
	checkpointRecentEventCap     = 2048
)

func newControlTracker(doc state.CheckpointDoc) *controlTracker {
	t := &controlTracker{
		lastEvent:      doc.LastEvent,
		lastUnix:       doc.LastUnix,
		recentEventIDs: normalizeCheckpointEventIDs(doc.RecentEventIDs),
		responses:      map[string]state.ControlResponseCacheDoc{},
	}
	nowUnix := time.Now().Unix()
	for _, entry := range doc.ControlResponses {
		callerPubKey := strings.TrimSpace(entry.CallerPubKey)
		requestID := strings.TrimSpace(entry.RequestID)
		if callerPubKey == "" || requestID == "" {
			continue
		}
		entry.CallerPubKey = callerPubKey
		entry.RequestID = requestID
		key := controlResponseCacheKey(callerPubKey, requestID)
		if _, exists := t.responses[key]; !exists {
			t.responseOrder = append(t.responseOrder, key)
		}
		t.responses[key] = entry
	}
	t.pruneResponsesLocked(nowUnix)
	return t
}

func ensureControlCheckpoint(ctx context.Context, repo *state.DocsRepository) (state.CheckpointDoc, error) {
	doc, err := repo.GetCheckpoint(ctx, "control_ingest")
	if err == nil {
		if doc.Name == "" {
			doc.Name = "control_ingest"
		}
		return doc, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.CheckpointDoc{}, err
	}
	fallback := state.CheckpointDoc{Version: 1, Name: "control_ingest"}
	if _, err := repo.PutCheckpoint(ctx, "control_ingest", fallback); err != nil {
		return state.CheckpointDoc{}, err
	}
	return fallback, nil
}

func (t *ingestTracker) AlreadyProcessed(eventID string, createdAt int64) bool {
	if eventID == "" || createdAt <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if createdAt < t.lastUnix {
		return true
	}
	if createdAt == t.lastUnix && checkpointEventSeen(t.recentEventIDs, eventID) {
		return true
	}
	return false
}

func (t *ingestTracker) MarkProcessed(ctx context.Context, repo *state.DocsRepository, eventID string, eventUnix int64) error {
	if eventID == "" {
		return nil
	}
	if eventUnix <= 0 {
		eventUnix = time.Now().Unix()
	}

	t.mu.Lock()
	if eventUnix < t.lastUnix || (eventUnix == t.lastUnix && checkpointEventSeen(t.recentEventIDs, eventID)) {
		t.mu.Unlock()
		return nil
	}
	t.lastEvent, t.lastUnix, t.recentEventIDs = checkpointAdvanceState(t.lastEvent, t.lastUnix, t.recentEventIDs, eventID, eventUnix)
	checkpoint := state.CheckpointDoc{
		Version:        1,
		Name:           "dm_ingest",
		LastEvent:      t.lastEvent,
		LastUnix:       t.lastUnix,
		RecentEventIDs: append([]string{}, t.recentEventIDs...),
	}
	t.mu.Unlock()

	_, err := repo.PutCheckpoint(ctx, "dm_ingest", checkpoint)
	return err
}

func (t *controlTracker) AlreadyProcessed(eventID string, createdAt int64) bool {
	if eventID == "" || createdAt <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if createdAt < t.lastUnix {
		return true
	}
	if createdAt == t.lastUnix && checkpointEventSeen(t.recentEventIDs, eventID) {
		return true
	}
	return false
}

func (t *controlTracker) LookupResponse(callerPubKey string, requestID string) (nostruntime.ControlRPCCachedResponse, bool) {
	callerPubKey = strings.TrimSpace(callerPubKey)
	requestID = strings.TrimSpace(requestID)
	if callerPubKey == "" || requestID == "" {
		return nostruntime.ControlRPCCachedResponse{}, false
	}
	cutoff := time.Now().Add(-controlResponseCheckpointTTL).Unix()
	t.mu.Lock()
	defer t.mu.Unlock()
	key := controlResponseCacheKey(callerPubKey, requestID)
	entry, ok := t.responses[key]
	if !ok {
		return nostruntime.ControlRPCCachedResponse{}, false
	}
	if entry.EventUnix > 0 && entry.EventUnix < cutoff {
		delete(t.responses, key)
		for i, existing := range t.responseOrder {
			if existing == key {
				t.responseOrder = append(t.responseOrder[:i], t.responseOrder[i+1:]...)
				break
			}
		}
		return nostruntime.ControlRPCCachedResponse{}, false
	}
	return nostruntime.ControlRPCCachedResponse{Payload: entry.Payload, Tags: controlResponseTags(entry.Tags)}, true
}

func (t *controlTracker) MarkHandled(ctx context.Context, repo *state.DocsRepository, handled nostruntime.ControlRPCHandled) error {
	if strings.TrimSpace(handled.EventID) == "" {
		return nil
	}
	nowUnix := time.Now().Unix()
	eventUnix := handled.EventUnix
	if eventUnix <= 0 || eventUnix > nowUnix+30 {
		eventUnix = nowUnix
	}
	t.mu.Lock()
	t.lastEvent, t.lastUnix, t.recentEventIDs = checkpointAdvanceState(t.lastEvent, t.lastUnix, t.recentEventIDs, handled.EventID, eventUnix)
	callerPubKey := strings.TrimSpace(handled.CallerPubKey)
	requestID := strings.TrimSpace(handled.RequestID)
	if callerPubKey != "" && requestID != "" && isCacheableControlMethod(handled.Method) {
		key := controlResponseCacheKey(callerPubKey, requestID)
		if _, exists := t.responses[key]; !exists {
			t.responseOrder = append(t.responseOrder, key)
		}
		t.responses[key] = state.ControlResponseCacheDoc{
			CallerPubKey: callerPubKey,
			RequestID:    requestID,
			Payload:      handled.Response.Payload,
			Tags:         controlResponseDocTags(handled.Response.Tags),
			EventUnix:    eventUnix,
		}
	}
	t.pruneResponsesLocked(nowUnix)
	checkpoint := state.CheckpointDoc{
		Version:          1,
		Name:             "control_ingest",
		LastEvent:        t.lastEvent,
		LastUnix:         t.lastUnix,
		RecentEventIDs:   append([]string{}, t.recentEventIDs...),
		ControlResponses: t.snapshotResponsesLocked(),
	}
	t.mu.Unlock()
	_, err := repo.PutCheckpoint(ctx, "control_ingest", checkpoint)
	return err
}

func controlResponseCacheKey(callerPubKey string, requestID string) string {
	return strings.TrimSpace(callerPubKey) + "\x00" + strings.TrimSpace(requestID)
}

func normalizeCheckpointEventIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) >= checkpointRecentEventCap {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func checkpointEventSeen(ids []string, eventID string) bool {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return false
	}
	for _, existing := range ids {
		if existing == eventID {
			return true
		}
	}
	return false
}

func checkpointAdvanceState(lastEvent string, lastUnix int64, recentEventIDs []string, eventID string, eventUnix int64) (string, int64, []string) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return lastEvent, lastUnix, recentEventIDs
	}
	if eventUnix < lastUnix {
		return lastEvent, lastUnix, recentEventIDs
	}
	if checkpointEventSeen(recentEventIDs, eventID) {
		return lastEvent, lastUnix, recentEventIDs
	}
	updated := append(append([]string{}, recentEventIDs...), eventID)
	if len(updated) > checkpointRecentEventCap {
		updated = updated[len(updated)-checkpointRecentEventCap:]
	}
	return eventID, lastUnix, updated
}

func isCacheableControlMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case methods.MethodSecretsResolve:
		return false
	default:
		return true
	}
}

func (t *controlTracker) pruneResponsesLocked(nowUnix int64) {
	if nowUnix <= 0 {
		nowUnix = time.Now().Unix()
	}
	cutoff := nowUnix - int64(controlResponseCheckpointTTL/time.Second)
	kept := t.responseOrder[:0]
	for _, key := range t.responseOrder {
		entry, ok := t.responses[key]
		if !ok {
			continue
		}
		if entry.EventUnix > 0 && entry.EventUnix < cutoff {
			delete(t.responses, key)
			continue
		}
		kept = append(kept, key)
	}
	t.responseOrder = kept
	for len(t.responseOrder) > controlResponseCheckpointCap {
		victim := t.responseOrder[0]
		t.responseOrder = t.responseOrder[1:]
		delete(t.responses, victim)
	}
}

func (t *controlTracker) snapshotResponsesLocked() []state.ControlResponseCacheDoc {
	if len(t.responseOrder) == 0 {
		return nil
	}
	out := make([]state.ControlResponseCacheDoc, 0, len(t.responseOrder))
	for _, key := range t.responseOrder {
		entry, ok := t.responses[key]
		if !ok {
			continue
		}
		out = append(out, state.ControlResponseCacheDoc{
			CallerPubKey: entry.CallerPubKey,
			RequestID:    entry.RequestID,
			Payload:      entry.Payload,
			Tags:         controlResponseDocTags(controlResponseTags(entry.Tags)),
			EventUnix:    entry.EventUnix,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func controlResponseDocTags(tags nostr.Tags) [][]string {
	if len(tags) == 0 {
		return nil
	}
	out := make([][]string, 0, len(tags))
	for _, tag := range tags {
		out = append(out, append([]string(nil), tag...))
	}
	return out
}

func controlResponseTags(tags [][]string) nostr.Tags {
	if len(tags) == 0 {
		return nil
	}
	out := make(nostr.Tags, 0, len(tags))
	for _, tag := range tags {
		out = append(out, nostr.Tag(append([]string(nil), tag...)))
	}
	return out
}

func persistInbound(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	sessionID string,
	msg nostruntime.InboundDM,
) error {
	now := time.Now().Unix()
	if err := updateSessionDoc(ctx, docsRepo, sessionID, msg.FromPubKey, func(session *state.SessionDoc) error {
		if msg.CreatedAt > 0 {
			session.LastInboundAt = msg.CreatedAt
		} else {
			session.LastInboundAt = now
		}
		return nil
	}); err != nil {
		return err
	}

	_, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version:   1,
		SessionID: sessionID,
		EntryID:   msg.EventID,
		Role:      "user",
		Text:      msg.Text,
		Unix:      msg.CreatedAt,
		Meta: map[string]any{
			"relay": msg.RelayURL,
		},
	})
	return err
}

func synthesizeInboundEventID(fromPubKey, text string, createdAt int64) string {
	seed := fmt.Sprintf("%s\x00%d\x00%s", strings.TrimSpace(fromPubKey), createdAt, strings.TrimSpace(text))
	sum := sha256.Sum256([]byte(seed))
	return "auto:" + hex.EncodeToString(sum[:])
}

func nostrWatchDeliveryMeta(name string, event map[string]any) (string, int64) {
	createdAt := time.Now().Unix()
	sourceID := ""
	if rawID, ok := event["id"].(string); ok {
		sourceID = strings.TrimSpace(rawID)
	}
	switch v := event["created_at"].(type) {
	case int64:
		if v > 0 {
			createdAt = v
		}
	case int:
		if v > 0 {
			createdAt = int64(v)
		}
	case float64:
		if v > 0 {
			createdAt = int64(v)
		}
	}
	if sourceID == "" {
		return synthesizeInboundEventID("watch:"+strings.TrimSpace(name), fmt.Sprintf("%v", event), createdAt), createdAt
	}
	return "watch:" + strings.TrimSpace(name) + ":" + sourceID, createdAt
}

// persistAndIngestTurnHistory writes the ordered HistoryDelta from a completed
// (or partially completed) turn into both the transcript store and context
// engine.  This makes tool interactions visible to future turns.
func persistAndIngestTurnHistory(
	ctx context.Context,
	transcriptRepo *state.TranscriptRepository,
	contextEngine ctxengine.Engine,
	sessionID string,
	requestEventID string,
	delta []agent.ConversationMessage,
	turnResultMeta *agent.TurnResultMetadata,
) {
	if len(delta) == 0 {
		return
	}
	// Guard against empty requestEventID — generate a fallback to prevent
	// colliding entry IDs across turns.
	if requestEventID == "" {
		requestEventID = fmt.Sprintf("anon:%d", time.Now().UnixNano())
	}
	nowUnix := time.Now().Unix()
	persistedTurnResultMeta := transcriptTurnResultMeta(turnResultMeta)
	for i, m := range delta {
		// Build a deterministic entry ID.
		var entryID string
		switch {
		case m.Role == "assistant" && len(m.ToolCalls) > 0:
			entryID = fmt.Sprintf("turn:%s:toolcall:%d", requestEventID, i)
		case m.Role == "tool" && m.ToolCallID != "":
			entryID = fmt.Sprintf("turn:%s:tool:%s", requestEventID, m.ToolCallID)
		case m.Role == "assistant":
			entryID = fmt.Sprintf("turn:%s:assistant:%d", requestEventID, i)
		default:
			entryID = fmt.Sprintf("turn:%s:msg:%d", requestEventID, i)
		}

		// Build transcript metadata.
		meta := map[string]any{"request_event_id": requestEventID}
		if persistedTurnResultMeta != nil && i == len(delta)-1 {
			meta["turn_result"] = persistedTurnResultMeta
		}
		if len(m.ToolCalls) > 0 {
			meta["message_kind"] = "tool_call"
			tcRefs := make([]map[string]any, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				tcRefs[j] = map[string]any{"id": tc.ID, "name": tc.Name}
				if tc.ArgsJSON != "" {
					tcRefs[j]["args_json"] = tc.ArgsJSON
				}
			}
			meta["tool_calls"] = tcRefs
		}
		if m.ToolCallID != "" {
			meta["message_kind"] = "tool_result"
			meta["tool_call_id"] = m.ToolCallID
		}

		// Persist to transcript store.
		if transcriptRepo != nil {
			if _, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
				Version:   1,
				SessionID: sessionID,
				EntryID:   entryID,
				Role:      m.Role,
				Text:      m.Content,
				Unix:      nowUnix,
				Meta:      meta,
			}); err != nil {
				log.Printf("persist turn history entry=%s err=%v", entryID, err)
			}
		}

		// Ingest into context engine.
		if contextEngine != nil {
			ctxMsg := ctxengine.Message{
				Role:       m.Role,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
				ID:         entryID,
				Unix:       nowUnix,
			}
			// Convert tool call refs to context engine format.
			for _, tc := range m.ToolCalls {
				ctxMsg.ToolCalls = append(ctxMsg.ToolCalls, ctxengine.ToolCallRef{
					ID:       tc.ID,
					Name:     tc.Name,
					ArgsJSON: tc.ArgsJSON,
				})
			}
			if _, err := contextEngine.Ingest(ctx, sessionID, ctxMsg); err != nil {
				log.Printf("context engine ingest turn history session=%s entry=%s err=%v", sessionID, entryID, err)
			}
		}
	}
}

func transcriptTurnResultMeta(meta *agent.TurnResultMetadata) map[string]any {
	if meta == nil {
		return nil
	}
	out := map[string]any{}
	if meta.Outcome != "" {
		out["outcome"] = string(meta.Outcome)
	}
	if meta.StopReason != "" {
		out["stop_reason"] = string(meta.StopReason)
	}
	usage := map[string]any{}
	if meta.Usage.InputTokens > 0 {
		usage["input_tokens"] = meta.Usage.InputTokens
	}
	if meta.Usage.OutputTokens > 0 {
		usage["output_tokens"] = meta.Usage.OutputTokens
	}
	if len(usage) > 0 {
		out["usage"] = usage
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func pendingTurnCreatedAt(pt autoreply.PendingTurn) int64 {
	if pt.CreatedAt > 0 {
		return pt.CreatedAt
	}
	if !pt.EnqueuedAt.IsZero() {
		return pt.EnqueuedAt.Unix()
	}
	return 0
}

type sessionTurnHandoffRegistry struct {
	mu     sync.Mutex
	nextID uint64
	tokens map[string]uint64
}

func newSessionTurnHandoffRegistry() *sessionTurnHandoffRegistry {
	return &sessionTurnHandoffRegistry{tokens: map[string]uint64{}}
}

func (r *sessionTurnHandoffRegistry) Reserve(sessionID string) uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	r.tokens[sessionID] = r.nextID
	return r.nextID
}

func (r *sessionTurnHandoffRegistry) Has(sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.tokens[sessionID]
	return ok
}

func (r *sessionTurnHandoffRegistry) ConsumeIfMatch(sessionID string, token uint64) bool {
	if r == nil || token == 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.tokens[sessionID]; ok && current == token {
		delete(r.tokens, sessionID)
		return true
	}
	return false
}

type eventInFlightRegistry struct {
	mu   sync.Mutex
	keys map[string]int
}

func newEventInFlightRegistry() *eventInFlightRegistry {
	return &eventInFlightRegistry{keys: map[string]int{}}
}

func (r *eventInFlightRegistry) Begin(key string) bool {
	if r == nil || strings.TrimSpace(key) == "" {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys[key] > 0 {
		return false
	}
	r.keys[key] = 1
	return true
}

func (r *eventInFlightRegistry) End(key string) {
	if r == nil || strings.TrimSpace(key) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys[key] <= 1 {
		delete(r.keys, key)
		return
	}
	r.keys[key]--
}

func pendingTurnsShareExecutionContext(pending []autoreply.PendingTurn) bool {
	if len(pending) < 2 {
		return true
	}
	first := pending[0]
	for _, pt := range pending[1:] {
		if strings.TrimSpace(pt.SenderID) != strings.TrimSpace(first.SenderID) {
			return false
		}
		if defaultAgentID(pt.AgentID) != defaultAgentID(first.AgentID) {
			return false
		}
		if strings.TrimSpace(strings.ToLower(pt.ToolProfile)) != strings.TrimSpace(strings.ToLower(first.ToolProfile)) {
			return false
		}
		if len(pt.EnabledTools) != len(first.EnabledTools) {
			return false
		}
		for i := range pt.EnabledTools {
			if strings.TrimSpace(pt.EnabledTools[i]) != strings.TrimSpace(first.EnabledTools[i]) {
				return false
			}
		}
	}
	return true
}

func setSessionActiveTurn(ctx context.Context, docsRepo *state.DocsRepository, sessionID, peerPubKey string, active bool) {
	if err := updateSessionDoc(ctx, docsRepo, sessionID, peerPubKey, func(session *state.SessionDoc) error {
		session.Meta = mergeSessionMeta(session.Meta, map[string]any{"active_turn": active})
		return nil
	}); err != nil {
		log.Printf("session active_turn persist failed session=%s err=%v", sessionID, err)
	}
}

func updateSessionDoc(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	sessionID string,
	peerPubKey string,
	mutate func(*state.SessionDoc) error,
) error {
	_, err := mutateSessionDoc(ctx, docsRepo, sessionID, peerPubKey, true, mutate)
	return err
}

func updateExistingSessionDoc(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	sessionID string,
	peerPubKey string,
	mutate func(*state.SessionDoc) error,
) (state.SessionDoc, error) {
	return mutateSessionDoc(ctx, docsRepo, sessionID, peerPubKey, false, mutate)
}

func replaceSessionDoc(ctx context.Context, docsRepo *state.DocsRepository, sessionID string, doc state.SessionDoc) error {
	return updateSessionDoc(ctx, docsRepo, sessionID, doc.PeerPubKey, func(session *state.SessionDoc) error {
		replacement := doc
		if replacement.Version == 0 {
			replacement.Version = 1
		}
		if strings.TrimSpace(replacement.SessionID) == "" {
			replacement.SessionID = strings.TrimSpace(sessionID)
		}
		if strings.TrimSpace(replacement.PeerPubKey) == "" {
			replacement.PeerPubKey = session.PeerPubKey
		}
		*session = replacement
		return nil
	})
}

func sessionDocUpdateLockFor(sessionID string) *sync.Mutex {
	// Use a small striped lock set to avoid unbounded growth while still
	// serializing concurrent read/modify/write cycles for a given session.
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessionID))
	idx := h.Sum32() % uint32(len(sessionDocUpdateLocks))
	return &sessionDocUpdateLocks[idx]
}

func mutateSessionDoc(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	sessionID string,
	peerPubKey string,
	createIfMissing bool,
	mutate func(*state.SessionDoc) error,
) (state.SessionDoc, error) {
	if docsRepo == nil {
		return state.SessionDoc{}, fmt.Errorf("docs repository is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return state.SessionDoc{}, fmt.Errorf("session id is empty")
	}
	if mutate == nil {
		return state.SessionDoc{}, fmt.Errorf("session mutator is nil")
	}
	mu := sessionDocUpdateLockFor(sessionID)
	mu.Lock()
	defer mu.Unlock()

	session, err := docsRepo.GetSession(ctx, sessionID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			return state.SessionDoc{}, err
		}
		if !createIfMissing {
			return state.SessionDoc{}, err
		}
		session = state.SessionDoc{
			Version:    1,
			SessionID:  sessionID,
			PeerPubKey: strings.TrimSpace(peerPubKey),
			Meta:       map[string]any{},
		}
	}
	if session.Version == 0 {
		session.Version = 1
	}
	if strings.TrimSpace(session.SessionID) == "" {
		session.SessionID = sessionID
	}
	if strings.TrimSpace(session.PeerPubKey) == "" && strings.TrimSpace(peerPubKey) != "" {
		session.PeerPubKey = strings.TrimSpace(peerPubKey)
	}
	if err := mutate(&session); err != nil {
		return state.SessionDoc{}, err
	}
	_, err = docsRepo.PutSession(ctx, sessionID, session)
	return session, err
}

func persistAssistant(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	sessionID string,
	reply string,
	requestEventID string,
) error {
	now := time.Now().Unix()
	if err := updateSessionDoc(ctx, docsRepo, sessionID, sessionID, func(session *state.SessionDoc) error {
		session.LastReplyAt = now
		return nil
	}); err != nil {
		return err
	}

	_, err := transcriptRepo.PutEntry(ctx, state.TranscriptEntryDoc{
		Version:   1,
		SessionID: sessionID,
		EntryID:   fmt.Sprintf("reply:%d:%s", now, requestEventID),
		Role:      "assistant",
		Text:      reply,
		Unix:      now,
		Meta: map[string]any{
			"reply_to_event_id": requestEventID,
		},
	})
	return err
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
	method := strings.TrimSpace(in.Method)
	cfg := configState.Get()
	decision := policy.EvaluateControlCall(in.FromPubKey, method, true, cfg)
	if usageState != nil {
		usageState.RecordControl()
	}
	if !decision.Allowed {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("forbidden")
		}
		if !strings.HasPrefix(strings.ToLower(reason), "forbidden") {
			reason = "forbidden: " + reason
		}
		return nostruntime.ControlRPCResult{}, errors.New(reason)
	}

	switch method {
	case methods.MethodSupportedMethods:
		return nostruntime.ControlRPCResult{Result: supportedMethods(cfg)}, nil
	case methods.MethodHealth:
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil
	case methods.MethodDoctorMemoryStatus:
		indexAvailable := memoryIndex != nil
		entryCount := 0
		sessionCount := 0
		if memoryIndex != nil {
			entryCount = memoryIndex.Count()
			sessionCount = memoryIndex.SessionCount()
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok": true,
			"index": map[string]any{
				"available":     indexAvailable,
				"entry_count":   entryCount,
				"session_count": sessionCount,
			},
		}}, nil
	case methods.MethodLogsTail:
		req, err := methods.DecodeLogsTailParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if logBuffer == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"cursor": req.Cursor, "lines": []string{}, "truncated": false, "reset": false}}, nil
		}
		return nostruntime.ControlRPCResult{Result: logBuffer.Tail(req.Cursor, req.Limit, req.MaxBytes)}, nil
	case methods.MethodRuntimeObserve:
		req, err := methods.DecodeRuntimeObserveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := toolbuiltin.ObserveRuntime(ctx, runtimeObserveToolRequest(req))
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: out}, nil
	case methods.MethodChannelsStatus:
		req, err := methods.DecodeChannelsStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if channelState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []map[string]any{buildNostrChannelStatusRow(map[string]any{}, "channel_state_unavailable")}}}, nil
		}
		status := channelState.Status(dmBus, controlBus, cfg)
		return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []map[string]any{buildNostrChannelStatusRow(status, "")}}}, nil
	case methods.MethodChannelsLogout:
		req, err := methods.DecodeChannelsLogoutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if channelState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel": req.Channel}}, nil
		}
		res, err := channelState.Logout(req.Channel)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: res}, nil
	case methods.MethodChannelsJoin:
		req, err := methods.DecodeChannelsJoinParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("channel runtime not configured")
		}
		ch, chErr := channels.NewNIP29GroupChannel(ctx, channels.NIP29GroupChannelOptions{
			GroupAddress: req.GroupAddress,
			Hub:          controlHub,
			Keyer:        controlKeyer,
			OnMessage: func(msg channels.InboundMessage) {
				emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
					TS:        time.Now().UnixMilli(),
					ChannelID: msg.ChannelID,
					GroupID:   msg.GroupID,
					Relay:     msg.Relay,
					Direction: "inbound",
					From:      msg.FromPubKey,
					Text:      msg.Text,
					EventID:   msg.EventID,
				})
				activeAgentID, rt := resolveInboundChannelRuntime("", msg.ChannelID)
				turnCtx, release := chatCancels.Begin(msg.ChannelID, ctx)
				go func() {
					defer release()
					filteredRuntime, turnExecutor, turnTools := resolveAgentTurnToolSurface(turnCtx, configState.Get(), docsRepo, msg.ChannelID, activeAgentID, rt, tools, turnToolConstraints{})
					result, turnErr := filteredRuntime.ProcessTurn(turnCtx, agent.Turn{
						SessionID: msg.ChannelID,
						UserText:  msg.Text,
						Tools:     turnTools,
						Executor:  turnExecutor,
					})
					if turnErr != nil {
						log.Printf("channel agent turn error channel=%s err=%v", msg.ChannelID, turnErr)
						return
					}
					if err := msg.Reply(turnCtx, result.Text); err != nil {
						log.Printf("channel reply error channel=%s err=%v", msg.ChannelID, err)
						return
					}
					emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
						TS:        time.Now().UnixMilli(),
						ChannelID: msg.ChannelID,
						GroupID:   msg.GroupID,
						Relay:     msg.Relay,
						Direction: "outbound",
						Text:      result.Text,
					})
				}()
			},
			OnError: func(err error) {
				log.Printf("nip29 channel error channel=%s err=%v", req.GroupAddress, err)
			},
		})
		if chErr != nil {
			return nostruntime.ControlRPCResult{}, chErr
		}
		if err := controlChannels.Add(ch); err != nil {
			ch.Close()
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":         true,
			"channel_id": ch.ID(),
			"type":       ch.Type(),
		}}, nil
	case methods.MethodChannelsLeave:
		req, err := methods.DecodeChannelsLeaveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("channel runtime not configured")
		}
		if err := controlChannels.Remove(req.ChannelID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel_id": req.ChannelID}}, nil
	case methods.MethodChannelsList:
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"channels": []any{}}}, nil
		}
		list := controlChannels.List()
		return nostruntime.ControlRPCResult{Result: map[string]any{"channels": list, "count": len(list)}}, nil
	case methods.MethodChannelsSend:
		req, err := methods.DecodeChannelsSendParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlChannels == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("channel runtime not configured")
		}
		ch, ok := controlChannels.Get(req.ChannelID)
		if !ok {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("channel %q not found; join it first with channels.join", req.ChannelID)
		}
		if err := ch.Send(ctx, req.Text); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		emitControlWSEvent(gatewayws.EventChannelMessage, gatewayws.ChannelMessagePayload{
			TS:        time.Now().UnixMilli(),
			ChannelID: req.ChannelID,
			Direction: "outbound",
			Text:      req.Text,
		})
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "channel_id": req.ChannelID}}, nil
	case methods.MethodStatus:
		pubkey := ""
		if dmBus != nil {
			pubkey = dmBus.PublicKey()
		}
		var subs []methods.SubHealthInfo
		if controlBus != nil {
			subs = append(subs, subHealthToInfo(controlBus.HealthSnapshot()))
		}
		if dmBus != nil {
			if reporter, ok := dmBus.(nostruntime.SubHealthReporter); ok {
				subs = append(subs, subHealthToInfo(reporter.HealthSnapshot()))
			}
		}
		if dvmHandler != nil {
			subs = append(subs, subHealthToInfo(dvmHandler.HealthSnapshot()))
		}
		// Collect relay sets for status response.
		var relaySets map[string][]string
		if relaySetRegistry != nil {
			all := relaySetRegistry.All()
			if len(all) > 0 {
				relaySets = make(map[string][]string, len(all))
				for dtag, entry := range all {
					relaySets[dtag] = entry.Relays
				}
			}
		}
		var mcpSnapshot *mcppkg.TelemetrySnapshot
		if controlMCPOps != nil {
			mcpSnapshot = controlMCPOps.telemetrySnapshotPtr()
		}
		return nostruntime.ControlRPCResult{Result: methods.StatusResponse{
			PubKey:        pubkey,
			Relays:        cfg.Relays.Read,
			DMPolicy:      cfg.DM.Policy,
			UptimeSeconds: int(time.Since(startedAt).Seconds()),
			UptimeMS:      time.Since(startedAt).Milliseconds(),
			Version:       "metiqd",
			Subscriptions: subs,
			RelaySets:     relaySets,
			MCP:           mcpSnapshot,
		}}, nil
	case methods.MethodUsageStatus:
		if usageState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "totals": map[string]any{"requests": 0, "tokens": 0}}}, nil
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "totals": usageState.Status()}}, nil
	case methods.MethodUsageCost:
		req, err := methods.DecodeUsageCostParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if usageState == nil {
			return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "total_usd": 0, "filtered": false}}, nil
		}
		if req.StartDate != "" || req.EndDate != "" || req.Days > 0 {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("usage.cost: date filtering is not supported")
		}
		cost := usageState.Cost()
		result := map[string]any{"ok": true, "total_usd": cost["total_usd"], "estimate": cost, "filtered": false}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodMemorySearch:
		req, err := methods.DecodeMemorySearchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.MemorySearchResponse{Results: memoryIndex.Search(req.Query, req.Limit)}}, nil

	case methods.MethodMemoryCompact:
		var compactReq methods.MemoryCompactRequest
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &compactReq)
		}
		if controlContextEngine == nil {
			return nostruntime.ControlRPCResult{Result: methods.MemoryCompactResponse{OK: false, Summary: "no context engine active"}}, nil
		}
		sessionToCompact := compactReq.SessionID
		flushOutcome, err := ensureSessionMemoryCurrent(ctx, configState.Get(), sessionToCompact, controlSessionStore)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("memory.compact session memory flush: %w", err)
		}
		cr, cErr := controlContextEngine.Compact(ctx, sessionToCompact)
		if cErr != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("memory.compact: %w", cErr)
		}
		recordSessionCompaction(controlSessionStore, sessionToCompact, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
		return nostruntime.ControlRPCResult{Result: methods.MemoryCompactResponse{
			OK:           cr.OK,
			SessionsRun:  1,
			TokensBefore: cr.TokensBefore,
			TokensAfter:  cr.TokensAfter,
			Summary:      cr.Summary,
		}}, nil
	case methods.MethodAgent:
		req, err := methods.DecodeAgentParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if req.SessionID == "" {
			req.SessionID = in.FromPubKey
		}
		if controlAgentJobs == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agent runtime not configured")
		}
		// Route to the agent assigned to this session, falling back to the default runtime.
		var rt agent.Runtime
		if controlSessionRouter != nil && controlAgentRegistry != nil {
			activeAgentID := controlSessionRouter.Get(req.SessionID)
			rt = controlAgentRegistry.Get(activeAgentID)
		}
		if rt == nil {
			rt = controlAgentRuntime
		}
		if rt == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agent runtime not configured")
		}
		activeAgentID := ""
		if controlSessionRouter != nil {
			activeAgentID = controlSessionRouter.Get(req.SessionID)
		}
		// Apply profile-based tool filtering when the agent has a non-full profile.
		rt = applyAgentProfileFilter(ctx, rt, req.SessionID, cfg, docsRepo)
		// Build fallback runtimes from the active agent's FallbackModels list.
		var fallbackRuntimes []agent.Runtime
		primaryLabel := strings.TrimSpace(cfg.Agent.DefaultModel)
		if primaryLabel == "" {
			primaryLabel = "primary"
		}
		runtimeLabels := []string{primaryLabel}
		if agCfg, ok := resolveAgentConfigByID(cfg, activeAgentID); ok {
			if strings.TrimSpace(agCfg.Model) != "" {
				primaryLabel = strings.TrimSpace(agCfg.Model)
				runtimeLabels[0] = primaryLabel
			}
			for _, fbModel := range agCfg.FallbackModels {
				fbModel = strings.TrimSpace(fbModel)
				if fbModel == "" {
					continue
				}
				fbRt, fbErr := buildRuntimeForAgentModel(cfg, agCfg, fbModel, controlToolRegistry)
				if fbErr == nil && fbRt != nil {
					fbRt = applyAgentProfileFilterForAgent(ctx, fbRt, activeAgentID, cfg, docsRepo)
					fallbackRuntimes = append(fallbackRuntimes, fbRt)
					runtimeLabels = append(runtimeLabels, fbModel)
				}
			}
		}
		runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
		snapshot := controlAgentJobs.Begin(runID, req.SessionID)
		go executeAgentRunWithFallbacks(runID, req, rt, fallbackRuntimes, runtimeLabels, memoryIndex, controlAgentJobs)
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"run_id": runID, "status": "accepted", "accepted_at": snapshot.StartedAt})}, nil
	case methods.MethodAgentWait:
		req, err := methods.DecodeAgentWaitParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlAgentJobs == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agent runtime not configured")
		}
		snap, ok := controlAgentJobs.Wait(ctx, req.RunID, time.Duration(req.TimeoutMS)*time.Millisecond)
		if !ok {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("run not found")
		}
		if snap.Status == "pending" {
			return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"run_id": req.RunID, "status": "timeout"})}, nil
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
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodAgentIdentityGet:
		req, err := methods.DecodeAgentIdentityParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agentID := strings.TrimSpace(req.AgentID)
		sessionID := strings.TrimSpace(req.SessionID)
		if sessionID == "" {
			sessionID = in.FromPubKey
		}
		if agentID == "" && controlSessionRouter != nil {
			agentID = controlSessionRouter.Get(sessionID)
		}
		if agentID == "" {
			agentID = "main"
		}
		displayName := "Metiq Agent"
		if docsRepo != nil {
			if ag, err2 := docsRepo.GetAgent(ctx, agentID); err2 == nil && ag.Name != "" {
				displayName = ag.Name
			}
		}
		pubkey := strings.TrimSpace(in.FromPubKey)
		if dmBus != nil {
			pubkey = dmBus.PublicKey()
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agent_id": agentID, "display_name": displayName, "session_id": sessionID, "pubkey": pubkey})}, nil
	case methods.MethodGatewayIdentityGet:
		pubkey := strings.TrimSpace(in.FromPubKey)
		if dmBus != nil {
			pubkey = dmBus.PublicKey()
		}
		deviceID := pubkey
		if len(deviceID) > 24 {
			deviceID = deviceID[:24]
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"deviceId": deviceID, "publicKey": pubkey, "pubkey": pubkey}}, nil
	case methods.MethodChatSend:
		req, err := methods.DecodeChatSendParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Preprocess any media attachments: transcribe audio, extract PDF text,
		// resolve image references. The augmented text is sent via DM; image
		// refs are currently logged (vision would require direct ProcessTurn access
		// which the RPC handler does not have without threading the runtime through).
		msgText := req.Text
		if len(req.Attachments) > 0 {
			var preprocessErr error
			msgText, _, preprocessErr = preprocessAttachments(ctx, req.Text, req.Attachments, controlMediaTranscriber)
			if preprocessErr != nil {
				log.Printf("chat.send: attachment preprocess error: %v", preprocessErr)
			}
		}
		if msgText == "" {
			msgText = req.Text
		}
		sendCtx := ctx
		release := func() {}
		if chatCancels != nil {
			sendCtx, release = chatCancels.Begin(req.To, ctx)
			defer release()
		}
		if err := dmBus.SendDM(sendCtx, req.To, msgText); err != nil {
			if errors.Is(err, context.Canceled) {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("chat aborted")
			}
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil
	case methods.MethodChatHistory:
		req, err := methods.DecodeChatHistoryParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.GetSession(ctx, req.SessionID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		transcript, err := transcriptRepo.ListSession(ctx, req.SessionID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"session_id": req.SessionID, "entries": transcript})}, nil
	case methods.MethodChatAbort:
		req, err := methods.DecodeChatAbortParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		aborted := 0
		if chatCancels != nil {
			if strings.TrimSpace(req.SessionID) == "" {
				aborted = chatCancels.AbortAll()
			} else if chatCancels.Abort(req.SessionID) {
				aborted = 1
			}
		}
		if usageState != nil {
			usageState.RecordAbort(aborted)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "aborted": aborted > 0, "aborted_count": aborted})}, nil
	case methods.MethodSessionGet:
		req, err := methods.DecodeSessionGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		session, err := docsRepo.GetSession(ctx, req.SessionID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		transcript, err := transcriptRepo.ListSession(ctx, req.SessionID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.SessionGetResponse{Session: session, Transcript: transcript}}, nil
	case methods.MethodSessionsList:
		req, err := methods.DecodeSessionsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		result, err := admin.BuildSessionsListResponse(ctx, req, admin.SessionsListResponseOptions{
			Config:         cfg,
			SessionStore:   controlSessionStore,
			ListSessions:   docsRepo.ListSessions,
			ListTranscript: transcriptRepo.ListSession,
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodSessionsPreview:
		req, err := methods.DecodeSessionsPreviewParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		session, err := docsRepo.GetSession(ctx, req.SessionID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		transcript, err := transcriptRepo.ListSession(ctx, req.SessionID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"session": session, "preview": transcript}}, nil
	case methods.MethodSessionsPatch:
		req, err := methods.DecodeSessionsPatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		session, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, req.Meta)
			return nil
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session": session}}, nil
	case methods.MethodSessionsReset:
		req, err := methods.DecodeSessionsResetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		var session state.SessionDoc
		if chatCancels != nil {
			chatCancels.Abort(req.SessionID)
		}
		err = withExclusiveSessionTurn(ctx, req.SessionID, 15*time.Second, func() error {
			var innerErr error
			session, innerErr = updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.LastInboundAt = 0
				session.LastReplyAt = 0
				session.Meta = map[string]any{}
				return nil
			})
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Fire hook event.
		if controlHooksMgr != nil {
			go controlHooksMgr.Fire("command:reset", req.SessionID, map[string]any{})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "session": session}}, nil
	case methods.MethodSessionsDelete:
		req, err := methods.DecodeSessionsDeleteParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if chatCancels != nil {
			chatCancels.Abort(req.SessionID)
		}
		err = withExclusiveSessionTurn(ctx, req.SessionID, 15*time.Second, func() error {
			_, innerErr := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{"deleted": true, "deleted_at": time.Now().Unix()})
				return nil
			})
			return innerErr
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "deleted": true})}, nil
	case methods.MethodSessionsCompact:
		req, err := methods.DecodeSessionsCompactParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		var compactResult map[string]any
		if chatCancels != nil {
			chatCancels.Abort(req.SessionID)
		}
		err = withExclusiveSessionTurn(ctx, req.SessionID, 15*time.Second, func() error {
			if _, err := docsRepo.GetSession(ctx, req.SessionID); err != nil {
				return err
			}
			flushOutcome, err := ensureSessionMemoryCurrent(ctx, configState.Get(), req.SessionID, controlSessionStore)
			if err != nil {
				return fmt.Errorf("sessions.compact session memory flush: %w", err)
			}
			entries, err := transcriptRepo.ListSessionAll(ctx, req.SessionID)
			if err != nil {
				return err
			}
			dropped := len(entries) - req.Keep
			if dropped < 0 {
				dropped = 0
			}

			// ── LLM summary generation ─────────────────────────────────────────
			// Before tombstoning, generate a compact summary of the entries that
			// are about to be removed and inject it as a system-role entry.
			summaryGenerated := false
			activeAgentID, summaryRuntime := resolveInboundChannelRuntime("", req.SessionID)
			if dropped > 0 && summaryRuntime != nil {
				compactedEntries := entries[:dropped]
				var sb strings.Builder
				for _, e := range compactedEntries {
					if e.Role == "deleted" {
						continue
					}
					sb.WriteString(e.Role)
					sb.WriteString(": ")
					text := e.Text
					if len(text) > 400 {
						text = text[:400] + "…"
					}
					sb.WriteString(text)
					sb.WriteString("\n")
				}
				snippet := sb.String()
				if len(snippet) > 6000 {
					snippet = snippet[:6000] + "…"
				}
				if snippet != "" {
					summaryPrompt := "You are a session-memory assistant. Summarize the following conversation history concisely in 2-4 sentences, capturing the key topics, decisions, and context needed to continue the conversation later. Do NOT include greetings or meta-commentary; only output the summary.\n\n" + snippet
					selectedRuntime := summaryRuntime
					usedAuxiliaryRuntime := false
					if agCfg, ok := resolveAgentConfigByID(cfg, activeAgentID); ok {
						if auxiliaryModel := resolveAuxiliaryModelForAgent(agCfg, auxiliaryModelUseCaseCompaction); auxiliaryModel != "" {
							lightRuntime, lightErr := buildRuntimeForAgentModel(cfg, agCfg, auxiliaryModel, controlToolRegistry)
							if lightErr != nil {
								log.Printf("sessions.compact: light summary runtime unavailable agent=%s model=%q err=%v", activeAgentID, auxiliaryModel, lightErr)
							} else if lightRuntime != nil {
								selectedRuntime = lightRuntime
								usedAuxiliaryRuntime = true
							}
						}
					}
					runSummary := func(rt agent.Runtime) (agent.TurnResult, error) {
						summaryCtx, summaryCancel := context.WithTimeout(ctx, 30*time.Second)
						defer summaryCancel()
						return rt.ProcessTurn(summaryCtx, agent.Turn{
							SessionID: req.SessionID + ":compact",
							UserText:  summaryPrompt,
						})
					}
					result, summaryErr := runSummary(selectedRuntime)
					if summaryErr != nil && usedAuxiliaryRuntime && summaryRuntime != nil {
						log.Printf("sessions.compact: light summary failed agent=%s err=%v — retrying primary runtime", activeAgentID, summaryErr)
						result, summaryErr = runSummary(summaryRuntime)
					}
					if summaryErr == nil && strings.TrimSpace(result.Text) != "" {
						summaryEntryID := fmt.Sprintf("compact-summary-%d", time.Now().UnixMilli())
						summaryEntry := state.TranscriptEntryDoc{
							Version:   1,
							SessionID: req.SessionID,
							EntryID:   summaryEntryID,
							Role:      "system",
							Text:      "[Compact summary of " + strconv.Itoa(dropped) + " earlier messages]\n\n" + strings.TrimSpace(result.Text),
							Unix:      time.Now().Unix(),
							Meta:      map[string]any{"compact": true, "compact_from": dropped},
						}
						if _, putErr := transcriptRepo.PutEntry(ctx, summaryEntry); putErr != nil {
							log.Printf("sessions.compact: insert summary entry: %v", putErr)
						} else {
							summaryGenerated = true
						}
					} else if summaryErr != nil {
						log.Printf("sessions.compact: LLM summary skipped: %v", summaryErr)
					}
				}
			}

			deleteErrors := 0
			for i := 0; i < dropped; i++ {
				if delErr := transcriptRepo.DeleteEntry(ctx, req.SessionID, entries[i].EntryID); delErr != nil {
					log.Printf("sessions.compact: delete entry %s: %v", entries[i].EntryID, delErr)
					deleteErrors++
				}
			}
			if _, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{
					"compacted_at":              time.Now().Unix(),
					"compacted_keep":            req.Keep,
					"compacted_from_entries":    len(entries),
					"compacted_dropped_entries": dropped - deleteErrors,
					"compacted_summary":         summaryGenerated,
				})
				return nil
			}); err != nil {
				return err
			}
			compactResult = methods.ApplyCompatResponseAliases(map[string]any{"ok": true, "session_id": req.SessionID, "kept": req.Keep, "from_entries": len(entries), "dropped": dropped - deleteErrors, "summary_generated": summaryGenerated})
			recordSessionCompaction(controlSessionStore, req.SessionID, strings.TrimSpace(flushOutcome.Path) != "", time.Now())
			return nil
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: compactResult}, nil
	case methods.MethodSessionsExport:
		var exportReq methods.SessionsExportRequest
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &exportReq)
		}
		if exportReq.SessionID == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("sessions.export: session_id is required")
		}
		exportFormat := strings.ToLower(strings.TrimSpace(exportReq.Format))
		if exportFormat == "" {
			exportFormat = "html"
		}
		if exportFormat != "html" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("sessions.export: unsupported format %q (only 'html' is supported)", exportFormat)
		}
		// Load transcript entries for the session.
		entries, err := transcriptRepo.ListSessionAll(ctx, exportReq.SessionID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("sessions.export: load transcript: %w", err)
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
		// Resolve agent name from registry.
		agentName := ""
		if agDoc, err2 := docsRepo.GetAgent(ctx, "main"); err2 == nil {
			agentName = agDoc.Name
		}
		htmlOut, err := exportpkg.SessionToHTML(exportpkg.SessionHTMLOptions{
			SessionID:  exportReq.SessionID,
			AgentID:    "main",
			AgentName:  agentName,
			PubKey:     in.FromPubKey,
			Messages:   msgs,
			ExportedAt: time.Now(),
		})
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("sessions.export: render: %w", err)
		}
		return nostruntime.ControlRPCResult{Result: methods.SessionsExportResponse{HTML: htmlOut, Format: "html"}}, nil

	case methods.MethodSessionsPrune:
		var pruneReq methods.SessionsPruneRequest
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &pruneReq)
		}
		result, err := runSessionsPrune(ctx, docsRepo, transcriptRepo, pruneReq, "manual")
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: result}, nil

	case methods.MethodSessionsSpawn:
		req, err := methods.DecodeSessionsSpawnParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySessionsSpawn(ctx, req, cfg, docsRepo, memoryIndex)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil

	case methods.MethodAgentsList:
		req, err := methods.DecodeAgentsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agents, err := docsRepo.ListAgents(ctx, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"agents": agents}}, nil
	case methods.MethodAgentsCreate:
		req, err := methods.DecodeAgentsCreateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.GetAgent(ctx, req.AgentID); err == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agent %q already exists", req.AgentID)
		} else if !errors.Is(err, state.ErrNotFound) {
			return nostruntime.ControlRPCResult{}, err
		}
		doc := state.AgentDoc{Version: 1, AgentID: req.AgentID, Name: req.Name, Workspace: req.Workspace, Model: req.Model, Meta: req.Meta}
		if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Register a runtime for the new agent (best-effort; falls back to default on failure).
		if controlAgentRegistry != nil {
			if rt, rtErr := agent.BuildRuntimeForModel(req.Model, tools); rtErr == nil {
				controlAgentRegistry.Set(req.AgentID, rt)
			} else {
				log.Printf("agents.create: runtime build warning id=%s model=%q err=%v", req.AgentID, req.Model, rtErr)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent": doc}}, nil
	case methods.MethodAgentsUpdate:
		req, err := methods.DecodeAgentsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		doc, err := docsRepo.GetAgent(ctx, req.AgentID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
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
		if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Rebuild the runtime if the model changed.
		if controlAgentRegistry != nil && req.Model != "" {
			if rt, rtErr := agent.BuildRuntimeForModel(doc.Model, tools); rtErr == nil {
				controlAgentRegistry.Set(req.AgentID, rt)
			} else {
				log.Printf("agents.update: runtime rebuild warning id=%s model=%q err=%v", req.AgentID, doc.Model, rtErr)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent": doc}}, nil
	case methods.MethodAgentsDelete:
		req, err := methods.DecodeAgentsDeleteParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		doc, err := docsRepo.GetAgent(ctx, req.AgentID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		doc.Deleted = true
		doc.Meta = mergeSessionMeta(doc.Meta, map[string]any{"deleted_at": time.Now().Unix()})
		if _, err := docsRepo.PutAgent(ctx, req.AgentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Remove the runtime and any session assignments for the deleted agent.
		if controlAgentRegistry != nil {
			controlAgentRegistry.Remove(req.AgentID)
		}
		if controlSessionRouter != nil {
			// Un-assign all sessions that were using this agent.
			for sessionID, aid := range controlSessionRouter.List() {
				if aid == req.AgentID {
					controlSessionRouter.Unassign(sessionID)
				}
			}
		}
		// Hard cleanup: remove persisted session.meta["agent_id"] references to
		// this agent so routes do not reappear after restarts.
		sessions, sessErr := docsRepo.ListSessions(ctx, 5000)
		if sessErr != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("agents.delete: list sessions for cleanup: %w", sessErr)
		}
		for _, sess := range sessions {
			if sess.Meta == nil {
				continue
			}
			aid, _ := sess.Meta["agent_id"].(string)
			if aid != req.AgentID {
				continue
			}
			sessionID := strings.TrimSpace(sess.SessionID)
			if sessionID == "" {
				continue
			}
			if _, err := updateExistingSessionDoc(ctx, docsRepo, sessionID, sess.PeerPubKey, func(session *state.SessionDoc) error {
				if session.Meta != nil {
					delete(session.Meta, "agent_id")
				}
				return nil
			}); err != nil {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("agents.delete: cleanup session %q: %w", sessionID, err)
			}
			if controlSessionRouter != nil {
				controlSessionRouter.Unassign(sessionID)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent_id": req.AgentID, "deleted": true}}, nil
	case methods.MethodAgentsAssign:
		req, err := methods.DecodeAgentsAssignParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Validate that the target agent exists and is not deleted.
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlSessionRouter != nil {
			controlSessionRouter.Assign(req.SessionID, req.AgentID)
		}
		// Persist assignment in session meta so it survives restarts.
		persisted := true
		if err := updateSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			session.Meta = mergeSessionMeta(session.Meta, map[string]any{"agent_id": req.AgentID})
			return nil
		}); err != nil {
			persisted = false
			log.Printf("agents.assign: persist session meta warning session=%s err=%v", req.SessionID, err)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":         true,
			"agent_id":   req.AgentID,
			"persisted":  persisted,
			"durability": "best_effort",
		}}, nil
	case methods.MethodAgentsUnassign:
		req, err := methods.DecodeAgentsUnassignParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlSessionRouter != nil {
			controlSessionRouter.Unassign(req.SessionID)
		}
		// Remove the persisted agent_id from session meta.
		persisted := true
		if _, err := updateExistingSessionDoc(ctx, docsRepo, req.SessionID, "", func(session *state.SessionDoc) error {
			if session.Meta != nil {
				delete(session.Meta, "agent_id")
			}
			return nil
		}); err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				persisted = false
				log.Printf("agents.unassign: load session warning session=%s err=%v", req.SessionID, err)
			}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":         true,
			"session_id": req.SessionID,
			"persisted":  persisted,
			"durability": "best_effort",
		}}, nil
	case methods.MethodAgentsActive:
		req, err := methods.DecodeAgentsActiveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		var registered []string
		if controlAgentRegistry != nil {
			registered = controlAgentRegistry.Registered()
			sort.Strings(registered)
		}
		var assignments []map[string]any
		if controlSessionRouter != nil {
			for sessionID, agentID := range controlSessionRouter.List() {
				assignments = append(assignments, map[string]any{
					"session_id": sessionID,
					"agent_id":   agentID,
				})
			}
			sortRecordsByKeyDesc(assignments, "session_id")
		}
		if req.Limit > 0 && len(assignments) > req.Limit {
			assignments = assignments[:req.Limit]
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"registered":  registered,
			"assignments": assignments,
		}}, nil
	case methods.MethodAgentsFilesList:
		req, err := methods.DecodeAgentsFilesListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		files, err := docsRepo.ListAgentFiles(ctx, req.AgentID, req.Limit)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out := make([]map[string]any, 0, len(files))
		for _, file := range files {
			out = append(out, map[string]any{"name": file.Name, "size": len(file.Content)})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"agent_id": req.AgentID, "files": out}}, nil
	case methods.MethodAgentsFilesGet:
		req, err := methods.DecodeAgentsFilesGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		file, err := docsRepo.GetAgentFile(ctx, req.AgentID, req.Name)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nostruntime.ControlRPCResult{Result: map[string]any{"agent_id": req.AgentID, "file": map[string]any{"name": req.Name, "missing": true}}}, nil
			}
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"agent_id": req.AgentID, "file": map[string]any{"name": file.Name, "missing": false, "content": file.Content}}}, nil
	case methods.MethodAgentsFilesSet:
		req, err := methods.DecodeAgentsFilesSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		doc := state.AgentFileDoc{Version: 1, AgentID: req.AgentID, Name: req.Name, Content: req.Content}
		if _, err := docsRepo.PutAgentFile(ctx, req.AgentID, req.Name, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "agent_id": req.AgentID, "file": map[string]any{"name": req.Name, "missing": false, "content": req.Content}}}, nil
	case methods.MethodModelsList:
		req, err := methods.DecodeModelsListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"models": defaultModelsCatalog(cfg.Providers)}}, nil
	case methods.MethodToolsCatalog:
		req, err := methods.DecodeToolsCatalogParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agentID := defaultAgentID(req.AgentID)
		groups := buildToolCatalogGroups(cfg, tools, req.IncludePlugins, pluginMgr)
		if req.Profile != nil && *req.Profile != "" {
			profileID := strings.TrimSpace(strings.ToLower(*req.Profile))
			if agent.LookupProfile(profileID) == nil {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("unknown profile %q; valid: %s", profileID, strings.Join(agent.ProfileListSorted(), ", "))
			}
			groups = agent.FilterCatalogByProfile(groups, profileID)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profiles": defaultToolProfiles(), "groups": groups})}, nil
	case methods.MethodToolsProfileGet:
		req, err := methods.DecodeToolsProfileGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agentID := defaultAgentID(req.AgentID)
		doc, _ := docsRepo.GetAgent(ctx, agentID)
		profileID := agent.DefaultProfile
		if p, ok := doc.Meta[agent.AgentProfileKey].(string); ok && p != "" {
			profileID = p
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profile": profileID})}, nil
	case methods.MethodToolsProfileSet:
		req, err := methods.DecodeToolsProfileSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if agent.LookupProfile(req.Profile) == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("unknown profile %q; valid: %s", req.Profile, strings.Join(agent.ProfileListSorted(), ", "))
		}
		agentID := defaultAgentID(req.AgentID)
		doc, _ := docsRepo.GetAgent(ctx, agentID)
		if doc.AgentID == "" {
			doc = state.AgentDoc{Version: 1, AgentID: agentID}
		}
		if doc.Meta == nil {
			doc.Meta = map[string]any{}
		}
		doc.Meta[agent.AgentProfileKey] = req.Profile
		if _, err := docsRepo.PutAgent(ctx, agentID, doc); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(map[string]any{"agentId": agentID, "profile": req.Profile})}, nil
	case methods.MethodSkillsStatus:
		req, err := methods.DecodeSkillsStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := isKnownAgentID(ctx, docsRepo, req.AgentID); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		agentID := defaultAgentID(req.AgentID)
		return nostruntime.ControlRPCResult{Result: buildSkillsStatusReport(cfg, agentID)}, nil
	case methods.MethodSkillsBins:
		req, err := methods.DecodeSkillsBinsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: applySkillsBins(cfg)}, nil
	case methods.MethodSkillsInstall:
		req, err := methods.DecodeSkillsInstallParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		_, installResult, err := applySkillInstall(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: installResult}, nil
	case methods.MethodSkillsUpdate:
		req, err := methods.DecodeSkillsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		_, entry, err := applySkillUpdate(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "skillKey": strings.ToLower(strings.TrimSpace(req.SkillKey)), "config": entry}}, nil
	case methods.MethodPluginsInstall:
		req, err := methods.DecodePluginsInstallParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyPluginInstallRuntime(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsUninstall:
		req, err := methods.DecodePluginsUninstallParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyPluginUninstallRuntime(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsUpdate:
		req, err := methods.DecodePluginsUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyPluginUpdateRuntime(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsRegistryList:
		req, err := methods.DecodePluginsRegistryListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := handlePluginsRegistryList(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsRegistryGet:
		req, err := methods.DecodePluginsRegistryGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := handlePluginsRegistryGet(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodPluginsRegistrySearch:
		req, err := methods.DecodePluginsRegistrySearchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := handlePluginsRegistrySearch(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairRequest:
		req, err := methods.DecodeNodePairRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairRequest(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		requestID := ""
		if id, ok := out["request_id"].(string); ok {
			requestID = id
		}
		emitControlWSEvent(gatewayws.EventNodePairRequested, gatewayws.NodePairRequestedPayload{
			TS:        time.Now().UnixMilli(),
			RequestID: requestID,
			Label:     req.DisplayName,
		})
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairList:
		req, err := methods.DecodeNodePairListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairList(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairApprove:
		req, err := methods.DecodeNodePairApproveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairApprove(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		nodeID := ""
		approvalToken := ""
		if node, ok := out["node"].(map[string]any); ok {
			if id, ok := node["node_id"].(string); ok {
				nodeID = id
			}
			if tok, ok := node["token"].(string); ok {
				approvalToken = tok
			}
		}
		emitControlWSEvent(gatewayws.EventNodePairResolved, gatewayws.NodePairResolvedPayload{
			TS:        time.Now().UnixMilli(),
			RequestID: req.RequestID,
			NodeID:    nodeID,
			Decision:  "approved",
		})
		// Notify the node via NIP-17 DM if node_id looks like a Nostr pubkey.
		if nodeID != "" && approvalToken != "" {
			go sendControlDM(ctx, nodeID, fmt.Sprintf(`{"type":"pair.approved","request_id":%q,"token":%q}`, req.RequestID, approvalToken))
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairReject:
		req, err := methods.DecodeNodePairRejectParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairReject(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		nodeID := ""
		if id, ok := out["node_id"].(string); ok {
			nodeID = id
		}
		emitControlWSEvent(gatewayws.EventNodePairResolved, gatewayws.NodePairResolvedPayload{
			TS:        time.Now().UnixMilli(),
			RequestID: req.RequestID,
			NodeID:    nodeID,
			Decision:  "rejected",
		})
		// Notify the node via NIP-17 DM if node_id looks like a Nostr pubkey.
		if nodeID != "" {
			go sendControlDM(ctx, nodeID, fmt.Sprintf(`{"type":"pair.rejected","request_id":%q}`, req.RequestID))
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePairVerify:
		req, err := methods.DecodeNodePairVerifyParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodePairVerify(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDevicePairList:
		req, err := methods.DecodeDevicePairListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDevicePairList(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDevicePairApprove:
		req, err := methods.DecodeDevicePairApproveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDevicePairApprove(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		deviceID := ""
		label := ""
		if device, ok := out["device"].(map[string]any); ok {
			if id, ok := device["id"].(string); ok {
				deviceID = id
			}
			if l, ok := device["label"].(string); ok {
				label = l
			}
		}
		emitControlWSEvent(gatewayws.EventDevicePairResolved, gatewayws.DevicePairResolvedPayload{
			TS:       time.Now().UnixMilli(),
			DeviceID: deviceID,
			Label:    label,
			Decision: "approved",
		})
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDevicePairReject:
		req, err := methods.DecodeDevicePairRejectParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDevicePairReject(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		deviceID := ""
		if device, ok := out["device"].(map[string]any); ok {
			if id, ok := device["id"].(string); ok {
				deviceID = id
			}
		}
		emitControlWSEvent(gatewayws.EventDevicePairResolved, gatewayws.DevicePairResolvedPayload{
			TS:       time.Now().UnixMilli(),
			DeviceID: deviceID,
			Decision: "rejected",
		})
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDevicePairRemove:
		req, err := methods.DecodeDevicePairRemoveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDevicePairRemove(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDeviceTokenRotate:
		req, err := methods.DecodeDeviceTokenRotateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDeviceTokenRotate(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodDeviceTokenRevoke:
		req, err := methods.DecodeDeviceTokenRevokeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyDeviceTokenRevoke(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeList:
		req, err := methods.DecodeNodeListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeList(configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeDescribe:
		req, err := methods.DecodeNodeDescribeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeDescribe(configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeRename:
		req, err := methods.DecodeNodeRenameParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeRename(ctx, docsRepo, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeCanvasCapabilityRefresh:
		req, err := methods.DecodeNodeCanvasCapabilityRefreshParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeCanvasCapabilityRefresh(configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeInvoke:
		req, err := methods.DecodeNodeInvokeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeInvoke(controlNodeInvocations, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		// Dispatch the invocation to the target node via NIP-17 DM if its
		// node_id looks like a Nostr pubkey (hex or npub).
		if req.NodeID != "" {
			runID, _ := out["run_id"].(string)
			payload, _ := json.Marshal(map[string]any{
				"type":    "node.invoke",
				"run_id":  runID,
				"command": req.Command,
				"args":    req.Args,
			})
			go sendControlDM(ctx, req.NodeID, string(payload))
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeEvent:
		req, err := methods.DecodeNodeEventParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeEvent(controlNodeInvocations, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodeResult, methods.MethodNodeInvokeResult:
		req, err := methods.DecodeNodeResultParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyNodeResult(controlNodeInvocations, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePendingEnqueue:
		req, err := methods.DecodeNodePendingEnqueueParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := controlNodePending.Enqueue(nodepending.EnqueueRequest{NodeID: req.NodeID, Command: req.Command, Args: req.Args, IdempotencyKey: req.IdempotencyKey, TTLMS: req.TTLMS})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePendingPull:
		req, err := methods.DecodeNodePendingPullParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := controlNodePending.Pull(req.NodeID)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePendingAck:
		req, err := methods.DecodeNodePendingAckParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := controlNodePending.Ack(nodepending.AckRequest{NodeID: req.NodeID, IDs: req.IDs})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodNodePendingDrain:
		req, err := methods.DecodeNodePendingDrainParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := controlNodePending.Drain(nodepending.DrainRequest{NodeID: req.NodeID, MaxItems: req.MaxItems})
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCanvasGet:
		var req methods.CanvasGetRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("invalid params: %w", err)
		}
		c := controlCanvas.GetCanvas(req.ID)
		if c == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("canvas %q not found", req.ID)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"canvas": c}}, nil
	case methods.MethodCanvasList:
		canvases := controlCanvas.ListCanvases()
		return nostruntime.ControlRPCResult{Result: map[string]any{"canvases": canvases, "count": len(canvases)}}, nil
	case methods.MethodCanvasUpdate:
		var req methods.CanvasUpdateRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("invalid params: %w", err)
		}
		if err := controlCanvas.UpdateCanvas(req.ID, req.ContentType, req.Data); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "canvas_id": req.ID}}, nil
	case methods.MethodCanvasDelete:
		var req methods.CanvasDeleteRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("invalid params: %w", err)
		}
		removed := controlCanvas.DeleteCanvas(req.ID)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "removed": removed, "canvas_id": req.ID}}, nil
	case methods.MethodCronList:
		req, err := methods.DecodeCronListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronList(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronStatus:
		req, err := methods.DecodeCronStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronStatus(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronAdd:
		req, err := methods.DecodeCronAddParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronAdd(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron jobs save warning (add): %v", saveErr)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronUpdate:
		req, err := methods.DecodeCronUpdateParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronUpdate(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron jobs save warning (update): %v", saveErr)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronRemove:
		req, err := methods.DecodeCronRemoveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronRemove(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if saveErr := controlCronJobs.Save(ctx, docsRepo); saveErr != nil {
			log.Printf("cron jobs save warning (remove): %v", saveErr)
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronRun:
		req, err := methods.DecodeCronRunParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronRun(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodCronRuns:
		req, err := methods.DecodeCronRunsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyCronRuns(controlCronJobs, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalsGet:
		req, err := methods.DecodeExecApprovalsGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalsGet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalsSet:
		req, err := methods.DecodeExecApprovalsSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalsSet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalsNodeGet:
		req, err := methods.DecodeExecApprovalsNodeGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalsNodeGet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalsNodeSet:
		req, err := methods.DecodeExecApprovalsNodeSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalsNodeSet(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalRequest:
		req, err := methods.DecodeExecApprovalRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalRequest(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalWaitDecision:
		req, err := methods.DecodeExecApprovalWaitDecisionParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalWaitDecision(ctx, controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodExecApprovalResolve:
		req, err := methods.DecodeExecApprovalResolveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyExecApprovalResolve(controlExecApprovals, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSandboxRun:
		req, err := methods.DecodeSandboxRunParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySandboxRun(ctx, configState, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPList:
		req, err := methods.DecodeMCPListParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyList(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPGet:
		req, err := methods.DecodeMCPGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyGet(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPPut:
		req, err := methods.DecodeMCPPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyPut(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPRemove:
		req, err := methods.DecodeMCPRemoveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyRemove(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPTest:
		req, err := methods.DecodeMCPTestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyTest(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPReconnect:
		req, err := methods.DecodeMCPReconnectParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPOps == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp operations not configured")
		}
		out, err := controlMCPOps.applyReconnect(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPAuthStart:
		req, err := methods.DecodeMCPAuthStartParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPAuth == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp auth not configured")
		}
		out, err := controlMCPAuth.applyStart(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPAuthRefresh:
		req, err := methods.DecodeMCPAuthRefreshParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPAuth == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp auth not configured")
		}
		out, err := controlMCPAuth.applyRefresh(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodMCPAuthClear:
		req, err := methods.DecodeMCPAuthClearParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if controlMCPAuth == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("mcp auth not configured")
		}
		out, err := controlMCPAuth.applyClear(ctx, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSecretsReload:
		req, err := methods.DecodeSecretsReloadParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySecretsReload(req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSecretsResolve:
		req, err := methods.DecodeSecretsResolveParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySecretsResolve(req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWizardStart:
		req, err := methods.DecodeWizardStartParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWizardStart(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWizardNext:
		req, err := methods.DecodeWizardNextParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWizardNext(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWizardCancel:
		req, err := methods.DecodeWizardCancelParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWizardCancel(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWizardStatus:
		req, err := methods.DecodeWizardStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWizardStatus(controlWizards, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodUpdateRun:
		req, err := methods.DecodeUpdateRunParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyUpdateRun(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTalkConfig:
		req, err := methods.DecodeTalkConfigParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTalkConfig(cfg, controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTalkMode:
		req, err := methods.DecodeTalkModeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTalkMode(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodLastHeartbeat:
		req, err := methods.DecodeLastHeartbeatParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyLastHeartbeat(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSetHeartbeats:
		req, err := methods.DecodeSetHeartbeatsParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySetHeartbeats(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodWake:
		req, err := methods.DecodeWakeParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyWake(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSystemPresence:
		req, err := methods.DecodeSystemPresenceParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySystemPresence(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSystemEvent:
		req, err := methods.DecodeSystemEventParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySystemEvent(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodSend:
		req, err := methods.DecodeSendParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applySend(ctx, dmBus, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodBrowserRequest:
		req, err := methods.DecodeBrowserRequestParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyBrowserRequest(req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodVoicewakeGet:
		req, err := methods.DecodeVoicewakeGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyVoicewakeGet(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodVoicewakeSet:
		req, err := methods.DecodeVoicewakeSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyVoicewakeSet(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSStatus:
		req, err := methods.DecodeTTSStatusParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSStatus(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSProviders:
		req, err := methods.DecodeTTSProvidersParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSProviders(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSSetProvider:
		req, err := methods.DecodeTTSSetProviderParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSSetProvider(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSEnable:
		req, err := methods.DecodeTTSEnableParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSEnable(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSDisable:
		req, err := methods.DecodeTTSDisableParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSDisable(controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
	case methods.MethodTTSConvert:
		req, err := methods.DecodeTTSConvertParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		out, err := applyTTSConvert(ctx, controlOps, req)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil

	// ── Hooks ────────────────────────────────────────────────────────────────
	case methods.MethodHooksList:
		var statuses []map[string]any
		if controlHooksMgr != nil {
			for _, s := range controlHooksMgr.List() {
				statuses = append(statuses, hookspkg.StatusToMap(s))
			}
		}
		if statuses == nil {
			statuses = []map[string]any{}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"hooks": statuses}}, nil

	case methods.MethodHooksEnable:
		var req struct {
			HookKey string `json:"hookKey"`
			Key     string `json:"key"`
		}
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &req)
		}
		key := req.HookKey
		if key == "" {
			key = req.Key
		}
		if key == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hookKey is required")
		}
		if controlHooksMgr != nil {
			controlHooksMgr.SetEnabled(key, true)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hookKey": key, "enabled": true}}, nil

	case methods.MethodHooksDisable:
		var req struct {
			HookKey string `json:"hookKey"`
			Key     string `json:"key"`
		}
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &req)
		}
		key := req.HookKey
		if key == "" {
			key = req.Key
		}
		if key == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hookKey is required")
		}
		if controlHooksMgr != nil {
			controlHooksMgr.SetEnabled(key, false)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hookKey": key, "enabled": false}}, nil

	case methods.MethodHooksInfo:
		var req struct {
			HookKey string `json:"hookKey"`
			Key     string `json:"key"`
		}
		if len(in.Params) > 0 {
			_ = json.Unmarshal(in.Params, &req)
		}
		key := req.HookKey
		if key == "" {
			key = req.Key
		}
		if key == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hookKey is required")
		}
		if controlHooksMgr == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hook %q not found", key)
		}
		info := controlHooksMgr.Info(key)
		if info == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("hook %q not found", key)
		}
		return nostruntime.ControlRPCResult{Result: hookspkg.StatusToMap(*info)}, nil

	case methods.MethodHooksCheck:
		var statuses []map[string]any
		if controlHooksMgr != nil {
			for _, s := range controlHooksMgr.List() {
				statuses = append(statuses, hookspkg.StatusToMap(s))
			}
		}
		if statuses == nil {
			statuses = []map[string]any{}
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"hooks":      statuses,
			"totalHooks": len(statuses),
			"eligible":   countEligible(statuses),
		}}, nil

	case methods.MethodConfigGet:
		redacted := config.Redact(cfg)
		// Include base_hash so OpenClaw clients can use optimistic-lock semantics on mutations.
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"config":    redacted,
			"hash":      cfg.Hash(),
			"base_hash": cfg.Hash(),
		}}, nil
	case methods.MethodRelayPolicyGet:
		dmRelays := []string{}
		controlRelays := []string{}
		if dmBus != nil {
			dmRelays = dmBus.Relays()
		}
		if controlBus != nil {
			controlRelays = controlBus.Relays()
		}
		return nostruntime.ControlRPCResult{Result: methods.RelayPolicyResponse{
			ReadRelays:           append([]string{}, cfg.Relays.Read...),
			WriteRelays:          append([]string{}, cfg.Relays.Write...),
			RuntimeDMRelays:      dmRelays,
			RuntimeControlRelays: controlRelays,
		}}, nil
	case methods.MethodListGet:
		req, err := methods.DecodeListGetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		list, err := docsRepo.GetList(ctx, req.Name)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: list}, nil
	case methods.MethodListPut:
		req, err := methods.DecodeListPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if req.ExpectedVersionSet || req.ExpectedEvent != "" {
			current, evt, err := docsRepo.GetListWithEvent(ctx, req.Name)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					if req.ExpectedVersionSet && req.ExpectedVersion == 0 && req.ExpectedEvent == "" {
						goto controlListPreconditionsSatisfied
					}
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nostruntime.ControlRPCResult{}, err
			}
			if req.ExpectedVersionSet {
				if req.ExpectedVersion == 0 {
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				} else if current.Version != req.ExpectedVersion {
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "list:" + req.Name,
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
					Resource:        "list:" + req.Name,
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
	controlListPreconditionsSatisfied:
		newVersion := 1
		if req.ExpectedVersionSet && req.ExpectedVersion > 0 {
			newVersion = req.ExpectedVersion + 1
		}
		if _, err := docsRepo.PutList(ctx, req.Name, state.ListDoc{Version: newVersion, Name: req.Name, Items: req.Items}); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil
	case methods.MethodConfigPut:
		req, err := methods.DecodeConfigPutParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if req.ExpectedVersionSet || req.ExpectedEvent != "" {
			current, evt, err := docsRepo.GetConfigWithEvent(ctx)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					if req.ExpectedVersionSet && req.ExpectedVersion == 0 && req.ExpectedEvent == "" {
						goto controlConfigPreconditionsSatisfied
					}
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  0,
						ExpectedEvent:   req.ExpectedEvent,
					}
				}
				return nostruntime.ControlRPCResult{}, err
			}
			if req.ExpectedVersionSet {
				if req.ExpectedVersion == 0 {
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				} else if current.Version != req.ExpectedVersion {
					return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
						Resource:        "config",
						ExpectedVersion: req.ExpectedVersion,
						CurrentVersion:  current.Version,
						ExpectedEvent:   req.ExpectedEvent,
						CurrentEvent:    evt.ID,
					}
				}
			}
			if req.ExpectedEvent != "" && evt.ID != req.ExpectedEvent {
				return nostruntime.ControlRPCResult{}, &methods.PreconditionConflictError{
					Resource:        "config",
					ExpectedVersion: req.ExpectedVersion,
					CurrentVersion:  current.Version,
					ExpectedEvent:   req.ExpectedEvent,
					CurrentEvent:    evt.ID,
				}
			}
		}
	controlConfigPreconditionsSatisfied:
		req.Config = policy.NormalizeConfig(req.Config)
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := policy.ValidateConfig(req.Config); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		newVersion := 1
		if req.ExpectedVersionSet && req.ExpectedVersion > 0 {
			newVersion = req.ExpectedVersion + 1
		}
		req.Config.Version = newVersion
		if err := persistRuntimeConfigFile(req.Config); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.PutConfig(ctx, req.Config); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		configState.Set(req.Config)
		restartPending := scheduleRestartIfNeeded(cfg, req.Config, 0)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": req.Config.Hash(), "restart_pending": restartPending}}, nil
	case methods.MethodConfigSet:
		req, err := methods.DecodeConfigSetParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next, err := methods.ApplyConfigSet(cfg, req.Key, req.Value)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next = policy.NormalizeConfig(next)
		if err := policy.ValidateConfig(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := persistRuntimeConfigFile(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		configState.Set(next)
		restartPending := scheduleRestartIfNeeded(cfg, next, 0)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": next.Hash(), "restart_pending": restartPending}}, nil
	case methods.MethodConfigApply:
		req, err := methods.DecodeConfigApplyParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next := policy.NormalizeConfig(req.Config)
		if err := policy.ValidateConfig(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := persistRuntimeConfigFile(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		configState.Set(next)
		restartPending := scheduleRestartIfNeeded(cfg, next, req.RestartDelayMS)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": next.Hash(), "restart_pending": restartPending}}, nil
	case methods.MethodConfigPatch:
		req, err := methods.DecodeConfigPatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next, err := methods.ApplyConfigPatch(cfg, req.Patch)
		if err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := methods.CheckBaseHash(cfg, req.BaseHash); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		next = policy.NormalizeConfig(next)
		if err := policy.ValidateConfig(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if err := persistRuntimeConfigFile(next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		configState.Set(next)
		restartPending := scheduleRestartIfNeeded(cfg, next, req.RestartDelayMS)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "hash": next.Hash(), "restart_pending": restartPending}}, nil
	case methods.MethodConfigSchema:
		return nostruntime.ControlRPCResult{Result: methods.ConfigSchema(cfg)}, nil
	case methods.MethodConfigSchemaLookup:
		// Look up a schema property by dot-notation path (e.g. "agents.model").
		// Returns the full schema when path is empty.
		path := ""
		if in.Params != nil {
			var p struct {
				Path  string `json:"path"`
				Field string `json:"field"`
			}
			if err := json.Unmarshal(in.Params, &p); err == nil {
				path = strings.TrimSpace(p.Path)
				if path == "" {
					path = strings.TrimSpace(p.Field)
				}
			}
		}
		full := methods.ConfigSchema(cfg)
		if path == "" {
			return nostruntime.ControlRPCResult{Result: full}, nil
		}
		// Walk the schema map by dot-separated segments.
		var cur any = full
		for _, seg := range strings.Split(path, ".") {
			m, ok := cur.(map[string]any)
			if !ok {
				cur = nil
				break
			}
			if v, found := m[seg]; found {
				cur = v
			} else if props, hasProps := m["properties"].(map[string]any); hasProps {
				cur = props[seg]
			} else {
				cur = nil
				break
			}
		}
		if cur == nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("schema path %q not found", path)
		}
		return nostruntime.ControlRPCResult{Result: cur}, nil
	case methods.MethodSecurityAudit:
		// Run security posture checks and return findings.
		report := securitypkg.Audit(securitypkg.AuditOptions{
			ConfigDoc: &cfg,
		})
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"findings": report.Findings,
			"critical": report.Critical,
			"warn":     report.Warn,
			"info":     report.Info,
		}}, nil

	// ── ACP (Agent Control Protocol) ────────────────────────────────────────
	case methods.MethodACPRegister:
		var req methods.ACPRegisterRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.register: invalid params: %w", err)
		}
		pk := strings.TrimSpace(req.PubKey)
		if pk == "" {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.register: pubkey required")
		}
		if err := controlACPPeers.Register(acppkg.PeerEntry{
			PubKey: pk,
			Alias:  req.Alias,
			Tags:   req.Tags,
		}); err != nil {
			return nostruntime.ControlRPCResult{}, err
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "pubkey": pk}}, nil

	case methods.MethodACPUnregister:
		var req methods.ACPUnregisterRequest
		if err := json.Unmarshal(in.Params, &req); err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.unregister: invalid params: %w", err)
		}
		controlACPPeers.Remove(req.PubKey)
		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true}}, nil

	case methods.MethodACPPeers:
		peers := controlACPPeers.List()
		out := make([]map[string]any, 0, len(peers))
		for _, p := range peers {
			out = append(out, map[string]any{
				"pubkey": p.PubKey,
				"alias":  p.Alias,
				"tags":   p.Tags,
			})
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{"peers": out}}, nil

	case methods.MethodACPDispatch:
		req, err := methods.DecodeACPDispatchParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: invalid params: %w", err)
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: %w", err)
		}
		cfg := state.ConfigDoc{}
		if configState != nil {
			cfg = configState.Get()
		}
		targetReqs := buildACPTargetRequirements(cfg, turnToolConstraints{ToolProfile: req.ToolProfile, EnabledTools: req.EnabledTools})
		target, _, err := resolveACPFleetTargetForConfigAndRequirements(req.TargetPubKey, cfg, targetReqs)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: %w", err)
		}
		dmBus, dmScheme, err := resolveACPDMTransport(cfg, target)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: %w", err)
		}
		taskID := fmt.Sprintf("acp-%d-%x", time.Now().UnixNano(), func() []byte {
			b := make([]byte, 4)
			_, _ = rand.Read(b)
			return b
		}())
		senderPubKey := dmBus.PublicKey()
		req.ToolProfile = strings.TrimSpace(req.ToolProfile)
		req.EnabledTools = normalizeACPEnabledTools(req.EnabledTools)
		var parentContext *acppkg.ParentContext
		if req.ParentContext != nil {
			parentContext = &acppkg.ParentContext{
				SessionID: strings.TrimSpace(req.ParentContext.SessionID),
				AgentID:   strings.TrimSpace(req.ParentContext.AgentID),
			}
		}
		acpMsg := acppkg.NewTask(taskID, senderPubKey, acppkg.TaskPayload{
			Instructions:    req.Instructions,
			ContextMessages: cloneACPContextMessages(req.ContextMessages),
			MemoryScope:     req.MemoryScope,
			ToolProfile:     req.ToolProfile,
			EnabledTools:    req.EnabledTools,
			ParentContext:   parentContext,
			TimeoutMS:       req.TimeoutMS,
			ReplyTo:         senderPubKey,
		})
		payload, err := json.Marshal(acpMsg)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: marshal: %w", err)
		}
		waitRegistered := false
		if req.Wait {
			controlACPDispatcher.Register(taskID)
			waitRegistered = true
		}
		if err := sendACPDMWithTransport(ctx, dmBus, dmScheme, target, string(payload)); err != nil {
			if waitRegistered {
				controlACPDispatcher.Cancel(taskID)
			}
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: send DM: %w", err)
		}

		// If wait==true, block until result arrives.
		if req.Wait {
			timeout := time.Duration(req.TimeoutMS) * time.Millisecond
			if timeout == 0 {
				timeout = 60 * time.Second
			}
			result, waitErr := controlACPDispatcher.Wait(ctx, taskID, timeout)
			if waitErr != nil {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: wait: %w", waitErr)
			}
			if result.Error != "" {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.dispatch: worker error: %s", result.Error)
			}
			out := map[string]any{
				"ok": true, "task_id": taskID, "target": target,
				"text": result.Text,
			}
			if result.SenderPubKey != "" {
				out["sender_pubkey"] = result.SenderPubKey
			}
			if result.Worker != nil {
				out["worker"] = result.Worker
			}
			if result.TokensUsed > 0 {
				out["tokens_used"] = result.TokensUsed
			}
			if result.CompletedAt > 0 {
				out["completed_at"] = result.CompletedAt
			}
			return nostruntime.ControlRPCResult{Result: methods.ApplyCompatResponseAliases(out)}, nil
		}

		return nostruntime.ControlRPCResult{Result: map[string]any{"ok": true, "task_id": taskID, "target": target}}, nil

	case methods.MethodACPPipeline:
		req, err := methods.DecodeACPPipelineParams(in.Params)
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.pipeline: invalid params: %w", err)
		}
		req, err = req.Normalize()
		if err != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.pipeline: %w", err)
		}
		cfg := state.ConfigDoc{}
		if configState != nil {
			cfg = configState.Get()
		}

		sendFn := func(ctx context.Context, peerPubKey, taskID string, payload acppkg.TaskPayload) error {
			dmBus, dmScheme, err := resolveACPDMTransport(cfg, peerPubKey)
			if err != nil {
				return err
			}
			senderPubKey := dmBus.PublicKey()
			payload.ReplyTo = senderPubKey
			acpMsg := acppkg.NewTask(taskID, senderPubKey, payload)
			encoded, _ := json.Marshal(acpMsg)
			return sendACPDMWithTransport(ctx, dmBus, dmScheme, peerPubKey, string(encoded))
		}

		steps := make([]acppkg.Step, 0, len(req.Steps))
		for i, s := range req.Steps {
			stepReqs := buildACPTargetRequirements(cfg, turnToolConstraints{ToolProfile: s.ToolProfile, EnabledTools: s.EnabledTools})
			resolvedPeer, _, routeErr := resolveACPFleetTargetForConfigAndRequirements(s.PeerPubKey, cfg, stepReqs)
			if routeErr != nil {
				return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.pipeline: %w at steps[%d]", routeErr, i)
			}
			s.PeerPubKey = resolvedPeer
			s.ToolProfile = strings.TrimSpace(s.ToolProfile)
			s.EnabledTools = normalizeACPEnabledTools(s.EnabledTools)
			var parentContext *acppkg.ParentContext
			if s.ParentContext != nil {
				parentContext = &acppkg.ParentContext{
					SessionID: strings.TrimSpace(s.ParentContext.SessionID),
					AgentID:   strings.TrimSpace(s.ParentContext.AgentID),
				}
			}
			steps = append(steps, acppkg.Step{
				PeerPubKey:      s.PeerPubKey,
				Instructions:    s.Instructions,
				ContextMessages: cloneACPContextMessages(s.ContextMessages),
				MemoryScope:     s.MemoryScope,
				ToolProfile:     s.ToolProfile,
				EnabledTools:    s.EnabledTools,
				ParentContext:   parentContext,
				TimeoutMS:       s.TimeoutMS,
			})
		}
		pipeline := &acppkg.Pipeline{Steps: steps}

		var pipelineResults []acppkg.PipelineResult
		var pipelineErr error
		if req.Parallel {
			pipelineResults, pipelineErr = pipeline.RunParallel(ctx, controlACPDispatcher, sendFn)
		} else {
			pipelineResults, pipelineErr = pipeline.RunSequential(ctx, controlACPDispatcher, sendFn)
		}

		out := make([]map[string]any, 0, len(pipelineResults))
		for _, r := range pipelineResults {
			item := map[string]any{
				"step_index": r.StepIndex,
				"task_id":    r.TaskID,
				"text":       r.Text,
				"error":      r.Error,
			}
			if r.SenderPubKey != "" {
				item["sender_pubkey"] = r.SenderPubKey
			}
			if r.Worker != nil {
				item["worker"] = r.Worker
			}
			if r.TokensUsed > 0 {
				item["tokens_used"] = r.TokensUsed
			}
			if r.CompletedAt > 0 {
				item["completed_at"] = r.CompletedAt
			}
			out = append(out, item)
		}
		aggregate := acppkg.AggregateResults(pipelineResults)

		if pipelineErr != nil {
			return nostruntime.ControlRPCResult{}, fmt.Errorf("acp.pipeline: %w", pipelineErr)
		}
		return nostruntime.ControlRPCResult{Result: map[string]any{
			"ok":      true,
			"results": out,
			"text":    aggregate,
		}}, nil

	default:
		return nostruntime.ControlRPCResult{}, fmt.Errorf("unknown method %q", method)
	}
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
				if controlRuntimeConfig != nil {
					cfg = controlRuntimeConfig.Get()
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
		if controlRuntimeConfig != nil {
			cfg = controlRuntimeConfig.Get()
		}
		taskTimeout := 60 * time.Second
		if taskPayload.TimeoutMS > 0 {
			taskTimeout = time.Duration(taskPayload.TimeoutMS) * time.Millisecond
		}
		processCtx, cancel := context.WithTimeout(ctx, taskTimeout)
		defer cancel()
		var result agent.TurnResult
		var historyEntryIDs []string
		procErr := withExclusiveSessionTurn(processCtx, sessionID, taskTimeout, func() (err error) {
			scopeCtx := resolveMemoryScopeContext(processCtx, cfg, docsRepo, controlSessionStore, sessionID, agentID, taskPayload.MemoryScope)
			persistSessionMemoryScope(controlSessionStore, sessionID, agentID, scopeCtx.Scope)
			cleanupWorkerTask, err := beginACPWorkerTask(processCtx, docsRepo, sessionID, fromPubKey, agentID, msg.TaskID, taskPayload, turnStartedAt)
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
			filteredRuntime := applyACPTaskRuntimeConstraints(turnCtx, rt, agentID, taskPayload, cfg, docsRepo)
			seedHistory := decodeACPConversationMessages(taskPayload.ContextMessages)
			historyEntryIDs = append(historyEntryIDs, persistACPContextHistory(processCtx, transcriptRepo, controlContextEngine, sessionID, msg.TaskID, fromPubKey, taskPayload.ParentContext, seedHistory)...)
			prepared := buildAgentRunTurn(turnCtx, methods.AgentRequest{
				SessionID: sessionID,
				Message:   instructions,
			}, controlMemoryStore, scopeCtx, workspaceDirForAgent(cfg, agentID), controlSessionStore)
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
					historyEntryIDs = append(historyEntryIDs, persistACPTurnHistory(processCtx, transcriptRepo, controlContextEngine, sessionID, msg.TaskID, fromPubKey, taskPayload.ParentContext, partial.HistoryDelta, turnResultMetadataPtr(result, err))...)
				}
				return err
			}
			commitMemoryRecallArtifacts(controlSessionStore, sessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
			delta := result.HistoryDelta
			if len(delta) == 0 && strings.TrimSpace(result.Text) != "" {
				delta = []agent.ConversationMessage{{Role: "assistant", Content: strings.TrimSpace(result.Text)}}
			}
			historyEntryIDs = append(historyEntryIDs, persistACPTurnHistory(processCtx, transcriptRepo, controlContextEngine, sessionID, msg.TaskID, fromPubKey, taskPayload.ParentContext, delta, turnResultMetadataPtr(result, nil))...)
			return err
		})
		if controlSessionStore != nil && (result.Usage.InputTokens > 0 || result.Usage.OutputTokens > 0) {
			_ = controlSessionStore.AddTokens(sessionID, result.Usage.InputTokens, result.Usage.OutputTokens)
		}
		turnTelemetry := buildTurnTelemetry(msg.TaskID, turnStartedAt, time.Now(), result, procErr, false, "", "", "")
		persistTurnTelemetry(controlSessionStore, sessionID, turnTelemetry)
		emitControlWSEvent(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, sessionID, turnTelemetry))

		var parentContext *acppkg.ParentContext
		if taskPayload.ParentContext != nil {
			parentContext = &acppkg.ParentContext{
				SessionID: strings.TrimSpace(taskPayload.ParentContext.SessionID),
				AgentID:   strings.TrimSpace(taskPayload.ParentContext.AgentID),
			}
		}
		worker := &acppkg.WorkerMetadata{
			SessionID:       sessionID,
			AgentID:         agentID,
			ParentContext:   parentContext,
			HistoryEntryIDs: cloneACPStringSlice(historyEntryIDs),
			TurnResult:      turnResultMetadataPtr(result, procErr),
		}
		senderPubKey := ""
		controlDMBusMu.RLock()
		if controlDMBus != nil {
			senderPubKey = controlDMBus.PublicKey()
		}
		controlDMBusMu.RUnlock()

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
		if controlACPDispatcher != nil {
			controlACPDispatcher.Deliver(acppkg.TaskResult{
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
		pong := acppkg.Message{ACPType: "pong", Version: acppkg.Version, TaskID: msg.TaskID}
		payload, _ := json.Marshal(pong)
		if sendErr := dm.Reply(ctx, string(payload)); sendErr != nil {
			log.Printf("acp pong send failed to=%s err=%v", fromPubKey, sendErr)
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
	if controlSessionRouter != nil {
		agentID = controlSessionRouter.Get(sessionID)
	}
	return applyAgentProfileFilterForAgent(ctx, rt, agentID, cfg, docsRepo)
}

// scheduleRestartIfNeeded compares old and next ConfigDoc.  If the change
// requires a daemon restart (model, providers, plugins changed) it sends a
// signal to the restart scheduler goroutine and returns true.
// delayMS is the caller-requested delay before restart; defaults to 500ms if zero.
func scheduleRestartIfNeeded(old, next state.ConfigDoc, delayMS int) (pending bool) {
	if !policy.ConfigChangedNeedsRestart(old, next) {
		return false
	}
	if delayMS <= 0 {
		delayMS = 500 // default grace period
	}
	select {
	case controlRestartCh <- delayMS:
	default:
		// already queued; ignore duplicate
	}
	return true
}

func setControlWSEmitter(emitter gatewayws.EventEmitter) {
	if emitter == nil {
		emitter = gatewayws.NoopEmitter{}
	}
	controlWsEmitterMu.Lock()
	defer controlWsEmitterMu.Unlock()
	controlWsEmitter = emitter
}

// autoResolveProviderOverride infers a ProviderOverride from the model name and
// the configured providers map.  It enables zero-config usage: a model named
// "claude-3-5-sonnet-20241022" will automatically pick up an "anthropic" entry
// from providers[], or fall back to env vars (handled by BuildRuntimeForModel).
// refreshKeyRings rebuilds the ProviderKeyRingRegistry from the current
// provider config.  It should be called whenever the config changes.
func refreshKeyRings(providers map[string]state.ProviderEntry) {
	if controlKeyRings == nil {
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
	controlKeyRings.Replace(rings)
}

func applyRuntimeConfigSideEffects(cfg state.ConfigDoc) {
	if controlStateEnvelopeCodec != nil {
		controlStateEnvelopeCodec.SetEncrypt(cfg.StorageEncryptEnabled())
	}
	refreshKeyRings(cfg.Providers)
	applyRuntimeRelayPolicy(nil, nil, cfg)
	applyCapabilityRuntimeState(cfg)
	if controlOps != nil {
		controlOps.SyncHeartbeatConfig(cfg.Heartbeat)
	}
}

func persistRuntimeConfigFile(doc state.ConfigDoc) error {
	if strings.TrimSpace(controlConfigFilePath) == "" {
		return nil
	}
	return config.WriteConfigFile(controlConfigFilePath, doc)
}

func providerOverrideForEntry(name string, pe state.ProviderEntry) agent.ProviderOverride {
	apiKey := pe.APIKey
	if controlKeyRings != nil {
		if ring := controlKeyRings.Get(name); ring != nil && ring.Len() > 0 {
			if picked, ok := ring.Pick(); ok && picked != "" {
				apiKey = picked
			}
		}
	}
	return agent.ProviderOverride{BaseURL: pe.BaseURL, APIKey: apiKey, Model: pe.Model}
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
	override.SystemPrompt = strings.TrimSpace(agCfg.SystemPrompt)
	return override
}

func buildProviderForAgentModel(cfg state.ConfigDoc, agCfg state.AgentConfig, model string) (agent.Provider, error) {
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
	controlWsEmitterMu.RLock()
	emitter := controlWsEmitter
	controlWsEmitterMu.RUnlock()
	emitter.Emit(event, payload)
}

func buildMCPLifecyclePayload(change mcppkg.StateChange, ts int64) gatewayws.MCPLifecyclePayload {
	return buildMCPLifecyclePayloadForTelemetry(mcppkg.TelemetryServer{
		Name:              change.Server.Name,
		State:             string(change.Server.State),
		Healthy:           !change.Removed && change.Server.State == mcppkg.ConnectionStateConnected,
		Enabled:           change.Server.Enabled,
		RuntimePresent:    !change.Removed,
		Source:            change.Server.Source,
		Precedence:        change.Server.Precedence,
		Signature:         change.Server.Signature,
		Transport:         change.Server.Transport,
		Command:           change.Server.Command,
		URL:               change.Server.URL,
		Capabilities:      change.Server.Capabilities,
		ToolCount:         change.Server.ToolCount,
		LastError:         change.Server.LastError,
		ReconnectAttempts: change.Server.ReconnectAttempts,
		LastAttemptAtMS:   change.Server.LastAttemptAtMS,
		LastConnectedAtMS: change.Server.LastConnectedAtMS,
		LastFailedAtMS:    change.Server.LastFailedAtMS,
		UpdatedAtMS:       change.Server.UpdatedAtMS,
	}, string(change.PreviousState), change.Reason, change.Removed, ts)
}

func buildMCPLifecyclePayloadForTelemetry(server mcppkg.TelemetryServer, previousState, reason string, removed bool, ts int64) gatewayws.MCPLifecyclePayload {
	caps := map[string]bool{}
	if server.Capabilities.Tools {
		caps["tools"] = true
	}
	if server.Capabilities.Resources {
		caps["resources"] = true
	}
	if server.Capabilities.Prompts {
		caps["prompts"] = true
	}
	if server.Capabilities.Logging {
		caps["logging"] = true
	}
	if len(caps) == 0 {
		caps = nil
	}
	return gatewayws.MCPLifecyclePayload{
		TS:                ts,
		Name:              server.Name,
		State:             server.State,
		PreviousState:     previousState,
		Reason:            reason,
		Removed:           removed,
		Healthy:           server.Healthy,
		Enabled:           server.Enabled,
		RuntimePresent:    server.RuntimePresent,
		Source:            string(server.Source),
		Precedence:        server.Precedence,
		Signature:         server.Signature,
		Transport:         server.Transport,
		Command:           server.Command,
		URL:               server.URL,
		ToolCount:         server.ToolCount,
		LastError:         server.LastError,
		ReconnectAttempts: server.ReconnectAttempts,
		LastAttemptAtMS:   server.LastAttemptAtMS,
		LastConnectedAtMS: server.LastConnectedAtMS,
		LastFailedAtMS:    server.LastFailedAtMS,
		UpdatedAtMS:       server.UpdatedAtMS,
		PolicyStatus:      string(server.PolicyStatus),
		PolicyReason:      string(server.PolicyReason),
		Capabilities:      caps,
	}
}

type filteredMCPLifecycleTracker struct {
	mu   sync.Mutex
	last map[string]mcppkg.TelemetryServer
}

func newFilteredMCPLifecycleTracker() *filteredMCPLifecycleTracker {
	return &filteredMCPLifecycleTracker{last: map[string]mcppkg.TelemetryServer{}}
}

func (t *filteredMCPLifecycleTracker) Emit(emitter gatewayws.EventEmitter, resolved mcppkg.Config, reason string, ts int64) {
	if t == nil || emitter == nil {
		return
	}
	current := map[string]mcppkg.TelemetryServer{}
	for _, server := range mcppkg.BuildTelemetrySnapshot(resolved, mcppkg.ManagerSnapshot{}).Servers {
		if server.PolicyStatus == "" {
			continue
		}
		current[server.Name] = server
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	for name, previous := range t.last {
		if _, ok := current[name]; ok {
			continue
		}
		if _, ok := resolved.Servers[name]; ok {
			continue
		}
		if _, ok := resolved.DisabledServers[name]; ok {
			disabled := previous
			disabled.State = string(mcppkg.ConnectionStateDisabled)
			disabled.Healthy = false
			disabled.RuntimePresent = false
			disabled.PolicyStatus = ""
			disabled.PolicyReason = ""
			emitter.Emit(gatewayws.EventMCPLifecycle, buildMCPLifecyclePayloadForTelemetry(disabled, previous.State, reason+".disabled", false, ts))
			continue
		}
		emitter.Emit(gatewayws.EventMCPLifecycle, buildMCPLifecyclePayloadForTelemetry(previous, previous.State, reason+".removed", true, ts))
	}
	for _, server := range current {
		emitter.Emit(gatewayws.EventMCPLifecycle, buildMCPLifecyclePayloadForTelemetry(server, "", reason, false, ts))
	}
	t.last = current
}

func currentMCPTelemetry(cfg state.ConfigDoc, mgr *mcppkg.Manager) mcppkg.TelemetrySnapshot {
	runtime := mcppkg.ManagerSnapshot{}
	if mgr != nil {
		runtime = mgr.Snapshot()
	}
	return mcppkg.BuildTelemetrySnapshot(mcppkg.ResolveConfigDoc(cfg), runtime)
}

func buildTurnTelemetry(turnID string, startedAt, endedAt time.Time, result agent.TurnResult, turnErr error, fallbackUsed bool, fallbackFrom, fallbackTo, fallbackReason string) agent.TurnTelemetry {
	telemetry := agent.TurnTelemetry{
		TurnID:         strings.TrimSpace(turnID),
		StartedAtMS:    startedAt.UnixMilli(),
		EndedAtMS:      endedAt.UnixMilli(),
		DurationMS:     endedAt.Sub(startedAt).Milliseconds(),
		FallbackUsed:   fallbackUsed,
		FallbackFrom:   strings.TrimSpace(fallbackFrom),
		FallbackTo:     strings.TrimSpace(fallbackTo),
		FallbackReason: strings.TrimSpace(fallbackReason),
	}
	if meta, ok := agent.BuildTurnResultMetadata(result, turnErr); ok {
		telemetry.Outcome = meta.Outcome
		telemetry.StopReason = meta.StopReason
		telemetry.Usage = meta.Usage
	}
	if turnErr != nil {
		telemetry.Error = truncateRunes(strings.TrimSpace(turnErr.Error()), 200)
	}
	telemetry.LoopBlocked = telemetry.StopReason == agent.TurnStopReasonLoopBlocked
	return telemetry
}

func turnResultMetadataPtr(result agent.TurnResult, turnErr error) *agent.TurnResultMetadata {
	meta, ok := agent.BuildTurnResultMetadata(result, turnErr)
	if !ok {
		return nil
	}
	return &meta
}

func persistTurnTelemetry(sessionStore *state.SessionStore, sessionID string, telemetry agent.TurnTelemetry) {
	if sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if err := sessionStore.RecordTurn(sessionID, state.TurnTelemetry{
		TurnID:         telemetry.TurnID,
		StartedAtMS:    telemetry.StartedAtMS,
		EndedAtMS:      telemetry.EndedAtMS,
		DurationMS:     telemetry.DurationMS,
		Outcome:        string(telemetry.Outcome),
		StopReason:     string(telemetry.StopReason),
		LoopBlocked:    telemetry.LoopBlocked,
		Error:          telemetry.Error,
		FallbackUsed:   telemetry.FallbackUsed,
		FallbackFrom:   telemetry.FallbackFrom,
		FallbackTo:     telemetry.FallbackTo,
		FallbackReason: telemetry.FallbackReason,
		InputTokens:    telemetry.Usage.InputTokens,
		OutputTokens:   telemetry.Usage.OutputTokens,
	}); err != nil {
		log.Printf("session store turn telemetry failed session=%s: %v", sessionID, err)
	}
}

func emitTurnTelemetry(emitter gatewayws.EventEmitter, agentID, sessionID string, telemetry agent.TurnTelemetry) {
	if emitter == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	emitter.Emit(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, sessionID, telemetry))
}

func turnTelemetryPayload(agentID, sessionID string, telemetry agent.TurnTelemetry) gatewayws.TurnResultPayload {
	return gatewayws.TurnResultPayload{
		TS:             telemetry.EndedAtMS,
		AgentID:        agentID,
		SessionID:      sessionID,
		TurnID:         telemetry.TurnID,
		StartedAtMS:    telemetry.StartedAtMS,
		EndedAtMS:      telemetry.EndedAtMS,
		DurationMS:     telemetry.DurationMS,
		Outcome:        string(telemetry.Outcome),
		StopReason:     string(telemetry.StopReason),
		LoopBlocked:    telemetry.LoopBlocked,
		Error:          telemetry.Error,
		FallbackUsed:   telemetry.FallbackUsed,
		FallbackFrom:   telemetry.FallbackFrom,
		FallbackTo:     telemetry.FallbackTo,
		FallbackReason: telemetry.FallbackReason,
		InputTokens:    telemetry.Usage.InputTokens,
		OutputTokens:   telemetry.Usage.OutputTokens,
	}
}

func toolLifecycleEmitter(emitter gatewayws.EventEmitter, agentID string) agent.ToolLifecycleSink {
	if emitter == nil {
		return nil
	}
	return func(evt agent.ToolLifecycleEvent) {
		payload := gatewayws.ToolLifecyclePayload{
			TS:         evt.TS,
			AgentID:    agentID,
			SessionID:  evt.SessionID,
			TurnID:     evt.TurnID,
			ToolCallID: evt.ToolCallID,
			ToolName:   evt.ToolName,
			Result:     evt.Result,
			Error:      evt.Error,
			Data:       projectToolLifecycleData(evt.Data),
		}
		switch evt.Type {
		case agent.ToolLifecycleEventStart:
			emitter.Emit(gatewayws.EventToolStart, payload)
		case agent.ToolLifecycleEventProgress:
			emitter.Emit(gatewayws.EventToolProgress, payload)
		case agent.ToolLifecycleEventResult:
			emitter.Emit(gatewayws.EventToolResult, payload)
		case agent.ToolLifecycleEventError:
			emitter.Emit(gatewayws.EventToolError, payload)
		}
	}
}

func projectToolLifecycleData(data any) any {
	switch decision := data.(type) {
	case agent.ToolSchedulerDecision:
		return gatewayws.ToolSchedulerDecisionPayload{
			Kind:             gatewayws.ToolDecisionKind(decision.Kind),
			Mode:             decision.Mode,
			BatchIndex:       decision.BatchIndex,
			BatchCount:       decision.BatchCount,
			BatchSize:        decision.BatchSize,
			BatchPosition:    decision.BatchPosition,
			ConcurrencySafe:  decision.ConcurrencySafe,
			ConcurrencyLimit: decision.ConcurrencyLimit,
		}
	case agent.ToolLoopDecision:
		return gatewayws.ToolLoopDecisionPayload{
			Kind:           gatewayws.ToolDecisionKind(decision.Kind),
			Blocked:        decision.Blocked,
			Level:          decision.Level,
			Detector:       decision.Detector,
			Count:          decision.Count,
			WarningKey:     decision.WarningKey,
			PairedToolName: decision.PairedToolName,
			Message:        decision.Message,
		}
	default:
		return data
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
	controlDMBusMu.RLock()
	bus := controlDMBus
	controlDMBusMu.RUnlock()
	if bus == nil {
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := bus.SendDM(sendCtx, toPubKey, text); err != nil {
		log.Printf("sendControlDM to=%s err=%v", toPubKey, err)
	}
}

func resolveInboundChannelRuntime(configuredAgentID, sessionID string) (string, agent.Runtime) {
	agentID := strings.TrimSpace(configuredAgentID)
	if agentID == "" && controlSessionRouter != nil {
		agentID = strings.TrimSpace(controlSessionRouter.Get(sessionID))
	}
	if agentID == "" {
		agentID = "main"
	}
	if controlAgentRegistry != nil {
		if rt := controlAgentRegistry.Get(agentID); rt != nil {
			return agentID, rt
		}
	}
	return agentID, controlAgentRuntime
}

// applySessionsSpawn creates a child agent session bounded by the depth limit.
// It dispatches an agent job and returns immediately; the caller can use agent.wait
// with the returned run_id to block until the sub-session completes.
func applySessionsSpawn(ctx context.Context, req methods.SessionsSpawnRequest, cfg state.ConfigDoc, docsRepo *state.DocsRepository, memoryIndex memory.Store) (map[string]any, error) {
	if controlAgentRuntime == nil || controlAgentJobs == nil {
		return nil, fmt.Errorf("agent runtime not configured")
	}
	if controlSubagents == nil {
		return nil, fmt.Errorf("subagent registry not initialised")
	}

	// Determine the depth of the new child session.
	parentDepth := 0
	if req.ParentSessionID != "" {
		parentDepth = controlSubagents.DepthOf(req.ParentSessionID)
	}
	childDepth := parentDepth + 1

	// Check depth limit.
	if childDepth > maxSubagentDepth {
		return nil, fmt.Errorf("subagent depth limit %d exceeded", maxSubagentDepth)
	}

	// Build IDs.
	runID := fmt.Sprintf("spawn-%d", time.Now().UnixNano())
	sessionID := fmt.Sprintf("spawn-sess-%d", time.Now().UnixNano())

	// Register in SubagentRegistry.
	rec, ok := controlSubagents.Spawn(runID, sessionID, req.ParentSessionID, childDepth, req.Message)
	if !ok {
		return nil, fmt.Errorf("subagent depth limit %d exceeded", maxSubagentDepth)
	}

	// Select the agent runtime for this sub-session.
	var rt agent.Runtime
	if controlAgentRegistry != nil && req.AgentID != "" {
		rt = controlAgentRegistry.Get(req.AgentID)
	}
	if rt == nil {
		rt = controlAgentRuntime
	}

	// Apply profile-based tool filtering.
	rt = applyAgentProfileFilter(ctx, rt, sessionID, cfg, docsRepo)

	// Build the agent request for the child session.
	agentReq := methods.AgentRequest{
		SessionID:   sessionID,
		Message:     req.Message,
		Context:     req.Context,
		MemoryScope: req.MemoryScope,
		TimeoutMS:   req.TimeoutMS,
	}
	persistSessionMemoryScope(controlSessionStore, sessionID, req.AgentID, req.MemoryScope)

	// Start the agent job and track in SubagentRegistry.
	snapshot := controlAgentJobs.Begin(runID, sessionID)
	go func() {
		executeAgentRun(runID, agentReq, rt, memoryIndex, controlAgentJobs)
		// Mirror final status into SubagentRegistry.
		if final, found := controlAgentJobs.Get(runID); found {
			controlSubagents.Finish(runID, final.Result, final.Err)
		}
	}()

	return methods.ApplyCompatResponseAliases(map[string]any{
		"run_id":            runID,
		"session_id":        sessionID,
		"parent_session_id": rec.ParentSessionID,
		"depth":             rec.Depth,
		"status":            "accepted",
		"accepted_at":       snapshot.StartedAt,
	}), nil
}

// isRetryableAgentError returns true if the error indicates a rate-limit or
// temporary unavailability that warrants trying a fallback model/key.
func isRetryableAgentError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "context length") ||
		strings.Contains(msg, "too many tokens") ||
		strings.Contains(msg, "model_not_found")
}

func executeAgentRun(runID string, req methods.AgentRequest, runtime agent.Runtime, memoryIndex memory.Store, jobs *agentJobRegistry) {
	executeAgentRunWithFallbacks(runID, req, runtime, nil, nil, memoryIndex, jobs)
}

type agentRunAttemptResult struct {
	Result         *agent.TurnResult
	Err            error
	FallbackUsed   bool
	FallbackFrom   string
	FallbackTo     string
	FallbackReason string
}

func runAgentTurnWithFallbacks(baseCtx context.Context, req methods.AgentRequest, primary agent.Runtime, fallbacks []agent.Runtime, runtimeLabels []string, memoryIndex memory.Store) agentRunAttemptResult {
	if primary == nil {
		return agentRunAttemptResult{Err: fmt.Errorf("agent runtime not configured")}
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	// Resolve the active agent ID so WS clients see per-agent status events.
	agentID := ""
	if controlSessionRouter != nil {
		agentID = controlSessionRouter.Get(req.SessionID)
	}
	if agentID == "" {
		agentID = "main"
	}

	emitControlWSEvent(gatewayws.EventAgentStatus, gatewayws.AgentStatusPayload{
		TS:      time.Now().UnixMilli(),
		AgentID: agentID,
		Status:  "thinking",
		Session: req.SessionID,
	})
	defer func() {
		emitControlWSEvent(gatewayws.EventAgentStatus, gatewayws.AgentStatusPayload{
			TS:      time.Now().UnixMilli(),
			AgentID: agentID,
			Status:  "idle",
			Session: req.SessionID,
		})
	}()

	runtimesToTry := append([]agent.Runtime{primary}, fallbacks...)
	cfg := state.ConfigDoc{}
	if controlRuntimeConfig != nil {
		cfg = controlRuntimeConfig.Get()
	}
	scopeCtx := resolveMemoryScopeContext(ctx, cfg, nil, controlSessionStore, req.SessionID, agentID, req.MemoryScope)
	persistSessionMemoryScope(controlSessionStore, req.SessionID, agentID, scopeCtx.Scope)
	ctx = contextWithMemoryScope(ctx, scopeCtx)
	prepared := buildAgentRunTurn(ctx, req, memoryIndex, scopeCtx, workspaceDirForAgent(cfg, agentID), controlSessionStore)
	turn := prepared.Turn
	var result *agent.TurnResult
	var lastErr error
	attempt := agentRunAttemptResult{}
	turnStartedAt := time.Now()
	for i, rt := range runtimesToTry {
		if rt == nil {
			continue
		}
		var r agent.TurnResult
		r, lastErr = rt.ProcessTurn(ctx, turn)
		if lastErr == nil {
			if i > 0 {
				attempt.FallbackUsed = true
				attempt.FallbackFrom = runtimeLabelAt(runtimeLabels, i-1)
				attempt.FallbackTo = runtimeLabelAt(runtimeLabels, i)
			}
			result = &r
			break
		}
		if i < len(runtimesToTry)-1 && isRetryableAgentError(lastErr) {
			log.Printf("executeAgentRun session=%s fallback attempt %d/%d err=%v", req.SessionID, i+1, len(runtimesToTry)-1, lastErr)
			if attempt.FallbackReason == "" {
				attempt.FallbackReason = strings.TrimSpace(lastErr.Error())
			}
			continue
		}
		break
	}

	if lastErr != nil {
		turnTelemetry := buildTurnTelemetry("", turnStartedAt, time.Now(), agent.TurnResult{}, lastErr, attempt.FallbackUsed, attempt.FallbackFrom, attempt.FallbackTo, attempt.FallbackReason)
		persistTurnTelemetry(controlSessionStore, req.SessionID, turnTelemetry)
		emitControlWSEvent(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, req.SessionID, turnTelemetry))
		attempt.Err = lastErr
		return attempt
	}
	if result == nil {
		err := fmt.Errorf("all runtimes returned nil result")
		turnTelemetry := buildTurnTelemetry("", turnStartedAt, time.Now(), agent.TurnResult{}, err, attempt.FallbackUsed, attempt.FallbackFrom, attempt.FallbackTo, attempt.FallbackReason)
		persistTurnTelemetry(controlSessionStore, req.SessionID, turnTelemetry)
		emitControlWSEvent(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, req.SessionID, turnTelemetry))
		attempt.Err = err
		return attempt
	}
	commitMemoryRecallArtifacts(controlSessionStore, req.SessionID, prepared.Turn.TurnID, prepared.MemoryRecallSample, prepared.SurfacedFileMemory)
	if controlSessionStore != nil {
		se := controlSessionStore.GetOrNew(req.SessionID)
		if attempt.FallbackUsed {
			se.FallbackFrom = attempt.FallbackFrom
			se.FallbackTo = attempt.FallbackTo
			se.FallbackReason = truncateRunes(attempt.FallbackReason, 200)
			se.FallbackAt = time.Now().UnixMilli()
		} else {
			se.FallbackFrom = ""
			se.FallbackTo = ""
			se.FallbackReason = ""
			se.FallbackAt = 0
		}
		if putErr := controlSessionStore.Put(req.SessionID, se); putErr != nil {
			log.Printf("session store put failed session=%s: %v", req.SessionID, putErr)
		}
	}
	turnTelemetry := buildTurnTelemetry("", turnStartedAt, time.Now(), *result, nil, attempt.FallbackUsed, attempt.FallbackFrom, attempt.FallbackTo, attempt.FallbackReason)
	persistTurnTelemetry(controlSessionStore, req.SessionID, turnTelemetry)
	emitControlWSEvent(gatewayws.EventTurnResult, turnTelemetryPayload(agentID, req.SessionID, turnTelemetry))
	attempt.Result = result
	return attempt
}

// executeAgentRunWithFallbacks tries the primary runtime; on retryable errors,
// it tries each fallback runtime in order before giving up.
func executeAgentRunWithFallbacks(runID string, req methods.AgentRequest, primary agent.Runtime, fallbacks []agent.Runtime, runtimeLabels []string, memoryIndex memory.Store, jobs *agentJobRegistry) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in executeAgentRun runID=%s panic=%v", runID, r)
			if jobs != nil {
				jobs.Finish(runID, "", fmt.Errorf("agent runtime panic: %v", r))
			}
		}
	}()

	if jobs == nil {
		return
	}
	attempt := runAgentTurnWithFallbacks(context.Background(), req, primary, fallbacks, runtimeLabels, memoryIndex)
	if attempt.FallbackUsed {
		jobs.SetFallback(runID, attempt.FallbackFrom, attempt.FallbackTo, attempt.FallbackReason)
	}
	if attempt.Err != nil {
		jobs.Finish(runID, "", attempt.Err)
		return
	}
	if attempt.Result == nil {
		jobs.Finish(runID, "", fmt.Errorf("all runtimes returned nil result"))
		return
	}
	jobs.Finish(runID, attempt.Result.Text, nil)
}

func runtimeLabelAt(labels []string, idx int) string {
	if idx < 0 || idx >= len(labels) {
		if idx == 0 {
			return "primary"
		}
		return fmt.Sprintf("fallback-%d", idx)
	}
	if strings.TrimSpace(labels[idx]) == "" {
		if idx == 0 {
			return "primary"
		}
		return fmt.Sprintf("fallback-%d", idx)
	}
	return strings.TrimSpace(labels[idx])
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

func normalizeCSVList(raw string) []string {
	items := strings.Split(raw, ",")
	return normalizeStringList(items)
}

// thinkingLevelToBudget converts a level string to an Anthropic thinking
// budget in tokens.  Returns 0 (disabled) for "off" or unknown values.
// pruneSessions deletes session transcript entries and marks session docs as
// deleted when the session's last activity is older than olderThanDays days.
func pruneSessions(ctx context.Context, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, olderThanDays int) {
	sessions, err := docsRepo.ListSessions(ctx, 10000)
	if err != nil {
		log.Printf("session prune: list sessions: %v", err)
		return
	}
	cutoff := time.Now().Add(-time.Duration(olderThanDays) * 24 * time.Hour)
	pruned := 0
	for _, sess := range sessions {
		if !shouldPruneSession(sess, cutoff) {
			continue
		}
		exclusiveCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		didPrune := false
		err := withExclusiveSessionTurn(exclusiveCtx, sess.SessionID, 0, func() error {
			current, err := docsRepo.GetSession(ctx, sess.SessionID)
			if err != nil {
				return err
			}
			if !shouldPruneSession(current, cutoff) {
				return nil
			}
			entries, _ := transcriptRepo.ListSessionAll(ctx, sess.SessionID)
			for _, e := range entries {
				if delErr := transcriptRepo.DeleteEntry(ctx, sess.SessionID, e.EntryID); delErr != nil {
					log.Printf("transcript delete failed session=%s entry=%s: %v", sess.SessionID, e.EntryID, delErr)
				}
			}
			_, err = updateExistingSessionDoc(ctx, docsRepo, sess.SessionID, current.PeerPubKey, func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{
					"deleted": true, "deleted_at": time.Now().Unix(), "prune_reason": "auto",
				})
				return nil
			})
			if err == nil {
				didPrune = true
			}
			return err
		})
		cancel()
		if err != nil {
			log.Printf("session prune: skip busy session=%s err=%v", sess.SessionID, err)
			continue
		}
		if didPrune {
			pruned++
		}
	}
	if pruned > 0 {
		log.Printf("session prune: deleted %d sessions older than %d days", pruned, olderThanDays)
	}
}

func thinkingLevelToBudget(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off", "":
		return 0
	case "minimal":
		return 1024
	case "low":
		return 5000
	case "medium":
		return 10000
	case "high":
		return 20000
	case "xhigh":
		return 40000
	default:
		return 10000
	}
}

func normalizeThinkingLevel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func normalizeReasoningLevel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func normalizeVerboseLevel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "quiet", "normal", "debug":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func normalizeResponseUsage(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "on", "tokens", "full":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func normalizeQueueDrop(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "summarize":
		return "summarize"
	case "old", "oldest":
		return "oldest"
	case "new", "newest":
		return "newest"
	default:
		return ""
	}
}

func normalizeQueueMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "collect", "followup", "queue", "steer", "steer-backlog", "steer+backlog", "interrupt":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func queueModeCollect(mode string) bool {
	return mode == "" || mode == "collect"
}

func queueModeSequential(mode string) bool {
	switch mode {
	case "followup", "queue", "steer-backlog", "steer+backlog":
		return true
	default:
		return false
	}
}

func resolveQueueRuntimeSettings(cfg state.ConfigDoc, sessionEntry *state.SessionEntry, channelID string, defaultCap int) queueRuntimeSettings {
	resolved := queueRuntimeSettings{Mode: "collect", Cap: defaultCap, Drop: autoreply.QueueDropSummarize}
	if cfg.Extra != nil {
		if m, ok := cfg.Extra["messages"].(map[string]any); ok {
			if q, ok := m["queue"].(map[string]any); ok {
				if mv, ok := q["mode"].(string); ok {
					if n := normalizeQueueMode(mv); n != "" {
						resolved.Mode = n
					}
				}
				if cv, ok := q["cap"].(float64); ok && cv > 0 {
					resolved.Cap = int(cv)
				}
				if dv, ok := q["drop"].(string); ok {
					switch normalizeQueueDrop(dv) {
					case "oldest":
						resolved.Drop = autoreply.QueueDropOldest
					case "newest":
						resolved.Drop = autoreply.QueueDropNewest
					case "summarize":
						resolved.Drop = autoreply.QueueDropSummarize
					}
				}
				if channelID != "" {
					if by, ok := q["by_channel"].(map[string]any); ok {
						if raw, ok := by[channelID].(string); ok {
							if n := normalizeQueueMode(raw); n != "" {
								resolved.Mode = n
							}
						}
					}
				}
			}
		}
	}
	if sessionEntry != nil {
		if n := normalizeQueueMode(sessionEntry.QueueMode); n != "" {
			resolved.Mode = n
		}
		if sessionEntry.QueueCap > 0 {
			resolved.Cap = sessionEntry.QueueCap
		}
		switch normalizeQueueDrop(sessionEntry.QueueDrop) {
		case "oldest":
			resolved.Drop = autoreply.QueueDropOldest
		case "newest":
			resolved.Drop = autoreply.QueueDropNewest
		case "summarize":
			resolved.Drop = autoreply.QueueDropSummarize
		}
	}
	if resolved.Cap <= 0 {
		resolved.Cap = defaultCap
	}
	return resolved
}

func normalizeStringList(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func prefixIfNeeded(value, prefix string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	return prefix + value
}

func applyFastSlash(sessionStore *state.SessionStore, sessionID string, args []string) string {
	if sessionStore == nil {
		return "⚠️  Session store unavailable."
	}
	if len(args) == 0 {
		se := sessionStore.GetOrNew(sessionID)
		if se.FastMode {
			return "⚡ fast mode is ON"
		}
		return "⚡ fast mode is OFF"
	}
	arg := strings.ToLower(strings.TrimSpace(args[0]))
	on := arg == "on" || arg == "true" || arg == "1"
	off := arg == "off" || arg == "false" || arg == "0"
	if !on && !off {
		return "Usage: /fast on|off"
	}
	se := sessionStore.GetOrNew(sessionID)
	se.FastMode = on
	if err := sessionStore.Put(sessionID, se); err != nil {
		return fmt.Sprintf("⚠️  Failed to persist: %v", err)
	}
	if on {
		return "⚡ fast mode enabled"
	}
	return "⚡ fast mode disabled"
}

func applyUsageSlash(sessionStore *state.SessionStore, sessionID string, args []string) string {
	if sessionStore == nil {
		return "⚠️  Session store unavailable."
	}
	se := sessionStore.GetOrNew(sessionID)
	if len(args) > 0 {
		mode := normalizeResponseUsage(strings.Join(args, " "))
		if mode == "" {
			return "Usage: /usage [off|on|tokens|full]"
		}
		se.ResponseUsage = mode
		if err := sessionStore.Put(sessionID, se); err != nil {
			return fmt.Sprintf("⚠️  Failed to persist: %v", err)
		}
		return fmt.Sprintf("✓ Usage mode set to %s.", mode)
	}
	mode := se.ResponseUsage
	if mode == "" {
		mode = "off"
	}
	lines := []string{
		fmt.Sprintf("Usage mode: %s", mode),
		fmt.Sprintf("Input tokens: %d", se.InputTokens),
		fmt.Sprintf("Output tokens: %d", se.OutputTokens),
		fmt.Sprintf("Total tokens: %d", se.TotalTokens),
	}
	if se.ContextTokens > 0 || se.CacheRead > 0 || se.CacheWrite > 0 {
		lines = append(lines,
			fmt.Sprintf("Context tokens: %d", se.ContextTokens),
			fmt.Sprintf("Cache read/write: %d / %d", se.CacheRead, se.CacheWrite),
		)
	}
	return strings.Join(lines, "\n")
}

func renderResponseWithUsage(base string, usage agent.TurnUsage, sessionEntry *state.SessionEntry) string {
	if sessionEntry == nil {
		return base
	}
	mode := normalizeResponseUsage(sessionEntry.ResponseUsage)
	if mode == "" || mode == "off" {
		return base
	}
	total := usage.InputTokens + usage.OutputTokens
	switch mode {
	case "on":
		return strings.TrimRight(base, "\n") + fmt.Sprintf("\n\n[usage: %d tokens]", total)
	case "tokens":
		return strings.TrimRight(base, "\n") + fmt.Sprintf("\n\n[usage: in=%d out=%d total=%d]", usage.InputTokens, usage.OutputTokens, total)
	case "full":
		projectedTotal := sessionEntry.TotalTokens + total
		return strings.TrimRight(base, "\n") + fmt.Sprintf(
			"\n\n[usage: in=%d out=%d total=%d | session_total=%d context=%d cache_read=%d cache_write=%d]",
			usage.InputTokens, usage.OutputTokens, total, projectedTotal, sessionEntry.ContextTokens, sessionEntry.CacheRead, sessionEntry.CacheWrite,
		)
	default:
		return base
	}
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

func mergeSessionMeta(base map[string]any, patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

func withExclusiveSessionTurn(ctx context.Context, sessionID string, timeout time.Duration, fn func() error) error {
	if fn == nil {
		return fmt.Errorf("exclusive session function is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || controlSessionTurns == nil {
		return fn()
	}
	lockCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && timeout > 0 {
		lockCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	release, err := controlSessionTurns.Acquire(lockCtx, sessionID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("session %q lock canceled: %w", sessionID, err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("session %q lock timed out: %w", sessionID, err)
		}
		return fmt.Errorf("session %q busy: %w", sessionID, err)
	}
	defer release()
	return fn()
}

func sessionLastActivityUnix(doc state.SessionDoc) int64 {
	lastActivity := doc.LastInboundAt
	if doc.LastReplyAt > lastActivity {
		lastActivity = doc.LastReplyAt
	}
	return lastActivity
}

func shouldPruneSession(doc state.SessionDoc, cutoff time.Time) bool {
	lastActivity := sessionLastActivityUnix(doc)
	if lastActivity == 0 {
		return true
	}
	return !time.Unix(lastActivity, 0).After(cutoff)
}

func runSessionsPrune(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	req methods.SessionsPruneRequest,
	pruneReason string,
) (map[string]any, error) {
	if docsRepo == nil {
		return nil, fmt.Errorf("sessions.prune: docs repository is nil")
	}
	if transcriptRepo == nil {
		return nil, fmt.Errorf("sessions.prune: transcript repository is nil")
	}
	if !req.All && req.OlderThanDays <= 0 {
		return nil, fmt.Errorf("older_than_days must be > 0 unless all=true")
	}
	sessions, err := docsRepo.ListSessions(ctx, 10000)
	if err != nil {
		return nil, fmt.Errorf("sessions.prune: list: %w", err)
	}
	cutoff := time.Now().Add(-time.Duration(req.OlderThanDays) * 24 * time.Hour)
	var deletedIDs []string
	var skippedIDs []string
	var ineligibleIDs []string
	for _, sess := range sessions {
		eligible := req.All || shouldPruneSession(sess, cutoff)
		if !eligible {
			ineligibleIDs = append(ineligibleIDs, sess.SessionID)
			continue
		}
		if req.DryRun {
			deletedIDs = append(deletedIDs, sess.SessionID)
			continue
		}
		err := withExclusiveSessionTurn(ctx, sess.SessionID, 500*time.Millisecond, func() error {
			current, err := docsRepo.GetSession(ctx, sess.SessionID)
			if err != nil {
				return err
			}
			if !req.All && !shouldPruneSession(current, cutoff) {
				return nil
			}
			entries, _ := transcriptRepo.ListSessionAll(ctx, sess.SessionID)
			for _, e := range entries {
				if delErr := transcriptRepo.DeleteEntry(ctx, sess.SessionID, e.EntryID); delErr != nil {
					log.Printf("transcript delete failed session=%s entry=%s: %v", sess.SessionID, e.EntryID, delErr)
				}
			}
			_, err = updateExistingSessionDoc(ctx, docsRepo, sess.SessionID, current.PeerPubKey, func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{
					"deleted":      true,
					"deleted_at":   time.Now().Unix(),
					"prune_reason": pruneReason,
				})
				return nil
			})
			return err
		})
		if err != nil {
			skippedIDs = append(skippedIDs, sess.SessionID)
			continue
		}
		current, currentErr := docsRepo.GetSession(ctx, sess.SessionID)
		if currentErr != nil {
			skippedIDs = append(skippedIDs, sess.SessionID)
			continue
		}
		if deleted, _ := current.Meta["deleted"].(bool); !deleted {
			skippedIDs = append(skippedIDs, sess.SessionID)
			continue
		}
		deletedIDs = append(deletedIDs, sess.SessionID)
	}
	return map[string]any{
		"ok":               true,
		"dry_run":          req.DryRun,
		"deleted_count":    len(deletedIDs),
		"deleted":          deletedIDs,
		"skipped_count":    len(skippedIDs),
		"ineligible_count": len(ineligibleIDs),
		"ineligible":       ineligibleIDs,
	}, nil
}

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

func defaultAgentID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.EqualFold(id, "main") {
		return "main"
	}
	return id
}

func isKnownAgentID(ctx context.Context, docsRepo *state.DocsRepository, id string) error {
	agentID := defaultAgentID(id)
	if agentID == "main" || docsRepo == nil {
		return nil
	}
	doc, err := docsRepo.GetAgent(ctx, agentID)
	if err == nil {
		if doc.Deleted {
			return fmt.Errorf("unknown agent id %q", agentID)
		}
		return nil
	}
	if errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("unknown agent id %q", agentID)
	}
	return fmt.Errorf("failed to get agent: %w", err)
}

func defaultModelsCatalog(configProviders map[string]state.ProviderEntry) []map[string]any {
	catalog := []map[string]any{
		{"id": "echo", "name": "Echo (built-in)", "provider": "echo", "context_window": 8192, "reasoning": false},
	}

	// HTTP provider — available when METIQ_AGENT_HTTP_URL is set.
	if strings.TrimSpace(os.Getenv("METIQ_AGENT_HTTP_URL")) != "" {
		catalog = append(catalog, map[string]any{"id": "http-default", "name": "HTTP Provider", "provider": "http", "context_window": 16384, "reasoning": true, "configured": true})
	}

	// Well-known LLM providers — listed when their API key env var is set.
	type providerEntry struct {
		id, name, envKey string
		contextWindow    int
		reasoning        bool
	}
	knownProviders := []providerEntry{
		{"claude-sonnet-4-20250514", "Anthropic Claude", "ANTHROPIC_API_KEY", 200000, true},
		{"gpt-4o", "OpenAI GPT-4o", "OPENAI_API_KEY", 128000, true},
		{"gemini-2.5-pro", "Google Gemini", "GEMINI_API_KEY", 1000000, true},
		{"grok-3", "xAI Grok", "XAI_API_KEY", 131072, true},
		{"command-r-plus", "Cohere Command", "COHERE_API_KEY", 128000, false},
		{"groq/llama-4-scout-17b-16e-instruct", "Groq", "GROQ_API_KEY", 131072, false},
		{"mistral-large-latest", "Mistral AI", "MISTRAL_API_KEY", 128000, true},
		{"together/meta-llama/Llama-4-Scout-17B-16E-Instruct", "Together AI", "TOGETHER_API_KEY", 131072, false},
		{"openrouter/anthropic/claude-sonnet-4", "OpenRouter", "OPENROUTER_API_KEY", 200000, true},
	}
	for _, p := range knownProviders {
		configured := strings.TrimSpace(os.Getenv(p.envKey)) != ""
		catalog = append(catalog, map[string]any{
			"id": p.id, "name": p.name, "provider": strings.SplitN(p.id, "/", 2)[0],
			"context_window": p.contextWindow, "reasoning": p.reasoning, "configured": configured,
		})
	}

	// Providers from runtime config (extra entries from providers[] config section).
	if configProviders != nil {
		for providerID := range configProviders {
			// Skip if already covered by known entries.
			found := false
			for _, c := range catalog {
				id, _ := c["id"].(string)
				provider, _ := c["provider"].(string)
				if id == providerID || provider == providerID || strings.HasPrefix(id, providerID+"/") {
					found = true
					break
				}
			}
			if !found {
				catalog = append(catalog, map[string]any{
					"id": providerID, "name": providerID + " (config)", "provider": providerID,
					"context_window": 128000, "reasoning": true, "configured": true,
				})
			}
		}
	}
	return catalog
}

func defaultToolProfiles() []map[string]any {
	return agent.ProfilesAsResponse()
}

// configuredTranscriber returns a Transcriber based on the live config's
// extra.media_understanding.transcriber field, or nil if not specified there.
func configuredTranscriber(cfg state.ConfigDoc) mediapkg.Transcriber {
	mu, ok := cfg.Extra["media_understanding"].(map[string]any)
	if !ok {
		return nil
	}
	name, _ := mu["transcriber"].(string)
	if strings.TrimSpace(name) == "" {
		return nil
	}
	t, err := mediapkg.ResolveTranscriber(name)
	if err != nil {
		log.Printf("media transcriber config: %v", err)
		return nil
	}
	return t
}

func configuredPDFAllowedRoots(cfg state.ConfigDoc) []string {
	if toolsExtra, ok := cfg.Extra["tools"].(map[string]any); ok {
		if pdfExtra, ok := toolsExtra["pdf"].(map[string]any); ok {
			if roots, ok := extensionPolicyList(pdfExtra, "allowed_roots"); ok && len(roots) > 0 {
				return roots
			}
		}
	}
	if cfg.Extra != nil {
		if ws, ok := cfg.Extra["workspace"].(map[string]any); ok {
			if d, ok := ws["dir"].(string); ok && strings.TrimSpace(d) != "" {
				return []string{strings.TrimSpace(d)}
			}
		}
	}
	if d := strings.TrimSpace(os.Getenv("METIQ_WORKSPACE")); d != "" {
		return []string{d}
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return []string{"."}
	}
	return []string{filepath.Join(home, ".metiq", "workspace")}
}

func supportedMethods(cfg state.ConfigDoc) []string {
	base := append([]string{}, methods.SupportedMethods()...)
	seen := map[string]struct{}{}
	for _, method := range base {
		seen[method] = struct{}{}
	}
	for _, method := range extensionGatewayMethods(cfg) {
		if _, ok := seen[method]; ok {
			continue
		}
		seen[method] = struct{}{}
		base = append(base, method)
	}
	sort.Strings(base)
	return base
}

func extensionGatewayMethods(cfg state.ConfigDoc) []string {
	if cfg.Extra == nil {
		return nil
	}
	rawExt, ok := cfg.Extra["extensions"].(map[string]any)
	if !ok {
		return nil
	}
	rawEntries, ok := rawExt["entries"].(map[string]any)
	if !ok {
		return nil
	}
	methodsOut := make([]string, 0)
	seen := map[string]struct{}{}
	push := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		methodsOut = append(methodsOut, v)
	}
	for pluginID, rawEntry := range rawEntries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		if !extensionEntryAllowed(rawExt, pluginID, entry) {
			continue
		}
		for _, key := range []string{"gateway_methods", "gatewayMethods"} {
			switch vals := entry[key].(type) {
			case []string:
				for _, method := range vals {
					push(method)
				}
			case []any:
				for _, raw := range vals {
					if method, ok := raw.(string); ok {
						push(method)
					}
				}
			}
		}
	}
	sort.Strings(methodsOut)
	return methodsOut
}

func extensionEntryAllowed(rawExt map[string]any, pluginID string, entry map[string]any) bool {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return false
	}
	if enabled, ok := rawExt["enabled"].(bool); ok && !enabled {
		return false
	}
	if load, ok := rawExt["load"].(bool); ok && !load {
		return false
	}
	denyList, denyValid := extensionPolicyList(rawExt, "deny")
	if !denyValid {
		log.Printf("WARNING: invalid plugins.deny list type, blocking all plugins (fail-closed)")
		return false
	}
	deny := map[string]struct{}{}
	for _, item := range denyList {
		deny[item] = struct{}{}
	}
	if _, blocked := deny[pluginID]; blocked {
		return false
	}
	allow, allowValid := extensionPolicyList(rawExt, "allow")
	if !allowValid {
		log.Printf("WARNING: invalid plugins.allow list type, blocking all plugins (fail-closed)")
		return false
	}
	if len(allow) > 0 {
		allowed := false
		for _, candidate := range allow {
			if candidate == pluginID {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	if enabled, ok := entry["enabled"].(bool); ok && !enabled {
		return false
	}
	return true
}

func extensionPolicyList(rawExt map[string]any, key string) ([]string, bool) {
	raw, exists := rawExt[key]
	if !exists {
		return nil, true
	}
	switch values := raw.(type) {
	case []string:
		return sanitizeStrings(values), true
	case []any:
		out := make([]string, 0, len(values))
		for _, item := range values {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return sanitizeStrings(out), true
	default:
		return nil, false
	}
}

func sanitizeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

type coreToolSection struct {
	ID    string
	Label string
}

type coreToolDef struct {
	ID          string
	Label       string
	Description string
	SectionID   string
	Profiles    []string
}

var coreToolSections = []coreToolSection{
	{ID: "fs", Label: "Files"},
	{ID: "runtime", Label: "Runtime"},
	{ID: "web", Label: "Web"},
	{ID: "memory", Label: "Memory"},
	{ID: "sessions", Label: "Sessions"},
	{ID: "ui", Label: "UI"},
	{ID: "messaging", Label: "Messaging"},
	{ID: "automation", Label: "Automation"},
	{ID: "nodes", Label: "Nodes"},
	{ID: "agents", Label: "Agents"},
	{ID: "media", Label: "Media"},
}

var coreToolCatalog = []coreToolDef{
	{ID: "apply_patch", Label: "apply_patch", Description: "Patch files", SectionID: "fs", Profiles: []string{"coding"}},
	{ID: "read_pdf", Label: "read_pdf", Description: "Read local PDF files", SectionID: "fs", Profiles: []string{"coding"}},
	{ID: "memory_search", Label: "memory_search", Description: "Search memory index", SectionID: "memory", Profiles: []string{"coding"}},
	{ID: "memory_store", Label: "memory_store", Description: "Store memory entries", SectionID: "memory", Profiles: []string{"coding"}},
	{ID: "memory_delete", Label: "memory_delete", Description: "Delete memory entries", SectionID: "memory", Profiles: []string{"coding"}},
	{ID: "sessions_list", Label: "sessions_list", Description: "List sessions", SectionID: "sessions", Profiles: []string{"coding", "messaging"}},
	{ID: "session_spawn", Label: "session_spawn", Description: "Spawn sub-agent session", SectionID: "sessions", Profiles: []string{"coding"}},
	{ID: "session_send", Label: "session_send", Description: "Send to session", SectionID: "sessions", Profiles: []string{"coding", "messaging"}},
	{ID: "canvas_update", Label: "canvas_update", Description: "Update shared canvas", SectionID: "ui", Profiles: []string{}},
	{ID: "add_reaction", Label: "add_reaction", Description: "Add emoji reaction", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "remove_reaction", Label: "remove_reaction", Description: "Remove emoji reaction", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "send_typing", Label: "send_typing", Description: "Send typing indicator", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "send_in_thread", Label: "send_in_thread", Description: "Send message in thread", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "edit_message", Label: "edit_message", Description: "Edit channel message", SectionID: "messaging", Profiles: []string{"messaging"}},
	{ID: "cron_add", Label: "cron_add", Description: "Schedule recurring task", SectionID: "automation", Profiles: []string{"coding"}},
	{ID: "cron_list", Label: "cron_list", Description: "List scheduled tasks", SectionID: "automation", Profiles: []string{"coding"}},
	{ID: "cron_remove", Label: "cron_remove", Description: "Remove scheduled task", SectionID: "automation", Profiles: []string{"coding"}},
	{ID: "social_plan_add", Label: "social_plan_add", Description: "Add social action plan", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "social_plan_list", Label: "social_plan_list", Description: "List social plans & usage", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "social_plan_remove", Label: "social_plan_remove", Description: "Remove social plan", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "social_history", Label: "social_history", Description: "Query social action history", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "social_record", Label: "social_record", Description: "Record social action", SectionID: "social", Profiles: []string{"coding"}},
	{ID: "fleet_agents", Label: "fleet_agents", Description: "List fleet agents", SectionID: "fleet", Profiles: []string{"coding"}},
	{ID: "nostr_agent_rpc", Label: "nostr_agent_rpc", Description: "Sync RPC to fleet agent", SectionID: "fleet", Profiles: []string{"coding"}},
	{ID: "nostr_agent_send", Label: "nostr_agent_send", Description: "Async send to fleet agent", SectionID: "fleet", Profiles: []string{"coding"}},
	{ID: "nostr_agent_inbox", Label: "nostr_agent_inbox", Description: "Poll fleet agent replies", SectionID: "fleet", Profiles: []string{"coding"}},
	{ID: "node_invoke", Label: "node_invoke", Description: "Invoke a remote node", SectionID: "nodes", Profiles: []string{}},
	{ID: "node_list", Label: "node_list", Description: "List known nodes", SectionID: "nodes", Profiles: []string{}},
	{ID: "acp_delegate", Label: "acp_delegate", Description: "Delegate ACP task to peer", SectionID: "nodes", Profiles: []string{}},
	{ID: "web_search", Label: "web_search", Description: "Search the web", SectionID: "web", Profiles: []string{}},
	{ID: "web_fetch", Label: "web_fetch", Description: "Fetch web content", SectionID: "web", Profiles: []string{}},
	{ID: "image", Label: "image", Description: "Image understanding", SectionID: "media", Profiles: []string{"coding"}},
	{ID: "tts", Label: "tts", Description: "Text-to-speech conversion", SectionID: "media", Profiles: []string{}},
}

func buildToolCatalogGroups(cfg state.ConfigDoc, registry *agent.ToolRegistry, includePlugins *bool, pm *pluginmanager.GojaPluginManager) []map[string]any {
	sectionTools := map[string][]map[string]any{}
	seen := map[string]struct{}{}
	addCore := func(sectionID, id, label, description string, profiles []string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		entry := map[string]any{
			"id":              id,
			"label":           label,
			"description":     description,
			"source":          "core",
			"defaultProfiles": profiles,
		}
		sectionTools[sectionID] = append(sectionTools[sectionID], entry)
	}
	for _, tool := range coreToolCatalog {
		addCore(tool.SectionID, tool.ID, tool.Label, tool.Description, tool.Profiles)
	}
	for _, tool := range coreToolCatalog {
		if tool.ID != "" {
			seen[tool.ID] = struct{}{}
		}
	}
	groups := make([]map[string]any, 0, len(coreToolSections)+4)
	for _, section := range coreToolSections {
		tools := sectionTools[section.ID]
		if len(tools) == 0 {
			continue
		}
		sort.Slice(tools, func(i, j int) bool {
			return fmt.Sprintf("%v", tools[i]["id"]) < fmt.Sprintf("%v", tools[j]["id"])
		})
		groups = append(groups, map[string]any{
			"id":     section.ID,
			"label":  section.Label,
			"source": "core",
			"tools":  tools,
		})
	}
	if includePlugins != nil && !*includePlugins {
		return groups
	}
	for _, group := range buildPluginToolGroups(cfg, seen) {
		groups = append(groups, group)
	}
	// Append live Goja plugin tools (real manifests from loaded VMs).
	if pm != nil {
		for _, group := range pm.CatalogGroups(seen) {
			groups = append(groups, group)
		}
	}
	return groups
}

func buildPluginToolGroups(cfg state.ConfigDoc, seen map[string]struct{}) []map[string]any {
	if cfg.Extra == nil {
		return nil
	}
	rawExt, ok := cfg.Extra["extensions"].(map[string]any)
	if !ok {
		return nil
	}
	rawEntries, ok := rawExt["entries"].(map[string]any)
	if !ok {
		return nil
	}
	pluginIDs := make([]string, 0, len(rawEntries))
	for pluginID := range rawEntries {
		pluginIDs = append(pluginIDs, pluginID)
	}
	sort.Strings(pluginIDs)
	groups := make([]map[string]any, 0, len(pluginIDs))
	for _, pluginID := range pluginIDs {
		rawEntry, ok := rawEntries[pluginID].(map[string]any)
		if !ok {
			continue
		}
		if !extensionEntryAllowed(rawExt, pluginID, rawEntry) {
			continue
		}
		tools := make([]map[string]any, 0)
		switch rawTools := rawEntry["tools"].(type) {
		case []string:
			for _, t := range rawTools {
				trimmed := strings.TrimSpace(t)
				if trimmed == "" {
					continue
				}
				if _, exists := seen[trimmed]; exists {
					continue
				}
				seen[trimmed] = struct{}{}
				tools = append(tools, map[string]any{
					"id":              trimmed,
					"label":           trimmed,
					"description":     "Plugin tool",
					"source":          "plugin",
					"pluginId":        pluginID,
					"defaultProfiles": []string{},
				})
			}
		case []any:
			for _, rawTool := range rawTools {
				switch t := rawTool.(type) {
				case string:
					trimmed := strings.TrimSpace(t)
					if trimmed == "" {
						continue
					}
					if _, exists := seen[trimmed]; exists {
						continue
					}
					seen[trimmed] = struct{}{}
					tools = append(tools, map[string]any{
						"id":              trimmed,
						"label":           trimmed,
						"description":     "Plugin tool",
						"source":          "plugin",
						"pluginId":        pluginID,
						"defaultProfiles": []string{},
					})
				case map[string]any:
					idRaw, ok := t["id"].(string)
					if !ok {
						continue
					}
					id := strings.TrimSpace(idRaw)
					if id == "" {
						continue
					}
					if _, exists := seen[id]; exists {
						continue
					}
					seen[id] = struct{}{}
					label, _ := t["label"].(string)
					label = strings.TrimSpace(label)
					if label == "" {
						label = id
					}
					description, _ := t["description"].(string)
					description = strings.TrimSpace(description)
					if description == "" {
						description = "Plugin tool"
					}
					optional, hasOptional := t["optional"].(bool)
					profiles := getStringSlice(t, "default_profiles")
					if len(profiles) == 0 {
						profiles = getStringSlice(t, "defaultProfiles")
					}
					toolEntry := map[string]any{
						"id":              id,
						"label":           label,
						"description":     description,
						"source":          "plugin",
						"pluginId":        pluginID,
						"defaultProfiles": profiles,
					}
					// Only include optional field when explicitly true to reduce payload size.
					// Omitting the field is semantically equivalent to optional=false.
					if hasOptional && optional {
						toolEntry["optional"] = true
					}
					tools = append(tools, toolEntry)
				}
			}
		}
		if len(tools) == 0 {
			continue
		}
		sort.Slice(tools, func(i, j int) bool {
			return fmt.Sprintf("%v", tools[i]["id"]) < fmt.Sprintf("%v", tools[j]["id"])
		})
		groups = append(groups, map[string]any{
			"id":       "plugin:" + pluginID,
			"label":    pluginID,
			"source":   "plugin",
			"pluginId": pluginID,
			"tools":    tools,
		})
	}
	return groups
}

func extractSkillEntries(cfg state.ConfigDoc) map[string]map[string]any {
	out := map[string]map[string]any{}
	if cfg.Extra == nil {
		return out
	}
	rawSkills, ok := cfg.Extra["skills"].(map[string]any)
	if !ok {
		return out
	}
	rawEntries, ok := rawSkills["entries"].(map[string]any)
	if !ok {
		return out
	}
	for key, value := range rawEntries {
		entryMap, ok := value.(map[string]any)
		if !ok {
			continue
		}
		copyEntry := map[string]any{}
		for ek, ev := range entryMap {
			copyEntry[ek] = ev
		}
		out[key] = copyEntry
	}
	return out
}

func configWithSkillEntries(cfg state.ConfigDoc, entries map[string]map[string]any) state.ConfigDoc {
	next := cfg
	if next.Extra == nil {
		next.Extra = map[string]any{}
	}
	rawSkills := map[string]any{}
	if existing, ok := next.Extra["skills"].(map[string]any); ok {
		for key, value := range existing {
			rawSkills[key] = value
		}
	}
	rawEntries := map[string]any{}
	for key, entry := range entries {
		entryCopy := map[string]any{}
		for ek, ev := range entry {
			entryCopy[ek] = ev
		}
		rawEntries[key] = entryCopy
	}
	rawSkills["entries"] = rawEntries
	next.Extra["skills"] = rawSkills
	return next
}

func buildSkillsStatusReport(cfg state.ConfigDoc, agentID string) map[string]any {
	requirementsToMap := func(r skillspkg.Requirements) map[string]any {
		bins := r.Bins
		if bins == nil {
			bins = []string{}
		}
		anyBins := r.AnyBins
		if anyBins == nil {
			anyBins = []string{}
		}
		env := r.Env
		if env == nil {
			env = []string{}
		}
		osReq := r.OS
		if osReq == nil {
			osReq = []string{}
		}
		config := r.Config
		if config == nil {
			config = []string{}
		}
		return map[string]any{"bins": bins, "anyBins": anyBins, "env": env, "os": osReq, "config": config}
	}

	catalog, err := skillspkg.BuildSkillCatalog(cfg, agentID)
	if err != nil || catalog == nil {
		return map[string]any{
			"workspaceDir":     skillspkg.ResolveAgentWorkspaceDir(cfg, agentID),
			"managedSkillsDir": skillspkg.ManagedSkillsDir(),
			"skills":           []map[string]any{},
		}
	}

	skillsList := make([]map[string]any, 0, len(catalog.Skills))
	for _, resolved := range catalog.Skills {
		if resolved == nil || resolved.Skill == nil {
			continue
		}
		installSpecs := make([]map[string]any, 0)
		for _, spec := range resolved.Skill.InstallSpecs() {
			m := map[string]any{
				"id":    spec.ID,
				"kind":  spec.Kind,
				"label": spec.Label,
				"bins":  spec.Bins,
			}
			if spec.Formula != "" {
				m["formula"] = spec.Formula
			}
			if spec.Package != "" {
				m["package"] = spec.Package
			}
			if spec.Module != "" {
				m["module"] = spec.Module
			}
			if spec.URL != "" {
				m["url"] = spec.URL
			}
			installSpecs = append(installSpecs, m)
		}
		if len(installSpecs) == 0 {
			for _, step := range resolved.Skill.Manifest.Install {
				installSpecs = append(installSpecs, map[string]any{"cmd": step.Cmd, "cwd": step.Cwd})
			}
		}
		configChecks := make([]map[string]any, 0, len(resolved.ConfigChecks))
		for _, check := range resolved.ConfigChecks {
			configChecks = append(configChecks, map[string]any{"path": check.Path, "satisfied": check.Satisfied})
		}
		name := strings.TrimSpace(resolved.Skill.Manifest.Name)
		if name == "" {
			name = resolved.Skill.SkillKey
		}
		entry := map[string]any{
			"id":                     resolved.Skill.SkillKey,
			"status":                 resolved.Status,
			"name":                   name,
			"description":            strings.TrimSpace(resolved.Skill.Manifest.Description),
			"source":                 string(resolved.SourceKind),
			"bundled":                resolved.SourceKind == skillspkg.SkillSourceBundled,
			"filePath":               strings.TrimSpace(resolved.Skill.FilePath),
			"baseDir":                strings.TrimSpace(resolved.Skill.BaseDir),
			"skillKey":               resolved.Skill.SkillKey,
			"primaryEnv":             resolved.PrimaryEnv,
			"emoji":                  resolved.Skill.Emoji(),
			"homepage":               resolved.Skill.Manifest.Homepage,
			"always":                 resolved.Always,
			"disabled":               resolved.Disabled,
			"blockedByAllowlist":     resolved.BlockedByAllowlist,
			"eligible":               resolved.Eligible,
			"requirements":           requirementsToMap(resolved.EffectiveRequirements),
			"missing":                requirementsToMap(resolved.Missing),
			"configChecks":           configChecks,
			"install":                installSpecs,
			"whenToUse":              resolved.WhenToUse,
			"userInvocable":          resolved.UserInvocable,
			"disableModelInvocation": resolved.DisableModelInvocation,
		}
		if resolved.SelectedInstallID != "" {
			entry["selectedInstallId"] = resolved.SelectedInstallID
		}
		skillsList = append(skillsList, entry)
	}
	sort.Slice(skillsList, func(i, j int) bool {
		return fmt.Sprintf("%v", skillsList[i]["skillKey"]) < fmt.Sprintf("%v", skillsList[j]["skillKey"])
	})
	return map[string]any{
		"workspaceDir":     catalog.WorkspaceDir,
		"managedSkillsDir": catalog.ManagedSkillsDir,
		"skills":           skillsList,
	}
}

func applySkillsBins(cfg state.ConfigDoc) map[string]any {
	seen := map[string]struct{}{}
	bins := make([]string, 0)
	push := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		bins = append(bins, v)
	}

	agentIDs := []string{"main"}
	for _, ag := range cfg.Agents {
		if id := defaultAgentID(ag.ID); id != "main" {
			agentIDs = append(agentIDs, id)
		}
	}
	seenAgents := map[string]struct{}{}
	for _, agentID := range agentIDs {
		agentID = defaultAgentID(agentID)
		if _, ok := seenAgents[agentID]; ok {
			continue
		}
		seenAgents[agentID] = struct{}{}
		catalog, err := skillspkg.BuildSkillCatalog(cfg, agentID)
		if err != nil || catalog == nil {
			continue
		}
		finalSkills := make([]*skillspkg.Skill, 0, len(catalog.Skills))
		for _, resolved := range catalog.Skills {
			if resolved == nil || resolved.Skill == nil || resolved.SourceKind == skillspkg.SkillSourceConfig {
				continue
			}
			finalSkills = append(finalSkills, resolved.Skill)
		}
		for _, b := range skillspkg.AggregateBins(finalSkills) {
			push(b)
		}
	}
	sort.Strings(bins)
	return map[string]any{"bins": bins}
}

func coalesceString(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

var execCommandContext = exec.CommandContext

// findInstallSpec searches the merged skill catalog for the install spec with the
// given ID on the named skill.
func findInstallSpec(cfg state.ConfigDoc, agentID, name, installID string) (*skillspkg.InstallSpec, *skillspkg.ResolvedSkill, bool) {
	nameNorm := strings.ToLower(strings.TrimSpace(name))
	idNorm := strings.ToLower(strings.TrimSpace(installID))

	catalog, err := skillspkg.BuildSkillCatalog(cfg, agentID)
	if err != nil || catalog == nil {
		return nil, nil, false
	}
	for _, resolved := range catalog.Skills {
		if resolved == nil || resolved.Skill == nil {
			continue
		}
		skillName := strings.ToLower(strings.TrimSpace(resolved.Skill.Manifest.Name))
		if skillName != nameNorm && strings.ToLower(resolved.Skill.SkillKey) != nameNorm {
			continue
		}
		for _, spec := range resolved.Skill.InstallSpecs() {
			if strings.ToLower(spec.ID) == idNorm {
				cp := spec
				return &cp, resolved, true
			}
		}
	}
	return nil, nil, false
}

// runDownloadInstall downloads a binary from spec.URL into ~/.metiq/bin/.
func runDownloadInstall(ctx context.Context, spec skillspkg.InstallSpec) (stdout, stderr string, code int, err error) {
	if spec.URL == "" {
		return "", "download spec missing url", 1, fmt.Errorf("download spec missing url")
	}
	req, herr := http.NewRequestWithContext(ctx, http.MethodGet, spec.URL, nil)
	if herr != nil {
		return "", herr.Error(), 1, herr
	}
	resp, herr := http.DefaultClient.Do(req)
	if herr != nil {
		return "", herr.Error(), 1, herr
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("download failed: HTTP %d", resp.StatusCode)
		return "", msg, 1, fmt.Errorf("%s", msg)
	}
	filename := filepath.Base(resp.Request.URL.Path)
	if filename == "" || filename == "." || filename == "/" {
		filename = "download"
	}
	homeDir, _ := os.UserHomeDir()
	binDir := filepath.Join(homeDir, ".metiq", "bin")
	if merr := os.MkdirAll(binDir, 0o755); merr != nil {
		return "", merr.Error(), 1, merr
	}
	destPath := filepath.Join(binDir, filename)
	f, ferr := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if ferr != nil {
		return "", ferr.Error(), 1, ferr
	}
	defer f.Close()
	if _, copyErr := io.Copy(f, resp.Body); copyErr != nil {
		return "", copyErr.Error(), 1, copyErr
	}
	return fmt.Sprintf("Downloaded to %s", destPath), "", 0, nil
}

// runInstallSpec executes the installation command described by spec, using ctx
// (which should already have a deadline set from req.TimeoutMS).
func runInstallSpec(ctx context.Context, spec skillspkg.InstallSpec, prefs skillspkg.InstallPreferences) (stdout, stderr string, code int, err error) {
	var cmd *exec.Cmd
	kind := strings.ToLower(strings.TrimSpace(spec.Kind))
	if kind == "npm" {
		kind = "node"
	}
	switch kind {
	case "brew":
		formula := spec.Formula
		if formula == "" {
			formula = spec.Package
		}
		if formula == "" {
			return "", "brew spec missing formula/package", 1, fmt.Errorf("brew spec missing formula/package")
		}
		cmd = execCommandContext(ctx, "brew", "install", formula)
	case "npm", "node":
		if spec.Package == "" {
			return "", "node spec missing package", 1, fmt.Errorf("node spec missing package")
		}
		nodeManager := strings.TrimSpace(prefs.NodeManager)
		if nodeManager == "" {
			nodeManager = "npm"
		}
		switch nodeManager {
		case "pnpm":
			cmd = execCommandContext(ctx, "pnpm", "add", "-g", spec.Package)
		case "yarn":
			cmd = execCommandContext(ctx, "yarn", "global", "add", spec.Package)
		case "bun":
			cmd = execCommandContext(ctx, "bun", "install", "-g", spec.Package)
		default:
			cmd = execCommandContext(ctx, "npm", "install", "-g", spec.Package)
		}
	case "go":
		if spec.Module == "" {
			return "", "go spec missing module", 1, fmt.Errorf("go spec missing module")
		}
		cmd = execCommandContext(ctx, "go", "install", spec.Module+"@latest")
	case "uv":
		if spec.Package == "" {
			return "", "uv spec missing package", 1, fmt.Errorf("uv spec missing package")
		}
		cmd = execCommandContext(ctx, "uv", "tool", "install", spec.Package)
	case "apt":
		if spec.Package == "" {
			return "", "apt spec missing package", 1, fmt.Errorf("apt spec missing package")
		}
		cmd = execCommandContext(ctx, "apt-get", "install", "-y", spec.Package)
	case "download":
		return runDownloadInstall(ctx, spec)
	default:
		return "", fmt.Sprintf("unsupported install kind %q", spec.Kind), 1, fmt.Errorf("unsupported install kind %q", spec.Kind)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
		err = runErr
	}
	return
}

func applySkillInstall(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.SkillsInstallRequest) (state.ConfigDoc, map[string]any, error) {
	cfg := configState.Get()
	agentID := defaultAgentID(req.AgentID)
	installPrefs := skillspkg.ResolveInstallPreferences(cfg)

	// Find the install spec in the merged skill catalog.
	spec, resolved, found := findInstallSpec(cfg, agentID, req.Name, req.InstallID)

	var installResult map[string]any
	if !found {
		installResult = map[string]any{
			"ok":      false,
			"message": "Skill install option not found",
			"stdout":  "",
			"stderr":  "",
			"code":    1,
		}
		return state.ConfigDoc{}, installResult, nil
	} else {
		// Apply timeout from the request.
		timeout := time.Duration(req.TimeoutMS) * time.Millisecond
		installCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		outStr, errStr, exitCode, runErr := runInstallSpec(installCtx, *spec, installPrefs)
		if runErr != nil {
			installResult = map[string]any{
				"ok":      false,
				"message": fmt.Sprintf("install failed (exit %d)", exitCode),
				"stdout":  outStr,
				"stderr":  errStr,
				"code":    exitCode,
			}
			// Do not update config on failure.
			return state.ConfigDoc{}, installResult, nil
		}
		installResult = map[string]any{
			"ok":      true,
			"message": "Installed",
			"stdout":  outStr,
			"stderr":  errStr,
			"code":    exitCode,
		}
	}

	// Persist the install record in config.
	entries := extractSkillEntries(cfg)
	key := strings.ToLower(strings.TrimSpace(req.Name))
	if resolved != nil && resolved.Skill != nil && strings.TrimSpace(resolved.Skill.SkillKey) != "" {
		key = strings.ToLower(strings.TrimSpace(resolved.Skill.SkillKey))
	}
	entry, ok := entries[key]
	if !ok {
		entry = map[string]any{}
	}
	if resolved != nil && resolved.Skill != nil && strings.TrimSpace(resolved.Skill.Manifest.Name) != "" {
		entry["name"] = resolved.Skill.Manifest.Name
	} else {
		entry["name"] = req.Name
	}
	entry["install_id"] = req.InstallID
	entry["enabled"] = true
	entry["status"] = "installed"
	entry["updated_at"] = time.Now().Unix()
	entries[key] = entry
	next := configWithSkillEntries(cfg, entries)
	if err := persistRuntimeConfigFile(next); err != nil {
		return state.ConfigDoc{}, installResult, err
	}
	if _, err := docsRepo.PutConfig(ctx, next); err != nil {
		return state.ConfigDoc{}, installResult, err
	}
	configState.Set(next)
	skillspkg.InvalidateSkillCatalogCache()
	return next, installResult, nil
}

func applySkillUpdate(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.SkillsUpdateRequest) (state.ConfigDoc, map[string]any, error) {
	cfg := configState.Get()
	entries := extractSkillEntries(cfg)
	rawSkillKey := strings.TrimSpace(req.SkillKey)
	skillKey := strings.ToLower(rawSkillKey)
	if skillKey == "" {
		return state.ConfigDoc{}, nil, fmt.Errorf("skill key is required")
	}
	entry, ok := entries[skillKey]
	if !ok {
		for key, existing := range entries {
			if strings.EqualFold(key, skillKey) {
				if !ok {
					entry = existing
					ok = true
				}
				delete(entries, key)
			}
		}
	}
	if !ok {
		entry = map[string]any{}
	}
	if req.Enabled != nil {
		entry["enabled"] = *req.Enabled
	}
	if req.APIKey != nil {
		if strings.TrimSpace(*req.APIKey) == "" {
			delete(entry, "api_key")
		} else {
			entry["api_key"] = strings.TrimSpace(*req.APIKey)
		}
	}
	if req.Env != nil {
		nextEnv := map[string]any{}
		if currentEnv, ok := entry["env"].(map[string]any); ok {
			for key, value := range currentEnv {
				nextEnv[key] = value
			}
		}
		for key, value := range req.Env {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}
			trimmedVal := strings.TrimSpace(value)
			if trimmedVal == "" {
				delete(nextEnv, trimmedKey)
				continue
			}
			nextEnv[trimmedKey] = trimmedVal
		}
		if len(nextEnv) == 0 {
			delete(entry, "env")
		} else {
			entry["env"] = nextEnv
		}
	}
	entry["updated_at"] = time.Now().Unix()
	entries[skillKey] = entry
	next := configWithSkillEntries(cfg, entries)
	if err := persistRuntimeConfigFile(next); err != nil {
		return state.ConfigDoc{}, nil, err
	}
	if _, err := docsRepo.PutConfig(ctx, next); err != nil {
		return state.ConfigDoc{}, nil, err
	}
	configState.Set(next)
	skillspkg.InvalidateSkillCatalogCache()
	entryCopy := map[string]any{}
	for key, value := range entry {
		entryCopy[key] = value
	}
	return next, entryCopy, nil
}

func applyPluginInstallRuntime(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.PluginsInstallRequest) (map[string]any, error) {
	cfg := configState.Get()
	install := map[string]any{}
	for key, value := range req.Install {
		install[key] = value
	}
	enableEntry := req.EnableEntry != nil && *req.EnableEntry
	includeLoadPath := req.IncludeLoadPath != nil && *req.IncludeLoadPath
	source := strings.ToLower(strings.TrimSpace(getString(install, "source")))
	sourcePath := strings.TrimSpace(getString(install, "sourcePath"))
	if sourcePath == "" {
		sourcePath = strings.TrimSpace(getString(install, "source_path"))
	}
	installPath := strings.TrimSpace(getString(install, "installPath"))
	if installPath == "" {
		installPath = strings.TrimSpace(getString(install, "install_path"))
	}
	spec := strings.TrimSpace(getString(install, "spec"))
	inst := installer.New()
	var installResult installer.Result
	switch source {
	case "path", "archive":
		if sourcePath == "" {
			return nil, fmt.Errorf("install.sourcePath is required for source=%s", source)
		}
		if _, err := os.Stat(sourcePath); err != nil {
			return nil, fmt.Errorf("install.sourcePath not accessible: %w", err)
		}
		if installPath == "" {
			if source == "path" {
				installPath = sourcePath
			} else {
				installPath = "./extensions/" + req.PluginID
			}
		}
		if source == "archive" {
			if !strings.HasSuffix(strings.ToLower(sourcePath), ".tar.gz") && !strings.HasSuffix(strings.ToLower(sourcePath), ".tgz") && !strings.HasSuffix(strings.ToLower(sourcePath), ".zip") {
				return nil, fmt.Errorf("install.sourcePath for archive must be .tar.gz, .tgz, or .zip file")
			}
			managedPath, ok := resolveManagedInstallPath(installPath)
			if !ok {
				return nil, fmt.Errorf("install.installPath for source=archive must be within managed extensions directory")
			}
			installPath = managedPath
			res, err := inst.ExtractArchive(ctx, sourcePath, installPath)
			if err != nil {
				log.Printf("plugins.install archive error for %s: %v", req.PluginID, err)
				return nil, fmt.Errorf("archive extraction failed: %w", err)
			}
			installResult = res
		}
		install["installPath"] = installPath
	case "npm":
		if spec == "" {
			return nil, fmt.Errorf("install.spec is required for source=npm")
		}
		if !isValidNPMSpec(spec) {
			return nil, fmt.Errorf("install.spec contains invalid or unsafe characters")
		}
		if installPath == "" {
			installPath = "./extensions/" + req.PluginID
		}
		managedPath, ok := resolveManagedInstallPath(installPath)
		if !ok {
			return nil, fmt.Errorf("install.installPath for source=npm must be within managed extensions directory")
		}
		installPath = managedPath
		res, err := inst.InstallNPM(ctx, spec, installPath)
		if err != nil {
			log.Printf("plugins.install npm error for %s: %v\nstdout: %s\nstderr: %s", req.PluginID, err, res.Stdout, res.Stderr)
			return nil, fmt.Errorf("npm install failed: %w", err)
		}
		installResult = res
		if installResult.ResolvedVersion != "" {
			install["version"] = installResult.ResolvedVersion
		}
		if installResult.ResolvedSpec != "" {
			install["resolvedSpec"] = installResult.ResolvedSpec
		}
		if installResult.Integrity != "" {
			install["integrity"] = installResult.Integrity
		}
		install["installPath"] = installPath
	case "url":
		srcURL := strings.TrimSpace(getString(install, "url"))
		if srcURL == "" {
			srcURL = sourcePath
		}
		if srcURL == "" {
			return nil, fmt.Errorf("install.url is required for source=url")
		}
		tmpFile, err := installer.DownloadURL(ctx, srcURL)
		if err != nil {
			log.Printf("plugins.install url download error for %s: %v", req.PluginID, err)
			return nil, fmt.Errorf("URL download failed: %w", err)
		}
		defer os.Remove(tmpFile)

		// Determine whether the download is an archive or a JS file.
		lower := strings.ToLower(tmpFile)
		if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".zip") {
			// Archive: extract to managed path.
			if installPath == "" {
				installPath = "./extensions/" + req.PluginID
			}
			managedPath, ok := resolveManagedInstallPath(installPath)
			if !ok {
				return nil, fmt.Errorf("install.installPath for source=url archive must be within managed extensions directory")
			}
			installPath = managedPath
			res, err := inst.ExtractArchive(ctx, tmpFile, installPath)
			if err != nil {
				log.Printf("plugins.install url archive error for %s: %v", req.PluginID, err)
				return nil, fmt.Errorf("archive extraction failed: %w", err)
			}
			installResult = res
		} else {
			// Single JS file: copy to managed directory.
			if installPath == "" {
				installPath = "./extensions/" + req.PluginID
			}
			managedPath, ok := resolveManagedInstallPath(installPath)
			if !ok {
				return nil, fmt.Errorf("install.installPath for source=url must be within managed extensions directory")
			}
			if err := os.MkdirAll(managedPath, 0o755); err != nil {
				return nil, fmt.Errorf("create install directory: %w", err)
			}
			destFile := filepath.Join(managedPath, "index.js")
			data, err := os.ReadFile(tmpFile)
			if err != nil {
				return nil, fmt.Errorf("read downloaded file: %w", err)
			}
			if err := os.WriteFile(destFile, data, 0o644); err != nil {
				return nil, fmt.Errorf("write plugin file: %w", err)
			}
			installPath = managedPath
		}
		install["url"] = srcURL
		install["installPath"] = installPath
	default:
		return nil, fmt.Errorf("unsupported install.source %q", source)
	}
	next, err := methods.ApplyPluginInstallOperation(cfg, req.PluginID, install, enableEntry, includeLoadPath)
	if err != nil {
		return nil, err
	}
	if err := persistRuntimeConfigFile(next); err != nil {
		return nil, err
	}
	if _, err := docsRepo.PutConfig(ctx, next); err != nil {
		return nil, err
	}
	configState.Set(next)
	rawExt, _ := next.Extra["extensions"].(map[string]any)
	rawInstalls, _ := rawExt["installs"].(map[string]any)
	record, _ := rawInstalls[req.PluginID].(map[string]any)
	if record == nil {
		return nil, fmt.Errorf("install operation succeeded but record not found in config")
	}
	result := map[string]any{
		"ok":       true,
		"pluginId": req.PluginID,
		"install":  record,
		"enabled":  enableEntry,
		"source":   source,
	}
	if installResult.Stdout != "" {
		result["stdout"] = installResult.Stdout
	}
	if installResult.Stderr != "" {
		result["stderr"] = installResult.Stderr
	}
	// Notify WS clients that a plugin was installed.
	version := ""
	if v, ok := record["version"].(string); ok {
		version = v
	}
	emitControlWSEvent(gatewayws.EventPluginLoaded, gatewayws.PluginLoadedPayload{
		TS:       time.Now().UnixMilli(),
		PluginID: req.PluginID,
		Version:  version,
		Action:   "installed",
	})
	return result, nil
}

func applyPluginUninstallRuntime(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.PluginsUninstallRequest) (map[string]any, error) {
	cfg := configState.Get()
	var installRecord map[string]any
	if cfg.Extra != nil {
		if rawExt, ok := cfg.Extra["extensions"].(map[string]any); ok {
			if rawInstalls, ok := rawExt["installs"].(map[string]any); ok {
				installRecord, _ = rawInstalls[req.PluginID].(map[string]any)
			}
		}
	}
	next, actions, err := methods.ApplyPluginUninstallOperation(cfg, req.PluginID)
	if err != nil {
		if errors.Is(err, methods.ErrPluginNotFound) {
			return nil, state.ErrNotFound
		}
		return nil, err
	}
	if err := persistRuntimeConfigFile(next); err != nil {
		return nil, err
	}
	if _, err := docsRepo.PutConfig(ctx, next); err != nil {
		return nil, err
	}
	configState.Set(next)
	warnings := []string{}
	deletedFiles := false
	if installRecord != nil {
		source := strings.ToLower(strings.TrimSpace(getString(installRecord, "source")))
		installPath := strings.TrimSpace(getString(installRecord, "installPath"))
		if source != "path" && installPath != "" {
			if candidate, ok := resolveManagedInstallPath(installPath); ok {
				if err := os.RemoveAll(candidate); err != nil {
					warnings = append(warnings, fmt.Sprintf("failed to remove installPath %s: %v", candidate, err))
				} else {
					deletedFiles = true
				}
			} else {
				warnings = append(warnings, fmt.Sprintf("skipped uninstall deletion for unmanaged installPath %s", installPath))
			}
		}
	}
	return map[string]any{"ok": true, "pluginId": req.PluginID, "actions": actions, "deletedFiles": deletedFiles, "warnings": warnings}, nil
}

func applyPluginUpdateRuntime(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.PluginsUpdateRequest) (map[string]any, error) {
	cfg := configState.Get()
	runner := func(pluginID string, record map[string]any, dryRun bool) methods.PluginUpdateResult {
		currentVersion := strings.TrimSpace(getString(record, "version"))
		spec := strings.TrimSpace(getString(record, "spec"))
		pinned := parsePinnedNPMVersion(spec)
		if pinned != "" && pinned == currentVersion {
			return methods.PluginUpdateResult{
				Status:      methods.PluginUpdateStatusUnchanged,
				Message:     fmt.Sprintf("%s already at %s.", pluginID, currentVersion),
				NextVersion: currentVersion,
			}
		}
		if dryRun {
			nextVersion := pinned
			if nextVersion == "" {
				nextVersion = currentVersion
			}
			return methods.PluginUpdateResult{Status: methods.PluginUpdateStatusUpdated, Message: fmt.Sprintf("Would update %s.", pluginID), NextVersion: nextVersion}
		}
		installPath := strings.TrimSpace(getString(record, "installPath"))
		if installPath == "" {
			return methods.PluginUpdateResult{Status: methods.PluginUpdateStatusError, Message: fmt.Sprintf("No installPath for %s.", pluginID)}
		}
		// Safety: only allow updating within managed extensions directory
		if _, ok := resolveManagedInstallPath(installPath); !ok {
			return methods.PluginUpdateResult{Status: methods.PluginUpdateStatusError, Message: fmt.Sprintf("installPath for %s is outside managed directory.", pluginID)}
		}
		inst := installer.New()
		res, err := inst.UpdateNPM(ctx, spec, installPath)
		if err != nil {
			log.Printf("plugins.update npm error for %s: %v\nstdout: %s\nstderr: %s", pluginID, err, res.Stdout, res.Stderr)
			return methods.PluginUpdateResult{Status: methods.PluginUpdateStatusError, Message: fmt.Sprintf("npm update failed: %v", err)}
		}
		nextVersion := res.ResolvedVersion
		if nextVersion == "" {
			nextVersion = pinned
		}
		if nextVersion == "" {
			nextVersion = currentVersion
		}
		patch := map[string]any{}
		if res.ResolvedSpec != "" {
			patch["resolvedSpec"] = res.ResolvedSpec
		}
		if res.Integrity != "" {
			patch["integrity"] = res.Integrity
		}
		status := methods.PluginUpdateStatusUpdated
		if nextVersion == currentVersion && nextVersion != "" {
			status = methods.PluginUpdateStatusUnchanged
		}
		return methods.PluginUpdateResult{
			Status:      status,
			Message:     fmt.Sprintf("Updated %s to %s.", pluginID, nextVersion),
			NextVersion: nextVersion,
			InstallPath: res.InstallPath,
			RecordPatch: patch,
		}
	}
	next, changed, outcomes := methods.ApplyPluginUpdateOperation(cfg, req.PluginIDs, req.DryRun, runner)
	if changed {
		if err := persistRuntimeConfigFile(next); err != nil {
			return nil, err
		}
		if _, err := docsRepo.PutConfig(ctx, next); err != nil {
			return nil, err
		}
		configState.Set(next)
	}
	return map[string]any{"ok": true, "changed": changed, "outcomes": outcomes}, nil
}

// ─── Plugin registry handlers ──────────────────────────────────────────────────

// resolveRegistryURL returns the registry URL to use for a request:
// the request's RegistryURL (if set) or the daemon's configured registry URL.
func resolveRegistryURL(configState *runtimeConfigStore, requestURL string) (string, error) {
	u := strings.TrimSpace(requestURL)
	if u != "" {
		return u, nil
	}
	cfg := configState.Get()
	if cfg.Extra != nil {
		if rawExt, ok := cfg.Extra["extensions"].(map[string]any); ok {
			if regURL, ok := rawExt["registry_url"].(string); ok && strings.TrimSpace(regURL) != "" {
				return strings.TrimSpace(regURL), nil
			}
		}
	}
	// Fall back to the default public registry.
	return "https://registry.metiq.com/plugins/index.json", nil
}

func handlePluginsRegistryList(ctx context.Context, configState *runtimeConfigStore, req methods.PluginsRegistryListRequest) (map[string]any, error) {
	regURL, err := resolveRegistryURL(configState, req.RegistryURL)
	if err != nil {
		return nil, err
	}
	idx, err := installer.FetchRegistry(ctx, regURL)
	if err != nil {
		return nil, fmt.Errorf("registry fetch failed: %w", err)
	}
	plugins := make([]map[string]any, 0, len(idx.Plugins))
	for _, p := range idx.Plugins {
		plugins = append(plugins, pluginEntryToMap(p))
	}
	return map[string]any{
		"ok":          true,
		"registryURL": regURL,
		"version":     idx.Version,
		"plugins":     plugins,
		"count":       len(plugins),
	}, nil
}

func handlePluginsRegistryGet(ctx context.Context, configState *runtimeConfigStore, req methods.PluginsRegistryGetRequest) (map[string]any, error) {
	if strings.TrimSpace(req.PluginID) == "" {
		return nil, fmt.Errorf("plugin_id is required")
	}
	regURL, err := resolveRegistryURL(configState, req.RegistryURL)
	if err != nil {
		return nil, err
	}
	idx, err := installer.FetchRegistry(ctx, regURL)
	if err != nil {
		return nil, fmt.Errorf("registry fetch failed: %w", err)
	}
	for _, p := range idx.Plugins {
		if strings.EqualFold(p.ID, req.PluginID) {
			return map[string]any{
				"ok":          true,
				"registryURL": regURL,
				"plugin":      pluginEntryToMap(p),
			}, nil
		}
	}
	return nil, state.ErrNotFound
}

func handlePluginsRegistrySearch(ctx context.Context, configState *runtimeConfigStore, req methods.PluginsRegistrySearchRequest) (map[string]any, error) {
	regURL, err := resolveRegistryURL(configState, req.RegistryURL)
	if err != nil {
		return nil, err
	}
	idx, err := installer.FetchRegistry(ctx, regURL)
	if err != nil {
		return nil, fmt.Errorf("registry fetch failed: %w", err)
	}
	query := strings.ToLower(strings.TrimSpace(req.Query))
	tag := strings.ToLower(strings.TrimSpace(req.Tag))
	results := make([]map[string]any, 0)
	for _, p := range idx.Plugins {
		if tag != "" {
			matched := false
			for _, t := range p.Tags {
				if strings.EqualFold(t, tag) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if query != "" {
			haystack := strings.ToLower(p.ID + " " + p.Name + " " + p.Description + " " + strings.Join(p.Tags, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		results = append(results, pluginEntryToMap(p))
	}
	return map[string]any{
		"ok":          true,
		"registryURL": regURL,
		"query":       req.Query,
		"tag":         req.Tag,
		"plugins":     results,
		"count":       len(results),
	}, nil
}

func pluginEntryToMap(p installer.RegistryPlugin) map[string]any {
	m := map[string]any{
		"id":          p.ID,
		"name":        p.Name,
		"description": p.Description,
		"url":         p.URL,
	}
	if p.Version != "" {
		m["version"] = p.Version
	}
	if p.Type != "" {
		m["type"] = p.Type
	}
	if p.Author != "" {
		m["author"] = p.Author
	}
	if p.License != "" {
		m["license"] = p.License
	}
	if len(p.Tags) > 0 {
		m["tags"] = p.Tags
	}
	return m
}

func isValidNPMSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	if len(spec) > 500 {
		return false
	}
	if strings.ContainsAny(spec, ";|&$`\n\r\t<>(){}") {
		return false
	}
	if strings.Contains(spec, "  ") {
		return false
	}
	return true
}

func parsePinnedNPMVersion(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	at := strings.LastIndex(spec, "@")
	if at <= 0 || at == len(spec)-1 {
		return ""
	}
	version := strings.TrimSpace(spec[at+1:])
	if version == "" || strings.EqualFold(version, "latest") {
		return ""
	}
	return version
}

func resolveManagedInstallPath(pathValue string) (string, bool) {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return "", false
	}
	root, err := filepath.Abs("./extensions")
	if err != nil {
		return "", false
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootResolved = root
	}
	candidate, err := filepath.Abs(filepath.Clean(pathValue))
	if err != nil {
		return "", false
	}
	candidateResolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		candidateResolved = candidate
	}
	rel, err := filepath.Rel(rootResolved, candidateResolved)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || rel == ".." {
		return "", false
	}
	return candidate, true
}

func pairingData(cfg state.ConfigDoc) map[string]any {
	if cfg.Extra == nil {
		cfg.Extra = map[string]any{}
	}
	pairing, _ := cfg.Extra["pairing"].(map[string]any)
	if pairing == nil {
		pairing = map[string]any{}
	}
	return pairing
}

func toRecordSlice(raw any) []map[string]any {
	out := []map[string]any{}
	switch arr := raw.(type) {
	case []map[string]any:
		for _, item := range arr {
			out = append(out, item)
		}
		return out
	case []any:
		for _, item := range arr {
			m, ok := item.(map[string]any)
			if ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return out
	}
}

func applyPairingConfigUpdate(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, mutator func(map[string]any) (map[string]any, map[string]any, error)) (map[string]any, error) {
	controlPairingConfigMu.Lock()
	defer controlPairingConfigMu.Unlock()

	cfg := configState.Get()
	pairing := pairingData(cfg)
	nextPairing, result, err := mutator(pairing)
	if err != nil {
		return nil, err
	}
	if cfg.Extra == nil {
		cfg.Extra = map[string]any{}
	}
	cfg.Extra["pairing"] = nextPairing
	if err := persistRuntimeConfigFile(cfg); err != nil {
		return nil, err
	}
	if _, err := docsRepo.PutConfig(ctx, cfg); err != nil {
		return nil, err
	}
	configState.Set(cfg)
	return result, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomRequestID(prefix string) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s", prefix, tok), nil
}

func copyRecord(record map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range record {
		out[key] = value
	}
	return out
}

func getString(record map[string]any, key string) string {
	return strings.TrimSpace(fmt.Sprintf("%v", record[key]))
}

func getStringSlice(record map[string]any, key string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	switch values := record[key].(type) {
	case []string:
		for _, value := range values {
			v := strings.TrimSpace(value)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	case []any:
		for _, raw := range values {
			v := strings.TrimSpace(fmt.Sprintf("%v", raw))
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func getInt64(record map[string]any, key string) int64 {
	switch v := record[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func mergeUniqueStrings(values ...[]string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, list := range values {
		for _, value := range list {
			v := strings.TrimSpace(value)
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

func sortRecordsByKeyDesc(records []map[string]any, key string) {
	sort.Slice(records, func(i, j int) bool {
		return getInt64(records[i], key) > getInt64(records[j], key)
	})
}

func scopesAllow(requested []string, allowed []string) bool {
	if len(requested) == 0 {
		return true
	}
	if len(allowed) == 0 {
		return false
	}
	allowedSet := map[string]struct{}{}
	for _, scope := range allowed {
		allowedSet[scope] = struct{}{}
	}
	for _, scope := range requested {
		if _, ok := allowedSet[scope]; !ok {
			return false
		}
	}
	return true
}

func redactDeviceForList(record map[string]any) map[string]any {
	out := copyRecord(record)
	if tokens, ok := record["tokens"].(map[string]any); ok {
		summaries := make([]map[string]any, 0, len(tokens))
		for _, raw := range tokens {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			summary := map[string]any{
				"role":          getString(entry, "role"),
				"scopes":        getStringSlice(entry, "scopes"),
				"created_at_ms": getInt64(entry, "created_at_ms"),
			}
			if ts := getInt64(entry, "rotated_at_ms"); ts > 0 {
				summary["rotated_at_ms"] = ts
			}
			if ts := getInt64(entry, "revoked_at_ms"); ts > 0 {
				summary["revoked_at_ms"] = ts
			}
			if ts := getInt64(entry, "last_used_at_ms"); ts > 0 {
				summary["last_used_at_ms"] = ts
			}
			summaries = append(summaries, summary)
		}
		sort.Slice(summaries, func(i, j int) bool {
			return fmt.Sprintf("%v", summaries[i]["role"]) < fmt.Sprintf("%v", summaries[j]["role"])
		})
		out["tokens"] = summaries
	}
	delete(out, "approved_scopes")
	return out
}

func redactNodeForList(record map[string]any) map[string]any {
	return config.RedactMap(record)
}

func buildNodePendingRecord(req methods.NodePairRequest, isRepair bool, requestID string, ts int64) map[string]any {
	record := map[string]any{
		"request_id": requestID,
		"node_id":    req.NodeID,
		"silent":     req.Silent,
		"is_repair":  isRepair,
		"ts":         ts,
	}
	if req.DisplayName != "" {
		record["display_name"] = req.DisplayName
	}
	if req.Platform != "" {
		record["platform"] = req.Platform
	}
	if req.Version != "" {
		record["version"] = req.Version
	}
	if req.CoreVersion != "" {
		record["core_version"] = req.CoreVersion
	}
	if req.UIVersion != "" {
		record["ui_version"] = req.UIVersion
	}
	if req.DeviceFamily != "" {
		record["device_family"] = req.DeviceFamily
	}
	if req.ModelIdentifier != "" {
		record["model_identifier"] = req.ModelIdentifier
	}
	if len(req.Caps) > 0 {
		record["caps"] = req.Caps
	}
	if len(req.Commands) > 0 {
		record["commands"] = req.Commands
	}
	if len(req.Permissions) > 0 {
		record["permissions"] = req.Permissions
	}
	if req.RemoteIP != "" {
		record["remote_ip"] = req.RemoteIP
	}
	return record
}

func applyNodePairRequest(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.NodePairRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		pending := toRecordSlice(pairing["node_pending"])
		paired := toRecordSlice(pairing["node_paired"])
		isRepair := false
		for _, p := range paired {
			if getString(p, "node_id") == req.NodeID {
				isRepair = true
				break
			}
		}
		for i, item := range pending {
			if getString(item, "node_id") != req.NodeID {
				continue
			}
			requestID := getString(item, "request_id")
			if requestID == "" {
				var err error
				requestID, err = randomRequestID("node")
				if err != nil {
					return nil, nil, err
				}
			}
			record := buildNodePendingRecord(req, isRepair, requestID, now)
			pending[i] = record
			pairing["node_pending"] = pending
			return pairing, map[string]any{"status": "pending", "created": false, "request": record}, nil
		}
		requestID, err := randomRequestID("node")
		if err != nil {
			return nil, nil, err
		}
		record := buildNodePendingRecord(req, isRepair, requestID, now)
		pending = append(pending, record)
		sortRecordsByKeyDesc(pending, "ts")
		pairing["node_pending"] = pending
		return pairing, map[string]any{"status": "pending", "created": true, "request": record}, nil
	})
}

func applyNodePairList(_ context.Context, configState *runtimeConfigStore, _ methods.NodePairListRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	pending := toRecordSlice(pairing["node_pending"])
	paired := toRecordSlice(pairing["node_paired"])
	sortRecordsByKeyDesc(pending, "ts")
	sortRecordsByKeyDesc(paired, "approved_at_ms")
	return map[string]any{"pending": pending, "paired": paired}, nil
}

func applyNodePairApprove(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.NodePairApproveRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		pending := toRecordSlice(pairing["node_pending"])
		paired := toRecordSlice(pairing["node_paired"])
		remaining := make([]map[string]any, 0, len(pending))
		var approved map[string]any
		for _, item := range pending {
			if getString(item, "request_id") == req.RequestID {
				approved = item
				continue
			}
			remaining = append(remaining, item)
		}
		if approved == nil {
			return nil, nil, state.ErrNotFound
		}
		token, err := randomToken()
		if err != nil {
			return nil, nil, err
		}
		nodeID := getString(approved, "node_id")
		createdAt := now
		filtered := make([]map[string]any, 0, len(paired))
		for _, node := range paired {
			if getString(node, "node_id") == nodeID {
				if prior := getInt64(node, "created_at_ms"); prior > 0 {
					createdAt = prior
				}
				continue
			}
			filtered = append(filtered, node)
		}
		node := map[string]any{
			"node_id":        nodeID,
			"token":          token,
			"created_at_ms":  createdAt,
			"approved_at_ms": now,
		}
		for _, key := range []string{"display_name", "platform", "version", "core_version", "ui_version", "device_family", "model_identifier", "caps", "commands", "permissions", "remote_ip"} {
			if value, ok := approved[key]; ok {
				node[key] = value
			}
		}
		filtered = append(filtered, node)
		sortRecordsByKeyDesc(filtered, "approved_at_ms")
		pairing["node_pending"] = remaining
		pairing["node_paired"] = filtered
		return pairing, map[string]any{"request_id": req.RequestID, "node": node}, nil
	})
}

func applyNodePairReject(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.NodePairRejectRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		pending := toRecordSlice(pairing["node_pending"])
		remaining := make([]map[string]any, 0, len(pending))
		var nodeID string
		for _, item := range pending {
			if getString(item, "request_id") == req.RequestID {
				nodeID = getString(item, "node_id")
				continue
			}
			remaining = append(remaining, item)
		}
		if nodeID == "" {
			return nil, nil, state.ErrNotFound
		}
		pairing["node_pending"] = remaining
		return pairing, map[string]any{"request_id": req.RequestID, "node_id": nodeID}, nil
	})
}

func applyNodePairVerify(_ context.Context, configState *runtimeConfigStore, req methods.NodePairVerifyRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	for _, item := range toRecordSlice(pairing["node_paired"]) {
		if getString(item, "node_id") == req.NodeID && getString(item, "token") == req.Token {
			return map[string]any{"ok": true, "node": item}, nil
		}
	}
	return map[string]any{"ok": false}, nil
}

func applyDevicePairList(_ context.Context, configState *runtimeConfigStore, _ methods.DevicePairListRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	pending := toRecordSlice(pairing["device_pending"])
	paired := toRecordSlice(pairing["device_paired"])
	sortRecordsByKeyDesc(pending, "ts")
	sortRecordsByKeyDesc(paired, "approved_at_ms")
	redacted := make([]map[string]any, 0, len(paired))
	for _, device := range paired {
		redacted = append(redacted, redactDeviceForList(device))
	}
	return map[string]any{"pending": pending, "paired": redacted}, nil
}

func applyDevicePairApprove(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DevicePairApproveRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		pending := toRecordSlice(pairing["device_pending"])
		paired := toRecordSlice(pairing["device_paired"])
		remaining := make([]map[string]any, 0, len(pending))
		var approved map[string]any
		for _, item := range pending {
			if getString(item, "request_id") == req.RequestID {
				approved = item
				continue
			}
			remaining = append(remaining, item)
		}
		if approved == nil {
			return nil, nil, state.ErrNotFound
		}
		deviceID := getString(approved, "device_id")
		if deviceID == "" {
			return nil, nil, fmt.Errorf("invalid pending pairing record")
		}
		device := copyRecord(approved)
		createdAt := now
		approvedScopes := getStringSlice(approved, "scopes")
		tokens := map[string]any{}
		filtered := make([]map[string]any, 0, len(paired))
		for _, item := range paired {
			if getString(item, "device_id") != deviceID {
				filtered = append(filtered, item)
				continue
			}
			if prior := getInt64(item, "created_at_ms"); prior > 0 {
				createdAt = prior
			}
			approvedScopes = mergeUniqueStrings(getStringSlice(item, "approved_scopes"), approvedScopes)
			if existingTokens, ok := item["tokens"].(map[string]any); ok {
				for key, value := range existingTokens {
					tokens[key] = value
				}
			}
		}
		role := getString(approved, "role")
		if role != "" {
			existing, _ := tokens[role].(map[string]any)
			scopes := getStringSlice(approved, "scopes")
			if len(scopes) == 0 {
				scopes = getStringSlice(existing, "scopes")
			}
			if len(scopes) == 0 {
				scopes = approvedScopes
			}
			tok, err := randomToken()
			if err != nil {
				return nil, nil, err
			}
			entry := map[string]any{
				"token":         tok,
				"role":          role,
				"scopes":        scopes,
				"created_at_ms": now,
			}
			if existing != nil {
				if created := getInt64(existing, "created_at_ms"); created > 0 {
					entry["created_at_ms"] = created
				}
				entry["rotated_at_ms"] = now
				if last := getInt64(existing, "last_used_at_ms"); last > 0 {
					entry["last_used_at_ms"] = last
				}
			}
			tokens[role] = entry
		}
		device["approved_scopes"] = approvedScopes
		device["scopes"] = approvedScopes
		device["tokens"] = tokens
		device["created_at_ms"] = createdAt
		device["approved_at_ms"] = now
		delete(device, "request_id")
		delete(device, "ts")
		filtered = append(filtered, device)
		sortRecordsByKeyDesc(filtered, "approved_at_ms")
		pairing["device_pending"] = remaining
		pairing["device_paired"] = filtered
		return pairing, map[string]any{"request_id": req.RequestID, "device": redactDeviceForList(device)}, nil
	})
}

func applyDevicePairReject(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DevicePairRejectRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		pending := toRecordSlice(pairing["device_pending"])
		remaining := make([]map[string]any, 0, len(pending))
		var deviceID string
		for _, item := range pending {
			if getString(item, "request_id") == req.RequestID {
				deviceID = getString(item, "device_id")
				continue
			}
			remaining = append(remaining, item)
		}
		if deviceID == "" {
			return nil, nil, state.ErrNotFound
		}
		pairing["device_pending"] = remaining
		return pairing, map[string]any{"request_id": req.RequestID, "device_id": deviceID}, nil
	})
}

func applyDevicePairRemove(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DevicePairRemoveRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		paired := toRecordSlice(pairing["device_paired"])
		remaining := make([]map[string]any, 0, len(paired))
		removed := false
		for _, item := range paired {
			if getString(item, "device_id") == req.DeviceID {
				removed = true
				continue
			}
			remaining = append(remaining, item)
		}
		if !removed {
			return nil, nil, state.ErrNotFound
		}
		pairing["device_paired"] = remaining
		return pairing, map[string]any{"device_id": req.DeviceID}, nil
	})
}

func applyDeviceTokenRotate(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DeviceTokenRotateRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		paired := toRecordSlice(pairing["device_paired"])
		for _, item := range paired {
			if getString(item, "device_id") != req.DeviceID {
				continue
			}
			tokens, _ := item["tokens"].(map[string]any)
			if tokens == nil {
				tokens = map[string]any{}
			}
			existing, _ := tokens[req.Role].(map[string]any)
			scopes := req.Scopes
			if len(scopes) == 0 {
				scopes = getStringSlice(existing, "scopes")
			}
			if len(scopes) == 0 {
				scopes = getStringSlice(item, "approved_scopes")
			}
			if !scopesAllow(scopes, getStringSlice(item, "approved_scopes")) {
				return nil, nil, fmt.Errorf("invalid scopes for role")
			}
			tok, err := randomToken()
			if err != nil {
				return nil, nil, err
			}
			entry := map[string]any{
				"token":         tok,
				"role":          req.Role,
				"scopes":        scopes,
				"created_at_ms": now,
				"rotated_at_ms": now,
			}
			if existing != nil {
				if created := getInt64(existing, "created_at_ms"); created > 0 {
					entry["created_at_ms"] = created
				}
				if last := getInt64(existing, "last_used_at_ms"); last > 0 {
					entry["last_used_at_ms"] = last
				}
			}
			tokens[req.Role] = entry
			item["tokens"] = tokens
			pairing["device_paired"] = paired
			return pairing, map[string]any{"device_id": req.DeviceID, "role": req.Role, "token": tok, "scopes": scopes, "rotated_at_ms": now}, nil
		}
		return nil, nil, state.ErrNotFound
	})
}

func applyDeviceTokenRevoke(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.DeviceTokenRevokeRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		now := time.Now().UnixMilli()
		paired := toRecordSlice(pairing["device_paired"])
		for _, item := range paired {
			if getString(item, "device_id") != req.DeviceID {
				continue
			}
			tokens, _ := item["tokens"].(map[string]any)
			if tokens == nil {
				return nil, nil, state.ErrNotFound
			}
			tok, ok := tokens[req.Role].(map[string]any)
			if !ok {
				return nil, nil, state.ErrNotFound
			}
			tok["revoked_at_ms"] = now
			tokens[req.Role] = tok
			item["tokens"] = tokens
			pairing["device_paired"] = paired
			return pairing, map[string]any{"device_id": req.DeviceID, "role": req.Role, "revoked_at_ms": now}, nil
		}
		return nil, nil, state.ErrNotFound
	})
}

func applyNodeList(configState *runtimeConfigStore, req methods.NodeListRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	nodes := toRecordSlice(pairing["node_paired"])
	sortRecordsByKeyDesc(nodes, "approved_at_ms")
	if req.Limit > 0 && len(nodes) > req.Limit {
		nodes = nodes[:req.Limit]
	}
	redacted := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		redacted = append(redacted, redactNodeForList(node))
	}
	return map[string]any{"nodes": redacted, "count": len(redacted)}, nil
}

func applyNodeDescribe(configState *runtimeConfigStore, req methods.NodeDescribeRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	for _, node := range toRecordSlice(pairing["node_paired"]) {
		if getString(node, "node_id") == req.NodeID {
			return map[string]any{"node": redactNodeForList(node), "status": "paired"}, nil
		}
	}
	for _, node := range toRecordSlice(pairing["node_pending"]) {
		if getString(node, "node_id") == req.NodeID {
			return map[string]any{"node": redactNodeForList(node), "status": "pending"}, nil
		}
	}
	return nil, state.ErrNotFound
}

func applyNodeRename(ctx context.Context, docsRepo *state.DocsRepository, configState *runtimeConfigStore, req methods.NodeRenameRequest) (map[string]any, error) {
	return applyPairingConfigUpdate(ctx, docsRepo, configState, func(pairing map[string]any) (map[string]any, map[string]any, error) {
		updated := false
		paired := toRecordSlice(pairing["node_paired"])
		for _, node := range paired {
			if getString(node, "node_id") == req.NodeID {
				node["display_name"] = req.Name
				updated = true
			}
		}
		pending := toRecordSlice(pairing["node_pending"])
		for _, node := range pending {
			if getString(node, "node_id") == req.NodeID {
				node["display_name"] = req.Name
				updated = true
			}
		}
		if !updated {
			return nil, nil, state.ErrNotFound
		}
		pairing["node_paired"] = paired
		pairing["node_pending"] = pending
		return pairing, map[string]any{"ok": true, "node_id": req.NodeID, "name": req.Name}, nil
	})
}

func applyNodeCanvasCapabilityRefresh(configState *runtimeConfigStore, req methods.NodeCanvasCapabilityRefreshRequest) (map[string]any, error) {
	pairing := pairingData(configState.Get())
	for _, node := range toRecordSlice(pairing["node_paired"]) {
		if getString(node, "node_id") == req.NodeID {
			caps := getStringSlice(node, "caps")
			return map[string]any{"ok": true, "node_id": req.NodeID, "caps": caps, "refreshed_at_ms": time.Now().UnixMilli()}, nil
		}
	}
	return nil, state.ErrNotFound
}

func applyNodeInvoke(reg *nodeInvocationRegistry, req methods.NodeInvokeRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("node invoke runtime not configured")
	}
	rec := reg.Begin(req)
	return map[string]any{
		"ok":         true,
		"run_id":     rec.RunID,
		"node_id":    rec.NodeID,
		"command":    rec.Command,
		"status":     rec.Status,
		"created_at": rec.CreatedAt,
	}, nil
}

func applyNodeEvent(reg *nodeInvocationRegistry, req methods.NodeEventRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("node invoke runtime not configured")
	}
	rec, err := reg.AddEvent(req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         true,
		"run_id":     rec.RunID,
		"node_id":    rec.NodeID,
		"status":     rec.Status,
		"updated_at": rec.UpdatedAt,
		"events":     rec.Events,
	}, nil
}

func applyNodeResult(reg *nodeInvocationRegistry, req methods.NodeResultRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("node invoke runtime not configured")
	}
	rec, err := reg.SetResult(req)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         true,
		"run_id":     rec.RunID,
		"node_id":    rec.NodeID,
		"status":     rec.Status,
		"result":     rec.Result,
		"error":      rec.Error,
		"updated_at": rec.UpdatedAt,
	}, nil
}

func applyCronList(reg *cronRegistry, req methods.CronListRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	jobs := reg.List(req.Limit)
	return map[string]any{"jobs": jobs, "count": len(jobs)}, nil
}

func applyCronStatus(reg *cronRegistry, req methods.CronStatusRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	job, ok := reg.Status(req.ID)
	if !ok {
		return nil, state.ErrNotFound
	}
	return map[string]any{"job": job}, nil
}

func applyCronAdd(reg *cronRegistry, req methods.CronAddRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	if _, err := cron.Parse(req.Schedule); err != nil {
		return nil, fmt.Errorf("invalid cron schedule %q: %w", req.Schedule, err)
	}
	job := reg.Add(req)
	return map[string]any{"ok": true, "job": job}, nil
}

func applyCronUpdate(reg *cronRegistry, req methods.CronUpdateRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	if req.Schedule != "" {
		if _, err := cron.Parse(req.Schedule); err != nil {
			return nil, fmt.Errorf("invalid cron schedule %q: %w", req.Schedule, err)
		}
	}
	job, err := reg.Update(req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "job": job}, nil
}

func applyCronRemove(reg *cronRegistry, req methods.CronRemoveRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	if err := reg.Remove(req.ID); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": req.ID, "removed": true}, nil
}

func applyCronRun(reg *cronRegistry, req methods.CronRunRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	emitControlWSEvent(gatewayws.EventCronTick, gatewayws.CronTickPayload{
		TS:    time.Now().UnixMilli(),
		JobID: req.ID,
	})
	started := time.Now()
	run, err := reg.Run(req.ID)
	if err != nil {
		return nil, err
	}
	emitControlWSEvent(gatewayws.EventCronResult, gatewayws.CronResultPayload{
		TS:         time.Now().UnixMilli(),
		JobID:      req.ID,
		Succeeded:  run.Status == "done",
		DurationMS: time.Since(started).Milliseconds(),
	})
	return map[string]any{"ok": true, "run": run}, nil
}

func applyCronRuns(reg *cronRegistry, req methods.CronRunsRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("cron runtime not configured")
	}
	runs := reg.Runs(req.ID, req.Limit)
	return map[string]any{"runs": runs, "count": len(runs)}, nil
}

func applyExecApprovalsGet(reg *execApprovalsRegistry, _ methods.ExecApprovalsGetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	approvals := reg.GetGlobal()
	return map[string]any{"approvals": approvals, "count": len(approvals)}, nil
}

func applyExecApprovalsSet(reg *execApprovalsRegistry, req methods.ExecApprovalsSetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	approvals := reg.SetGlobal(req.Approvals)
	return map[string]any{"ok": true, "approvals": approvals, "count": len(approvals)}, nil
}

func applyExecApprovalsNodeGet(reg *execApprovalsRegistry, req methods.ExecApprovalsNodeGetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	approvals := reg.GetNode(req.NodeID)
	return map[string]any{"node_id": req.NodeID, "approvals": approvals, "count": len(approvals)}, nil
}

func applyExecApprovalsNodeSet(reg *execApprovalsRegistry, req methods.ExecApprovalsNodeSetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	approvals := reg.SetNode(req.NodeID, req.Approvals)
	return map[string]any{"ok": true, "node_id": req.NodeID, "approvals": approvals, "count": len(approvals)}, nil
}

func applyExecApprovalRequest(reg *execApprovalsRegistry, req methods.ExecApprovalRequestRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	rec := reg.Request(req)
	emitControlWSEvent(gatewayws.EventExecApprovalRequested, gatewayws.ExecApprovalRequestedPayload{
		TS:        time.Now().UnixMilli(),
		ID:        rec.ID,
		NodeID:    rec.NodeID,
		Command:   rec.Command,
		ExpiresAt: rec.ExpiresAt,
	})
	return map[string]any{"id": rec.ID, "status": "accepted", "requested": rec}, nil
}

func applyExecApprovalWaitDecision(ctx context.Context, reg *execApprovalsRegistry, req methods.ExecApprovalWaitDecisionRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	rec, resolved, err := reg.WaitForDecision(ctx, req.ID, req.TimeoutMS)
	if err != nil {
		return nil, err
	}
	if resolved {
		return map[string]any{"ok": true, "id": rec.ID, "resolved": true, "decision": rec.Decision, "record": rec}, nil
	}
	if rec.ExpiresAt > 0 && time.Now().UnixMilli() > rec.ExpiresAt {
		return map[string]any{"ok": false, "id": rec.ID, "resolved": false, "expired": true, "record": rec}, nil
	}
	if ctx.Err() != nil {
		return map[string]any{"ok": false, "id": rec.ID, "resolved": false, "cancelled": true, "record": rec}, nil
	}
	return map[string]any{"ok": true, "id": rec.ID, "resolved": false, "timed_out": true, "record": rec}, nil
}

func applyExecApprovalResolve(reg *execApprovalsRegistry, req methods.ExecApprovalResolveRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("exec approvals runtime not configured")
	}
	rec, err := reg.Resolve(req)
	if err != nil {
		return nil, err
	}
	emitControlWSEvent(gatewayws.EventExecApprovalResolved, gatewayws.ExecApprovalResolvedPayload{
		TS:       time.Now().UnixMilli(),
		ID:       rec.ID,
		Decision: rec.Decision,
		NodeID:   rec.NodeID,
	})
	return map[string]any{"ok": true, "id": rec.ID, "decision": rec.Decision, "resolved": rec}, nil
}

func applySandboxRun(ctx context.Context, configState *runtimeConfigStore, req methods.SandboxRunRequest) (map[string]any, error) {
	if len(req.Cmd) == 0 {
		return nil, fmt.Errorf("sandbox.run: cmd is required")
	}

	// Build sandbox config from daemon config + request overrides.
	cfg := sandbox.Config{}
	daemonCfg := configState.Get()
	if daemonCfg.Extra != nil {
		if rawSandbox, ok := daemonCfg.Extra["sandbox"].(map[string]any); ok {
			cfg.Driver = getString(rawSandbox, "driver")
			cfg.MemoryLimit = getString(rawSandbox, "memory_limit")
			cfg.CPULimit = getString(rawSandbox, "cpu_limit")
			cfg.DockerImage = getString(rawSandbox, "docker_image")
			if v, ok := rawSandbox["timeout_s"].(float64); ok {
				cfg.TimeoutSeconds = int(v)
			}
			if v, ok := rawSandbox["network_disabled"].(bool); ok {
				cfg.NetworkDisabled = v
			}
		}
	}
	// Request overrides.
	if req.Driver != "" {
		cfg.Driver = req.Driver
	}
	if req.TimeoutSeconds > 0 {
		cfg.TimeoutSeconds = req.TimeoutSeconds
	}

	runner, err := sandbox.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("sandbox.run: %w", err)
	}

	result, err := runner.Run(ctx, req.Cmd, req.Env, req.Workdir)
	if err != nil {
		return nil, fmt.Errorf("sandbox.run: %w", err)
	}
	return map[string]any{
		"ok":        true,
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
		"timed_out": result.TimedOut,
		"driver":    result.Driver,
	}, nil
}

func applySecretsReload(_ methods.SecretsReloadRequest) (map[string]any, error) {
	if controlSecrets == nil {
		return map[string]any{"ok": true, "count": 0, "warningCount": 0, "warnings": []string{}}, nil
	}
	count, warnings := controlSecrets.Reload()
	return map[string]any{
		"ok":           true,
		"count":        count,
		"warningCount": len(warnings),
		"warnings":     warnings,
	}, nil
}

func applySecretsResolve(req methods.SecretsResolveRequest) (map[string]any, error) {
	assignments := make([]map[string]any, 0, len(req.TargetIDs))
	var diagnostics []string
	var inactive []string

	for _, id := range req.TargetIDs {
		entry := map[string]any{
			"path":         id,
			"pathSegments": strings.Split(id, "."),
		}
		if controlSecrets == nil {
			entry["value"] = nil
			entry["found"] = false
			inactive = append(inactive, id)
		} else {
			v, found := controlSecrets.Resolve(id)
			if found {
				// Never log the actual secret value — only indicate presence.
				entry["found"] = true
				entry["value"] = v // caller sees value; we redact in logs
			} else {
				entry["found"] = false
				entry["value"] = nil
				diagnostics = append(diagnostics, "unresolved ref: "+id)
				inactive = append(inactive, id)
			}
		}
		assignments = append(assignments, entry)
	}

	return map[string]any{
		"ok":               true,
		"assignments":      assignments,
		"diagnostics":      diagnostics,
		"inactiveRefPaths": inactive,
	}, nil
}

func wizardStepToMap(s *wizardStep) map[string]any {
	if s == nil {
		return nil
	}
	m := map[string]any{
		"id":       s.ID,
		"type":     s.Type,
		"prompt":   s.Prompt,
		"required": s.Required,
		"secret":   s.Secret,
	}
	if len(s.Options) > 0 {
		m["options"] = s.Options
	}
	if s.Default != "" {
		m["default"] = s.Default
	}
	return m
}

func applyWizardStart(reg *wizardRegistry, req methods.WizardStartRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wizard runtime not configured")
	}
	rec, step := reg.Start(req)
	out := map[string]any{
		"session_id": rec.SessionID,
		"sessionId":  rec.SessionID,
		"status":     rec.Status,
		"mode":       rec.Mode,
		"done":       false,
	}
	if step != nil {
		out["step"] = wizardStepToMap(step)
	}
	return out, nil
}

func applyWizardNext(reg *wizardRegistry, req methods.WizardNextRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wizard runtime not configured")
	}
	rec, step, done, err := reg.Next(req)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"session_id": rec.SessionID,
		"sessionId":  rec.SessionID,
		"status":     rec.Status,
		"done":       done,
	}
	if step != nil {
		out["step"] = wizardStepToMap(step)
	}
	if done && len(rec.Input) > 0 {
		out["result"] = rec.Input
	}
	return out, nil
}

func applyWizardCancel(reg *wizardRegistry, req methods.WizardCancelRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wizard runtime not configured")
	}
	rec, err := reg.Cancel(req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": rec.Status, "error": rec.Error}, nil
}

func applyWizardStatus(reg *wizardRegistry, req methods.WizardStatusRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wizard runtime not configured")
	}
	rec, err := reg.Status(req)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"session_id": rec.SessionID,
		"sessionId":  rec.SessionID,
		"status":     rec.Status,
		"mode":       rec.Mode,
		"step":       rec.Step,
		"error":      rec.Error,
	}
	step := currentWizardStep(rec)
	if step != nil {
		out["currentStep"] = wizardStepToMap(step)
	}
	return out, nil
}

func applyTalkConfig(cfg state.ConfigDoc, reg *operationsRegistry, req methods.TalkConfigRequest) (map[string]any, error) {
	// Build the talk section by merging persisted config with live registry state.
	talk := map[string]any{
		"enabled":      false,
		"mode":         "disabled",
		"hotword":      []string{"openclaw", "metiq"},
		"sensitivity":  0.5,
		"tts_provider": "openai",
		"stt_provider": "openai-whisper",
		"voice_model":  "alloy",
	}

	// Overlay persisted talk config from cfg.Extra["talk"].
	if cfg.Extra != nil {
		if raw, ok := cfg.Extra["talk"].(map[string]any); ok {
			for k, v := range raw {
				talk[k] = v
			}
		}
	}

	// Overlay live state from ops registry.
	if reg != nil {
		ttsEnabled, ttsProvider := reg.TTSStatus()
		talkMode := reg.TalkMode()
		voicewake := reg.Voicewake()
		talk["enabled"] = ttsEnabled
		talk["mode"] = talkMode
		if ttsProvider != "" {
			talk["tts_provider"] = ttsProvider
		}
		if len(voicewake) > 0 {
			talk["hotword"] = voicewake
		}
	}

	// Optionally redact API keys unless includeSecrets is set.
	if !req.IncludeSecrets {
		delete(talk, "api_key")
		delete(talk, "apiKey")
	}

	configPayload := map[string]any{"talk": talk}

	// Include additional config sections.
	if cfg.Extra != nil {
		if session, ok := cfg.Extra["session"]; ok {
			configPayload["session"] = session
		}
		if ui, ok := cfg.Extra["ui"]; ok {
			configPayload["ui"] = ui
		}
	}
	return map[string]any{"config": configPayload}, nil
}

func applyUpdateRun(reg *operationsRegistry, req methods.UpdateRunRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("update runtime not configured")
	}
	checkedAt := reg.RecordUpdateCheck()

	// Use the shared version checker (initialised in main).
	if controlUpdateChecker == nil {
		return map[string]any{"ok": true, "status": "checker_unavailable", "checked_at_ms": checkedAt}, nil
	}

	result := controlUpdateChecker.Check(context.Background(), req.Force)

	out := map[string]any{
		"ok":               true,
		"current_version":  result.Current,
		"latest_version":   result.Latest,
		"update_available": result.Available,
		"checked_at_ms":    result.CheckedAt,
	}
	if result.Error != "" {
		out["error"] = result.Error
		out["status"] = "error"
	} else if result.Available {
		out["status"] = "update_available"
		emitControlWSEvent(gatewayws.EventUpdateAvailable, gatewayws.UpdateAvailablePayload{
			TS:      result.CheckedAt,
			Version: result.Latest,
			Source:  "update.run",
		})
	} else {
		out["status"] = "up_to_date"
	}
	return out, nil
}

// validTalkModes lists the modes accepted by talk.mode.
var validTalkModes = map[string]bool{
	"disabled":     true,
	"off":          true,
	"push-to-talk": true,
	"always-on":    true,
	"hotword":      true,
}

func applyTalkMode(reg *operationsRegistry, req methods.TalkModeRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("talk runtime not configured")
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if !validTalkModes[mode] {
		return nil, fmt.Errorf("invalid talk mode %q; valid modes: disabled, off, push-to-talk, always-on, hotword", req.Mode)
	}
	mode = reg.SetTalkMode(mode)
	ts := time.Now().UnixMilli()
	emitControlWSEvent(gatewayws.EventTalkMode, gatewayws.TalkModePayload{TS: ts, Mode: mode})
	return map[string]any{"mode": mode, "ts": ts}, nil
}

func applyLastHeartbeat(reg *operationsRegistry, _ methods.LastHeartbeatRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("heartbeat runtime not configured")
	}
	status := reg.HeartbeatStatus()
	return map[string]any{
		"last_heartbeat_ms": status.LastRunMS,
		"last_run_ms":       status.LastRunMS,
		"last_wake_ms":      status.LastWakeMS,
		"enabled":           status.Enabled,
		"interval_ms":       status.IntervalMS,
		"pending_wakes":     status.PendingWakes,
	}, nil
}

func applySetHeartbeats(reg *operationsRegistry, req methods.SetHeartbeatsRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("heartbeat runtime not configured")
	}
	status := reg.SetHeartbeats(req.Enabled, req.IntervalMS)
	return map[string]any{
		"ok":                true,
		"enabled":           status.Enabled,
		"interval_ms":       status.IntervalMS,
		"last_heartbeat_ms": status.LastRunMS,
		"last_run_ms":       status.LastRunMS,
		"last_wake_ms":      status.LastWakeMS,
		"pending_wakes":     status.PendingWakes,
	}, nil
}

func applyWake(reg *operationsRegistry, req methods.WakeRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("wake runtime not configured")
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		agentID = "main"
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "control"
	}
	status := reg.QueueHeartbeatWake(agentID, source, req.Text, req.Mode)
	at := status.LastWakeMS
	// Emit voice.wake when the source is voice-related.
	if source == "voice" || source == "voicewake" || source == "hotword" {
		emitControlWSEvent(gatewayws.EventVoicewake, gatewayws.VoicewakePayload{
			TS:     at,
			Source: source,
		})
	}
	return map[string]any{
		"ok":                true,
		"woken":             true,
		"agent_id":          agentID,
		"source":            source,
		"mode":              req.Mode,
		"text":              req.Text,
		"wake_at_ms":        at,
		"last_heartbeat_ms": status.LastRunMS,
		"last_run_ms":       status.LastRunMS,
		"last_wake_ms":      status.LastWakeMS,
		"pending_wakes":     status.PendingWakes,
	}, nil
}

func applySystemPresence(reg *operationsRegistry, _ methods.SystemPresenceRequest) ([]map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("system runtime not configured")
	}
	return reg.ListSystemPresence(), nil
}

func applySystemEvent(reg *operationsRegistry, req methods.SystemEventRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("system runtime not configured")
	}
	_ = reg.RecordSystemEvent(req)
	return map[string]any{"ok": true}, nil
}

func applySend(ctx context.Context, dmBus nostruntime.DMTransport, req methods.SendRequest) (map[string]any, error) {
	if dmBus == nil {
		return nil, fmt.Errorf("send runtime not configured")
	}
	if err := dmBus.SendDM(ctx, req.To, req.Message); err != nil {
		return nil, err
	}
	messageID := fmt.Sprintf("msg-%d", time.Now().UnixNano())
	return map[string]any{"runId": req.IdempotencyKey, "messageId": messageID, "channel": "nostr"}, nil
}

// browserBridgePaths are path prefixes that must go through the browser
// bridge proxy (Playwright sandbox).  All other paths are treated as direct
// HTTP fetch targets.
var browserBridgePaths = []string{
	"/act", "/snapshot", "/screenshot", "/evaluate",
	"/tabs", "/storage", "/fetch",
}

func applyBrowserRequest(req methods.BrowserRequestRequest) (map[string]any, error) {
	// browser.request routes through a local browser proxy (e.g. a Playwright
	// bridge server).  The proxy base URL is configured via METIQ_BROWSER_URL.
	// When the env var is absent, browser control is disabled.
	proxyBase := strings.TrimRight(os.Getenv("METIQ_BROWSER_URL"), "/")
	if proxyBase == "" {
		return nil, fmt.Errorf("browser control is disabled")
	}

	path := req.Path
	if path == "" {
		path = "/"
	}

	// Route browser automation paths to the bridge proxy.
	isBridgePath := false
	for _, prefix := range browserBridgePaths {
		if path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix+"?") {
			isBridgePath = true
			break
		}
	}

	// Check if path looks like an absolute URL (direct fetch).
	isAbsoluteURL := strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")

	// Build the full URL.
	var fullURL string
	if isAbsoluteURL {
		// Direct HTTP fetch — do not proxy through bridge.
		fullURL = path
	} else {
		fullURL = proxyBase + path
	}

	_ = isBridgePath // available for future routing decisions

	headers := map[string]string{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}
	if isBridgePath {
		headers["Accept"] = "application/json"
		headers["Content-Type"] = "application/json"
	}

	// Pass auth token if configured.
	if token := os.Getenv("METIQ_BROWSER_TOKEN"); token != "" {
		headers["Authorization"] = "Bearer " + token
	}

	var bodyVal any
	if req.Body != nil {
		bodyVal = req.Body
	}

	fetchResp, err := browserpkg.Fetch(context.Background(), browserpkg.Request{
		Method:    req.Method,
		URL:       fullURL,
		Query:     req.Query,
		Headers:   headers,
		Body:      bodyVal,
		TimeoutMS: req.TimeoutMS,
	})
	if err != nil {
		return nil, fmt.Errorf("browser.request: %w", err)
	}

	out := map[string]any{
		"ok":           true,
		"status_code":  fetchResp.StatusCode,
		"content_type": fetchResp.ContentType,
		"url":          fetchResp.URL,
	}
	if fetchResp.Text != "" {
		out["text"] = fetchResp.Text
	}
	if fetchResp.Body != "" {
		out["body"] = fetchResp.Body
	}
	return out, nil
}

func applyVoicewakeGet(reg *operationsRegistry, _ methods.VoicewakeGetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("voicewake runtime not configured")
	}
	return map[string]any{"triggers": reg.Voicewake()}, nil
}

func applyVoicewakeSet(reg *operationsRegistry, req methods.VoicewakeSetRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("voicewake runtime not configured")
	}
	return map[string]any{"triggers": reg.SetVoicewake(req.Triggers)}, nil
}

func applyTTSStatus(reg *operationsRegistry, _ methods.TTSStatusRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	enabled, provider := reg.TTSStatus()
	return map[string]any{"enabled": enabled, "provider": provider}, nil
}

func applyTTSProviders(reg *operationsRegistry, _ methods.TTSProvidersRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	_, active := reg.TTSStatus()
	var providers []map[string]any
	if controlTTSMgr != nil {
		providers = controlTTSMgr.Providers()
	} else {
		providers = []map[string]any{
			{"id": "openai", "name": "OpenAI TTS", "configured": false, "voices": []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}},
			{"id": "kokoro", "name": "Kokoro TTS (local)", "configured": false, "voices": []string{}},
		}
	}
	return map[string]any{"providers": providers, "active": active}, nil
}

func applyTTSSetProvider(reg *operationsRegistry, req methods.TTSSetProviderRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	provider := reg.SetTTSProvider(req.Provider)
	return map[string]any{"ok": true, "provider": provider}, nil
}

func applyTTSEnable(reg *operationsRegistry, _ methods.TTSEnableRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	return map[string]any{"enabled": reg.SetTTSEnabled(true)}, nil
}

func applyTTSDisable(reg *operationsRegistry, _ methods.TTSDisableRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	return map[string]any{"enabled": reg.SetTTSEnabled(false)}, nil
}

// countEligible counts how many hook status maps have eligible=true.
func countEligible(statuses []map[string]any) int {
	n := 0
	for _, s := range statuses {
		if v, ok := s["eligible"]; ok {
			if b, ok := v.(bool); ok && b {
				n++
			}
		}
	}
	return n
}

func applyTTSConvert(ctx context.Context, reg *operationsRegistry, req methods.TTSConvertRequest) (map[string]any, error) {
	if reg == nil {
		return nil, fmt.Errorf("tts runtime not configured")
	}
	enabled, activeProvider := reg.TTSStatus()
	providerID := activeProvider
	if req.Provider != "" {
		providerID = req.Provider
	}

	// If TTS is disabled, the manager is unavailable, or the provider is not
	// configured, return a metadata-only response (no audio) so callers can
	// always query the method without an error.
	doConvert := enabled && controlTTSMgr != nil
	if doConvert {
		if p := controlTTSMgr.Get(providerID); p == nil || !p.Configured() {
			doConvert = false
		}
	}

	if doConvert {
		result, err := controlTTSMgr.Convert(ctx, providerID, req.Text, req.Voice)
		if err != nil {
			return nil, fmt.Errorf("tts.convert: %w", err)
		}
		return map[string]any{
			"ok":           true,
			"audioPath":    result.AudioPath,
			"audioBase64":  result.AudioBase64,
			"provider":     result.Provider,
			"voice":        result.Voice,
			"outputFormat": result.Format,
			"text":         req.Text,
		}, nil
	}

	// Synthesis is not available — return an error with diagnostics so the
	// caller knows *why* conversion was skipped rather than silently getting
	// empty audio fields.
	reason := "tts disabled"
	if !enabled {
		reason = "tts is disabled (call tts.enable first)"
	} else if controlTTSMgr == nil {
		reason = "tts manager not initialised"
	} else if p := controlTTSMgr.Get(providerID); p == nil {
		reason = fmt.Sprintf("unknown tts provider %q", providerID)
	} else if !p.Configured() {
		reason = fmt.Sprintf("tts provider %q is not configured (check environment variables)", providerID)
	}
	return nil, fmt.Errorf("tts.convert: %s", reason)
}

func persistMemories(
	ctx context.Context,
	docsRepo *state.DocsRepository,
	repo *state.MemoryRepository,
	index memory.Store,
	tracker *memoryIndexTracker,
	docs []state.MemoryDoc,
) {
	for _, doc := range docs {
		if _, err := repo.Put(ctx, doc); err != nil {
			log.Printf("persist memory failed memory_id=%s err=%v", doc.MemoryID, err)
			continue
		}
		memory.AddDoc(ctx, index, doc)
		if err := index.Save(); err != nil {
			log.Printf("memory index save failed memory_id=%s err=%v", doc.MemoryID, err)
		}
		if err := tracker.MarkIndexed(ctx, docsRepo, doc.MemoryID, doc.Unix); err != nil {
			log.Printf("memory index checkpoint failed memory_id=%s err=%v", doc.MemoryID, err)
		}
	}
}

func (t *memoryIndexTracker) MarkIndexed(ctx context.Context, repo *state.DocsRepository, memoryID string, unix int64) error {
	if memoryID == "" {
		return nil
	}
	if unix <= 0 {
		unix = time.Now().Unix()
	}
	t.mu.Lock()
	if unix < t.lastUnix || (unix == t.lastUnix && memoryID <= t.lastEvent) {
		t.mu.Unlock()
		return nil
	}
	t.lastEvent = memoryID
	t.lastUnix = unix
	checkpoint := state.CheckpointDoc{Version: 1, Name: "memory_index", LastEvent: t.lastEvent, LastUnix: t.lastUnix}
	t.mu.Unlock()

	_, err := repo.PutCheckpoint(ctx, "memory_index", checkpoint)
	return err
}

type sessionRotationOutcome struct {
	ArchivePath string
	Forked      bool
}

func fireHookEvent(mgr *hookspkg.Manager, eventName, sessionID string, ctx map[string]any) {
	if mgr == nil {
		return
	}
	errs := mgr.Fire(eventName, sessionID, ctx)
	for _, err := range errs {
		log.Printf("hook event error event=%s session=%s err=%v", eventName, sessionID, err)
	}
}

func fireSessionResetHooks(mgr *hookspkg.Manager, sessionID, reason string, isACP bool, entries []state.TranscriptEntryDoc) {
	if mgr == nil {
		return
	}
	beforeCtx := buildBeforeResetHookContext(sessionID, reason, isACP, entries)
	fireHookEvent(mgr, "session:before_reset", sessionID, beforeCtx)
	endCtx := map[string]any{
		"reason":                 "reset",
		"trigger":                reason,
		"acp":                    isACP,
		"previous_message_count": len(beforeCtx["previous_messages"].([]map[string]any)),
	}
	fireHookEvent(mgr, "session:end", sessionID, endCtx)
}

func buildBeforeResetHookContext(sessionID, reason string, isACP bool, entries []state.TranscriptEntryDoc) map[string]any {
	const maxMessages = 24
	prev := make([]map[string]any, 0, min(maxMessages, len(entries)))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Role) == "" || entry.Role == "deleted" {
			continue
		}
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		prev = append(prev, map[string]any{
			"entry_id": entry.EntryID,
			"role":     entry.Role,
			"text":     truncateRunes(text, 320),
			"unix":     entry.Unix,
		})
		if len(prev) >= maxMessages {
			break
		}
	}
	ctx := map[string]any{
		"reason":                 "reset",
		"trigger":                reason,
		"acp":                    isACP,
		"session_id":             sessionID,
		"previous_messages":      prev,
		"previous_message_count": len(prev),
	}
	if len(prev) > 0 {
		var sb strings.Builder
		for _, m := range prev {
			sb.WriteString("- ")
			sb.WriteString(fmt.Sprintf("%s: %s", m["role"], m["text"]))
			sb.WriteByte('\n')
		}
		ctx["previous_transcript"] = strings.TrimSpace(sb.String())
	}
	return ctx
}

func rotateSessionCoordinated(
	ctx context.Context,
	sessionID string,
	reason string,
	isACP bool,
	chatCancels *chatAbortRegistry,
	sessionRouter *agent.AgentSessionRouter,
	seenChannelSessions *sync.Map,
	hooksMgr *hookspkg.Manager,
	transcriptRepo *state.TranscriptRepository,
	sessionStore *state.SessionStore,
	cfg state.ConfigDoc,
) error {
	var priorEntries []state.TranscriptEntryDoc
	if transcriptRepo != nil {
		if entries, listErr := transcriptRepo.ListSessionTail(ctx, sessionID, 24); listErr != nil {
			log.Printf("session hook context list warning session=%s reason=%s err=%v", sessionID, reason, listErr)
		} else {
			priorEntries = entries
		}
	}
	fireSessionResetHooks(hooksMgr, sessionID, reason, isACP, priorEntries)
	if chatCancels != nil {
		chatCancels.Abort(sessionID)
	}
	return withExclusiveSessionTurn(ctx, sessionID, 15*time.Second, func() error {
		if seenChannelSessions != nil {
			seenChannelSessions.Delete(sessionID)
		}
		if !isACP && sessionRouter != nil {
			sessionRouter.Assign(sessionID, "")
		}
		_, err := rotateSessionLifecycle(ctx, sessionID, reason, cfg, transcriptRepo, sessionStore, time.Now())
		return err
	})
}

type sessionMemoryLifecycleOutcome struct {
	Path    string
	Updated bool
}

func ensureSessionMemoryCurrent(ctx context.Context, cfg state.ConfigDoc, sessionID string, sessionStore *state.SessionStore) (sessionMemoryLifecycleOutcome, error) {
	outcome := sessionMemoryLifecycleOutcome{}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || controlSessionMemoryRuntime == nil {
		return outcome, nil
	}
	generator, workspaceDir := resolveSessionMemoryRuntimeDependencies(cfg, sessionID, sessionStore)
	if generator == nil || strings.TrimSpace(workspaceDir) == "" {
		return outcome, nil
	}
	path, updated, err := controlSessionMemoryRuntime.EnsureCurrent(ctx, cfg, generator, sessionID, workspaceDir)
	if err != nil {
		return outcome, err
	}
	outcome.Path = path
	outcome.Updated = updated
	return outcome, nil
}

func resolveSessionMemoryRuntimeDependencies(cfg state.ConfigDoc, sessionID string, sessionStore *state.SessionStore) (sessionMemoryGenerator, string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ""
	}
	agentID := "main"
	workspaceDir := ""
	if sessionStore != nil {
		if entry, ok := sessionStore.Get(sessionID); ok {
			if strings.TrimSpace(entry.AgentID) != "" {
				agentID = defaultAgentID(entry.AgentID)
			}
			workspaceDir = strings.TrimSpace(entry.SpawnedWorkspace)
		}
	}
	if workspaceDir == "" {
		workspaceDir = workspaceDirForAgent(cfg, agentID)
	}
	rt := controlAgentRuntime
	if controlAgentRegistry != nil {
		if candidate := controlAgentRegistry.Get(agentID); candidate != nil {
			rt = candidate
		}
	}
	if rt == nil || strings.TrimSpace(workspaceDir) == "" {
		return nil, ""
	}
	return runtimeSessionMemoryGenerator{runtime: rt}, workspaceDir
}

func recordSessionCompaction(sessionStore *state.SessionStore, sessionID string, sessionMemoryReady bool, now time.Time) {
	if sessionStore == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	entry := sessionStore.GetOrNew(sessionID)
	entry.CompactionCount++
	if sessionMemoryReady {
		entry.MemoryFlushAt = now.Unix()
		entry.MemoryFlushCount = entry.CompactionCount
	}
	if err := sessionStore.Put(sessionID, entry); err != nil {
		log.Printf("session compaction metadata save failed session=%s err=%v", sessionID, err)
	}
}

func carrySessionMemoryAcrossRotation(entry *state.SessionEntry, prior state.SessionEntry, checkpointEntryID string) {
	if entry == nil {
		return
	}
	if strings.TrimSpace(prior.SessionMemoryFile) == "" && !prior.SessionMemoryInitialized && prior.SessionMemoryUpdatedAt == 0 {
		return
	}
	entry.SessionMemoryFile = prior.SessionMemoryFile
	entry.SessionMemoryInitialized = prior.SessionMemoryInitialized || strings.TrimSpace(prior.SessionMemoryFile) != ""
	entry.SessionMemoryObservedChars = 0
	entry.SessionMemoryPendingChars = 0
	entry.SessionMemoryPendingToolCalls = 0
	entry.SessionMemoryLastEntryID = strings.TrimSpace(checkpointEntryID)
	entry.SessionMemoryUpdatedAt = prior.SessionMemoryUpdatedAt
}

func deleteSessionCoordinated(
	ctx context.Context,
	sessionID string,
	chatCancels *chatAbortRegistry,
	sessionRouter *agent.AgentSessionRouter,
	seenChannelSessions *sync.Map,
	docsRepo *state.DocsRepository,
	transcriptRepo *state.TranscriptRepository,
	sessionStore *state.SessionStore,
) error {
	if chatCancels != nil {
		chatCancels.Abort(sessionID)
	}
	return withExclusiveSessionTurn(ctx, sessionID, 15*time.Second, func() error {
		if sessionRouter != nil {
			sessionRouter.Assign(sessionID, "")
		}
		if seenChannelSessions != nil {
			seenChannelSessions.Delete(sessionID)
		}
		if transcriptRepo != nil {
			if entries, lErr := transcriptRepo.ListSessionAll(ctx, sessionID); lErr == nil {
				for _, e := range entries {
					if delErr := transcriptRepo.DeleteEntry(ctx, sessionID, e.EntryID); delErr != nil {
						log.Printf("transcript delete failed session=%s entry=%s: %v", sessionID, e.EntryID, delErr)
					}
				}
			}
		}
		if docsRepo != nil {
			if _, err := updateExistingSessionDoc(ctx, docsRepo, sessionID, "", func(session *state.SessionDoc) error {
				session.Meta = mergeSessionMeta(session.Meta, map[string]any{"deleted": true, "deleted_at": time.Now().Unix()})
				return nil
			}); err != nil && !errors.Is(err, state.ErrNotFound) {
				return err
			}
		}
		if sessionStore != nil {
			return sessionStore.Delete(sessionID)
		}
		return nil
	})
}

func rotateSessionLifecycle(
	ctx context.Context,
	sessionID string,
	reason string,
	cfg state.ConfigDoc,
	transcriptRepo *state.TranscriptRepository,
	sessionStore *state.SessionStore,
	now time.Time,
) (sessionRotationOutcome, error) {
	outcome := sessionRotationOutcome{}
	if strings.TrimSpace(sessionID) == "" {
		return outcome, fmt.Errorf("session id is required")
	}
	if transcriptRepo == nil {
		return outcome, fmt.Errorf("transcript repository is required")
	}
	if _, err := ensureSessionMemoryCurrent(ctx, cfg, sessionID, sessionStore); err != nil {
		return outcome, fmt.Errorf("flush session memory: %w", err)
	}
	entries, err := transcriptRepo.ListSessionAll(ctx, sessionID)
	if err != nil {
		return outcome, fmt.Errorf("list transcript: %w", err)
	}
	if len(entries) > 0 {
		archivePath, archiveErr := archiveTranscriptSnapshot(sessionID, reason, entries, now, defaultSessionArchiveDir())
		if archiveErr != nil {
			return outcome, archiveErr
		}
		outcome.ArchivePath = archivePath
	}
	for _, e := range entries {
		if delErr := transcriptRepo.DeleteEntry(ctx, sessionID, e.EntryID); delErr != nil {
			return outcome, fmt.Errorf("delete transcript entry %s: %w", e.EntryID, delErr)
		}
	}

	forkPolicy := resolveSessionForkPolicy(cfg)
	forkCheckpointEntryID := ""
	if forkPolicy.Enabled && len(entries) > 0 {
		if seed := buildForkSeedEntry(sessionID, reason, entries, now, forkPolicy.MaxEntries); seed != nil {
			if _, putErr := transcriptRepo.PutEntry(ctx, *seed); putErr != nil {
				return outcome, fmt.Errorf("write fork seed entry: %w", putErr)
			}
			forkCheckpointEntryID = seed.EntryID
			outcome.Forked = true
		}
	}

	if sessionStore != nil {
		priorEntry := sessionStore.GetOrNew(sessionID)
		entry := priorEntry.CarryOverFlags(sessionID)
		entry.SpawnedBy = reason
		entry.SessionFile = sessionTranscriptPath(sessionID)
		entry.ForkedFromParent = outcome.Forked
		carrySessionMemoryAcrossRotation(&entry, priorEntry, forkCheckpointEntryID)
		if putErr := sessionStore.Put(sessionID, entry); putErr != nil {
			return outcome, fmt.Errorf("persist session entry: %w", putErr)
		}
	}
	return outcome, nil
}

type sessionForkPolicy struct {
	Enabled    bool
	MaxEntries int
}

func resolveSessionForkPolicy(cfg state.ConfigDoc) sessionForkPolicy {
	policy := sessionForkPolicy{Enabled: false, MaxEntries: 8}
	if cfg.Extra == nil {
		return policy
	}
	raw, ok := cfg.Extra["session_reset"].(map[string]any)
	if !ok {
		return policy
	}
	if v, ok := raw["fork_parent"].(bool); ok {
		policy.Enabled = v
	}
	if v, ok := raw["fork_max_entries"].(float64); ok && int(v) > 0 {
		policy.MaxEntries = int(v)
	}
	return policy
}

func sessionTranscriptPath(sessionID string) string {
	safe := strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(strings.TrimSpace(sessionID))
	return filepath.Join(defaultSessionArtifactsRoot(), "active", safe+".jsonl")
}

func defaultSessionArtifactsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "."
	}
	return filepath.Join(home, ".metiq", "sessions")
}

func defaultSessionArchiveDir() string {
	if v := strings.TrimSpace(os.Getenv("METIQ_SESSION_ARCHIVE_DIR")); v != "" {
		return v
	}
	return filepath.Join(defaultSessionArtifactsRoot(), "archive")
}

func archiveTranscriptSnapshot(sessionID, reason string, entries []state.TranscriptEntryDoc, now time.Time, archiveDir string) (string, error) {
	if len(entries) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		return "", fmt.Errorf("create archive dir: %w", err)
	}
	safeSession := strings.NewReplacer("/", "_", ":", "_", "\\", "_").Replace(strings.TrimSpace(sessionID))
	if safeSession == "" {
		safeSession = "session"
	}
	filename := fmt.Sprintf("%s-%s.jsonl", safeSession, now.UTC().Format("20060102T150405Z"))
	path := filepath.Join(archiveDir, filename)

	var b strings.Builder
	for _, entry := range entries {
		row := map[string]any{
			"session_id": entry.SessionID,
			"entry_id":   entry.EntryID,
			"role":       entry.Role,
			"text":       entry.Text,
			"unix":       entry.Unix,
			"meta":       entry.Meta,
			"reason":     reason,
		}
		raw, err := json.Marshal(row)
		if err != nil {
			return "", fmt.Errorf("encode archive row: %w", err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", fmt.Errorf("write archive: %w", err)
	}
	return path, nil
}

func buildForkSeedEntry(sessionID, reason string, entries []state.TranscriptEntryDoc, now time.Time, maxEntries int) *state.TranscriptEntryDoc {
	if len(entries) == 0 {
		return nil
	}
	if maxEntries <= 0 {
		maxEntries = 8
	}
	start := len(entries) - maxEntries
	if start < 0 {
		start = 0
	}
	selected := entries[start:]
	lines := make([]string, 0, len(selected))
	for _, entry := range selected {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", entry.Role, truncateRunes(text, 240)))
	}
	if len(lines) == 0 {
		return nil
	}
	text := "Parent context carried over from previous transcript reset.\n" + strings.Join(lines, "\n")
	return &state.TranscriptEntryDoc{
		Version:   1,
		SessionID: sessionID,
		EntryID:   fmt.Sprintf("fork-%d", now.UnixNano()),
		Role:      "system",
		Text:      text,
		Unix:      now.Unix(),
		Meta: map[string]any{
			"kind":   "session_fork",
			"reason": reason,
			"count":  len(lines),
		},
	}
}

func generateSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return "sess-" + hex.EncodeToString(b)
}

type sessionFreshnessPolicy struct {
	IdleMinutes int
	DailyReset  bool
}

type queueRuntimeSettings struct {
	Mode string
	Cap  int
	Drop autoreply.QueueDropPolicy
}

func resolveSessionFreshnessPolicy(cfg state.ConfigDoc, sessionType, channelID string) sessionFreshnessPolicy {
	policy := sessionFreshnessPolicy{}
	if cfg.Session.TTLSeconds > 0 {
		policy.IdleMinutes = cfg.Session.TTLSeconds / 60
	}
	apply := func(raw map[string]any) {
		if raw == nil {
			return
		}
		if v, ok := raw["idle_minutes"].(float64); ok && v >= 0 {
			policy.IdleMinutes = int(v)
		}
		if v, ok := raw["daily_reset"].(bool); ok {
			policy.DailyReset = v
		}
	}

	if extra, ok := cfg.Extra["session_reset"].(map[string]any); ok {
		if m, ok := extra["default"].(map[string]any); ok {
			apply(m)
		}
		if m, ok := extra[strings.ToLower(strings.TrimSpace(sessionType))].(map[string]any); ok {
			apply(m)
		}
		if channelID != "" {
			if chans, ok := extra["channels"].(map[string]any); ok {
				if m, ok := chans[channelID].(map[string]any); ok {
					apply(m)
				}
			}
		}
	}

	if policy.IdleMinutes < 0 {
		policy.IdleMinutes = 0
	}
	return policy
}

func shouldAutoRotateSession(entry state.SessionEntry, now time.Time, policy sessionFreshnessPolicy) bool {
	if entry.UpdatedAt.IsZero() {
		return false
	}
	if policy.IdleMinutes > 0 {
		if now.Sub(entry.UpdatedAt) > time.Duration(policy.IdleMinutes)*time.Minute {
			return true
		}
	}
	if policy.DailyReset {
		y1, m1, d1 := entry.UpdatedAt.In(time.Local).Date()
		y2, m2, d2 := now.In(time.Local).Date()
		if y1 != y2 || m1 != m2 || d1 != d2 {
			return true
		}
	}
	return false
}

func stripStructuralPrefixes(text string) string {
	trimmed := strings.TrimSpace(text)
	for {
		if strings.HasPrefix(trimmed, "[") {
			if idx := strings.Index(trimmed, "]"); idx > 0 && idx <= 48 {
				trimmed = strings.TrimSpace(trimmed[idx+1:])
				continue
			}
		}
		break
	}
	return trimmed
}

// parseResetTrigger checks whether text starts with a session-reset trigger
// (/new or /reset, case-insensitive, optional leading whitespace).
// It returns the matched trigger word and any text that follows it.
// Both return values are empty strings when no trigger is found.
func parseResetTrigger(text string) (trigger, remainder string) {
	trimmed := stripStructuralPrefixes(text)
	lower := strings.ToLower(trimmed)
	for _, kw := range []string{"/new", "/reset"} {
		if lower == kw {
			return kw, ""
		}
		if strings.HasPrefix(lower, kw+" ") || strings.HasPrefix(lower, kw+"\n") {
			rest := strings.TrimSpace(trimmed[len(kw):])
			return kw, rest
		}
	}
	return "", ""
}

func extractMediaOutputPath(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, toolbuiltin.MediaPrefix) {
		return "", false
	}
	payload := strings.TrimPrefix(trimmed, toolbuiltin.MediaPrefix)
	if i := strings.IndexByte(payload, '\n'); i >= 0 {
		payload = payload[:i]
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return "", false
	}
	return payload, true
}
