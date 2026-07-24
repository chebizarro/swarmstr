package nip62

import (
	"context"
	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"testing"
)

func TestBuildValidateScopedAndGlobal(t *testing.T) {
	s := keyer.NewPlainKeySigner(nostr.Generate())
	e, err := Build(context.Background(), s, []string{"wss://relay.example"}, "legal", 123)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Validate(e)
	if err != nil || len(r) != 1 || e.Kind != 62 || e.Tags[0][0] != "relay" {
		t.Fatalf("wire: %#v %v", e, err)
	}
	g, err := Build(context.Background(), s, []string{AllRelays}, "", 124)
	if err != nil || !IsGlobal(g) {
		t.Fatal("global")
	}
	out, err := PublishRelays(g, []string{"wss://a", "wss://b"})
	if err != nil || len(out) != 2 {
		t.Fatal(out, err)
	}
}
func TestRejectsMissingRelay(t *testing.T) {
	sk := nostr.Generate()
	e := nostr.Event{Kind: Kind, CreatedAt: nostr.Now()}
	_ = e.Sign(sk)
	if _, err := Validate(e); err == nil {
		t.Fatal("accepted missing relay")
	}
}
