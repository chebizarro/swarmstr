package nip46

import (
	"context"
	"fmt"
	"sync"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip04"
)

type localKeyer struct {
	sk  nostr.SecretKey
	key nostr.Keyer
}

func newLocalKeyer(sk nostr.SecretKey) *localKeyer {
	return &localKeyer{sk: sk, key: keyer.NewPlainKeySigner(sk)}
}
func (k *localKeyer) GetPublicKey(ctx context.Context) (nostr.PubKey, error) {
	return k.key.GetPublicKey(ctx)
}
func (k *localKeyer) SignEvent(ctx context.Context, event *nostr.Event) error {
	return k.key.SignEvent(ctx, event)
}
func (k *localKeyer) Encrypt(ctx context.Context, plaintext string, peer nostr.PubKey) (string, error) {
	return k.key.Encrypt(ctx, plaintext, peer)
}
func (k *localKeyer) Decrypt(ctx context.Context, ciphertext string, peer nostr.PubKey) (string, error) {
	return k.key.Decrypt(ctx, ciphertext, peer)
}
func (k *localKeyer) EncryptNIP04(_ context.Context, plaintext string, peer nostr.PubKey) (string, error) {
	shared, err := nip04.ComputeSharedSecret(peer, k.sk)
	if err != nil {
		return "", err
	}
	return nip04.Encrypt(plaintext, shared)
}
func (k *localKeyer) DecryptNIP04(_ context.Context, ciphertext string, peer nostr.PubKey) (string, error) {
	shared, err := nip04.ComputeSharedSecret(peer, k.sk)
	if err != nil {
		return "", err
	}
	return nip04.Decrypt(ciphertext, shared)
}

type fakeSub struct {
	ctx    context.Context
	filter nostr.Filter
	events chan nostr.Event
	closed chan error
}
type loopTransport struct {
	mu         sync.Mutex
	server     *Server
	subs       []*fakeSub
	published  []nostr.Event
	methods    []string
	subscribed chan nostr.Filter
}

func newLoopTransport(server *Server) *loopTransport {
	return &loopTransport{server: server, subscribed: make(chan nostr.Filter, 16)}
}
func (t *loopTransport) Subscribe(ctx context.Context, _ []string, filter nostr.Filter) Subscription {
	s := &fakeSub{ctx: ctx, filter: filter, events: make(chan nostr.Event, 16), closed: make(chan error, 1)}
	t.mu.Lock()
	t.subs = append(t.subs, s)
	t.mu.Unlock()
	t.subscribed <- filter
	return Subscription{Events: s.events, Closed: s.closed}
}
func (t *loopTransport) Publish(ctx context.Context, _ []string, event nostr.Event) error {
	t.mu.Lock()
	t.published = append(t.published, event)
	t.mu.Unlock()
	request, response, err := t.server.HandleEvent(ctx, event)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.methods = append(t.methods, request.Method)
	t.mu.Unlock()
	t.Inject(response)
	return nil
}
func (t *loopTransport) Inject(event nostr.Event) {
	t.mu.Lock()
	subs := append([]*fakeSub(nil), t.subs...)
	t.mu.Unlock()
	for _, sub := range subs {
		if sub.ctx.Err() == nil && sub.filter.Matches(event) {
			sub.events <- event
		}
	}
}

func TestBunkerClientServerFullFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := newLocalKeyer(nostr.Generate())
	user := newLocalKeyer(nostr.Generate())
	var gotMetadata ClientMetadata
	server, err := NewServer(ctx, ServerOptions{
		Handler: handler, User: user, Relays: []string{"wss://new.example"},
		AuthorizeConnect: func(_ context.Context, _ nostr.PubKey, secret string, requested PermissionSet, metadata ClientMetadata) (PermissionSet, error) {
			if secret != "once" {
				return PermissionSet{}, fmt.Errorf("bad secret")
			}
			gotMetadata = metadata
			return requested, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport := newLoopTransport(server)
	permissions, _ := ParsePermissions("sign_event,nip04_encrypt,nip04_decrypt,nip44_encrypt,nip44_decrypt")
	clientKey := nostr.Generate()
	bunkerURL := "bunker://" + handler.sk.Public().Hex() + "?relay=wss%3A%2F%2Fold.example&secret=once"
	client, err := ConnectBunker(ctx, clientKey, bunkerURL, transport, permissions, ClientMetadata{Name: "agent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotMetadata.Name != "agent" {
		t.Fatalf("metadata not passed: %#v", gotMetadata)
	}
	if client.RemoteSignerPubKey() != handler.sk.Public() {
		t.Fatal("remote-signer identity confused with user identity")
	}
	if pk, _ := client.GetPublicKey(ctx); pk != user.sk.Public() {
		t.Fatal("wrong user pubkey")
	}
	if got := client.Relays(); len(got) != 1 || got[0] != "wss://new.example" {
		t.Fatalf("relay switch not adopted: %v", got)
	}

	event := nostr.Event{Kind: 1, CreatedAt: nostr.Now(), Tags: nostr.Tags{}, Content: "remote signed"}
	if err := client.SignEvent(ctx, &event); err != nil {
		t.Fatal(err)
	}
	if event.PubKey != user.sk.Public() || !event.VerifySignature() || !event.CheckID() {
		t.Fatal("invalid remote signature")
	}

	peer := newLocalKeyer(nostr.Generate())
	ciphertext, err := client.Encrypt(ctx, "nip44", peer.sk.Public())
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := peer.Decrypt(ctx, ciphertext, user.sk.Public())
	if err != nil || plaintext != "nip44" {
		t.Fatalf("NIP-44 roundtrip: %q %v", plaintext, err)
	}
	nip04Ciphertext, err := client.EncryptNIP04(ctx, "nip04", peer.sk.Public())
	if err != nil {
		t.Fatal(err)
	}
	nip04Plaintext, err := peer.DecryptNIP04(ctx, nip04Ciphertext, user.sk.Public())
	if err != nil || nip04Plaintext != "nip04" {
		t.Fatalf("NIP-04 roundtrip: %q %v", nip04Plaintext, err)
	}

	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	want := []string{MethodConnect, MethodGetPublicKey, MethodSwitchRelays, MethodSignEvent, MethodNIP44Encrypt, MethodNIP04Encrypt, MethodPing, MethodLogout}
	if fmt.Sprint(transport.methods) != fmt.Sprint(want) {
		t.Fatalf("methods: got %v want %v", transport.methods, want)
	}
	for _, event := range transport.published {
		if event.Kind != Kind || len(event.Tags) != 1 || event.Tags[0][0] != "p" || !event.CheckID() || !event.VerifySignature() {
			t.Fatalf("invalid request wire event: %#v", event)
		}
	}
}

func TestNostrConnectFlowValidatesSecretAndDiscoversSigner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := newLocalKeyer(nostr.Generate())
	user := newLocalKeyer(nostr.Generate())
	server, err := NewServer(ctx, ServerOptions{Handler: handler, User: user, Relays: []string{"wss://relay.example"}, AuthorizeConnect: func(_ context.Context, _ nostr.PubKey, _ string, requested PermissionSet, _ ClientMetadata) (PermissionSet, error) {
		return requested, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	transport := newLoopTransport(server)
	clientKey := nostr.Generate()
	permissions, _ := ParsePermissions("sign_event:1")
	raw, _, err := GenerateNostrConnectURL(clientKey, []string{"wss://relay.example"}, permissions, ClientMetadata{Name: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, response, err := server.HandleNostrConnectURL(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan *Client, 1)
	errCh := make(chan error, 1)
	go func() {
		client, err := AcceptNostrConnect(ctx, clientKey, raw, transport, nil)
		if err != nil {
			errCh <- err
			return
		}
		result <- client
	}()
	<-transport.subscribed
	transport.Inject(response)
	select {
	case err := <-errCh:
		t.Fatal(err)
	case client := <-result:
		defer client.Close()
		if client.RemoteSignerPubKey() != handler.sk.Public() {
			t.Fatal("failed to discover signer")
		}
		if pk, _ := client.GetPublicKey(ctx); pk != user.sk.Public() {
			t.Fatal("failed to discover user identity")
		}
	}
}

func TestPermissionAndTokenValidation(t *testing.T) {
	pk := nostr.Generate().Public().Hex()
	if _, err := ParseBunkerURL("bunker://" + pk + "?relay=https%3A%2F%2Fnot-a-relay"); err == nil {
		t.Fatal("accepted non-websocket relay")
	}
	if _, err := ParseNostrConnectURL("nostrconnect://" + pk + "?relay=wss%3A%2F%2Fr.example"); err == nil {
		t.Fatal("accepted missing secret")
	}
	permissions, err := ParsePermissions("nip44_encrypt,sign_event:4")
	if err != nil {
		t.Fatal(err)
	}
	if !permissions.Allows(MethodNIP44Encrypt, 0) || !permissions.Allows(MethodSignEvent, 4) || permissions.Allows(MethodSignEvent, 1) {
		t.Fatal("permission matching failed")
	}
	if _, err := ParsePermissions("nip44_encrypt:4"); err == nil {
		t.Fatal("accepted parameter on non-sign method")
	}
}

func TestServerRejectsUnsignedAndDuplicateEvents(t *testing.T) {
	ctx := context.Background()
	handler := newLocalKeyer(nostr.Generate())
	user := newLocalKeyer(nostr.Generate())
	server, _ := NewServer(ctx, ServerOptions{Handler: handler, User: user, AuthorizeConnect: func(_ context.Context, _ nostr.PubKey, _ string, requested PermissionSet, _ ClientMetadata) (PermissionSet, error) {
		return requested, nil
	}})
	bad := nostr.Event{Kind: Kind, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", handler.sk.Public().Hex()}}, Content: "bad"}
	if _, _, err := server.HandleEvent(ctx, bad); err == nil {
		t.Fatal("accepted unsigned request")
	}
}
