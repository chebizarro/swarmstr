package secure

import (
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
)

func TestPublishGuard_NilSafe(t *testing.T) {
	var g *PublishGuard
	if err := g.CheckEvent(&nostr.Event{Content: "nsec1" + strings.Repeat("q", 58)}); err != nil {
		t.Fatalf("nil guard should return nil, got %v", err)
	}
	if err := g.CheckContent("nsec1" + strings.Repeat("q", 58)); err != nil {
		t.Fatalf("nil guard CheckContent should return nil, got %v", err)
	}
	if g.Policy() != PublishPolicyOff {
		t.Fatalf("nil guard policy should be off, got %s", g.Policy())
	}
}

func TestPublishGuard_PolicyOff(t *testing.T) {
	g := NewPublishGuard(PublishPolicyOff)
	evt := &nostr.Event{Content: "sk-" + strings.Repeat("a", 48)}
	if err := g.CheckEvent(evt); err != nil {
		t.Fatalf("policy=off should return nil, got %v", err)
	}
}

func TestPublishGuard_PolicyBlock_DetectsSecret(t *testing.T) {
	g := NewPublishGuard(PublishPolicyBlock)
	evt := &nostr.Event{
		Kind:    1,
		Content: "Here's my OpenAI key: sk-" + strings.Repeat("a", 48),
	}
	err := g.CheckEvent(evt)
	if err == nil {
		t.Fatal("policy=block should return error for secret content")
	}
	if !strings.Contains(err.Error(), "publish blocked") {
		t.Fatalf("error should mention 'publish blocked', got: %s", err)
	}
	if !strings.Contains(err.Error(), "openai-api-key") {
		t.Fatalf("error should mention pattern name, got: %s", err)
	}
}

func TestPublishGuard_PolicyBlock_AllowsClean(t *testing.T) {
	g := NewPublishGuard(PublishPolicyBlock)
	evt := &nostr.Event{
		Kind:    1,
		Content: "Just a normal note about my day",
	}
	if err := g.CheckEvent(evt); err != nil {
		t.Fatalf("clean content should pass, got %v", err)
	}
}

func TestPublishGuard_PolicyWarn_AllowsSecret(t *testing.T) {
	g := NewPublishGuard(PublishPolicyWarn)
	evt := &nostr.Event{
		Kind:    1,
		Content: "-----BEGIN RSA PRIVATE KEY-----",
	}
	if err := g.CheckEvent(evt); err != nil {
		t.Fatalf("policy=warn should return nil even for secrets, got %v", err)
	}
}

func TestPublishGuard_ScansTagValues(t *testing.T) {
	g := NewPublishGuard(PublishPolicyBlock)
	evt := &nostr.Event{
		Kind:    30078,
		Content: "nothing secret here",
		Tags: nostr.Tags{
			{"d", "my-config"},
			{"secret", "sk-" + strings.Repeat("z", 48)}, // API key in a tag value
		},
	}
	err := g.CheckEvent(evt)
	if err == nil {
		t.Fatal("should detect secret in tag values")
	}
}

func TestPublishGuard_CheckContent(t *testing.T) {
	g := NewPublishGuard(PublishPolicyBlock)

	// Clean content passes
	if err := g.CheckContent("Hello world"); err != nil {
		t.Fatalf("clean content should pass, got %v", err)
	}

	// Secret content blocked
	err := g.CheckContent("my password=SuperSecret123!")
	if err == nil {
		t.Fatal("should block content with password")
	}
}

func TestPublishGuard_NilEvent(t *testing.T) {
	g := NewPublishGuard(PublishPolicyBlock)
	if err := g.CheckEvent(nil); err != nil {
		t.Fatalf("nil event should return nil, got %v", err)
	}
}

func TestParsePublishPolicy(t *testing.T) {
	tests := []struct {
		input string
		want  PublishPolicy
	}{
		{"block", PublishPolicyBlock},
		{"Block", PublishPolicyBlock},
		{"BLOCK", PublishPolicyBlock},
		{"warn", PublishPolicyWarn},
		{"off", PublishPolicyOff},
		{"disabled", PublishPolicyOff},
		{"none", PublishPolicyOff},
		{"", PublishPolicyBlock},       // default
		{"unknown", PublishPolicyBlock}, // default
	}
	for _, tc := range tests {
		got := ParsePublishPolicy(tc.input)
		if got != tc.want {
			t.Errorf("ParsePublishPolicy(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestPublishGuard_MultipleFindings(t *testing.T) {
	g := NewPublishGuard(PublishPolicyBlock)
	evt := &nostr.Event{
		Kind: 1,
		Content: "keys:\n" +
			"nsec1" + strings.Repeat("q", 58) + "\n" +
			"AKIAIOSFODNN7EXAMPLE\n" +
			"-----BEGIN PRIVATE KEY-----",
	}
	err := g.CheckEvent(evt)
	if err == nil {
		t.Fatal("should detect multiple secrets")
	}
	// Should mention multiple patterns
	errMsg := err.Error()
	if !strings.Contains(errMsg, "nostr-nsec") {
		t.Error("should mention nostr-nsec")
	}
	if !strings.Contains(errMsg, "aws-access-key-id") {
		t.Error("should mention aws-access-key-id")
	}
	if !strings.Contains(errMsg, "pem-private-key") {
		t.Error("should mention pem-private-key")
	}
}
