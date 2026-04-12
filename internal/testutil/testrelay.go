// Package testutil provides shared test helpers for the swarmstr project.
//
// The centrepiece is NewTestRelay, which spins up a fully-functional in-process
// nostr relay backed by an in-memory eventstore. Tests get a real ws:// URL they
// can connect to with nostr.RelayConnect — no mocking, no external services.
package testutil

import (
	"encoding/hex"
	"net/http/httptest"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"fiatjaf.com/nostr/khatru"
	"fiatjaf.com/nostr/keyer"
)

// NewTestRelay starts a fully-functional in-process nostr relay on a random port.
// The relay is backed by a slicestore (in-memory eventstore) and handles the full
// nostr WebSocket protocol: EVENT, REQ, CLOSE, OK, EOSE, NIP-09 deletion,
// replaceable events, etc.
//
// The returned URL is a ws:// address suitable for nostr.RelayConnect.
// The server is automatically shut down when the test completes.
func NewTestRelay(t *testing.T) string {
	t.Helper()

	relay := khatru.NewRelay()
	store := &slicestore.SliceStore{}
	store.Init()
	relay.UseEventstore(store, 400)

	server := httptest.NewServer(relay)
	t.Cleanup(server.Close)

	return "ws" + server.URL[4:] // http://… → ws://…
}

// TestKeyPair holds a secret key and its corresponding public key for testing.
type TestKeyPair struct {
	SecretKey nostr.SecretKey
	PublicKey nostr.PubKey
	Keyer    keyer.KeySigner
}

// SecretKeyHex returns the hex-encoded secret key (useful for APIs that want a string).
func (kp TestKeyPair) SecretKeyHex() string {
	return hex.EncodeToString(kp.SecretKey[:])
}

// PubKeyHex returns the hex-encoded public key.
func (kp TestKeyPair) PubKeyHex() string {
	return kp.PublicKey.Hex()
}

// NewTestKeyPair generates a fresh random key pair for testing.
// The returned TestKeyPair includes a ready-to-use Keyer for signing events.
func NewTestKeyPair(t *testing.T) TestKeyPair {
	t.Helper()
	sk := nostr.Generate()
	pk := nostr.GetPublicKey(sk)
	kr := keyer.NewPlainKeySigner(sk)
	return TestKeyPair{
		SecretKey: sk,
		PublicKey: pk,
		Keyer:    kr,
	}
}

// SignEvent signs the given event with the test key pair.
func (kp TestKeyPair) SignEvent(t *testing.T, evt *nostr.Event) {
	t.Helper()
	evt.PubKey = kp.PublicKey
	if err := evt.Sign(kp.SecretKey); err != nil {
		t.Fatalf("sign event: %v", err)
	}
}

// MustRelayConnect connects to the given relay URL and returns the Relay.
// It fails the test if the connection cannot be established.
func MustRelayConnect(t *testing.T, url string) *nostr.Relay {
	t.Helper()
	rl, err := nostr.RelayConnect(t.Context(), url, nostr.RelayOptions{})
	if err != nil {
		t.Fatalf("relay connect %q: %v", url, err)
	}
	t.Cleanup(func() { rl.Close() })
	return rl
}
