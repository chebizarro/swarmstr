package lightning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip04"
	"fiatjaf.com/nostr/nip44"

	nostruntime "metiq/internal/nostr/runtime"
)

const (
	KindNWCInfo     = 13194
	KindNWCRequest  = 23194
	KindNWCResponse = 23195
	// KindNWCNotificationNIP04 is the legacy NIP-04 notification kind.
	KindNWCNotificationNIP04 = 23196
	// KindNWCNotification is the current NIP-44 notification kind.
	KindNWCNotification = 23197

	NWCMethodGetBalance       = "get_balance"
	NWCMethodPayInvoice       = "pay_invoice"
	NWCMethodMakeInvoice      = "make_invoice"
	NWCMethodLookupInvoice    = "lookup_invoice"
	NWCMethodListTransactions = "list_transactions"
)

var ErrNWCAmbiguous = errors.New("NWC request was published but no conclusive response was received")

type NWCConnection struct {
	WalletPubKey string
	Secret       string
	Relays       []string
}

const (
	NWCEncryptionNIP44 = "nip44_v2"
	NWCEncryptionNIP04 = "nip04"
)

// NWCInfo is the verified capability advertisement from kind 13194.
type NWCInfo struct {
	Event         nostr.Event
	Methods       []string
	Encryptions   []string
	Notifications []string
}

// NWCNotification is the decrypted payload of kind 23196 or 23197.
type NWCNotification struct {
	Type         string         `json:"notification_type"`
	Notification map[string]any `json:"notification"`
	Event        nostr.Event    `json:"-"`
	Encryption   string         `json:"-"`
}

type NWCSubscription struct {
	Filter  nostr.Filter
	Relays  []string
	OnEvent func(nostr.Event)
}

// NWCTransport is injectable below encryption so tests can inspect the actual
// signed event and encrypted NIP-47 request.
type NWCTransport interface {
	Subscribe(context.Context, NWCSubscription) (func(), error)
	Publish(context.Context, []string, nostr.Event) (bool, error)
}

type NWCTransportFunc struct {
	SubscribeFunc func(context.Context, NWCSubscription) (func(), error)
	PublishFunc   func(context.Context, []string, nostr.Event) (bool, error)
}

func (f NWCTransportFunc) Subscribe(ctx context.Context, sub NWCSubscription) (func(), error) {
	if f.SubscribeFunc == nil {
		return nil, errors.New("NWC subscribe transport is unavailable")
	}
	return f.SubscribeFunc(ctx, sub)
}

func (f NWCTransportFunc) Publish(ctx context.Context, relays []string, event nostr.Event) (bool, error) {
	if f.PublishFunc == nil {
		return false, errors.New("NWC publish transport is unavailable")
	}
	return f.PublishFunc(ctx, relays, event)
}

// NewHubNWCTransport adapts the daemon's shared event-driven Nostr hub.
func NewHubNWCTransport(hubFunc func() *nostruntime.NostrHub) NWCTransport {
	return NWCTransportFunc{
		SubscribeFunc: func(ctx context.Context, subscription NWCSubscription) (func(), error) {
			if hubFunc == nil || hubFunc() == nil {
				return nil, errors.New("NWC nostr hub is unavailable")
			}
			hub := hubFunc()
			sub, err := hub.Subscribe(ctx, nostruntime.SubOpts{
				Filter: subscription.Filter,
				Relays: subscription.Relays,
				OnEvent: func(event nostr.RelayEvent) {
					subscription.OnEvent(event.Event)
				},
			})
			if err != nil {
				return nil, err
			}
			var once sync.Once
			return func() { once.Do(func() { hub.Unsubscribe(sub.ID) }) }, nil
		},
		PublishFunc: func(ctx context.Context, relays []string, event nostr.Event) (bool, error) {
			if hubFunc == nil || hubFunc() == nil {
				return false, errors.New("NWC nostr hub is unavailable")
			}
			published := false
			var joined error
			for result := range hubFunc().Publish(ctx, relays, event) {
				if result.Error == nil {
					published = true
				} else {
					joined = errors.Join(joined, result.Error)
				}
			}
			if published {
				return true, nil
			}
			if joined == nil {
				joined = errors.New("request was not accepted by any relay")
			}
			return false, joined
		},
	}
}

type NWCClientConfig struct {
	ID        string
	URI       string
	Relays    []string
	Keyer     nostr.Keyer
	Timeout   time.Duration
	Transport NWCTransport
}

type NWCClient struct {
	id         string
	connection NWCConnection
	keyer      nostr.Keyer
	timeout    time.Duration
	transport  NWCTransport

	mu     sync.RWMutex
	closed bool
	info   *NWCInfo
}

type nwcKeyer struct {
	keyer.KeySigner
	secret nostr.SecretKey
}

func (k nwcKeyer) Encrypt(_ context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	conversation, err := nip44.GenerateConversationKey(recipient, k.secret)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, conversation)
}

func (k nwcKeyer) Decrypt(_ context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	conversation, err := nip44.GenerateConversationKey(sender, k.secret)
	if err != nil {
		return "", err
	}
	return nip44.Decrypt(ciphertext, conversation)
}

func (k nwcKeyer) EncryptNIP04(_ context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	shared, err := nip04.ComputeSharedSecret(recipient, [32]byte(k.secret))
	if err != nil {
		return "", err
	}
	return nip04.Encrypt(plaintext, shared)
}

func (k nwcKeyer) DecryptNIP04(_ context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	shared, err := nip04.ComputeSharedSecret(sender, [32]byte(k.secret))
	if err != nil {
		return "", err
	}
	return nip04.Decrypt(ciphertext, shared)
}

func NewNWCKeyer(hexSecret string) (nostr.Keyer, error) {
	secret, err := nostr.SecretKeyFromHex(strings.TrimSpace(hexSecret))
	if err != nil {
		return nil, errors.New("invalid NWC secret key")
	}
	return nwcKeyer{KeySigner: keyer.NewPlainKeySigner([32]byte(secret)), secret: secret}, nil
}

func ParseNWCURI(raw string) (NWCConnection, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return NWCConnection{}, errors.New("NWC connection URI is empty")
	}
	raw = strings.Replace(raw, "nostrwalletconnect://", "nwc://", 1)
	raw = strings.Replace(raw, "nostr+walletconnect://", "nwc://", 1)
	if !strings.HasPrefix(raw, "nwc://") {
		return NWCConnection{}, errors.New("invalid NWC connection URI scheme")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return NWCConnection{}, errors.New("invalid NWC connection URI")
	}
	if _, err := nostr.PubKeyFromHex(parsed.Host); err != nil {
		return NWCConnection{}, errors.New("invalid NWC wallet public key")
	}
	connection := NWCConnection{
		WalletPubKey: parsed.Host,
		Secret:       strings.TrimSpace(parsed.Query().Get("secret")),
	}
	for _, relay := range parsed.Query()["relay"] {
		if relay = strings.TrimSpace(relay); relay != "" {
			connection.Relays = append(connection.Relays, relay)
		}
	}
	return connection, nil
}

func NewNWCClient(cfg NWCClientConfig) (*NWCClient, error) {
	connection, err := ParseNWCURI(cfg.URI)
	if err != nil {
		return nil, err
	}
	if len(cfg.Relays) > 0 {
		connection.Relays = append([]string(nil), cfg.Relays...)
	}
	if len(connection.Relays) == 0 {
		return nil, errors.New("NWC connection has no relays")
	}
	clientKeyer := cfg.Keyer
	if connection.Secret != "" {
		clientKeyer, err = NewNWCKeyer(connection.Secret)
		if err != nil {
			return nil, err
		}
	}
	if clientKeyer == nil {
		return nil, errors.New("NWC client keyer is unavailable")
	}
	if cfg.Transport == nil {
		return nil, errors.New("NWC transport is unavailable")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	id := strings.TrimSpace(cfg.ID)
	if id == "" {
		id = "nwc"
	}
	return &NWCClient{
		id: id, connection: connection, keyer: clientKeyer,
		timeout: cfg.Timeout, transport: cfg.Transport,
	}, nil
}

func (c *NWCClient) ID() string { return c.id }

func (c *NWCClient) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

// Info returns the latest verified wallet information event observed by this client.
func (c *NWCClient) Info() (NWCInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.info == nil {
		return NWCInfo{}, false
	}
	return *c.info, true
}

// SubscribeInfo opens an event-driven kind-13194 discovery subscription. The
// latest verified replaceable event is cached for request encryption negotiation.
func (c *NWCClient) SubscribeInfo(ctx context.Context, onInfo func(NWCInfo)) (func(), error) {
	walletKey, err := nostr.PubKeyFromHex(c.connection.WalletPubKey)
	if err != nil {
		return nil, errors.New("NWC wallet public key is invalid")
	}
	return c.transport.Subscribe(ctx, NWCSubscription{
		Filter: nostr.Filter{
			Kinds:   []nostr.Kind{nostr.Kind(KindNWCInfo)},
			Authors: []nostr.PubKey{walletKey},
		},
		Relays: append([]string(nil), c.connection.Relays...),
		OnEvent: func(event nostr.Event) {
			info, parseErr := ParseNWCInfoEvent(event, walletKey)
			if parseErr != nil {
				return
			}
			c.mu.Lock()
			if c.info != nil {
				older := c.info.Event.CreatedAt > info.Event.CreatedAt
				sameTimeHigherID := c.info.Event.CreatedAt == info.Event.CreatedAt &&
					info.Event.ID.Hex() > c.info.Event.ID.Hex()
				if older || sameTimeHigherID {
					c.mu.Unlock()
					return
				}
			}
			c.info = &info
			c.mu.Unlock()
			if onInfo != nil {
				onInfo(info)
			}
		},
	})
}

// SubscribeNotifications opens a persistent, scoped notification subscription.
// It selects the notification kind from verified info when available and listens
// to both formats while capabilities are still unknown.
func (c *NWCClient) SubscribeNotifications(ctx context.Context, onNotification func(NWCNotification)) (func(), error) {
	walletKey, err := nostr.PubKeyFromHex(c.connection.WalletPubKey)
	if err != nil {
		return nil, errors.New("NWC wallet public key is invalid")
	}
	ourKey, err := c.keyer.GetPublicKey(ctx)
	if err != nil {
		return nil, errors.New("resolve NWC client public key failed")
	}
	kinds := []nostr.Kind{nostr.Kind(KindNWCNotification), nostr.Kind(KindNWCNotificationNIP04)}
	if info, ok := c.Info(); ok {
		scheme, negotiateErr := NegotiateNWCEncryption(info)
		if negotiateErr != nil {
			return nil, negotiateErr
		}
		if scheme == NWCEncryptionNIP44 {
			kinds = []nostr.Kind{nostr.Kind(KindNWCNotification)}
		} else {
			kinds = []nostr.Kind{nostr.Kind(KindNWCNotificationNIP04)}
		}
	}
	seen := make(map[string]struct{})
	var seenMu sync.Mutex
	return c.transport.Subscribe(ctx, NWCSubscription{
		Filter: nostr.Filter{
			Kinds:   kinds,
			Authors: []nostr.PubKey{walletKey},
			Tags:    nostr.TagMap{"p": []string{ourKey.Hex()}},
		},
		Relays: append([]string(nil), c.connection.Relays...),
		OnEvent: func(event nostr.Event) {
			if !validNWCNotificationEvent(event, walletKey, ourKey.Hex()) {
				return
			}
			seenMu.Lock()
			if _, duplicate := seen[event.ID.Hex()]; duplicate {
				seenMu.Unlock()
				return
			}
			seen[event.ID.Hex()] = struct{}{}
			seenMu.Unlock()
			scheme := NWCEncryptionNIP44
			if event.Kind == nostr.Kind(KindNWCNotificationNIP04) {
				scheme = NWCEncryptionNIP04
			}
			plaintext, decryptErr := c.decryptPayload(ctx, event.Content, walletKey, scheme)
			if decryptErr != nil {
				return
			}
			var notification NWCNotification
			if json.Unmarshal([]byte(plaintext), &notification) != nil || notification.Type == "" {
				return
			}
			notification.Event = event
			notification.Encryption = scheme
			if onNotification != nil {
				onNotification(notification)
			}
		},
	})
}

// ParseNWCInfoEvent validates and parses the current NIP-47 info wire format.
func ParseNWCInfoEvent(event nostr.Event, wallet nostr.PubKey) (NWCInfo, error) {
	if event.Kind != nostr.Kind(KindNWCInfo) || event.PubKey != wallet || !validSignedNWCEvent(event) {
		return NWCInfo{}, errors.New("invalid NWC info event")
	}
	now := nostr.Now()
	if event.CreatedAt > now+600 || event.CreatedAt < now-nostr.Timestamp((365*24*time.Hour)/time.Second) {
		return NWCInfo{}, errors.New("NWC info event timestamp is unreasonable")
	}
	info := NWCInfo{Event: event, Methods: strings.Fields(event.Content)}
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "encryption":
			info.Encryptions = append(info.Encryptions, strings.Fields(tag[1])...)
		case "notifications":
			info.Notifications = append(info.Notifications, strings.Fields(tag[1])...)
		}
	}
	return info, nil
}

// NegotiateNWCEncryption prefers NIP-44. Per NIP-47, a missing encryption
// advertisement means the wallet is legacy NIP-04-only.
func NegotiateNWCEncryption(info NWCInfo) (string, error) {
	if len(info.Encryptions) == 0 {
		return NWCEncryptionNIP04, nil
	}
	for _, scheme := range info.Encryptions {
		if scheme == NWCEncryptionNIP44 {
			return NWCEncryptionNIP44, nil
		}
	}
	for _, scheme := range info.Encryptions {
		if scheme == NWCEncryptionNIP04 {
			return NWCEncryptionNIP04, nil
		}
	}
	return "", errors.New("NWC wallet advertises no supported encryption scheme")
}

func (c *NWCClient) Request(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	result, _, err := c.request(ctx, method, params)
	return result, err
}

func (c *NWCClient) GetBalance(ctx context.Context) (map[string]any, error) {
	return c.Request(ctx, NWCMethodGetBalance, nil)
}

func (c *NWCClient) PayInvoiceRaw(ctx context.Context, invoice string, amountMSat int64) (map[string]any, error) {
	params := map[string]any{"invoice": invoice}
	if amountMSat > 0 {
		params["amount"] = amountMSat
	}
	return c.Request(ctx, NWCMethodPayInvoice, params)
}

// PayInvoiceTool preserves the legacy raw JSON result while routing payment
// through invoice decoding and cryptographic preimage verification. Published
// timeouts surface as ErrPaymentPending rather than an ordinary retryable error.
func (c *NWCClient) PayInvoiceTool(ctx context.Context, encoded string, amountOverrideMSat int64) (map[string]any, error) {
	decoded, err := decodeBOLT11AnyNetwork(encoded)
	if err != nil {
		return nil, err
	}
	if !time.Now().Before(decoded.ExpiresAt) {
		return nil, ErrInvoiceExpired
	}
	amount := decoded.AmountMSat
	if amount <= 0 {
		amount = amountOverrideMSat
	}
	if amount <= 0 {
		return nil, fmt.Errorf("%w: amountless invoice requires amount_msats", ErrInvoiceAmount)
	}
	params := map[string]any{"invoice": encoded}
	if amountOverrideMSat > 0 {
		params["amount"] = amountOverrideMSat
	}
	result, published, err := c.request(ctx, NWCMethodPayInvoice, params)
	if err != nil {
		if published && errors.Is(err, ErrNWCAmbiguous) {
			return nil, ErrPaymentPending
		}
		return nil, err
	}
	payment := nwcPaymentResult(result, decoded.PaymentHash, amount)
	if payment.Status != PaymentStatusSucceeded {
		return nil, ErrPaymentPending
	}
	if err := payment.Validate(); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *NWCClient) MakeInvoice(ctx context.Context, params map[string]any) (map[string]any, error) {
	return c.Request(ctx, NWCMethodMakeInvoice, params)
}

func (c *NWCClient) LookupInvoice(ctx context.Context, params map[string]any) (map[string]any, error) {
	return c.Request(ctx, NWCMethodLookupInvoice, params)
}

func (c *NWCClient) ListTransactions(ctx context.Context, params map[string]any) (map[string]any, error) {
	return c.Request(ctx, NWCMethodListTransactions, params)
}

type nwcRequest struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type nwcResponse struct {
	ResultType string          `json:"result_type"`
	Error      *NWCWalletError `json:"error,omitempty"`
	Result     map[string]any  `json:"result,omitempty"`
}

type NWCWalletError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *NWCWalletError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("NWC wallet error (%s): %s", e.Code, e.Message)
}

func (c *NWCClient) request(ctx context.Context, method string, params map[string]any) (map[string]any, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return nil, false, errors.New("NWC client is closed")
	}
	walletKey, err := nostr.PubKeyFromHex(c.connection.WalletPubKey)
	if err != nil {
		return nil, false, errors.New("NWC wallet public key is invalid")
	}
	payload, err := json.Marshal(nwcRequest{Method: method, Params: params})
	if err != nil {
		return nil, false, fmt.Errorf("encode NWC request: %w", err)
	}
	scheme := NWCEncryptionNIP44
	if info, ok := c.Info(); ok {
		scheme, err = NegotiateNWCEncryption(info)
		if err != nil {
			return nil, false, err
		}
	}
	encrypted, err := c.encryptPayload(ctx, string(payload), walletKey, scheme)
	if err != nil {
		return nil, false, errors.New("encrypt NWC request failed")
	}
	ourKey, err := c.keyer.GetPublicKey(ctx)
	if err != nil {
		return nil, false, errors.New("resolve NWC client public key failed")
	}
	event := nostr.Event{
		Kind: nostr.Kind(KindNWCRequest), Content: encrypted, CreatedAt: nostr.Now(),
		Tags: nostr.Tags{{"p", c.connection.WalletPubKey}},
	}
	if scheme == NWCEncryptionNIP44 {
		event.Tags = append(event.Tags, nostr.Tag{"encryption", NWCEncryptionNIP44})
	}
	if deadline, ok := ctx.Deadline(); ok {
		event.Tags = append(event.Tags, nostr.Tag{"expiration", fmt.Sprint(deadline.Unix())})
	}
	if err := c.keyer.SignEvent(ctx, &event); err != nil {
		return nil, false, errors.New("sign NWC request failed")
	}

	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	responses := make(chan nwcResponse, 1)
	stop, err := c.transport.Subscribe(requestCtx, NWCSubscription{
		Filter: nostr.Filter{
			Kinds:   []nostr.Kind{nostr.Kind(KindNWCResponse)},
			Authors: []nostr.PubKey{walletKey},
			Tags:    nostr.TagMap{"p": []string{ourKey.Hex()}, "e": []string{event.ID.Hex()}},
			Since:   nostr.Now() - 10,
		},
		Relays: append([]string(nil), c.connection.Relays...),
		OnEvent: func(responseEvent nostr.Event) {
			if !validNWCResponseEvent(responseEvent, walletKey, ourKey.Hex(), event.ID.Hex()) {
				return
			}
			plaintext, decryptErr := c.decryptPayload(requestCtx, responseEvent.Content, walletKey, scheme)
			if decryptErr != nil {
				return
			}
			var response nwcResponse
			if json.Unmarshal([]byte(plaintext), &response) != nil {
				return
			}
			select {
			case responses <- response:
			default:
			}
		},
	})
	if err != nil {
		return nil, false, errors.New("subscribe for NWC response failed")
	}
	if stop == nil {
		stop = func() {}
	}
	defer stop()

	published, err := c.transport.Publish(requestCtx, append([]string(nil), c.connection.Relays...), event)
	if err != nil && !published {
		return nil, false, errors.New("publish NWC request failed")
	}
	select {
	case response := <-responses:
		if response.ResultType != method {
			return nil, published, errors.New("NWC response type mismatch")
		}
		if response.Error != nil {
			return nil, published, response.Error
		}
		if response.Result == nil {
			response.Result = map[string]any{}
		}
		return response.Result, published, nil
	case <-requestCtx.Done():
		if published {
			return nil, true, ErrNWCAmbiguous
		}
		return nil, false, requestCtx.Err()
	}
}

type nwcNIP04Cipher interface {
	EncryptNIP04(context.Context, string, nostr.PubKey) (string, error)
	DecryptNIP04(context.Context, string, nostr.PubKey) (string, error)
}

func (c *NWCClient) encryptPayload(ctx context.Context, plaintext string, recipient nostr.PubKey, scheme string) (string, error) {
	if scheme == NWCEncryptionNIP44 {
		return c.keyer.Encrypt(ctx, plaintext, recipient)
	}
	cipher, ok := c.keyer.(nwcNIP04Cipher)
	if !ok {
		return "", errors.New("NWC keyer does not support NIP-04")
	}
	return cipher.EncryptNIP04(ctx, plaintext, recipient)
}

func (c *NWCClient) decryptPayload(ctx context.Context, ciphertext string, sender nostr.PubKey, scheme string) (string, error) {
	if scheme == NWCEncryptionNIP44 {
		return c.keyer.Decrypt(ctx, ciphertext, sender)
	}
	cipher, ok := c.keyer.(nwcNIP04Cipher)
	if !ok {
		return "", errors.New("NWC keyer does not support NIP-04")
	}
	return cipher.DecryptNIP04(ctx, ciphertext, sender)
}

func validNWCResponseEvent(event nostr.Event, wallet nostr.PubKey, ourKey, requestID string) bool {
	if event.Kind != nostr.Kind(KindNWCResponse) || event.PubKey != wallet {
		return false
	}
	if !validSignedNWCEvent(event) {
		return false
	}
	now := nostr.Now()
	if event.CreatedAt > now+600 || event.CreatedAt < now-nostr.Timestamp((365*24*time.Hour)/time.Second) {
		return false
	}
	var hasP, hasE bool
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "p":
			hasP = hasP || tag[1] == ourKey
		case "e":
			hasE = hasE || tag[1] == requestID
		}
	}
	return hasP && hasE
}

func validNWCNotificationEvent(event nostr.Event, wallet nostr.PubKey, ourKey string) bool {
	if (event.Kind != nostr.Kind(KindNWCNotification) &&
		event.Kind != nostr.Kind(KindNWCNotificationNIP04)) ||
		event.PubKey != wallet || !validSignedNWCEvent(event) {
		return false
	}
	now := nostr.Now()
	if event.CreatedAt > now+600 || event.CreatedAt < now-nostr.Timestamp((365*24*time.Hour)/time.Second) {
		return false
	}
	return event.Tags.ContainsAny("p", []string{ourKey})
}

func (c *NWCClient) PayInvoice(ctx context.Context, request PaymentRequest) (PaymentResult, error) {
	result, published, err := c.request(ctx, NWCMethodPayInvoice, map[string]any{"invoice": request.Invoice})
	if err != nil {
		if published && (errors.Is(err, ErrNWCAmbiguous) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: request.PaymentHash}, nil
		}
		var walletErr *NWCWalletError
		if errors.As(err, &walletErr) {
			return PaymentResult{
				Status: PaymentStatusFailed, PaymentHash: request.PaymentHash,
				FailureCode: walletErr.Code, FailureMessage: walletErr.Message,
			}, nil
		}
		return PaymentResult{}, err
	}
	payment := nwcPaymentResult(result, request.PaymentHash, request.AmountMSat)
	if payment.Status == PaymentStatusSucceeded {
		if err := ValidateSucceededResult(request, payment); err != nil {
			return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: request.PaymentHash}, err
		}
	}
	return payment, nil
}

func (c *NWCClient) LookupPayment(ctx context.Context, lookup PaymentLookup) (PaymentResult, error) {
	hashHex := hex.EncodeToString(lookup.PaymentHash[:])
	result, published, err := c.request(ctx, NWCMethodLookupInvoice, map[string]any{"payment_hash": hashHex})
	if err != nil {
		var walletErr *NWCWalletError
		if errors.As(err, &walletErr) && strings.EqualFold(walletErr.Code, "NOT_FOUND") {
			return PaymentResult{Status: PaymentStatusNotFound, PaymentHash: lookup.PaymentHash}, nil
		}
		if published {
			return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: lookup.PaymentHash}, nil
		}
		return PaymentResult{}, err
	}
	if !strings.EqualFold(stringValue(result["type"]), "outgoing") {
		return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: lookup.PaymentHash}, nil
	}
	switch strings.ToLower(stringValue(result["state"])) {
	case "settled", "succeeded":
		return nwcPaymentResult(result, lookup.PaymentHash, int64Value(result["amount"])), nil
	case "failed":
		return PaymentResult{
			Status: PaymentStatusFailed, PaymentHash: lookup.PaymentHash,
			FailureCode: "payment_failed", FailureMessage: "wallet reports terminal payment failure",
		}, nil
	default:
		return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: lookup.PaymentHash}, nil
	}
}

func nwcPaymentResult(result map[string]any, paymentHash [32]byte, amountMSat int64) PaymentResult {
	preimage, err := hex.DecodeString(strings.TrimSpace(stringValue(result["preimage"])))
	if err != nil || len(preimage) != 32 {
		return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: paymentHash}
	}
	if digest := sha256.Sum256(preimage); digest != paymentHash {
		return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: paymentHash}
	}
	if returnedHash := strings.TrimSpace(stringValue(result["payment_hash"])); returnedHash != "" {
		decoded, decodeErr := decodeHash(returnedHash)
		if decodeErr != nil || decoded != paymentHash {
			return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: paymentHash}
		}
	}
	if returnedAmount := int64Value(result["amount"]); returnedAmount > 0 {
		amountMSat = returnedAmount
	}
	return PaymentResult{
		Status: PaymentStatusSucceeded, PaymentHash: paymentHash, Preimage: preimage,
		AmountMSat: amountMSat, FeeMSat: int64Value(result["fees_paid"]),
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		number, _ := typed.Int64()
		return number
	default:
		return 0
	}
}
