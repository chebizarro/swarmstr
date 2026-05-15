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
		MethodModelsList,
		MethodToolsCatalog,
		MethodToolsProfileGet,
		MethodToolsProfileSet,
		MethodSkillsStatus,
		MethodSkillsBins,
		MethodSkillsInstall,
		MethodSkillsUpdate,
	}},
	{AdminDispatchChannels, []string{
		MethodChannelsStatus,
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
		MethodCronList,
		MethodCronStatus,
		MethodCronAdd,
		MethodCronUpdate,
		MethodCronRemove,
		MethodCronRun,
		MethodCronRuns,
	}},
	{AdminDispatchExec, []string{
		MethodExecApprovalsGet,
		MethodExecApprovalsSet,
		MethodExecApprovalRequest,
		MethodExecApprovalWaitDecision,
		MethodExecApprovalResolve,
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
		MethodNodePairVerify,
		MethodDevicePairList,
		MethodDevicePairApprove,
		MethodDevicePairReject,
		MethodDevicePairRemove,
		MethodDeviceTokenRotate,
		MethodDeviceTokenRevoke,
		MethodNodeList,
		MethodNodeDescribe,
		MethodNodeRename,
		MethodNodeCanvasCapabilityRefresh,
		MethodNodeInvoke,
		MethodNodeEvent,
		MethodNodeResult,
		MethodNodeInvokeResult,
		MethodNodePendingEnqueue,
		MethodNodePendingPull,
		MethodNodePendingAck,
		MethodNodePendingDrain,
		MethodExecApprovalsNodeGet,
		MethodExecApprovalsNodeSet,
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
		MethodSessionsList,
		MethodSessionsPreview,
		MethodSessionsPatch,
		MethodSessionsReset,
		MethodSessionsDelete,
		MethodSessionsCompact,
		MethodSessionsPrune,
		MethodSessionsExport,
		MethodSessionsSpawn,
	}},
	{AdminDispatchTasks, []string{
		MethodTasksCreate,
		MethodTasksGet,
		MethodTasksList,
		MethodTasksCancel,
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
		MethodHooksList,
		MethodHooksEnable,
		MethodHooksDisable,
		MethodHooksInfo,
		MethodHooksCheck,
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
