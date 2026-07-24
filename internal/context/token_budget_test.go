package context

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTokenBudgetArithmetic(t *testing.T) {
	if got := EstimateTokensFloor(5, 4); got != 1 {
		t.Fatalf("floor: got %d", got)
	}
	if got := EstimateTokensCeil(5, 4); got != 2 {
		t.Fatalf("ceil: got %d", got)
	}
	if got := AvailableTokens(1000, 100, .8, 0); got != 720 {
		t.Fatalf("available: got %d", got)
	}
	if got := AvailableTokens(100, 200, .8, 256); got != 256 {
		t.Fatalf("minimum: got %d", got)
	}
	if got := CharacterCapacity(7, 3.75); got != 26 {
		t.Fatalf("capacity: got %d", got)
	}
}

func TestTruncateUTF8Boundary(t *testing.T) {
	got := TruncateUTF8("ab🙂cd", 5)
	if !utf8.ValidString(got) || got != "ab" {
		t.Fatalf("invalid boundary truncation %q", got)
	}
}

func TestEstimateMessageTokensIncludesToolCalls(t *testing.T) {
	plain := EstimateMessageTokens(Message{Role: "assistant", Content: strings.Repeat("x", 8)})
	withCall := EstimateMessageTokens(Message{Role: "assistant", Content: strings.Repeat("x", 8), ToolCalls: []ToolCallRef{{ID: "call", Name: "tool", ArgsJSON: `{"x":1}`}}})
	if plain != 2 || withCall <= plain {
		t.Fatalf("unexpected estimates plain=%d withCall=%d", plain, withCall)
	}
	if got := EstimateMessageTokens(Message{}); got != 1 {
		t.Fatalf("empty message minimum: %d", got)
	}
}
