package main

import (
	"strings"
	"testing"

	"swarmstr/internal/agent"
	"swarmstr/internal/store/state"
)

func TestBuildTurnRuntimeContext_ContainsAllSections(t *testing.T) {
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
		Model:         "claude-3-5-sonnet-20241022",
		Channel:       "nostr",
		Tools:         tools,
		Config:        cfg,
		WorkspaceDir:  "/tmp/test-ws",
		ThinkingLevel: "medium",
	})

	checks := []struct {
		name    string
		substr  string
	}{
		{"runtime section", "## Runtime"},
		{"agent id", "agent=main"},
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
