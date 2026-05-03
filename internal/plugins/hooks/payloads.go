package hooks

import "metiq/internal/plugins/registry"

type EventEnvelope struct {
	Event   registry.HookEvent `json:"event"`
	Payload any                `json:"payload"`
}

type BaseEvent struct {
	SessionID string         `json:"session_id,omitempty"`
	TurnID    string         `json:"turn_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	Trace     map[string]any `json:"trace,omitempty"`
}

var AllHookEvents = []registry.HookEvent{
	registry.HookBeforeAgentStart, registry.HookBeforeAgentReply, registry.HookBeforePromptBuild,
	registry.HookBeforeModelResolve, registry.HookLLMInput, registry.HookLLMOutput,
	registry.HookModelCallStarted, registry.HookModelCallEnded, registry.HookAgentEnd,
	registry.HookBeforeAgentFinalize, registry.HookBeforeCompaction, registry.HookAfterCompaction,
	registry.HookBeforeReset, registry.HookBeforeToolCall, registry.HookAfterToolCall,
	registry.HookToolResultPersist, registry.HookBeforeMessageWrite, registry.HookInboundClaim,
	registry.HookMessageReceived, registry.HookMessageSending, registry.HookMessageSent,
	registry.HookBeforeDispatch, registry.HookReplyDispatch, registry.HookSessionStart,
	registry.HookSessionEnd, registry.HookSubagentSpawning, registry.HookSubagentSpawned,
	registry.HookSubagentEnded, registry.HookSubagentDeliveryTarget, registry.HookGatewayStart,
	registry.HookGatewayStop, registry.HookCronChanged, registry.HookBeforeInstall,
	registry.HookAgentTurnPrepare, registry.HookHeartbeatPrompt,
}

type BeforeAgentStartEvent struct {
	BaseEvent
	UserText  string         `json:"user_text,omitempty"`
	ChannelID string         `json:"channel_id,omitempty"`
	SenderID  string         `json:"sender_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type BeforeAgentStartResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type BeforeAgentReplyEvent struct {
	BaseEvent
	ReplyText string         `json:"reply_text"`
	ChannelID string         `json:"channel_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type BeforeAgentReplyResult struct {
	ReplyText string         `json:"reply_text,omitempty"`
	Mutation  map[string]any `json:"mutation,omitempty"`
	Reject    bool           `json:"reject,omitempty"`
	Reason    string         `json:"reason,omitempty"`
}
type BeforePromptBuildEvent struct {
	BaseEvent
	UserText     string         `json:"user_text,omitempty"`
	History      []Message      `json:"history,omitempty"`
	Tools        []ToolDef      `json:"tools,omitempty"`
	Context      string         `json:"context,omitempty"`
	SystemPrompt string         `json:"system_prompt,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}
type BeforePromptBuildResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type BeforeModelResolveEvent struct {
	BaseEvent
	RequestedModel string         `json:"requested_model,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}
type BeforeModelResolveResult struct {
	Model    string         `json:"model,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type LLMInputEvent struct {
	BaseEvent
	Model    string         `json:"model,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Messages []Message      `json:"messages,omitempty"`
	Tools    []ToolDef      `json:"tools,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}
type LLMInputResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type LLMOutputEvent struct {
	BaseEvent
	Model       string         `json:"model,omitempty"`
	Provider    string         `json:"provider,omitempty"`
	Text        string         `json:"text,omitempty"`
	ToolCalls   []ToolCall     `json:"tool_calls,omitempty"`
	Usage       map[string]any `json:"usage,omitempty"`
	StopReason  string         `json:"stop_reason,omitempty"`
	RawResponse any            `json:"raw_response,omitempty"`
}
type LLMOutputResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type ModelCallStartedEvent struct {
	BaseEvent
	Model    string         `json:"model,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Messages int            `json:"messages,omitempty"`
	Tools    int            `json:"tools,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}
type ModelCallEndedEvent struct {
	BaseEvent
	Model      string         `json:"model,omitempty"`
	Provider   string         `json:"provider,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Usage      map[string]any `json:"usage,omitempty"`
	Error      string         `json:"error,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}
type AgentEndEvent struct {
	BaseEvent
	Outcome    string         `json:"outcome,omitempty"`
	StopReason string         `json:"stop_reason,omitempty"`
	Text       string         `json:"text,omitempty"`
	Error      string         `json:"error,omitempty"`
	Usage      map[string]any `json:"usage,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}
type BeforeAgentFinalizeEvent struct {
	BaseEvent
	Text       string         `json:"text,omitempty"`
	ToolTraces []ToolTrace    `json:"tool_traces,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}
type BeforeAgentFinalizeResult struct {
	Text     string         `json:"text,omitempty"`
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type BeforeCompactionEvent struct {
	BaseEvent
	Reason     string    `json:"reason,omitempty"`
	TokenCount int       `json:"token_count,omitempty"`
	MaxTokens  int       `json:"max_tokens,omitempty"`
	Messages   []Message `json:"messages,omitempty"`
}
type BeforeCompactionResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type AfterCompactionEvent struct {
	BaseEvent
	Compacted    bool   `json:"compacted"`
	Reason       string `json:"reason,omitempty"`
	TokensBefore int    `json:"tokens_before,omitempty"`
	TokensAfter  int    `json:"tokens_after,omitempty"`
	Summary      string `json:"summary,omitempty"`
}
type BeforeResetEvent struct {
	BaseEvent
	Reason    string         `json:"reason,omitempty"`
	ChannelID string         `json:"channel_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type BeforeResetResult struct {
	Reject bool   `json:"reject,omitempty"`
	Reason string `json:"reason,omitempty"`
}
type BeforeToolCallEvent struct {
	BaseEvent
	ToolName   string         `json:"tool_name"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}
type BeforeToolCallResult struct {
	Approved        bool           `json:"approved"`
	RejectionReason string         `json:"rejection_reason,omitempty"`
	MutatedArgs     map[string]any `json:"mutated_args,omitempty"`
}
type AfterToolCallEvent struct {
	BaseEvent
	ToolName   string         `json:"tool_name"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Args       map[string]any `json:"args,omitempty"`
	Result     string         `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}
type ToolResultPersistEvent struct {
	BaseEvent
	ToolName   string         `json:"tool_name"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Result     string         `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}
type ToolResultPersistResult struct {
	Result   string         `json:"result,omitempty"`
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type BeforeMessageWriteEvent struct {
	BaseEvent
	Message   Message        `json:"message"`
	ChannelID string         `json:"channel_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type BeforeMessageWriteResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type InboundClaimEvent struct {
	ChannelID string         `json:"channel_id"`
	SenderID  string         `json:"sender_id"`
	Text      string         `json:"text"`
	EventID   string         `json:"event_id,omitempty"`
	ThreadID  string         `json:"thread_id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type InboundClaimResult struct {
	Claimed   bool   `json:"claimed"`
	SkipAgent bool   `json:"skip_agent"`
	ReplyText string `json:"reply_text,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
}
type MessageReceivedEvent struct {
	ChannelID string         `json:"channel_id"`
	SenderID  string         `json:"sender_id"`
	Text      string         `json:"text"`
	EventID   string         `json:"event_id,omitempty"`
	ThreadID  string         `json:"thread_id,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	CreatedAt int64          `json:"created_at,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type MessageSendingEvent struct {
	ChannelID string         `json:"channel_id"`
	SenderID  string         `json:"sender_id,omitempty"`
	Recipient string         `json:"recipient,omitempty"`
	Text      string         `json:"text"`
	SessionID string         `json:"session_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type MessageSendingResult struct {
	Text     string         `json:"text,omitempty"`
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type MessageSentEvent struct {
	ChannelID string         `json:"channel_id"`
	SenderID  string         `json:"sender_id,omitempty"`
	Recipient string         `json:"recipient,omitempty"`
	Text      string         `json:"text"`
	SessionID string         `json:"session_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	EventID   string         `json:"event_id,omitempty"`
	Success   bool           `json:"success"`
	Error     string         `json:"error,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type BeforeDispatchEvent struct {
	BaseEvent
	ChannelID string         `json:"channel_id,omitempty"`
	SenderID  string         `json:"sender_id,omitempty"`
	Text      string         `json:"text,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type BeforeDispatchResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type ReplyDispatchEvent struct {
	BaseEvent
	ChannelID string         `json:"channel_id,omitempty"`
	SenderID  string         `json:"sender_id,omitempty"`
	Text      string         `json:"text"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type ReplyDispatchResult struct {
	Text     string         `json:"text,omitempty"`
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type SessionStartEvent struct {
	SessionID   string         `json:"session_id"`
	ChannelID   string         `json:"channel_id,omitempty"`
	AgentID     string         `json:"agent_id,omitempty"`
	InitiatorID string         `json:"initiator_id,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}
type SessionEndEvent struct {
	SessionID string         `json:"session_id"`
	ChannelID string         `json:"channel_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type SubagentSpawningEvent struct {
	BaseEvent
	ParentAgentID string         `json:"parent_agent_id,omitempty"`
	SubagentID    string         `json:"subagent_id,omitempty"`
	Instructions  string         `json:"instructions,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}
type SubagentSpawningResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type SubagentSpawnedEvent struct {
	BaseEvent
	ParentAgentID string         `json:"parent_agent_id,omitempty"`
	SubagentID    string         `json:"subagent_id,omitempty"`
	RunID         string         `json:"run_id,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}
type SubagentEndedEvent struct {
	BaseEvent
	ParentAgentID string         `json:"parent_agent_id,omitempty"`
	SubagentID    string         `json:"subagent_id,omitempty"`
	Outcome       string         `json:"outcome,omitempty"`
	Error         string         `json:"error,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}
type SubagentDeliveryTargetEvent struct {
	BaseEvent
	ParentAgentID string         `json:"parent_agent_id,omitempty"`
	SubagentID    string         `json:"subagent_id,omitempty"`
	DefaultTarget string         `json:"default_target,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}
type SubagentDeliveryTargetResult struct {
	Target   string         `json:"target,omitempty"`
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type GatewayStartEvent struct {
	GatewayID string         `json:"gateway_id,omitempty"`
	Address   string         `json:"address,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type GatewayStopEvent struct {
	GatewayID string         `json:"gateway_id,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type CronChangedEvent struct {
	JobID    string         `json:"job_id,omitempty"`
	Action   string         `json:"action,omitempty"`
	Schedule string         `json:"schedule,omitempty"`
	Enabled  bool           `json:"enabled,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}
type BeforeInstallEvent struct {
	PluginID    string         `json:"plugin_id,omitempty"`
	Source      string         `json:"source,omitempty"`
	InstallPath string         `json:"install_path,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}
type BeforeInstallResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type AgentTurnPrepareEvent struct {
	BaseEvent
	ChannelID string         `json:"channel_id,omitempty"`
	SenderID  string         `json:"sender_id,omitempty"`
	UserText  string         `json:"user_text,omitempty"`
	History   []Message      `json:"history,omitempty"`
	Tools     []ToolDef      `json:"tools,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
type AgentTurnPrepareResult struct {
	Mutation map[string]any `json:"mutation,omitempty"`
	Reject   bool           `json:"reject,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}
type HeartbeatPromptContributionEvent struct {
	BaseEvent
	Wakes    []map[string]any `json:"wakes,omitempty"`
	Prompt   string           `json:"prompt,omitempty"`
	Metadata map[string]any   `json:"metadata,omitempty"`
}
type HeartbeatPromptContributionResult struct {
	Contribution string         `json:"contribution,omitempty"`
	Mutation     map[string]any `json:"mutation,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}
type ToolCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}
type ToolTrace struct {
	Call   ToolCall `json:"call"`
	Result string   `json:"result,omitempty"`
	Error  string   `json:"error,omitempty"`
}
