package methods

import (
	"sort"
	"strings"

	"metiq/internal/gateway/controlreplay"
)

type AdminDispatchGroup string

const (
	AdminDispatchAgents      AdminDispatchGroup = "agents"
	AdminDispatchChannels    AdminDispatchGroup = "channels"
	AdminDispatchConfig      AdminDispatchGroup = "config"
	AdminDispatchCron        AdminDispatchGroup = "cron"
	AdminDispatchExec        AdminDispatchGroup = "exec"
	AdminDispatchMCP         AdminDispatchGroup = "mcp"
	AdminDispatchMedia       AdminDispatchGroup = "media"
	AdminDispatchNodes       AdminDispatchGroup = "nodes"
	AdminDispatchPlugins     AdminDispatchGroup = "plugins"
	AdminDispatchRuntime     AdminDispatchGroup = "runtime"
	AdminDispatchSessions    AdminDispatchGroup = "sessions"
	AdminDispatchTasks       AdminDispatchGroup = "tasks"
	AdminDispatchSystem      AdminDispatchGroup = "system"
	AdminDispatchACP         AdminDispatchGroup = "acp"
	AdminDispatchSoulFactory AdminDispatchGroup = "soulfactory"
	AdminDispatchWorkspace   AdminDispatchGroup = "workspace"
	AdminDispatchSkills      AdminDispatchGroup = "skills"
	AdminDispatchTalk        AdminDispatchGroup = "talk"
	AdminDispatchUsers       AdminDispatchGroup = "users"
	AdminDispatchOpenclaw    AdminDispatchGroup = "openclaw"
)

var adminDispatchRegistry = []struct {
	group   AdminDispatchGroup
	methods []string
}{
	{AdminDispatchAgents, []string{
		MethodAgent,
		MethodAgentWait,
		MethodAgentIdentityGet,
		MethodGatewayIdentityGet,
		MethodAgentsList,
		MethodAgentsCreate,
		MethodAgentsUpdate,
		MethodAgentsDelete,
		MethodAgentsAssign,
		MethodAgentsUnassign,
		MethodAgentsActive,
		MethodAgentsFilesList,
		MethodAgentsFilesGet,
		MethodAgentsFilesSet,
		// Gateway introspection long tail (swarmstr-wapc): tools.effective (the
		// catalog filtered by profile + permission overlay), tools.invoke (operator
		// tool call via the live ToolRegistry), and the workspace-scoped agent
		// list/get. Grouped with the agents/tools surface.
		MethodToolsEffective,
		MethodToolsInvoke,
		MethodAgentsWorkspaceList,
		MethodAgentsWorkspaceGet,
		MethodModelsList,
		// models.* provider-auth surface (swarmstr-kmhu, BUCKET 1). Grouped with
		// models.list on the agents dispatch surface; backed by the provider/model
		// layer + OAuth adapters. authStatus/probe read; authLogout is admin.
		MethodModelsAuthStatus,
		MethodModelsAuthLogout,
		MethodModelsProbe,
		MethodToolsCatalog,
		MethodToolsProfileGet,
		MethodToolsProfileSet,
		MethodSkillsStatus,
		MethodSkillsBins,
		MethodSkillsInstall,
		MethodSkillsUpdate,
	}},
	{AdminDispatchSkills, []string{
		// Curator lifecycle (swarmstr-xfny.3) + skill-workshop proposals
		// (swarmstr-xfny.4). Served over the control-RPC tooling surface only
		// (like soulfactory/workspace); not exposed on the admin HTTP dispatch.
		MethodSkillsCuratorStatus,
		MethodSkillsCuratorPin,
		MethodSkillsCuratorUnpin,
		MethodSkillsCuratorRestore,
		MethodSkillsProposalsList,
		MethodSkillsProposalsEventsList,
		MethodSkillsProposalsEvaluate,
		MethodSkillsProposalsInspect,
		MethodSkillsProposalsHistoryStatus,
		MethodSkillsProposalsHistoryScan,
		MethodSkillsProposalsCreate,
		MethodSkillsProposalsUpdate,
		MethodSkillsProposalsRevise,
		MethodSkillsProposalsRequestRevision,
		MethodSkillsProposalsApply,
		MethodSkillsProposalsReject,
		MethodSkillsProposalsQuarantine,
		// Discovery quartet (swarmstr-xfny.1) + chunked skill-archive upload
		// (swarmstr-xfny.2). Control-RPC tooling surface only.
		MethodSkillsSearch,
		MethodSkillsDetail,
		MethodSkillsSecurityVerdicts,
		MethodSkillsSkillCard,
		MethodSkillsUploadBegin,
		MethodSkillsUploadChunk,
		MethodSkillsUploadCommit,
	}},
	{AdminDispatchTalk, []string{
		// Voice/talk long tail (swarmstr-0tfj). Control-RPC talk surface only.
		// Phase A: personas / catalog / speak / voicewake routing.
		MethodTTSPersonas,
		MethodTTSSetPersona,
		MethodTalkCatalog,
		MethodTalkSpeak,
		MethodTTSSpeak, // compat alias for talk.speak
		MethodVoicewakeRoutingGet,
		MethodVoicewakeRoutingSet,
		// Phase B: talk.session.* turn lifecycle over gateway-relay.
		MethodTalkSessionCreate,
		MethodTalkSessionJoin,
		MethodTalkSessionAppendAudio,
		MethodTalkSessionStartTurn,
		MethodTalkSessionEndTurn,
		MethodTalkSessionCancelTurn,
		MethodTalkSessionCancelOutput,
		MethodTalkSessionAcknowledgeMark,
		MethodTalkSessionSubmitToolResult,
		MethodTalkSessionSteer,
		MethodTalkSessionClose,
		// Phase C: talk.client.* client-owned sessions.
		MethodTalkClientCreate,
		MethodTalkClientTranscript,
		MethodTalkClientClose,
		MethodTalkClientToolCall,
		MethodTalkClientSteer,
	}},
	{AdminDispatchChannels, []string{
		MethodChannelsStatus,
		MethodChannelsStart,
		MethodChannelsStop,
		MethodChannelsPairingList,
		MethodChannelsPairingApprove,
		MethodChannelsPairingDismiss,
		MethodChannelsLogout,
		MethodChannelsJoin,
		MethodChannelsLeave,
		MethodChannelsList,
		MethodChannelsSend,
		MethodUsageCost,
	}},
	{AdminDispatchConfig, []string{
		MethodConfigGet,
		MethodListGet,
		MethodListPut,
		MethodConfigPut,
		MethodConfigSet,
		MethodConfigApply,
		MethodConfigPatch,
		MethodConfigSchema,
		MethodConfigSchemaLookup,
		MethodSecurityAudit,
	}},
	{AdminDispatchCron, []string{
		MethodCronGet,
		MethodCronList,
		MethodCronStatus,
		MethodCronScratchGet,
		MethodCronScratchSet,
		MethodCronAdd,
		MethodCronUpdate,
		MethodCronRemove,
		MethodCronRun,
		MethodCronRuns,
	}},
	{AdminDispatchExec, []string{
		MethodExecApprovalsGet,
		MethodExecApprovalsSet,
		MethodExecApprovalGet,
		MethodExecApprovalList,
		MethodExecApprovalRequest,
		MethodExecApprovalWaitDecision,
		MethodExecApprovalResolve,
		MethodExecApprovalGrantsList,
		MethodExecApprovalGrantsRevoke,
		MethodApprovalGet,
		MethodApprovalList,
		MethodApprovalResolve,
		// approval.history (swarmstr-wapc): resolved-records view of the durable
		// approval ledger; the closed counterpart of approval.list.
		MethodApprovalHistory,
	}},
	{AdminDispatchMCP, []string{
		MethodMCPList,
		MethodMCPGet,
		MethodMCPPut,
		MethodMCPRemove,
		MethodMCPTest,
		MethodMCPReconnect,
		MethodMCPAuthStart,
		MethodMCPAuthRefresh,
		MethodMCPAuthClear,
		MethodSecretsReload,
		MethodSecretsResolve,
		MethodSecretsStoreList,
		MethodSecretsStoreSet,
		MethodSecretsStoreDelete,
	}},
	{AdminDispatchMedia, []string{
		MethodTalkConfig,
		MethodTalkMode,
		MethodBrowserRequest,
		MethodVoicewakeGet,
		MethodVoicewakeSet,
		MethodTTSStatus,
		MethodTTSProviders,
		MethodTTSSetProvider,
		MethodTTSEnable,
		MethodTTSDisable,
		MethodTTSConvert,
	}},
	{AdminDispatchNodes, []string{
		MethodNodePairRequest,
		MethodNodePairList,
		MethodNodePairApprove,
		MethodNodePairReject,
		MethodNodePairRemove,
		MethodNodePairVerify,
		MethodDevicePairList,
		MethodDevicePairApprove,
		MethodDevicePairReject,
		MethodDevicePairRemove,
		MethodDevicePairRename,
		MethodDeviceTokenRotate,
		MethodDeviceTokenRevoke,
		MethodNodeList,
		MethodNodeDescribe,
		MethodNodeRename,
		MethodNodeCanvasCapabilityRefresh,
		MethodNodeInvoke,
		MethodNodeInvokeProgress,
		MethodNodeEvent,
		MethodNodeResult,
		MethodNodeInvokeResult,
		MethodNodePendingEnqueue,
		MethodNodePendingPull,
		MethodNodePendingAck,
		MethodNodePendingDrain,
		MethodExecApprovalsNodeGet,
		MethodExecApprovalsNodeSet,
		// node.* plugin/skills surface (swarmstr-kmhu, BUCKET 3). Node-scoped
		// refresh/update ops delivered over the durable node pending-command queue
		// (gated on the node being paired + not revoked); the node applies on pull.
		MethodNodePluginSurfaceRefresh,
		MethodNodePluginToolsUpdate,
		MethodNodeSkillsUpdate,
		MethodCanvasGet,
		MethodCanvasList,
		MethodCanvasUpdate,
		MethodCanvasDelete,
	}},
	{AdminDispatchPlugins, []string{
		MethodPluginsInstall,
		MethodPluginsUninstall,
		MethodPluginsUpdate,
		MethodPluginsRegistryList,
		MethodPluginsRegistryGet,
		MethodPluginsRegistrySearch,
		// Plugin-surface long tail (swarmstr-zzin). Control-RPC tooling
		// surface only; merged installed/loaded listing + config-backed
		// enable toggle + manager reload + durable plugin.approval.* ledger.
		MethodPluginsList,
		MethodPluginsSearch,
		MethodPluginsSetEnabled,
		MethodPluginsRefresh,
		// Plugin UI-surface contribution model (swarmstr-qmxu).
		MethodPluginsUIDescriptors,
		MethodPluginsSessionAction,
		MethodPluginSurfaceRefresh,
		MethodPluginApprovalList,
		MethodPluginApprovalRequest,
		MethodPluginApprovalWaitDecision,
		MethodPluginApprovalResolve,
	}},
	{AdminDispatchRuntime, []string{
		MethodLogsTail,
		MethodRuntimeObserve,
		MethodRelayPolicyGet,
	}},
	{AdminDispatchSessions, []string{
		MethodChatSend,
		MethodChatHistory,
		MethodSessionGet,
		MethodSessionsGet,
		MethodSessionsResolve,
		MethodSessionsRecover,
		MethodSessionsGoalUpdate,
		MethodSessionsGoalClear,
		MethodSessionsUsage,
		MethodSessionsUsageTimeseries,
		MethodSessionsUsageLogs,
		MethodSessionsSteer,
		MethodSessionsViewersSet,
		MethodSessionsAssignOwner,
		MethodSessionsList,
		MethodSessionsSubscribe,
		MethodSessionsUnsubscribe,
		MethodSessionsMessagesSubscribe,
		MethodSessionsMessagesUnsubscribe,
		MethodSessionsDescribe,
		MethodSessionsCreate,
		MethodSessionsSend,
		MethodSessionsAbort,
		MethodSessionsPreview,
		MethodSessionsPatch,
		MethodSessionsReset,
		MethodSessionsDelete,
		MethodSessionsCompact,
		MethodSessionsFilesList,
		MethodSessionsFilesGet,
		MethodSessionsFilesSet,
		MethodSessionsFilesReveal,
		MethodSessionsCatalogList,
		MethodSessionsCatalogRead,
		MethodSessionsCatalogContinue,
		MethodSessionsCatalogArchive,
		MethodSessionsCompactionList,
		MethodSessionsCompactionGet,
		MethodSessionsCompactionBranch,
		MethodSessionsCompactionRestore,
		MethodSessionsBranchesList,
		MethodSessionsBranchesSwitch,
		MethodSessionsRewind,
		MethodSessionsFork,
		MethodSessionsSearch,
		MethodSessionsDispatch,
		MethodSessionsReclaim,
		MethodSessionsGroupsList,
		MethodSessionsGroupsDefaults,
		MethodSessionsGroupsUpdate,
		MethodSessionsGroupsPut,
		MethodSessionsGroupsRename,
		MethodSessionsGroupsDelete,
		MethodSessionsPrune,
		MethodSessionsExport,
		MethodSessionsSpawn,
		MethodSessionVisibilitySet,
		MethodSessionMembersList,
		MethodSessionMembersAdd,
		MethodSessionMembersRemove,
		MethodSessionsObserverVisibility,
		MethodSessionSuggestionsAdd,
		MethodSessionSuggestionsList,
		MethodSessionSuggestionsResolve,
		MethodSessionTyping,
		MethodSessionDiscussionInfo,
		MethodSessionDiscussionOpen,
		MethodSessionsObserverAsk,
		// Chat control-UI surface (swarmstr-viqq). chat prefix -> sessions-chat-v4
		// triage group. Backed by the docs/transcript session subsystem.
		MethodChatStartup,
		MethodChatMetadata,
		MethodChatMessageGet,
		MethodChatToolTitles,
		// message.action (swarmstr-ko2f). message prefix -> media-and-messaging
		// triage; grouped here because it mutates the same durable transcript
		// entries the chat surface reads. Verb-dispatched react/edit/delete/retry.
		MethodMessageAction,
		// sessions.* operational long tail (swarmstr-kmhu, BUCKET 2). pluginPatch
		// (plugin-namespaced session-meta mutation), cleanup (terminal/stale-session
		// GC), diff (durable compaction-checkpoint snapshot diff).
		MethodSessionsPluginPatch,
		MethodSessionsCleanup,
		MethodSessionsDiff,
	}},
	{AdminDispatchUsers, []string{
		// Durable user-profile surface (swarmstr-5lln). nostr-user-identity
		// accepted-deviation: profiles keyed by nostr identity + optional email
		// aliases. Reads=OperatorRead, mutations=OperatorAdmin.
		MethodUsersList,
		MethodUsersSelf,
		MethodUsersLinkEmail,
		MethodUsersSetDisplayName,
		MethodUsersSetAvatar,
		MethodUsersPrefsGet,
		MethodUsersPrefsSet,
		MethodUsersSetRole,
	}},
	{AdminDispatchTasks, []string{
		MethodTasksCreate,
		MethodTasksGet,
		MethodTasksList,
		MethodTasksCancel,
		MethodTasksRetry,
		MethodTasksDismiss,
		MethodTasksResume,
		MethodTasksDoctor,
		MethodTasksSummary,
		MethodTasksAuditExport,
		MethodTasksTrace,
	}},
	{AdminDispatchSystem, []string{
		MethodSupportedMethods,
		MethodHealth,
		MethodDoctorMemoryStatus,
		MethodStatus,
		MethodStatusAlias,
		MethodUsageStatus,
		MethodMemorySearch,
		MethodMemoryCompact,
		MethodChatAbort,
		MethodSandboxRun,
		MethodWizardStart,
		MethodWizardNext,
		MethodWizardCancel,
		MethodWizardStatus,
		MethodUpdateRun,
		MethodLastHeartbeat,
		MethodSetHeartbeats,
		MethodWake,
		MethodSystemPresence,
		MethodSystemEvent,
		MethodSend,
		MethodPoll,
		MethodHooksStatus,
		MethodHooksList,
		MethodHooksEnable,
		MethodHooksDisable,
		MethodHooksInfo,
		MethodHooksCheck,
		// Gateway lifecycle (swarmstr-iiot / swarmstr-ngrd). gateway prefix ->
		// core-runtime triage. restart.preflight = readiness snapshot;
		// restart.request triggers the real restart scheduler. suspend.prepare/
		// status/resume drive the cooperative suspend coordinator
		// (internal/gateway/suspend) — durable suspension-id lifecycle that gates
		// the background dispatchers while quiescing in-flight work.
		MethodGatewayRestartPreflight,
		MethodGatewayRestartRequest,
		MethodGatewaySuspendPrepare,
		MethodGatewaySuspendStatus,
		MethodGatewaySuspendResume,
		// Memory-maintenance long tail (swarmstr-wvwk). doctor prefix -> memory-
		// health triage; migrations prefix -> memory-health triage. Backed by
		// internal/memory diagnostics (RepairMemoryHealth/CompactMemoryRecords),
		// the REM dreaming/consolidation phase, and the schema_version migration
		// machinery. Served over the metiqd control-RPC surface
		// (handleMemoryMaintenanceRPC), not admin HTTP dispatch. The remaining
		// dream-diary/grounded-short-term ops stay an honest UNAVAILABLE gap
		// (swarmstr has no persisted diary artifact / grounded-short-term tier;
		// follow-up swarmstr-qc53).
		MethodDoctorMemoryRepairDreamingArtifacts,
		MethodDoctorMemoryDedupeDreamDiary,
		MethodDoctorMemoryRemHarness,
		MethodMigrationsMemoryPlan,
		MethodMigrationsMemoryApply,
		// Persisted dream-diary + grounded-short-term subsystem (swarmstr-qc53):
		// real implementations backed by the durable dream_diary table, the
		// encrypted memory outbox, and the grounded-short-term view over the
		// promotion tier. Served over handleMemoryMaintenanceRPC.
		MethodDoctorMemoryDreamDiary,
		MethodDoctorMemoryBackfillDreamDiary,
		MethodDoctorMemoryResetDreamDiary,
		MethodDoctorMemoryResetGroundedShortTerm,
		// Gateway introspection long tail (swarmstr-wapc). system.info (daemon
		// identity/health), diagnostics.stability (runtime stability snapshot),
		// commands.list (Web UI command catalog), update.status (self-update
		// status over the real update.Checker), audit.list / audit.activity.list
		// (WS-G permission-engine audit log), and ui.command (dispatch a named UI
		// command). All served over handleIntrospectionRPC.
		MethodSystemInfo,
		MethodDiagnosticsStability,
		MethodCommandsList,
		MethodUpdateStatus,
		MethodAuditList,
		MethodAuditActivityList,
		MethodAuditRunInspect,
		MethodUICommand,
	}},
	{AdminDispatchACP, []string{
		MethodACPRegister,
		MethodACPUnregister,
		MethodACPPeers,
		MethodACPDispatch,
		MethodACPPipeline,
		MethodACPSessionInit,
		MethodACPSessionRun,
		MethodACPSessionSpawn,
		MethodACPSessionCancel,
		MethodACPSessionClose,
		MethodACPSessionStatus,
		MethodACPManagerStatus,
	}},
	{AdminDispatchSoulFactory, SoulFactoryMethods()},
	// OpenClaw-branded control-surface compat aliases (swarmstr-i413). Thin
	// aliases that re-dispatch to native Metiq methods (chat.send / chat.history
	// / sessions.files.list / approval.list) and return the native, OpenClaw-
	// modeled response shape. Registering them here flips their parity status to
	// "implemented"; their triage stays accepted-deviation (prefix-locked to the
	// openclaw-branded-control-ui group). The five openclaw.setup.* onboarding
	// methods are deliberately NOT registered — they onboard/activate an OpenClaw
	// install and stay an honest UNAVAILABLE accepted deviation (swarmstr-nuqy).
	{AdminDispatchOpenclaw, []string{
		MethodOpenclawChat,
		MethodOpenclawChatHistory,
		MethodOpenclawChangesList,
		MethodOpenclawApprovalList,
	}},
	{AdminDispatchWorkspace, []string{
		MethodTerminalOpen,
		MethodTerminalInput,
		MethodTerminalResize,
		MethodTerminalClose,
		MethodTerminalAttach,
		MethodTerminalList,
		MethodTerminalText,
		MethodTerminalUpload,
		MethodAttachGrant,
		MethodAttachRevoke,
		MethodFSListDir,
		MethodWorktreesList,
		MethodWorktreesBranches,
		MethodWorktreesCreate,
		MethodWorktreesRemove,
		MethodWorktreesRestore,
		MethodWorktreesGc,
		MethodBoardGet,
		MethodBoardUpdate,
		MethodBoardWidgetPut,
		MethodBoardWidgetGrant,
		MethodBoardEvent,
		MethodBoardWidgetAppView,
		MethodBoardPromptAuthorize,
		MethodBoardDataRead,
		MethodBoardAction,
		MethodMcpAppView,
		MethodMcpAppListTools,
		MethodMcpAppListResources,
		MethodMcpAppListResourceTpls,
		MethodMcpAppReadResource,
		MethodMcpAppCallTool,
		MethodMcpAppUpdateModelContext,
		MethodConversationsList,
		MethodConversationsSend,
		MethodConversationsTurn,
		MethodConversationsTurnCancel,
		MethodQuestionRequest,
		MethodQuestionWaitAnswer,
		MethodQuestionResolve,
		MethodQuestionGet,
		MethodQuestionList,
		MethodTaskSuggestionsList,
		MethodTaskSuggestionsCreate,
		MethodTaskSuggestionsAccept,
		MethodTaskSuggestionsDismiss,
		MethodArtifactsList,
		MethodArtifactsGet,
		MethodArtifactsDownload,
		MethodEnvironmentsList,
		MethodEnvironmentsStatus,
		MethodEnvironmentsCreate,
		MethodEnvironmentsDestroy,
	}},
}

type ControlReplayPolicy = controlreplay.Policy

const (
	ControlReplayNone            = controlreplay.None
	ControlReplayEventOnly       = controlreplay.EventOnly
	ControlReplayEventAndRequest = controlreplay.EventAndRequest
)

func ControlMethodReplayPolicy(method string) ControlReplayPolicy {
	return controlreplay.MethodPolicy(method)
}

func SoulFactoryMethods() []string {
	return []string{
		MethodSoulFactoryProvision,
		MethodSoulFactoryUpdate,
		MethodSoulFactorySuspend,
		MethodSoulFactoryResume,
		MethodSoulFactoryRedeploy,
		MethodSoulFactoryRevoke,
		MethodSoulFactoryAvatarGenerate,
		MethodSoulFactoryAvatarSet,
		MethodSoulFactoryVoiceConfigure,
		MethodSoulFactoryVoiceSample,
		MethodSoulFactoryMemoryConfigure,
		MethodSoulFactoryMemoryReindex,
		MethodSoulFactoryPersonaUpdate,
		MethodSoulFactoryConfigReload,
	}
}

func IsSoulFactoryMethod(method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return false
	}
	for _, candidate := range SoulFactoryMethods() {
		if method == candidate {
			return true
		}
	}
	return false
}

func SupportedMethods() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, entry := range adminDispatchRegistry {
		for _, method := range entry.methods {
			method = strings.TrimSpace(method)
			if method == "" {
				continue
			}
			if _, ok := seen[method]; ok {
				continue
			}
			seen[method] = struct{}{}
			out = append(out, method)
		}
	}
	sort.Strings(out)
	return out
}

func InAdminDispatchGroup(group AdminDispatchGroup, method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return false
	}
	for _, entry := range adminDispatchRegistry {
		if entry.group != group {
			continue
		}
		for _, candidate := range entry.methods {
			if method == candidate {
				return true
			}
		}
		return false
	}
	return false
}
