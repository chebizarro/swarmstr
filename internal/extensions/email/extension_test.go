package email

import (
	"context"
	"testing"

	"swarmstr/internal/plugins/sdk"
)

func TestReplyTargetFromContextOrLast_ContextWins(t *testing.T) {
	ctx := sdk.WithChannelReplyTarget(context.Background(), "alice@example.com")
	got := replyTargetFromContextOrLast(ctx, "bob@example.com")
	if got != "alice@example.com" {
		t.Fatalf("reply target = %q, want %q", got, "alice@example.com")
	}
}

func TestReplyTargetFromContextOrLast_FallbackUsed(t *testing.T) {
	got := replyTargetFromContextOrLast(context.Background(), "bob@example.com")
	if got != "bob@example.com" {
		t.Fatalf("reply target = %q, want %q", got, "bob@example.com")
	}
}
