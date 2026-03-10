package nip61_test

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/nostr/nip61"
)

// ─── stub signer ─────────────────────────────────────────────────────────────

type stubSigner struct{}

func (s *stubSigner) Sign(_ context.Context, _ *nostr.Event) error {
	// no-op stub: leave ID/Sig at zero values
	return nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// hexPubkey is a valid 64-char hex pubkey for test use.
const hexPubkey = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

// ─── tests ───────────────────────────────────────────────────────────────────

func TestPublishAndFetchNutzapInfo(t *testing.T) {
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

	signer := &stubSigner{}
	client := nip61.NewClient(signer, publishFn, queryFn)

	mints := []nip61.MintInfo{
		{URL: "https://mint.example.com", Units: []string{"sat"}},
	}

	ev, err := client.PublishNutzapInfo(ctx, mints, "p2pkpubkey123", "sat")
	if err != nil {
		t.Fatalf("PublishNutzapInfo error: %v", err)
	}
	if int(ev.Kind) != nip61.KindNutzapInfo {
		t.Errorf("expected kind %d, got %d", nip61.KindNutzapInfo, ev.Kind)
	}

	info, _, err := client.FetchNutzapInfo(ctx, hexPubkey)
	if err != nil {
		t.Fatalf("FetchNutzapInfo error: %v", err)
	}
	if info.P2PKPubkey != "p2pkpubkey123" {
		t.Errorf("expected p2pk 'p2pkpubkey123', got %q", info.P2PKPubkey)
	}
}

func TestSendNutzap(t *testing.T) {
	ctx := context.Background()

	var published []nostr.Event
	publishFn := func(_ context.Context, ev nostr.Event) error {
		published = append(published, ev)
		return nil
	}
	queryFn := func(_ context.Context, _ nostr.Filter) ([]*nostr.Event, error) { return nil, nil }

	signer := &stubSigner{}
	client := nip61.NewClient(signer, publishFn, queryFn)

	proofs := []nip61.Proof{
		{Amount: 21, ID: "ks1", Secret: "sec1", C: "sig1"},
	}

	ev, err := client.SendNutzap(ctx, hexPubkey, "https://mint.example.com", proofs, "hello!", "")
	if err != nil {
		t.Fatalf("SendNutzap error: %v", err)
	}
	if int(ev.Kind) != nip61.KindNutzap {
		t.Errorf("expected kind %d, got %d", nip61.KindNutzap, ev.Kind)
	}

	// Check tags
	tagMap := make(map[string]string)
	for _, tag := range ev.Tags {
		if len(tag) >= 2 {
			tagMap[tag[0]] = tag[1]
		}
	}
	if tagMap["p"] != hexPubkey {
		t.Errorf("expected p tag %q, got %q", hexPubkey, tagMap["p"])
	}
	if tagMap["u"] != "https://mint.example.com" {
		t.Errorf("expected u tag 'https://mint.example.com', got %q", tagMap["u"])
	}
	if tagMap["amount"] != "21" {
		t.Errorf("expected amount tag '21', got %q", tagMap["amount"])
	}
}

func TestParseNutzap(t *testing.T) {
	proofsJSON := `[{"amount":100,"id":"ks1","secret":"sec1","C":"sig1"}]`

	ev := &nostr.Event{
		Kind:    nostr.Kind(nip61.KindNutzap),
		Content: `{"comment":"test nutzap"}`,
		Tags: nostr.Tags{
			{"p", hexPubkey},
			{"u", "https://mint.example.com"},
			{"proof", proofsJSON},
			{"amount", "100"},
		},
	}

	nz, err := nip61.ParseNutzap(ev)
	if err != nil {
		t.Fatalf("ParseNutzap error: %v", err)
	}
	if nz.Mint != "https://mint.example.com" {
		t.Errorf("unexpected mint: %q", nz.Mint)
	}
	if len(nz.Proofs) != 1 || nz.Proofs[0].Amount != 100 {
		t.Errorf("unexpected proofs: %+v", nz.Proofs)
	}
	if nz.Amount != 100 {
		t.Errorf("expected amount 100, got %d", nz.Amount)
	}
	if nz.Comment != "test nutzap" {
		t.Errorf("expected comment 'test nutzap', got %q", nz.Comment)
	}
}

func TestParseNutzap_WrongKind(t *testing.T) {
	ev := &nostr.Event{Kind: nostr.Kind(1)}
	_, err := nip61.ParseNutzap(ev)
	if err == nil {
		t.Error("expected error for wrong kind, got nil")
	}
}

func TestSendNutzap_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	signer := &stubSigner{}
	publishFn := func(_ context.Context, _ nostr.Event) error { return nil }
	queryFn := func(_ context.Context, _ nostr.Filter) ([]*nostr.Event, error) { return nil, nil }
	client := nip61.NewClient(signer, publishFn, queryFn)

	proofs := []nip61.Proof{{Amount: 1, ID: "k", Secret: "s", C: "c"}}

	// missing recipient
	if _, err := client.SendNutzap(ctx, "", "https://mint.example.com", proofs, "", ""); err == nil {
		t.Error("expected error for missing recipient")
	}
	// missing mint
	if _, err := client.SendNutzap(ctx, hexPubkey, "", proofs, "", ""); err == nil {
		t.Error("expected error for missing mint")
	}
	// missing proofs
	if _, err := client.SendNutzap(ctx, hexPubkey, "https://mint.example.com", nil, "", ""); err == nil {
		t.Error("expected error for missing proofs")
	}
}
