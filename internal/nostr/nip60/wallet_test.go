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
