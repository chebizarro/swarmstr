package ws

import (
	"context"
	"errors"
	"testing"
)

// TestStartDisabledReturnsExplicitError asserts that Start with no listen
// address returns the ErrDisabled sentinel rather than a misleading (nil, nil)
// result that looks initialized while nothing is actually listening.
func TestStartDisabledReturnsExplicitError(t *testing.T) {
	rt, err := Start(context.Background(), RuntimeOptions{Addr: "   "})
	if rt != nil {
		t.Fatalf("expected nil runtime for empty addr, got %#v", rt)
	}
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got: %v", err)
	}
}
