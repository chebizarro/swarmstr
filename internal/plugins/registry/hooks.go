package registry

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// HookEvent represents an OpenClaw hook event name.
type HookEvent string

const (
	HookBeforeAgentStart       HookEvent = "before_agent_start"
	HookBeforeAgentReply       HookEvent = "before_agent_reply"
	HookBeforePromptBuild      HookEvent = "before_prompt_build"
	HookBeforeModelResolve     HookEvent = "before_model_resolve"
	HookLLMInput               HookEvent = "llm_input"
	HookLLMOutput              HookEvent = "llm_output"
	HookModelCallStarted       HookEvent = "model_call_started"
	HookModelCallEnded         HookEvent = "model_call_ended"
	HookAgentEnd               HookEvent = "agent_end"
	HookBeforeAgentFinalize    HookEvent = "before_agent_finalize"
	HookBeforeCompaction       HookEvent = "before_compaction"
	HookAfterCompaction        HookEvent = "after_compaction"
	HookBeforeReset            HookEvent = "before_reset"
	HookBeforeToolCall         HookEvent = "before_tool_call"
	HookAfterToolCall          HookEvent = "after_tool_call"
	HookToolResultPersist      HookEvent = "tool_result_persist"
	HookBeforeMessageWrite     HookEvent = "before_message_write"
	HookInboundClaim           HookEvent = "inbound_claim"
	HookMessageReceived        HookEvent = "message_received"
	HookMessageSending         HookEvent = "message_sending"
	HookMessageSent            HookEvent = "message_sent"
	HookBeforeDispatch         HookEvent = "before_dispatch"
	HookReplyDispatch          HookEvent = "reply_dispatch"
	HookSessionStart           HookEvent = "session_start"
	HookSessionEnd             HookEvent = "session_end"
	HookSubagentSpawning       HookEvent = "subagent_spawning"
	HookSubagentSpawned        HookEvent = "subagent_spawned"
	HookSubagentEnded          HookEvent = "subagent_ended"
	HookSubagentDeliveryTarget HookEvent = "subagent_delivery_target"
	HookGatewayStart           HookEvent = "gateway_start"
	HookGatewayStop            HookEvent = "gateway_stop"
	HookCronChanged            HookEvent = "cron_changed"
	HookBeforeInstall          HookEvent = "before_install"
	HookAgentTurnPrepare       HookEvent = "agent_turn_prepare"
	HookHeartbeatPrompt        HookEvent = "heartbeat_prompt_contribution"
)

type HookSource string

const (
	HookSourceNode   HookSource = "node"
	HookSourceNative HookSource = "native"
)

// HookRegistrationData describes a hook registration.
type HookRegistrationData struct {
	HookID   string
	Events   []HookEvent
	Priority int
	Source   HookSource
	Raw      map[string]any
}

// RegisteredHook represents a hook registration.
type RegisteredHook struct {
	ID           string
	PluginID     string
	Events       []HookEvent
	Priority     int
	Source       HookSource
	Raw          map[string]any
	RegisteredAt time.Time
}

// HookRegistry indexes hooks by ID and event. Event lists are sorted by priority.
type HookRegistry struct {
	mu       sync.RWMutex
	hooks    map[HookEvent][]*RegisteredHook
	byID     map[string]*RegisteredHook
	byPlugin map[string]map[string]struct{}
}

func NewHookRegistry() *HookRegistry {
	return &HookRegistry{hooks: map[HookEvent][]*RegisteredHook{}, byID: map[string]*RegisteredHook{}, byPlugin: map[string]map[string]struct{}{}}
}

func HookDataFromRegistration(source PluginSource, reg Registration) HookRegistrationData {
	events := stringSlice(reg.Events, reg.Raw, "events")
	out := make([]HookEvent, 0, len(events))
	for _, event := range events {
		out = append(out, HookEvent(event))
	}
	hookSource := HookSourceNode
	if source == PluginSourceNative {
		hookSource = HookSourceNative
	}
	return HookRegistrationData{
		HookID:   firstNonEmpty(reg.HookID, stringFromRaw(reg.Raw, "hookId"), reg.ID),
		Events:   out,
		Priority: intFromRaw(reg.Raw, "priority", reg.Priority),
		Source:   hookSource,
		Raw:      cloneRaw(reg.Raw),
	}
}

func (r *HookRegistry) Register(pluginID string, data HookRegistrationData) (CapabilityRef, error) {
	if err := requireID("hook", data.HookID); err != nil {
		return CapabilityRef{}, err
	}
	if len(data.Events) == 0 {
		return CapabilityRef{}, fmt.Errorf("hook %q registration missing events", data.HookID)
	}
	if data.Source == "" {
		data.Source = HookSourceNode
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.byID[data.HookID]; ok && existing.PluginID != pluginID {
		return CapabilityRef{}, fmt.Errorf("hook %q already registered by plugin %q", data.HookID, existing.PluginID)
	}
	hook := &RegisteredHook{
		ID:           data.HookID,
		PluginID:     pluginID,
		Events:       append([]HookEvent(nil), data.Events...),
		Priority:     data.Priority,
		Source:       data.Source,
		Raw:          cloneRaw(data.Raw),
		RegisteredAt: time.Now(),
	}
	if old, ok := r.byID[data.HookID]; ok {
		for _, event := range old.Events {
			r.hooks[event] = removeHook(r.hooks[event], data.HookID)
			if len(r.hooks[event]) == 0 {
				delete(r.hooks, event)
			}
		}
		removePluginIndex(r.byPlugin, old.PluginID, old.ID)
	}
	r.byID[data.HookID] = hook
	addPluginIndex(r.byPlugin, pluginID, data.HookID)
	for _, event := range data.Events {
		r.hooks[event] = append(r.hooks[event], hook)
		sort.SliceStable(r.hooks[event], func(i, j int) bool {
			if r.hooks[event][i].Priority == r.hooks[event][j].Priority {
				return r.hooks[event][i].ID < r.hooks[event][j].ID
			}
			return r.hooks[event][i].Priority < r.hooks[event][j].Priority
		})
	}
	return CapabilityRef{Type: capabilityTypeString(CapabilityTypeHook), ID: data.HookID}, nil
}

func (r *HookRegistry) Unregister(hookID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	hook, ok := r.byID[hookID]
	if !ok {
		return
	}
	for _, event := range hook.Events {
		r.hooks[event] = removeHook(r.hooks[event], hookID)
		if len(r.hooks[event]) == 0 {
			delete(r.hooks, event)
		}
	}
	delete(r.byID, hookID)
	removePluginIndex(r.byPlugin, hook.PluginID, hookID)
}

func (r *HookRegistry) Get(hookID string) (*RegisteredHook, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hook, ok := r.byID[hookID]
	if !ok {
		return nil, false
	}
	return cloneHook(hook), true
}

func (r *HookRegistry) HandlersFor(event HookEvent) []*RegisteredHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handlers := r.hooks[event]
	out := make([]*RegisteredHook, 0, len(handlers))
	for _, hook := range handlers {
		out = append(out, cloneHook(hook))
	}
	return out
}

func (r *HookRegistry) List() []*RegisteredHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RegisteredHook, 0, len(r.byID))
	for _, hook := range r.byID {
		out = append(out, cloneHook(hook))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *HookRegistry) ByPlugin(pluginID string) []*RegisteredHook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := idsForPlugin(r.byPlugin, pluginID)
	out := make([]*RegisteredHook, 0, len(ids))
	for _, id := range ids {
		if hook := r.byID[id]; hook != nil {
			out = append(out, cloneHook(hook))
		}
	}
	return out
}

func (r *HookRegistry) Events() []HookEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	events := make([]HookEvent, 0, len(r.hooks))
	for event := range r.hooks {
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool { return events[i] < events[j] })
	return events
}

func (r *HookRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}

func removeHook(hooks []*RegisteredHook, hookID string) []*RegisteredHook {
	filtered := hooks[:0]
	for _, hook := range hooks {
		if hook.ID != hookID {
			filtered = append(filtered, hook)
		}
	}
	return filtered
}

func cloneHook(hook *RegisteredHook) *RegisteredHook {
	if hook == nil {
		return nil
	}
	cp := *hook
	cp.Events = append([]HookEvent(nil), hook.Events...)
	cp.Raw = cloneRaw(hook.Raw)
	return &cp
}
