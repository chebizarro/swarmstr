package nip60_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/nostr/nip60"
)

var fixedNow = time.Unix(1_800_000_000, 0)

type testEncryptor struct {
	pubkey       string
	decrypts     int
	decryptError error
}

func (s *testEncryptor) Encrypt(_ context.Context, _ string, plaintext string) (string, error) {
	return "enc:" + plaintext, nil
}
func (s *testEncryptor) Decrypt(_ context.Context, _ string, ciphertext string) (string, error) {
	s.decrypts++
	if s.decryptError != nil {
		return "", s.decryptError
	}
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}
func (s *testEncryptor) PublicKeyHex() string { return s.pubkey }

type testSigner struct{ sk nostr.SecretKey }

func (s testSigner) Sign(_ context.Context, ev *nostr.Event) error { return ev.Sign(s.sk) }

type eventStore struct {
	events       []nostr.Event
	publishErr   error
	publishKinds []nostr.Kind
}

func (s *eventStore) publish(_ context.Context, ev nostr.Event) error {
	s.publishKinds = append(s.publishKinds, ev.Kind)
	if s.publishErr != nil {
		return s.publishErr
	}
	s.events = append(s.events, ev)
	return nil
}

func (s *eventStore) query(_ context.Context, filter nostr.Filter) ([]*nostr.Event, error) {
	var out []*nostr.Event
	for i := range s.events {
		ev := &s.events[i]
		if len(filter.Kinds) > 0 && !containsKind(filter.Kinds, ev.Kind) {
			continue
		}
		if len(filter.Authors) > 0 && !containsAuthor(filter.Authors, ev.PubKey) {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func containsKind(kinds []nostr.Kind, kind nostr.Kind) bool {
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}
func containsAuthor(authors []nostr.PubKey, author nostr.PubKey) bool {
	for _, candidate := range authors {
		if candidate == author {
			return true
		}
	}
	return false
}

func newHarness(t *testing.T) (*nip60.WalletClient, *testEncryptor, *eventStore, nostr.SecretKey) {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex(strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	enc := &testEncryptor{pubkey: nostr.GetPublicKey(sk).Hex()}
	store := &eventStore{}
	client := nip60.NewWalletClient(enc, testSigner{sk}, store.publish, store.query, nip60.WithClock(func() time.Time { return fixedNow }))
	return client, enc, store, sk
}

func TestPublishAndFetchWalletValidatesAndDecrypts(t *testing.T) {
	client, _, _, _ := newHarness(t)
	ev, err := client.PublishWalletWithPrivkey(context.Background(), "wallet-secret", []nip60.MintEntry{{URL: "https://MINT.example/", Units: []string{"SAT", "sat"}}}, "sat")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != nip60.KindWallet || !ev.CheckID() || !ev.VerifySignature() {
		t.Fatalf("invalid published wallet event: %+v", ev)
	}
	wallet, _, err := client.FetchWallet(context.Background(), ev.PubKey.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Privkey != "wallet-secret" || len(wallet.Mints) != 1 || wallet.Mints[0].URL != "https://mint.example" {
		t.Fatalf("unexpected wallet: %+v", wallet)
	}
}

func TestFetchWalletRejectsInvalidEventBeforeDecrypt(t *testing.T) {
	client, enc, store, sk := newHarness(t)
	ev := nostr.Event{Kind: nip60.KindWallet, CreatedAt: nostr.Timestamp(fixedNow.Unix()), Content: `enc:[["privkey","x"],["mint","https://mint.example"]]`}
	if err := ev.Sign(sk); err != nil {
		t.Fatal(err)
	}
	ev.Content += "tampered"
	store.events = append(store.events, ev)
	_, _, err := client.FetchWallet(context.Background(), nostr.GetPublicKey(sk).Hex())
	if err == nil {
		t.Fatal("expected invalid event rejection")
	}
	if enc.decrypts != 0 {
		t.Fatalf("decrypt called %d times before validation", enc.decrypts)
	}
}

func TestFetchUnspentTokensDerivesLiveStateFromDelAndDeletion(t *testing.T) {
	client, _, _, _ := newHarness(t)
	ctx := context.Background()
	old, err := client.PublishUnspentToken(ctx, "https://mint.example", []nip60.Proof{{Amount: 4, ID: "ks", Secret: "old", C: "02aa"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.PublishRolloverTransition(ctx, nip60.RolloverRequest{
		Mint: "https://mint.example", Unit: "sat",
		Proofs:            []nip60.Proof{{Amount: 3, ID: "ks", Secret: "new", C: "02bb"}},
		DestroyedEventIDs: []string{old.ID.Hex()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deletion == nil || result.CreatedToken == nil {
		t.Fatalf("incomplete transition: %+v", result)
	}
	tokens, events, err := client.FetchUnspentTokens(ctx, old.PubKey.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || len(events) != 1 || events[0].ID != result.CreatedToken.ID || tokens[0].Del[0] != old.ID.Hex() {
		t.Fatalf("unexpected live state tokens=%+v events=%+v", tokens, events)
	}
}

func TestDeletedSuccessorDoesNotResurrectPredecessor(t *testing.T) {
	client, _, _, _ := newHarness(t)
	ctx := context.Background()
	old, _ := client.PublishUnspentToken(ctx, "https://mint.example", []nip60.Proof{{Amount: 2, ID: "ks", Secret: "old", C: "c1"}})
	transition, err := client.PublishRolloverTransition(ctx, nip60.RolloverRequest{Mint: "https://mint.example", Unit: "sat", Proofs: []nip60.Proof{{Amount: 1, ID: "ks", Secret: "new", C: "c2"}}, DestroyedEventIDs: []string{old.ID.Hex()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PublishTokenDeletion(ctx, transition.CreatedToken.ID.Hex()); err != nil {
		t.Fatal(err)
	}
	tokens, _, err := client.FetchUnspentTokens(ctx, old.PubKey.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("destroyed predecessor resurrected: %+v", tokens)
	}
}

func TestRolloverCreatePrecedesDeletionAndReportsPartialFailure(t *testing.T) {
	client, _, store, _ := newHarness(t)
	oldID := strings.Repeat("a", 64)
	calls := 0
	// A routed publisher lets the first publish succeed and the deletion fail.
	client, enc, _, sk := newHarness(t)
	client = nip60.NewWalletClient(enc, testSigner{sk}, nil, func(context.Context, nostr.Filter) ([]*nostr.Event, error) { return nil, nil },
		nip60.WithClock(func() time.Time { return fixedNow }),
		nip60.WithRouting(nil, func(_ context.Context, _ []string, ev nostr.Event) error {
			calls++
			store.publishKinds = append(store.publishKinds, ev.Kind)
			if calls == 2 {
				return errors.New("relay rejected deletion")
			}
			return nil
		}, nil))
	result, err := client.PublishRolloverTransition(context.Background(), nip60.RolloverRequest{Mint: "https://mint.example", Unit: "sat", Proofs: []nip60.Proof{{Amount: 1, ID: "ks", Secret: "s", C: "c"}}, DestroyedEventIDs: []string{oldID}})
	if err == nil || result == nil || result.CreatedToken == nil || result.Deletion != nil {
		t.Fatalf("expected partial transition, result=%+v err=%v", result, err)
	}
	if len(store.publishKinds) != 2 || store.publishKinds[0] != nip60.KindUnspentToken || store.publishKinds[1] != nip60.KindDeletion {
		t.Fatalf("wrong transition order: %v", store.publishKinds)
	}
}

func TestTokenHistoryRequiresEventReferences(t *testing.T) {
	client, _, _, _ := newHarness(t)
	if _, err := client.PublishTokenHistory(context.Background(), "in", 1, "sat", "https://mint.example", ""); err == nil {
		t.Fatal("expected reference-free history rejection")
	}
	created := strings.Repeat("b", 64)
	redeemed := strings.Repeat("c", 64)
	ev, err := client.PublishTokenHistory(context.Background(), "in", 1, "sat", "https://mint.example", "", nip60.HistoryRef{EventID: created, Marker: "created"}, nip60.HistoryRef{EventID: redeemed, RelayHint: "wss://relay.example", Marker: "redeemed"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ev.Content, created) || len(ev.Tags) != 1 || ev.Tags[0][3] != "redeemed" {
		t.Fatalf("unexpected history event: %+v", ev)
	}
}

func TestQuotePublishAndFetch(t *testing.T) {
	client, _, _, _ := newHarness(t)
	ev, err := client.PublishQuote(context.Background(), "quote-123", "https://mint.example/", fixedNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != nip60.KindQuote {
		t.Fatalf("kind=%d", ev.Kind)
	}
	quotes, err := client.FetchActiveQuotes(context.Background(), ev.PubKey.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if len(quotes) != 1 || quotes[0].QuoteID != "quote-123" || quotes[0].Mint != "https://mint.example" {
		t.Fatalf("unexpected quotes: %+v", quotes)
	}
}

func TestWalletRoutingPrefersKind10019Relays(t *testing.T) {
	client, enc, store, sk := newHarness(t)
	info := nostr.Event{Kind: nip60.KindNutzapInfo, CreatedAt: nostr.Timestamp(fixedNow.Unix()), Tags: nostr.Tags{{"relay", "wss://wallet.example/"}}}
	if err := info.Sign(sk); err != nil {
		t.Fatal(err)
	}
	store.events = append(store.events, info)
	wallet := nostr.Event{Kind: nip60.KindWallet, CreatedAt: nostr.Timestamp(fixedNow.Unix()), Content: `enc:[["privkey","x"],["mint","https://mint.example"]]`}
	if err := wallet.Sign(sk); err != nil {
		t.Fatal(err)
	}
	store.events = append(store.events, wallet)
	var routed [][]string
	client = nip60.NewWalletClient(enc, testSigner{sk}, store.publish, store.query,
		nip60.WithClock(func() time.Time { return fixedNow }),
		nip60.WithRouting(func(ctx context.Context, relays []string, filter nostr.Filter) ([]*nostr.Event, error) {
			routed = append(routed, append([]string(nil), relays...))
			return store.query(ctx, filter)
		}, nil, func(context.Context, string, nip60.RelayPurpose) ([]string, error) {
			return []string{"wss://nip65.example"}, nil
		}))
	if _, _, err := client.FetchWallet(context.Background(), enc.pubkey); err != nil {
		t.Fatal(err)
	}
	if len(routed) < 2 || len(routed[0]) != 1 || routed[0][0] != "wss://nip65.example" || len(routed[1]) != 1 || routed[1][0] != "wss://wallet.example" {
		t.Fatalf("unexpected routes: %v", routed)
	}
}
