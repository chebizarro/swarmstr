package qqbot

import (
	"context"
	"strings"
	"testing"
)

// TestQQSendTypingOutsideC2CErrors asserts SendTyping fails explicitly for
// non-c2c targets (where the QQ input_notify API is undefined) instead of
// silently returning nil while advertising Typing support.
func TestQQSendTypingOutsideC2CErrors(t *testing.T) {
	b := &qqBot{channelID: "qq-main", targetType: "group", targetID: "g1"}
	err := b.SendTyping(context.Background(), 0)
	if err == nil {
		t.Fatal("expected SendTyping to error for non-c2c target")
	}
	if !strings.Contains(err.Error(), "c2c") {
		t.Fatalf("expected c2c-only error, got: %v", err)
	}
}

// TestQQSendTypingC2CWithoutTargetErrors asserts SendTyping errors when the c2c
// target id is unknown rather than silently succeeding.
func TestQQSendTypingC2CWithoutTargetErrors(t *testing.T) {
	b := &qqBot{channelID: "qq-main", targetType: "c2c", targetID: ""}
	if err := b.SendTyping(context.Background(), 0); err == nil {
		t.Fatal("expected SendTyping to error for c2c with no target id")
	}
}
