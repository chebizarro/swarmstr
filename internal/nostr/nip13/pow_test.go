package nip13

import (
	"context"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func TestDifficultyCountsBits(t *testing.T) {
	var id nostr.ID
	id[0] = 0
	id[1] = 0x20
	if got := Difficulty(id); got != 10 {
		t.Fatalf("got %d", got)
	}
}
func TestMineCommitValidateAndSign(t *testing.T) {
	sk := nostr.Generate()
	signer := keyer.NewPlainKeySigner(sk)
	e := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"t", "pow"}}, Content: "mine"}
	if err := MineAndSign(context.Background(), signer, &e, 10); err != nil {
		t.Fatal(err)
	}
	if err := Validate(e, 10, true); err != nil {
		t.Fatal(err)
	}
	target, _, present, err := Commitment(e)
	if err != nil || !present || target != 10 {
		t.Fatalf("commitment: %d %v %v", target, present, err)
	}
	if !e.VerifySignature() {
		t.Fatal("not signed")
	}
}
func TestRejectsLuckyLowCommitment(t *testing.T) {
	e := nostr.Event{PubKey: nostr.Generate().Public(), Kind: 1, CreatedAt: nostr.Now(), Tags: nostr.Tags{}, Content: "x"}
	if err := Mine(context.Background(), &e, 8); err != nil {
		t.Fatal(err)
	}
	e.Tags[len(e.Tags)-1][2] = "4"
	e.SetID()
	if err := Validate(e, 8, true); err == nil {
		t.Fatal("accepted low commitment")
	}
}
func TestMiningCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := nostr.Event{PubKey: nostr.Generate().Public(), CreatedAt: nostr.Now()}
	if err := Mine(ctx, &e, 256); err == nil {
		t.Fatal("expected cancellation")
	}
}
