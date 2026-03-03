package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	nostr "fiatjaf.com/nostr"
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

type DMBusOptions struct {
	PrivateKey  string
	Relays      []string
	SinceUnix   int64
	OnMessage   func(context.Context, InboundDM) error
	OnError     func(error)
	SeenCap     int
	WorkerCount int
	QueueSize   int
}

type DMBus struct {
	pool      *nostr.Pool
	relays    []string
	relaysMu  sync.RWMutex
	secret    nostr.SecretKey
	public    nostr.PubKey
	onMessage func(context.Context, InboundDM) error
	onError   func(error)

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
	if opts.PrivateKey == "" {
		return nil, fmt.Errorf("private key is required")
	}

	sk, err := ParseSecretKey(opts.PrivateKey)
	if err != nil {
		return nil, err
	}

	since := opts.SinceUnix
	if since <= 0 {
		since = time.Now().Add(-10 * time.Minute).Unix()
	}
	workerCount := max(opts.WorkerCount, 4)
	queueSize := max(opts.QueueSize, 256)

	ctx, cancel := context.WithCancel(parent)
	bus := &DMBus{
		pool:         nostr.NewPool(nostr.PoolOptions{PenaltyBox: true}),
		relays:       initialRelays,
		secret:       sk,
		public:       sk.Public(),
		onMessage:    opts.OnMessage,
		onError:      opts.OnError,
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
	_, err = publishEncryptedDM(ctx, b.pool, b.secret, b.currentRelays(), pk, text)
	return err
}

func (b *DMBus) SetRelays(relays []string) error {
	next := sanitizeRelayList(relays)
	if len(next) == 0 {
		return fmt.Errorf("at least one relay is required")
	}
	b.relaysMu.Lock()
	b.relays = next
	b.relaysMu.Unlock()
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

	pool := nostr.NewPool(nostr.PoolOptions{PenaltyBox: true})
	defer pool.Close("send once finished")
	return publishEncryptedDM(ctx, pool, sk, relays, pk, text)
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
	if !re.Event.Tags.ContainsAny("p", []string{b.public.Hex()}) {
		return
	}

	eventID := re.Event.ID.Hex()
	if b.markSeen(eventID) {
		return
	}

	plaintext, err := decryptNIP04(b.secret, re.Event.PubKey, re.Event.Content)
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
			_, err := publishEncryptedDM(ctx, b.pool, b.secret, b.currentRelays(), re.Event.PubKey, text)
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

func publishEncryptedDM(ctx context.Context, pool *nostr.Pool, sk nostr.SecretKey, relays []string, to nostr.PubKey, text string) (string, error) {
	var err error
	text, err = sanitizeDMText(text)
	if err != nil {
		return "", err
	}
	ciphertext, err := encryptNIP04(sk, to, text)
	if err != nil {
		return "", err
	}

	evt := nostr.Event{
		Kind:      nostr.KindEncryptedDirectMessage,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"p", to.Hex()}},
		Content:   ciphertext,
	}
	if err := evt.Sign([32]byte(sk)); err != nil {
		return "", fmt.Errorf("sign dm event: %w", err)
	}

	published := false
	var lastErr error
	for result := range pool.PublishMany(ctx, relays, evt) {
		if result.Error == nil {
			published = true
			continue
		}
		lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
	}

	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relay accepted publish")
		}
		return "", lastErr
	}

	return evt.ID.Hex(), nil
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
