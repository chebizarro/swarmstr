package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNostrCompose_Reply(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template":   "reply",
		"content":    "Great post!",
		"event_id":   "abc123def456",
		"pubkey":     "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"relay_hint": "wss://relay.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var spec struct {
		Kind    int        `json:"kind"`
		Content string     `json:"content"`
		Tags    [][]string `json:"tags"`
	}
	if err := json.Unmarshal([]byte(out), &spec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if spec.Kind != 1 {
		t.Errorf("kind = %d, want 1", spec.Kind)
	}
	if spec.Content != "Great post!" {
		t.Errorf("content = %q, want 'Great post!'", spec.Content)
	}
	// Should have e-tag and p-tag.
	if len(spec.Tags) < 2 {
		t.Fatalf("expected at least 2 tags, got %d", len(spec.Tags))
	}
	if spec.Tags[0][0] != "e" || spec.Tags[0][1] != "abc123def456" {
		t.Errorf("e-tag = %v, want [e abc123def456 ...]", spec.Tags[0])
	}
	if spec.Tags[0][2] != "wss://relay.example.com" {
		t.Errorf("relay hint = %q", spec.Tags[0][2])
	}
	if spec.Tags[0][3] != "reply" {
		t.Errorf("marker = %q, want 'reply'", spec.Tags[0][3])
	}
}

func TestNostrCompose_Reply_MissingEventID(t *testing.T) {
	tool := NostrComposeTool()
	_, err := tool(context.Background(), map[string]any{
		"template": "reply",
		"content":  "test",
	})
	if err == nil {
		t.Fatal("expected error for missing event_id")
	}
	if !strings.Contains(err.Error(), "event_id") {
		t.Errorf("error = %q, expected mention of event_id", err.Error())
	}
}

func TestNostrCompose_QuoteRepost(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template": "quote_repost",
		"content":  "Check this out!",
		"event_id": "deadbeef",
		"pubkey":   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var spec struct {
		Kind    int        `json:"kind"`
		Content string     `json:"content"`
		Tags    [][]string `json:"tags"`
	}
	json.Unmarshal([]byte(out), &spec)
	if spec.Kind != 1 {
		t.Errorf("kind = %d, want 1", spec.Kind)
	}
	if spec.Tags[0][0] != "q" || spec.Tags[0][1] != "deadbeef" {
		t.Errorf("q-tag = %v", spec.Tags[0])
	}
}

func TestNostrCompose_Repost(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template":   "repost",
		"event_id":   "abcdef",
		"pubkey":     "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"relay_hint": "wss://r.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var spec struct {
		Kind int        `json:"kind"`
		Tags [][]string `json:"tags"`
	}
	json.Unmarshal([]byte(out), &spec)
	if spec.Kind != 6 {
		t.Errorf("kind = %d, want 6", spec.Kind)
	}
	if spec.Tags[0][0] != "e" {
		t.Error("expected e-tag")
	}
	// Should have p-tag.
	foundP := false
	for _, tag := range spec.Tags {
		if tag[0] == "p" {
			foundP = true
		}
	}
	if !foundP {
		t.Error("expected p-tag for repost")
	}
}

func TestNostrCompose_Reaction(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template": "reaction",
		"event_id": "deadbeef",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var spec struct {
		Kind    int    `json:"kind"`
		Content string `json:"content"`
	}
	json.Unmarshal([]byte(out), &spec)
	if spec.Kind != 7 {
		t.Errorf("kind = %d, want 7", spec.Kind)
	}
	if spec.Content != "+" {
		t.Errorf("content = %q, want '+' (default reaction)", spec.Content)
	}
}

func TestNostrCompose_Reaction_Custom(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template": "reaction",
		"event_id": "deadbeef",
		"content":  "🤙",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var spec struct {
		Content string `json:"content"`
	}
	json.Unmarshal([]byte(out), &spec)
	if spec.Content != "🤙" {
		t.Errorf("content = %q, want '🤙'", spec.Content)
	}
}

func TestNostrCompose_MentionNote(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template":   "mention_note",
		"content":    "Hey!",
		"pubkey":     "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		"event_id":   "evt123",
		"relay_hint": "wss://relay.test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var spec struct {
		Kind    int        `json:"kind"`
		Tags    [][]string `json:"tags"`
		Content string     `json:"content"`
	}
	json.Unmarshal([]byte(out), &spec)
	if spec.Kind != 1 {
		t.Errorf("kind = %d, want 1", spec.Kind)
	}
	if spec.Content != "Hey!" {
		t.Errorf("content = %q", spec.Content)
	}
	// Should have p and e tags.
	foundP, foundE := false, false
	for _, tag := range spec.Tags {
		if tag[0] == "p" {
			foundP = true
		}
		if tag[0] == "e" {
			foundE = true
		}
	}
	if !foundP {
		t.Error("expected p-tag")
	}
	if !foundE {
		t.Error("expected e-tag")
	}
}

func TestNostrCompose_TaggedNote(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template": "tagged_note",
		"content":  "Bitcoin is freedom!",
		"hashtags": []any{"Bitcoin", "#nostr", "  freedom "},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var spec struct {
		Kind    int        `json:"kind"`
		Tags    [][]string `json:"tags"`
		Content string     `json:"content"`
	}
	json.Unmarshal([]byte(out), &spec)
	if spec.Kind != 1 {
		t.Errorf("kind = %d, want 1", spec.Kind)
	}
	// Check t-tags are lowercase and cleaned.
	tTags := 0
	for _, tag := range spec.Tags {
		if tag[0] == "t" {
			tTags++
			if tag[1] != strings.ToLower(strings.TrimSpace(strings.TrimLeft(tag[1], "#"))) {
				t.Errorf("t-tag not normalized: %q", tag[1])
			}
		}
	}
	if tTags != 3 {
		t.Errorf("expected 3 t-tags, got %d", tTags)
	}
}

func TestNostrCompose_Labeled(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template":        "labeled",
		"event_id":        "evt456",
		"labels":          []any{"spam", "nsfw"},
		"label_namespace": "content-mod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var spec struct {
		Kind int        `json:"kind"`
		Tags [][]string `json:"tags"`
	}
	json.Unmarshal([]byte(out), &spec)
	if spec.Kind != 1985 {
		t.Errorf("kind = %d, want 1985", spec.Kind)
	}
	// Should have e, L, and l tags.
	foundE, foundL, foundSmall := false, false, 0
	for _, tag := range spec.Tags {
		switch tag[0] {
		case "e":
			foundE = true
		case "L":
			foundL = true
			if tag[1] != "content-mod" {
				t.Errorf("L namespace = %q, want content-mod", tag[1])
			}
		case "l":
			foundSmall++
			if tag[2] != "content-mod" {
				t.Errorf("l namespace = %q, want content-mod", tag[2])
			}
		}
	}
	if !foundE {
		t.Error("expected e-tag")
	}
	if !foundL {
		t.Error("expected L-tag")
	}
	if foundSmall != 2 {
		t.Errorf("expected 2 l-tags, got %d", foundSmall)
	}
}

func TestNostrCompose_Labeled_DefaultNamespace(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template": "labeled",
		"event_id": "evt789",
		"labels":   []any{"quality"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var spec struct {
		Tags [][]string `json:"tags"`
	}
	json.Unmarshal([]byte(out), &spec)
	for _, tag := range spec.Tags {
		if tag[0] == "L" && tag[1] != "ugc" {
			t.Errorf("default namespace should be 'ugc', got %q", tag[1])
		}
	}
}

func TestNostrCompose_Labeled_MissingLabels(t *testing.T) {
	tool := NostrComposeTool()
	_, err := tool(context.Background(), map[string]any{
		"template": "labeled",
		"event_id": "evt789",
	})
	if err == nil {
		t.Fatal("expected error for missing labels")
	}
}

func TestNostrCompose_Labeled_MissingEventID(t *testing.T) {
	tool := NostrComposeTool()
	_, err := tool(context.Background(), map[string]any{
		"template": "labeled",
		"labels":   []any{"test"},
	})
	if err == nil {
		t.Fatal("expected error for missing event_id")
	}
}

func TestNostrCompose_UnknownTemplate(t *testing.T) {
	tool := NostrComposeTool()
	_, err := tool(context.Background(), map[string]any{
		"template": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown template")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error = %q, expected mention of template name", err.Error())
	}
}

func TestNostrCompose_MissingTemplate(t *testing.T) {
	tool := NostrComposeTool()
	_, err := tool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}

func TestNostrCompose_ReplyNoRelayHint(t *testing.T) {
	tool := NostrComposeTool()
	out, err := tool(context.Background(), map[string]any{
		"template": "reply",
		"content":  "test",
		"event_id": "abc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var spec struct {
		Tags [][]string `json:"tags"`
	}
	json.Unmarshal([]byte(out), &spec)
	// Should still have the reply marker with empty relay hint.
	if spec.Tags[0][2] != "" {
		t.Errorf("relay hint = %q, expected empty", spec.Tags[0][2])
	}
	if spec.Tags[0][3] != "reply" {
		t.Errorf("marker = %q, want 'reply'", spec.Tags[0][3])
	}
}

func TestNostrCompose_QuoteAlias(t *testing.T) {
	tool := NostrComposeTool()
	_, err := tool(context.Background(), map[string]any{
		"template": "quote",
		"event_id": "abc",
		"content":  "test",
	})
	if err != nil {
		t.Fatal("'quote' alias should work")
	}
}
