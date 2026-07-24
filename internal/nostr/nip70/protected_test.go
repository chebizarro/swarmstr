package nip70

import (
	nostr "fiatjaf.com/nostr"
	"testing"
)

func TestProtectedAuthAndRepost(t *testing.T) {
	sk := nostr.Generate()
	e := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Tags: nostr.Tags{}, Content: "secret"}
	if err := Protect(&e); err != nil {
		t.Fatal(err)
	}
	_ = e.Sign(sk)
	if !IsProtected(e) || ValidatePublish(e, nil) == nil {
		t.Fatal("protection not enforced")
	}
	pk := sk.Public()
	if err := ValidatePublish(e, &pk); err != nil {
		t.Fatal(err)
	}
	repost := nostr.Event{Kind: 6, Content: e.String()}
	if ValidateRepost(repost, e) == nil {
		t.Fatal("embedded repost accepted")
	}
	repost.Content = ""
	if err := ValidateRepost(repost, e); err != nil {
		t.Fatal(err)
	}
}
func TestMalformedTagRejected(t *testing.T) {
	e := nostr.Event{Tags: nostr.Tags{{"-", "bad"}}}
	if err := Protect(&e); err == nil {
		t.Fatal("accepted malformed tag")
	}
}
