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
	skillsPromptCacheMu sync.Mutex
	skillsPromptCache   = map[string]ttlCacheEntry[string]{}

	docsSectionCacheMu sync.Mutex
	docsSectionCache   = map[string]ttlCacheEntry[string]{}

	bootstrapWarnCacheMu sync.Mutex
	bootstrapWarnCache   = map[string]ttlCacheEntry[string]{}
)

// turnRuntimeParams holds the data needed to build the runtime context.
type turnRuntimeParams struct {
	// Agent identity
	AgentID      string
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
}

func buildSkillsPromptCached(bundledSkillsDir, skillsWsDir string) string {
	const ttl = 60 * time.Second
	key := strings.TrimSpace(bundledSkillsDir) + "\n" + strings.TrimSpace(skillsWsDir)
	now := time.Now()

	skillsPromptCacheMu.Lock()
	if ent, ok := skillsPromptCache[key]; ok && now.Before(ent.expiresAt) {
		v := ent.value
		skillsPromptCacheMu.Unlock()
		return v
	}
	skillsPromptCacheMu.Unlock()

	var skillLines []string
	if dir := strings.TrimSpace(bundledSkillsDir); dir != "" {
		if skills, err := skillspkg.ScanBundledDir(dir); err == nil {
			for _, s := range skills {
				if s.IsEnabled() {
					name := s.Manifest.Name
					if name == "" {
						name = s.SkillKey
					}
					desc := strings.TrimSpace(s.Manifest.Description)
					if desc != "" {
						skillLines = append(skillLines, fmt.Sprintf("- %s: %s (location: %s)", name, desc, s.FilePath))
					}
				}
			}
		}
	}
	if dir := strings.TrimSpace(skillsWsDir); dir != "" {
		if skills, err := skillspkg.ScanWorkspace(dir); err == nil {
			for _, s := range skills {
				if s.IsEnabled() {
					name := s.Manifest.Name
					if name == "" {
						name = s.SkillKey
					}
					desc := strings.TrimSpace(s.Manifest.Description)
					if desc != "" {
						skillLines = append(skillLines, fmt.Sprintf("- %s: %s (location: %s)", name, desc, s.FilePath))
					}
				}
			}
		}
	}

	out := ""
	if len(skillLines) > 0 {
		out = "<available_skills>\n" + strings.Join(skillLines, "\n") + "\n</available_skills>"
	}

	skillsPromptCacheMu.Lock()
	skillsPromptCache[key] = ttlCacheEntry[string]{value: out, expiresAt: now.Add(ttl)}
	skillsPromptCacheMu.Unlock()
	return out
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

func buildBootstrapWarningSectionCached(workspaceDir string) string {
	const ttl = 5 * time.Minute
	key := strings.TrimSpace(workspaceDir)
	now := time.Now()

	bootstrapWarnCacheMu.Lock()
	if ent, ok := bootstrapWarnCache[key]; ok && now.Before(ent.expiresAt) {
		v := ent.value
		bootstrapWarnCacheMu.Unlock()
		return v
	}
	bootstrapWarnCacheMu.Unlock()

	out := buildBootstrapWarningSection(workspaceDir)
	bootstrapWarnCacheMu.Lock()
	bootstrapWarnCache[key] = ttlCacheEntry[string]{value: out, expiresAt: now.Add(ttl)}
	bootstrapWarnCacheMu.Unlock()
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
	if bw := buildBootstrapWarningSectionCached(p.WorkspaceDir); bw != "" {
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
	parts := []string{
		fmt.Sprintf("agent=%s", p.AgentID),
		fmt.Sprintf("host=%s", hostname),
		fmt.Sprintf("os=%s (%s)", runtime.GOOS, runtime.GOARCH),
		fmt.Sprintf("go=%s", runtime.Version()),
	}
	if p.Model != "" {
		parts = append(parts, fmt.Sprintf("model=%s", p.Model))
	}
	if p.Channel != "" {
		parts = append(parts, fmt.Sprintf("channel=%s", p.Channel))
	}
	if len(p.Capabilities) > 0 {
		parts = append(parts, fmt.Sprintf("capabilities=%s", strings.Join(p.Capabilities, ",")))
	} else if p.Channel != "" {
		parts = append(parts, "capabilities=none")
	}
	if p.ThinkingLevel != "" && p.ThinkingLevel != "off" {
		parts = append(parts, fmt.Sprintf("thinking=%s", p.ThinkingLevel))
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
		desc := strings.TrimSpace(t.Description)
		if desc != "" {
			// Use just the first sentence for the summary line.
			if idx := strings.Index(desc, ". "); idx > 0 && idx < 120 {
				desc = desc[:idx]
			}
			if len(desc) > 120 {
				desc = desc[:117] + "..."
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", t.Name, desc))
		} else {
			lines = append(lines, fmt.Sprintf("- %s", t.Name))
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
		lines = append(lines, fmt.Sprintf("Provider: %s", cfg.TTS.Provider))
	}
	if cfg.TTS.Voice != "" {
		lines = append(lines, fmt.Sprintf("Voice: %s", cfg.TTS.Voice))
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
		lines = append(lines, fmt.Sprintf("Sandbox workspace: %s", ws))
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
	lines = append(lines, "Before replying: scan <available_skills> <description> entries.")
	lines = append(lines, "- If exactly one skill clearly applies: read its SKILL.md, then follow it.")
	lines = append(lines, "- If multiple could apply: choose the most specific one.")
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
			return fmt.Sprintf("## Documentation\nMetiq docs: %s\nFor metiq behavior, commands, config, or architecture: consult local docs first.", d)
		}
	}
	return ""
}

func buildBootstrapWarningSection(workspaceDir string) string {
	// Check if any bootstrap files were large enough to warrant a truncation
	// warning.  We warn if any single file exceeds 50 KB.
	const warnThreshold = 50 * 1024
	bootstrapFiles := []string{"BOOTSTRAP.md", "SOUL.md", "IDENTITY.md", "USER.md", "AGENTS.md"}
	var warnings []string
	for _, fname := range bootstrapFiles {
		fpath := filepath.Join(workspaceDir, fname)
		info, err := os.Stat(fpath)
		if err != nil {
			continue
		}
		if info.Size() > warnThreshold {
			warnings = append(warnings, fmt.Sprintf("- %s is large (%d KB); consider trimming to improve cache efficiency",
				fname, info.Size()/1024))
		}
	}
	if len(warnings) == 0 {
		return ""
	}
	return "⚠ Bootstrap truncation warning:\n" + strings.Join(warnings, "\n")
}
