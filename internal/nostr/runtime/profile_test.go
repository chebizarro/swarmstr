package runtime

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func boolPtr(b bool) *bool { return &b }

// signKind0 builds and signs a kind:0 profile event with the given secret key.
// The signer sets PubKey/ID/Sig from sk, so the returned event is
// signature-valid and authored by sk's public key.
func signKind0(t *testing.T, sk nostr.SecretKey, content string, createdAt nostr.Timestamp) nostr.Event {
	t.Helper()
	kr := keyer.NewPlainKeySigner(sk)
	evt := nostr.Event{Kind: 0, Content: content, CreatedAt: createdAt}
	if err := kr.SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("sign kind:0: %v", err)
	}
	return evt
}

func signKind(t *testing.T, sk nostr.SecretKey, kind nostr.Kind, content string, createdAt nostr.Timestamp) nostr.Event {
	t.Helper()
	kr := keyer.NewPlainKeySigner(sk)
	evt := nostr.Event{Kind: kind, Content: content, CreatedAt: createdAt}
	if err := kr.SignEvent(context.Background(), &evt); err != nil {
		t.Fatalf("sign kind:%d: %v", kind, err)
	}
	return evt
}

func TestParseProfileContent_BotFlag(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantBot bool
	}{
		{"bot true", `{"name":"agent","bot":true}`, true},
		{"bot false", `{"name":"human","bot":false}`, false},
		{"bot absent", `{"name":"human"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pc, err := ParseProfileContent(tc.content)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if pc.IsBot() != tc.wantBot {
				t.Errorf("IsBot() = %v, want %v", pc.IsBot(), tc.wantBot)
			}
		})
	}
}

func TestParseProfileContent_InvalidJSON(t *testing.T) {
	if _, err := ParseProfileContent("not json"); err == nil {
		t.Fatal("expected error for invalid JSON content")
	}
}

func TestProfileContent_IsBot_ExplicitOnly(t *testing.T) {
	if (ProfileContent{Bot: nil}).IsBot() {
		t.Error("nil bot flag must be not-a-bot")
	}
	if (ProfileContent{Bot: boolPtr(false)}).IsBot() {
		t.Error("explicit false must be not-a-bot")
	}
	if !(ProfileContent{Bot: boolPtr(true)}).IsBot() {
		t.Error("explicit true must be a bot")
	}
}

func TestSelectIdentityBoundProfile_ValidBot(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()
	ev := signKind0(t, sk, `{"name":"agent","bot":true}`, 1000)

	best, ok := selectIdentityBoundProfile([]nostr.Event{ev}, pk)
	if !ok {
		t.Fatal("expected a valid matching profile")
	}
	pc, err := ParseProfileContent(best.Content)
	if err != nil {
		t.Fatalf("parse winner: %v", err)
	}
	if !pc.IsBot() {
		t.Error("winning profile should report bot:true")
	}
}

func TestSelectIdentityBoundProfile_NewestWins(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()
	older := signKind0(t, sk, `{"bot":false}`, 1000)
	newer := signKind0(t, sk, `{"bot":true}`, 2000)

	// Order should not matter; newest created_at wins.
	best, ok := selectIdentityBoundProfile([]nostr.Event{newer, older}, pk)
	if !ok {
		t.Fatal("expected a valid matching profile")
	}
	if best.CreatedAt != 2000 {
		t.Fatalf("expected newest (2000), got %d", best.CreatedAt)
	}
	pc, _ := ParseProfileContent(best.Content)
	if !pc.IsBot() {
		t.Error("newest profile declares bot:true")
	}
}

// Parity matrix row 12: a relay returns a kind:0 with bot:true whose author is
// a DIFFERENT key than the one requested. It must be rejected — the human's key
// must not be poisoned by another key's spoofed bot flag.
func TestSelectIdentityBoundProfile_RejectsAuthorMismatch(t *testing.T) {
	human := nostr.Generate()
	attacker := nostr.Generate()

	// Attacker signs a bot:true profile (valid signature, but for attacker's key).
	spoof := signKind0(t, attacker, `{"bot":true}`, 5000)

	// Query for the human's pubkey; the attacker's event must not match.
	if _, ok := selectIdentityBoundProfile([]nostr.Event{spoof}, human.Public()); ok {
		t.Fatal("author-mismatched event must be rejected (row 12)")
	}
}

func TestSelectIdentityBoundProfile_RejectsTamperedSignature(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()
	ev := signKind0(t, sk, `{"bot":false}`, 1000)

	// Tamper the content after signing: the event now claims the correct
	// author but its id/signature no longer match.
	ev.Content = `{"bot":true}`

	if _, ok := selectIdentityBoundProfile([]nostr.Event{ev}, pk); ok {
		t.Fatal("tampered event must be rejected")
	}
}

func TestSelectIdentityBoundProfile_RejectsWrongKind(t *testing.T) {
	sk := nostr.Generate()
	pk := sk.Public()
	// A signature-valid kind:1 event from the same author is not a profile.
	ev := signKind(t, sk, 1, `{"bot":true}`, 1000)

	if _, ok := selectIdentityBoundProfile([]nostr.Event{ev}, pk); ok {
		t.Fatal("non-kind:0 event must be rejected")
	}
}

func TestSelectIdentityBoundProfile_Empty(t *testing.T) {
	if _, ok := selectIdentityBoundProfile(nil, nostr.Generate().Public()); ok {
		t.Fatal("no candidates must yield no match (fail-open unknown)")
	}
}

func TestFetchIdentityBoundProfile_SetupErrors(t *testing.T) {
	pk := nostr.Generate().Public()
	if _, _, err := FetchIdentityBoundProfile(context.Background(), nil, []string{"wss://relay"}, pk); err == nil {
		t.Error("nil pool must be a setup error")
	}
}

func TestEnsureProfileBotFlag(t *testing.T) {
	// nil map => fresh map with bot set.
	got := EnsureProfileBotFlag(nil, true)
	if v, _ := got["bot"].(bool); !v {
		t.Errorf("nil map: expected bot=true, got %v", got["bot"])
	}

	// existing fields preserved, bot overridden.
	in := map[string]any{"name": "agent", "bot": false}
	out := EnsureProfileBotFlag(in, true)
	if v, _ := out["bot"].(bool); !v {
		t.Errorf("expected bot overridden to true, got %v", out["bot"])
	}
	if out["name"] != "agent" {
		t.Errorf("expected name preserved, got %v", out["name"])
	}
	// input not mutated.
	if v, _ := in["bot"].(bool); v {
		t.Error("input map must not be mutated")
	}
}
