package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/policy"
)

func TestBuildControlAdminAuthHeaderProducesValidNIP98Header(t *testing.T) {
	payload := []byte(`{"method":"exec.approval.resolve","params":{"id":"approval-123","decision":"approve"}}`)
	secret := nostr.Generate()
	header, err := buildControlAdminAuthHeader(http.MethodPost, "http://localhost:7423/call", payload, secret)
	if err != nil {
		t.Fatalf("buildControlAdminAuthHeader error: %v", err)
	}
	if !strings.HasPrefix(header, "Nostr ") {
		t.Fatalf("header = %q, want Nostr prefix", header)
	}

	req := httptest.NewRequest(http.MethodPost, "http://localhost:7423/call", bytes.NewReader(payload))
	req.Header.Set("X-Nostr-Authorization", header)
	auth := policy.AuthenticateControlCall(req, payload, 30*time.Second)
	if !auth.Authenticated {
		t.Fatalf("expected authenticated request, got reason=%q", auth.Reason)
	}
	if auth.CallerPubKey != nostr.GetPublicKey(secret).Hex() {
		t.Fatalf("caller pubkey = %q, want %q", auth.CallerPubKey, nostr.GetPublicKey(secret).Hex())
	}
}

func TestIsTransientModelUnload(t *testing.T) {
	if !isTransientModelUnload(`agent.wait status="failed" error=Model unloaded.`) {
		t.Fatal("expected model unloaded to be treated as transient")
	}
	if isTransientModelUnload("tool execution failed") {
		t.Fatal("unexpected transient classification")
	}
}

func TestShouldRetryForMissingToolUse(t *testing.T) {
	obs := liveRunObservation{}
	if !shouldRetryForMissingToolUse("Relay", obs, []string{"my_identity"}) {
		t.Fatal("expected retry when no tool.start events were observed")
	}
	obs.ToolStarts = []string{"my_identity"}
	if shouldRetryForMissingToolUse("Relay", obs, []string{"my_identity"}) {
		t.Fatal("unexpected retry when tool.start was observed")
	}
}

func TestStrengthenToolPrompt(t *testing.T) {
	got := strengthenToolPrompt("Use my_identity before answering.", []string{"my_identity", "relay_ping"})
	if !strings.Contains(got, "MANDATORY") || !strings.Contains(got, "my_identity") {
		t.Fatalf("strengthened prompt missing enforcement text: %q", got)
	}
}
