package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"

	acppkg "metiq/internal/acp"
	"metiq/internal/agent"
	"metiq/internal/autoreply"
	"metiq/internal/canvas"
	ctxengine "metiq/internal/context"
	attachpkg "metiq/internal/gateway/attach"
	boardpkg "metiq/internal/gateway/board"
	"metiq/internal/gateway/channels"
	conversationspkg "metiq/internal/gateway/conversations"
	environmentspkg "metiq/internal/gateway/environments"
	mcpapppkg "metiq/internal/gateway/mcpapp"
	"metiq/internal/gateway/methods"
	"metiq/internal/gateway/nodepending"
	pluginapprovalpkg "metiq/internal/gateway/pluginapproval"
	questionspkg "metiq/internal/gateway/questions"
	"metiq/internal/gateway/sessioncoord"
	suspendpkg "metiq/internal/gateway/suspend"
	talkpkg "metiq/internal/gateway/talk"
	tasksuggestionspkg "metiq/internal/gateway/tasksuggestions"
	terminalpkg "metiq/internal/gateway/terminal"
	userprofilespkg "metiq/internal/gateway/userprofiles"
	worktreespkg "metiq/internal/gateway/worktrees"
	hookspkg "metiq/internal/hooks"
	mediapkg "metiq/internal/media"
	"metiq/internal/memory"
	metricspkg "metiq/internal/metrics"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/permissions"
	pluginmanager "metiq/internal/plugins/manager"
	pluginregistry "metiq/internal/plugins/registry"
	pluginsurface "metiq/internal/plugins/surface"
	"metiq/internal/policy"
	"metiq/internal/store/state"
	taskspkg "metiq/internal/tasks"
)

type controlRPCDeps struct {
	dmBus             nostruntime.DMTransport
	controlBus        *nostruntime.ControlRPCBus
	chatCancels       *chatAbortRegistry
	steeringMailboxes *autoreply.SteeringMailboxRegistry
	usageState        *usageTracker
	logBuffer         *runtimeLogBuffer
	channelState      *channelRuntimeState
	docsRepo          *state.DocsRepository
	taskService       *taskspkg.Service
	transcriptRepo    *state.TranscriptRepository
	memoryIndex       memory.Store
	configState       *runtimeConfigStore
	tools             *agent.ToolRegistry
	pluginMgr         *pluginmanager.GojaPluginManager
	pluginSurface     *pluginsurface.Registry
	// surfaceDispatch invokes resolved plugin surface verbs in the owning
	// plugin's sandboxed runtime (production: pluginMgr; tests inject a fake).
	surfaceDispatch pluginSurfaceDispatcher
	startedAt       time.Time
	bootstrapPath   string
	onboarding      *onboardingService

	sessionStore       *state.SessionStore
	sessionCoordinator *sessioncoord.Service
	hooksMgr           hooksEventFirer
	hooksMgrFull       *hookspkg.Manager
	pluginRegistry     *pluginregistry.UnifiedRegistry
	mediaTranscriber   mediapkg.Transcriber
	toolRegistry       *agent.ToolRegistry
	agentJobs          *agentJobRegistry
	sessionRouter      *agent.AgentSessionRouter
	agentRegistry      *agent.AgentRuntimeRegistry
	agentRuntime       agent.Runtime

	// Fields below replace direct global access inside Handle().
	sessionMemoryRuntime *sessionMemoryRuntime
	acpPeers             *acppkg.PeerRegistry
	acpDispatcher        *acppkg.Dispatcher
	acpManager           *acppkg.Manager
	acpFlowRegistry      *acppkg.FlowRegistry

	// services provides access to the consolidated daemonServices struct.
	// Extracted handler files and RPC sub-handlers can use this instead of
	// reading package-level globals.
	services *daemonServices

	// Operation registries — replace direct global reads in RPC sub-handlers.
	ops             *operationsRegistry
	cronJobs        *cronRegistry
	execApprovals   *execApprovalsRegistry
	wizards         *wizardRegistry
	contextEngine   ctxengine.Engine
	mcpOps          *mcpOpsController
	mcpAuth         *mcpAuthController
	nodeInvocations *nodeInvocationRegistry
	nodePending     *nodepending.Store
	canvasHost      *canvas.Host
	channels        *channels.Registry
	channelAccounts *channels.AccountRuntime
	channelPairing  *channels.PairingStore
	approvePairing  func(context.Context, channels.PairingRequest) error
	nostrHub        *nostruntime.NostrHub
	keyer           nostr.Keyer
	messageNostr    messageActionNostrPropagator
	secretResolver  channels.ConcordSecretResolver
	terminalManager *terminalpkg.Manager
	attachGrants    *attachpkg.Store
	worktrees       *worktreespkg.Service
	boardStore      *boardpkg.Store
	boardNotices    *boardpkg.NoticeDeduper
	mcpAppViews     *mcpapppkg.Registry
	conversations   *conversationspkg.Registry
	questions       *questionspkg.Manager
	pluginApprovals *pluginapprovalpkg.Manager
	userProfiles    *userprofilespkg.Manager

	// permEngine is the live WS-G unified permission engine (nil when the
	// operator has not configured permissions). Read-only consumers use it for
	// the effective tool policy overlay (tools.effective) and the permission
	// audit log (audit.list / audit.activity.list).
	permEngine *permissions.Engine

	// restartCh mirrors main()'s restart scheduler channel so gateway.restart.request
	// can trigger the real restart path. nil in unit tests that do not exercise it.
	restartCh chan int
	// suspendCoordinator owns the cooperative daemon suspend/resume lifecycle
	// backing gateway.suspend.prepare/status/resume (swarmstr-ngrd). nil in unit
	// tests that do not exercise the suspend surface (handlers report the surface
	// unavailable rather than nil-deref).
	suspendCoordinator *suspendpkg.Coordinator
	taskSuggestions    *tasksuggestionspkg.Registry
	environments       *environmentspkg.Manager

	// Voice/talk long-tail surface (swarmstr-0tfj). talkSessions is nil until
	// the WS gateway starts (session output streams to the owning connection);
	// talkRouting/talkClients are process-local and always present.
	talkSessions *talkpkg.SessionManager
	talkClients  *talkpkg.ClientStore
	talkRouting  *talkpkg.RoutingStore
}

type hooksEventFirer interface {
	Fire(eventName string, sessionKey string, ctx map[string]any) []error
}

type controlRPCHandler struct {
	deps controlRPCDeps
}

func newControlRPCHandler(deps controlRPCDeps) controlRPCHandler {
	return controlRPCHandler{deps: deps}
}

func requesterSessionKeyFromParent(parent *acppkg.ParentContext, fallback string) string {
	if parent != nil && strings.TrimSpace(parent.SessionID) != "" {
		return strings.TrimSpace(parent.SessionID)
	}
	return strings.TrimSpace(fallback)
}

func (h controlRPCHandler) Handle(ctx context.Context, in nostruntime.ControlRPCInbound) (result nostruntime.ControlRPCResult, err error) {
	defer func() {
		if err != nil {
			metricspkg.RecordHandlerFailure("control_rpc")
		}
	}()

	usageState := h.deps.usageState
	memoryIndex := h.deps.memoryIndex
	configState := h.deps.configState

	method := strings.TrimSpace(in.Method)
	if isSetupMethod(method) {
		return h.handleSetupRPC(ctx, in, method)
	}
	cfg := configState.Get()
	internal := in.Internal
	if !internal && !in.Authenticated && strings.TrimSpace(in.FromPubKey) != "" && !cfg.Control.RequireAuth && in.EventID == "" && in.RequestID == "" && in.RelayURL == "" {
		// Backward-compatible path for in-process daemon/test dispatchers that
		// predate explicit auth metadata. Real ingress paths now set either
		// Authenticated (Nostr/admin/WS principal) or Internal explicitly.
		internal = true
	}
	decision := policy.ControlDecision{Allowed: true, Authenticated: true}
	if !internal {
		decision = policy.EvaluateControlCall(in.FromPubKey, method, in.Authenticated, cfg)
	}
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

	if result, handled, err := h.handleSoulFactoryRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleAgentRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleSessionUsageRPC(ctx, in, method); handled {
		return result, err
	}
	if result, handled, err := h.handleSessionGoalRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleSessionRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleSessionsOpsRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleSessionCollabRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleTaskRPC(ctx, in, method); handled {
		return result, err
	}
	if result, handled, err := h.handleChannelRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleWorkspaceSurfaceRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleSkillProposalEventsRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleToolingRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleAuditRunRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleIntrospectionRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleOpenclawRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleModelsRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handlePluginSurfaceRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleUsersRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleGatewayLifecycleRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleMemoryMaintenanceRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleChatSurfaceRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleMessageActionRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleSkillsSurfaceRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleTalkRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleNodeRPC(ctx, in, method); handled {
		return result, err
	}
	if result, handled, err := h.handleNodeSurfaceRPC(ctx, in, method); handled {
		return result, err
	}
	if result, handled, err := h.handleHooksStatusRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleOpsRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleConfigRPC(ctx, in, method, cfg); handled {
		return result, err
	}
	if result, handled, err := h.handleACPRPC(ctx, in, method); handled {
		return result, err
	}

	switch method {
	case methods.MethodSupportedMethods:
		return nostruntime.ControlRPCResult{Result: supportedMethods(cfg)}, nil
	case methods.MethodHealth:
		result := map[string]any{"ok": true}
		if recovery := recoveryStatusSnapshot(); recovery != nil {
			result["recovery"] = recovery
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	case methods.MethodDoctorMemoryStatus:
		indexAvailable := memoryIndex != nil
		entryCount := 0
		sessionCount := 0
		sessionCountSupported := true
		countSource := "primary_index"
		var storeStatus *memory.StoreStatus
		if memoryIndex != nil {
			if reporter, ok := memoryIndex.(interface{ MemoryStatus() memory.StoreStatus }); ok {
				status := reporter.MemoryStatus()
				storeStatus = &status
				indexAvailable = status.Primary.Available || status.Kind == "hybrid"
				switch status.Kind {
				case "hybrid":
					countSource = "fallback_index"
				case "backend":
					countSource = "primary_backend"
					if status.Primary.Name == "qdrant" {
						sessionCountSupported = false
					}
				}
			}
			if storeStatus == nil || storeStatus.Kind == "index" || storeStatus.Kind == "hybrid" || storeStatus.Primary.Available {
				entryCount = memoryIndex.Count()
				if sessionCountSupported {
					sessionCount = memoryIndex.SessionCount()
				}
			}
		}
		indexStatus := map[string]any{
			"available":    indexAvailable,
			"entry_count":  entryCount,
			"count_source": countSource,
		}
		if sessionCountSupported {
			indexStatus["session_count"] = sessionCount
		} else {
			indexStatus["session_count_supported"] = false
		}
		result := map[string]any{
			"ok":             true,
			"index":          indexStatus,
			"file_memory":    fileMemoryStatusPayload(h.deps.sessionStore),
			"session_memory": sessionMemoryStatusPayload(cfg, h.deps.sessionStore, h.deps.sessionMemoryRuntime),
			"maintenance":    memoryMaintenanceStatusPayload(h.deps.sessionStore),
		}
		if storeStatus != nil {
			result["store"] = memoryStoreStatusPayload(*storeStatus)
		}
		return nostruntime.ControlRPCResult{Result: result}, nil
	default:
		return nostruntime.ControlRPCResult{}, fmt.Errorf("unknown method %q", method)
	}

}
