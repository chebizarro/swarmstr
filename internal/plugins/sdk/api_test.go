package sdk

import (
	"context"
	"encoding/json"
	"testing"
)

// ─── WithChannelReplyTarget / ChannelReplyTarget ──────────────────────────────

func TestChannelReplyTarget_RoundTrip(t *testing.T) {
	ctx := WithChannelReplyTarget(context.Background(), "target-123")
	got := ChannelReplyTarget(ctx)
	if got != "target-123" {
		t.Errorf("got %q, want target-123", got)
	}
}

func TestChannelReplyTarget_EmptyTarget(t *testing.T) {
	ctx := WithChannelReplyTarget(context.Background(), "")
	// Should return original context (no value set)
	got := ChannelReplyTarget(ctx)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestChannelReplyTarget_NilContext(t *testing.T) {
	ctx := WithChannelReplyTarget(nil, "target")
	if ctx != nil {
		t.Error("expected nil context returned")
	}
	got := ChannelReplyTarget(nil)
	if got != "" {
		t.Errorf("expected empty for nil context, got %q", got)
	}
}

func TestChannelReplyTarget_NoValueSet(t *testing.T) {
	got := ChannelReplyTarget(context.Background())
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ─── ValidateManifest ─────────────────────────────────────────────────────────

func TestValidateManifest_Valid(t *testing.T) {
	m := Manifest{
		ID:      "my-plugin",
		Version: "1.0.0",
		Tools: []ToolSchema{
			{Name: "tool1", Description: "does thing"},
		},
	}
	if err := ValidateManifest(m); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateManifest_EmptyID(t *testing.T) {
	m := Manifest{ID: ""}
	if err := ValidateManifest(m); err == nil {
		t.Error("expected error for empty ID")
	}
}

func TestValidateManifest_WhitespaceID(t *testing.T) {
	m := Manifest{ID: "  "}
	if err := ValidateManifest(m); err == nil {
		t.Error("expected error for whitespace-only ID")
	}
}

func TestValidateManifest_EmptyToolName(t *testing.T) {
	m := Manifest{
		ID:    "p1",
		Tools: []ToolSchema{{Name: ""}},
	}
	if err := ValidateManifest(m); err == nil {
		t.Error("expected error for empty tool name")
	}
}

func TestValidateManifest_DuplicateToolName(t *testing.T) {
	m := Manifest{
		ID: "p1",
		Tools: []ToolSchema{
			{Name: "tool1"},
			{Name: "tool1"},
		},
	}
	if err := ValidateManifest(m); err == nil {
		t.Error("expected error for duplicate tool name")
	}
}

func TestValidateManifest_NoTools(t *testing.T) {
	m := Manifest{ID: "p1"}
	if err := ValidateManifest(m); err != nil {
		t.Errorf("no tools should be valid: %v", err)
	}
}

// ─── ValidateToolSchema ──────────────────────────────────────────────────────

func TestValidateToolSchema_EmptyName(t *testing.T) {
	if err := ValidateToolSchema(ToolSchema{Name: ""}); err == nil {
		t.Error("expected error")
	}
}

func TestValidateToolSchema_NoParams(t *testing.T) {
	if err := ValidateToolSchema(ToolSchema{Name: "t1"}); err != nil {
		t.Errorf("no params should be valid: %v", err)
	}
}

func TestValidateToolSchema_ValidParams(t *testing.T) {
	tool := ToolSchema{
		Name: "t1",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
	}
	if err := ValidateToolSchema(tool); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateToolSchema_InvalidType(t *testing.T) {
	tool := ToolSchema{
		Name:       "t1",
		Parameters: map[string]any{"type": "array"},
	}
	if err := ValidateToolSchema(tool); err == nil {
		t.Error("expected error for non-object type")
	}
}

func TestValidateToolSchema_EmptyType(t *testing.T) {
	tool := ToolSchema{
		Name:       "t1",
		Parameters: map[string]any{"type": ""},
	}
	if err := ValidateToolSchema(tool); err == nil {
		t.Error("expected error for empty type")
	}
}

func TestValidateToolSchema_NonStringType(t *testing.T) {
	tool := ToolSchema{
		Name:       "t1",
		Parameters: map[string]any{"type": 42},
	}
	if err := ValidateToolSchema(tool); err == nil {
		t.Error("expected error for non-string type")
	}
}

func TestValidateToolSchema_PropertiesNotObject(t *testing.T) {
	tool := ToolSchema{
		Name:       "t1",
		Parameters: map[string]any{"properties": "nope"},
	}
	if err := ValidateToolSchema(tool); err == nil {
		t.Error("expected error for non-object properties")
	}
}

func TestValidateToolSchema_RequiredNotArray(t *testing.T) {
	tool := ToolSchema{
		Name:       "t1",
		Parameters: map[string]any{"required": "name"},
	}
	if err := ValidateToolSchema(tool); err == nil {
		t.Error("expected error for non-array required")
	}
}

func TestValidateToolSchema_RequiredEmptyItem(t *testing.T) {
	tool := ToolSchema{
		Name:       "t1",
		Parameters: map[string]any{"required": []any{""}},
	}
	if err := ValidateToolSchema(tool); err == nil {
		t.Error("expected error for empty required item")
	}
}

func TestValidateToolSchema_RequiredStringSlice(t *testing.T) {
	tool := ToolSchema{
		Name:       "t1",
		Parameters: map[string]any{"required": []string{"name", "age"}},
	}
	if err := ValidateToolSchema(tool); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateToolSchema_RequiredStringSliceEmpty(t *testing.T) {
	tool := ToolSchema{
		Name:       "t1",
		Parameters: map[string]any{"required": []string{""}},
	}
	if err := ValidateToolSchema(tool); err == nil {
		t.Error("expected error for empty string in required")
	}
}

// ─── JSON round-trips ─────────────────────────────────────────────────────────

func TestManifest_JSONRoundTrip(t *testing.T) {
	m := Manifest{
		ID: "test", Version: "1.0",
		Tools: []ToolSchema{{Name: "t1", Description: "desc"}},
	}
	b, _ := json.Marshal(m)
	var decoded Manifest
	json.Unmarshal(b, &decoded)
	if decoded.ID != m.ID || len(decoded.Tools) != 1 {
		t.Errorf("mismatch: %+v", decoded)
	}
}

func TestInvokeRequest_JSONRoundTrip(t *testing.T) {
	r := InvokeRequest{
		Tool: "my_tool",
		Args: map[string]any{"key": "val"},
		Meta: map[string]any{"session_id": "s1"},
	}
	b, _ := json.Marshal(r)
	var decoded InvokeRequest
	json.Unmarshal(b, &decoded)
	if decoded.Tool != r.Tool {
		t.Errorf("tool: %q", decoded.Tool)
	}
}

func TestInvokeResult_JSONRoundTrip(t *testing.T) {
	r := InvokeResult{Value: "ok", Error: ""}
	b, _ := json.Marshal(r)
	var decoded InvokeResult
	json.Unmarshal(b, &decoded)
	if decoded.Value != "ok" || decoded.Error != "" {
		t.Errorf("mismatch: %+v", decoded)
	}
}

func TestCompletionOpts_JSONRoundTrip(t *testing.T) {
	opts := CompletionOpts{Model: "gpt-4", SystemPrompt: "You are helpful", MaxTokens: 1000}
	b, _ := json.Marshal(opts)
	var decoded CompletionOpts
	json.Unmarshal(b, &decoded)
	if decoded != opts {
		t.Errorf("mismatch: %+v", decoded)
	}
}

func TestChannelCapabilities_Defaults(t *testing.T) {
	var caps ChannelCapabilities
	if caps.Typing || caps.Reactions || caps.Threads || caps.Audio || caps.Edit || caps.MultiAccount || caps.E2EEncryption {
		t.Error("default capabilities should all be false")
	}
}

func TestInboundChannelMessage_Fields(t *testing.T) {
	msg := InboundChannelMessage{
		ChannelID:      "ch1",
		SenderID:       "s1",
		Text:           "hello",
		EventID:        "e1",
		CreatedAt:      1700000000,
		ThreadID:       "th1",
		ReplyToEventID: "re1",
		MediaURL:       "https://example.com/image.png",
		MediaMIME:      "image/png",
	}
	if msg.ChannelID != "ch1" || msg.SenderID != "s1" || msg.Text != "hello" {
		t.Errorf("basic fields: %+v", msg)
	}
	if msg.ThreadID != "th1" || msg.ReplyToEventID != "re1" {
		t.Errorf("thread fields: %+v", msg)
	}
	if msg.MediaURL == "" || msg.MediaMIME == "" {
		t.Error("media fields should be set")
	}
}
