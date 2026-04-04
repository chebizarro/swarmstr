package methods

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"metiq/internal/memory"
	"metiq/internal/store/state"
)

// ErrConfigConflict is returned when a config mutation is rejected because
// the caller's base_hash does not match the server's current config hash.
// This implements optimistic concurrency control for config updates.
var ErrConfigConflict = errors.New("config conflict: base_hash mismatch")

// CheckBaseHash validates that baseHash (from the request) matches the hash
// of current.  If baseHash is empty, the check is skipped.  Returns
// ErrConfigConflict if the hashes differ.
func CheckBaseHash(current state.ConfigDoc, baseHash string) error {
	baseHash = strings.TrimSpace(baseHash)
	if baseHash == "" {
		return nil
	}
	got := current.Hash()
	if got != baseHash {
		return fmt.Errorf("%w: have %s, client sent %s", ErrConfigConflict, got, baseHash)
	}
	return nil
}

const (
	MethodSupportedMethods            = "supportedmethods"
	MethodHealth                      = "health"
	MethodDoctorMemoryStatus          = "doctor.memory.status"
	MethodLogsTail                    = "logs.tail"
	MethodRuntimeObserve              = "runtime.observe"
	MethodChannelsStatus              = "channels.status"
	MethodChannelsLogout              = "channels.logout"
	MethodChannelsJoin                = "channels.join"
	MethodChannelsLeave               = "channels.leave"
	MethodChannelsList                = "channels.list"
	MethodChannelsSend                = "channels.send"
	MethodStatus                      = "status.get"
	MethodStatusAlias                 = "status"
	MethodUsageStatus                 = "usage.status"
	MethodUsageCost                   = "usage.cost"
	MethodMemorySearch                = "memory.search"
	MethodMemoryCompact               = "memory.compact"
	MethodAgent                       = "agent"
	MethodAgentWait                   = "agent.wait"
	MethodAgentIdentityGet            = "agent.identity.get"
	MethodChatSend                    = "chat.send"
	MethodChatHistory                 = "chat.history"
	MethodChatAbort                   = "chat.abort"
	MethodSessionGet                  = "session.get"
	MethodSessionsList                = "sessions.list"
	MethodSessionsPreview             = "sessions.preview"
	MethodSessionsPatch               = "sessions.patch"
	MethodSessionsReset               = "sessions.reset"
	MethodSessionsDelete              = "sessions.delete"
	MethodSessionsCompact             = "sessions.compact"
	MethodSessionsSpawn               = "sessions.spawn"
	MethodSessionsExport              = "sessions.export"
	MethodSessionsPrune               = "sessions.prune"
	MethodListGet                     = "list.get"
	MethodListPut                     = "list.put"
	MethodRelayPolicyGet              = "relay.policy.get"
	MethodConfigGet                   = "config.get"
	MethodConfigPut                   = "config.put"
	MethodConfigSet                   = "config.set"
	MethodConfigApply                 = "config.apply"
	MethodConfigPatch                 = "config.patch"
	MethodConfigSchema                = "config.schema"
	MethodConfigSchemaLookup          = "config.schema.lookup"
	MethodSecurityAudit               = "security.audit"
	MethodACPRegister                 = "acp.register"
	MethodACPUnregister               = "acp.unregister"
	MethodACPPeers                    = "acp.peers"
	MethodACPDispatch                 = "acp.dispatch"
	MethodACPPipeline                 = "acp.pipeline"
	MethodAgentsList                  = "agents.list"
	MethodAgentsCreate                = "agents.create"
	MethodAgentsUpdate                = "agents.update"
	MethodAgentsDelete                = "agents.delete"
	MethodAgentsAssign                = "agents.assign"
	MethodAgentsUnassign              = "agents.unassign"
	MethodAgentsActive                = "agents.active"
	MethodAgentsFilesList             = "agents.files.list"
	MethodAgentsFilesGet              = "agents.files.get"
	MethodAgentsFilesSet              = "agents.files.set"
	MethodModelsList                  = "models.list"
	MethodToolsCatalog                = "tools.catalog"
	MethodToolsProfileGet             = "tools.profile.get"
	MethodToolsProfileSet             = "tools.profile.set"
	MethodSkillsStatus                = "skills.status"
	MethodSkillsBins                  = "skills.bins"
	MethodSkillsInstall               = "skills.install"
	MethodSkillsUpdate                = "skills.update"
	MethodPluginsInstall              = "plugins.install"
	MethodPluginsUninstall            = "plugins.uninstall"
	MethodPluginsUpdate               = "plugins.update"
	MethodPluginsRegistryList         = "plugins.registry.list"
	MethodPluginsRegistryGet          = "plugins.registry.get"
	MethodPluginsRegistrySearch       = "plugins.registry.search"
	MethodNodePairRequest             = "node.pair.request"
	MethodNodePairList                = "node.pair.list"
	MethodNodePairApprove             = "node.pair.approve"
	MethodNodePairReject              = "node.pair.reject"
	MethodNodePairVerify              = "node.pair.verify"
	MethodDevicePairList              = "device.pair.list"
	MethodDevicePairApprove           = "device.pair.approve"
	MethodDevicePairReject            = "device.pair.reject"
	MethodDevicePairRemove            = "device.pair.remove"
	MethodDeviceTokenRotate           = "device.token.rotate"
	MethodDeviceTokenRevoke           = "device.token.revoke"
	MethodNodeList                    = "node.list"
	MethodNodeDescribe                = "node.describe"
	MethodNodeRename                  = "node.rename"
	MethodNodeInvoke                  = "node.invoke"
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

	MethodCronList                 = "cron.list"
	MethodCronStatus               = "cron.status"
	MethodCronAdd                  = "cron.add"
	MethodCronUpdate               = "cron.update"
	MethodCronRemove               = "cron.remove"
	MethodCronRun                  = "cron.run"
	MethodCronRuns                 = "cron.runs"
	MethodExecApprovalsGet         = "exec.approvals.get"
	MethodExecApprovalsSet         = "exec.approvals.set"
	MethodExecApprovalsNodeGet     = "exec.approvals.node.get"
	MethodExecApprovalsNodeSet     = "exec.approvals.node.set"
	MethodExecApprovalRequest      = "exec.approval.request"
	MethodExecApprovalWaitDecision = "exec.approval.waitDecision"
	MethodExecApprovalResolve      = "exec.approval.resolve"
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
	MethodBrowserRequest           = "browser.request"
	MethodVoicewakeGet             = "voicewake.get"
	MethodVoicewakeSet             = "voicewake.set"
	MethodTTSStatus                = "tts.status"
	MethodTTSProviders             = "tts.providers"
	MethodTTSSetProvider           = "tts.setProvider"
	MethodTTSEnable                = "tts.enable"
	MethodTTSDisable               = "tts.disable"
	MethodTTSConvert               = "tts.convert"
	MethodHooksList                = "hooks.list"
	MethodHooksEnable              = "hooks.enable"
	MethodHooksDisable             = "hooks.disable"
	MethodHooksInfo                = "hooks.info"
	MethodHooksCheck               = "hooks.check"
)

type CallRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type CallResponse struct {
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type StatusResponse struct {
	PubKey        string   `json:"pubkey"`
	Relays        []string `json:"relays"`
	DMPolicy      string   `json:"dm_policy"`
	UptimeSeconds int      `json:"uptime_seconds"`
	UptimeMS      int64    `json:"uptime_ms"`
	Version       string   `json:"version"`

	// Subscriptions reports health snapshots for long-lived subscriptions.
	// Omitted when empty (e.g. during early startup).
	Subscriptions []SubHealthInfo `json:"subscriptions,omitempty"`

	// RelaySets reports current NIP-51 kind:30002 relay sets.
	// Omitted when no relay sets are loaded.
	RelaySets map[string][]string `json:"relay_sets,omitempty"`
}

// SubHealthInfo is the JSON-friendly representation of a subscription health
// snapshot, suitable for the status.get response and /status slash command.
type SubHealthInfo struct {
	Label            string   `json:"label"`
	BoundRelays      []string `json:"bound_relays"`
	LastEventAt      int64    `json:"last_event_at,omitempty"`
	LastReconnectAt  int64    `json:"last_reconnect_at,omitempty"`
	LastClosedReason string   `json:"last_closed_reason,omitempty"`
	ReplayWindowMS   int64    `json:"replay_window_ms"`
	EventCount       int64    `json:"event_count"`
	ReconnectCount   int64    `json:"reconnect_count"`
}

type MemorySearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type MemorySearchResponse struct {
	Results []memory.IndexedMemory `json:"results"`
}

// MemoryCompactRequest asks the context engine to compact the given session.
// If SessionID is empty, all sessions are compacted.
type MemoryCompactRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

// MemoryCompactResponse reports the result of a compaction operation.
type MemoryCompactResponse struct {
	OK           bool   `json:"ok"`
	SessionsRun  int    `json:"sessions_run"`
	TokensBefore int    `json:"tokens_before,omitempty"`
	TokensAfter  int    `json:"tokens_after,omitempty"`
	Summary      string `json:"summary,omitempty"`
}

type AgentRequest struct {
	SessionID   string                 `json:"session_id,omitempty"`
	Message     string                 `json:"message"`
	Context     string                 `json:"context,omitempty"`
	MemoryScope state.AgentMemoryScope `json:"memory_scope,omitempty"`
	TimeoutMS   int                    `json:"timeout_ms,omitempty"`
}

// ── ACP (Agent Control Protocol) request/response types ─────────────────────

// ACPRegisterRequest registers a remote agent peer by Nostr pubkey.
type ACPRegisterRequest struct {
	// PubKey is the Nostr pubkey (hex) of the remote agent.
	PubKey string `json:"pubkey"`
	// Alias is a human-friendly label for the peer.
	Alias string `json:"alias,omitempty"`
	// Tags holds arbitrary key-value metadata for this peer.
	Tags map[string]string `json:"tags,omitempty"`
}

// ACPUnregisterRequest removes a remote agent peer.
type ACPUnregisterRequest struct {
	PubKey string `json:"pubkey"`
}

type ACPParentContextHint struct {
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

// ACPDispatchRequest sends an ACP task to a registered peer.
type ACPDispatchRequest struct {
	// TargetPubKey is the Nostr pubkey of the destination agent.
	TargetPubKey string `json:"target_pubkey"`
	// Instructions is the task description.
	Instructions string `json:"instructions"`
	// ContextMessages seeds the worker with prior parent history/context.
	ContextMessages []map[string]any `json:"context_messages,omitempty"`
	// MemoryScope carries the explicit worker memory scope contract.
	MemoryScope state.AgentMemoryScope `json:"memory_scope,omitempty"`
	// ToolProfile carries the inherited worker tool profile contract.
	ToolProfile string `json:"tool_profile,omitempty"`
	// EnabledTools carries an explicit inherited tool allowlist.
	EnabledTools []string `json:"enabled_tools,omitempty"`
	// ParentContext carries optional metadata about the originating runtime.
	ParentContext *ACPParentContextHint `json:"parent_context,omitempty"`
	// TimeoutMS, when > 0, limits the round-trip wait in milliseconds.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
	// Wait, when true, blocks until the worker sends a result DM and returns
	// the result text.  When false (default), returns immediately with the task_id.
	Wait bool `json:"wait,omitempty"`
}

// ACPPipelineStepRequest is a single step in an ACP pipeline.
type ACPPipelineStepRequest struct {
	// PeerPubKey is the Nostr pubkey of the target worker agent.
	PeerPubKey string `json:"peer_pubkey"`
	// Instructions is the task text for this step.
	Instructions string `json:"instructions"`
	// ContextMessages seeds the worker with prior parent history/context.
	ContextMessages []map[string]any `json:"context_messages,omitempty"`
	// MemoryScope carries the explicit worker memory scope contract.
	MemoryScope state.AgentMemoryScope `json:"memory_scope,omitempty"`
	// ToolProfile carries the inherited worker tool profile contract.
	ToolProfile string `json:"tool_profile,omitempty"`
	// EnabledTools carries an explicit inherited tool allowlist.
	EnabledTools []string `json:"enabled_tools,omitempty"`
	// ParentContext carries optional metadata about the originating runtime.
	ParentContext *ACPParentContextHint `json:"parent_context,omitempty"`
	// TimeoutMS is the per-step timeout.  0 = 60 s default.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

// ACPPipelineRequest orchestrates a multi-step ACP workflow.
type ACPPipelineRequest struct {
	// Steps are the pipeline stages in execution order.
	Steps []ACPPipelineStepRequest `json:"steps"`
	// Parallel, when true, dispatches all steps concurrently.
	// When false (default), steps run sequentially and each step receives
	// the previous step's result as context.
	Parallel bool `json:"parallel,omitempty"`
}

func normalizeACPParentContext(parent *ACPParentContextHint) *ACPParentContextHint {
	if parent == nil {
		return nil
	}
	out := &ACPParentContextHint{
		SessionID: strings.TrimSpace(parent.SessionID),
		AgentID:   strings.TrimSpace(parent.AgentID),
	}
	if out.SessionID == "" && out.AgentID == "" {
		return nil
	}
	return out
}

func normalizeACPEnabledToolList(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
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
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r ACPDispatchRequest) Normalize() (ACPDispatchRequest, error) {
	r.TargetPubKey = strings.TrimSpace(r.TargetPubKey)
	r.Instructions = strings.TrimSpace(r.Instructions)
	r.ToolProfile = strings.TrimSpace(r.ToolProfile)
	r.EnabledTools = normalizeACPEnabledToolList(r.EnabledTools)
	r.ParentContext = normalizeACPParentContext(r.ParentContext)
	r.ContextMessages = compactObjectSlice(r.ContextMessages)
	if raw := strings.TrimSpace(string(r.MemoryScope)); raw != "" {
		scope, ok := state.ParseAgentMemoryScope(raw)
		if !ok {
			return r, fmt.Errorf("memory_scope must be one of: user, project, local")
		}
		r.MemoryScope = scope
	}
	if r.TargetPubKey == "" {
		return r, fmt.Errorf("target_pubkey required")
	}
	if r.Instructions == "" {
		return r, fmt.Errorf("instructions required")
	}
	if r.TimeoutMS < 0 {
		r.TimeoutMS = 0
	}
	return r, nil
}

func (r ACPPipelineRequest) Normalize() (ACPPipelineRequest, error) {
	if len(r.Steps) == 0 {
		return r, fmt.Errorf("steps required")
	}
	for i := range r.Steps {
		r.Steps[i].PeerPubKey = strings.TrimSpace(r.Steps[i].PeerPubKey)
		r.Steps[i].Instructions = strings.TrimSpace(r.Steps[i].Instructions)
		r.Steps[i].ToolProfile = strings.TrimSpace(r.Steps[i].ToolProfile)
		r.Steps[i].EnabledTools = normalizeACPEnabledToolList(r.Steps[i].EnabledTools)
		r.Steps[i].ParentContext = normalizeACPParentContext(r.Steps[i].ParentContext)
		r.Steps[i].ContextMessages = compactObjectSlice(r.Steps[i].ContextMessages)
		if raw := strings.TrimSpace(string(r.Steps[i].MemoryScope)); raw != "" {
			scope, ok := state.ParseAgentMemoryScope(raw)
			if !ok {
				return r, fmt.Errorf("steps[%d].memory_scope must be one of: user, project, local", i)
			}
			r.Steps[i].MemoryScope = scope
		}
		if r.Steps[i].PeerPubKey == "" {
			return r, fmt.Errorf("steps[%d].peer_pubkey required", i)
		}
		if r.Steps[i].Instructions == "" {
			return r, fmt.Errorf("steps[%d].instructions required", i)
		}
		if r.Steps[i].TimeoutMS < 0 {
			r.Steps[i].TimeoutMS = 0
		}
	}
	return r, nil
}

func DecodeACPDispatchParams(params json.RawMessage) (ACPDispatchRequest, error) {
	params = normalizeObjectParamAliases(params)
	type acpParentContextCompat struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		AgentID        string `json:"agent_id,omitempty"`
		AgentIDCamel   string `json:"agentId,omitempty"`
	}
	type acpDispatchCompat struct {
		TargetPubKey    string                  `json:"target_pubkey"`
		Instructions    string                  `json:"instructions"`
		ContextMessages []map[string]any        `json:"context_messages,omitempty"`
		MemoryScope     state.AgentMemoryScope  `json:"memory_scope,omitempty"`
		ToolProfile     string                  `json:"tool_profile,omitempty"`
		EnabledTools    []string                `json:"enabled_tools,omitempty"`
		ParentContext   *acpParentContextCompat `json:"parent_context,omitempty"`
		TimeoutMS       int64                   `json:"timeout_ms,omitempty"`
		Wait            bool                    `json:"wait,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat acpDispatchCompat
	if err := dec.Decode(&compat); err != nil {
		return ACPDispatchRequest{}, fmt.Errorf("invalid params")
	}
	req := ACPDispatchRequest{
		TargetPubKey:    compat.TargetPubKey,
		Instructions:    compat.Instructions,
		ContextMessages: compat.ContextMessages,
		MemoryScope:     compat.MemoryScope,
		ToolProfile:     compat.ToolProfile,
		EnabledTools:    compat.EnabledTools,
		TimeoutMS:       compat.TimeoutMS,
		Wait:            compat.Wait,
	}
	if compat.ParentContext != nil {
		req.ParentContext = &ACPParentContextHint{
			SessionID: firstNonEmpty(compat.ParentContext.SessionID, compat.ParentContext.SessionIDCamel),
			AgentID:   firstNonEmpty(compat.ParentContext.AgentID, compat.ParentContext.AgentIDCamel),
		}
	}
	return req, nil
}

func DecodeACPPipelineParams(params json.RawMessage) (ACPPipelineRequest, error) {
	params = normalizeObjectParamAliases(params)
	type acpParentContextCompat struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		AgentID        string `json:"agent_id,omitempty"`
		AgentIDCamel   string `json:"agentId,omitempty"`
	}
	type acpPipelineStepCompat struct {
		PeerPubKey           string                  `json:"peer_pubkey"`
		PeerPubKeyCamel      string                  `json:"peerPubKey,omitempty"`
		Instructions         string                  `json:"instructions"`
		ContextMessages      []map[string]any        `json:"context_messages,omitempty"`
		ContextMessagesCamel []map[string]any        `json:"contextMessages,omitempty"`
		MemoryScope          state.AgentMemoryScope  `json:"memory_scope,omitempty"`
		MemoryScopeCamel     state.AgentMemoryScope  `json:"memoryScope,omitempty"`
		ToolProfile          string                  `json:"tool_profile,omitempty"`
		ToolProfileCamel     string                  `json:"toolProfile,omitempty"`
		EnabledTools         []string                `json:"enabled_tools,omitempty"`
		EnabledToolsCamel    []string                `json:"enabledTools,omitempty"`
		ParentContext        *acpParentContextCompat `json:"parent_context,omitempty"`
		ParentContextCamel   *acpParentContextCompat `json:"parentContext,omitempty"`
		TimeoutMS            int64                   `json:"timeout_ms,omitempty"`
		TimeoutMSCamel       int64                   `json:"timeoutMs,omitempty"`
	}
	type acpPipelineCompat struct {
		Steps    []acpPipelineStepCompat `json:"steps"`
		Parallel bool                    `json:"parallel,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat acpPipelineCompat
	if err := dec.Decode(&compat); err != nil {
		return ACPPipelineRequest{}, fmt.Errorf("invalid params")
	}
	req := ACPPipelineRequest{Parallel: compat.Parallel}
	for _, step := range compat.Steps {
		contextMessages := step.ContextMessages
		if len(contextMessages) == 0 {
			contextMessages = step.ContextMessagesCamel
		}
		enabledTools := step.EnabledTools
		if len(enabledTools) == 0 {
			enabledTools = step.EnabledToolsCamel
		}
		parentContext := step.ParentContext
		if parentContext == nil {
			parentContext = step.ParentContextCamel
		}
		memoryScope := step.MemoryScope
		if memoryScope == "" {
			memoryScope = step.MemoryScopeCamel
		}
		timeoutMS := step.TimeoutMS
		if timeoutMS == 0 {
			timeoutMS = step.TimeoutMSCamel
		}
		next := ACPPipelineStepRequest{
			PeerPubKey:      firstNonEmpty(step.PeerPubKey, step.PeerPubKeyCamel),
			Instructions:    step.Instructions,
			ContextMessages: contextMessages,
			MemoryScope:     memoryScope,
			ToolProfile:     firstNonEmpty(step.ToolProfile, step.ToolProfileCamel),
			EnabledTools:    enabledTools,
			TimeoutMS:       timeoutMS,
		}
		if parentContext != nil {
			next.ParentContext = &ACPParentContextHint{
				SessionID: firstNonEmpty(parentContext.SessionID, parentContext.SessionIDCamel),
				AgentID:   firstNonEmpty(parentContext.AgentID, parentContext.AgentIDCamel),
			}
		}
		req.Steps = append(req.Steps, next)
	}
	return req, nil
}

type AgentWaitRequest struct {
	RunID     string `json:"run_id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type AgentIdentityRequest struct {
	SessionID string `json:"session_id,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

// AttachmentInput represents a media file attached to a chat.send or agent.run request.
// The handler pre-processes attachments before forwarding: audio is transcribed,
// PDFs are text-extracted, and images are resolved to ImageRef for vision providers.
type AttachmentInput struct {
	Type     string `json:"type"`             // "image", "audio", "pdf"
	URL      string `json:"url,omitempty"`    // remote URL (optional)
	Base64   string `json:"base64,omitempty"` // base64-encoded content (optional)
	MimeType string `json:"mime_type,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type ChatSendRequest struct {
	To             string            `json:"to"`
	Text           string            `json:"text"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	RunID          string            `json:"run_id,omitempty"`
	Attachments    []AttachmentInput `json:"attachments,omitempty"`
}

type ChatHistoryRequest struct {
	SessionID string `json:"session_id"`
	Limit     int    `json:"limit,omitempty"`
}

type ChatAbortRequest struct {
	SessionID string `json:"session_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
}

type SessionGetRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type SessionsListRequest struct {
	Limit                int    `json:"limit,omitempty"`
	ActiveMinutes        int    `json:"activeMinutes,omitempty"`
	IncludeGlobal        bool   `json:"includeGlobal,omitempty"`
	IncludeUnknown       bool   `json:"includeUnknown,omitempty"`
	IncludeDerivedTitles bool   `json:"includeDerivedTitles,omitempty"`
	IncludeLastMessage   bool   `json:"includeLastMessage,omitempty"`
	Label                string `json:"label,omitempty"`
	SpawnedBy            string `json:"spawnedBy,omitempty"`
	AgentID              string `json:"agentId,omitempty"`
	AgentIDSnake         string `json:"agent_id,omitempty"`
	Search               string `json:"search,omitempty"`
}

type SessionsPreviewRequest struct {
	SessionID string   `json:"session_id"`
	Key       string   `json:"key,omitempty"`
	Keys      []string `json:"keys,omitempty"`
	MaxChars  int      `json:"maxChars,omitempty"`
	Limit     int      `json:"limit,omitempty"`
}

type SessionsPatchRequest struct {
	SessionID string         `json:"session_id"`
	Key       string         `json:"key,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type SessionsResetRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key,omitempty"`
}

type SessionsDeleteRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key,omitempty"`
}

// SessionsExportRequest requests exporting a session transcript.
type SessionsExportRequest struct {
	// SessionID is the session to export.
	SessionID string `json:"session_id"`
	// Format is the export format. Currently only "html" is supported.
	Format string `json:"format,omitempty"`
}

// SessionsExportResponse holds the exported content.
type SessionsExportResponse struct {
	// HTML is set when Format == "html".
	HTML   string `json:"html,omitempty"`
	Format string `json:"format"`
}

type SessionsCompactRequest struct {
	SessionID string `json:"session_id"`
	Key       string `json:"key,omitempty"`
	Keep      int    `json:"keep,omitempty"`
}

// SessionsPruneRequest deletes transcript entries for sessions that are older
// than OlderThanDays days (measured by last_inbound_at).  When All is true
// every session is deleted regardless of age.  DryRun reports what would be
// deleted without actually removing anything.
type SessionsPruneRequest struct {
	OlderThanDays int  `json:"older_than_days,omitempty"`
	DryRun        bool `json:"dry_run,omitempty"`
	All           bool `json:"all,omitempty"`
}

type SessionsSpawnRequest struct {
	// Message is the initial prompt for the spawned subagent.
	Message string `json:"message"`
	// ParentSessionID is the caller's session ID for depth/ancestry tracking.
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// AgentID selects which configured agent handles the sub-session.
	AgentID string `json:"agent_id,omitempty"`
	// MemoryScope carries the explicit worker memory scope contract.
	MemoryScope state.AgentMemoryScope `json:"memory_scope,omitempty"`
	// Context is extra system context to inject into the child session.
	Context string `json:"context,omitempty"`
	// TimeoutMS limits how long the caller will wait via agent.wait.
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

func (r SessionsSpawnRequest) Normalize() (SessionsSpawnRequest, error) {
	r.Message = strings.TrimSpace(r.Message)
	if r.Message == "" {
		return r, fmt.Errorf("message is required")
	}
	r.ParentSessionID = strings.TrimSpace(r.ParentSessionID)
	r.AgentID = normalizeAgentID(r.AgentID)
	if raw := strings.TrimSpace(string(r.MemoryScope)); raw != "" {
		scope, ok := state.ParseAgentMemoryScope(raw)
		if !ok {
			return r, fmt.Errorf("memory_scope must be one of: user, project, local")
		}
		r.MemoryScope = scope
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 60_000, 300_000)
	return r, nil
}

func DecodeSessionsSpawnParams(params json.RawMessage) (SessionsSpawnRequest, error) {
	return decodeMethodParams[SessionsSpawnRequest](params)
}

type SessionGetResponse struct {
	Session    state.SessionDoc           `json:"session"`
	Transcript []state.TranscriptEntryDoc `json:"transcript"`
}

type ListGetRequest struct {
	Name string `json:"name"`
}

type ListPutRequest struct {
	Name               string   `json:"name"`
	Items              []string `json:"items"`
	ExpectedVersion    int      `json:"expected_version,omitempty"`
	ExpectedVersionSet bool     `json:"-"`
	ExpectedEvent      string   `json:"expected_event,omitempty"`
}

type ConfigPutRequest struct {
	Config             state.ConfigDoc `json:"config"`
	ExpectedVersion    int             `json:"expected_version,omitempty"`
	ExpectedVersionSet bool            `json:"-"`
	ExpectedEvent      string          `json:"expected_event,omitempty"`
	BaseHash           string          `json:"baseHash,omitempty"`
}

type ConfigSetRequest struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Raw      string `json:"raw,omitempty"`
	BaseHash string `json:"baseHash,omitempty"`
}

type ConfigApplyRequest struct {
	Config         state.ConfigDoc `json:"config"`
	Raw            string          `json:"raw,omitempty"`
	BaseHash       string          `json:"baseHash,omitempty"`
	SessionKey     string          `json:"sessionKey,omitempty"`
	Note           string          `json:"note,omitempty"`
	RestartDelayMS int             `json:"restartDelayMs,omitempty"`
}

type ConfigPatchRequest struct {
	Patch          map[string]any `json:"patch"`
	Raw            string         `json:"raw,omitempty"`
	BaseHash       string         `json:"baseHash,omitempty"`
	SessionKey     string         `json:"sessionKey,omitempty"`
	Note           string         `json:"note,omitempty"`
	RestartDelayMS int            `json:"restartDelayMs,omitempty"`
}

type LogsTailRequest struct {
	Cursor   int64 `json:"cursor,omitempty"`
	Limit    int   `json:"limit,omitempty"`
	MaxBytes int   `json:"max_bytes,omitempty"`
	Lines    int   `json:"lines,omitempty"`
}

type RuntimeObserveRequest struct {
	IncludeEvents *bool    `json:"include_events,omitempty"`
	IncludeLogs   *bool    `json:"include_logs,omitempty"`
	EventCursor   int64    `json:"event_cursor,omitempty"`
	LogCursor     int64    `json:"log_cursor,omitempty"`
	EventLimit    int      `json:"event_limit,omitempty"`
	LogLimit      int      `json:"log_limit,omitempty"`
	MaxBytes      int      `json:"max_bytes,omitempty"`
	WaitTimeoutMS int      `json:"wait_timeout_ms,omitempty"`
	Events        []string `json:"events,omitempty"`
	AgentID       string   `json:"agent_id,omitempty"`
	SessionID     string   `json:"session_id,omitempty"`
	ChannelID     string   `json:"channel_id,omitempty"`
	Direction     string   `json:"direction,omitempty"`
	Subsystem     string   `json:"subsystem,omitempty"`
	Source        string   `json:"source,omitempty"`
}

type ChannelsStatusRequest struct {
	Probe     bool `json:"probe,omitempty"`
	TimeoutMS int  `json:"timeout_ms,omitempty"`
}

type ChannelsLogoutRequest struct {
	Channel string `json:"channel"`
}

// ChannelsJoinRequest joins a NIP-29 relay group or other channel.
// For NIP-29, GroupAddress has the form "<relayHost>'<groupID>".
type ChannelsJoinRequest struct {
	Type         string `json:"type"`          // "nip29-group"
	GroupAddress string `json:"group_address"` // relay'groupID
}

// ChannelsLeaveRequest leaves a previously joined channel.
type ChannelsLeaveRequest struct {
	ChannelID string `json:"channel_id"`
}

// ChannelsListRequest requests the list of joined channels.
type ChannelsListRequest struct{}

// ChannelsSendRequest sends a message to a joined channel.
type ChannelsSendRequest struct {
	ChannelID string `json:"channel_id"`
	Text      string `json:"text"`
}

type UsageCostRequest struct {
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
	Days      int    `json:"days,omitempty"`
	Mode      string `json:"mode,omitempty"`
	UTCOffset string `json:"utcOffset,omitempty"`
}

type RelayPolicyResponse struct {
	ReadRelays           []string `json:"read_relays"`
	WriteRelays          []string `json:"write_relays"`
	RuntimeDMRelays      []string `json:"runtime_dm_relays"`
	RuntimeControlRelays []string `json:"runtime_control_relays"`
}

type AgentsListRequest struct {
	Limit int `json:"limit,omitempty"`
}

type AgentsCreateRequest struct {
	AgentID   string         `json:"agent_id"`
	Name      string         `json:"name,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Model     string         `json:"model,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type AgentsUpdateRequest struct {
	AgentID   string         `json:"agent_id"`
	Name      string         `json:"name,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Model     string         `json:"model,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

type AgentsDeleteRequest struct {
	AgentID string `json:"agent_id"`
}

type AgentsFilesListRequest struct {
	AgentID string `json:"agent_id"`
	Limit   int    `json:"limit,omitempty"`
}

type AgentsFilesGetRequest struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
}

type AgentsFilesSetRequest struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// AgentsAssignRequest routes a session (peer pubkey or WS client ID) to a
// specific agent.  Subsequent DM/WS messages from that session are handled by
// the named agent's runtime.
type AgentsAssignRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// AgentsUnassignRequest removes a session→agent assignment; the session falls
// back to the default "main" agent.
type AgentsUnassignRequest struct {
	SessionID string `json:"session_id"`
}

// AgentsActiveRequest requests the list of active agent runtimes and their
// session assignments.
type AgentsActiveRequest struct {
	Limit int `json:"limit,omitempty"`
}

type ModelsListRequest struct{}

type ToolsCatalogRequest struct {
	AgentID        string  `json:"agent_id,omitempty"`
	IncludePlugins *bool   `json:"include_plugins,omitempty"`
	Profile        *string `json:"profile,omitempty"`
}

type ToolsProfileGetRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

type ToolsProfileSetRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	Profile string `json:"profile"`
}

type SkillsStatusRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

type SkillsBinsRequest struct{}

type SkillsInstallRequest struct {
	AgentID   string `json:"agent_id,omitempty"`
	Name      string `json:"name"`
	InstallID string `json:"install_id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type SkillsUpdateRequest struct {
	AgentID  string            `json:"agent_id,omitempty"`
	SkillKey string            `json:"skill_key"`
	Enabled  *bool             `json:"enabled,omitempty"`
	APIKey   *string           `json:"api_key,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

type PluginsInstallRequest struct {
	PluginID        string         `json:"plugin_id"`
	Install         map[string]any `json:"install"`
	EnableEntry     *bool          `json:"enable_entry,omitempty"`
	IncludeLoadPath *bool          `json:"include_load_path,omitempty"`
}

type PluginsUninstallRequest struct {
	PluginID string `json:"plugin_id"`
}

type PluginsUpdateRequest struct {
	PluginIDs []string `json:"plugin_ids,omitempty"`
	DryRun    bool     `json:"dry_run,omitempty"`
}

// PluginsRegistryListRequest fetches the full plugin index from a remote registry.
type PluginsRegistryListRequest struct {
	// RegistryURL overrides the registry URL configured in the daemon config.
	// If empty, the daemon's configured registry URL is used.
	RegistryURL string `json:"registry_url,omitempty"`
}

// PluginsRegistryGetRequest fetches details for a single plugin from the registry.
type PluginsRegistryGetRequest struct {
	// PluginID is the plugin identifier to look up.
	PluginID string `json:"plugin_id"`
	// RegistryURL overrides the configured registry URL.
	RegistryURL string `json:"registry_url,omitempty"`
}

// PluginsRegistrySearchRequest searches the remote registry by keyword or tag.
type PluginsRegistrySearchRequest struct {
	// Query is a keyword to match against plugin name, description, and tags.
	Query string `json:"query,omitempty"`
	// Tag filters results by a specific tag.
	Tag string `json:"tag,omitempty"`
	// RegistryURL overrides the configured registry URL.
	RegistryURL string `json:"registry_url,omitempty"`
}

type NodePairRequest struct {
	NodeID          string         `json:"node_id"`
	DisplayName     string         `json:"display_name,omitempty"`
	Platform        string         `json:"platform,omitempty"`
	Version         string         `json:"version,omitempty"`
	CoreVersion     string         `json:"core_version,omitempty"`
	UIVersion       string         `json:"ui_version,omitempty"`
	DeviceFamily    string         `json:"device_family,omitempty"`
	ModelIdentifier string         `json:"model_identifier,omitempty"`
	Caps            []string       `json:"caps,omitempty"`
	Commands        []string       `json:"commands,omitempty"`
	Permissions     map[string]any `json:"permissions,omitempty"`
	RemoteIP        string         `json:"remote_ip,omitempty"`
	Silent          bool           `json:"silent,omitempty"`
}

type NodePairListRequest struct{}

type NodePairApproveRequest struct {
	RequestID string `json:"request_id"`
}

type NodePairRejectRequest struct {
	RequestID string `json:"request_id"`
}

type NodePairVerifyRequest struct {
	NodeID string `json:"node_id"`
	Token  string `json:"token"`
}

type DevicePairListRequest struct{}

type DevicePairApproveRequest struct {
	RequestID string `json:"request_id"`
}

type DevicePairRejectRequest struct {
	RequestID string `json:"request_id"`
}

type DevicePairRemoveRequest struct {
	DeviceID string `json:"device_id"`
}

type DeviceTokenRotateRequest struct {
	DeviceID string   `json:"device_id"`
	Role     string   `json:"role"`
	Scopes   []string `json:"scopes,omitempty"`
}

type DeviceTokenRevokeRequest struct {
	DeviceID string `json:"device_id"`
	Role     string `json:"role"`
}

type NodeListRequest struct {
	Limit int `json:"limit,omitempty"`
}

type NodeDescribeRequest struct {
	NodeID string `json:"node_id"`
}

type NodeRenameRequest struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
}

type NodeCanvasCapabilityRefreshRequest struct {
	NodeID string `json:"node_id"`
}

type NodeInvokeRequest struct {
	NodeID    string         `json:"node_id"`
	Command   string         `json:"command"`
	Args      map[string]any `json:"args,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
	RunID     string         `json:"run_id,omitempty"`
}

type NodeEventRequest struct {
	RunID   string         `json:"run_id"`
	NodeID  string         `json:"node_id,omitempty"`
	Type    string         `json:"type"`
	Status  string         `json:"status,omitempty"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

type NodeResultRequest struct {
	RunID  string `json:"run_id"`
	NodeID string `json:"node_id,omitempty"`
	Status string `json:"status,omitempty"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type NodePendingEnqueueRequest struct {
	NodeID         string         `json:"node_id"`
	Command        string         `json:"command"`
	Args           map[string]any `json:"args,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	TTLMS          int            `json:"ttl_ms,omitempty"`
}

type NodePendingPullRequest struct {
	NodeID string `json:"node_id,omitempty"`
}

type NodePendingAckRequest struct {
	NodeID string   `json:"node_id"`
	IDs    []string `json:"ids,omitempty"`
}

type NodePendingDrainRequest struct {
	NodeID   string `json:"node_id"`
	MaxItems int    `json:"max_items,omitempty"`
}

type CronListRequest struct {
	Limit int `json:"limit,omitempty"`
}

type CronStatusRequest struct {
	ID string `json:"id"`
}

type CronAddRequest struct {
	ID       string          `json:"id,omitempty"`
	Schedule string          `json:"schedule"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
	Enabled  *bool           `json:"enabled,omitempty"`
}

type CronUpdateRequest struct {
	ID       string          `json:"id"`
	Schedule string          `json:"schedule,omitempty"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	Enabled  *bool           `json:"enabled,omitempty"`
}

type CronRemoveRequest struct {
	ID string `json:"id"`
}

type CronRunRequest struct {
	ID string `json:"id"`
}

type CronRunsRequest struct {
	ID    string `json:"id,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type CanvasGetRequest struct {
	ID string `json:"id"`
}

type CanvasListRequest struct{}

type CanvasUpdateRequest struct {
	ID          string `json:"id"`
	ContentType string `json:"content_type"`
	Data        string `json:"data"`
}

type CanvasDeleteRequest struct {
	ID string `json:"id"`
}

type ExecApprovalsGetRequest struct{}

type ExecApprovalsSetRequest struct {
	Approvals map[string]any `json:"approvals"`
}

type ExecApprovalsNodeGetRequest struct {
	NodeID string `json:"node_id"`
}

type ExecApprovalsNodeSetRequest struct {
	NodeID    string         `json:"node_id"`
	Approvals map[string]any `json:"approvals"`
}

type ExecApprovalRequestRequest struct {
	NodeID    string         `json:"node_id,omitempty"`
	Command   string         `json:"command"`
	Args      map[string]any `json:"args,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
}

type ExecApprovalWaitDecisionRequest struct {
	ID        string `json:"id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type ExecApprovalResolveRequest struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

// SandboxRunRequest is the request payload for sandbox.run.
type SandboxRunRequest struct {
	// Cmd is the command and arguments to execute.
	Cmd []string `json:"cmd"`
	// Env is a list of "KEY=VALUE" environment overrides.
	Env []string `json:"env,omitempty"`
	// Workdir is the working directory for the command.
	Workdir string `json:"workdir,omitempty"`
	// TimeoutSeconds overrides the daemon's configured sandbox timeout.
	TimeoutSeconds int `json:"timeout_s,omitempty"`
	// Driver overrides the daemon's configured sandbox driver.
	Driver string `json:"driver,omitempty"`
}

type MCPAuthStartRequest struct {
	Server       string `json:"server"`
	ClientSecret string `json:"client_secret,omitempty"`
	TimeoutMS    int    `json:"timeout_ms,omitempty"`
}

type MCPAuthRefreshRequest struct {
	Server string `json:"server"`
}

type MCPAuthClearRequest struct {
	Server string `json:"server"`
}

type SecretsReloadRequest struct{}

type SecretsResolveRequest struct {
	CommandName string   `json:"commandName,omitempty"`
	TargetIDs   []string `json:"targetIds"`
}

type WizardStartRequest struct {
	Mode string `json:"mode,omitempty"`
}

type WizardNextRequest struct {
	ID    string         `json:"id"`
	Input map[string]any `json:"input,omitempty"`
}

type WizardCancelRequest struct {
	ID string `json:"id"`
}

type WizardStatusRequest struct {
	ID string `json:"id,omitempty"`
}

type TalkConfigRequest struct {
	IncludeSecrets bool `json:"includeSecrets,omitempty"`
}

type TalkModeRequest struct {
	Mode string `json:"mode"`
}

type UpdateRunRequest struct {
	Force bool `json:"force,omitempty"`
}

type LastHeartbeatRequest struct{}

type SetHeartbeatsRequest struct {
	Enabled    *bool `json:"enabled,omitempty"`
	IntervalMS int   `json:"interval_ms,omitempty"`
}

type WakeRequest struct {
	Source string `json:"source,omitempty"`
	Text   string `json:"text,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

type SystemPresenceRequest struct{}

type SystemEventRequest struct {
	Text             string   `json:"text"`
	DeviceID         string   `json:"device_id,omitempty"`
	InstanceID       string   `json:"instance_id,omitempty"`
	Host             string   `json:"host,omitempty"`
	IP               string   `json:"ip,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	Version          string   `json:"version,omitempty"`
	Platform         string   `json:"platform,omitempty"`
	DeviceFamily     string   `json:"device_family,omitempty"`
	ModelIdentifier  string   `json:"model_identifier,omitempty"`
	LastInputSeconds float64  `json:"last_input_seconds,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	Roles            []string `json:"roles,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

type SendRequest struct {
	To             string   `json:"to"`
	Message        string   `json:"message,omitempty"`
	Text           string   `json:"text,omitempty"`
	MediaURL       string   `json:"mediaUrl,omitempty"`
	MediaURLs      []string `json:"mediaUrls,omitempty"`
	Channel        string   `json:"channel,omitempty"`
	IdempotencyKey string   `json:"idempotencyKey,omitempty"`
}

type BrowserRequestRequest struct {
	Method    string         `json:"method"`
	Path      string         `json:"path"`
	Query     map[string]any `json:"query,omitempty"`
	Body      any            `json:"body,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
}

type VoicewakeGetRequest struct{}

type VoicewakeSetRequest struct {
	Triggers []string `json:"triggers"`
}

type TTSStatusRequest struct{}

type TTSProvidersRequest struct{}

type TTSSetProviderRequest struct {
	Provider string `json:"provider"`
}

type TTSEnableRequest struct{}

type TTSDisableRequest struct{}

type TTSConvertRequest struct {
	Text     string `json:"text"`
	Provider string `json:"provider,omitempty"`
	Voice    string `json:"voice,omitempty"`
}

func (r MemorySearchRequest) Normalize() (MemorySearchRequest, error) {
	r.Query = strings.TrimSpace(r.Query)
	if r.Query == "" {
		return r, fmt.Errorf("query is required")
	}
	if utf8.RuneCountInString(r.Query) > 256 {
		r.Query = truncateRunes(r.Query, 256)
	}
	r.Limit = normalizeLimit(r.Limit, 20, 200)
	return r, nil
}

func (r AgentRequest) Normalize() (AgentRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Message = strings.TrimSpace(r.Message)
	r.Context = strings.TrimSpace(r.Context)
	if raw := strings.TrimSpace(string(r.MemoryScope)); raw != "" {
		scope, ok := state.ParseAgentMemoryScope(raw)
		if !ok {
			return r, fmt.Errorf("memory_scope must be one of: user, project, local")
		}
		r.MemoryScope = scope
	}
	if r.Message == "" {
		return r, fmt.Errorf("message is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 60_000, 300_000)
	return r, nil
}

func (r AgentWaitRequest) Normalize() (AgentWaitRequest, error) {
	r.RunID = strings.TrimSpace(r.RunID)
	if r.RunID == "" {
		return r, fmt.Errorf("run_id is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 30_000, 120_000)
	return r, nil
}

func (r AgentIdentityRequest) Normalize() (AgentIdentityRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.AgentID = strings.TrimSpace(r.AgentID)
	return r, nil
}

func (r ChatSendRequest) Normalize() (ChatSendRequest, error) {
	r.To = strings.TrimSpace(r.To)
	r.Text = strings.TrimSpace(r.Text)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	r.RunID = strings.TrimSpace(r.RunID)
	if r.RunID == "" {
		r.RunID = r.IdempotencyKey
	}
	if r.To == "" {
		return r, fmt.Errorf("to is required")
	}
	// text is optional when attachments are present (e.g. image-only messages).
	if r.Text == "" && len(r.Attachments) == 0 {
		return r, fmt.Errorf("text or attachments are required")
	}
	const maxTextRunes = 4096
	if utf8.RuneCountInString(r.Text) > maxTextRunes {
		return r, fmt.Errorf("text exceeds %d characters", maxTextRunes)
	}
	return r, nil
}

func (r ChatHistoryRequest) Normalize() (ChatHistoryRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 50, 500)
	return r, nil
}

func (r ChatAbortRequest) Normalize() (ChatAbortRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.RunID = strings.TrimSpace(r.RunID)
	return r, nil
}

func (r SessionGetRequest) Normalize() (SessionGetRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 50, 500)
	return r, nil
}

func (r SessionsListRequest) Normalize() (SessionsListRequest, error) {
	if strings.TrimSpace(r.AgentID) == "" {
		r.AgentID = strings.TrimSpace(r.AgentIDSnake)
	}
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r SessionsPreviewRequest) Normalize() (SessionsPreviewRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	cleaned := make([]string, 0, len(r.Keys))
	for _, key := range r.Keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cleaned = append(cleaned, key)
	}
	r.Keys = cleaned
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" && len(r.Keys) > 0 {
		r.SessionID = r.Keys[0]
	}
	if r.SessionID == "" && len(r.Keys) == 0 {
		return r, fmt.Errorf("session_id or keys is required")
	}
	r.Limit = normalizeLimit(r.Limit, 25, 200)
	return r, nil
}

func (r SessionsPatchRequest) Normalize() (SessionsPatchRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	return r, nil
}

func (r SessionsResetRequest) Normalize() (SessionsResetRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r SessionsDeleteRequest) Normalize() (SessionsDeleteRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r SessionsCompactRequest) Normalize() (SessionsCompactRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.Key = strings.TrimSpace(r.Key)
	if r.SessionID == "" {
		r.SessionID = r.Key
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	r.Keep = normalizeLimit(r.Keep, 50, 500)
	return r, nil
}

func (r ListGetRequest) Normalize() (ListGetRequest, error) {
	r.Name = normalizeListName(r.Name)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	return r, nil
}

func (r ListPutRequest) Normalize() (ListPutRequest, error) {
	r.Name = normalizeListName(r.Name)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	out := make([]string, 0, len(r.Items))
	seen := map[string]struct{}{}
	for _, item := range r.Items {
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
	r.Items = out
	if r.ExpectedVersionSet && r.ExpectedVersion < 0 {
		return r, fmt.Errorf("expected_version must be >= 0")
	}
	r.ExpectedEvent = strings.TrimSpace(r.ExpectedEvent)
	return r, nil
}

func (r ConfigPutRequest) Normalize() (ConfigPutRequest, error) {
	if strings.TrimSpace(r.Config.DM.Policy) == "" {
		return r, fmt.Errorf("config.dm.policy is required")
	}
	if r.Config.Version == 0 {
		r.Config.Version = 1
	}
	if r.ExpectedVersionSet && r.ExpectedVersion < 0 {
		return r, fmt.Errorf("expected_version must be >= 0")
	}
	r.BaseHash = strings.TrimSpace(r.BaseHash)
	r.ExpectedEvent = strings.TrimSpace(r.ExpectedEvent)
	return r, nil
}

func (r ConfigSetRequest) Normalize() (ConfigSetRequest, error) {
	r.Key = strings.TrimSpace(r.Key)
	r.Raw = strings.TrimSpace(r.Raw)
	r.BaseHash = strings.TrimSpace(r.BaseHash)
	if r.Key == "" && r.Raw == "" {
		return r, fmt.Errorf("key is required")
	}
	return r, nil
}

func (r ConfigApplyRequest) Normalize() (ConfigApplyRequest, error) {
	r.Raw = strings.TrimSpace(r.Raw)
	r.BaseHash = strings.TrimSpace(r.BaseHash)
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.Note = strings.TrimSpace(r.Note)
	if r.RestartDelayMS < 0 {
		r.RestartDelayMS = 0
	}
	if r.Raw != "" {
		return r, nil
	}
	if strings.TrimSpace(r.Config.DM.Policy) == "" {
		return r, fmt.Errorf("config.dm.policy is required")
	}
	if r.Config.Version == 0 {
		r.Config.Version = 1
	}
	return r, nil
}

func (r ConfigPatchRequest) Normalize() (ConfigPatchRequest, error) {
	r.Raw = strings.TrimSpace(r.Raw)
	r.BaseHash = strings.TrimSpace(r.BaseHash)
	r.SessionKey = strings.TrimSpace(r.SessionKey)
	r.Note = strings.TrimSpace(r.Note)
	if r.RestartDelayMS < 0 {
		r.RestartDelayMS = 0
	}
	if r.Raw == "" && len(r.Patch) == 0 {
		return r, fmt.Errorf("patch is required")
	}
	return r, nil
}

func (r LogsTailRequest) Normalize() (LogsTailRequest, error) {
	if r.Limit == 0 && r.Lines != 0 {
		r.Limit = r.Lines
	}
	r.Limit = normalizeLimit(r.Limit, 100, 2000)
	r.MaxBytes = normalizeLimit(r.MaxBytes, 64*1024, 2*1024*1024)
	if r.Cursor < 0 {
		r.Cursor = 0
	}
	return r, nil
}

func (r RuntimeObserveRequest) Normalize() (RuntimeObserveRequest, error) {
	includeEvents := true
	if r.IncludeEvents != nil {
		includeEvents = *r.IncludeEvents
	}
	includeLogs := true
	if r.IncludeLogs != nil {
		includeLogs = *r.IncludeLogs
	}
	if !includeEvents && !includeLogs {
		return r, fmt.Errorf("at least one of include_events or include_logs must be true")
	}
	r.IncludeEvents = boolPtr(includeEvents)
	r.IncludeLogs = boolPtr(includeLogs)
	if r.EventCursor < 0 {
		r.EventCursor = 0
	}
	if r.LogCursor < 0 {
		r.LogCursor = 0
	}
	r.EventLimit = normalizeLimit(r.EventLimit, 20, 200)
	r.LogLimit = normalizeLimit(r.LogLimit, 20, 200)
	r.MaxBytes = normalizeLimit(r.MaxBytes, 32*1024, 256*1024)
	if r.WaitTimeoutMS < 0 {
		r.WaitTimeoutMS = 0
	}
	if r.WaitTimeoutMS > 60_000 {
		r.WaitTimeoutMS = 60_000
	}
	r.AgentID = strings.TrimSpace(r.AgentID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	r.ChannelID = strings.TrimSpace(r.ChannelID)
	r.Direction = strings.TrimSpace(r.Direction)
	r.Subsystem = strings.TrimSpace(r.Subsystem)
	r.Source = strings.TrimSpace(r.Source)
	r.Events = compactStringSlice(r.Events)
	return r, nil
}

func (r ChannelsStatusRequest) Normalize() (ChannelsStatusRequest, error) {
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 10_000, 60_000)
	return r, nil
}

func (r ChannelsLogoutRequest) Normalize() (ChannelsLogoutRequest, error) {
	r.Channel = strings.ToLower(strings.TrimSpace(r.Channel))
	if r.Channel == "" {
		return r, fmt.Errorf("channel is required")
	}
	return r, nil
}

func (r ChannelsJoinRequest) Normalize() (ChannelsJoinRequest, error) {
	r.Type = strings.ToLower(strings.TrimSpace(r.Type))
	r.GroupAddress = strings.TrimSpace(r.GroupAddress)
	if r.Type == "" {
		r.Type = "nip29-group"
	}
	if r.Type != "nip29-group" {
		return r, fmt.Errorf("unsupported channel type %q", r.Type)
	}
	if r.GroupAddress == "" {
		return r, fmt.Errorf("group_address is required")
	}
	return r, nil
}

func (r ChannelsLeaveRequest) Normalize() (ChannelsLeaveRequest, error) {
	r.ChannelID = strings.TrimSpace(r.ChannelID)
	if r.ChannelID == "" {
		return r, fmt.Errorf("channel_id is required")
	}
	return r, nil
}

func (r ChannelsListRequest) Normalize() (ChannelsListRequest, error) { return r, nil }

func (r ChannelsSendRequest) Normalize() (ChannelsSendRequest, error) {
	r.ChannelID = strings.TrimSpace(r.ChannelID)
	r.Text = strings.TrimSpace(r.Text)
	if r.ChannelID == "" {
		return r, fmt.Errorf("channel_id is required")
	}
	if r.Text == "" {
		return r, fmt.Errorf("text is required")
	}
	return r, nil
}

func (r UsageCostRequest) Normalize() (UsageCostRequest, error) {
	r.StartDate = strings.TrimSpace(r.StartDate)
	r.EndDate = strings.TrimSpace(r.EndDate)
	r.Mode = strings.TrimSpace(r.Mode)
	r.UTCOffset = strings.TrimSpace(r.UTCOffset)
	if r.Days < 0 {
		return r, fmt.Errorf("days must be >= 0")
	}
	return r, nil
}

func (r AgentsListRequest) Normalize() (AgentsListRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r AgentsCreateRequest) Normalize() (AgentsCreateRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	r.Workspace = strings.TrimSpace(r.Workspace)
	r.Model = strings.TrimSpace(r.Model)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	return r, nil
}

func (r AgentsUpdateRequest) Normalize() (AgentsUpdateRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	r.Workspace = strings.TrimSpace(r.Workspace)
	r.Model = strings.TrimSpace(r.Model)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if r.Meta == nil {
		r.Meta = map[string]any{}
	}
	return r, nil
}

func (r AgentsDeleteRequest) Normalize() (AgentsDeleteRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	return r, nil
}

func (r AgentsFilesListRequest) Normalize() (AgentsFilesListRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	r.Limit = normalizeLimit(r.Limit, 200, 1000)
	return r, nil
}

func (r AgentsFilesGetRequest) Normalize() (AgentsFilesGetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if !isSafeAgentFileName(r.Name) {
		return r, fmt.Errorf("invalid file name")
	}
	return r, nil
}

func (r AgentsFilesSetRequest) Normalize() (AgentsFilesSetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if !isSafeAgentFileName(r.Name) {
		return r, fmt.Errorf("invalid file name")
	}
	return r, nil
}

func (r AgentsAssignRequest) Normalize() (AgentsAssignRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.AgentID == "" {
		return r, fmt.Errorf("agent_id is required")
	}
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r AgentsUnassignRequest) Normalize() (AgentsUnassignRequest, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		return r, fmt.Errorf("session_id is required")
	}
	return r, nil
}

func (r AgentsActiveRequest) Normalize() (AgentsActiveRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r ModelsListRequest) Normalize() (ModelsListRequest, error) {
	return r, nil
}

func (r ToolsCatalogRequest) Normalize() (ToolsCatalogRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	return r, nil
}

func (r ToolsProfileGetRequest) Normalize() (ToolsProfileGetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	return r, nil
}

func (r ToolsProfileSetRequest) Normalize() (ToolsProfileSetRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Profile = strings.TrimSpace(strings.ToLower(r.Profile))
	if r.Profile == "" {
		return r, fmt.Errorf("profile is required")
	}
	return r, nil
}

func (r SkillsStatusRequest) Normalize() (SkillsStatusRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	return r, nil
}

func (r SkillsBinsRequest) Normalize() (SkillsBinsRequest, error) {
	return r, nil
}

func (r SkillsInstallRequest) Normalize() (SkillsInstallRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.Name = strings.TrimSpace(r.Name)
	r.InstallID = strings.TrimSpace(r.InstallID)
	if r.Name == "" {
		return r, fmt.Errorf("name is required")
	}
	if r.InstallID == "" {
		return r, fmt.Errorf("install_id is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 120_000, 600_000)
	return r, nil
}

func (r SkillsUpdateRequest) Normalize() (SkillsUpdateRequest, error) {
	r.AgentID = normalizeAgentID(r.AgentID)
	r.SkillKey = strings.ToLower(strings.TrimSpace(r.SkillKey))
	if r.SkillKey == "" {
		return r, fmt.Errorf("skill_key is required")
	}
	if r.Env == nil {
		r.Env = map[string]string{}
	} else {
		cleaned := make(map[string]string, len(r.Env))
		for key, value := range r.Env {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}
			trimmedValue := strings.TrimSpace(value)
			if trimmedValue == "" {
				continue
			}
			cleaned[trimmedKey] = trimmedValue
		}
		r.Env = cleaned
	}
	if r.APIKey != nil {
		trimmed := strings.TrimSpace(*r.APIKey)
		r.APIKey = &trimmed
	}
	return r, nil
}

func (r PluginsInstallRequest) Normalize() (PluginsInstallRequest, error) {
	r.PluginID = strings.ToLower(strings.TrimSpace(r.PluginID))
	if r.PluginID == "" {
		return r, fmt.Errorf("plugin_id is required")
	}
	if !isSafePluginID(r.PluginID) {
		return r, fmt.Errorf("invalid plugin_id")
	}
	if r.Install == nil || len(r.Install) == 0 {
		return r, fmt.Errorf("install is required")
	}
	if r.EnableEntry == nil {
		v := true
		r.EnableEntry = &v
	}
	if r.IncludeLoadPath == nil {
		v := true
		r.IncludeLoadPath = &v
	}
	return r, nil
}

func (r PluginsUninstallRequest) Normalize() (PluginsUninstallRequest, error) {
	r.PluginID = strings.ToLower(strings.TrimSpace(r.PluginID))
	if r.PluginID == "" {
		return r, fmt.Errorf("plugin_id is required")
	}
	if !isSafePluginID(r.PluginID) {
		return r, fmt.Errorf("invalid plugin_id")
	}
	return r, nil
}

func (r PluginsUpdateRequest) Normalize() (PluginsUpdateRequest, error) {
	r.PluginIDs = compactStringSlice(r.PluginIDs)
	return r, nil
}

func (r NodePairRequest) Normalize() (NodePairRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.DisplayName = strings.TrimSpace(r.DisplayName)
	r.Platform = strings.TrimSpace(r.Platform)
	r.Version = strings.TrimSpace(r.Version)
	r.CoreVersion = strings.TrimSpace(r.CoreVersion)
	r.UIVersion = strings.TrimSpace(r.UIVersion)
	r.DeviceFamily = strings.TrimSpace(r.DeviceFamily)
	r.ModelIdentifier = strings.TrimSpace(r.ModelIdentifier)
	r.RemoteIP = strings.TrimSpace(r.RemoteIP)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	if r.Permissions == nil {
		r.Permissions = map[string]any{}
	}
	return r, nil
}

func (r NodePairListRequest) Normalize() (NodePairListRequest, error) { return r, nil }

func (r NodePairApproveRequest) Normalize() (NodePairApproveRequest, error) {
	r.RequestID = strings.TrimSpace(r.RequestID)
	if r.RequestID == "" {
		return r, fmt.Errorf("request_id is required")
	}
	return r, nil
}

func (r NodePairRejectRequest) Normalize() (NodePairRejectRequest, error) {
	r.RequestID = strings.TrimSpace(r.RequestID)
	if r.RequestID == "" {
		return r, fmt.Errorf("request_id is required")
	}
	return r, nil
}

func (r NodePairVerifyRequest) Normalize() (NodePairVerifyRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Token = strings.TrimSpace(r.Token)
	if r.NodeID == "" || r.Token == "" {
		return r, fmt.Errorf("node_id and token are required")
	}
	return r, nil
}

func (r DevicePairListRequest) Normalize() (DevicePairListRequest, error) { return r, nil }

func (r DevicePairApproveRequest) Normalize() (DevicePairApproveRequest, error) {
	r.RequestID = strings.TrimSpace(r.RequestID)
	if r.RequestID == "" {
		return r, fmt.Errorf("request_id is required")
	}
	return r, nil
}

func (r DevicePairRejectRequest) Normalize() (DevicePairRejectRequest, error) {
	r.RequestID = strings.TrimSpace(r.RequestID)
	if r.RequestID == "" {
		return r, fmt.Errorf("request_id is required")
	}
	return r, nil
}

func (r DevicePairRemoveRequest) Normalize() (DevicePairRemoveRequest, error) {
	r.DeviceID = strings.TrimSpace(r.DeviceID)
	if r.DeviceID == "" {
		return r, fmt.Errorf("device_id is required")
	}
	return r, nil
}

func (r DeviceTokenRotateRequest) Normalize() (DeviceTokenRotateRequest, error) {
	r.DeviceID = strings.TrimSpace(r.DeviceID)
	r.Role = strings.TrimSpace(r.Role)
	if r.DeviceID == "" || r.Role == "" {
		return r, fmt.Errorf("device_id and role are required")
	}
	return r, nil
}

func (r DeviceTokenRevokeRequest) Normalize() (DeviceTokenRevokeRequest, error) {
	r.DeviceID = strings.TrimSpace(r.DeviceID)
	r.Role = strings.TrimSpace(r.Role)
	if r.DeviceID == "" || r.Role == "" {
		return r, fmt.Errorf("device_id and role are required")
	}
	return r, nil
}

func (r NodeListRequest) Normalize() (NodeListRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r NodeDescribeRequest) Normalize() (NodeDescribeRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	return r, nil
}

func (r NodeRenameRequest) Normalize() (NodeRenameRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Name = strings.TrimSpace(r.Name)
	if r.NodeID == "" || r.Name == "" {
		return r, fmt.Errorf("node_id and name are required")
	}
	return r, nil
}

func (r NodeCanvasCapabilityRefreshRequest) Normalize() (NodeCanvasCapabilityRefreshRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	return r, nil
}

func (r NodeInvokeRequest) Normalize() (NodeInvokeRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Command = strings.TrimSpace(r.Command)
	r.RunID = strings.TrimSpace(r.RunID)
	if r.NodeID == "" || r.Command == "" {
		return r, fmt.Errorf("node_id and command are required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 30_000, 300_000)
	if r.Args == nil {
		r.Args = map[string]any{}
	}
	return r, nil
}

func (r NodeEventRequest) Normalize() (NodeEventRequest, error) {
	r.RunID = strings.TrimSpace(r.RunID)
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Type = strings.TrimSpace(r.Type)
	r.Status = strings.TrimSpace(r.Status)
	r.Message = strings.TrimSpace(r.Message)
	if r.RunID == "" || r.Type == "" {
		return r, fmt.Errorf("run_id and type are required")
	}
	if r.Data == nil {
		r.Data = map[string]any{}
	}
	return r, nil
}

func (r NodeResultRequest) Normalize() (NodeResultRequest, error) {
	r.RunID = strings.TrimSpace(r.RunID)
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Status = strings.TrimSpace(r.Status)
	r.Error = strings.TrimSpace(r.Error)
	if r.RunID == "" {
		return r, fmt.Errorf("run_id is required")
	}
	if r.Status == "" {
		r.Status = "ok"
	}
	return r, nil
}

func (r NodePendingEnqueueRequest) Normalize() (NodePendingEnqueueRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Command = strings.TrimSpace(r.Command)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if r.NodeID == "" || r.Command == "" {
		return r, fmt.Errorf("node_id and command are required")
	}
	if r.Args == nil {
		r.Args = map[string]any{}
	}
	if r.TTLMS < 0 {
		r.TTLMS = 0
	}
	return r, nil
}

func (r NodePendingPullRequest) Normalize() (NodePendingPullRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	return r, nil
}

func (r NodePendingAckRequest) Normalize() (NodePendingAckRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	out := make([]string, 0, len(r.IDs))
	for _, id := range r.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	r.IDs = out
	return r, nil
}

func (r NodePendingDrainRequest) Normalize() (NodePendingDrainRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	if r.MaxItems < 0 {
		r.MaxItems = 0
	}
	return r, nil
}

func (r CronListRequest) Normalize() (CronListRequest, error) {
	r.Limit = normalizeLimit(r.Limit, 100, 500)
	return r, nil
}

func (r CronStatusRequest) Normalize() (CronStatusRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	return r, nil
}

func (r CronAddRequest) Normalize() (CronAddRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.Schedule = strings.TrimSpace(r.Schedule)
	r.Method = strings.TrimSpace(r.Method)
	if r.Schedule == "" || r.Method == "" {
		return r, fmt.Errorf("schedule and method are required")
	}
	return r, nil
}

func (r CronUpdateRequest) Normalize() (CronUpdateRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.Schedule = strings.TrimSpace(r.Schedule)
	r.Method = strings.TrimSpace(r.Method)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	if r.Schedule == "" && r.Method == "" && len(r.Params) == 0 && r.Enabled == nil {
		return r, fmt.Errorf("at least one update field is required")
	}
	return r, nil
}

func (r CronRemoveRequest) Normalize() (CronRemoveRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	return r, nil
}

func (r CronRunRequest) Normalize() (CronRunRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	return r, nil
}

func (r CronRunsRequest) Normalize() (CronRunsRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.Limit = normalizeLimit(r.Limit, 50, 500)
	return r, nil
}

func (r ExecApprovalsGetRequest) Normalize() (ExecApprovalsGetRequest, error) { return r, nil }

func (r ExecApprovalsSetRequest) Normalize() (ExecApprovalsSetRequest, error) {
	if r.Approvals == nil {
		r.Approvals = map[string]any{}
	}
	return r, nil
}

func (r ExecApprovalsNodeGetRequest) Normalize() (ExecApprovalsNodeGetRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	return r, nil
}

func (r ExecApprovalsNodeSetRequest) Normalize() (ExecApprovalsNodeSetRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	if r.NodeID == "" {
		return r, fmt.Errorf("node_id is required")
	}
	if r.Approvals == nil {
		r.Approvals = map[string]any{}
	}
	return r, nil
}

func (r ExecApprovalRequestRequest) Normalize() (ExecApprovalRequestRequest, error) {
	r.NodeID = strings.TrimSpace(r.NodeID)
	r.Command = strings.TrimSpace(r.Command)
	if r.Command == "" {
		return r, fmt.Errorf("command is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 60_000, 600_000)
	if r.Args == nil {
		r.Args = map[string]any{}
	}
	return r, nil
}

func (r ExecApprovalWaitDecisionRequest) Normalize() (ExecApprovalWaitDecisionRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	r.TimeoutMS = normalizeLimit(r.TimeoutMS, 30_000, 600_000)
	return r, nil
}

func (r ExecApprovalResolveRequest) Normalize() (ExecApprovalResolveRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.Decision = strings.TrimSpace(r.Decision)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.ID == "" || r.Decision == "" {
		return r, fmt.Errorf("id and decision are required")
	}
	return r, nil
}

func (r SecretsReloadRequest) Normalize() (SecretsReloadRequest, error) { return r, nil }

func (r SecretsResolveRequest) Normalize() (SecretsResolveRequest, error) {
	r.CommandName = strings.TrimSpace(r.CommandName)
	clean := make([]string, 0, len(r.TargetIDs))
	for _, id := range r.TargetIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		clean = append(clean, id)
	}
	r.TargetIDs = clean
	if len(r.TargetIDs) == 0 {
		return r, fmt.Errorf("targetIds is required")
	}
	return r, nil
}

func (r WizardStartRequest) Normalize() (WizardStartRequest, error) {
	r.Mode = strings.TrimSpace(r.Mode)
	if r.Mode == "" {
		r.Mode = "local"
	}
	return r, nil
}

func (r WizardNextRequest) Normalize() (WizardNextRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	if r.Input == nil {
		r.Input = map[string]any{}
	}
	return r, nil
}

func (r WizardCancelRequest) Normalize() (WizardCancelRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" {
		return r, fmt.Errorf("id is required")
	}
	return r, nil
}

func (r WizardStatusRequest) Normalize() (WizardStatusRequest, error) {
	r.ID = strings.TrimSpace(r.ID)
	return r, nil
}

func (r TalkConfigRequest) Normalize() (TalkConfigRequest, error) { return r, nil }

func (r TalkModeRequest) Normalize() (TalkModeRequest, error) {
	r.Mode = strings.TrimSpace(r.Mode)
	if r.Mode == "" {
		return r, fmt.Errorf("mode is required")
	}
	return r, nil
}

func (r UpdateRunRequest) Normalize() (UpdateRunRequest, error) { return r, nil }

func (r LastHeartbeatRequest) Normalize() (LastHeartbeatRequest, error) { return r, nil }

func (r SetHeartbeatsRequest) Normalize() (SetHeartbeatsRequest, error) {
	if r.Enabled == nil {
		return r, fmt.Errorf("enabled is required")
	}
	if r.IntervalMS < 0 {
		return r, fmt.Errorf("interval_ms cannot be negative")
	}
	if r.Enabled != nil && *r.Enabled && r.IntervalMS == 0 {
		return r, fmt.Errorf("interval_ms is required when enabled is true")
	}
	if r.IntervalMS > 0 {
		r.IntervalMS = normalizeLimit(r.IntervalMS, 60_000, 3_600_000)
	}
	return r, nil
}

func (r WakeRequest) Normalize() (WakeRequest, error) {
	r.Source = strings.TrimSpace(r.Source)
	r.Text = strings.TrimSpace(r.Text)
	r.Mode = strings.TrimSpace(r.Mode)
	return r, nil
}

func (r SystemPresenceRequest) Normalize() (SystemPresenceRequest, error) { return r, nil }

func (r SystemEventRequest) Normalize() (SystemEventRequest, error) {
	r.Text = strings.TrimSpace(r.Text)
	r.DeviceID = strings.TrimSpace(r.DeviceID)
	r.InstanceID = strings.TrimSpace(r.InstanceID)
	r.Host = strings.TrimSpace(r.Host)
	r.IP = strings.TrimSpace(r.IP)
	r.Mode = strings.TrimSpace(r.Mode)
	r.Version = strings.TrimSpace(r.Version)
	r.Platform = strings.TrimSpace(r.Platform)
	r.DeviceFamily = strings.TrimSpace(r.DeviceFamily)
	r.ModelIdentifier = strings.TrimSpace(r.ModelIdentifier)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.Text == "" {
		return r, fmt.Errorf("text is required")
	}
	r.Roles = compactStringSlice(r.Roles)
	r.Scopes = compactStringSlice(r.Scopes)
	r.Tags = compactStringSlice(r.Tags)
	if r.LastInputSeconds < 0 {
		r.LastInputSeconds = 0
	}
	return r, nil
}

func (r SendRequest) Normalize() (SendRequest, error) {
	r.To = strings.TrimSpace(r.To)
	r.Message = strings.TrimSpace(r.Message)
	r.Text = strings.TrimSpace(r.Text)
	r.MediaURL = strings.TrimSpace(r.MediaURL)
	r.Channel = strings.ToLower(strings.TrimSpace(r.Channel))
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	if r.Message == "" && r.Text != "" {
		r.Message = r.Text
	}
	if r.To == "" {
		return r, fmt.Errorf("to is required")
	}
	if !isValidNostrIdentifier(r.To) {
		return r, fmt.Errorf("to must be a valid npub or hex pubkey")
	}
	if r.Channel != "" && r.Channel != "nostr" {
		return r, fmt.Errorf("unsupported channel: %s", r.Channel)
	}
	r.MediaURLs = compactStringSlice(r.MediaURLs)
	if r.Message == "" && r.MediaURL == "" && len(r.MediaURLs) == 0 {
		return r, fmt.Errorf("text or media is required")
	}
	if r.IdempotencyKey == "" {
		r.IdempotencyKey = fmt.Sprintf("send-%d", time.Now().UnixNano())
	}
	return r, nil
}

func (r BrowserRequestRequest) Normalize() (BrowserRequestRequest, error) {
	r.Method = strings.ToUpper(strings.TrimSpace(r.Method))
	r.Path = strings.TrimSpace(r.Path)
	if r.Method == "" || r.Path == "" {
		return r, fmt.Errorf("method and path are required")
	}
	switch r.Method {
	case "GET", "POST", "DELETE":
	default:
		return r, fmt.Errorf("method must be GET, POST, or DELETE")
	}
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("timeoutMs cannot be negative")
	}
	if r.TimeoutMS > 0 {
		r.TimeoutMS = normalizeLimit(r.TimeoutMS, 5_000, 120_000)
	}
	return r, nil
}

func (r VoicewakeGetRequest) Normalize() (VoicewakeGetRequest, error) { return r, nil }

func (r VoicewakeSetRequest) Normalize() (VoicewakeSetRequest, error) {
	clean := make([]string, 0, len(r.Triggers))
	for _, trigger := range r.Triggers {
		trigger = strings.TrimSpace(trigger)
		if trigger == "" {
			continue
		}
		clean = append(clean, trigger)
	}
	r.Triggers = clean
	return r, nil
}

func (r TTSStatusRequest) Normalize() (TTSStatusRequest, error) { return r, nil }

func (r TTSProvidersRequest) Normalize() (TTSProvidersRequest, error) { return r, nil }

func (r TTSSetProviderRequest) Normalize() (TTSSetProviderRequest, error) {
	r.Provider = strings.TrimSpace(r.Provider)
	if r.Provider == "" {
		return r, fmt.Errorf("provider is required")
	}
	return r, nil
}

func (r TTSEnableRequest) Normalize() (TTSEnableRequest, error) { return r, nil }

func (r TTSDisableRequest) Normalize() (TTSDisableRequest, error) { return r, nil }

func (r TTSConvertRequest) Normalize() (TTSConvertRequest, error) {
	r.Text = strings.TrimSpace(r.Text)
	r.Provider = strings.TrimSpace(r.Provider)
	r.Voice = strings.TrimSpace(r.Voice)
	if r.Text == "" {
		return r, fmt.Errorf("text is required")
	}
	return r, nil
}

func (r MCPAuthStartRequest) Normalize() (MCPAuthStartRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	r.ClientSecret = strings.TrimSpace(r.ClientSecret)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	if r.TimeoutMS < 0 {
		return r, fmt.Errorf("timeout_ms must be >= 0")
	}
	return r, nil
}

func (r MCPAuthRefreshRequest) Normalize() (MCPAuthRefreshRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	return r, nil
}

func (r MCPAuthClearRequest) Normalize() (MCPAuthClearRequest, error) {
	r.Server = strings.TrimSpace(r.Server)
	if r.Server == "" {
		return r, fmt.Errorf("server is required")
	}
	return r, nil
}

func SupportedMethods() []string {
	return []string{
		MethodSupportedMethods,
		MethodHealth,
		MethodDoctorMemoryStatus,
		MethodLogsTail,
		MethodRuntimeObserve,
		MethodChannelsStatus,
		MethodChannelsLogout,
		MethodChannelsJoin,
		MethodChannelsLeave,
		MethodChannelsList,
		MethodChannelsSend,
		MethodStatus,
		MethodStatusAlias,
		MethodUsageStatus,
		MethodUsageCost,
		MethodMemorySearch,
		MethodMemoryCompact,
		MethodAgent,
		MethodAgentWait,
		MethodAgentIdentityGet,
		MethodChatSend,
		MethodChatHistory,
		MethodChatAbort,
		MethodSessionGet,
		MethodSessionsList,
		MethodSessionsPreview,
		MethodSessionsPatch,
		MethodSessionsReset,
		MethodSessionsDelete,
		MethodSessionsCompact,
		MethodSessionsSpawn,
		MethodSessionsExport,
		MethodSessionsPrune,
		MethodListGet,
		MethodListPut,
		MethodRelayPolicyGet,
		MethodConfigGet,
		MethodConfigPut,
		MethodConfigSet,
		MethodConfigApply,
		MethodConfigPatch,
		MethodConfigSchema,
		MethodConfigSchemaLookup,
		MethodSecurityAudit,
		MethodACPRegister,
		MethodACPUnregister,
		MethodACPPeers,
		MethodACPDispatch,
		MethodACPPipeline,
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
		MethodPluginsInstall,
		MethodPluginsUninstall,
		MethodPluginsUpdate,
		MethodPluginsRegistryList,
		MethodPluginsRegistryGet,
		MethodPluginsRegistrySearch,
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
		MethodNodeInvoke,
		MethodNodeInvokeResult,
		MethodNodeEvent,
		MethodNodeResult,
		MethodNodePendingEnqueue,
		MethodNodePendingPull,
		MethodNodePendingAck,
		MethodNodePendingDrain,
		MethodNodeCanvasCapabilityRefresh,
		MethodCanvasGet,
		MethodCanvasList,
		MethodCanvasUpdate,
		MethodCanvasDelete,
		MethodCronList,
		MethodCronStatus,
		MethodCronAdd,
		MethodCronUpdate,
		MethodCronRemove,
		MethodCronRun,
		MethodCronRuns,
		MethodExecApprovalsGet,
		MethodExecApprovalsSet,
		MethodExecApprovalsNodeGet,
		MethodExecApprovalsNodeSet,
		MethodExecApprovalRequest,
		MethodExecApprovalWaitDecision,
		MethodExecApprovalResolve,
		MethodMCPAuthStart,
		MethodMCPAuthRefresh,
		MethodMCPAuthClear,
		MethodSecretsReload,
		MethodSandboxRun,
		MethodSecretsResolve,
		MethodWizardStart,
		MethodWizardNext,
		MethodWizardCancel,
		MethodWizardStatus,
		MethodUpdateRun,
		MethodTalkConfig,
		MethodTalkMode,
		MethodGatewayIdentityGet,
		MethodLastHeartbeat,
		MethodSetHeartbeats,
		MethodWake,
		MethodSystemPresence,
		MethodSystemEvent,
		MethodSend,
		MethodBrowserRequest,
		MethodVoicewakeGet,
		MethodVoicewakeSet,
		MethodTTSStatus,
		MethodTTSProviders,
		MethodTTSSetProvider,
		MethodTTSEnable,
		MethodTTSDisable,
		MethodTTSConvert,
		MethodHooksList,
		MethodHooksEnable,
		MethodHooksDisable,
		MethodHooksInfo,
		MethodHooksCheck,
	}
}

func DecodeMemorySearchParams(params json.RawMessage) (MemorySearchRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return MemorySearchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return MemorySearchRequest{}, fmt.Errorf("invalid params")
		}
		query, ok := arr[0].(string)
		if !ok {
			return MemorySearchRequest{}, fmt.Errorf("invalid params")
		}
		req := MemorySearchRequest{Query: query}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return MemorySearchRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			}
		}
		return req, nil
	}
	return decodeMethodParams[MemorySearchRequest](params)
}

func DecodeAgentParams(params json.RawMessage) (AgentRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 4 {
			return AgentRequest{}, fmt.Errorf("invalid params")
		}
		message, ok := arr[0].(string)
		if !ok {
			return AgentRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentRequest{Message: message}
		if len(arr) > 1 {
			sessionID, ok := arr[1].(string)
			if !ok {
				return AgentRequest{}, fmt.Errorf("invalid params")
			}
			req.SessionID = sessionID
		}
		if len(arr) > 2 {
			contextText, ok := arr[2].(string)
			if !ok {
				return AgentRequest{}, fmt.Errorf("invalid params")
			}
			req.Context = contextText
		}
		if len(arr) > 3 {
			switch v := arr[3].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentRequest{}, fmt.Errorf("invalid params")
				}
				req.TimeoutMS = int(v)
			case int:
				req.TimeoutMS = v
			default:
				return AgentRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[AgentRequest](params)
}

func DecodeAgentWaitParams(params json.RawMessage) (AgentWaitRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentWaitRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return AgentWaitRequest{}, fmt.Errorf("invalid params")
		}
		runID, ok := arr[0].(string)
		if !ok {
			return AgentWaitRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentWaitRequest{RunID: runID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentWaitRequest{}, fmt.Errorf("invalid params")
				}
				req.TimeoutMS = int(v)
			case int:
				req.TimeoutMS = v
			default:
				return AgentWaitRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[AgentWaitRequest](params)
}

func DecodeAgentIdentityParams(params json.RawMessage) (AgentIdentityRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentIdentityRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 2 {
			return AgentIdentityRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentIdentityRequest{}
		if len(arr) > 0 {
			sessionID, ok := arr[0].(string)
			if !ok {
				return AgentIdentityRequest{}, fmt.Errorf("invalid params")
			}
			req.SessionID = sessionID
		}
		if len(arr) > 1 {
			agentID, ok := arr[1].(string)
			if !ok {
				return AgentIdentityRequest{}, fmt.Errorf("invalid params")
			}
			req.AgentID = agentID
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return AgentIdentityRequest{}, nil
	}
	return decodeMethodParams[AgentIdentityRequest](params)
}

func DecodeChatSendParams(params json.RawMessage) (ChatSendRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		to, ok := arr[0].(string)
		if !ok {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		text, ok := arr[1].(string)
		if !ok {
			return ChatSendRequest{}, fmt.Errorf("invalid params")
		}
		return ChatSendRequest{To: to, Text: text}, nil
	}
	type chatSendCompatRequest struct {
		To             string            `json:"to,omitempty"`
		Text           string            `json:"text,omitempty"`
		SessionID      string            `json:"session_id,omitempty"`
		SessionIDCamel string            `json:"sessionId,omitempty"`
		SessionKey     string            `json:"sessionKey,omitempty"`
		Message        string            `json:"message,omitempty"`
		Thinking       string            `json:"thinking,omitempty"`
		Deliver        *bool             `json:"deliver,omitempty"`
		TimeoutMS      int               `json:"timeoutMs,omitempty"`
		IdempotencyKey string            `json:"idempotencyKey,omitempty"`
		IdempotencyAlt string            `json:"idempotency_key,omitempty"`
		RunID          string            `json:"runId,omitempty"`
		RunIDAlt       string            `json:"run_id,omitempty"`
		Attachments    []AttachmentInput `json:"attachments,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat chatSendCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ChatSendRequest{}, fmt.Errorf("invalid params")
	}
	to := strings.TrimSpace(compat.To)
	if to == "" {
		to = strings.TrimSpace(compat.SessionID)
	}
	if to == "" {
		to = strings.TrimSpace(compat.SessionIDCamel)
	}
	if to == "" {
		to = strings.TrimSpace(compat.SessionKey)
	}
	text := strings.TrimSpace(compat.Text)
	if text == "" {
		text = strings.TrimSpace(compat.Message)
	}
	idempotency := strings.TrimSpace(compat.IdempotencyKey)
	if idempotency == "" {
		idempotency = strings.TrimSpace(compat.IdempotencyAlt)
	}
	runID := strings.TrimSpace(compat.RunID)
	if runID == "" {
		runID = strings.TrimSpace(compat.RunIDAlt)
	}
	return ChatSendRequest{To: to, Text: text, IdempotencyKey: idempotency, RunID: runID, Attachments: compat.Attachments}, nil
}

func DecodeSessionGetParams(params json.RawMessage) (SessionGetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionGetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionGetRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionGetRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionGetRequest{SessionID: sessionID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return SessionGetRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			}
		}
		return req, nil
	}
	type sessionGetCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
		Limit          int    `json:"limit,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionGetCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionGetRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionGetRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key), Limit: compat.Limit}, nil
}

func DecodeChatHistoryParams(params json.RawMessage) (ChatHistoryRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChatHistoryRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return ChatHistoryRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return ChatHistoryRequest{}, fmt.Errorf("invalid params")
		}
		req := ChatHistoryRequest{SessionID: sessionID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return ChatHistoryRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			}
		}
		return req, nil
	}
	type chatHistoryCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Limit          int    `json:"limit,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat chatHistoryCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ChatHistoryRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	return ChatHistoryRequest{SessionID: sessionID, Limit: compat.Limit}, nil
}

func DecodeChatAbortParams(params json.RawMessage) (ChatAbortRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChatAbortRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return ChatAbortRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 {
			return ChatAbortRequest{}, nil
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return ChatAbortRequest{}, fmt.Errorf("invalid params")
		}
		return ChatAbortRequest{SessionID: sessionID}, nil
	}
	type chatAbortCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		RunID          string `json:"run_id,omitempty"`
		RunIDCamel     string `json:"runId,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat chatAbortCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ChatAbortRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	runID := strings.TrimSpace(compat.RunID)
	if runID == "" {
		runID = strings.TrimSpace(compat.RunIDCamel)
	}
	return ChatAbortRequest{SessionID: sessionID, RunID: runID}, nil
}

func DecodeSessionsListParams(params json.RawMessage) (SessionsListRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return SessionsListRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionsListRequest{}
		if len(arr) == 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return SessionsListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return SessionsListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[SessionsListRequest](params)
}

func DecodeSessionsPreviewParams(params json.RawMessage) (SessionsPreviewRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionsPreviewRequest{SessionID: sessionID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	type sessionsPreviewCompatRequest struct {
		SessionID      string   `json:"session_id,omitempty"`
		SessionIDCamel string   `json:"sessionId,omitempty"`
		SessionKey     string   `json:"sessionKey,omitempty"`
		Key            string   `json:"key,omitempty"`
		Keys           []string `json:"keys,omitempty"`
		Limit          int      `json:"limit,omitempty"`
		MaxChars       int      `json:"maxChars,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsPreviewCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsPreviewRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionsPreviewRequest{
		SessionID: sessionID,
		Key:       strings.TrimSpace(compat.Key),
		Keys:      compat.Keys,
		Limit:     compat.Limit,
		MaxChars:  compat.MaxChars,
	}, nil
}

func DecodeSessionsPatchParams(params json.RawMessage) (SessionsPatchRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsPatchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionsPatchRequest{}, fmt.Errorf("invalid params")
		}
		var req SessionsPatchRequest
		if err := json.Unmarshal(arr[0], &req.SessionID); err != nil {
			return SessionsPatchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 2 {
			if err := json.Unmarshal(arr[1], &req.Meta); err != nil {
				return SessionsPatchRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	type sessionsPatchCompatRequest struct {
		SessionID      string         `json:"session_id,omitempty"`
		SessionIDCamel string         `json:"sessionId,omitempty"`
		SessionKey     string         `json:"sessionKey,omitempty"`
		Key            string         `json:"key,omitempty"`
		Meta           map[string]any `json:"meta,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsPatchCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsPatchRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionsPatchRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key), Meta: compat.Meta}, nil
}

func DecodeSessionsResetParams(params json.RawMessage) (SessionsResetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsResetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return SessionsResetRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionsResetRequest{}, fmt.Errorf("invalid params")
		}
		return SessionsResetRequest{SessionID: sessionID}, nil
	}
	type sessionsResetCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsResetCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsResetRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionsResetRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key)}, nil
}

func DecodeSessionsDeleteParams(params json.RawMessage) (SessionsDeleteRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsDeleteRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return SessionsDeleteRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionsDeleteRequest{}, fmt.Errorf("invalid params")
		}
		return SessionsDeleteRequest{SessionID: sessionID}, nil
	}
	type sessionsDeleteCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsDeleteCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsDeleteRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	return SessionsDeleteRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key)}, nil
}

func DecodeSessionsCompactParams(params json.RawMessage) (SessionsCompactRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return SessionsCompactRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return SessionsCompactRequest{}, fmt.Errorf("invalid params")
		}
		sessionID, ok := arr[0].(string)
		if !ok {
			return SessionsCompactRequest{}, fmt.Errorf("invalid params")
		}
		req := SessionsCompactRequest{SessionID: sessionID}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return SessionsCompactRequest{}, fmt.Errorf("invalid params")
				}
				req.Keep = int(v)
			case int:
				req.Keep = v
			default:
				return SessionsCompactRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	type sessionsCompactCompatRequest struct {
		SessionID      string `json:"session_id,omitempty"`
		SessionIDCamel string `json:"sessionId,omitempty"`
		SessionKey     string `json:"sessionKey,omitempty"`
		Key            string `json:"key,omitempty"`
		Keep           int    `json:"keep,omitempty"`
		MaxLines       int    `json:"maxLines,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat sessionsCompactCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return SessionsCompactRequest{}, fmt.Errorf("invalid params")
	}
	sessionID := strings.TrimSpace(compat.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionIDCamel)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.SessionKey)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(compat.Key)
	}
	keep := compat.Keep
	if keep <= 0 {
		keep = compat.MaxLines
	}
	return SessionsCompactRequest{SessionID: sessionID, Key: strings.TrimSpace(compat.Key), Keep: keep}, nil
}

func DecodeConfigPutParams(params json.RawMessage) (ConfigPutRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigPutRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return ConfigPutRequest{}, fmt.Errorf("invalid params")
		}
		var cfg state.ConfigDoc
		if err := json.Unmarshal(arr[0], &cfg); err != nil {
			return ConfigPutRequest{}, fmt.Errorf("invalid params")
		}
		req := ConfigPutRequest{Config: cfg}
		if len(arr) == 2 {
			expectedVersionSet, err := decodeWritePrecondition(arr[1], &req.ExpectedVersion, &req.ExpectedEvent, &req.BaseHash)
			if err != nil {
				return ConfigPutRequest{}, fmt.Errorf("invalid params")
			}
			req.ExpectedVersionSet = expectedVersionSet
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type configPutCompatRequest struct {
		Config          state.ConfigDoc `json:"config"`
		ExpectedVersion *int            `json:"expected_version,omitempty"`
		ExpectedEvent   string          `json:"expected_event,omitempty"`
		BaseHash        string          `json:"baseHash,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat configPutCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ConfigPutRequest{}, fmt.Errorf("invalid params")
	}
	req := ConfigPutRequest{Config: compat.Config, ExpectedEvent: compat.ExpectedEvent, BaseHash: compat.BaseHash}
	if compat.ExpectedVersion != nil {
		req.ExpectedVersionSet = true
		req.ExpectedVersion = *compat.ExpectedVersion
	}
	return req, nil
}

func DecodeConfigSetParams(params json.RawMessage) (ConfigSetRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		var req ConfigSetRequest
		if err := json.Unmarshal(arr[0], &req.Key); err != nil {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		if err := json.Unmarshal(arr[1], &req.Value); err != nil {
			return ConfigSetRequest{}, fmt.Errorf("invalid params")
		}
		return req, nil
	}
	return decodeMethodParams[ConfigSetRequest](params)
}

func DecodeConfigApplyParams(params json.RawMessage) (ConfigApplyRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigApplyRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ConfigApplyRequest{}, fmt.Errorf("invalid params")
		}
		var rawString string
		if err := json.Unmarshal(arr[0], &rawString); err == nil {
			return ConfigApplyRequest{Raw: rawString}, nil
		}
		var cfg state.ConfigDoc
		if err := json.Unmarshal(arr[0], &cfg); err != nil {
			return ConfigApplyRequest{}, fmt.Errorf("invalid params")
		}
		return ConfigApplyRequest{Config: cfg}, nil
	}
	return decodeMethodParams[ConfigApplyRequest](params)
}

func DecodeConfigPatchParams(params json.RawMessage) (ConfigPatchRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ConfigPatchRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ConfigPatchRequest{}, fmt.Errorf("invalid params")
		}
		var rawString string
		if err := json.Unmarshal(arr[0], &rawString); err == nil {
			return ConfigPatchRequest{Raw: rawString}, nil
		}
		var patch map[string]any
		if err := json.Unmarshal(arr[0], &patch); err != nil {
			return ConfigPatchRequest{}, fmt.Errorf("invalid params")
		}
		return ConfigPatchRequest{Patch: patch}, nil
	}
	req, err := decodeMethodParams[ConfigPatchRequest](params)
	if err != nil {
		return ConfigPatchRequest{}, err
	}
	if req.Raw != "" || len(req.Patch) > 0 {
		return req, nil
	}
	patch, err := decodeMethodParams[map[string]any](params)
	if err != nil {
		return ConfigPatchRequest{}, err
	}
	return ConfigPatchRequest{Patch: patch}, nil
}

func DecodeLogsTailParams(params json.RawMessage) (LogsTailRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return LogsTailRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 3 {
			return LogsTailRequest{}, fmt.Errorf("invalid params")
		}
		req := LogsTailRequest{}
		if len(arr) >= 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return LogsTailRequest{}, fmt.Errorf("invalid params")
				}
				req.Cursor = int64(v)
			case int:
				req.Cursor = int64(v)
			}
		}
		if len(arr) >= 2 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return LogsTailRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return LogsTailRequest{}, fmt.Errorf("invalid params")
			}
		}
		if len(arr) == 3 {
			switch v := arr[2].(type) {
			case float64:
				if math.Trunc(v) != v {
					return LogsTailRequest{}, fmt.Errorf("invalid params")
				}
				req.MaxBytes = int(v)
			case int:
				req.MaxBytes = v
			default:
				return LogsTailRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[LogsTailRequest](params)
}

func DecodeRuntimeObserveParams(params json.RawMessage) (RuntimeObserveRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return RuntimeObserveRequest{}, nil
	}
	return decodeMethodParams[RuntimeObserveRequest](params)
}

func DecodeChannelsStatusParams(params json.RawMessage) (ChannelsStatusRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 2 {
			return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
		}
		req := ChannelsStatusRequest{}
		if len(arr) >= 1 {
			b, ok := arr[0].(bool)
			if !ok {
				return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
			}
			req.Probe = b
		}
		if len(arr) == 2 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
				}
				req.TimeoutMS = int(v)
			case int:
				req.TimeoutMS = v
			default:
				return ChannelsStatusRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[ChannelsStatusRequest](params)
}

func DecodeChannelsLogoutParams(params json.RawMessage) (ChannelsLogoutRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ChannelsLogoutRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ChannelsLogoutRequest{}, fmt.Errorf("invalid params")
		}
		channel, ok := arr[0].(string)
		if !ok {
			return ChannelsLogoutRequest{}, fmt.Errorf("invalid params")
		}
		return ChannelsLogoutRequest{Channel: channel}, nil
	}
	return decodeMethodParams[ChannelsLogoutRequest](params)
}

func DecodeChannelsJoinParams(params json.RawMessage) (ChannelsJoinRequest, error) {
	return decodeMethodParams[ChannelsJoinRequest](params)
}

func DecodeChannelsLeaveParams(params json.RawMessage) (ChannelsLeaveRequest, error) {
	return decodeMethodParams[ChannelsLeaveRequest](params)
}

func DecodeChannelsListParams(params json.RawMessage) (ChannelsListRequest, error) {
	return ChannelsListRequest{}, nil
}

func DecodeChannelsSendParams(params json.RawMessage) (ChannelsSendRequest, error) {
	return decodeMethodParams[ChannelsSendRequest](params)
}

func DecodeUsageCostParams(params json.RawMessage) (UsageCostRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return UsageCostRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return UsageCostRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 {
			return UsageCostRequest{}, nil
		}
		var req UsageCostRequest
		if err := json.Unmarshal(arr[0], &req); err != nil {
			return UsageCostRequest{}, fmt.Errorf("invalid params")
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return UsageCostRequest{}, nil
	}
	return decodeMethodParams[UsageCostRequest](params)
}

func DecodeListGetParams(params json.RawMessage) (ListGetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ListGetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return ListGetRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[0].(string)
		if !ok {
			return ListGetRequest{}, fmt.Errorf("invalid params")
		}
		return ListGetRequest{Name: name}, nil
	}
	return decodeMethodParams[ListGetRequest](params)
}

func DecodeListPutParams(params json.RawMessage) (ListPutRequest, error) {
	if isJSONArray(params) {
		var arr []json.RawMessage
		if err := json.Unmarshal(params, &arr); err != nil {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) < 2 || len(arr) > 3 {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		var name string
		if err := json.Unmarshal(arr[0], &name); err != nil {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		var items []string
		if err := json.Unmarshal(arr[1], &items); err != nil {
			return ListPutRequest{}, fmt.Errorf("invalid params")
		}
		req := ListPutRequest{Name: name, Items: items}
		if len(arr) == 3 {
			expectedVersionSet, err := decodeWritePrecondition(arr[2], &req.ExpectedVersion, &req.ExpectedEvent, nil)
			if err != nil {
				return ListPutRequest{}, fmt.Errorf("invalid params")
			}
			req.ExpectedVersionSet = expectedVersionSet
		}
		return req, nil
	}
	params = normalizeObjectParamAliases(params)
	type listPutCompatRequest struct {
		Name            string   `json:"name"`
		Items           []string `json:"items"`
		ExpectedVersion *int     `json:"expected_version,omitempty"`
		ExpectedEvent   string   `json:"expected_event,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var compat listPutCompatRequest
	if err := dec.Decode(&compat); err != nil {
		return ListPutRequest{}, fmt.Errorf("invalid params")
	}
	req := ListPutRequest{Name: compat.Name, Items: compat.Items, ExpectedEvent: compat.ExpectedEvent}
	if compat.ExpectedVersion != nil {
		req.ExpectedVersionSet = true
		req.ExpectedVersion = *compat.ExpectedVersion
	}
	return req, nil
}

func DecodeAgentsListParams(params json.RawMessage) (AgentsListRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return AgentsListRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentsListRequest{}
		if len(arr) == 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentsListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return AgentsListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return AgentsListRequest{}, nil
	}
	return decodeMethodParams[AgentsListRequest](params)
}

func DecodeAgentsCreateParams(params json.RawMessage) (AgentsCreateRequest, error) {
	return decodeMethodParams[AgentsCreateRequest](params)
}

func DecodeAgentsUpdateParams(params json.RawMessage) (AgentsUpdateRequest, error) {
	return decodeMethodParams[AgentsUpdateRequest](params)
}

func DecodeAgentsDeleteParams(params json.RawMessage) (AgentsDeleteRequest, error) {
	return decodeMethodParams[AgentsDeleteRequest](params)
}

func DecodeAgentsAssignParams(params json.RawMessage) (AgentsAssignRequest, error) {
	return decodeMethodParams[AgentsAssignRequest](params)
}

func DecodeAgentsUnassignParams(params json.RawMessage) (AgentsUnassignRequest, error) {
	return decodeMethodParams[AgentsUnassignRequest](params)
}

func DecodeAgentsActiveParams(params json.RawMessage) (AgentsActiveRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return AgentsActiveRequest{}, nil
	}
	return decodeMethodParams[AgentsActiveRequest](params)
}

func DecodeAgentsFilesListParams(params json.RawMessage) (AgentsFilesListRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 2 {
			return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
		}
		agentID, ok := arr[0].(string)
		if !ok {
			return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
		}
		req := AgentsFilesListRequest{AgentID: agentID}
		if len(arr) == 2 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return AgentsFilesListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[AgentsFilesListRequest](params)
}

func DecodeAgentsFilesGetParams(params json.RawMessage) (AgentsFilesGetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		agentID, ok := arr[0].(string)
		if !ok {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[1].(string)
		if !ok {
			return AgentsFilesGetRequest{}, fmt.Errorf("invalid params")
		}
		return AgentsFilesGetRequest{AgentID: agentID, Name: name}, nil
	}
	return decodeMethodParams[AgentsFilesGetRequest](params)
}

func DecodeAgentsFilesSetParams(params json.RawMessage) (AgentsFilesSetRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 3 {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		agentID, ok := arr[0].(string)
		if !ok {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[1].(string)
		if !ok {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		content, ok := arr[2].(string)
		if !ok {
			return AgentsFilesSetRequest{}, fmt.Errorf("invalid params")
		}
		return AgentsFilesSetRequest{AgentID: agentID, Name: name, Content: content}, nil
	}
	return decodeMethodParams[AgentsFilesSetRequest](params)
}

func DecodeModelsListParams(params json.RawMessage) (ModelsListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ModelsListRequest{}, nil
	}
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return ModelsListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 0 {
			return ModelsListRequest{}, fmt.Errorf("invalid params")
		}
		return ModelsListRequest{}, nil
	}
	return decodeMethodParams[ModelsListRequest](params)
}

func DecodeToolsCatalogParams(params json.RawMessage) (ToolsCatalogRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolsCatalogRequest{}, nil
	}
	return decodeMethodParams[ToolsCatalogRequest](params)
}

func DecodeToolsProfileGetParams(params json.RawMessage) (ToolsProfileGetRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolsProfileGetRequest{}, nil
	}
	return decodeMethodParams[ToolsProfileGetRequest](params)
}

func DecodeToolsProfileSetParams(params json.RawMessage) (ToolsProfileSetRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolsProfileSetRequest{}, fmt.Errorf("profile is required")
	}
	return decodeMethodParams[ToolsProfileSetRequest](params)
}

func DecodeSkillsStatusParams(params json.RawMessage) (SkillsStatusRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return SkillsStatusRequest{}, nil
	}
	return decodeMethodParams[SkillsStatusRequest](params)
}

func DecodeSkillsBinsParams(params json.RawMessage) (SkillsBinsRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return SkillsBinsRequest{}, nil
	}
	return decodeMethodParams[SkillsBinsRequest](params)
}

func DecodeSkillsInstallParams(params json.RawMessage) (SkillsInstallRequest, error) {
	return decodeMethodParams[SkillsInstallRequest](params)
}

func DecodeSkillsUpdateParams(params json.RawMessage) (SkillsUpdateRequest, error) {
	return decodeMethodParams[SkillsUpdateRequest](params)
}

func DecodePluginsInstallParams(params json.RawMessage) (PluginsInstallRequest, error) {
	return decodeMethodParams[PluginsInstallRequest](params)
}

func DecodePluginsUninstallParams(params json.RawMessage) (PluginsUninstallRequest, error) {
	return decodeMethodParams[PluginsUninstallRequest](params)
}

func DecodePluginsUpdateParams(params json.RawMessage) (PluginsUpdateRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return PluginsUpdateRequest{}, nil
	}
	return decodeMethodParams[PluginsUpdateRequest](params)
}

func DecodePluginsRegistryListParams(params json.RawMessage) (PluginsRegistryListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return PluginsRegistryListRequest{}, nil
	}
	return decodeMethodParams[PluginsRegistryListRequest](params)
}

func DecodePluginsRegistryGetParams(params json.RawMessage) (PluginsRegistryGetRequest, error) {
	return decodeMethodParams[PluginsRegistryGetRequest](params)
}

func DecodePluginsRegistrySearchParams(params json.RawMessage) (PluginsRegistrySearchRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return PluginsRegistrySearchRequest{}, nil
	}
	return decodeMethodParams[PluginsRegistrySearchRequest](params)
}

func DecodeNodePairRequestParams(params json.RawMessage) (NodePairRequest, error) {
	return decodeMethodParams[NodePairRequest](params)
}

func DecodeNodePairListParams(params json.RawMessage) (NodePairListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return NodePairListRequest{}, nil
	}
	return decodeMethodParams[NodePairListRequest](params)
}

func DecodeNodePairApproveParams(params json.RawMessage) (NodePairApproveRequest, error) {
	return decodeMethodParams[NodePairApproveRequest](params)
}

func DecodeNodePairRejectParams(params json.RawMessage) (NodePairRejectRequest, error) {
	return decodeMethodParams[NodePairRejectRequest](params)
}

func DecodeNodePairVerifyParams(params json.RawMessage) (NodePairVerifyRequest, error) {
	return decodeMethodParams[NodePairVerifyRequest](params)
}

func DecodeDevicePairListParams(params json.RawMessage) (DevicePairListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return DevicePairListRequest{}, nil
	}
	return decodeMethodParams[DevicePairListRequest](params)
}

func DecodeDevicePairApproveParams(params json.RawMessage) (DevicePairApproveRequest, error) {
	return decodeMethodParams[DevicePairApproveRequest](params)
}

func DecodeDevicePairRejectParams(params json.RawMessage) (DevicePairRejectRequest, error) {
	return decodeMethodParams[DevicePairRejectRequest](params)
}

func DecodeDevicePairRemoveParams(params json.RawMessage) (DevicePairRemoveRequest, error) {
	return decodeMethodParams[DevicePairRemoveRequest](params)
}

func DecodeDeviceTokenRotateParams(params json.RawMessage) (DeviceTokenRotateRequest, error) {
	return decodeMethodParams[DeviceTokenRotateRequest](params)
}

func DecodeDeviceTokenRevokeParams(params json.RawMessage) (DeviceTokenRevokeRequest, error) {
	return decodeMethodParams[DeviceTokenRevokeRequest](params)
}

func DecodeNodeListParams(params json.RawMessage) (NodeListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return NodeListRequest{}, nil
	}
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return NodeListRequest{}, fmt.Errorf("invalid params")
		}
		req := NodeListRequest{}
		if len(arr) == 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return NodeListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return NodeListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[NodeListRequest](params)
}

func DecodeNodeDescribeParams(params json.RawMessage) (NodeDescribeRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeDescribeRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return NodeDescribeRequest{}, fmt.Errorf("invalid params")
		}
		nodeID, ok := arr[0].(string)
		if !ok {
			return NodeDescribeRequest{}, fmt.Errorf("invalid params")
		}
		return NodeDescribeRequest{NodeID: nodeID}, nil
	}
	return decodeMethodParams[NodeDescribeRequest](params)
}

func DecodeNodeRenameParams(params json.RawMessage) (NodeRenameRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeRenameRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 2 {
			return NodeRenameRequest{}, fmt.Errorf("invalid params")
		}
		nodeID, ok := arr[0].(string)
		if !ok {
			return NodeRenameRequest{}, fmt.Errorf("invalid params")
		}
		name, ok := arr[1].(string)
		if !ok {
			return NodeRenameRequest{}, fmt.Errorf("invalid params")
		}
		return NodeRenameRequest{NodeID: nodeID, Name: name}, nil
	}
	return decodeMethodParams[NodeRenameRequest](params)
}

func DecodeNodeCanvasCapabilityRefreshParams(params json.RawMessage) (NodeCanvasCapabilityRefreshRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeCanvasCapabilityRefreshRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return NodeCanvasCapabilityRefreshRequest{}, fmt.Errorf("invalid params")
		}
		nodeID, ok := arr[0].(string)
		if !ok {
			return NodeCanvasCapabilityRefreshRequest{}, fmt.Errorf("invalid params")
		}
		return NodeCanvasCapabilityRefreshRequest{NodeID: nodeID}, nil
	}
	return decodeMethodParams[NodeCanvasCapabilityRefreshRequest](params)
}

func DecodeNodeInvokeParams(params json.RawMessage) (NodeInvokeRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return NodeInvokeRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) == 0 || len(arr) > 4 {
			return NodeInvokeRequest{}, fmt.Errorf("invalid params")
		}
		nodeID, ok := arr[0].(string)
		if !ok {
			return NodeInvokeRequest{}, fmt.Errorf("invalid params")
		}
		req := NodeInvokeRequest{NodeID: nodeID}
		if len(arr) > 1 {
			command, ok := arr[1].(string)
			if !ok {
				return NodeInvokeRequest{}, fmt.Errorf("invalid params")
			}
			req.Command = command
		}
		if len(arr) > 2 {
			args, ok := arr[2].(map[string]any)
			if !ok {
				return NodeInvokeRequest{}, fmt.Errorf("invalid params")
			}
			req.Args = args
		}
		if len(arr) > 3 {
			switch v := arr[3].(type) {
			case float64:
				if math.Trunc(v) != v {
					return NodeInvokeRequest{}, fmt.Errorf("invalid params")
				}
				req.TimeoutMS = int(v)
			case int:
				req.TimeoutMS = v
			default:
				return NodeInvokeRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[NodeInvokeRequest](params)
}

func DecodeNodeEventParams(params json.RawMessage) (NodeEventRequest, error) {
	return decodeMethodParams[NodeEventRequest](params)
}

func DecodeNodeResultParams(params json.RawMessage) (NodeResultRequest, error) {
	return decodeMethodParams[NodeResultRequest](params)
}

func DecodeNodePendingEnqueueParams(params json.RawMessage) (NodePendingEnqueueRequest, error) {
	return decodeMethodParams[NodePendingEnqueueRequest](params)
}

func DecodeNodePendingPullParams(params json.RawMessage) (NodePendingPullRequest, error) {
	return decodeMethodParams[NodePendingPullRequest](params)
}

func DecodeNodePendingAckParams(params json.RawMessage) (NodePendingAckRequest, error) {
	return decodeMethodParams[NodePendingAckRequest](params)
}

func DecodeNodePendingDrainParams(params json.RawMessage) (NodePendingDrainRequest, error) {
	return decodeMethodParams[NodePendingDrainRequest](params)
}

func DecodeCronListParams(params json.RawMessage) (CronListRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return CronListRequest{}, nil
	}
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronListRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 1 {
			return CronListRequest{}, fmt.Errorf("invalid params")
		}
		req := CronListRequest{}
		if len(arr) == 1 {
			switch v := arr[0].(type) {
			case float64:
				if math.Trunc(v) != v {
					return CronListRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return CronListRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	return decodeMethodParams[CronListRequest](params)
}

func DecodeCronStatusParams(params json.RawMessage) (CronStatusRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronStatusRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return CronStatusRequest{}, fmt.Errorf("invalid params")
		}
		id, ok := arr[0].(string)
		if !ok {
			return CronStatusRequest{}, fmt.Errorf("invalid params")
		}
		return CronStatusRequest{ID: id}, nil
	}
	return decodeMethodParams[CronStatusRequest](params)
}

func DecodeCronAddParams(params json.RawMessage) (CronAddRequest, error) {
	return decodeMethodParams[CronAddRequest](params)
}

func DecodeCronUpdateParams(params json.RawMessage) (CronUpdateRequest, error) {
	return decodeMethodParams[CronUpdateRequest](params)
}

func DecodeCronRemoveParams(params json.RawMessage) (CronRemoveRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronRemoveRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return CronRemoveRequest{}, fmt.Errorf("invalid params")
		}
		id, ok := arr[0].(string)
		if !ok {
			return CronRemoveRequest{}, fmt.Errorf("invalid params")
		}
		return CronRemoveRequest{ID: id}, nil
	}
	return decodeMethodParams[CronRemoveRequest](params)
}

func DecodeCronRunParams(params json.RawMessage) (CronRunRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronRunRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return CronRunRequest{}, fmt.Errorf("invalid params")
		}
		id, ok := arr[0].(string)
		if !ok {
			return CronRunRequest{}, fmt.Errorf("invalid params")
		}
		return CronRunRequest{ID: id}, nil
	}
	return decodeMethodParams[CronRunRequest](params)
}

func DecodeCronRunsParams(params json.RawMessage) (CronRunsRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return CronRunsRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) > 2 {
			return CronRunsRequest{}, fmt.Errorf("invalid params")
		}
		req := CronRunsRequest{}
		if len(arr) > 0 {
			id, ok := arr[0].(string)
			if !ok {
				return CronRunsRequest{}, fmt.Errorf("invalid params")
			}
			req.ID = id
		}
		if len(arr) > 1 {
			switch v := arr[1].(type) {
			case float64:
				if math.Trunc(v) != v {
					return CronRunsRequest{}, fmt.Errorf("invalid params")
				}
				req.Limit = int(v)
			case int:
				req.Limit = v
			default:
				return CronRunsRequest{}, fmt.Errorf("invalid params")
			}
		}
		return req, nil
	}
	if len(bytes.TrimSpace(params)) == 0 {
		return CronRunsRequest{}, nil
	}
	return decodeMethodParams[CronRunsRequest](params)
}

func DecodeExecApprovalsGetParams(params json.RawMessage) (ExecApprovalsGetRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ExecApprovalsGetRequest{}, nil
	}
	return decodeMethodParams[ExecApprovalsGetRequest](params)
}

func DecodeExecApprovalsSetParams(params json.RawMessage) (ExecApprovalsSetRequest, error) {
	return decodeMethodParams[ExecApprovalsSetRequest](params)
}

func DecodeExecApprovalsNodeGetParams(params json.RawMessage) (ExecApprovalsNodeGetRequest, error) {
	return decodeMethodParams[ExecApprovalsNodeGetRequest](params)
}

func DecodeExecApprovalsNodeSetParams(params json.RawMessage) (ExecApprovalsNodeSetRequest, error) {
	return decodeMethodParams[ExecApprovalsNodeSetRequest](params)
}

func DecodeExecApprovalRequestParams(params json.RawMessage) (ExecApprovalRequestRequest, error) {
	return decodeMethodParams[ExecApprovalRequestRequest](params)
}

func DecodeExecApprovalWaitDecisionParams(params json.RawMessage) (ExecApprovalWaitDecisionRequest, error) {
	return decodeMethodParams[ExecApprovalWaitDecisionRequest](params)
}

func DecodeExecApprovalResolveParams(params json.RawMessage) (ExecApprovalResolveRequest, error) {
	return decodeMethodParams[ExecApprovalResolveRequest](params)
}

func DecodeSandboxRunParams(params json.RawMessage) (SandboxRunRequest, error) {
	return decodeMethodParams[SandboxRunRequest](params)
}

func DecodeMCPAuthStartParams(params json.RawMessage) (MCPAuthStartRequest, error) {
	return decodeMethodParams[MCPAuthStartRequest](params)
}

func DecodeMCPAuthRefreshParams(params json.RawMessage) (MCPAuthRefreshRequest, error) {
	return decodeMethodParams[MCPAuthRefreshRequest](params)
}

func DecodeMCPAuthClearParams(params json.RawMessage) (MCPAuthClearRequest, error) {
	return decodeMethodParams[MCPAuthClearRequest](params)
}

func DecodeSecretsReloadParams(params json.RawMessage) (SecretsReloadRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return SecretsReloadRequest{}, nil
	}
	return decodeMethodParams[SecretsReloadRequest](params)
}

func DecodeSecretsResolveParams(params json.RawMessage) (SecretsResolveRequest, error) {
	return decodeMethodParams[SecretsResolveRequest](params)
}

func DecodeWizardStartParams(params json.RawMessage) (WizardStartRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return WizardStartRequest{}, nil
	}
	return decodeMethodParams[WizardStartRequest](params)
}

func DecodeWizardNextParams(params json.RawMessage) (WizardNextRequest, error) {
	return decodeMethodParams[WizardNextRequest](params)
}

func DecodeWizardCancelParams(params json.RawMessage) (WizardCancelRequest, error) {
	return decodeMethodParams[WizardCancelRequest](params)
}

func DecodeWizardStatusParams(params json.RawMessage) (WizardStatusRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return WizardStatusRequest{}, nil
	}
	return decodeMethodParams[WizardStatusRequest](params)
}

func DecodeUpdateRunParams(params json.RawMessage) (UpdateRunRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return UpdateRunRequest{}, nil
	}
	return decodeMethodParams[UpdateRunRequest](params)
}

func DecodeTalkConfigParams(params json.RawMessage) (TalkConfigRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TalkConfigRequest{}, nil
	}
	return decodeMethodParams[TalkConfigRequest](params)
}

func DecodeTalkModeParams(params json.RawMessage) (TalkModeRequest, error) {
	return decodeMethodParams[TalkModeRequest](params)
}

func DecodeLastHeartbeatParams(params json.RawMessage) (LastHeartbeatRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return LastHeartbeatRequest{}, nil
	}
	return decodeMethodParams[LastHeartbeatRequest](params)
}

func DecodeSetHeartbeatsParams(params json.RawMessage) (SetHeartbeatsRequest, error) {
	return decodeMethodParams[SetHeartbeatsRequest](params)
}

func DecodeWakeParams(params json.RawMessage) (WakeRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return WakeRequest{}, nil
	}
	return decodeMethodParams[WakeRequest](params)
}

func DecodeSystemPresenceParams(params json.RawMessage) (SystemPresenceRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return SystemPresenceRequest{}, nil
	}
	return decodeMethodParams[SystemPresenceRequest](params)
}

func DecodeSystemEventParams(params json.RawMessage) (SystemEventRequest, error) {
	return decodeMethodParams[SystemEventRequest](params)
}

func DecodeSendParams(params json.RawMessage) (SendRequest, error) {
	return decodeMethodParams[SendRequest](params)
}

func DecodeBrowserRequestParams(params json.RawMessage) (BrowserRequestRequest, error) {
	return decodeMethodParams[BrowserRequestRequest](params)
}

func DecodeVoicewakeGetParams(params json.RawMessage) (VoicewakeGetRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return VoicewakeGetRequest{}, nil
	}
	return decodeMethodParams[VoicewakeGetRequest](params)
}

func DecodeVoicewakeSetParams(params json.RawMessage) (VoicewakeSetRequest, error) {
	return decodeMethodParams[VoicewakeSetRequest](params)
}

func DecodeTTSStatusParams(params json.RawMessage) (TTSStatusRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TTSStatusRequest{}, nil
	}
	return decodeMethodParams[TTSStatusRequest](params)
}

func DecodeTTSProvidersParams(params json.RawMessage) (TTSProvidersRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TTSProvidersRequest{}, nil
	}
	return decodeMethodParams[TTSProvidersRequest](params)
}

func DecodeTTSSetProviderParams(params json.RawMessage) (TTSSetProviderRequest, error) {
	if isJSONArray(params) {
		var arr []any
		if err := json.Unmarshal(params, &arr); err != nil {
			return TTSSetProviderRequest{}, fmt.Errorf("invalid params")
		}
		if len(arr) != 1 {
			return TTSSetProviderRequest{}, fmt.Errorf("invalid params")
		}
		provider, ok := arr[0].(string)
		if !ok {
			return TTSSetProviderRequest{}, fmt.Errorf("invalid params")
		}
		return TTSSetProviderRequest{Provider: provider}, nil
	}
	return decodeMethodParams[TTSSetProviderRequest](params)
}

func DecodeTTSEnableParams(params json.RawMessage) (TTSEnableRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TTSEnableRequest{}, nil
	}
	return decodeMethodParams[TTSEnableRequest](params)
}

func DecodeTTSDisableParams(params json.RawMessage) (TTSDisableRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return TTSDisableRequest{}, nil
	}
	return decodeMethodParams[TTSDisableRequest](params)
}

func DecodeTTSConvertParams(params json.RawMessage) (TTSConvertRequest, error) {
	return decodeMethodParams[TTSConvertRequest](params)
}

func DecodeConfigDocFromRaw(raw string) (state.ConfigDoc, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return state.ConfigDoc{}, fmt.Errorf("raw is required")
	}
	var cfg state.ConfigDoc
	if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
		return state.ConfigDoc{}, fmt.Errorf("invalid raw config")
	}
	return cfg, nil
}

func DecodeConfigPatchFromRaw(raw string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("raw is required")
	}
	var patch map[string]any
	if err := json.Unmarshal([]byte(trimmed), &patch); err != nil {
		return nil, fmt.Errorf("invalid raw patch")
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("patch is required")
	}
	return patch, nil
}

func decodeMethodParams[T any](params json.RawMessage) (T, error) {
	var out T
	if len(bytes.TrimSpace(params)) == 0 {
		return out, nil
	}
	params = normalizeObjectParamAliases(params)
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("invalid params")
	}
	return out, nil
}

var objectParamAliases = map[string]string{
	"sessionId":        "session_id",
	"session_key":      "sessionKey",
	"runId":            "run_id",
	"timeoutMs":        "timeout_ms",
	"targetPubKey":     "target_pubkey",
	"peerPubKey":       "peer_pubkey",
	"contextMessages":  "context_messages",
	"memoryScope":      "memory_scope",
	"toolProfile":      "tool_profile",
	"enabledTools":     "enabled_tools",
	"parentContext":    "parent_context",
	"requestId":        "request_id",
	"expectedVersion":  "expected_version",
	"expectedEvent":    "expected_event",
	"agentId":          "agent_id",
	"installId":        "install_id",
	"skillKey":         "skill_key",
	"apiKey":           "api_key",
	"includePlugins":   "include_plugins",
	"includeEvents":    "include_events",
	"includeLogs":      "include_logs",
	"eventCursor":      "event_cursor",
	"logCursor":        "log_cursor",
	"eventLimit":       "event_limit",
	"logLimit":         "log_limit",
	"maxBytes":         "max_bytes",
	"waitTimeoutMs":    "wait_timeout_ms",
	"channelId":        "channel_id",
	"nodeId":           "node_id",
	"maxItems":         "max_items",
	"ttlMs":            "ttl_ms",
	"deviceId":         "device_id",
	"instanceId":       "instance_id",
	"lastInputSeconds": "last_input_seconds",
	"displayName":      "display_name",
	"coreVersion":      "core_version",
	"uiVersion":        "ui_version",
	"deviceFamily":     "device_family",
	"modelIdentifier":  "model_identifier",
	"remoteIp":         "remote_ip",
	"start_date":       "startDate",
	"end_date":         "endDate",
	"utc_offset":       "utcOffset",
	"base_hash":        "baseHash",
	"restart_delay_ms": "restartDelayMs",
}

func normalizeObjectParamAliases(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return raw
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return raw
	}
	changed := false
	for alias, canonical := range objectParamAliases {
		value, ok := payload[alias]
		if !ok {
			continue
		}
		if _, exists := payload[canonical]; !exists {
			payload[canonical] = value
		}
		delete(payload, alias)
		changed = true
	}
	if !changed {
		return raw
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return normalized
}

func isJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func normalizeLimit(value, def, max int) int {
	if value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}

func normalizeListName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if utf8.RuneCountInString(name) > 64 {
		name = truncateRunes(name, 64)
	}
	return name
}

func normalizeAgentID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if utf8.RuneCountInString(id) > 64 {
		id = truncateRunes(id, 64)
	}
	return id
}

func isSafeAgentFileName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	if strings.ContainsAny(name, "\\/") {
		return false
	}
	return true
}

func isSafePluginID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if len(id) > 100 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '@' || r == '.') {
			return false
		}
	}
	if strings.Contains(id, "..") {
		return false
	}
	if strings.HasPrefix(id, ".") || strings.HasPrefix(id, "-") {
		return false
	}
	return true
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boolPtr(v bool) *bool {
	return &v
}

func compactStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func compactObjectSlice(values []map[string]any) []map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		cp := make(map[string]any, len(value))
		for k, v := range value {
			cp[k] = v
		}
		out = append(out, cp)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isValidNostrIdentifier(id string) bool {
	if strings.HasPrefix(id, "npub1") && len(id) == 63 {
		return true
	}
	if len(id) == 64 {
		for _, c := range id {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return false
			}
		}
		return true
	}
	return false
}

func decodeWritePrecondition(raw json.RawMessage, expectedVersion *int, expectedEvent *string, baseHash *string) (bool, error) {
	raw = normalizeObjectParamAliases(raw)
	var pre struct {
		ExpectedVersion *int   `json:"expected_version"`
		ExpectedEvent   string `json:"expected_event"`
		BaseHash        string `json:"baseHash"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pre); err != nil {
		return false, err
	}
	expectedVersionSet := false
	if pre.ExpectedVersion != nil {
		expectedVersionSet = true
		*expectedVersion = *pre.ExpectedVersion
	}
	*expectedEvent = pre.ExpectedEvent
	if baseHash != nil {
		*baseHash = pre.BaseHash
	}
	return expectedVersionSet, nil
}
