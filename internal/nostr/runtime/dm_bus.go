package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip04"
)

type InboundDM struct {
	EventID    string
	FromPubKey string
	Text       string
	RelayURL   string
	CreatedAt  int64
	Reply      func(ctx context.Context, text string) error
}

// NIP04Decrypter is an optional extension of nostr.Keyer for signers that
// support NIP-04 (kind:4) AES-CBC decryption in addition to NIP-44.
// DMBus checks for this interface at runtime and uses it when available;
// without it the bus can subscribe and sign AUTH but cannot decrypt kind:4 DMs.
type NIP04Decrypter interface {
	nostr.Keyer
	DecryptNIP04(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error)
}

type DMBusOptions struct {
	PrivateKey string
	// Keyer is an optional pre-built nostr.Keyer (e.g. a NIP-46 BunkerSigner).
	// When set, it is used for NIP-42 AUTH signing. PrivateKey is still needed
	// for NIP-04 encryption/decryption (which requires a raw secret key).
	Keyer        nostr.Keyer
	Relays       []string
	SinceUnix    int64
	OnMessage    func(context.Context, InboundDM) error
	OnError      func(error)
	SeenCap      int
	WorkerCount  int
	QueueSize    int
	ReplayWindow time.Duration
}

type DMBus struct {
	pool         *nostr.Pool
	ks           nostr.Keyer
	relays       []string
	relaysMu     sync.RWMutex
	public       nostr.PubKey
	onMessage    func(context.Context, InboundDM) error
	onError      func(error)
	health       *RelayHealthTracker
	replayWindow time.Duration

	seenMu   sync.Mutex
	seenSet  map[string]struct{}
	seenList []string
	seenCap  int

	messageQueue chan InboundDM

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

const maxDMPlaintextRunes = 4096

func StartDMBus(parent context.Context, opts DMBusOptions) (*DMBus, error) {
	initialRelays := sanitizeRelayList(opts.Relays)
	if len(initialRelays) == 0 {
		return nil, fmt.Errorf("at least one relay is required")
	}
	if opts.PrivateKey == "" && opts.Keyer == nil {
		return nil, fmt.Errorf("private key is required (NIP-04 requires raw secret key)")
	}

	// NIP-04 always requires a raw secret key for shared-secret computation.
	// If only a Keyer is provided (e.g. bunker mode), callers should use NIP17Bus instead.
	var sk nostr.SecretKey
	var ks nostr.Keyer
	var public nostr.PubKey
	if opts.PrivateKey != "" {
		var err error
		sk, err = ParseSecretKey(opts.PrivateKey)
		if err != nil {
			return nil, err
		}
		public = sk.Public()
		// Wrap the raw key in a NIP-04-capable adapter.  If the caller also
		// provided a Keyer (e.g. for bunker AUTH), prefer it for signing but
		// still use the adapter for NIP-04 encrypt/decrypt via the stored sk.
		if opts.Keyer == nil {
			ks = newNIP04KeyerAdapter(sk)
		} else {
			ks = opts.Keyer
		}
	} else {
		// Keyer-only mode: NIP-04 works if the Keyer implements NIP04Decrypter.
		ks = opts.Keyer
		pk, err := ks.GetPublicKey(parent)
		if err != nil {
			return nil, fmt.Errorf("dm bus: get public key from keyer: %w", err)
		}
		public = pk
	}

	since := opts.SinceUnix
	if since <= 0 {
		since = time.Now().Add(-30 * time.Minute).Unix()
	}
	workerCount := max(opts.WorkerCount, 4)
	queueSize := max(opts.QueueSize, 256)
	replayWindow := opts.ReplayWindow
	if replayWindow <= 0 {
		replayWindow = 30 * time.Minute
	}

	ctx, cancel := context.WithCancel(parent)
	health := NewRelayHealthTracker()
	health.Seed(initialRelays)
	bus := &DMBus{
		ks: ks,
		pool: nostr.NewPool(nostr.PoolOptions{
			PenaltyBox: true,
			RelayOptions: nostr.RelayOptions{
				// NIP-42: sign AUTH challenges via Keyer (supports both raw keys and bunker).
				AuthHandler: func(ctx context.Context, r *nostr.Relay, evt *nostr.Event) error {
					return ks.SignEvent(ctx, evt)
				},
			},
		}),
		relays:       initialRelays,
		public:       public,
		onMessage:    opts.OnMessage,
		health:       health,
		onError:      opts.OnError,
		replayWindow: replayWindow,
		seenSet:      make(map[string]struct{}),
		seenCap:      max(opts.SeenCap, 10_000),
		messageQueue: make(chan InboundDM, queueSize),
		ctx:          ctx,
		cancel:       cancel,
	}

	if bus.onMessage != nil {
		for i := 0; i < workerCount; i++ {
			bus.wg.Add(1)
			go func() {
				defer bus.wg.Done()
				for msg := range bus.messageQueue {
					if err := bus.onMessage(bus.ctx, msg); err != nil {
						bus.emitErr(fmt.Errorf("on message handler: %w", err))
					}
				}
			}()
		}
	}

	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.KindEncryptedDirectMessage},
		Tags:  nostr.TagMap{"p": {bus.public.Hex()}},
		Since: nostr.Timestamp(since),
	}
	stream := bus.pool.SubscribeMany(ctx, bus.currentRelays(), filter, nostr.SubscriptionOptions{})

	bus.wg.Add(1)
	go func() {
		defer bus.wg.Done()
		defer close(bus.messageQueue)
		for relayEvent := range stream {
			bus.handleInbound(relayEvent)
		}
	}()

	return bus, nil
}

func (b *DMBus) PublicKey() string {
	return b.public.Hex()
}

func (b *DMBus) Close() {
	b.cancel()
	b.pool.Close("dm bus closed")
	b.wg.Wait()
}

func (b *DMBus) SendDM(ctx context.Context, toPubKey string, text string) error {
	pk, err := ParsePubKey(toPubKey)
	if err != nil {
		return err
	}
	text, err = sanitizeDMText(text)
	if err != nil {
		return err
	}
	_, err = publishEncryptedDMWithRetry(ctx, b.pool, b.ks, b.currentRelays(), pk, text, b.health)
	return err
}

// SendDMWithScheme sends a DM using an explicit encryption scheme request.
// DMBus only supports nip04; auto/empty resolves to nip04.
func (b *DMBus) SendDMWithScheme(ctx context.Context, toPubKey string, text string, scheme string) error {
	s := strings.ToLower(strings.TrimSpace(scheme))
	switch s {
	case "", "auto", "nip04", "nip-04":
		return b.SendDM(ctx, toPubKey, text)
	default:
		return fmt.Errorf("dm scheme %q not supported by NIP-04 transport", scheme)
	}
}

func (b *DMBus) SetRelays(relays []string) error {
	next := sanitizeRelayList(relays)
	if len(next) == 0 {
		return fmt.Errorf("at least one relay is required")
	}
	b.relaysMu.Lock()
	b.relays = next
	b.relaysMu.Unlock()
	if b.health != nil {
		b.health.Seed(next)
	}
	return nil
}

func (b *DMBus) Relays() []string {
	return b.currentRelays()
}

func SendDMOnce(ctx context.Context, privateKey string, relays []string, toPubKey string, text string) (string, error) {
	if len(relays) == 0 {
		return "", fmt.Errorf("at least one relay is required")
	}

	sk, err := ParseSecretKey(privateKey)
	if err != nil {
		return "", err
	}
	pk, err := ParsePubKey(toPubKey)
	if err != nil {
		return "", err
	}

	// Build a NIP-04-capable keyer from the raw secret key so that
	// publishEncryptedDM can use the unified keyer interface.
	onceKS := newNIP04KeyerAdapter(sk)
	pool := nostr.NewPool(nostr.PoolOptions{
		PenaltyBox: true,
		RelayOptions: nostr.RelayOptions{
			AuthHandler: func(ctx context.Context, r *nostr.Relay, evt *nostr.Event) error {
				return onceKS.SignEvent(ctx, evt)
			},
		},
	})
	defer pool.Close("send once finished")
	return publishEncryptedDMWithRetry(ctx, pool, onceKS, relays, pk, text, nil)
}

// nip04KeyerAdapter wraps a raw secret key as a nostr.Keyer that also
// implements NIP04Decrypter and NIP04Encrypter.  Used internally when a
// raw private key (e.g. from SendDMOnce or legacy DMBusOptions.PrivateKey)
// must satisfy the unified keyer interface without importing the config package.
type nip04KeyerAdapter struct {
	keyer.KeySigner
	sk nostr.SecretKey
}

func newNIP04KeyerAdapter(sk nostr.SecretKey) nip04KeyerAdapter {
	return nip04KeyerAdapter{KeySigner: keyer.NewPlainKeySigner([32]byte(sk)), sk: sk}
}

func (a nip04KeyerAdapter) DecryptNIP04(ctx context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	return decryptNIP04(a.sk, sender, ciphertext)
}

func (a nip04KeyerAdapter) EncryptNIP04(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	return encryptNIP04(a.sk, recipient, plaintext)
}

func (b *DMBus) handleInbound(re nostr.RelayEvent) {
	if re.Relay == nil {
		b.emitErr(fmt.Errorf("received relay event without relay context"))
		return
	}
	if re.Event.Kind != nostr.KindEncryptedDirectMessage {
		return
	}
	if re.Event.PubKey == b.public {
		return
	}
	if !re.Event.CheckID() || !re.Event.VerifySignature() {
		b.emitErr(fmt.Errorf("rejected invalid event from relay=%s", re.Relay.URL))
		return
	}
	if b.replayWindow > 0 {
		now := time.Now().Unix()
		if int64(re.Event.CreatedAt) < now-int64(b.replayWindow.Seconds()) {
			// Too old/replayed; drop after signature validation.
			return
		}
	}
	if !re.Event.Tags.ContainsAny("p", []string{b.public.Hex()}) {
		return
	}

	eventID := re.Event.ID.Hex()
	if b.markSeen(eventID) {
		return
	}

	dec, ok := b.ks.(NIP04Decrypter)
	if !ok {
		b.emitErr(fmt.Errorf("decrypt dm %s: keyer does not support NIP-04; use a local key signer", eventID))
		return
	}
	plaintext, err := dec.DecryptNIP04(context.Background(), re.Event.Content, re.Event.PubKey)
	if err != nil {
		b.emitErr(fmt.Errorf("decrypt dm %s: %w", eventID, err))
		return
	}
	plaintext, err = sanitizeDMText(plaintext)
	if err != nil {
		b.emitErr(fmt.Errorf("reject dm %s: %w", eventID, err))
		return
	}
	if b.onMessage == nil {
		return
	}

	if b.health != nil {
		b.health.RecordSuccess(re.Relay.URL)
	}

	msg := InboundDM{
		EventID:    eventID,
		FromPubKey: re.Event.PubKey.Hex(),
		Text:       plaintext,
		RelayURL:   re.Relay.URL,
		CreatedAt:  int64(re.Event.CreatedAt),
		Reply: func(ctx context.Context, text string) error {
			text, err = sanitizeDMText(text)
			if err != nil {
				return err
			}
			_, err := publishEncryptedDMWithRetry(ctx, b.pool, b.ks, b.currentRelays(), re.Event.PubKey, text, b.health)
			return err
		},
	}

	select {
	case b.messageQueue <- msg:
	case <-b.ctx.Done():
	case <-time.After(2 * time.Second):
		b.emitErr(fmt.Errorf("dropped dm event=%s due to full queue", eventID))
	}
}

func (b *DMBus) emitErr(err error) {
	if b.onError != nil && err != nil {
		b.onError(err)
	}
}

func (b *DMBus) markSeen(id string) bool {
	b.seenMu.Lock()
	defer b.seenMu.Unlock()

	if _, ok := b.seenSet[id]; ok {
		return true
	}

	b.seenSet[id] = struct{}{}
	b.seenList = append(b.seenList, id)
	if len(b.seenList) > b.seenCap {
		victim := b.seenList[0]
		b.seenList = b.seenList[1:]
		delete(b.seenSet, victim)
	}
	return false
}

// NIP04Encrypter is an optional interface for signers that support NIP-04
// AES-CBC encryption (distinct from the NIP-44 Cipher interface on nostr.Keyer).
type NIP04Encrypter interface {
	EncryptNIP04(ctx context.Context, plaintext string, recipient nostr.PubKey) (string, error)
}

func publishEncryptedDM(ctx context.Context, pool *nostr.Pool, ks nostr.Keyer, relays []string, to nostr.PubKey, text string) (string, error) {
	return publishEncryptedDMWithRetry(ctx, pool, ks, relays, to, text, nil)
}

func publishEncryptedDMWithRetry(ctx context.Context, pool *nostr.Pool, ks nostr.Keyer, relays []string, to nostr.PubKey, text string, health *RelayHealthTracker) (string, error) {
	var err error
	text, err = sanitizeDMText(text)
	if err != nil {
		return "", err
	}
	enc, ok := ks.(NIP04Encrypter)
	if !ok {
		return "", fmt.Errorf("keyer does not support NIP-04 encryption; use a local key signer")
	}
	ciphertext, err := enc.EncryptNIP04(ctx, text, to)
	if err != nil {
		return "", err
	}

	evt := nostr.Event{
		Kind:      nostr.KindEncryptedDirectMessage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", to.Hex()}},
		Content:   ciphertext,
	}
	if err := ks.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("sign dm event: %w", err)
	}

	maxAttempts := 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptRelays := relays
		if health != nil {
			attemptRelays = health.Candidates(relays, time.Now())
		}
		published := false
		for result := range pool.PublishMany(ctx, attemptRelays, evt) {
			if result.Error == nil {
				published = true
				if health != nil {
					health.RecordSuccess(result.RelayURL)
				}
				continue
			}
			if health != nil {
				health.RecordFailure(result.RelayURL)
			}
			lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
		}
		if published {
			return evt.ID.Hex(), nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if attempt < maxAttempts-1 {
			backoff := time.Duration(150*(1<<attempt)) * time.Millisecond
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no relay accepted publish")
	}
	return "", lastErr
}

func decryptNIP04(sk nostr.SecretKey, sender nostr.PubKey, content string) (string, error) {
	shared, err := nip04.ComputeSharedSecret(sender, [32]byte(sk))
	if err != nil {
		return "", fmt.Errorf("compute shared secret: %w", err)
	}
	plaintext, err := nip04.Decrypt(content, shared)
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

func encryptNIP04(sk nostr.SecretKey, recipient nostr.PubKey, plaintext string) (string, error) {
	shared, err := nip04.ComputeSharedSecret(recipient, [32]byte(sk))
	if err != nil {
		return "", fmt.Errorf("compute shared secret: %w", err)
	}
	ciphertext, err := nip04.Encrypt(plaintext, shared)
	if err != nil {
		return "", err
	}
	return ciphertext, nil
}

func (b *DMBus) currentRelays() []string {
	b.relaysMu.RLock()
	defer b.relaysMu.RUnlock()
	out := make([]string, len(b.relays))
	copy(out, b.relays)
	return out
}

func sanitizeDMText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("dm text is empty")
	}
	if utf8.RuneCountInString(text) > maxDMPlaintextRunes {
		return "", fmt.Errorf("dm text exceeds %d characters", maxDMPlaintextRunes)
	}
	return text, nil
}
