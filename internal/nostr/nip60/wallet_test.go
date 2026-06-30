package nip60_test

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/nostr/nip60"
)

// ─── stub encryptor ───────────────────────────────────────────────────────────

type stubEncryptor struct{ pubkey string }

func (s *stubEncryptor) Encrypt(_ context.Context, _ string, plaintext string) (string, error) {
	return "enc:" + plaintext, nil
}

func (s *stubEncryptor) Decrypt(_ context.Context, _ string, ciphertext string) (string, error) {
	if len(ciphertext) > 4 && ciphertext[:4] == "enc:" {
		return ciphertext[4:], nil
	}
	return ciphertext, nil
}

func (s *stubEncryptor) PublicKeyHex() string { return s.pubkey }

// ─── stub signer ─────────────────────────────────────────────────────────────

type stubSigner struct{ pubkey string }

func (s *stubSigner) Sign(_ context.Context, ev *nostr.Event) error {
	// no-op stub: leave ID/Sig at zero values
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// hexPubkey is a valid 64-char hex pubkey for test use.
const hexPubkey = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// ─── tests ─────────────────────────────────────────────────────────────────────

func TestPublishAndFetchWallet(t *testing.T) {
	ctx := context.Background()

	var published []nostr.Event
	publishFn := func(_ context.Context, ev nostr.Event) error {
		published = append(published, ev)
		return nil
	}
	queryFn := func(_ context.Context, _ nostr.Filter) ([]*nostr.Event, error) {
		if len(published) == 0 {
			return nil, nil
		}
		ev := published[len(published)-1]
		return []*nostr.Event{&ev}, nil
	}

	enc := &stubEncryptor{pubkey: hexPubkey}
	signer := &stubSigner{pubkey: hexPubkey}
	client := nip60.NewWalletClient(enc, signer, publishFn, queryFn)

	mints := []nip60.MintEntry{
		{URL: "https://mint.example.com", Units: []string{"sat"}},
	}

	ev, err := client.PublishWallet(ctx, "test-wallet", mints, "sat")
	if err != nil {
		t.Fatalf("PublishWallet error: %v", err)
	}
	if int(ev.Kind) != nip60.KindWallet {
		t.Errorf("expected kind %d, got %d", nip60.KindWallet, ev.Kind)
	}

	content, _, err := client.FetchWallet(ctx, hexPubkey)
	if err != nil {
		t.Fatalf("FetchWallet error: %v", err)
	}
	if content.Name != "test-wallet" {
		t.Errorf("expected name 'test-wallet', got %q", content.Name)
	}
	if len(content.Mints) != 1 || content.Mints[0].URL != "https://mint.example.com" {
		t.Errorf("unexpected mints: %+v", content.Mints)
	}
}

func TestPublishAndFetchUnspentTokens(t *testing.T) {
	ctx := context.Background()

	var published []nostr.Event
	publishFn := func(_ context.Context, ev nostr.Event) error {
		published = append(published, ev)
		return nil
	}
	queryFn := func(_ context.Context, _ nostr.Filter) ([]*nostr.Event, error) {
		result := make([]*nostr.Event, len(published))
		for i := range published {
			ev := published[i]
			result[i] = &ev
		}
		return result, nil
	}

	enc := &stubEncryptor{pubkey: hexPubkey}
	signer := &stubSigner{pubkey: hexPubkey}
	client := nip60.NewWalletClient(enc, signer, publishFn, queryFn)

	proofs := []nip60.Proof{
		{Amount: 100, ID: "keyset1", Secret: "secret1", C: "sig1"},
		{Amount: 50, ID: "keyset1", Secret: "secret2", C: "sig2"},
	}

	ev, err := client.PublishUnspentToken(ctx, "https://mint.example.com", proofs)
	if err != nil {
		t.Fatalf("PublishUnspentToken error: %v", err)
	}
	if int(ev.Kind) != nip60.KindUnspentToken {
		t.Errorf("expected kind %d, got %d", nip60.KindUnspentToken, ev.Kind)
	}

	tokens, _, err := client.FetchUnspentTokens(ctx, hexPubkey)
	if err != nil {
		t.Fatalf("FetchUnspentTokens error: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token bundle, got %d", len(tokens))
	}
	if tokens[0].Mint != "https://mint.example.com" {
		t.Errorf("unexpected mint: %q", tokens[0].Mint)
	}
	if len(tokens[0].Proofs) != 2 {
		t.Errorf("expected 2 proofs, got %d", len(tokens[0].Proofs))
	}
}

func TestTokenHistoryKind(t *testing.T) {
	ctx := context.Background()

	var published []nostr.Event
	publishFn := func(_ context.Context, ev nostr.Event) error {
		published = append(published, ev)
		return nil
	}
	queryFn := func(_ context.Context, _ nostr.Filter) ([]*nostr.Event, error) { return nil, nil }

	enc := &stubEncryptor{pubkey: hexPubkey}
	signer := &stubSigner{pubkey: hexPubkey}
	client := nip60.NewWalletClient(enc, signer, publishFn, queryFn)

	ev, err := client.PublishTokenHistory(ctx, "in", 100, "sat", "https://mint.example.com", "received nutzap")
	if err != nil {
		t.Fatalf("PublishTokenHistory error: %v", err)
	}
	if int(ev.Kind) != nip60.KindTokenHistory {
		t.Errorf("expected kind %d, got %d", nip60.KindTokenHistory, ev.Kind)
	}
}

func TestPublishWalletUsesNIP60EncryptedTagArray(t *testing.T) {
	ctx := context.Background()
	var published nostr.Event
	client := nip60.NewWalletClient(&stubEncryptor{pubkey: hexPubkey}, &stubSigner{pubkey: hexPubkey}, func(_ context.Context, ev nostr.Event) error {
		published = ev
		return nil
	}, func(_ context.Context, _ nostr.Filter) ([]*nostr.Event, error) { return nil, nil })

	ev, err := client.PublishWalletWithPrivkey(ctx, "wallet-privkey", []nip60.MintEntry{{URL: "https://mint.example.com", Units: []string{"sat", "usd"}}}, "sat")
	if err != nil {
		t.Fatalf("PublishWalletWithPrivkey error: %v", err)
	}
	if int(ev.Kind) != 17375 {
		t.Fatalf("wallet kind = %d, want 17375", ev.Kind)
	}
	if len(ev.Tags) != 0 {
		t.Fatalf("wallet event should not have public d tag: %v", ev.Tags)
	}
	want := `enc:[["privkey","wallet-privkey"],["mint","https://mint.example.com","sat","usd"]]`
	if published.Content != want {
		t.Fatalf("wallet content = %q, want %q", published.Content, want)
	}
}

func TestPublishTokenRolloverAndDeletion(t *testing.T) {
	ctx := context.Background()
	var published []nostr.Event
	client := nip60.NewWalletClient(&stubEncryptor{pubkey: hexPubkey}, &stubSigner{pubkey: hexPubkey}, func(_ context.Context, ev nostr.Event) error {
		published = append(published, ev)
		return nil
	}, func(_ context.Context, _ nostr.Filter) ([]*nostr.Event, error) { return nil, nil })

	_, err := client.PublishUnspentTokenWithRollover(ctx, "https://mint.example.com", "sat", []nip60.Proof{{Amount: 1, ID: "ks", Secret: "s", C: "c"}}, []string{"old-token"})
	if err != nil {
		t.Fatalf("PublishUnspentTokenWithRollover error: %v", err)
	}
	if published[0].Content != `enc:{"mint":"https://mint.example.com","unit":"sat","proofs":[{"amount":1,"id":"ks","secret":"s","C":"c"}],"del":["old-token"]}` {
		t.Fatalf("unexpected token content: %q", published[0].Content)
	}

	del, err := client.PublishTokenDeletion(ctx, "old-token")
	if err != nil {
		t.Fatalf("PublishTokenDeletion error: %v", err)
	}
	if int(del.Kind) != 5 {
		t.Fatalf("deletion kind = %d, want 5", del.Kind)
	}
	if len(del.Tags) < 2 || del.Tags[0][0] != "k" || del.Tags[0][1] != "7375" || del.Tags[1][0] != "e" || del.Tags[1][1] != "old-token" {
		t.Fatalf("unexpected deletion tags: %v", del.Tags)
	}
}

func TestPublishTokenHistoryEncryptedTagsAndPublicRedeemed(t *testing.T) {
	ctx := context.Background()
	var published nostr.Event
	client := nip60.NewWalletClient(&stubEncryptor{pubkey: hexPubkey}, &stubSigner{pubkey: hexPubkey}, func(_ context.Context, ev nostr.Event) error {
		published = ev
		return nil
	}, func(_ context.Context, _ nostr.Filter) ([]*nostr.Event, error) { return nil, nil })

	_, err := client.PublishTokenHistoryTags(ctx, [][]string{{"direction", "in"}, {"amount", "1"}, {"unit", "sat"}, {"e", "new-token", "", "created"}}, nostr.Tags{{"e", "nutzap-event", "wss://relay", "redeemed"}})
	if err != nil {
		t.Fatalf("PublishTokenHistoryTags error: %v", err)
	}
	if published.Content != `enc:[["direction","in"],["amount","1"],["unit","sat"],["e","new-token","","created"]]` {
		t.Fatalf("unexpected history content: %q", published.Content)
	}
	if len(published.Tags) != 1 || published.Tags[0][3] != "redeemed" {
		t.Fatalf("expected public redeemed e tag, got %v", published.Tags)
	}
}
