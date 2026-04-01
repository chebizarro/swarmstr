package agent

import "testing"

func TestBuildPromptAssembly_PreservesStaticAndDynamicBoundaries(t *testing.T) {
	assembly := buildPromptAssembly("provider static", "turn static", "dynamic memory")
	if got := assembly.Combined(); got != "provider static\n\nturn static\n\ndynamic memory" {
		t.Fatalf("combined prompt = %q", got)
	}
	parts := assembly.SystemParts()
	if len(parts) != 2 {
		t.Fatalf("expected 2 system parts, got %d", len(parts))
	}
	if parts[0].Text != "provider static\n\nturn static" || parts[0].CacheControl == nil || parts[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("unexpected static system part: %+v", parts[0])
	}
	if parts[1].Text != "dynamic memory" || parts[1].CacheControl != nil {
		t.Fatalf("unexpected dynamic system part: %+v", parts[1])
	}
}

func TestBuildLLMMessagesFromTurn_BuildsSplitSystemMessage(t *testing.T) {
	msgs := buildLLMMessagesFromTurn(Turn{UserText: "hello", StaticSystemPrompt: "turn static", Context: "dynamic memory"}, "provider static")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("expected first message to be system, got %q", msgs[0].Role)
	}
	if msgs[0].Content != "provider static\n\nturn static\n\ndynamic memory" {
		t.Fatalf("unexpected system content: %q", msgs[0].Content)
	}
	if len(msgs[0].SystemParts) != 2 {
		t.Fatalf("expected 2 system parts, got %d", len(msgs[0].SystemParts))
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hello" {
		t.Fatalf("unexpected user message: %+v", msgs[1])
	}
}
