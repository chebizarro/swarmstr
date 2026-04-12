package agent

import (
	"strings"
	"testing"
)

// ─── Policy resolver tests ───────────────────────────────────────────────────

func TestResolveOpenAITranscriptPolicy_UsesStrict9ForMistral(t *testing.T) {
	policy := ResolveOpenAITranscriptPolicy("mistral-large-latest", "https://api.mistral.ai/v1")
	if !policy.SanitizeToolCallIDs || policy.ToolCallIDMode != ToolCallIDModeStrict9 {
		t.Fatalf("unexpected policy: %+v", policy)
	}
	if policy.AllowSyntheticResults {
		t.Fatalf("openai-compatible policy should not synthesize tool results")
	}
}

func TestResolveAnthropicTranscriptPolicy_AllowsSyntheticRepair(t *testing.T) {
	policy := ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5")
	if !policy.RepairToolUseResultPair || !policy.AllowSyntheticResults {
		t.Fatalf("unexpected anthropic policy: %+v", policy)
	}
}

func TestResolveAnthropicTranscriptPolicy_HasProviderConstraints(t *testing.T) {
	policy := ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5")
	if !policy.EnforceRoleAlternation {
		t.Fatal("anthropic should enforce role alternation")
	}
	if !policy.RequireLeadingUser {
		t.Fatal("anthropic should require leading user")
	}
	if !policy.MergeConsecutiveRoles {
		t.Fatal("anthropic should merge consecutive roles")
	}
	if !policy.StripMidSystemMessages {
		t.Fatal("anthropic should strip mid-system messages")
	}
	if !policy.FillEmptyContent {
		t.Fatal("anthropic should fill empty content")
	}
	if policy.ToolCallIDMaxLen != 64 {
		t.Fatalf("anthropic tool call ID max len should be 64, got %d", policy.ToolCallIDMaxLen)
	}
}

func TestResolveGeminiTranscriptPolicy_HasProviderConstraints(t *testing.T) {
	policy := ResolveGeminiTranscriptPolicy("gemini-2.0-flash")
	if !policy.EnforceRoleAlternation {
		t.Fatal("gemini should enforce role alternation")
	}
	if !policy.StripMidSystemMessages {
		t.Fatal("gemini should strip mid-system messages")
	}
	if policy.FillEmptyContent {
		t.Fatal("gemini should not fill empty content")
	}
}

func TestResolveOpenAITranscriptPolicy_NoOrderingConstraints(t *testing.T) {
	policy := ResolveOpenAITranscriptPolicy("gpt-4o", "https://api.openai.com/v1")
	if policy.EnforceRoleAlternation {
		t.Fatal("openai should not enforce role alternation")
	}
	if policy.RequireLeadingUser {
		t.Fatal("openai should not require leading user")
	}
	if policy.StripMidSystemMessages {
		t.Fatal("openai should not strip mid-system messages")
	}
}

// ─── Existing pipeline tests (updated) ──────────────────────────────────────

func TestPrepareTranscriptMessages_MovesResultsAndSynthesizesForAnthropic(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call:1", Name: "read_file"}, {ID: "call:2", Name: "bash_exec"}}},
		{Role: "user", Content: "interleaved user"},
		{Role: "tool", ToolCallID: "call:1", Content: "first result"},
		{Role: "tool", ToolCallID: "orphan", Content: "drop me"},
	}

	prepared := PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))

	// After repair: sys, [synthetic user], assistant(2 calls), tool(call1), tool(call2 synth), user(interleaved)
	// The pipeline:
	// 1. normalize → keeps all valid
	// 2. strip mid-system → sys stays (it's first)
	// 3. sanitize IDs → call1, call2
	// 4. repair pairs → assistant+2tools, then user remainder
	// 5. merge consecutive → no consecutive same-role
	// 6. enforce alternation → sys already at start; first non-sys is assistant → need leading user
	// 7. leading user → inserted before assistant
	// 8. fill empty → no empty msgs

	// Find the assistant message.
	var assistantIdx int
	for i, m := range prepared {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			assistantIdx = i
			break
		}
	}

	if prepared[assistantIdx].ToolCalls[0].ID != "call1" || prepared[assistantIdx].ToolCalls[1].ID != "call2" {
		t.Fatalf("expected strict sanitized IDs, got %+v", prepared[assistantIdx].ToolCalls)
	}

	// After assistant, we should see two tool results.
	if assistantIdx+1 >= len(prepared) || prepared[assistantIdx+1].Role != "tool" {
		t.Fatalf("expected tool result after assistant, got %+v", prepared[assistantIdx+1:])
	}
	if prepared[assistantIdx+1].ToolCallID != "call1" || prepared[assistantIdx+1].Content != "first result" {
		t.Fatalf("expected matching tool result, got %+v", prepared[assistantIdx+1])
	}
	if assistantIdx+2 >= len(prepared) || prepared[assistantIdx+2].Role != "tool" {
		t.Fatalf("expected synthetic tool result, got %+v", prepared[assistantIdx+2:])
	}
	if !strings.Contains(prepared[assistantIdx+2].Content, "previous turn ended") {
		t.Fatalf("expected synthetic content, got %+v", prepared[assistantIdx+2])
	}

	// Should end with user message.
	last := prepared[len(prepared)-1]
	if last.Role != "user" || last.Content != "interleaved user" {
		t.Fatalf("expected trailing user, got %+v", last)
	}
}

func TestPrepareTranscriptMessages_DoesNotSynthesizeForOpenAI(t *testing.T) {
	messages := []LLMMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "call:1", Name: "read_file"}}}}
	prepared := PrepareTranscriptMessages(messages, ResolveOpenAITranscriptPolicy("gpt-4o", "https://api.openai.com/v1"))
	if len(prepared) != 0 {
		t.Fatalf("expected dangling openai tool call to be dropped, got %+v", prepared)
	}
}

func TestPrepareTranscriptMessages_Strict9SanitizesAndDeduplicates(t *testing.T) {
	messages := []LLMMessage{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "mistral/tool:one", Name: "read_file"},
				{ID: "mistral-tool-one", Name: "bash_exec"},
			},
		},
		{Role: "tool", ToolCallID: "mistral/tool:one", Content: "one"},
		{Role: "tool", ToolCallID: "mistral-tool-one", Content: "two"},
	}
	prepared := PrepareTranscriptMessages(messages, ResolveOpenAITranscriptPolicy("mistral-large-latest", "https://api.mistral.ai/v1"))
	if len(prepared) != 3 {
		t.Fatalf("expected repaired mistral transcript, got %+v", prepared)
	}
	ids := []string{prepared[0].ToolCalls[0].ID, prepared[0].ToolCalls[1].ID}
	for _, id := range ids {
		if len(id) != 9 {
			t.Fatalf("expected strict9 id, got %q", id)
		}
		for _, r := range id {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
				t.Fatalf("expected alphanumeric strict9 id, got %q", id)
			}
		}
	}
	if ids[0] == ids[1] {
		t.Fatalf("expected collision-resistant strict9 ids, got %+v", ids)
	}
}

func TestPrepareTranscriptMessages_AllowsSameToolIDAcrossSeparateTurns(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "search", Name: "search"}}},
		{Role: "tool", ToolCallID: "search", Content: "first"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "search", Name: "search"}}},
		{Role: "tool", ToolCallID: "search", Content: "second"},
	}
	prepared := PrepareTranscriptMessages(messages, ResolveGeminiTranscriptPolicy("gemini-2.0-flash"))

	// With Gemini policy (enforces alternation), we get synthetic user messages
	// between the tool spans: [synthetic user], assistant, tool, [synthetic user], assistant, tool
	assistantCount := 0
	toolCount := 0
	for _, m := range prepared {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			assistantCount++
		}
		if m.Role == "tool" {
			toolCount++
		}
	}
	if assistantCount != 2 || toolCount != 2 {
		t.Fatalf("expected 2 assistant+tool spans, got assistants=%d tools=%d in %+v", assistantCount, toolCount, prepared)
	}
}

func TestPrepareTranscriptMessages_PreservesDottedAndSlashedToolNames(t *testing.T) {
	messages := []LLMMessage{{
		Role:      "assistant",
		ToolCalls: []ToolCall{{ID: "call:1", Name: "memory.search"}, {ID: "call:2", Name: "plugin/raw"}},
	}}
	prepared := PrepareTranscriptMessages(messages, ResolveOpenAITranscriptPolicy("gpt-4o", "https://api.openai.com/v1"))
	if len(prepared) != 0 {
		t.Fatalf("expected openai policy to drop dangling tool span but preserve valid names during normalization, got %+v", prepared)
	}
	anthropicPrepared := PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))
	// With anthropic: repair synthesizes results, leading user inserted, fill empty
	foundNames := map[string]bool{}
	for _, m := range anthropicPrepared {
		for _, tc := range m.ToolCalls {
			foundNames[tc.Name] = true
		}
	}
	if !foundNames["memory.search"] || !foundNames["plugin/raw"] {
		t.Fatalf("expected dotted/slashed tool names to survive, got names %v in %+v", foundNames, anthropicPrepared)
	}
}

func TestPrepareTranscriptMessages_DropsInvalidToolCalls(t *testing.T) {
	messages := []LLMMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "ok", Name: "bad name"}}}}
	prepared := PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))
	// Invalid tool call gets dropped → empty assistant gets dropped → nothing left
	for _, m := range prepared {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			t.Fatalf("expected invalid tool call to be dropped, got %+v", prepared)
		}
	}
}

// ─── New: Strip mid-system messages ──────────────────────────────────────────

func TestStripMidSystemMessages(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "sys prompt"},
		{Role: "user", Content: "hello"},
		{Role: "system", Content: "injected system"},
		{Role: "assistant", Content: "response"},
	}
	stripped, ok := stripMidSystemMessages(messages)
	if !ok {
		t.Fatal("expected change")
	}
	if len(stripped) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(stripped), stripped)
	}
	for _, m := range stripped {
		if m.Role == "system" && m.Content == "injected system" {
			t.Fatal("mid-system message should have been stripped")
		}
	}
}

func TestStripMidSystemMessages_KeepsLeadingSystem(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "keep me"},
		{Role: "user", Content: "hello"},
	}
	_, ok := stripMidSystemMessages(messages)
	if ok {
		t.Fatal("expected no change when system is only at start")
	}
}

func TestStripMidSystemMessages_MultipleleadingSystems(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "first sys"},
		{Role: "system", Content: "second sys"},
		{Role: "user", Content: "hello"},
	}
	_, ok := stripMidSystemMessages(messages)
	if ok {
		t.Fatal("expected no change — both systems are before any non-system")
	}
}

// ─── New: Merge consecutive roles ────────────────────────────────────────────

func TestMergeConsecutiveTranscriptRoles(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "response"},
	}
	merged, ok := mergeConsecutiveTranscriptRoles(messages)
	if !ok {
		t.Fatal("expected change")
	}
	if len(merged) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(merged), merged)
	}
	if merged[0].Content != "first\n\nsecond" {
		t.Fatalf("expected merged content, got %q", merged[0].Content)
	}
}

func TestMergeConsecutiveTranscriptRoles_SkipsToolMessages(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "1", Name: "search"}}},
		{Role: "assistant", Content: "follow-up"},
	}
	merged, ok := mergeConsecutiveTranscriptRoles(messages)
	if ok {
		t.Fatalf("expected no merge for tool-bearing assistant, got %+v", merged)
	}
}

func TestMergeConsecutiveTranscriptRoles_SkipsImageMessages(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: "text", Images: []ImageRef{{URL: "http://example.com/img.png"}}},
		{Role: "user", Content: "more text"},
	}
	_, ok := mergeConsecutiveTranscriptRoles(messages)
	if ok {
		t.Fatal("expected no merge for image-bearing messages")
	}
}

// ─── New: Enforce role alternation ───────────────────────────────────────────

func TestEnforceTranscriptRoleAlternation_InsertsSyntheticUser(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "assistant", Content: "continued"}, // violation
	}
	fixed, ok := enforceTranscriptRoleAlternation(messages)
	if !ok {
		t.Fatal("expected change")
	}
	// Should have synthetic user before second assistant.
	if len(fixed) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(fixed), fixed)
	}
	if fixed[2].Role != "user" || fixed[2].Content != syntheticAlternationText {
		t.Fatalf("expected synthetic user at index 2, got %+v", fixed[2])
	}
	if fixed[3].Role != "assistant" || fixed[3].Content != "continued" {
		t.Fatalf("expected original assistant at index 3, got %+v", fixed[3])
	}
}

func TestEnforceTranscriptRoleAlternation_InsertsSyntheticAssistant(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"}, // violation
	}
	fixed, ok := enforceTranscriptRoleAlternation(messages)
	if !ok {
		t.Fatal("expected change")
	}
	if len(fixed) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(fixed), fixed)
	}
	if fixed[1].Role != "assistant" || fixed[1].Content != syntheticAlternationText {
		t.Fatalf("expected synthetic assistant, got %+v", fixed[1])
	}
}

func TestEnforceTranscriptRoleAlternation_IgnoresToolAndSystem(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "1", Name: "fn"}}},
		{Role: "tool", ToolCallID: "1", Content: "result"},
		{Role: "assistant", Content: "follow-up"}, // tool is pass-through, so this follows assistant → violation
	}
	fixed, ok := enforceTranscriptRoleAlternation(messages)
	if !ok {
		t.Fatal("expected change — consecutive assistant messages around tool")
	}
	// Should inject synthetic user between tool result and second assistant.
	foundSynthetic := false
	for i, m := range fixed {
		if m.Role == "user" && m.Content == syntheticAlternationText && i > 0 {
			foundSynthetic = true
		}
	}
	if !foundSynthetic {
		t.Fatalf("expected synthetic message injected, got %+v", fixed)
	}
}

func TestEnforceTranscriptRoleAlternation_NoChangeWhenAlternating(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "bye"},
	}
	_, ok := enforceTranscriptRoleAlternation(messages)
	if ok {
		t.Fatal("expected no change for properly alternating messages")
	}
}

// ─── New: Ensure leading user ────────────────────────────────────────────────

func TestEnsureTranscriptLeadingUser_InsertsAfterSystem(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "hi"},
	}
	fixed, ok := ensureTranscriptLeadingUser(messages)
	if !ok {
		t.Fatal("expected change")
	}
	if len(fixed) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(fixed), fixed)
	}
	if fixed[0].Role != "system" {
		t.Fatal("system should remain first")
	}
	if fixed[1].Role != "user" || fixed[1].Content != syntheticAlternationText {
		t.Fatalf("expected synthetic user at index 1, got %+v", fixed[1])
	}
	if fixed[2].Role != "assistant" {
		t.Fatal("assistant should follow synthetic user")
	}
}

func TestEnsureTranscriptLeadingUser_NoChangeWhenAlreadyUser(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
	}
	_, ok := ensureTranscriptLeadingUser(messages)
	if ok {
		t.Fatal("expected no change when first non-system is user")
	}
}

func TestEnsureTranscriptLeadingUser_NoSystemMessages(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", Content: "hi"},
	}
	fixed, ok := ensureTranscriptLeadingUser(messages)
	if !ok {
		t.Fatal("expected change")
	}
	if len(fixed) != 2 || fixed[0].Role != "user" {
		t.Fatalf("expected synthetic user prepended, got %+v", fixed)
	}
}

// ─── New: Fill empty content ─────────────────────────────────────────────────

func TestFillTranscriptEmptyContent_FillsEmptyUser(t *testing.T) {
	messages := []LLMMessage{
		{Role: "user", Content: ""},
		{Role: "assistant", Content: ""},
	}
	filled, ok := fillTranscriptEmptyContent(messages)
	if !ok {
		t.Fatal("expected change")
	}
	if filled[0].Content != emptyContentFill {
		t.Fatalf("expected fill, got %q", filled[0].Content)
	}
	if filled[1].Content != emptyContentFill {
		t.Fatalf("expected fill for empty assistant, got %q", filled[1].Content)
	}
}

func TestFillTranscriptEmptyContent_SkipsToolCallAssistant(t *testing.T) {
	messages := []LLMMessage{
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "1", Name: "fn"}}},
	}
	_, ok := fillTranscriptEmptyContent(messages)
	if ok {
		t.Fatal("should not fill tool-calling assistant with empty content")
	}
}

func TestFillTranscriptEmptyContent_SkipsToolAndSystem(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: ""},
		{Role: "tool", ToolCallID: "1", Content: ""},
	}
	_, ok := fillTranscriptEmptyContent(messages)
	if ok {
		t.Fatal("should not fill system or tool messages")
	}
}

// ─── New: Tool call ID max length ────────────────────────────────────────────

func TestToolCallIDMaxLen_Anthropic64(t *testing.T) {
	longID := strings.Repeat("a", 100)
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: longID, Name: "search"}}},
		{Role: "tool", ToolCallID: longID, Content: "result"},
	}
	prepared := PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))

	for _, m := range prepared {
		for _, tc := range m.ToolCalls {
			if len(tc.ID) > 64 {
				t.Fatalf("tool call ID exceeds 64 chars: %d", len(tc.ID))
			}
		}
		if m.Role == "tool" && len(m.ToolCallID) > 64 {
			t.Fatalf("tool result ID exceeds 64 chars: %d", len(m.ToolCallID))
		}
	}
}

func TestToolCallIDMaxLen_DefaultForOpenAI(t *testing.T) {
	longID := strings.Repeat("x", 50)
	messages := []LLMMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: longID, Name: "search"}}},
		{Role: "tool", ToolCallID: longID, Content: "result"},
	}
	prepared := PrepareTranscriptMessages(messages, ResolveOpenAITranscriptPolicy("gpt-4o", ""))

	for _, m := range prepared {
		for _, tc := range m.ToolCalls {
			if len(tc.ID) > 40 {
				t.Fatalf("openai tool call ID should truncate to 40, got %d", len(tc.ID))
			}
		}
	}
}

// ─── New: Full pipeline integration with Anthropic policy ────────────────────

func TestPrepareTranscriptMessages_AnthropicFullPipeline(t *testing.T) {
	// Pathological transcript: mid-system, consecutive users, starts with assistant,
	// empty content, long tool IDs.
	messages := []LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "system", Content: "mid-system should be stripped"},
		{Role: "assistant", Content: "orphan assistant"},
		{Role: "user", Content: ""},
		{Role: "user", Content: "real question"},
		{Role: "assistant", Content: "answer"},
	}

	prepared := PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))

	// Verify no mid-system messages.
	systemCount := 0
	for _, m := range prepared {
		if m.Role == "system" {
			systemCount++
		}
	}
	// Both system messages are before any non-system, so both survive strip.
	// But after strip, we have: sys, sys, assistant, user(""), user, assistant
	// Leading system is fine. But the second system is also before non-system.
	// Actually stripMidSystemMessages checks seenNonSystem — both systems are
	// before any non-system, so both survive. Let me re-check.
	// sys → non-system? no → keep. sys("mid") → non-system? no → keep.
	// assistant → non-system → seenNonSystem=true. etc.
	// So both systems survive. That's correct — they're both leading.
	if systemCount != 2 {
		t.Fatalf("expected 2 leading system messages to survive, got %d in %+v", systemCount, prepared)
	}

	// Verify no empty content.
	for _, m := range prepared {
		if m.Content == "" && m.Role != "tool" && m.Role != "system" {
			if m.Role == "assistant" && len(m.ToolCalls) > 0 {
				continue
			}
			t.Fatalf("empty content found: %+v", m)
		}
	}

	// Verify role alternation: check no consecutive user or assistant.
	lastConv := ""
	for _, m := range prepared {
		if m.Role == "system" || m.Role == "tool" {
			continue
		}
		if m.Role == lastConv {
			t.Fatalf("alternation violated: consecutive %q in %+v", m.Role, prepared)
		}
		lastConv = m.Role
	}
}

func TestPrepareTranscriptMessages_GeminiStripsMidSystem(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "system", Content: "injected"},
		{Role: "assistant", Content: "world"},
	}
	prepared := PrepareTranscriptMessages(messages, ResolveGeminiTranscriptPolicy("gemini-2.0-flash"))
	for _, m := range prepared {
		if m.Role == "system" && m.Content == "injected" {
			t.Fatal("mid-system should be stripped for Gemini")
		}
	}
}

func TestPrepareTranscriptMessages_OpenAIKeepsMidSystem(t *testing.T) {
	messages := []LLMMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "system", Content: "mid-system ok for openai"},
		{Role: "assistant", Content: "world"},
	}
	prepared := PrepareTranscriptMessages(messages, ResolveOpenAITranscriptPolicy("gpt-4o", ""))
	found := false
	for _, m := range prepared {
		if m.Role == "system" && m.Content == "mid-system ok for openai" {
			found = true
		}
	}
	if !found {
		t.Fatal("OpenAI policy should preserve mid-system messages")
	}
}

// ─── Edge cases ──────────────────────────────────────────────────────────────

func TestPrepareTranscriptMessages_EmptyInput(t *testing.T) {
	prepared := PrepareTranscriptMessages(nil, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))
	if len(prepared) != 0 {
		t.Fatalf("expected empty output for nil input, got %+v", prepared)
	}
	prepared = PrepareTranscriptMessages([]LLMMessage{}, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))
	if len(prepared) != 0 {
		t.Fatalf("expected empty output for empty input, got %+v", prepared)
	}
}

func TestPrepareTranscriptMessages_SystemOnlyInput(t *testing.T) {
	messages := []LLMMessage{{Role: "system", Content: "sys"}}
	prepared := PrepareTranscriptMessages(messages, ResolveAnthropicTranscriptPolicy("claude-sonnet-4-5"))
	if len(prepared) != 1 || prepared[0].Role != "system" {
		t.Fatalf("expected system-only passthrough, got %+v", prepared)
	}
}

func TestResolveToolCallIDMaxLen_Strict9Always9(t *testing.T) {
	got := resolveToolCallIDMaxLen(ToolCallIDModeStrict9, 100)
	if got != 9 {
		t.Fatalf("strict9 should always return 9, got %d", got)
	}
}

func TestResolveToolCallIDMaxLen_UsesPolicy(t *testing.T) {
	got := resolveToolCallIDMaxLen(ToolCallIDModeStrict, 64)
	if got != 64 {
		t.Fatalf("expected policy max 64, got %d", got)
	}
}

func TestResolveToolCallIDMaxLen_DefaultsTo40(t *testing.T) {
	got := resolveToolCallIDMaxLen(ToolCallIDModeStrict, 0)
	if got != 40 {
		t.Fatalf("expected default 40, got %d", got)
	}
}
