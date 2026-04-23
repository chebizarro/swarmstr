package main

import (
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestShouldPruneIdleSession(t *testing.T) {
	now := time.Now()
	cutoff := now.Add(-7 * 24 * time.Hour)

	if !shouldPruneIdleSession(state.SessionDoc{}, cutoff) {
		t.Fatal("expected zero-last-inbound session to be eligible for idle pruning")
	}

	recent := state.SessionDoc{
		SessionID:     "recent",
		LastInboundAt: now.Add(-24 * time.Hour).Unix(),
		LastReplyAt:   now.Unix(),
	}
	if shouldPruneIdleSession(recent, cutoff) {
		t.Fatalf("expected recent inbound session not to be pruned: %#v", recent)
	}

	staleInbound := state.SessionDoc{
		SessionID:     "stale",
		LastInboundAt: now.Add(-10 * 24 * time.Hour).Unix(),
		LastReplyAt:   now.Unix(),
	}
	if !shouldPruneIdleSession(staleInbound, cutoff) {
		t.Fatalf("expected stale inbound session to be pruned despite recent reply: %#v", staleInbound)
	}
}
