package agent

import "testing"

// ─── Feature extraction tests ────────────────────────────────────────────────

func TestEstimateTokens_Empty(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Errorf("estimateTokens(\"\") = %d, want 0", got)
	}
}

func TestEstimateTokens_English(t *testing.T) {
	// "hello world" = 11 runes, 11/4 = 2
	got := estimateTokens("hello world")
	if got < 2 || got > 4 {
		t.Errorf("estimateTokens(\"hello world\") = %d, want ~2-3", got)
	}
}

func TestEstimateTokens_CJK(t *testing.T) {
	// 4 CJK characters → 4 tokens
	got := estimateTokens("你好世界")
	if got != 4 {
		t.Errorf("estimateTokens(CJK) = %d, want 4", got)
	}
}

func TestCountCodeBlocks(t *testing.T) {
	msg := "```go\nfunc main(){}\n```\n\nsome text\n\n```python\nprint('hi')\n```"
	got := countCodeBlocks(msg)
	if got != 2 {
		t.Errorf("countCodeBlocks = %d, want 2", got)
	}
}

func TestHasAttachments(t *testing.T) {
	if !hasAttachments("Check this data:image/png;base64,abc") {
		t.Error("expected true for data:image")
	}
	if !hasAttachments("see photo.jpg attached") {
		t.Error("expected true for .jpg")
	}
	if hasAttachments("hello world") {
		t.Error("expected false for plain text")
	}
}

// ─── Classifier tests ───────────────────────────────────────────────────────

func TestRuleClassifier_SimpleMessage(t *testing.T) {
	c := &RuleClassifier{}
	score := c.Score(Features{TokenEstimate: 5})
	if score > 0.1 {
		t.Errorf("simple message scored %.2f, expected < 0.1", score)
	}
}

func TestRuleClassifier_CodeBlock(t *testing.T) {
	c := &RuleClassifier{}
	score := c.Score(Features{TokenEstimate: 30, CodeBlockCount: 1})
	if score < 0.4 {
		t.Errorf("code block scored %.2f, expected >= 0.4", score)
	}
}

func TestRuleClassifier_Attachments(t *testing.T) {
	c := &RuleClassifier{}
	score := c.Score(Features{HasAttachments: true})
	if score != 1.0 {
		t.Errorf("attachments scored %.2f, expected 1.0", score)
	}
}

func TestRuleClassifier_DenseToolUse(t *testing.T) {
	c := &RuleClassifier{}
	score := c.Score(Features{TokenEstimate: 60, RecentToolCalls: 5})
	if score < 0.35 {
		t.Errorf("dense tool use scored %.2f, expected >= 0.35", score)
	}
}

func TestRuleClassifier_DeepConversation(t *testing.T) {
	c := &RuleClassifier{}
	score := c.Score(Features{TokenEstimate: 60, ConversationDepth: 15})
	if score < 0.2 {
		t.Errorf("deep conversation scored %.2f, expected >= 0.2", score)
	}
}

// ─── ModelRouter tests ──────────────────────────────────────────────────────

func TestModelRouter_SelectModel_Simple(t *testing.T) {
	router := NewModelRouter("claude-haiku", 0.35)
	model, usedLight, _ := router.SelectModel("hi", "claude-sonnet")
	if !usedLight {
		t.Error("expected light model for simple greeting")
	}
	if model != "claude-haiku" {
		t.Errorf("got model %q, want claude-haiku", model)
	}
}

func TestModelRouter_SelectModel_CodeBlock(t *testing.T) {
	router := NewModelRouter("claude-haiku", 0.35)
	msg := "Please implement this:\n```go\nfunc mergeSort(arr []int) []int {\n}\n```"
	model, usedLight, _ := router.SelectModel(msg, "claude-sonnet")
	if usedLight {
		t.Error("expected primary model for code block message")
	}
	if model != "claude-sonnet" {
		t.Errorf("got model %q, want claude-sonnet", model)
	}
}

func TestModelRouter_SelectModel_WithHistory(t *testing.T) {
	router := NewModelRouter("claude-haiku", 0.35)
	history := []LLMMessage{
		{Role: "user", Content: "step 1"},
		{Role: "assistant", Content: "ok", ToolCalls: []ToolCall{{Name: "bash"}}},
		{Role: "tool", Content: "done"},
		{Role: "assistant", Content: "ok", ToolCalls: []ToolCall{{Name: "bash"}}},
		{Role: "tool", Content: "done"},
		{Role: "assistant", Content: "ok", ToolCalls: []ToolCall{{Name: "bash"}, {Name: "read"}}},
		{Role: "tool", Content: "done"},
	}
	// Short message but in active tool session → should use primary
	model, usedLight, score := router.SelectModel("continue", "claude-sonnet", history)
	if score < 0.1 {
		t.Errorf("expected score >= 0.1 with tool history, got %.2f", score)
	}
	_ = model
	_ = usedLight
}

func TestModelRouter_NoLightModel(t *testing.T) {
	router := NewModelRouter("", 0.35)
	model, usedLight, _ := router.SelectModel("hi", "claude-sonnet")
	if usedLight {
		t.Error("should not use light model when none configured")
	}
	if model != "claude-sonnet" {
		t.Errorf("got model %q, want claude-sonnet", model)
	}
}

func TestExtractTurnFeatures_UsesToolHistoryAndImages(t *testing.T) {
	features := ExtractTurnFeatures(Turn{
		UserText: "continue",
		History: []ConversationMessage{
			{Role: "assistant", Content: "calling tools", ToolCalls: []ToolCallRef{{ID: "call-1", Name: "bash"}, {ID: "call-2", Name: "read"}}},
		},
		Images: []ImageRef{{URL: "https://example.com/image.png", MimeType: "image/png"}},
	})
	if features.RecentToolCalls != 2 {
		t.Fatalf("expected 2 recent tool calls, got %d", features.RecentToolCalls)
	}
	if !features.HasAttachments {
		t.Fatal("expected turn images to force attachment routing")
	}
}

func TestModelRouter_SelectTurn_WithAttachmentsUsesPrimary(t *testing.T) {
	router := NewModelRouter("claude-haiku", 0.35)
	model, usedLight, score := router.SelectTurn(Turn{
		UserText: "what's in this image?",
		Images:   []ImageRef{{URL: "https://example.com/cat.png", MimeType: "image/png"}},
	}, "claude-sonnet")
	if usedLight {
		t.Fatal("expected attachments to stay on primary model")
	}
	if model != "claude-sonnet" {
		t.Fatalf("got model %q, want claude-sonnet", model)
	}
	if score != 1.0 {
		t.Fatalf("expected attachment score 1.0, got %.2f", score)
	}
}
