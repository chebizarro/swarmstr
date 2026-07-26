package methods

const (
	MethodSupportedMethods       = "supportedmethods"
	MethodHealth                 = "health"
	MethodDoctorMemoryStatus     = "doctor.memory.status"
	MethodLogsTail               = "logs.tail"
	MethodRuntimeObserve         = "runtime.observe"
	MethodChannelsStatus         = "channels.status"
	MethodChannelsStart          = "channels.start"
	MethodChannelsStop           = "channels.stop"
	MethodChannelsPairingList    = "channels.pairing.list"
	MethodChannelsPairingApprove = "channels.pairing.approve"
	MethodChannelsPairingDismiss = "channels.pairing.dismiss"
	MethodChannelsLogout         = "channels.logout"
	MethodChannelsJoin           = "channels.join"
	MethodChannelsLeave          = "channels.leave"
	MethodChannelsList           = "channels.list"
	MethodChannelsSend           = "channels.send"
	MethodStatus                 = "status.get"
	MethodStatusAlias            = "status"
	MethodUsageStatus            = "usage.status"
	MethodUsageCost              = "usage.cost"
	MethodMemorySearch           = "memory.search"
	MethodMemoryCompact          = "memory.compact"
	// Memory-maintenance long tail (swarmstr-wvwk). doctor.memory.* diagnostic/
	// consolidation ops + migrations.memory.* versioned-store maintenance,
	// backed by internal/memory diagnostics + schema_version machinery.
	MethodDoctorMemoryRepairDreamingArtifacts = "doctor.memory.repairDreamingArtifacts"
	MethodDoctorMemoryDedupeDreamDiary        = "doctor.memory.dedupeDreamDiary"
	MethodDoctorMemoryRemHarness              = "doctor.memory.remHarness"
	MethodMigrationsMemoryPlan                = "migrations.memory.plan"
	MethodMigrationsMemoryApply               = "migrations.memory.apply"
	// Persisted dream-diary + grounded-short-term subsystem (swarmstr-qc53).
	// dreamDiary reads the durable diary; backfillDreamDiary replays
	// consolidation over existing memories into retroactive dated entries;
	// resetDreamDiary clears the diary (confirm-gated); resetGroundedShortTerm
	// demotes/unpromotes the grounded-short-term buffer (confirm-gated).
	MethodDoctorMemoryDreamDiary             = "doctor.memory.dreamDiary"
	MethodDoctorMemoryBackfillDreamDiary     = "doctor.memory.backfillDreamDiary"
	MethodDoctorMemoryResetDreamDiary        = "doctor.memory.resetDreamDiary"
	MethodDoctorMemoryResetGroundedShortTerm = "doctor.memory.resetGroundedShortTerm"
	MethodAgent                              = "agent"
	MethodAgentWait                          = "agent.wait"
	MethodAgentIdentityGet                   = "agent.identity.get"
	MethodChatSend                           = "chat.send"
	MethodChatHistory                        = "chat.history"
	MethodChatAbort                          = "chat.abort"
	MethodSessionGet                         = "session.get"
	MethodSessionsList                       = "sessions.list"
	MethodSessionsSubscribe                  = "sessions.subscribe"
	MethodSessionsUnsubscribe                = "sessions.unsubscribe"
	MethodSessionsMessagesSubscribe          = "sessions.messages.subscribe"
	MethodSessionsMessagesUnsubscribe        = "sessions.messages.unsubscribe"
	MethodSessionsDescribe                   = "sessions.describe"
	MethodSessionsCreate                     = "sessions.create"
	MethodSessionsSend                       = "sessions.send"
	MethodSessionsAbort                      = "sessions.abort"
	MethodSessionsPreview                    = "sessions.preview"
	MethodSessionsPatch                      = "sessions.patch"
	MethodSessionsReset                      = "sessions.reset"
	MethodSessionsDelete                     = "sessions.delete"
	MethodSessionsCompact                    = "sessions.compact"
	MethodSessionsFilesList                  = "sessions.files.list"
	MethodSessionsFilesGet                   = "sessions.files.get"
	MethodSessionsFilesSet                   = "sessions.files.set"
	MethodSessionsFilesReveal                = "sessions.files.reveal"
	MethodSessionsCatalogList                = "sessions.catalog.list"
	MethodSessionsCatalogRead                = "sessions.catalog.read"
	MethodSessionsCatalogContinue            = "sessions.catalog.continue"
	MethodSessionsCatalogArchive             = "sessions.catalog.archive"
	MethodSessionsCompactionList             = "sessions.compaction.list"
	MethodSessionsCompactionGet              = "sessions.compaction.get"
	MethodSessionsCompactionBranch           = "sessions.compaction.branch"
	MethodSessionsCompactionRestore          = "sessions.compaction.restore"
	MethodSessionsBranchesList               = "sessions.branches.list"
	MethodSessionsBranchesSwitch             = "sessions.branches.switch"
	MethodSessionsRewind                     = "sessions.rewind"
	MethodSessionsFork                       = "sessions.fork"
	MethodSessionsSearch                     = "sessions.search"
	MethodSessionsDispatch                   = "sessions.dispatch"
	MethodSessionsReclaim                    = "sessions.reclaim"
	MethodSessionsGroupsList                 = "sessions.groups.list"
	MethodSessionsGroupsPut                  = "sessions.groups.put"
	MethodSessionsGroupsRename               = "sessions.groups.rename"
	MethodSessionsGroupsDelete               = "sessions.groups.delete"
	MethodSessionsSpawn                      = "sessions.spawn"
	MethodSessionVisibilitySet               = "session.visibility.set"
	MethodSessionMembersList                 = "session.members.list"
	MethodSessionMembersAdd                  = "session.members.add"
	MethodSessionMembersRemove               = "session.members.remove"
	MethodSessionsObserverVisibility         = "sessions.observer.visibility"
	MethodSessionSuggestionsAdd              = "session.suggestions.add"
	MethodSessionSuggestionsList             = "session.suggestions.list"
	MethodSessionSuggestionsResolve          = "session.suggestions.resolve"
	MethodSessionTyping                      = "session.typing"
	MethodSessionDiscussionInfo              = "session.discussion.info"
	MethodSessionDiscussionOpen              = "session.discussion.open"
	MethodSessionsObserverAsk                = "sessions.observer.ask"
	MethodSessionsExport                     = "sessions.export"
	MethodSessionsPrune                      = "sessions.prune"
	MethodTasksCreate                        = "tasks.create"
	MethodTasksGet                           = "tasks.get"
	MethodTasksList                          = "tasks.list"
	MethodTasksCancel                        = "tasks.cancel"
	MethodTasksResume                        = "tasks.resume"
	MethodTasksDoctor                        = "tasks.doctor"
	MethodTasksSummary                       = "tasks.summary"
	MethodTasksAuditExport                   = "tasks.audit_export"
	MethodTasksTrace                         = "tasks.trace"
	MethodListGet                            = "list.get"
	MethodListPut                            = "list.put"
	MethodRelayPolicyGet                     = "relay.policy.get"
	MethodConfigGet                          = "config.get"
	MethodConfigPut                          = "config.put"
	MethodConfigSet                          = "config.set"
	MethodConfigApply                        = "config.apply"
	MethodConfigPatch                        = "config.patch"
	MethodConfigSchema                       = "config.schema"
	MethodConfigSchemaLookup                 = "config.schema.lookup"
	MethodSecurityAudit                      = "security.audit"
	MethodACPRegister                        = "acp.register"
	MethodACPUnregister                      = "acp.unregister"
	MethodACPPeers                           = "acp.peers"
	MethodACPDispatch                        = "acp.dispatch"
	MethodACPPipeline                        = "acp.pipeline"
	MethodACPSessionInit                     = "acp.session.init"
	MethodACPSessionRun                      = "acp.session.run"
	MethodACPSessionSpawn                    = "acp.session.spawn"
	MethodACPSessionCancel                   = "acp.session.cancel"
	MethodACPSessionClose                    = "acp.session.close"
	MethodACPSessionStatus                   = "acp.session.status"
	MethodACPManagerStatus                   = "acp.manager.status"
	MethodSoulFactoryProvision               = "soulfactory.provision"
	MethodSoulFactoryUpdate                  = "soulfactory.update"
	MethodSoulFactorySuspend                 = "soulfactory.suspend"
	MethodSoulFactoryResume                  = "soulfactory.resume"
	MethodSoulFactoryRedeploy                = "soulfactory.redeploy"
	MethodSoulFactoryRevoke                  = "soulfactory.revoke"
	MethodSoulFactoryAvatarGenerate          = "soulfactory.avatar.generate"
	MethodSoulFactoryAvatarSet               = "soulfactory.avatar.set"
	MethodSoulFactoryVoiceConfigure          = "soulfactory.voice.configure"
	MethodSoulFactoryVoiceSample             = "soulfactory.voice.sample"
	MethodSoulFactoryMemoryConfigure         = "soulfactory.memory.configure"
	MethodSoulFactoryMemoryReindex           = "soulfactory.memory.reindex"
	MethodSoulFactoryPersonaUpdate           = "soulfactory.persona.update"
	MethodSoulFactoryConfigReload            = "soulfactory.config.reload"
	MethodAgentsList                         = "agents.list"
	MethodAgentsCreate                       = "agents.create"
	MethodAgentsUpdate                       = "agents.update"
	MethodAgentsDelete                       = "agents.delete"
	MethodAgentsAssign                       = "agents.assign"
	MethodAgentsUnassign                     = "agents.unassign"
	MethodAgentsActive                       = "agents.active"
	MethodAgentsFilesList                    = "agents.files.list"
	MethodAgentsFilesGet                     = "agents.files.get"
	MethodAgentsFilesSet                     = "agents.files.set"
	MethodModelsList                         = "models.list"
	MethodToolsCatalog                       = "tools.catalog"
	MethodToolsProfileGet                    = "tools.profile.get"
	MethodToolsProfileSet                    = "tools.profile.set"
	MethodSkillsStatus                       = "skills.status"
	MethodSkillsBins                         = "skills.bins"
	MethodSkillsInstall                      = "skills.install"
	MethodSkillsUpdate                       = "skills.update"
	MethodSkillsSearch                       = "skills.search"
	MethodSkillsDetail                       = "skills.detail"
	MethodSkillsSecurityVerdicts             = "skills.securityVerdicts"
	MethodSkillsSkillCard                    = "skills.skillCard"
	MethodSkillsUploadBegin                  = "skills.upload.begin"
	MethodSkillsUploadChunk                  = "skills.upload.chunk"
	MethodSkillsUploadCommit                 = "skills.upload.commit"

	// skills.curator.* — curator lifecycle (WS-G, swarmstr-xfny.3).
	MethodSkillsCuratorStatus  = "skills.curator.status"
	MethodSkillsCuratorPin     = "skills.curator.pin"
	MethodSkillsCuratorUnpin   = "skills.curator.unpin"
	MethodSkillsCuratorRestore = "skills.curator.restore"

	// skills.proposals.* — skill-workshop proposals core (WS-G, swarmstr-xfny.4).
	// NOTE: historyStatus/historyScan/requestRevision (swarmstr-xfny.5) are
	// intentionally deferred; see that issue.
	MethodSkillsProposalsList       = "skills.proposals.list"
	MethodSkillsProposalsInspect    = "skills.proposals.inspect"
	MethodSkillsProposalsCreate     = "skills.proposals.create"
	MethodSkillsProposalsUpdate     = "skills.proposals.update"
	MethodSkillsProposalsRevise     = "skills.proposals.revise"
	MethodSkillsProposalsApply      = "skills.proposals.apply"
	MethodSkillsProposalsReject     = "skills.proposals.reject"
	MethodSkillsProposalsQuarantine = "skills.proposals.quarantine"
	MethodPluginsInstall            = "plugins.install"
	MethodPluginsUninstall          = "plugins.uninstall"
	MethodPluginsUpdate             = "plugins.update"
	MethodPluginsRegistryList       = "plugins.registry.list"
	MethodPluginsRegistryGet        = "plugins.registry.get"
	MethodPluginsRegistrySearch     = "plugins.registry.search"
	// Plugin-surface long tail (swarmstr-zzin, WS-G). Control-RPC tooling
	// surface, mirroring the skills discovery wiring.
	MethodPluginsList       = "plugins.list"
	MethodPluginsSearch     = "plugins.search"
	MethodPluginsSetEnabled = "plugins.setEnabled"
	MethodPluginsRefresh    = "plugins.refresh"
	// Plugin UI-surface contribution model (swarmstr-qmxu): aggregate plugin
	// board-widget UI descriptors, dispatch a plugin session-action verb, and
	// re-scan/re-aggregate + emit plugin.surface.changed.
	MethodPluginsUIDescriptors       = "plugins.uiDescriptors"
	MethodPluginsSessionAction       = "plugins.sessionAction"
	MethodPluginSurfaceRefresh       = "plugin.surface.refresh"
	MethodPluginApprovalList         = "plugin.approval.list"
	MethodPluginApprovalRequest      = "plugin.approval.request"
	MethodPluginApprovalWaitDecision = "plugin.approval.waitDecision"
	MethodPluginApprovalResolve      = "plugin.approval.resolve"
	// Users durable-profile surface (swarmstr-5lln). Metiq deviation
	// (nostr-user-identity accepted-deviation): profiles are keyed by nostr
	// identity rather than OpenClaw's email-primary accounts, with optional
	// email aliases + display name + avatar. Control-RPC surface only.
	MethodUsersList           = "users.list"
	MethodUsersSelf           = "users.self"
	MethodUsersLinkEmail      = "users.linkEmail"
	MethodUsersSetDisplayName = "users.setDisplayName"
	MethodUsersSetAvatar      = "users.setAvatar"
	// Gateway lifecycle (swarmstr-iiot). restart.preflight reports restart
	// readiness (in-flight agent runs + active sessions); restart.request
	// triggers the real restart scheduler (restartCh). gateway.suspend.* is
	// intentionally NOT registered — the daemon has no cooperative suspend/
	// resume machinery, so it stays an honest UNAVAILABLE gap (follow-up
	// swarmstr issue); the shared `gateway` triage prefix is locked to the
	// core-runtime/implement category so per-method accepted-deviation is not
	// expressible in the parity matrix.
	MethodGatewayRestartPreflight = "gateway.restart.preflight"
	MethodGatewayRestartRequest   = "gateway.restart.request"
	// Chat control-UI surface (swarmstr-viqq). Backed by the existing
	// docs/transcript session subsystem. startup returns bootstrap state;
	// metadata returns per-session chat metadata; message.get fetches one
	// transcript entry by id; toolTitles returns deterministic tool-call
	// display titles.
	MethodChatStartup                 = "chat.startup"
	MethodChatMetadata                = "chat.metadata"
	MethodChatMessageGet              = "chat.message.get"
	MethodChatToolTitles              = "chat.toolTitles"
	MethodNodePairRequest             = "node.pair.request"
	MethodNodePairList                = "node.pair.list"
	MethodNodePairApprove             = "node.pair.approve"
	MethodNodePairReject              = "node.pair.reject"
	MethodNodePairRemove              = "node.pair.remove"
	MethodNodePairVerify              = "node.pair.verify"
	MethodDevicePairList              = "device.pair.list"
	MethodDevicePairApprove           = "device.pair.approve"
	MethodDevicePairReject            = "device.pair.reject"
	MethodDevicePairRemove            = "device.pair.remove"
	MethodDevicePairRename            = "device.pair.rename"
	MethodDeviceTokenRotate           = "device.token.rotate"
	MethodDeviceTokenRevoke           = "device.token.revoke"
	MethodNodeList                    = "node.list"
	MethodNodeDescribe                = "node.describe"
	MethodNodeRename                  = "node.rename"
	MethodNodeInvoke                  = "node.invoke"
	MethodNodeInvokeProgress          = "node.invoke.progress"
	MethodNodeInvokeResult            = "node.invoke.result"
	MethodNodeEvent                   = "node.event"
	MethodNodeResult                  = "node.result"
	MethodNodePendingEnqueue          = "node.pending.enqueue"
	MethodNodePendingPull             = "node.pending.pull"
	MethodNodePendingAck              = "node.pending.ack"
	MethodNodePendingDrain            = "node.pending.drain"
	MethodNodeCanvasCapabilityRefresh = "node.canvas.capability.refresh"

	MethodCanvasGet    = "canvas.get"
	MethodCanvasList   = "canvas.list"
	MethodCanvasUpdate = "canvas.update"
	MethodCanvasDelete = "canvas.delete"

	MethodCronGet                  = "cron.get"
	MethodCronList                 = "cron.list"
	MethodCronStatus               = "cron.status"
	MethodCronScratchGet           = "cron.scratch.get"
	MethodCronScratchSet           = "cron.scratch.set"
	MethodCronAdd                  = "cron.add"
	MethodCronUpdate               = "cron.update"
	MethodCronRemove               = "cron.remove"
	MethodCronRun                  = "cron.run"
	MethodCronRuns                 = "cron.runs"
	MethodExecApprovalsGet         = "exec.approvals.get"
	MethodExecApprovalsSet         = "exec.approvals.set"
	MethodExecApprovalsNodeGet     = "exec.approvals.node.get"
	MethodExecApprovalsNodeSet     = "exec.approvals.node.set"
	MethodExecApprovalGet          = "exec.approval.get"
	MethodExecApprovalList         = "exec.approval.list"
	MethodExecApprovalRequest      = "exec.approval.request"
	MethodExecApprovalWaitDecision = "exec.approval.waitDecision"
	MethodExecApprovalResolve      = "exec.approval.resolve"
	MethodApprovalGet              = "approval.get"
	MethodApprovalList             = "approval.list"
	MethodApprovalResolve          = "approval.resolve"
	MethodMCPList                  = "mcp.list"
	MethodMCPGet                   = "mcp.get"
	MethodMCPPut                   = "mcp.put"
	MethodMCPRemove                = "mcp.remove"
	MethodMCPTest                  = "mcp.test"
	MethodMCPReconnect             = "mcp.reconnect"
	MethodMCPAuthStart             = "mcp.auth.start"
	MethodMCPAuthRefresh           = "mcp.auth.refresh"
	MethodMCPAuthClear             = "mcp.auth.clear"
	MethodSecretsReload            = "secrets.reload"
	MethodSandboxRun               = "sandbox.run"
	MethodSecretsResolve           = "secrets.resolve"
	MethodWizardStart              = "wizard.start"
	MethodWizardNext               = "wizard.next"
	MethodWizardCancel             = "wizard.cancel"
	MethodWizardStatus             = "wizard.status"
	MethodUpdateRun                = "update.run"
	MethodTalkConfig               = "talk.config"
	MethodTalkMode                 = "talk.mode"
	MethodGatewayIdentityGet       = "gateway.identity.get"
	MethodLastHeartbeat            = "last-heartbeat"
	MethodSetHeartbeats            = "set-heartbeats"
	MethodWake                     = "wake"
	MethodSystemPresence           = "system-presence"
	MethodSystemEvent              = "system-event"
	MethodSend                     = "send"
	MethodPoll                     = "poll"
	MethodBrowserRequest           = "browser.request"
	MethodVoicewakeGet             = "voicewake.get"
	MethodVoicewakeSet             = "voicewake.set"
	MethodTTSStatus                = "tts.status"
	MethodTTSProviders             = "tts.providers"
	MethodTTSSetProvider           = "tts.setProvider"
	MethodTTSEnable                = "tts.enable"
	MethodTTSDisable               = "tts.disable"
	MethodTTSConvert               = "tts.convert"

	// Voice/talk long tail (swarmstr-0tfj). Served over the control-RPC talk
	// surface (handleTalkRPC).
	MethodTTSPersonas   = "tts.personas"
	MethodTTSSetPersona = "tts.setPersona"
	MethodTalkCatalog   = "talk.catalog"
	MethodTalkSpeak     = "talk.speak"
	// tts.speak is a compat alias for talk.speak (openclaw naming): identical
	// synthesis via the live tts manager with persona/voice-alias overrides.
	MethodTTSSpeak                    = "tts.speak"
	MethodVoicewakeRoutingGet         = "voicewake.routing.get"
	MethodVoicewakeRoutingSet         = "voicewake.routing.set"
	MethodTalkSessionCreate           = "talk.session.create"
	MethodTalkSessionJoin             = "talk.session.join"
	MethodTalkSessionAppendAudio      = "talk.session.appendAudio"
	MethodTalkSessionStartTurn        = "talk.session.startTurn"
	MethodTalkSessionEndTurn          = "talk.session.endTurn"
	MethodTalkSessionCancelTurn       = "talk.session.cancelTurn"
	MethodTalkSessionCancelOutput     = "talk.session.cancelOutput"
	MethodTalkSessionAcknowledgeMark  = "talk.session.acknowledgeMark"
	MethodTalkSessionSubmitToolResult = "talk.session.submitToolResult"
	MethodTalkSessionSteer            = "talk.session.steer"
	MethodTalkSessionClose            = "talk.session.close"
	MethodTalkClientCreate            = "talk.client.create"
	MethodTalkClientTranscript        = "talk.client.transcript"
	MethodTalkClientClose             = "talk.client.close"
	MethodTalkClientToolCall          = "talk.client.toolCall"
	MethodTalkClientSteer             = "talk.client.steer"

	MethodHooksList                = "hooks.list"
	MethodHooksEnable              = "hooks.enable"
	MethodHooksDisable             = "hooks.disable"
	MethodHooksInfo                = "hooks.info"
	MethodHooksCheck               = "hooks.check"
	MethodTerminalOpen             = "terminal.open"
	MethodTerminalInput            = "terminal.input"
	MethodTerminalResize           = "terminal.resize"
	MethodTerminalClose            = "terminal.close"
	MethodTerminalAttach           = "terminal.attach"
	MethodTerminalList             = "terminal.list"
	MethodTerminalText             = "terminal.text"
	MethodTerminalUpload           = "terminal.upload"
	MethodAttachGrant              = "attach.grant"
	MethodAttachRevoke             = "attach.revoke"
	MethodFSListDir                = "fs.listDir"
	MethodWorktreesList            = "worktrees.list"
	MethodWorktreesBranches        = "worktrees.branches"
	MethodWorktreesCreate          = "worktrees.create"
	MethodWorktreesRemove          = "worktrees.remove"
	MethodWorktreesRestore         = "worktrees.restore"
	MethodWorktreesGc              = "worktrees.gc"
	MethodBoardGet                 = "board.get"
	MethodBoardUpdate              = "board.update"
	MethodBoardWidgetPut           = "board.widget.put"
	MethodBoardWidgetGrant         = "board.widget.grant"
	MethodBoardEvent               = "board.event"
	MethodBoardWidgetAppView       = "board.widget.appView"
	MethodBoardPromptAuthorize     = "board.prompt.authorize"
	MethodBoardDataRead            = "board.data.read"
	MethodBoardAction              = "board.action"
	MethodMcpAppView               = "mcp.app.view"
	MethodMcpAppListTools          = "mcp.app.listTools"
	MethodMcpAppListResources      = "mcp.app.listResources"
	MethodMcpAppListResourceTpls   = "mcp.app.listResourceTemplates"
	MethodMcpAppReadResource       = "mcp.app.readResource"
	MethodMcpAppCallTool           = "mcp.app.callTool"
	MethodMcpAppUpdateModelContext = "mcp.app.updateModelContext"
	MethodConversationsList        = "conversations.list"
	MethodConversationsSend        = "conversations.send"
	MethodConversationsTurn        = "conversations.turn"
	MethodConversationsTurnCancel  = "conversations.turn.cancel"
	MethodArtifactsList            = "artifacts.list"
	MethodArtifactsGet             = "artifacts.get"
	MethodArtifactsDownload        = "artifacts.download"
	MethodEnvironmentsList         = "environments.list"
	MethodEnvironmentsStatus       = "environments.status"
	MethodEnvironmentsCreate       = "environments.create"
	MethodEnvironmentsDestroy      = "environments.destroy"
	MethodQuestionRequest          = "question.request"
	MethodQuestionWaitAnswer       = "question.waitAnswer"
	MethodQuestionResolve          = "question.resolve"
	MethodQuestionGet              = "question.get"
	MethodQuestionList             = "question.list"
	MethodTaskSuggestionsList      = "taskSuggestions.list"
	MethodTaskSuggestionsCreate    = "taskSuggestions.create"
	MethodTaskSuggestionsAccept    = "taskSuggestions.accept"
	MethodTaskSuggestionsDismiss   = "taskSuggestions.dismiss"
)

// Gateway operational long tail (swarmstr-kmhu): three buckets of OpenClaw
// parity methods backed by existing swarmstr subsystems.
const (
	// models.* provider-auth surface (BUCKET 1). Backed by the provider/model
	// layer (cfg.Providers + env credentials + the OAuth adapters in
	// internal/agent). authStatus/probe are reads; authLogout clears stored
	// provider credentials.
	MethodModelsAuthStatus = "models.authStatus"
	MethodModelsAuthLogout = "models.authLogout"
	MethodModelsProbe      = "models.probe"

	// sessions.* operational long tail (BUCKET 2). Backed by the session
	// subsystem (docs/transcript repos + the durable compaction-checkpoint DAG).
	// pluginPatch applies a plugin-namespaced mutation; cleanup GCs terminal/
	// stale sessions; diff compares two durable snapshots (compaction
	// checkpoints).
	MethodSessionsPluginPatch = "sessions.pluginPatch"
	MethodSessionsCleanup     = "sessions.cleanup"
	MethodSessionsDiff        = "sessions.diff"

	// node.* plugin/skills surface (BUCKET 3). Node-scoped refresh/update ops
	// delivered to a paired+active node over the durable node pending-command
	// queue (the same delivery channel as node.invoke/node.pending.enqueue); the
	// node applies them on pull.
	MethodNodePluginSurfaceRefresh = "node.pluginSurface.refresh"
	MethodNodePluginToolsUpdate    = "node.pluginTools.update"
	MethodNodeSkillsUpdate         = "node.skills.update"
)

// Gateway introspection long tail (swarmstr-wapc): mostly-read operator
// introspection methods backed by existing swarmstr subsystems. Each is wired
// through handleIntrospectionRPC (cmd/metiqd/control_rpc_introspection.go).
const (
	// system.info — daemon identity/health snapshot (version+commit, platform,
	// uptime, pid, capabilities). Backed by the build-info globals + runtime +
	// the live capability signals already assembled for status.
	MethodSystemInfo = "system.info"

	// diagnostics.stability — process stability/health snapshot (uptime,
	// goroutines, cpu, memory, GC, crash/restart recovery outcomes, in-flight
	// runs). Backed by the Go runtime + the boot recovery snapshot + agentJobs.
	MethodDiagnosticsStability = "diagnostics.stability"

	// commands.list — operator/slash command registry. Backed by the embedded
	// Web UI command catalog (internal/webui) advertised to clients.
	MethodCommandsList = "commands.list"

	// update.status — self-update status (current version + available update).
	// Read-only view over the real update.Checker (never forces a network
	// fetch); reports the cached release check when one exists.
	MethodUpdateStatus = "update.status"

	// tools.effective — the effective tool set/policy for an agent: the tool
	// catalog filtered by the agent's active profile, overlaid (when the WS-G
	// permission engine is configured) with the per-tool permission behavior.
	MethodToolsEffective = "tools.effective"

	// tools.invoke — invoke a single builtin tool by name through the gateway
	// (operator-driven). Backed by the live agent.ToolRegistry.Execute path,
	// which enforces schema validation, semantic validation and tool policy.
	MethodToolsInvoke = "tools.invoke"

	// audit.list — list permission-engine audit-log entries (filters:
	// time/type/tool/limit). Backed by the WS-G permission engine's durable
	// Auditor (internal/permissions).
	MethodAuditList = "audit.list"

	// audit.activity.list — agent/session activity feed projected from the same
	// permission-engine audit log (decision/override/escalation events carry the
	// agent + session that acted).
	MethodAuditActivityList = "audit.activity.list"

	// agents.workspace.list — list workspace-scoped agents (the durable agent
	// docs surface, optionally filtered to one workspace).
	MethodAgentsWorkspaceList = "agents.workspace.list"

	// agents.workspace.get — one workspace agent's detail (durable agent doc).
	MethodAgentsWorkspaceGet = "agents.workspace.get"

	// approval.history — historical (resolved) approvals from the durable
	// exec/unified approval ledger; the closed-records counterpart of
	// approval.list (which defaults to pending).
	MethodApprovalHistory = "approval.history"

	// ui.command — execute a UI-originated named command by dispatching the
	// gateway method the embedded Web UI would invoke for that slash command.
	MethodUICommand = "ui.command"
)

// OpenClaw-branded control-surface compat aliases (swarmstr-i413). These are
// OpenClaw's own names for data-plane functionality Metiq already implements
// under its native method names. Each is a thin alias that re-dispatches to the
// native handler (Internal=true) and returns the native, OpenClaw-modeled
// response shape — real functionality, no fabricated stub.
//
// The five openclaw.setup.* onboarding/activation methods (setup.detect /
// activate / auth.start / prepare.start / verify) are NOT defined here: they
// onboard/activate an OpenClaw application install, which has no meaningful
// equivalent for a nostr-key-native daemon that is not OpenClaw. They remain an
// accepted deviation (honest UNAVAILABLE, unregistered) — see the parity matrix
// notes and follow-up swarmstr-nuqy.
const (
	// openclaw.chat — OpenClaw's Control-UI chat send. Alias of chat.send: posts
	// a message to a peer over the native DM transport.
	MethodOpenclawChat = "openclaw.chat"

	// openclaw.chat.history — OpenClaw's Control-UI transcript history. Alias of
	// chat.history: the durable docs/transcript window for a session.
	MethodOpenclawChatHistory = "openclaw.chat.history"

	// openclaw.changes.list — OpenClaw's Control-UI "changes" review surface.
	// Alias of sessions.files.list: the session's touched/changed workspace
	// files (files the session's transcript recorded as written/edited).
	MethodOpenclawChangesList = "openclaw.changes.list"

	// openclaw.approval.list — OpenClaw's Control-UI approval queue. Alias of
	// approval.list: pending/resolved records from the durable approval ledger.
	MethodOpenclawApprovalList = "openclaw.approval.list"
)
