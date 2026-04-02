package runtime

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip04"
)

var (
	ErrInvalidPadding   = errors.New("invalid padding")
	ErrInvalidPlaintext = errors.New("invalid plaintext")
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
	Hub          *NostrHub
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
	pool     *nostr.Pool
	hub      *NostrHub
	ownsPool bool
	// authKeyer is used for NIP-42 relay AUTH signing (PoolOptsNIP42).
	authKeyer nostr.Keyer
	// signKeyer is used to sign DM events (kind:4).
	signKeyer nostr.Keyer
	// nip04Keyer is used for NIP-04 encryption/decryption when a raw secret key
	// is available locally.
	nip04Keyer   nip04KeyerAdapter
	hasNIP04Key  bool
	relays       []string
	relaysMu     sync.RWMutex
	public       nostr.PubKey
	onMessage    func(context.Context, InboundDM) error
	onError      func(error)
	health       *RelayHealthTracker
	subHealth    *SubHealthTracker
	replayWindow time.Duration

	seenMu   sync.Mutex
	seenSet  map[string]struct{}
	seenList []string
	seenCap  int

	messageQueue chan InboundDM
	rebindCh     chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (b *DMBus) nip04EncryptKeyer() nostr.Keyer {
	if b.hasNIP04Key {
		return b.nip04Keyer
	}
	return b.signKeyer
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
	var authKeyer nostr.Keyer
	var signKeyer nostr.Keyer
	var nip04Key nip04KeyerAdapter
	var hasNIP04Key bool
	var public nostr.PubKey
	if opts.PrivateKey != "" {
		var err error
		sk, err = ParseSecretKey(opts.PrivateKey)
		if err != nil {
			return nil, err
		}
		public = sk.Public()

		nip04Key = newNIP04KeyerAdapter(sk)
		hasNIP04Key = true

		// If the caller provided a Keyer (e.g. bunker mode), use it for signing and
		// NIP-42 AUTH *only if* it matches the raw private key's identity.
		// Encryption/decryption always uses the local NIP-04 key.
		if opts.Keyer != nil {
			pk2, err := opts.Keyer.GetPublicKey(parent)
			if err != nil {
				return nil, fmt.Errorf("dm bus: get public key from keyer: %w", err)
			}
			if pk2 != public {
				return nil, fmt.Errorf("dm bus: provided keyer pubkey does not match private key pubkey")
			}
			authKeyer = opts.Keyer
			signKeyer = opts.Keyer
		} else {
			authKeyer = nip04Key
			signKeyer = nip04Key
		}
	} else {
		// Keyer-only mode requires explicit NIP-04 support for inbound decrypt and
		// outbound encrypt. This matters for bunker/remote signers, where raw key
		// access is unavailable.
		authKeyer = opts.Keyer
		signKeyer = opts.Keyer
		if _, ok := opts.Keyer.(NIP04Decrypter); !ok {
			return nil, fmt.Errorf("dm bus: provided keyer does not support NIP-04 decrypt")
		}
		if _, ok := opts.Keyer.(NIP04Encrypter); !ok {
			return nil, fmt.Errorf("dm bus: provided keyer does not support NIP-04 encrypt")
		}
		pk, err := authKeyer.GetPublicKey(parent)
		if err != nil {
			return nil, fmt.Errorf("dm bus: get public key from keyer: %w", err)
		}
		public = pk
	}

	since := opts.SinceUnix
	if since <= 0 {
		since = ResubscribeSince(DMReplayWindowDefault)
	}
	workerCount := max(opts.WorkerCount, 4)
	queueSize := max(opts.QueueSize, 256)
	replayWindow := opts.ReplayWindow
	if replayWindow <= 0 {
		replayWindow = DMReplayWindowDefault
	}

	ctx, cancel := context.WithCancel(parent)
	health := NewRelayHealthTracker()
	health.Seed(initialRelays)
	pool := NewPoolNIP42(authKeyer)
	ownsPool := true
	if opts.Hub != nil {
		pool = opts.Hub.Pool()
		ownsPool = false
	}
	bus := &DMBus{
		authKeyer:    authKeyer,
		signKeyer:    signKeyer,
		nip04Keyer:   nip04Key,
		hasNIP04Key:  hasNIP04Key,
		pool:         pool,
		hub:          opts.Hub,
		ownsPool:     ownsPool,
		relays:       initialRelays,
		public:       public,
		onMessage:    opts.OnMessage,
		health:       health,
		onError:      opts.OnError,
		replayWindow: replayWindow,
		seenSet:      make(map[string]struct{}),
		seenCap:      max(opts.SeenCap, 10_000),
		messageQueue: make(chan InboundDM, queueSize),
		rebindCh:     make(chan struct{}, 1),
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

	bus.subHealth = NewSubHealthTracker("dm")
	bus.subHealth.RecordReconnect()
	bus.wg.Add(1)
	go bus.subscriptionLoop(since)

	return bus, nil
}

func (b *DMBus) PublicKey() string {
	return b.public.Hex()
}

func (b *DMBus) Close() {
	b.cancel()
	if b.ownsPool {
		b.pool.Close("dm bus closed")
	}
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
	_, err = publishEncryptedDMWithRetry(ctx, b.pool, b.signKeyer, b.nip04EncryptKeyer(), b.currentRelays(), pk, text, b.health)
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
	b.relaysMu.Lock()
	b.relays = next
	b.relaysMu.Unlock()
	if b.health != nil {
		b.health.Seed(next)
	}
	b.requestRebind()
	return nil
}

// HealthSnapshot returns a point-in-time view of the DM subscription's health.
func (b *DMBus) HealthSnapshot() SubHealthSnapshot {
	rw := b.replayWindow
	if rw <= 0 {
		rw = DMReplayWindowDefault
	}
	if b.subHealth == nil {
		return SubHealthSnapshot{Label: "dm", BoundRelays: b.currentRelays(), ReplayWindowMS: int64(rw / time.Millisecond)}
	}
	return b.subHealth.Snapshot(b.currentRelays(), rw)
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
	pool := NewPoolNIP42(onceKS)
	defer pool.Close("send once finished")
	return publishEncryptedDMWithRetry(ctx, pool, onceKS, onceKS, relays, pk, text, nil)
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
	if b.subHealth != nil {
		b.subHealth.RecordEvent()
	}

	eventID := re.Event.ID.Hex()
	if b.markSeen(eventID) {
		return
	}

	dec, ok := b.nip04EncryptKeyer().(NIP04Decrypter)
	if !ok {
		b.emitErr(fmt.Errorf("decrypt dm %s: keyer does not support NIP-04; use a local key signer", eventID))
		return
	}
	decryptCtx, decryptCancel := context.WithTimeout(b.ctx, 10*time.Second)
	plaintext, err := dec.DecryptNIP04(decryptCtx, re.Event.Content, re.Event.PubKey)
	decryptCancel()
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
			_, err := publishEncryptedDMWithRetry(ctx, b.pool, b.signKeyer, b.nip04EncryptKeyer(), b.currentRelays(), re.Event.PubKey, text, b.health)
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

func (b *DMBus) requestRebind() {
	select {
	case b.rebindCh <- struct{}{}:
	default:
	}
}

func (b *DMBus) resubscribeSinceUnix() int64 {
	if b.replayWindow > 0 {
		return ResubscribeSince(b.replayWindow)
	}
	return ResubscribeSince(DMReplayWindowDefault)
}

func (b *DMBus) subscriptionLoop(initialSince int64) {
	defer b.wg.Done()
	defer close(b.messageQueue)
	since := initialSince
	for {
		if b.ctx.Err() != nil {
			return
		}
		restart := b.runSubscription(since)
		if b.ctx.Err() != nil {
			return
		}
		if b.subHealth != nil {
			b.subHealth.RecordReconnect()
		}
		since = b.resubscribeSinceUnix()
		if !restart {
			select {
			case <-b.ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

func (b *DMBus) runSubscription(since int64) bool {
	filter := b.dmFilter(since)
	if b.hub != nil {
		return b.runHubSubscription(filter)
	}
	relays := b.currentRelays()
	if len(relays) == 0 {
		select {
		case <-b.ctx.Done():
			return true
		case <-b.rebindCh:
			return true
		case <-time.After(500 * time.Millisecond):
			return false
		}
	}
	stream := b.pool.SubscribeMany(b.ctx, relays, filter, nostr.SubscriptionOptions{})
	for {
		select {
		case <-b.ctx.Done():
			return true
		case <-b.rebindCh:
			return true
		case relayEvent, ok := <-stream:
			if !ok {
				b.emitErr(fmt.Errorf("dm subscription closed; restarting"))
				return false
			}
			b.handleInbound(relayEvent)
		}
	}
}

type dmRelayClose struct {
	relayURL   string
	reason     string
	generation int
}

type dmRelayRetry struct {
	relayURL   string
	generation int
}

func (b *DMBus) dmFilter(since int64) nostr.Filter {
	return nostr.Filter{
		Kinds: []nostr.Kind{nostr.KindEncryptedDirectMessage},
		Tags:  nostr.TagMap{"p": {b.public.Hex()}},
		Since: nostr.Timestamp(since),
	}
}

func (b *DMBus) dmSubID(relay string, generation int) string {
	return fmt.Sprintf("dm-bus:%s:%d", strings.TrimSpace(relay), generation)
}

func (b *DMBus) runHubSubscription(filter nostr.Filter) bool {
	subCtx, cancel := context.WithCancel(b.ctx)
	defer cancel()

	relays := b.currentRelays()
	queueCap := max(len(relays)*2, 8)
	closedCh := make(chan dmRelayClose, queueCap)
	resubscribeCh := make(chan dmRelayRetry, queueCap)
	pending := map[string]int{}
	generation := map[string]int{}

	nextGeneration := func(relay string) int {
		relay = strings.TrimSpace(relay)
		generation[relay]++
		return generation[relay]
	}

	emitRelayClose := func(close dmRelayClose) {
		select {
		case closedCh <- close:
		default:
			go func() {
				select {
				case <-subCtx.Done():
				case closedCh <- close:
				}
			}()
		}
	}

	subscribeRelay := func(relay string, filter nostr.Filter, gen int) bool {
		relayKey := strings.TrimSpace(relay)
		if relayKey == "" {
			return true
		}
		if _, err := b.hub.Subscribe(subCtx, SubOpts{
			ID:      b.dmSubID(relayKey, gen),
			Filter:  filter,
			Relays:  []string{relayKey},
			OnEvent: b.handleInbound,
			OnClosed: func(closedRelay *nostr.Relay, reason string, handledAuth bool) {
				if handledAuth {
					return
				}
				reportedRelay := relayKey
				if closedRelay != nil && strings.TrimSpace(closedRelay.URL) != "" {
					reportedRelay = strings.TrimSpace(closedRelay.URL)
				}
				if b.health != nil {
					b.health.RecordFailure(reportedRelay)
				}
				if b.subHealth != nil {
					b.subHealth.RecordClosed(reason)
				}
				b.emitErr(fmt.Errorf("dm subscription closed relay=%s reason=%s", reportedRelay, reason))
				emitRelayClose(dmRelayClose{relayURL: relayKey, reason: reason, generation: gen})
			},
		}); err != nil {
			if b.health != nil {
				b.health.RecordFailure(relayKey)
			}
			b.emitErr(fmt.Errorf("dm subscription start relay=%s: %w", relayKey, err))
			return false
		}
		return true
	}

	scheduleResubscribe := func(relay string, gen int) {
		relay = strings.TrimSpace(relay)
		if relay == "" {
			return
		}
		if pendingGen, ok := pending[relay]; ok && pendingGen >= gen {
			return
		}
		pending[relay] = gen
		go func(relay string, gen int) {
			for {
				if subCtx.Err() != nil {
					return
				}
				if b.health == nil || b.health.Allowed(relay, time.Now()) {
					break
				}
				// Sleep for the exact remaining cooldown instead of polling.
				wait := b.health.NextAllowedIn(relay, time.Now())
				if wait <= 0 {
					break
				}
				select {
				case <-subCtx.Done():
					return
				case <-time.After(wait):
				}
			}
			select {
			case <-subCtx.Done():
			case resubscribeCh <- dmRelayRetry{relayURL: relay, generation: gen}:
			}
		}(relay, gen)
	}

	started := 0
	for _, relay := range relays {
		gen := nextGeneration(relay)
		if subscribeRelay(relay, filter, gen) {
			started++
			continue
		}
		scheduleResubscribe(relay, gen)
	}
	if started == 0 {
		return false
	}

	for {
		select {
		case <-b.ctx.Done():
			for relay, gen := range generation {
				b.hub.Unsubscribe(b.dmSubID(relay, gen))
			}
			return true
		case <-b.rebindCh:
			for relay, gen := range generation {
				b.hub.Unsubscribe(b.dmSubID(relay, gen))
			}
			return true
		case closed := <-closedCh:
			relay := strings.TrimSpace(closed.relayURL)
			if relay == "" {
				return false
			}
			if generation[relay] != closed.generation {
				continue
			}
			b.hub.Unsubscribe(b.dmSubID(relay, closed.generation))
			scheduleResubscribe(relay, closed.generation)
		case retry := <-resubscribeCh:
			relay := strings.TrimSpace(retry.relayURL)
			if relay == "" {
				continue
			}
			if pending[relay] != retry.generation {
				continue
			}
			delete(pending, relay)
			if !containsRelay(b.currentRelays(), relay) {
				continue
			}
			gen := nextGeneration(relay)
			resubscribeFilter := b.dmFilter(b.resubscribeSinceUnix())
			if !subscribeRelay(relay, resubscribeFilter, gen) {
				scheduleResubscribe(relay, gen)
			}
		}
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
	return publishEncryptedDMWithRetry(ctx, pool, ks, ks, relays, to, text, nil)
}

func publishEncryptedDMWithRetry(ctx context.Context, pool *nostr.Pool, signer nostr.Keyer, crypto nostr.Keyer, relays []string, to nostr.PubKey, text string, health *RelayHealthTracker) (string, error) {
	var err error
	text, err = sanitizeDMText(text)
	if err != nil {
		return "", err
	}
	enc, ok := crypto.(NIP04Encrypter)
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
	if err := signer.SignEvent(ctx, &evt); err != nil {
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
	plaintext, err := decryptNIP04WithSharedSecret(shared, content)
	if err != nil {
		return "", err
	}
	return plaintext, nil
}

func decryptNIP04WithSharedSecret(shared []byte, content string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(content), "?iv=", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("invalid ciphertext format")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	iv, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode iv: %w", err)
	}
	if len(iv) != aes.BlockSize {
		return "", fmt.Errorf("invalid iv length: %d", len(iv))
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("invalid ciphertext length")
	}
	block, err := aes.NewCipher(shared)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	plaintext, err = pkcs7Unpad(plaintext, aes.BlockSize)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(plaintext) {
		return "", ErrInvalidPlaintext
	}
	return string(plaintext), nil
}

func pkcs7Unpad(plaintext []byte, blockSize int) ([]byte, error) {
	if len(plaintext) == 0 || len(plaintext)%blockSize != 0 {
		return nil, ErrInvalidPadding
	}
	padding := int(plaintext[len(plaintext)-1])
	if padding == 0 || padding > blockSize || padding > len(plaintext) {
		return nil, ErrInvalidPadding
	}
	for _, b := range plaintext[len(plaintext)-padding:] {
		if int(b) != padding {
			return nil, ErrInvalidPadding
		}
	}
	return plaintext[:len(plaintext)-padding], nil
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
