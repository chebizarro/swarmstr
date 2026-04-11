package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/agent"
	"metiq/internal/agent/toolbuiltin"
	"metiq/internal/store/state"
)

func TestBuildTurnRuntimeContext_ContainsAllSections(t *testing.T) {
	var sk [32]byte
	sk[31] = 1
	selfPubkey := nostr.GetPublicKey(sk).Hex()
	selfNPub := toolbuiltin.NostrNPubFromHex(selfPubkey)
	if selfNPub == "" {
		t.Fatal("expected test pubkey to encode to npub")
	}
	tools := []agent.ToolDefinition{
		{Name: "memory_search", Description: "Search agent memory for relevant entries."},
		{Name: "nostr_fetch", Description: "Fetch Nostr events matching a filter."},
	}
	cfg := state.ConfigDoc{
		TTS: state.TTSConfig{Enabled: true, Provider: "elevenlabs", Voice: "adam"},
		Extra: map[string]any{
			"reactions": map[string]any{"enabled": true, "level": "minimal"},
		},
	}
	result := buildTurnRuntimeContext(turnRuntimeParams{
		AgentID:       "main",
		SelfPubkey:    selfPubkey,
		SelfNPub:      selfNPub,
		Model:         "claude-3-5-sonnet-20241022",
		Channel:       "nostr",
		Tools:         tools,
		Config:        cfg,
		WorkspaceDir:  "/tmp/test-ws",
		ThinkingLevel: "medium",
	})

	checks := []struct {
		name   string
		substr string
	}{
		{"runtime section", "## Runtime"},
		{"agent id", "agent=main"},
		{"self pubkey", "self_pubkey=" + selfPubkey},
		{"self npub", "self_npub=" + selfNPub},
		{"model", "model=claude-3-5-sonnet-20241022"},
		{"channel", "channel=nostr"},
		{"thinking", "thinking=medium"},
		{"os info", "os="},
		{"time section", "## Current Date & Time"},
		{"timezone", "Time zone:"},
		{"tool summaries", "## Available Tools"},
		{"memory_search tool", "memory_search:"},
		{"nostr_fetch tool", "nostr_fetch:"},
		{"model aliases", "## Model Aliases"},
		{"claude alias", "claude-*"},
		{"TTS section", "## Voice (TTS)"},
		{"TTS provider", "elevenlabs"},
		{"reactions section", "## Reactions"},
		{"reactions minimal", "MINIMAL mode"},
	}
	for _, c := range checks {
		if !strings.Contains(result, c.substr) {
			t.Errorf("%s: expected %q in output", c.name, c.substr)
		}
	}
}

func TestBuildTurnRuntimeContext_SplitsStaticAndDynamicSections(t *testing.T) {
	params := turnRuntimeParams{
		AgentID:       "main",
		Model:         "claude-3-5-sonnet-20241022",
		Channel:       "nostr",
		Tools:         []agent.ToolDefinition{{Name: "memory_search", Description: "Search agent memory for relevant entries."}},
		Config:        state.ConfigDoc{},
		WorkspaceDir:  "/tmp/test-ws",
		ThinkingLevel: "medium",
		SkillsPrompt:  "<available_skills>\n- test-skill: Does testing\n</available_skills>",
	}
	staticCtx := buildTurnRuntimeStaticContext(params)
	dynamicCtx := buildTurnRuntimeDynamicContext()
	combined := buildTurnRuntimeContext(params)

	if strings.Contains(staticCtx, "## Current Date & Time") {
		t.Fatal("static runtime context should not contain time section")
	}
	for _, want := range []string{"## Runtime", "## Available Tools", "## Skills (mandatory)"} {
		if !strings.Contains(staticCtx, want) {
			t.Fatalf("static runtime context missing %q", want)
		}
	}
	if dynamicCtx == "" || !strings.Contains(dynamicCtx, "## Current Date & Time") {
		t.Fatalf("dynamic runtime context should contain only time section, got %q", dynamicCtx)
	}
	if got := joinPromptSections(staticCtx, dynamicCtx); got != combined {
		t.Fatalf("combined runtime context mismatch")
	}
}

func TestBuildTurnRuntimeContext_NoTTS_WhenDisabled(t *testing.T) {
	result := buildTurnRuntimeContext(turnRuntimeParams{
		AgentID: "main",
		Config:  state.ConfigDoc{TTS: state.TTSConfig{Enabled: false}},
	})
	if strings.Contains(result, "## Voice (TTS)") {
		t.Error("TTS section should not appear when disabled")
	}
}

func TestBuildTurnRuntimeContext_NoReactions_WhenMissing(t *testing.T) {
	result := buildTurnRuntimeContext(turnRuntimeParams{
		AgentID: "main",
		Config:  state.ConfigDoc{},
	})
	if strings.Contains(result, "## Reactions") {
		t.Error("reactions section should not appear when not configured")
	}
}

func TestBuildTurnRuntimeContext_NoSandbox_WhenMissing(t *testing.T) {
	result := buildTurnRuntimeContext(turnRuntimeParams{
		AgentID: "main",
		Config:  state.ConfigDoc{},
	})
	if strings.Contains(result, "## Sandbox") {
		t.Error("sandbox section should not appear when not configured")
	}
}

func TestBuildTurnRuntimeContext_SkillsSection(t *testing.T) {
	result := buildTurnRuntimeContext(turnRuntimeParams{
		AgentID:      "main",
		Config:       state.ConfigDoc{},
		SkillsPrompt: "<available_skills>\n- test-skill: Does testing\n</available_skills>",
	})
	if !strings.Contains(result, "## Skills (mandatory)") {
		t.Error("skills section should appear when skills prompt provided")
	}
	if !strings.Contains(result, "test-skill") {
		t.Error("skills prompt content should be included")
	}
}

func TestBuildTurnRuntimeContext_ThinkingOff(t *testing.T) {
	result := buildTurnRuntimeContext(turnRuntimeParams{
		AgentID: "main",
		Config:  state.ConfigDoc{},
	})
	if !strings.Contains(result, "thinking=off") {
		t.Error("thinking=off should appear when no thinking level set")
	}
}

func TestBuildToolSummarySection_TruncatesLongDescriptions(t *testing.T) {
	tools := []agent.ToolDefinition{
		{Name: "verbose_tool", Description: strings.Repeat("x", 200)},
	}
	result := buildToolSummarySection(tools)
	if len(result) > 300 {
		t.Error("tool summary should truncate very long descriptions")
	}
	if !strings.Contains(result, "...") {
		t.Error("truncated description should end with ...")
	}
}

func TestBuildToolSummarySection_Empty(t *testing.T) {
	result := buildToolSummarySection(nil)
	if result != "" {
		t.Error("should return empty for no tools")
	}
}

func TestBuildSkillsPromptCached_UsesMergedCatalogAndAlwaysWarnings(t *testing.T) {
	bundledDir := t.TempDir()
	workspaceDir := t.TempDir()
	managedDir := t.TempDir()
	t.Setenv("METIQ_BUNDLED_SKILLS_DIR", bundledDir)
	t.Setenv("METIQ_MANAGED_SKILLS_DIR", managedDir)
	t.Setenv("METIQ_WORKSPACE", workspaceDir)

	writeSkill := func(root, name, content string) {
		t.Helper()
		skillDir := filepath.Join(root, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeSkill(bundledDir, "dup", `---
name: dup
description: bundled version
---
# Bundled
`)
	writeSkill(managedDir, "managed", `---
name: managed
description: managed skill
---
# Managed
`)
	writeSkill(workspaceDir, "dup", `---
name: dup
description: workspace version
when_to_use: Use when a workspace-specific override exists.
---
# Workspace
`)
	writeSkill(workspaceDir, "always-skill", `---
name: always-skill
description: always included
metadata:
  openclaw:
    always: true
    requires:
      env: ["MISSING_PROMPT_TOKEN"]
---
# Always
`)
	cfg := state.ConfigDoc{}

	prompt := buildSkillsPromptCached(cfg, "main")
	if !strings.Contains(prompt, "workspace version") {
		t.Fatalf("expected workspace skill in prompt: %s", prompt)
	}
	if strings.Contains(prompt, "bundled version") {
		t.Fatalf("expected bundled duplicate to be shadowed: %s", prompt)
	}
	if !strings.Contains(prompt, "managed skill") {
		t.Fatalf("expected managed skill in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "always; missing env: MISSING_PROMPT_TOKEN") {
		t.Fatalf("expected always skill warning in prompt: %s", prompt)
	}
}

func TestBuildExternalSessionPromptContext_HookSession(t *testing.T) {
	got := buildExternalSessionPromptContext("hook:webhook:deploys")
	if !strings.Contains(got, "## External Request Context") {
		t.Fatalf("expected external request header, got %q", got)
	}
	if !strings.Contains(got, "Source: Webhook") {
		t.Fatalf("expected webhook source metadata, got %q", got)
	}
}

func TestBuildExternalChannelMetadataContext_EmailIncludesThreadMetadata(t *testing.T) {
	cfg := state.ConfigDoc{
		NostrChannels: map[string]state.NostrChannelConfig{
			"inbox": {Kind: "email"},
		},
	}
	got := buildExternalChannelMetadataContext(cfg, "inbox", "user@example.com", "ch:inbox:user@example.com:thread:msg-42")
	for _, want := range []string{
		"## External Channel Metadata",
		"Source: Email",
		"channel_id: inbox",
		"sender_id: user@example.com",
		"thread_id: msg-42",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output: %q", want, got)
		}
	}
}
