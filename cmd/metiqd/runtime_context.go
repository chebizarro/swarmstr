package main

// runtime_context.go builds the additional system-prompt sections injected at
// turn time.  It brings the context injection up to parity with OpenClaw's
// buildAgentSystemPrompt: runtime info, user timezone/time, tool summaries,
// model aliases, TTS hints, reaction guidance, sandbox info, skills prompt,
// docs path, and bootstrap truncation warnings.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
	skillspkg "metiq/internal/skills"
	"metiq/internal/store/state"
)

type ttlCacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

var (
	docsSectionCacheMu sync.Mutex
	docsSectionCache   = map[string]ttlCacheEntry[string]{}
)

// turnRuntimeParams holds the data needed to build the runtime context.
type turnRuntimeParams struct {
	// Agent identity
	AgentID      string
	SelfPubkey   string
	SelfNPub     string
	Model        string
	Channel      string // e.g. "nostr", "telegram", "discord"
	Capabilities []string

	// Tool definitions available to this agent.
	Tools []agent.ToolDefinition

	// Config
	Config state.ConfigDoc

	// Workspace
	WorkspaceDir string

	// Thinking level (from session or agent config).
	ThinkingLevel string

	// Skills prompt (pre-rendered list of available skills).
	SkillsPrompt string
	// BootstrapWarnings reports truncation / boundary warnings from the current
	// bootstrap prompt assembly pass.
	BootstrapWarnings []string
}

func buildSkillsPromptCached(cfg state.ConfigDoc, agentID string) string {
	// Use a standard 200K budget — ProfileFromContextWindowTokens derives all
	// fields proportionally, no manual profile construction needed.
	standardBudget := agent.ComputeContextBudget(agent.ProfileFromContextWindowTokens(200_000))
	return buildSkillsPromptWithBudget(cfg, agentID, standardBudget)
}

// buildSkillsPromptWithBudget builds the skills prompt section with optional
// context budget awareness. When budget is zero-valued (standard tier), behavior
// is unchanged from the original buildSkillsPromptCached.
func buildSkillsPromptWithBudget(cfg state.ConfigDoc, agentID string, budget agent.ContextBudget) string {
	catalog, err := skillspkg.BuildSkillCatalog(cfg, agentID)
	if err != nil || catalog == nil {
		return ""
	}
	mirrorPaths, _ := skillspkg.SyncPromptSkillsToWorkspace(catalog)

	// Use budget-aware prompt limits when a budget is provided.
	var limits skillspkg.PromptLimits
	if budget.SkillsMaxCount > 0 || budget.SkillsTotalMax > 0 {
		limits = skillspkg.ResolvePromptLimitsWithBudget(cfg, budget.SkillsMaxCount, budget.SkillsTotalMax)
	} else {
		limits = skillspkg.ResolvePromptLimits(cfg)
	}

	// Determine the tier string for skill text rendering.
	tierStr := budget.Profile.Tier.String()

	// Compute per-skill budget for compact rendering decisions.
	perSkillBudget := 0
	if limits.MaxChars > 0 && limits.MaxCount > 0 {
		perSkillBudget = limits.MaxChars / limits.MaxCount
	}

	visible := skillspkg.PromptVisibleSkills(catalog)
	if len(visible) == 0 {
		return ""
	}

	var lines []string
	truncated := false
	for _, resolved := range visible {
		if limits.MaxCount > 0 && len(lines) >= limits.MaxCount {
			truncated = true
			break
		}

		// Use compressed skill rendering when the per-skill budget is tight.
		// A threshold of 2000 chars means even large models with many skills
		// get compact rendering when the per-skill budget is small.
		if perSkillBudget > 0 && perSkillBudget < 2000 {
			if compactText := skillspkg.SkillPromptText(resolved, perSkillBudget, tierStr); compactText != "" {
				candidate := append(lines, compactText)
				joined := strings.Join(candidate, "\n")
				if limits.MaxChars > 0 && len(joined) > limits.MaxChars {
					truncated = true
					break
				}
				lines = candidate
				continue
			}
		}

		// Standard rendering (unchanged).
		name := agent.SanitizePromptLiteral(strings.TrimSpace(resolved.Skill.Manifest.Name))
		if name == "" {
			name = agent.SanitizePromptLiteral(resolved.Skill.SkillKey)
		}
		desc := agent.SanitizePromptLiteral(strings.TrimSpace(resolved.Skill.Manifest.Description))
		if desc == "" {
			desc = "No description provided."
		}
		if when := agent.SanitizePromptLiteral(strings.TrimSpace(resolved.WhenToUse)); when != "" {
			desc += fmt.Sprintf(" (when: %s)", when)
		}
		line := fmt.Sprintf("- %s: %s", name, desc)
		if path := agent.SanitizePromptLiteral(strings.TrimSpace(skillspkg.PromptSkillPath(catalog, resolved, mirrorPaths))); path != "" {
			line += fmt.Sprintf(" (location: %s)", path)
		}
		if resolved.Always && !resolved.Eligible {
			if missing := formatSkillMissingSummary(resolved.Missing); missing != "" {
				line += fmt.Sprintf(" [always; missing %s]", missing)
			}
		}
		candidate := append(lines, line)
		joined := strings.Join(candidate, "\n")
		if limits.MaxChars > 0 && len(joined) > limits.MaxChars {
			truncated = true
			break
		}
		lines = candidate
	}
	if len(lines) == 0 {
		return ""
	}
	prefix := ""
	if truncated {
		prefix = fmt.Sprintf("⚠ Skills truncated: included %d of %d.\n", len(lines), len(visible))
	}
	return prefix + "<available_skills>\n" + strings.Join(lines, "\n") + "\n</available_skills>"
}

func formatSkillMissingSummary(missing skillspkg.Requirements) string {
	var parts []string
	if len(missing.Bins) > 0 {
		parts = append(parts, "bins: "+strings.Join(missing.Bins, ","))
	}
	if len(missing.AnyBins) > 0 {
		parts = append(parts, "anyBins: "+strings.Join(missing.AnyBins, ","))
	}
	if len(missing.Env) > 0 {
		parts = append(parts, "env: "+strings.Join(missing.Env, ","))
	}
	if len(missing.Config) > 0 {
		parts = append(parts, "config: "+strings.Join(missing.Config, ","))
	}
	if len(missing.OS) > 0 {
		parts = append(parts, "os: "+strings.Join(missing.OS, ","))
	}
	return strings.Join(parts, "; ")
}

func buildDocsSectionCached(workspaceDir string) string {
	const ttl = 5 * time.Minute
	key := strings.TrimSpace(workspaceDir)
	now := time.Now()

	docsSectionCacheMu.Lock()
	if ent, ok := docsSectionCache[key]; ok && now.Before(ent.expiresAt) {
		v := ent.value
		docsSectionCacheMu.Unlock()
		return v
	}
	docsSectionCacheMu.Unlock()

	out := buildDocsSection(workspaceDir)
	docsSectionCacheMu.Lock()
	docsSectionCache[key] = ttlCacheEntry[string]{value: out, expiresAt: now.Add(ttl)}
	docsSectionCacheMu.Unlock()
	return out
}

// buildTurnRuntimeContext generates the supplementary system prompt sections
// that OpenClaw injects on every turn: runtime info, time, tool summaries,
// model aliases, TTS, reactions, sandbox, skills, docs, and bootstrap warnings.
func buildTurnRuntimeContext(p turnRuntimeParams) string {
	return joinPromptSections(buildTurnRuntimeStaticContext(p), buildTurnRuntimeDynamicContext())
}

func buildTurnRuntimeStaticContext(p turnRuntimeParams) string {
	var sections []string

	// ── Runtime info ────────────────────────────────────────────────────────
	sections = append(sections, buildRuntimeSection(p))

	// ── Tool summaries ──────────────────────────────────────────────────────
	if ts := buildToolSummarySection(p.Tools); ts != "" {
		sections = append(sections, ts)
	}

	// ── Model aliases ───────────────────────────────────────────────────────
	if ma := buildModelAliasSection(); ma != "" {
		sections = append(sections, ma)
	}

	// ── TTS hints ───────────────────────────────────────────────────────────
	if tts := buildTTSSection(p.Config); tts != "" {
		sections = append(sections, tts)
	}

	// ── Reaction guidance ───────────────────────────────────────────────────
	if rg := buildReactionSection(p.Config, p.Channel); rg != "" {
		sections = append(sections, rg)
	}

	// ── Sandbox info ────────────────────────────────────────────────────────
	if sb := buildSandboxSection(p.Config); sb != "" {
		sections = append(sections, sb)
	}

	// ── Skills prompt ───────────────────────────────────────────────────────
	if sp := buildSkillsSection(p.SkillsPrompt); sp != "" {
		sections = append(sections, sp)
	}

	// ── Documentation path ──────────────────────────────────────────────────
	if dp := buildDocsSectionCached(p.WorkspaceDir); dp != "" {
		sections = append(sections, dp)
	}

	// ── Bootstrap truncation warnings ───────────────────────────────────────
	if bw := buildBootstrapWarningSection(p.BootstrapWarnings); bw != "" {
		sections = append(sections, bw)
	}

	return strings.Join(sections, "\n\n")
}

func buildTurnRuntimeDynamicContext() string {
	return buildTimeSection()
}

// ─── Section builders ─────────────────────────────────────────────────────────

func buildRuntimeSection(p turnRuntimeParams) string {
	hostname, _ := os.Hostname()
	safeCaps := make([]string, 0, len(p.Capabilities))
	for _, cap := range p.Capabilities {
		if safe := agent.SanitizePromptLiteral(strings.TrimSpace(cap)); safe != "" {
			safeCaps = append(safeCaps, safe)
		}
	}
	parts := []string{
		fmt.Sprintf("agent=%s", agent.SanitizePromptLiteral(p.AgentID)),
		fmt.Sprintf("host=%s", agent.SanitizePromptLiteral(hostname)),
		fmt.Sprintf("os=%s (%s)", agent.SanitizePromptLiteral(runtime.GOOS), agent.SanitizePromptLiteral(runtime.GOARCH)),
		fmt.Sprintf("go=%s", agent.SanitizePromptLiteral(runtime.Version())),
	}
	if p.SelfPubkey != "" {
		parts = append(parts, fmt.Sprintf("self_pubkey=%s", agent.SanitizePromptLiteral(p.SelfPubkey)))
	}
	if p.SelfNPub != "" {
		parts = append(parts, fmt.Sprintf("self_npub=%s", agent.SanitizePromptLiteral(p.SelfNPub)))
	}
	if p.Model != "" {
		parts = append(parts, fmt.Sprintf("model=%s", agent.SanitizePromptLiteral(p.Model)))
	}
	if p.Channel != "" {
		parts = append(parts, fmt.Sprintf("channel=%s", agent.SanitizePromptLiteral(p.Channel)))
	}
	if len(safeCaps) > 0 {
		parts = append(parts, fmt.Sprintf("capabilities=%s", strings.Join(safeCaps, ",")))
	} else if p.Channel != "" {
		parts = append(parts, "capabilities=none")
	}
	if p.ThinkingLevel != "" && p.ThinkingLevel != "off" {
		parts = append(parts, fmt.Sprintf("thinking=%s", agent.SanitizePromptLiteral(p.ThinkingLevel)))
	} else {
		parts = append(parts, "thinking=off")
	}
	return "## Runtime\nRuntime: " + strings.Join(parts, " | ")
}

func buildTimeSection() string {
	now := time.Now()
	tz := now.Location().String()
	timeStr := now.Format("2006-01-02 15:04 MST")
	weekday := now.Weekday().String()
	return fmt.Sprintf("## Current Date & Time\nTime zone: %s\nCurrent time: %s (%s)", tz, timeStr, weekday)
}

func buildToolSummarySection(tools []agent.ToolDefinition) string {
	if len(tools) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "## Available Tools")
	lines = append(lines, "Tool availability (filtered by policy):")
	lines = append(lines, "Tool names are case-sensitive. Call tools exactly as listed.")

	// Sort by name for stable output.
	sorted := make([]agent.ToolDefinition, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	for _, t := range sorted {
		name := agent.SanitizePromptLiteral(strings.TrimSpace(t.Name))
		desc := agent.SanitizePromptLiteral(strings.TrimSpace(t.Description))
		if desc != "" {
			// Use just the first sentence for the summary line.
			if idx := strings.Index(desc, ". "); idx > 0 && idx < 120 {
				desc = desc[:idx]
			}
			if len(desc) > 120 {
				desc = desc[:117] + "..."
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", name, desc))
		} else {
			lines = append(lines, fmt.Sprintf("- %s", name))
		}
	}
	return strings.Join(lines, "\n")
}

func buildModelAliasSection() string {
	// Model alias mappings from provider.go's openAICompatProviders plus
	// the well-known Anthropic and Google prefixes.
	aliases := []struct{ alias, desc string }{
		{"claude-*", "Anthropic Claude models (claude-3-5-sonnet, claude-3-haiku, etc.)"},
		{"gpt-*", "OpenAI GPT models"},
		{"o1-*, o3-*, o4-*", "OpenAI reasoning models"},
		{"gemini-*", "Google Gemini models"},
		{"grok-*", "xAI Grok models (XAI_API_KEY)"},
		{"groq", "Groq hosted models (GROQ_API_KEY)"},
		{"mistral-*", "Mistral AI models (MISTRAL_API_KEY)"},
		{"openrouter/*", "OpenRouter routing (OPENROUTER_API_KEY)"},
		{"together/*", "Together AI (TOGETHER_API_KEY)"},
		{"deepinfra/*", "DeepInfra (DEEPINFRA_API_KEY)"},
		{"pplx-*", "Perplexity (PERPLEXITY_API_KEY)"},
		{"cohere, command-*", "Cohere Command models (COHERE_API_KEY)"},
	}
	var lines []string
	lines = append(lines, "## Model Aliases")
	lines = append(lines, "Prefer aliases when specifying model overrides; full provider/model is also accepted.")
	for _, a := range aliases {
		lines = append(lines, fmt.Sprintf("- %s → %s", a.alias, a.desc))
	}
	return strings.Join(lines, "\n")
}

func buildTTSSection(cfg state.ConfigDoc) string {
	if !cfg.TTS.Enabled {
		return ""
	}
	var lines []string
	lines = append(lines, "## Voice (TTS)")
	lines = append(lines, "Text-to-speech is enabled.")
	if cfg.TTS.Provider != "" {
		lines = append(lines, fmt.Sprintf("Provider: %s", agent.SanitizePromptLiteral(cfg.TTS.Provider)))
	}
	if cfg.TTS.Voice != "" {
		lines = append(lines, fmt.Sprintf("Voice: %s", agent.SanitizePromptLiteral(cfg.TTS.Voice)))
	}
	lines = append(lines, "When replying to a voice message, keep responses natural and conversational.")
	return strings.Join(lines, "\n")
}

func buildReactionSection(cfg state.ConfigDoc, channel string) string {
	// Check if reactions are configured via Extra["reactions"].
	reactionsRaw, ok := cfg.Extra["reactions"]
	if !ok {
		return ""
	}
	reactionsMap, ok := reactionsRaw.(map[string]any)
	if !ok {
		return ""
	}
	enabled, _ := reactionsMap["enabled"].(bool)
	if !enabled {
		return ""
	}
	level, _ := reactionsMap["level"].(string)
	if level == "" {
		level = "minimal"
	}
	if channel == "" {
		channel = "nostr"
	}
	channel = agent.SanitizePromptLiteral(channel)

	var lines []string
	lines = append(lines, "## Reactions")
	if level == "extensive" {
		lines = append(lines, fmt.Sprintf("Reactions are enabled for %s in EXTENSIVE mode.", channel))
		lines = append(lines, "Feel free to react liberally:")
		lines = append(lines, "- Acknowledge messages with appropriate emojis")
		lines = append(lines, "- Express sentiment and personality through reactions")
		lines = append(lines, "- React to interesting content, humor, or notable events")
		lines = append(lines, "Guideline: react whenever it feels natural.")
	} else {
		lines = append(lines, fmt.Sprintf("Reactions are enabled for %s in MINIMAL mode.", channel))
		lines = append(lines, "React ONLY when truly relevant:")
		lines = append(lines, "- Acknowledge important user requests or confirmations")
		lines = append(lines, "- Express genuine sentiment (humor, appreciation) sparingly")
		lines = append(lines, "- Avoid reacting to routine messages or your own replies")
		lines = append(lines, "Guideline: at most 1 reaction per 5-10 exchanges.")
	}
	return strings.Join(lines, "\n")
}

func buildSandboxSection(cfg state.ConfigDoc) string {
	sandboxRaw, ok := cfg.Extra["sandbox"]
	if !ok {
		return ""
	}
	sandboxMap, ok := sandboxRaw.(map[string]any)
	if !ok {
		return ""
	}
	enabled, _ := sandboxMap["enabled"].(bool)
	if !enabled {
		return ""
	}

	var lines []string
	lines = append(lines, "## Sandbox")
	lines = append(lines, "You are running in a sandboxed runtime (exec commands run in an isolated environment).")
	lines = append(lines, "Some tools may be unavailable due to sandbox policy.")
	if ws, ok := sandboxMap["workspace_dir"].(string); ok && ws != "" {
		lines = append(lines, fmt.Sprintf("Sandbox workspace: %s", agent.SanitizePromptLiteral(ws)))
	}
	return strings.Join(lines, "\n")
}

func buildSkillsSection(skillsPrompt string) string {
	trimmed := strings.TrimSpace(skillsPrompt)
	if trimmed == "" {
		return ""
	}
	var lines []string
	lines = append(lines, "## Skills (mandatory)")
	lines = append(lines, "Before replying: scan <available_skills> descriptions and choose the narrowest matching skill.")
	lines = append(lines, "- Read exactly one skill unless multiple are clearly required for the task.")
	lines = append(lines, "- If multiple could apply: choose the most specific one first.")
	lines = append(lines, "- If none clearly apply: do not read any SKILL.md.")
	lines = append(lines, trimmed)
	return strings.Join(lines, "\n")
}

func buildDocsSection(workspaceDir string) string {
	// Look for a docs directory in the workspace or a standard location.
	candidates := []string{
		filepath.Join(workspaceDir, "docs"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".metiq", "docs"))
	}
	for _, d := range candidates {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return fmt.Sprintf("## Documentation\nMetiq docs: %s\nFor metiq behavior, commands, config, or architecture: consult local docs first.", agent.SanitizePromptLiteral(d))
		}
	}
	return ""
}

func buildBootstrapWarningSection(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}
	lines := []string{"⚠ Bootstrap prompt warnings:"}
	for _, warning := range warnings {
		warning = strings.TrimSpace(agent.SanitizePromptLiteral(warning))
		if warning == "" {
			continue
		}
		lines = append(lines, "- "+warning)
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}
