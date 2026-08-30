package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/gateway/channels"
	"metiq/internal/store/state"
)

// newNostrShouldReplyModelResolver adapts the configured per-agent light model
// (or an explicit room override) to the provider-independent R1 hook.
func newNostrShouldReplyModelResolver(configState *runtimeConfigStore) channels.ShouldReplyModelHookResolver {
	return func(hookCtx channels.ShouldReplyModelHookContext) channels.ShouldReplyModelHook {
		if configState == nil {
			return nil
		}
		cfg := configState.Get()
		agentID := strings.TrimSpace(hookCtx.AccountID)
		if agentID == "" {
			agentID = "main"
		}
		agCfg, ok := resolveAgentConfigByID(cfg, agentID)
		if !ok {
			return nil
		}
		model := strings.TrimSpace(hookCtx.Model)
		if model == "" {
			model = strings.TrimSpace(agCfg.LightModel)
		}
		if model == "" {
			return nil
		}
		return nostrShouldReplyModelHook{config: cfg, agent: agCfg, model: model}
	}
}

type nostrShouldReplyModelHook struct {
	config state.ConfigDoc
	agent  state.AgentConfig
	model  string
}

func (h nostrShouldReplyModelHook) Classify(ctx context.Context, input channels.ShouldReplyModelInput) (channels.ShouldReplyModelVerdict, error) {
	// Construct a no-tools, no-session runtime for this single bounded
	// classification. This keeps the cheap gate independent from the full turn,
	// transcript, memory, and tool loop.
	runtime, err := buildRuntimeForAgentModel(h.config, h.agent, h.model, nil)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	result, err := runtime.ProcessTurn(ctx, agent.Turn{
		UserText: "Classify whether this ambient group-chat message needs an agent response. " +
			"Return exactly one token: RESPOND, IGNORE, or STOP. RESPOND means useful action/answer is needed; " +
			"IGNORE means no response; STOP means the conversation asks agents to stop.\n\nInput JSON:\n" + string(encoded),
		MaxOutputTokens:      8,
		MaxAgenticIterations: 1,
	})
	if err != nil {
		return "", err
	}
	verdict := strings.ToUpper(strings.Trim(strings.TrimSpace(result.Text), "` .,:;!\n\t"))
	if fields := strings.Fields(verdict); len(fields) > 0 {
		verdict = fields[0]
	}
	switch channels.ShouldReplyModelVerdict(verdict) {
	case channels.ShouldReplyModelRespond, channels.ShouldReplyModelIgnore, channels.ShouldReplyModelStop:
		return channels.ShouldReplyModelVerdict(verdict), nil
	default:
		return "", fmt.Errorf("should-reply model returned invalid verdict %q", result.Text)
	}
}
