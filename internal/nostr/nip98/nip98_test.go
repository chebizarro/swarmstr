package nip98

import (
	"context"
	"strings"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
)

type testKeyer struct{ sk [32]byte }

func (k testKeyer) GetPublicKey(context.Context) (nostr.PubKey, error) {
	return nostr.GetPublicKey(k.sk), nil
}
func (k testKeyer) SignEvent(_ context.Context, evt *nostr.Event) error { return evt.Sign(k.sk) }
func keyer(t *testing.T) testKeyer {
	t.Helper()
	sk, err := nostr.SecretKeyFromHex("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	return testKeyer{sk: [32]byte(sk)}
}

func TestVerifyHappyPath(t *testing.T) {
	k := keyer(t)
	body := []byte(`{"ok":true}`)
	_, header, err := Build(context.Background(), k, BuildOptions{Method: "POST", URL: "https://relay.example/manage", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	pub, err := Verify(header, "POST", "https://relay.example/manage", body)
	if err != nil {
		t.Fatal(err)
	}
	want := nostr.GetPublicKey(k.sk).Hex()
	if pub != want {
		t.Fatalf("pubkey=%s want %s", pub, want)
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	k := keyer(t)
	_, header, err := Build(context.Background(), k, BuildOptions{Method: "POST", URL: "https://relay.example/manage", Body: []byte("a")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(header, "POST", "https://relay.example/manage", []byte("b"))
	if err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("expected payload error, got %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	k := keyer(t)
	_, header, err := Build(context.Background(), k, BuildOptions{Method: "GET", URL: "https://relay.example/manage", CreatedAt: time.Now().Add(-2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(header, "GET", "https://relay.example/manage", nil)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error, got %v", err)
	}
}

func TestVerifyRejectsWrongURL(t *testing.T) {
	k := keyer(t)
	_, header, err := Build(context.Background(), k, BuildOptions{Method: "GET", URL: "https://relay.example/manage"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(header, "GET", "https://relay.example/other", nil)
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("expected url error, got %v", err)
	}
}
