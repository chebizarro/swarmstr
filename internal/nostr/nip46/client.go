package nip46

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip44"
)

type ClientOptions struct {
	ClientKey    nostr.SecretKey
	RemoteSigner nostr.PubKey
	Relays       []string
	Transport    Transport
	OnAuth       func(string)
	OnClosed     func(error)
}

// Client is a NIP-46 client and a nostr.Keyer. The client key only authenticates
// the NIP-46 channel; all user identity operations stay in the remote signer.
type Client struct {
	mu              sync.RWMutex
	clientKey       nostr.SecretKey
	clientPub       nostr.PubKey
	remoteSigner    nostr.PubKey
	userPub         nostr.PubKey
	relays          []string
	transport       Transport
	conversationKey [32]byte
	onAuth          func(string)
	onClosed        func(error)
	ctx             context.Context
	cancel          context.CancelFunc
	subCancel       context.CancelFunc
	pending         map[string]chan Response
	seen            map[nostr.ID]struct{}
	seenOrder       []nostr.ID
	now             func() time.Time
}

var _ nostr.Keyer = (*Client)(nil)

func NewClient(parent context.Context, opts ClientOptions) (*Client, error) {
	if parent == nil {
		parent = context.Background()
	}
	if opts.RemoteSigner == nostr.ZeroPK {
		return nil, fmt.Errorf("NIP-46 remote-signer pubkey is required")
	}
	relays, err := normalizeRelayURLs(opts.Relays)
	if err != nil || len(relays) == 0 {
		if err == nil {
			err = fmt.Errorf("NIP-46 requires at least one relay")
		}
		return nil, err
	}
	if opts.Transport == nil {
		opts.Transport = NewPoolTransport(nil, opts.ClientKey)
	}
	conversation, err := nip44.GenerateConversationKey(opts.RemoteSigner, opts.ClientKey)
	if err != nil {
		return nil, fmt.Errorf("derive NIP-46 conversation key: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	c := &Client{
		clientKey: opts.ClientKey, clientPub: opts.ClientKey.Public(), remoteSigner: opts.RemoteSigner,
		relays: relays, transport: opts.Transport, conversationKey: conversation,
		onAuth: opts.OnAuth, onClosed: opts.OnClosed, ctx: ctx, cancel: cancel,
		pending: map[string]chan Response{}, seen: map[nostr.ID]struct{}{}, now: time.Now,
	}
	if err := c.replaceSubscription(relays); err != nil {
		cancel()
		return nil, err
	}
	return c, nil
}

func ConnectBunker(ctx context.Context, clientKey nostr.SecretKey, rawURL string, transport Transport, permissions PermissionSet, metadata ClientMetadata, onAuth func(string)) (*Client, error) {
	token, err := ParseBunkerURL(rawURL)
	if err != nil {
		return nil, err
	}
	client, err := NewClient(ctx, ClientOptions{ClientKey: clientKey, RemoteSigner: token.RemoteSigner, Relays: token.Relays, Transport: transport, OnAuth: onAuth})
	if err != nil {
		return nil, err
	}
	metadataJSON := ""
	if metadata != (ClientMetadata{}) {
		metadataJSON, err = encodeJSON(metadata)
		if err != nil {
			client.Close()
			return nil, err
		}
	}
	result, err := client.RPC(ctx, MethodConnect, []string{token.RemoteSigner.Hex(), token.Secret, permissions.String(), metadataJSON})
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("NIP-46 connect: %w", err)
	}
	if result != "ack" && result != token.Secret {
		client.Close()
		return nil, fmt.Errorf("NIP-46 connect returned invalid acknowledgement")
	}
	if _, err := client.GetPublicKey(ctx); err != nil {
		client.Close()
		return nil, err
	}
	if _, err := client.SwitchRelays(ctx); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

// AcceptNostrConnect waits on the client-created token for a signer response.
// Completion is driven by an authenticated EVENT; the caller controls lifetime
// solely through ctx.
func AcceptNostrConnect(ctx context.Context, clientKey nostr.SecretKey, rawURL string, transport Transport, onAuth func(string)) (*Client, error) {
	token, err := ParseNostrConnectURL(rawURL)
	if err != nil {
		return nil, err
	}
	if token.ClientPubKey != clientKey.Public() {
		return nil, fmt.Errorf("nostrconnect client pubkey does not match client key")
	}
	if transport == nil {
		transport = NewPoolTransport(nil, clientKey)
	}
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sub := transport.Subscribe(subCtx, token.Relays, nostr.Filter{Kinds: []nostr.Kind{Kind}, Tags: nostr.TagMap{"p": []string{token.ClientPubKey.Hex()}}, Since: nostr.Now()})
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err, ok := <-sub.Closed:
			if ok && err != nil {
				continue
			}
		case event, ok := <-sub.Events:
			if !ok {
				return nil, fmt.Errorf("NIP-46 nostrconnect subscription ended before connection")
			}
			if err := validateProtocolEvent(event, nostr.ZeroPK, token.ClientPubKey, time.Now()); err != nil {
				continue
			}
			conversation, err := nip44.GenerateConversationKey(event.PubKey, clientKey)
			if err != nil {
				continue
			}
			plain, err := nip44.Decrypt(event.Content, conversation)
			if err != nil {
				continue
			}
			var response Response
			if json.Unmarshal([]byte(plain), &response) != nil || response.Error != "" || response.Result != token.Secret {
				continue
			}
			client, err := NewClient(ctx, ClientOptions{ClientKey: clientKey, RemoteSigner: event.PubKey, Relays: token.Relays, Transport: transport, OnAuth: onAuth})
			if err != nil {
				return nil, err
			}
			if _, err := client.GetPublicKey(ctx); err != nil {
				client.Close()
				return nil, err
			}
			if _, err := client.SwitchRelays(ctx); err != nil {
				client.Close()
				return nil, err
			}
			return client, nil
		}
	}
}

func (c *Client) replaceSubscription(relays []string) error {
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return c.ctx.Err()
	}
	subCtx, subCancel := context.WithCancel(c.ctx)
	filter := nostr.Filter{Kinds: []nostr.Kind{Kind}, Authors: []nostr.PubKey{c.remoteSigner}, Tags: nostr.TagMap{"p": []string{c.clientPub.Hex()}}, Since: nostr.Now()}
	sub := c.transport.Subscribe(subCtx, relays, filter)
	oldCancel := c.subCancel
	c.subCancel = subCancel
	c.relays = append([]string(nil), relays...)
	c.mu.Unlock()
	go c.consume(subCtx, sub)
	if oldCancel != nil {
		oldCancel()
	}
	return nil
}

func (c *Client) consume(ctx context.Context, sub Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-sub.Closed:
			if ok && err != nil && c.onClosed != nil {
				c.onClosed(err)
			}
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			c.handleResponse(event)
		}
	}
}

func (c *Client) handleResponse(event nostr.Event) {
	if err := validateProtocolEvent(event, c.remoteSigner, c.clientPub, c.now()); err != nil {
		return
	}
	c.mu.Lock()
	if _, ok := c.seen[event.ID]; ok {
		c.mu.Unlock()
		return
	}
	c.seen[event.ID] = struct{}{}
	c.seenOrder = append(c.seenOrder, event.ID)
	if len(c.seenOrder) > 2048 {
		delete(c.seen, c.seenOrder[0])
		c.seenOrder = c.seenOrder[1:]
	}
	c.mu.Unlock()
	plain, err := nip44.Decrypt(event.Content, c.conversationKey)
	if err != nil {
		return
	}
	var response Response
	if json.Unmarshal([]byte(plain), &response) != nil || response.ID == "" {
		return
	}
	c.mu.RLock()
	ch := c.pending[response.ID]
	c.mu.RUnlock()
	if ch == nil {
		return
	}
	if response.Result == "auth_url" && response.Error != "" {
		if c.onAuth != nil {
			c.onAuth(response.Error)
		}
		return // keep the same correlation listener for the post-auth response
	}
	select {
	case ch <- response:
	default:
	}
}

func validateProtocolEvent(event nostr.Event, author, recipient nostr.PubKey, now time.Time) error {
	if event.Kind != Kind {
		return fmt.Errorf("unexpected NIP-46 kind %d", event.Kind)
	}
	if author != nostr.ZeroPK && event.PubKey != author {
		return fmt.Errorf("unexpected NIP-46 author")
	}
	if !hasPTag(event.Tags, recipient.Hex()) {
		return fmt.Errorf("missing NIP-46 recipient p tag")
	}
	if !event.CheckID() || !event.VerifySignature() {
		return fmt.Errorf("invalid NIP-46 event signature")
	}
	delta := now.Unix() - int64(event.CreatedAt)
	if delta < -600 || delta > 600 {
		return fmt.Errorf("NIP-46 event timestamp outside 10 minute window")
	}
	return nil
}

func hasPTag(tags nostr.Tags, pubkey string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == "p" && tag[1] == pubkey {
			return true
		}
	}
	return false
}

func (c *Client) RPC(ctx context.Context, method string, params []string) (string, error) {
	if !knownMethod(method) {
		return "", fmt.Errorf("unsupported NIP-46 method %q", method)
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("generate NIP-46 request id: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	payload, err := encodeJSON(Request{ID: id, Method: method, Params: params})
	if err != nil {
		return "", err
	}
	content, err := nip44.Encrypt(payload, c.conversationKey)
	if err != nil {
		return "", fmt.Errorf("encrypt NIP-46 request: %w", err)
	}
	event := nostr.Event{Kind: Kind, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", c.remoteSigner.Hex()}}, Content: content}
	if err := event.Sign(c.clientKey); err != nil {
		return "", fmt.Errorf("sign NIP-46 request: %w", err)
	}
	responses := make(chan Response, 1)
	c.mu.Lock()
	if c.ctx.Err() != nil {
		c.mu.Unlock()
		return "", c.ctx.Err()
	}
	c.pending[id] = responses
	relays := append([]string(nil), c.relays...)
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.transport.Publish(ctx, relays, event); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-c.ctx.Done():
		return "", c.ctx.Err()
	case response := <-responses:
		if response.Error != "" {
			return "", fmt.Errorf("NIP-46 %s: %s", method, response.Error)
		}
		return response.Result, nil
	}
}

func (c *Client) Ping(ctx context.Context) error {
	result, err := c.RPC(ctx, MethodPing, nil)
	if err == nil && result != "pong" {
		return fmt.Errorf("NIP-46 ping returned %q", result)
	}
	return err
}

func (c *Client) GetPublicKey(ctx context.Context) (nostr.PubKey, error) {
	c.mu.RLock()
	cached := c.userPub
	c.mu.RUnlock()
	if cached != nostr.ZeroPK {
		return cached, nil
	}
	result, err := c.RPC(ctx, MethodGetPublicKey, nil)
	if err != nil {
		return nostr.ZeroPK, err
	}
	pk, err := nostr.PubKeyFromHex(result)
	if err != nil {
		return nostr.ZeroPK, fmt.Errorf("invalid NIP-46 user pubkey: %w", err)
	}
	c.mu.Lock()
	c.userPub = pk
	c.mu.Unlock()
	return pk, nil
}

func (c *Client) SignEvent(ctx context.Context, event *nostr.Event) error {
	if event == nil {
		return fmt.Errorf("NIP-46 sign_event requires event")
	}
	template := *event
	unsigned := struct {
		Kind      nostr.Kind      `json:"kind"`
		Content   string          `json:"content"`
		Tags      nostr.Tags      `json:"tags"`
		CreatedAt nostr.Timestamp `json:"created_at"`
	}{event.Kind, event.Content, event.Tags, event.CreatedAt}
	payload, err := encodeJSON(unsigned)
	if err != nil {
		return err
	}
	result, err := c.RPC(ctx, MethodSignEvent, []string{payload})
	if err != nil {
		return err
	}
	var signed nostr.Event
	if err := json.Unmarshal([]byte(result), &signed); err != nil {
		return fmt.Errorf("decode NIP-46 signed event: %w", err)
	}
	pk, err := c.GetPublicKey(ctx)
	if err != nil {
		return err
	}
	if signed.PubKey != pk || !signed.CheckID() || !signed.VerifySignature() {
		return fmt.Errorf("NIP-46 signer returned invalid event")
	}
	if signed.Kind != template.Kind || signed.Content != template.Content || signed.CreatedAt != template.CreatedAt || !reflect.DeepEqual(signed.Tags, template.Tags) {
		return fmt.Errorf("NIP-46 signer changed unsigned event fields")
	}
	*event = signed
	return nil
}

func (c *Client) Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return c.RPC(ctx, MethodNIP44Encrypt, []string{recipient.Hex(), plaintext})
}
func (c *Client) Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return c.RPC(ctx, MethodNIP44Decrypt, []string{sender.Hex(), ciphertext})
}
func (c *Client) EncryptNIP04(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return c.RPC(ctx, MethodNIP04Encrypt, []string{recipient.Hex(), plaintext})
}
func (c *Client) DecryptNIP04(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return c.RPC(ctx, MethodNIP04Decrypt, []string{sender.Hex(), ciphertext})
}
func (c *Client) Nip04Encrypt(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return c.EncryptNIP04(ctx, plaintext, recipient)
}
func (c *Client) Nip04Decrypt(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return c.DecryptNIP04(ctx, ciphertext, sender)
}

func (c *Client) SwitchRelays(ctx context.Context) ([]string, error) {
	result, err := c.RPC(ctx, MethodSwitchRelays, nil)
	if err != nil {
		return nil, err
	}
	if result == "" || result == "null" {
		return nil, nil
	}
	var relays []string
	if err := json.Unmarshal([]byte(result), &relays); err != nil {
		return nil, fmt.Errorf("decode NIP-46 relay switch: %w", err)
	}
	relays, err = normalizeRelayURLs(relays)
	if err != nil || len(relays) == 0 {
		if err == nil {
			err = fmt.Errorf("empty NIP-46 relay switch")
		}
		return nil, err
	}
	if err := c.replaceSubscription(relays); err != nil {
		return nil, err
	}
	return relays, nil
}

func (c *Client) Logout(ctx context.Context) error {
	result, err := c.RPC(ctx, MethodLogout, nil)
	if err != nil {
		c.Close()
		return err
	}
	if result != "ack" {
		c.Close()
		return fmt.Errorf("NIP-46 logout returned %q", result)
	}
	c.Close()
	return nil
}

func (c *Client) Close() {
	c.mu.Lock()
	if c.subCancel != nil {
		c.subCancel()
		c.subCancel = nil
	}
	c.clientKey = nostr.SecretKey{}
	c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *Client) Relays() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.relays...)
}
func (c *Client) RemoteSignerPubKey() nostr.PubKey { return c.remoteSigner }
