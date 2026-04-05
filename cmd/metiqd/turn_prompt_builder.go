package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"metiq/internal/agent"
	toolbuiltin "metiq/internal/agent/toolbuiltin"
	hookspkg "metiq/internal/hooks"
	"metiq/internal/store/state"
)

type turnPromptBuilderParams struct {
	Config               state.ConfigDoc
	SessionID            string
	AgentID              string
	Channel              string
	SelfPubkey           string
	SelfNPub             string
	StaticSystemPrompt   string
	Context              string
	Tools                []agent.ToolDefinition
	SessionThinkingLevel string
}

type builtTurnPrompt struct {
	StaticSystemPrompt  string
	Context             string
	ContextWindowTokens int
}

func applyPromptEnvelopeToPreparedTurn(prepared preparedAgentRunTurn, params turnPromptBuilderParams) preparedAgentRunTurn {
	envelope := buildTurnPromptEnvelope(params)
	prepared.Turn.StaticSystemPrompt = envelope.StaticSystemPrompt
	prepared.Turn.Context = envelope.Context
	prepared.Turn.ContextWindowTokens = envelope.ContextWindowTokens
	return prepared
}

func buildTurnPromptEnvelope(params turnPromptBuilderParams) builtTurnPrompt {
	agentID := defaultAgentID(params.AgentID)
	wsDir, agentSystemPrompt, agentModel, agentThinkingLevel := resolvePromptAgentConfig(params.Config, agentID)
	if agentThinkingLevel == "" {
		agentThinkingLevel = strings.TrimSpace(params.SessionThinkingLevel)
	}
	if params.SelfNPub == "" && params.SelfPubkey != "" {
		params.SelfNPub = toolbuiltin.NostrNPubFromHex(params.SelfPubkey)
	}
	bootstrapWarnings := make([]string, 0)
	bootstrapFiles, warnings := agent.LoadWorkspaceBootstrapFiles(wsDir, agent.DefaultBootstrapFileNames())
	bootstrapWarnings = append(bootstrapWarnings, warnings...)
	if extraFiles, extraWarnings := loadBootstrapHookFiles(controlHooksMgr, params.SessionID, params.Config, wsDir); len(extraFiles) > 0 || len(extraWarnings) > 0 {
		bootstrapWarnings = append(bootstrapWarnings, extraWarnings...)
		bootstrapFiles = append(bootstrapFiles, extraFiles...)
	}
	bootstrapFiles = dedupeBootstrapFiles(bootstrapFiles)
	contextFiles := agent.BuildBootstrapContextFiles(bootstrapFiles, func(message string) {
		bootstrapWarnings = append(bootstrapWarnings, message)
	}, agent.DefaultBootstrapMaxChars, agent.DefaultBootstrapTotalMaxChars)
	analysis := agent.AnalyzeBootstrapBudget(
		agent.BuildBootstrapInjectionStats(bootstrapFiles, contextFiles),
		agent.DefaultBootstrapMaxChars,
		agent.DefaultBootstrapTotalMaxChars,
	)
	bootstrapWarnings = append(bootstrapWarnings, agent.FormatBootstrapTruncationWarningLines(analysis, agent.DefaultBootstrapPromptWarningMaxFiles)...)

	staticSystemPrompt := params.StaticSystemPrompt
	if bootstrapPrompt := agent.RenderBootstrapPromptContext(contextFiles); bootstrapPrompt != "" {
		staticSystemPrompt = joinPromptSections(staticSystemPrompt, bootstrapPrompt)
	}
	if agentSystemPrompt != "" {
		staticSystemPrompt = joinPromptSections(staticSystemPrompt, agentSystemPrompt)
	}

	channel := strings.TrimSpace(params.Channel)
	if channel == "" {
		channel = "nostr"
	}
	runtimeParams := turnRuntimeParams{
		AgentID:           agentID,
		SelfPubkey:        params.SelfPubkey,
		SelfNPub:          params.SelfNPub,
		Model:             agentModel,
		Channel:           channel,
		Capabilities:      resolveRuntimeCapabilities(params.Config),
		Tools:             params.Tools,
		Config:            params.Config,
		WorkspaceDir:      wsDir,
		ThinkingLevel:     agentThinkingLevel,
		SkillsPrompt:      buildSkillsPromptCached(params.Config, agentID),
		BootstrapWarnings: bootstrapWarnings,
	}
	staticSystemPrompt = joinPromptSections(staticSystemPrompt, buildTurnRuntimeStaticContext(runtimeParams))
	contextText := joinPromptSections(params.Context, buildTurnRuntimeDynamicContext())

	return builtTurnPrompt{
		StaticSystemPrompt:  staticSystemPrompt,
		Context:             contextText,
		ContextWindowTokens: maxContextTokensForAgent(params.Config, agentID),
	}
}

func resolvePromptAgentConfig(cfg state.ConfigDoc, agentID string) (workspaceDir, systemPrompt, model, thinking string) {
	agentID = defaultAgentID(agentID)
	for _, ac := range cfg.Agents {
		if defaultAgentID(ac.ID) != agentID {
			continue
		}
		workspaceDir = strings.TrimSpace(ac.WorkspaceDir)
		systemPrompt = strings.TrimSpace(ac.SystemPrompt)
		model = strings.TrimSpace(ac.Model)
		thinking = strings.TrimSpace(ac.ThinkingLevel)
		break
	}
	if workspaceDir == "" {
		workspaceDir = defaultWorkspaceDir()
	}
	if model == "" {
		model = strings.TrimSpace(cfg.Agent.DefaultModel)
	}
	return workspaceDir, systemPrompt, model, thinking
}

func defaultWorkspaceDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".metiq", "workspace")
	}
	return ""
}

func resolveRuntimeCapabilities(cfg state.ConfigDoc) []string {
	capsRaw, ok := cfg.Extra["capabilities"]
	if !ok {
		return nil
	}
	var caps []string
	switch v := capsRaw.(type) {
	case []string:
		caps = append(caps, v...)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				caps = append(caps, s)
			}
		}
	}
	return caps
}

func loadBootstrapHookFiles(hooksMgr *hookspkg.Manager, sessionID string, cfg state.ConfigDoc, workspaceDir string) ([]agent.WorkspaceBootstrapFile, []string) {
	if hooksMgr == nil || strings.TrimSpace(workspaceDir) == "" {
		return nil, nil
	}
	var extraPaths []string
	if befRaw, ok := cfg.Extra["bootstrap_extra_files"]; ok {
		if befMap, ok := befRaw.(map[string]any); ok {
			for _, key := range []string{"paths", "patterns", "files"} {
				if raw, ok := befMap[key]; ok {
					switch v := raw.(type) {
					case []string:
						extraPaths = append(extraPaths, v...)
					case []any:
						for _, item := range v {
							if s, ok := item.(string); ok {
								extraPaths = append(extraPaths, s)
							}
						}
					}
				}
			}
		}
	}
	if len(extraPaths) == 0 {
		return nil, nil
	}
	evCtx := map[string]any{"paths": extraPaths}
	errs := hooksMgr.Fire("agent:bootstrap", sessionID, evCtx)
	warnings := make([]string, 0, len(errs))
	for _, err := range errs {
		msg := fmt.Sprintf("agent:bootstrap hook: %v", err)
		log.Print(msg)
		warnings = append(warnings, msg)
	}
	if files, ok := evCtx["bootstrapFiles"].([]agent.WorkspaceBootstrapFile); ok {
		return files, warnings
	}
	legacy, ok := evCtx["injectedFiles"].([]string)
	if !ok || len(legacy) == 0 {
		return nil, warnings
	}
	out := make([]agent.WorkspaceBootstrapFile, 0, len(legacy))
	for i, content := range legacy {
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			continue
		}
		name := fmt.Sprintf("HOOK_EXTRA_%d.md", i+1)
		out = append(out, agent.WorkspaceBootstrapFile{Name: name, Path: name, Content: trimmed})
	}
	return out, warnings
}

func dedupeBootstrapFiles(files []agent.WorkspaceBootstrapFile) []agent.WorkspaceBootstrapFile {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]agent.WorkspaceBootstrapFile, 0, len(files))
	for _, file := range files {
		key := strings.TrimSpace(file.Path)
		if key == "" {
			key = strings.TrimSpace(file.Name)
		}
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, file)
	}
	return out
}
