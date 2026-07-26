package methods

import (
	"sort"
	"strings"

	"metiq/internal/gateway/protocol"
)

const descriptorSince = "<=2026.7"

// MethodDescriptors returns deterministic public policy metadata for every
// dispatchable method. Core methods are classified explicitly by method family;
// extension-owned names fail closed at operator.admin until their host supplies
// a narrower descriptor.
func MethodDescriptors(names []string) []protocol.MethodDescriptor {
	seen := make(map[string]struct{}, len(names))
	out := make([]protocol.MethodDescriptor, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, MethodDescriptor(name))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MethodDescriptor resolves the canonical descriptor for one method. Unknown
// extension methods deliberately receive admin scope rather than inheriting a
// permissive default.
func MethodDescriptor(name string) protocol.MethodDescriptor {
	name = strings.TrimSpace(name)
	d := protocol.MethodDescriptor{
		Name:  name,
		Scope: protocol.MethodScopeOperatorAdmin,
		Since: descriptorSince,
	}

	if scope, ok := exactMethodScopes[name]; ok {
		d.Scope = scope
	} else {
		d.Scope = inferredMethodScope(name)
	}
	if startupUnavailableMethods[name] {
		d.Startup = protocol.MethodStartupUnavailableUntilSidecars
	}
	if controlPlaneWriteMethods[name] {
		d.ControlPlaneWrite = true
	}
	return d
}

var exactMethodScopes = map[string]string{
	MethodAgent:                    protocol.MethodScopeOperatorWrite,
	MethodAgentWait:                protocol.MethodScopeOperatorWrite,
	MethodAgentIdentityGet:         protocol.MethodScopeOperatorRead,
	MethodGatewayIdentityGet:       protocol.MethodScopeOperatorRead,
	MethodApprovalGet:              protocol.MethodScopeOperatorApprovals,
	MethodApprovalList:             protocol.MethodScopeOperatorApprovals,
	MethodApprovalResolve:          protocol.MethodScopeOperatorApprovals,
	MethodExecApprovalGet:          protocol.MethodScopeOperatorApprovals,
	MethodExecApprovalList:         protocol.MethodScopeOperatorApprovals,
	MethodExecApprovalRequest:      protocol.MethodScopeOperatorApprovals,
	MethodExecApprovalWaitDecision: protocol.MethodScopeOperatorApprovals,
	MethodExecApprovalResolve:      protocol.MethodScopeOperatorApprovals,
	MethodNodeInvoke:               protocol.MethodScopeOperatorAdmin,
	MethodSessionsCreate:           protocol.MethodScopeOperatorAdmin,
	MethodSessionsDispatch:         protocol.MethodScopeOperatorAdmin,
	MethodSessionsReclaim:          protocol.MethodScopeOperatorAdmin,
	MethodSessionsGroupsPut:        protocol.MethodScopeOperatorWrite,
	MethodSessionsGroupsRename:     protocol.MethodScopeOperatorWrite,
	MethodSessionsGroupsDelete:     protocol.MethodScopeOperatorWrite,
	MethodSessionsPatch:            protocol.MethodScopeOperatorAdmin,
	MethodTalkConfig:               protocol.MethodScopeOperatorAdmin,
	MethodSkillsBins:               protocol.MethodScopeNode,
	MethodNodePendingPull:          protocol.MethodScopeNode,
	MethodNodePendingAck:           protocol.MethodScopeNode,
	MethodNodePendingDrain:         protocol.MethodScopeNode,
	MethodNodeInvokeProgress:       protocol.MethodScopeNode,
	MethodNodeInvokeResult:         protocol.MethodScopeNode,
	MethodNodeEvent:                protocol.MethodScopeNode,
	MethodNodeResult:               protocol.MethodScopeNode,
	MethodNodePairList:             protocol.MethodScopeOperatorPairing,
	MethodNodePairApprove:          protocol.MethodScopeOperatorPairing,
	MethodNodePairReject:           protocol.MethodScopeOperatorPairing,
	MethodNodePairRemove:           protocol.MethodScopeOperatorPairing,
	MethodNodePairRequest:          protocol.MethodScopeNode,
	MethodNodePairVerify:           protocol.MethodScopeNode,
	MethodNodeRename:               protocol.MethodScopeOperatorPairing,
	MethodDevicePairList:           protocol.MethodScopeOperatorPairing,
	MethodDevicePairApprove:        protocol.MethodScopeOperatorPairing,
	MethodDevicePairReject:         protocol.MethodScopeOperatorPairing,
	MethodDevicePairRemove:         protocol.MethodScopeOperatorPairing,
	MethodDevicePairRename:         protocol.MethodScopeOperatorPairing,
	MethodDeviceTokenRotate:        protocol.MethodScopeOperatorPairing,
	MethodDeviceTokenRevoke:        protocol.MethodScopeOperatorPairing,
	MethodExecApprovalsGet:         protocol.MethodScopeOperatorAdmin,
	MethodExecApprovalsSet:         protocol.MethodScopeOperatorAdmin,
	MethodExecApprovalsNodeGet:     protocol.MethodScopeOperatorAdmin,
	MethodExecApprovalsNodeSet:     protocol.MethodScopeOperatorAdmin,
	MethodSupportedMethods:         protocol.MethodScopeOperatorRead,
	MethodHealth:                   protocol.MethodScopeOperatorRead,
	MethodStatus:                   protocol.MethodScopeOperatorRead,
	MethodStatusAlias:              protocol.MethodScopeOperatorRead,
	MethodDoctorMemoryStatus:       protocol.MethodScopeOperatorRead,
	// Memory-maintenance long tail (swarmstr-wvwk): the whole bucket is an
	// operator.admin maintenance surface. plan is side-effect free but still
	// admin-gated (it reports the store path/versions and pairs with apply);
	// apply + the mutating doctor.memory consolidation/dedupe/repair ops
	// mutate the store.
	MethodMigrationsMemoryPlan:                protocol.MethodScopeOperatorAdmin,
	MethodMigrationsMemoryApply:               protocol.MethodScopeOperatorAdmin,
	MethodDoctorMemoryRepairDreamingArtifacts: protocol.MethodScopeOperatorAdmin,
	MethodDoctorMemoryDedupeDreamDiary:        protocol.MethodScopeOperatorAdmin,
	MethodDoctorMemoryRemHarness:              protocol.MethodScopeOperatorAdmin,
	// Persisted dream-diary + grounded-short-term (swarmstr-qc53). dreamDiary is
	// a read view (operator.read); backfill writes retroactive entries and the
	// two reset ops are destructive/demoting — all admin-gated.
	MethodDoctorMemoryDreamDiary:             protocol.MethodScopeOperatorRead,
	MethodDoctorMemoryBackfillDreamDiary:     protocol.MethodScopeOperatorAdmin,
	MethodDoctorMemoryResetDreamDiary:        protocol.MethodScopeOperatorAdmin,
	MethodDoctorMemoryResetGroundedShortTerm: protocol.MethodScopeOperatorAdmin,
	MethodLogsTail:                            protocol.MethodScopeOperatorRead,
	MethodRuntimeObserve:                      protocol.MethodScopeOperatorRead,
	MethodRelayPolicyGet:                      protocol.MethodScopeOperatorRead,
	MethodSecurityAudit:                       protocol.MethodScopeOperatorRead,
	MethodMemorySearch:                        protocol.MethodScopeOperatorRead,
	MethodSystemPresence:                      protocol.MethodScopeOperatorRead,
	MethodLastHeartbeat:                       protocol.MethodScopeOperatorRead,
	MethodUsageStatus:                         protocol.MethodScopeOperatorRead,
	MethodUsageCost:                           protocol.MethodScopeOperatorRead,
	MethodModelsList:                          protocol.MethodScopeOperatorRead,
	// models.* provider-auth surface (swarmstr-kmhu, BUCKET 1): status/probe are
	// read-only (probe is a bounded reachability check that leaks no secrets);
	// authLogout clears stored provider credentials → operator.admin.
	MethodModelsAuthStatus: protocol.MethodScopeOperatorRead,
	MethodModelsProbe:      protocol.MethodScopeOperatorRead,
	MethodModelsAuthLogout: protocol.MethodScopeOperatorAdmin,
	// sessions.* operational long tail (swarmstr-kmhu, BUCKET 2): diff is a
	// read-only snapshot comparison; pluginPatch + cleanup mutate → operator.admin.
	MethodSessionsDiff:        protocol.MethodScopeOperatorRead,
	MethodSessionsPluginPatch: protocol.MethodScopeOperatorAdmin,
	MethodSessionsCleanup:     protocol.MethodScopeOperatorAdmin,
	// node.* plugin/skills surface (swarmstr-kmhu, BUCKET 3): all three enqueue a
	// durable command to a paired node → operator.admin.
	MethodNodePluginSurfaceRefresh: protocol.MethodScopeOperatorAdmin,
	MethodNodePluginToolsUpdate:    protocol.MethodScopeOperatorAdmin,
	MethodNodeSkillsUpdate:         protocol.MethodScopeOperatorAdmin,
	// Gateway introspection long tail (swarmstr-wapc): all reads are
	// operator.read. tools.invoke executes a tool and ui.command dispatches an
	// operator command, so both are operator.admin. approval.history reads the
	// approval ledger and matches the approval.* family (operator.approvals).
	MethodSystemInfo:           protocol.MethodScopeOperatorRead,
	MethodDiagnosticsStability: protocol.MethodScopeOperatorRead,
	MethodCommandsList:         protocol.MethodScopeOperatorRead,
	MethodUpdateStatus:         protocol.MethodScopeOperatorRead,
	MethodToolsEffective:       protocol.MethodScopeOperatorRead,
	MethodToolsInvoke:          protocol.MethodScopeOperatorAdmin,
	MethodAuditList:            protocol.MethodScopeOperatorRead,
	MethodAuditActivityList:    protocol.MethodScopeOperatorRead,
	MethodAgentsWorkspaceList:  protocol.MethodScopeOperatorRead,
	MethodAgentsWorkspaceGet:   protocol.MethodScopeOperatorRead,
	MethodApprovalHistory:      protocol.MethodScopeOperatorApprovals,
	MethodUICommand:            protocol.MethodScopeOperatorAdmin,
	// OpenClaw-branded control-surface compat aliases (swarmstr-i413). Each alias
	// carries the same scope as the native method it re-dispatches to:
	// openclaw.chat -> chat.send (operator.write), openclaw.chat.history ->
	// chat.history (operator.read), openclaw.changes.list -> sessions.files.list
	// (operator.read), openclaw.approval.list -> approval.list (operator.approvals).
	MethodOpenclawChat:         protocol.MethodScopeOperatorWrite,
	MethodOpenclawChatHistory:  protocol.MethodScopeOperatorRead,
	MethodOpenclawChangesList:  protocol.MethodScopeOperatorRead,
	MethodOpenclawApprovalList: protocol.MethodScopeOperatorApprovals,
	MethodToolsCatalog:                        protocol.MethodScopeOperatorRead,
	MethodToolsProfileGet:                     protocol.MethodScopeOperatorRead,
	MethodSkillsStatus:                        protocol.MethodScopeOperatorRead,
	MethodSkillsCuratorStatus:                 protocol.MethodScopeOperatorRead,
	MethodSkillsProposalsList:                 protocol.MethodScopeOperatorRead,
	MethodSkillsProposalsInspect:              protocol.MethodScopeOperatorRead,
	MethodSkillsSearch:                        protocol.MethodScopeOperatorRead,
	MethodSkillsDetail:                        protocol.MethodScopeOperatorRead,
	MethodSkillsSecurityVerdicts:              protocol.MethodScopeOperatorRead,
	MethodSkillsSkillCard:                     protocol.MethodScopeOperatorRead,
	MethodSkillsUploadBegin:                   protocol.MethodScopeOperatorAdmin,
	MethodSkillsUploadChunk:                   protocol.MethodScopeOperatorAdmin,
	MethodSkillsUploadCommit:                  protocol.MethodScopeOperatorAdmin,
	// Plugin-surface long tail (swarmstr-zzin): reads for listing/search +
	// pending-approval enumeration; setEnabled/refresh + approval
	// request/waitDecision/resolve default to OperatorAdmin.
	MethodPluginsList:        protocol.MethodScopeOperatorRead,
	MethodPluginsSearch:      protocol.MethodScopeOperatorRead,
	MethodPluginApprovalList: protocol.MethodScopeOperatorRead,
	// Users durable-profile surface (swarmstr-5lln): reads enumerate/self,
	// mutations (linkEmail/setDisplayName/setAvatar) require operator.admin.
	MethodUsersList:           protocol.MethodScopeOperatorRead,
	MethodUsersSelf:           protocol.MethodScopeOperatorRead,
	MethodUsersLinkEmail:      protocol.MethodScopeOperatorAdmin,
	MethodUsersSetDisplayName: protocol.MethodScopeOperatorAdmin,
	MethodUsersSetAvatar:      protocol.MethodScopeOperatorAdmin,
	// Gateway lifecycle (swarmstr-iiot): preflight is a read-only readiness
	// snapshot (OpenClaw scopes it operator.read); request triggers a restart.
	MethodGatewayRestartPreflight: protocol.MethodScopeOperatorRead,
	MethodGatewayRestartRequest:   protocol.MethodScopeOperatorAdmin,
	// Chat control-UI surface (swarmstr-viqq): read-only bootstrap/metadata/
	// message lookup + deterministic tool titles.
	MethodChatStartup:    protocol.MethodScopeOperatorRead,
	MethodChatMetadata:   protocol.MethodScopeOperatorRead,
	MethodChatMessageGet: protocol.MethodScopeOperatorRead,
	MethodChatToolTitles: protocol.MethodScopeOperatorRead,
	MethodVoicewakeGet:   protocol.MethodScopeOperatorRead,
	MethodTTSStatus:      protocol.MethodScopeOperatorRead,
	MethodTTSProviders:   protocol.MethodScopeOperatorRead,
	// Voice/talk long tail (swarmstr-0tfj): read for discovery/get, write for
	// synthesis + session/turn/routing mutations.
	MethodTTSPersonas:                 protocol.MethodScopeOperatorRead,
	MethodTTSSetPersona:               protocol.MethodScopeOperatorWrite,
	MethodTalkCatalog:                 protocol.MethodScopeOperatorRead,
	MethodTalkSpeak:                   protocol.MethodScopeOperatorWrite,
	MethodVoicewakeRoutingGet:         protocol.MethodScopeOperatorRead,
	MethodVoicewakeRoutingSet:         protocol.MethodScopeOperatorWrite,
	MethodTalkSessionCreate:           protocol.MethodScopeOperatorWrite,
	MethodTalkSessionJoin:             protocol.MethodScopeOperatorWrite,
	MethodTalkSessionAppendAudio:      protocol.MethodScopeOperatorWrite,
	MethodTalkSessionStartTurn:        protocol.MethodScopeOperatorWrite,
	MethodTalkSessionEndTurn:          protocol.MethodScopeOperatorWrite,
	MethodTalkSessionCancelTurn:       protocol.MethodScopeOperatorWrite,
	MethodTalkSessionCancelOutput:     protocol.MethodScopeOperatorWrite,
	MethodTalkSessionAcknowledgeMark:  protocol.MethodScopeOperatorWrite,
	MethodTalkSessionSubmitToolResult: protocol.MethodScopeOperatorWrite,
	MethodTalkSessionSteer:            protocol.MethodScopeOperatorWrite,
	MethodTalkSessionClose:            protocol.MethodScopeOperatorWrite,
	MethodTalkClientCreate:            protocol.MethodScopeOperatorWrite,
	MethodTalkClientTranscript:        protocol.MethodScopeOperatorWrite,
	MethodTalkClientClose:             protocol.MethodScopeOperatorWrite,
	MethodTalkClientToolCall:          protocol.MethodScopeOperatorWrite,
	MethodTalkClientSteer:             protocol.MethodScopeOperatorWrite,
	MethodConfigGet:                   protocol.MethodScopeOperatorRead,
	MethodConfigSchemaLookup:          protocol.MethodScopeOperatorRead,
	MethodListGet:                     protocol.MethodScopeOperatorRead,
	MethodCronGet:                     protocol.MethodScopeOperatorRead,
	MethodCronList:                    protocol.MethodScopeOperatorRead,
	MethodCronStatus:                  protocol.MethodScopeOperatorRead,
	MethodCronRuns:                    protocol.MethodScopeOperatorRead,
	MethodChannelsStatus:              protocol.MethodScopeOperatorRead,
	MethodChannelsStart:               protocol.MethodScopeOperatorAdmin,
	MethodChannelsStop:                protocol.MethodScopeOperatorAdmin,
	MethodChannelsPairingList:         protocol.MethodScopeOperatorPairing,
	MethodChannelsPairingApprove:      protocol.MethodScopeOperatorPairing,
	MethodChannelsPairingDismiss:      protocol.MethodScopeOperatorPairing,
	MethodChannelsList:                protocol.MethodScopeOperatorRead,
	MethodAgentsList:                  protocol.MethodScopeOperatorRead,
	MethodAgentsActive:                protocol.MethodScopeOperatorRead,
	MethodAgentsFilesList:             protocol.MethodScopeOperatorRead,
	MethodAgentsFilesGet:              protocol.MethodScopeOperatorRead,
	MethodMCPList:                     protocol.MethodScopeOperatorRead,
	MethodMCPGet:                      protocol.MethodScopeOperatorRead,
	MethodMCPTest:                     protocol.MethodScopeOperatorRead,
	MethodPluginsRegistryList:         protocol.MethodScopeOperatorRead,
	MethodPluginsRegistryGet:          protocol.MethodScopeOperatorRead,
	MethodPluginsRegistrySearch:       protocol.MethodScopeOperatorRead,
	MethodSessionsList:                protocol.MethodScopeOperatorRead,
	MethodSessionGet:                  protocol.MethodScopeOperatorRead,
	MethodSessionsDescribe:            protocol.MethodScopeOperatorRead,
	MethodSessionsPreview:             protocol.MethodScopeOperatorRead,
	MethodSessionsFilesList:           protocol.MethodScopeOperatorRead,
	MethodSessionsFilesGet:            protocol.MethodScopeOperatorRead,
	MethodSessionsFilesSet:            protocol.MethodScopeOperatorAdmin,
	MethodSessionsFilesReveal:         protocol.MethodScopeOperatorAdmin,
	MethodSessionsCatalogList:         protocol.MethodScopeOperatorRead,
	MethodSessionsCatalogRead:         protocol.MethodScopeOperatorRead,
	MethodSessionsCatalogContinue:     protocol.MethodScopeOperatorWrite,
	MethodSessionsCatalogArchive:      protocol.MethodScopeOperatorWrite,
	MethodSessionsCompactionList:      protocol.MethodScopeOperatorRead,
	MethodSessionsCompactionGet:       protocol.MethodScopeOperatorRead,
	MethodSessionsCompactionBranch:    protocol.MethodScopeOperatorWrite,
	MethodSessionsCompactionRestore:   protocol.MethodScopeOperatorAdmin,
	MethodSessionsBranchesList:        protocol.MethodScopeOperatorRead,
	MethodSessionsBranchesSwitch:      protocol.MethodScopeOperatorAdmin,
	MethodSessionsRewind:              protocol.MethodScopeOperatorAdmin,
	MethodSessionsFork:                protocol.MethodScopeOperatorWrite,
	MethodSessionsSearch:              protocol.MethodScopeOperatorRead,
	MethodSessionsGroupsList:          protocol.MethodScopeOperatorRead,
	MethodSessionVisibilitySet:        protocol.MethodScopeOperatorWrite,
	MethodSessionMembersList:          protocol.MethodScopeOperatorRead,
	MethodSessionMembersAdd:           protocol.MethodScopeOperatorWrite,
	MethodSessionMembersRemove:        protocol.MethodScopeOperatorWrite,
	MethodSessionsObserverVisibility:  protocol.MethodScopeOperatorRead,
	MethodSessionSuggestionsAdd:       protocol.MethodScopeOperatorWrite,
	MethodSessionSuggestionsList:      protocol.MethodScopeOperatorRead,
	MethodSessionSuggestionsResolve:   protocol.MethodScopeOperatorWrite,
	MethodSessionTyping:               protocol.MethodScopeOperatorWrite,
	MethodSessionDiscussionInfo:       protocol.MethodScopeOperatorRead,
	MethodSessionDiscussionOpen:       protocol.MethodScopeOperatorWrite,
	MethodSessionsObserverAsk:         protocol.MethodScopeOperatorRead,
	MethodSessionsExport:              protocol.MethodScopeOperatorRead,
	MethodSessionsSubscribe:           protocol.MethodScopeOperatorRead,
	MethodSessionsUnsubscribe:         protocol.MethodScopeOperatorRead,
	MethodSessionsMessagesSubscribe:   protocol.MethodScopeOperatorRead,
	MethodSessionsMessagesUnsubscribe: protocol.MethodScopeOperatorRead,
	MethodChatHistory:                 protocol.MethodScopeOperatorRead,
	MethodNodeList:                    protocol.MethodScopeOperatorRead,
	MethodNodeDescribe:                protocol.MethodScopeOperatorRead,
	MethodCanvasGet:                   protocol.MethodScopeOperatorRead,
	MethodCanvasList:                  protocol.MethodScopeOperatorRead,
	MethodTasksGet:                    protocol.MethodScopeOperatorRead,
	MethodTasksList:                   protocol.MethodScopeOperatorRead,
	MethodTasksDoctor:                 protocol.MethodScopeOperatorRead,
	MethodTasksSummary:                protocol.MethodScopeOperatorRead,
	MethodTasksTrace:                  protocol.MethodScopeOperatorRead,
	MethodTasksAuditExport:            protocol.MethodScopeOperatorRead,
	MethodACPPeers:                    protocol.MethodScopeOperatorRead,
	MethodACPManagerStatus:            protocol.MethodScopeOperatorRead,
	MethodACPSessionStatus:            protocol.MethodScopeOperatorRead,
	MethodHooksList:                   protocol.MethodScopeOperatorRead,
	MethodHooksInfo:                   protocol.MethodScopeOperatorRead,
	MethodHooksCheck:                  protocol.MethodScopeOperatorRead,
	"events.list":                     protocol.MethodScopeOperatorRead,
	"events.subscribe":                protocol.MethodScopeOperatorRead,
	"events.unsubscribe":              protocol.MethodScopeOperatorRead,
	MethodTerminalOpen:                protocol.MethodScopeOperatorAdmin,
	MethodTerminalInput:               protocol.MethodScopeOperatorAdmin,
	MethodTerminalResize:              protocol.MethodScopeOperatorAdmin,
	MethodTerminalClose:               protocol.MethodScopeOperatorAdmin,
	MethodTerminalAttach:              protocol.MethodScopeOperatorAdmin,
	MethodTerminalList:                protocol.MethodScopeOperatorAdmin,
	MethodTerminalText:                protocol.MethodScopeOperatorAdmin,
	MethodTerminalUpload:              protocol.MethodScopeOperatorAdmin,
	MethodAttachGrant:                 protocol.MethodScopeOperatorAdmin,
	MethodAttachRevoke:                protocol.MethodScopeOperatorAdmin,
	MethodFSListDir:                   protocol.MethodScopeOperatorAdmin,
	MethodWorktreesList:               protocol.MethodScopeOperatorRead,
	MethodWorktreesBranches:           protocol.MethodScopeOperatorWrite,
	MethodWorktreesCreate:             protocol.MethodScopeOperatorAdmin,
	MethodWorktreesRemove:             protocol.MethodScopeOperatorAdmin,
	MethodWorktreesRestore:            protocol.MethodScopeOperatorAdmin,
	MethodWorktreesGc:                 protocol.MethodScopeOperatorAdmin,
	MethodBoardGet:                    protocol.MethodScopeOperatorRead,
	MethodBoardUpdate:                 protocol.MethodScopeOperatorWrite,
	MethodBoardWidgetPut:              protocol.MethodScopeOperatorWrite,
	MethodBoardWidgetGrant:            protocol.MethodScopeOperatorApprovals,
	MethodBoardEvent:                  protocol.MethodScopeOperatorWrite,
	MethodBoardWidgetAppView:          protocol.MethodScopeOperatorRead,
	MethodBoardPromptAuthorize:        protocol.MethodScopeOperatorRead,
	MethodBoardDataRead:               protocol.MethodScopeOperatorRead,
	MethodBoardAction:                 protocol.MethodScopeOperatorWrite,
	MethodMcpAppView:                  protocol.MethodScopeOperatorRead,
	MethodMcpAppListTools:             protocol.MethodScopeOperatorRead,
	MethodMcpAppListResources:         protocol.MethodScopeOperatorRead,
	MethodMcpAppListResourceTpls:      protocol.MethodScopeOperatorRead,
	MethodMcpAppReadResource:          protocol.MethodScopeOperatorRead,
	MethodMcpAppCallTool:              protocol.MethodScopeOperatorWrite,
	MethodMcpAppUpdateModelContext:    protocol.MethodScopeOperatorWrite,
	MethodConversationsList:           protocol.MethodScopeOperatorAdmin,
	MethodConversationsSend:           protocol.MethodScopeOperatorAdmin,
	MethodConversationsTurn:           protocol.MethodScopeOperatorAdmin,
	MethodConversationsTurnCancel:     protocol.MethodScopeOperatorAdmin,
	MethodQuestionRequest:             protocol.MethodScopeOperatorQuestions,
	MethodQuestionWaitAnswer:          protocol.MethodScopeOperatorQuestions,
	MethodQuestionResolve:             protocol.MethodScopeOperatorQuestions,
	MethodQuestionGet:                 protocol.MethodScopeOperatorQuestions,
	MethodQuestionList:                protocol.MethodScopeOperatorQuestions,
	MethodTaskSuggestionsList:         protocol.MethodScopeOperatorRead,
	MethodTaskSuggestionsCreate:       protocol.MethodScopeOperatorWrite,
	MethodTaskSuggestionsAccept:       protocol.MethodScopeOperatorAdmin,
	MethodTaskSuggestionsDismiss:      protocol.MethodScopeOperatorWrite,
	MethodArtifactsList:               protocol.MethodScopeOperatorRead,
	MethodArtifactsGet:                protocol.MethodScopeOperatorRead,
	MethodArtifactsDownload:           protocol.MethodScopeOperatorRead,
	MethodEnvironmentsList:            protocol.MethodScopeOperatorRead,
	MethodEnvironmentsStatus:          protocol.MethodScopeOperatorRead,
	MethodEnvironmentsCreate:          protocol.MethodScopeOperatorAdmin,
	MethodEnvironmentsDestroy:         protocol.MethodScopeOperatorAdmin,
}

func inferredMethodScope(name string) string {
	if name == "" {
		return protocol.MethodScopeOperatorAdmin
	}
	// Ordinary runtime actions can be delegated to an operator.write token.
	if hasAnyPrefix(name,
		"chat.", "sessions.send", "sessions.abort", "sessions.spawn",
		"node.pending.enqueue", "canvas.update", "canvas.delete",
		"channels.send", "send", "poll", "wake", "talk.mode",
		"tts.enable", "tts.disable", "tts.convert", "tts.setProvider",
		"voicewake.set", "tasks.create", "tasks.cancel", "tasks.resume",
		"acp.dispatch", "acp.pipeline", "acp.session.",
	) {
		return protocol.MethodScopeOperatorWrite
	}
	return protocol.MethodScopeOperatorAdmin
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if value == prefix || strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

var startupUnavailableMethods = map[string]bool{
	MethodAgent:                   true,
	MethodAgentWait:               true,
	MethodChatHistory:             true,
	MethodChatSend:                true,
	MethodModelsList:              true,
	MethodSessionsList:            true,
	MethodSessionsCreate:          true,
	MethodSessionsSend:            true,
	MethodSessionsAbort:           true,
	MethodSessionsFilesList:       true,
	MethodSessionsFilesGet:        true,
	MethodSessionsFilesSet:        true,
	MethodSessionsFilesReveal:     true,
	MethodSessionsCatalogList:     true,
	MethodSessionsCatalogRead:     true,
	MethodSessionsCatalogContinue: true,
	MethodSessionsCatalogArchive:  true,
}

var controlPlaneWriteMethods = map[string]bool{
	MethodConfigApply:             true,
	MethodConfigPatch:             true,
	MethodUpdateRun:               true,
	MethodPluginsInstall:          true,
	MethodPluginsUninstall:        true,
	MethodPluginsUpdate:           true,
	MethodSoulFactoryProvision:    true,
	MethodSoulFactoryUpdate:       true,
	MethodSoulFactorySuspend:      true,
	MethodSoulFactoryResume:       true,
	MethodSoulFactoryRedeploy:     true,
	MethodSoulFactoryRevoke:       true,
	MethodSoulFactoryConfigReload: true,
	MethodWorktreesCreate:         true,
	MethodWorktreesRemove:         true,
	MethodWorktreesRestore:        true,
	MethodWorktreesGc:             true,
	MethodAttachGrant:             true,
	MethodEnvironmentsCreate:      true,
	MethodEnvironmentsDestroy:     true,
}
