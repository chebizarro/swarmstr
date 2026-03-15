package toolbuiltin

import (
	"context"
	"strings"
	"testing"

	"swarmstr/internal/agent"
)

func TestRegisterNIPTools_RegistersDeleteAliasesWithDefinitions(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterNIPTools(tools, NostrToolOpts{})

	defs := tools.Definitions()
	hasDelete := false
	hasEventDelete := false
	hasArticlePublish := false
	hasReport := false
	for _, d := range defs {
		switch d.Name {
		case "nostr_delete":
			hasDelete = true
		case "nostr_event_delete":
			hasEventDelete = true
		case "nostr_article_publish":
			hasArticlePublish = true
		case "nostr_report":
			hasReport = true
		}
	}
	if !hasDelete {
		t.Fatal("nostr_delete definition missing")
	}
	if !hasEventDelete {
		t.Fatal("nostr_event_delete definition missing")
	}
	if !hasArticlePublish {
		t.Fatal("nostr_article_publish definition missing")
	}
	if !hasReport {
		t.Fatal("nostr_report definition missing")
	}
}

func TestArticleHelpers_ExtractSummaryImageAndTags(t *testing.T) {
	md := "# Title\n\n![hero](https://img.example/hero.png)\n\nThis is a long body about #Nostr and #AI agents."
	if got := firstMarkdownImage(md); got != "https://img.example/hero.png" {
		t.Fatalf("unexpected image: %q", got)
	}
	s := autoArticleSummary(md)
	if s == "" {
		t.Fatal("expected summary")
	}
	tags := inferHashtags(md)
	if len(tags) != 2 || tags[0] != "nostr" || tags[1] != "ai" {
		t.Fatalf("unexpected tags: %v", tags)
	}
}

func TestNostrEventDelete_NoRelaysValidation(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterNIPTools(tools, NostrToolOpts{})

	_, err := tools.Execute(context.Background(), agent.ToolCall{
		Name: "nostr_event_delete",
		Args: map[string]any{"ids": []any{"deadbeef"}},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "nostr_event_delete_error:") {
		t.Fatalf("expected no-relays error, got: %v", err)
	}
}

func TestNostrReport_TargetValidation(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterNIPTools(tools, NostrToolOpts{Relays: []string{"wss://example.com"}})

	_, err := tools.Execute(context.Background(), agent.ToolCall{
		Name: "nostr_report",
		Args: map[string]any{"report_type": "spam"},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "nostr_report_error:") {
		t.Fatalf("expected target validation error, got: %v", err)
	}
}

func TestNostrEventDelete_MissingIDsValidation(t *testing.T) {
	tools := agent.NewToolRegistry()
	RegisterNIPTools(tools, NostrToolOpts{Relays: []string{"wss://example.com"}})

	_, err := tools.Execute(context.Background(), agent.ToolCall{Name: "nostr_event_delete", Args: map[string]any{}})
	if err == nil || !strings.HasPrefix(err.Error(), "nostr_event_delete_error:") {
		t.Fatalf("expected ids-required error, got: %v", err)
	}
}
