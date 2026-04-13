package methods

import (
	"bytes"
	"encoding/json"
	"fmt"
	"metiq/internal/store/state"
	"strings"
)

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
	// Task carries the canonical machine-readable task contract aligned with the
	// shared TaskSpec schema. When Task.TaskID is set, it becomes the outbound ACP task_id.
	Task *state.TaskSpec `json:"task,omitempty"`
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
	// Task carries the canonical machine-readable task contract aligned with the
	// shared TaskSpec schema for this pipeline step.
	Task *state.TaskSpec `json:"task,omitempty"`
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
	var err error
	r.Task, err = normalizeACPTaskSpec(r.Task, r.Instructions, r.MemoryScope, r.ToolProfile, r.EnabledTools)
	if err != nil {
		return r, err
	}
	if r.Task != nil {
		if r.Instructions == "" {
			r.Instructions = r.Task.Instructions
		}
		if r.MemoryScope == "" {
			r.MemoryScope = r.Task.MemoryScope
		}
		if r.ToolProfile == "" {
			r.ToolProfile = r.Task.ToolProfile
		}
		if len(r.EnabledTools) == 0 {
			r.EnabledTools = append([]string(nil), r.Task.EnabledTools...)
		}
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
		var err error
		r.Steps[i].Task, err = normalizeACPTaskSpec(r.Steps[i].Task, r.Steps[i].Instructions, r.Steps[i].MemoryScope, r.Steps[i].ToolProfile, r.Steps[i].EnabledTools)
		if err != nil {
			return r, fmt.Errorf("steps[%d].%w", i, err)
		}
		if r.Steps[i].Task != nil {
			if r.Steps[i].Instructions == "" {
				r.Steps[i].Instructions = r.Steps[i].Task.Instructions
			}
			if r.Steps[i].MemoryScope == "" {
				r.Steps[i].MemoryScope = r.Steps[i].Task.MemoryScope
			}
			if r.Steps[i].ToolProfile == "" {
				r.Steps[i].ToolProfile = r.Steps[i].Task.ToolProfile
			}
			if len(r.Steps[i].EnabledTools) == 0 {
				r.Steps[i].EnabledTools = append([]string(nil), r.Steps[i].Task.EnabledTools...)
			}
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
		Task            *state.TaskSpec         `json:"task,omitempty"`
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
		Task:            compat.Task,
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
		Task                 *state.TaskSpec         `json:"task,omitempty"`
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
			Task:            step.Task,
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
