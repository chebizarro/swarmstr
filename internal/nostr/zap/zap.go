// Package zap implements NIP-57 zap request/receipt support.
//
// ZapSender: given a lud16 address (Lightning Address), fetches the LNURL-pay
// metadata, creates a NIP-57 zap request event, sends it to the callback URL,
// and returns the BOLT-11 invoice returned by the wallet service.
//
// ZapReceiver: subscribes to kind:9735 zap receipt events addressed to a
// given pubkey and fires a callback for each receipt.
package zap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
)

// ─── LNURL-pay helpers ────────────────────────────────────────────────────────

type lnurlPayMetadata struct {
	Callback      string `json:"callback"`
	MinSendable   int64  `json:"minSendable"`  // millisatoshis
	MaxSendable   int64  `json:"maxSendable"`  // millisatoshis
	NostrPubkey   string `json:"nostrPubkey"`
	AllowsNostr   bool   `json:"allowsNostr"`
}

// ResolveLNURL resolves a Lightning Address (name@domain) to LNURL-pay metadata.
func ResolveLNURL(ctx context.Context, lud16 string) (*lnurlPayMetadata, error) {
	parts := strings.SplitN(lud16, "@", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("zap: invalid lightning address %q", lud16)
	}
	name, domain := parts[0], parts[1]
	reqURL := fmt.Sprintf("https://%s/.well-known/lnurlp/%s", domain, url.PathEscape(name))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("zap: build request: %w", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zap: LNURL fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zap: LNURL server returned %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var meta lnurlPayMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("zap: parse LNURL metadata: %w", err)
	}
	if !meta.AllowsNostr {
		return nil, fmt.Errorf("zap: wallet does not support Nostr zaps (allowsNostr=false)")
	}
	return &meta, nil
}

// ─── ZapSender ────────────────────────────────────────────────────────────────

// SendOpts configures a zap send operation.
type SendOpts struct {
	// PrivateKey is the sender's hex-encoded secret key.
	PrivateKey string
	// Relays is the relay list embedded in the zap request.
	Relays []string
}

// ZapResult is the result of a successful zap send.
type ZapResult struct {
	// Invoice is the BOLT-11 Lightning invoice returned by the recipient wallet.
	Invoice string `json:"invoice"`
	// ZapRequestID is the Nostr event ID of the zap request.
	ZapRequestID string `json:"zap_request_id"`
}

// Send sends a NIP-57 zap to a recipient identified by lud16 address.
//
// amountSats is the amount in satoshis; comment is optional.
// recipientPubkey is the hex pubkey of the Nostr user being zapped.
// noteID is the optional note being zapped (hex event ID).
func Send(ctx context.Context, opts SendOpts, lud16, recipientPubkeyHex, noteID string, amountSats int64, comment string) (*ZapResult, error) {
	if opts.PrivateKey == "" {
		return nil, fmt.Errorf("zap: sender private key is required")
	}
	sk, err := nostr.SecretKeyFromHex(strings.TrimSpace(opts.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("zap: parse private key: %w", err)
	}

	meta, err := ResolveLNURL(ctx, lud16)
	if err != nil {
		return nil, err
	}

	amountMsat := amountSats * 1000
	if amountMsat < meta.MinSendable {
		return nil, fmt.Errorf("zap: amount %d msat is below minimum %d msat", amountMsat, meta.MinSendable)
	}
	if meta.MaxSendable > 0 && amountMsat > meta.MaxSendable {
		return nil, fmt.Errorf("zap: amount %d msat exceeds maximum %d msat", amountMsat, meta.MaxSendable)
	}

	// Build NIP-57 zap request event (kind:9734).
	tags := nostr.Tags{
		{"relays"},
		{"amount", fmt.Sprintf("%d", amountMsat)},
		{"lnurl", lud16},
		{"p", recipientPubkeyHex},
	}
	// Embed relay list in the "relays" tag.
	relaysTag := nostr.Tag{"relays"}
	for _, r := range opts.Relays {
		relaysTag = append(relaysTag, r)
	}
	tags[0] = relaysTag

	if noteID != "" {
		tags = append(tags, nostr.Tag{"e", noteID})
	}

	zapReq := nostr.Event{
		Kind:      9734,
		Content:   comment,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      tags,
	}
	zapReq.PubKey = sk.Public()
	if err := zapReq.Sign(sk); err != nil {
		return nil, fmt.Errorf("zap: sign zap request: %w", err)
	}

	zapReqJSON, err := json.Marshal(zapReq)
	if err != nil {
		return nil, fmt.Errorf("zap: marshal zap request: %w", err)
	}

	// Send to LNURL callback.
	callbackURL := fmt.Sprintf("%s?amount=%d&nostr=%s",
		meta.Callback,
		amountMsat,
		url.QueryEscape(string(zapReqJSON)),
	)
	if comment != "" {
		callbackURL += "&comment=" + url.QueryEscape(comment)
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, callbackURL, nil)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zap: callback request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var callbackResp struct {
		PR     string `json:"pr"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &callbackResp); err != nil {
		return nil, fmt.Errorf("zap: parse callback response: %w", err)
	}
	if callbackResp.Status == "ERROR" {
		return nil, fmt.Errorf("zap: wallet error: %s", callbackResp.Reason)
	}
	if callbackResp.PR == "" {
		return nil, fmt.Errorf("zap: wallet returned no invoice")
	}

	return &ZapResult{
		Invoice:      callbackResp.PR,
		ZapRequestID: zapReq.ID.Hex(),
	}, nil
}

// ─── ZapReceiver ──────────────────────────────────────────────────────────────

// ZapReceipt represents a received kind:9735 zap receipt.
type ZapReceipt struct {
	ID             string `json:"id"`
	SenderPubkey   string `json:"sender_pubkey"`
	AmountMsat     int64  `json:"amount_msat"`
	Comment        string `json:"comment"`
	ZapRequestID   string `json:"zap_request_id"`
	CreatedAt      int64  `json:"created_at"`
}

// OnZapFunc is called for each received zap receipt.
type OnZapFunc func(receipt ZapReceipt)

// ReceiveOpts configures the zap receipt listener.
type ReceiveOpts struct {
	// RecipientPubkeyHex is the pubkey to watch for zap receipts.
	RecipientPubkeyHex string
	// Relays is the list of relays to subscribe to.
	Relays []string
	// OnZap is called for each incoming zap receipt.
	OnZap OnZapFunc
}

// StartReceiver subscribes to kind:9735 zap receipts and calls OnZap for each.
// Returns a cancel function to stop the receiver.
func StartReceiver(ctx context.Context, opts ReceiveOpts) (context.CancelFunc, error) {
	if opts.RecipientPubkeyHex == "" {
		return nil, fmt.Errorf("zap: recipient pubkey is required")
	}
	if opts.OnZap == nil {
		return nil, fmt.Errorf("zap: OnZap callback is required")
	}
	if len(opts.Relays) == 0 {
		return nil, fmt.Errorf("zap: relays must be non-empty")
	}

	ctx2, cancel := context.WithCancel(ctx)
	pool := nostr.NewPool(nostr.PoolOptions{})

	f := nostr.Filter{
		Kinds: []nostr.Kind{9735},
		Tags:  nostr.TagMap{"p": []string{opts.RecipientPubkeyHex}},
	}

	go func() {
		defer pool.Close("zap receiver stopped")
		sub := pool.SubscribeMany(ctx2, opts.Relays, f, nostr.SubscriptionOptions{})
		for {
			select {
			case <-ctx2.Done():
				return
			case re, ok := <-sub:
				if !ok {
					return
				}
				receipt := parseZapReceipt(re.Event)
				opts.OnZap(receipt)
			}
		}
	}()

	return cancel, nil
}

// parseZapReceipt extracts useful fields from a kind:9735 event.
func parseZapReceipt(ev nostr.Event) ZapReceipt {
	r := ZapReceipt{
		ID:           ev.ID.Hex(),
		SenderPubkey: ev.PubKey.Hex(),
		CreatedAt:    int64(ev.CreatedAt),
	}
	for _, tag := range ev.Tags {
		switch {
		case len(tag) >= 2 && tag[0] == "bolt11":
			// Amount is embedded in the bolt11 invoice; we leave parsing
			// to higher-level code since it requires a BOLT-11 decoder.
		case len(tag) >= 2 && tag[0] == "description":
			// The zap request JSON is in the "description" tag.
			var zapReq nostr.Event
			if err := json.Unmarshal([]byte(tag[1]), &zapReq); err == nil {
				for _, ztag := range zapReq.Tags {
					if len(ztag) >= 2 && ztag[0] == "amount" {
						fmt.Sscanf(ztag[1], "%d", &r.AmountMsat)
					}
				}
				r.Comment = zapReq.Content
				r.ZapRequestID = zapReq.ID.Hex()
			}
		}
	}
	return r
}
