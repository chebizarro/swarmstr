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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	nostr "fiatjaf.com/nostr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/zpay32"
)

// ─── LNURL-pay helpers ────────────────────────────────────────────────────────

type lnurlPayMetadata struct {
	Callback    string `json:"callback"`
	MinSendable int64  `json:"minSendable"` // millisatoshis
	MaxSendable int64  `json:"maxSendable"` // millisatoshis
	NostrPubkey string `json:"nostrPubkey"`
	AllowsNostr bool   `json:"allowsNostr"`
	LNURL       string `json:"-"`
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
	providerPubkey, err := validateBIP340Pubkey(meta.NostrPubkey)
	if err != nil {
		return nil, fmt.Errorf("zap: invalid LNURL nostrPubkey: %w", err)
	}
	meta.NostrPubkey = providerPubkey
	meta.LNURL, err = encodeLNURL(reqURL)
	if err != nil {
		return nil, fmt.Errorf("zap: encode LNURL pay URL: %w", err)
	}
	return &meta, nil
}

func validateBIP340Pubkey(pubkeyHex string) (string, error) {
	decoded, err := hex.DecodeString(strings.TrimSpace(pubkeyHex))
	if err != nil || len(decoded) != schnorr.PubKeyBytesLen {
		return "", fmt.Errorf("must be a 32-byte hex key")
	}
	if _, err := schnorr.ParsePubKey(decoded); err != nil {
		return "", fmt.Errorf("invalid x-only secp256k1 key: %w", err)
	}
	return hex.EncodeToString(decoded), nil
}

func encodeLNURL(payURL string) (string, error) {
	data, err := bech32.ConvertBits([]byte(payURL), 8, 5, true)
	if err != nil {
		return "", err
	}
	return bech32.Encode("lnurl", data)
}

// ─── ZapSender ────────────────────────────────────────────────────────────────

// SendOpts configures a zap send operation.
type SendOpts struct {
	// Keyer is the sender signing interface.
	Keyer nostr.Keyer
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
	if opts.Keyer == nil {
		return nil, fmt.Errorf("zap: sender keyer is required")
	}
	pk, err := opts.Keyer.GetPublicKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("zap: resolve sender pubkey: %w", err)
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
		{"lnurl", meta.LNURL},
		{"p", recipientPubkeyHex},
	}
	// Embed relay list in the "relays" tag.
	relaysTag := nostr.Tag{"relays"}
	for _, r := range opts.Relays {
		relaysTag = append(relaysTag, r)
	}
	tags[0] = relaysTag

	if noteID != "" {
		tags = append(tags, nostr.Tag{"e", noteID}, nostr.Tag{"k", "1"})
	}

	zapReq := nostr.Event{
		Kind:      9734,
		Content:   comment,
		CreatedAt: nostr.Timestamp(time.Now().Unix()),
		Tags:      tags,
	}
	zapReq.PubKey = pk
	if err := opts.Keyer.SignEvent(ctx, &zapReq); err != nil {
		return nil, fmt.Errorf("zap: sign zap request: %w", err)
	}

	zapReqJSON, err := json.Marshal(zapReq)
	if err != nil {
		return nil, fmt.Errorf("zap: marshal zap request: %w", err)
	}

	// Send to LNURL callback.
	callbackURL := fmt.Sprintf("%s?amount=%d&nostr=%s&lnurl=%s",
		meta.Callback,
		amountMsat,
		url.QueryEscape(string(zapReqJSON)),
		url.QueryEscape(meta.LNURL),
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
	ID           string `json:"id"`
	SenderPubkey string `json:"sender_pubkey"`
	AmountMsat   int64  `json:"amount_msat"`
	Comment      string `json:"comment"`
	ZapRequestID string `json:"zap_request_id"`
	CreatedAt    int64  `json:"created_at"`
}

// OnZapFunc is called for each received zap receipt.
type OnZapFunc func(receipt ZapReceipt)

// ReceiveOpts configures the zap receipt listener.
type ReceiveOpts struct {
	// RecipientPubkeyHex is the pubkey to watch for zap receipts.
	RecipientPubkeyHex string
	// ProviderPubkeyHex is the LNURL provider's nostrPubkey from its metadata.
	ProviderPubkeyHex string
	// RecipientLNURL is the recipient's bech32 LNURL pay URL. When set, an
	// embedded zap request lnurl tag must match it.
	RecipientLNURL string
	// Relays is the list of relays to subscribe to.
	Relays []string
	// OnZap is called for each incoming validated zap receipt.
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
	providerPubkey, err := validateBIP340Pubkey(opts.ProviderPubkeyHex)
	if err != nil {
		return nil, fmt.Errorf("zap: provider pubkey is required and must be valid: %w", err)
	}
	if len(opts.Relays) == 0 {
		return nil, fmt.Errorf("zap: relays must be non-empty")
	}

	ctx2, cancel := context.WithCancel(ctx)
	pool := nostr.NewPool()

	f := nostr.Filter{
		Kinds: []nostr.Kind{9735},
		Tags:  nostr.TagMap{"p": []string{opts.RecipientPubkeyHex}},
	}

	go func() {
		defer pool.Close("zap receiver stopped")
		seen := make(map[string]struct{})
		sub := pool.SubscribeMany(ctx2, opts.Relays, f, nostr.SubscriptionOptions{})
		for {
			select {
			case <-ctx2.Done():
				return
			case re, ok := <-sub:
				if !ok {
					return
				}
				receipt, accepted := acceptZapReceipt(re.Event, opts, providerPubkey, seen)
				if !accepted {
					continue
				}
				opts.OnZap(receipt)
			}
		}
	}()

	return cancel, nil
}

func acceptZapReceipt(ev nostr.Event, opts ReceiveOpts, providerPubkey string, seen map[string]struct{}) (ZapReceipt, bool) {
	eventID := ev.ID.Hex()
	if _, duplicate := seen[eventID]; duplicate {
		return ZapReceipt{}, false
	}
	receipt, err := validateZapReceipt(ev, opts, providerPubkey)
	if err != nil {
		return ZapReceipt{}, false
	}
	seen[eventID] = struct{}{}
	return receipt, true
}

func validateZapReceipt(ev nostr.Event, opts ReceiveOpts, providerPubkey string) (ZapReceipt, error) {
	if ev.Kind != 9735 {
		return ZapReceipt{}, fmt.Errorf("zap: receipt has kind %d", ev.Kind)
	}
	if !ev.CheckID() || !ev.VerifySignature() {
		return ZapReceipt{}, fmt.Errorf("zap: invalid receipt id or signature")
	}
	if ev.PubKey.Hex() != providerPubkey {
		return ZapReceipt{}, fmt.Errorf("zap: receipt publisher does not match LNURL provider")
	}

	description, err := exactlyOneTag(ev.Tags, "description")
	if err != nil {
		return ZapReceipt{}, err
	}
	bolt11, err := exactlyOneTag(ev.Tags, "bolt11")
	if err != nil {
		return ZapReceipt{}, err
	}
	var request nostr.Event
	if err := json.Unmarshal([]byte(description), &request); err != nil {
		return ZapReceipt{}, fmt.Errorf("zap: invalid zap request description: %w", err)
	}
	if err := validateZapRequest(request, ev, opts); err != nil {
		return ZapReceipt{}, err
	}

	invoice, err := decodeZapInvoice(bolt11)
	if err != nil {
		return ZapReceipt{}, err
	}
	descriptionHash := sha256.Sum256([]byte(description))
	if invoice.DescriptionHash == nil || *invoice.DescriptionHash != descriptionHash {
		return ZapReceipt{}, fmt.Errorf("zap: invoice description hash does not match zap request")
	}
	if invoice.MilliSat == nil || int64(*invoice.MilliSat) <= 0 {
		return ZapReceipt{}, fmt.Errorf("zap: invoice has no amount")
	}
	amountMsat := int64(*invoice.MilliSat)
	if amountTag := tagValues(request.Tags, "amount"); len(amountTag) > 0 {
		requested, err := strconv.ParseInt(amountTag[0], 10, 64)
		if err != nil || requested <= 0 || requested != amountMsat {
			return ZapReceipt{}, fmt.Errorf("zap: invoice amount does not match zap request")
		}
	}

	return ZapReceipt{
		ID:           ev.ID.Hex(),
		SenderPubkey: request.PubKey.Hex(),
		AmountMsat:   amountMsat,
		Comment:      request.Content,
		ZapRequestID: request.ID.Hex(),
		CreatedAt:    int64(ev.CreatedAt),
	}, nil
}

func validateZapRequest(request, receipt nostr.Event, opts ReceiveOpts) error {
	if request.Kind != 9734 || !request.CheckID() || !request.VerifySignature() {
		return fmt.Errorf("zap: invalid embedded zap request")
	}
	if len(request.Tags) == 0 {
		return fmt.Errorf("zap: zap request has no tags")
	}
	requestP, err := exactlyOneTag(request.Tags, "p")
	if err != nil || requestP != opts.RecipientPubkeyHex {
		return fmt.Errorf("zap: zap request recipient mismatch")
	}
	receiptP, err := exactlyOneTag(receipt.Tags, "p")
	if err != nil || receiptP != requestP {
		return fmt.Errorf("zap: receipt recipient mismatch")
	}
	if relays := tagValues(request.Tags, "relays"); len(relays) != 1 || len(relays[0]) == 0 {
		return fmt.Errorf("zap: zap request must contain one non-empty relays tag")
	}
	if amounts := tagValues(request.Tags, "amount"); len(amounts) > 1 {
		return fmt.Errorf("zap: zap request has multiple amount tags")
	}
	if lnurls := tagValues(request.Tags, "lnurl"); len(lnurls) > 1 ||
		(len(lnurls) == 1 && opts.RecipientLNURL != "" && lnurls[0] != opts.RecipientLNURL) {
		return fmt.Errorf("zap: zap request LNURL mismatch")
	}

	for _, name := range []string{"e", "a"} {
		requestValues := tagValues(request.Tags, name)
		receiptValues := tagValues(receipt.Tags, name)
		if len(requestValues) > 1 || len(receiptValues) != len(requestValues) ||
			(len(requestValues) == 1 && receiptValues[0] != requestValues[0]) {
			return fmt.Errorf("zap: %s tag mismatch", name)
		}
	}
	if values := tagValues(request.Tags, "a"); len(values) == 1 && !validEventCoordinate(values[0]) {
		return fmt.Errorf("zap: invalid a tag")
	}

	requestProvider := tagValues(request.Tags, "P")
	if len(requestProvider) > 1 || (len(requestProvider) == 1 && requestProvider[0] != receipt.PubKey.Hex()) {
		return fmt.Errorf("zap: invalid zap request P tag")
	}
	receiptSender := tagValues(receipt.Tags, "P")
	if len(receiptSender) > 1 || (len(receiptSender) == 1 && receiptSender[0] != request.PubKey.Hex()) {
		return fmt.Errorf("zap: receipt sender mismatch")
	}
	return nil
}

func exactlyOneTag(tags nostr.Tags, name string) (string, error) {
	values := tagValues(tags, name)
	if len(values) != 1 || values[0] == "" {
		return "", fmt.Errorf("zap: expected exactly one non-empty %s tag", name)
	}
	return values[0], nil
}

func tagValues(tags nostr.Tags, name string) []string {
	values := make([]string, 0, 1)
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			values = append(values, tag[1])
		}
	}
	return values
}

func validEventCoordinate(coordinate string) bool {
	parts := strings.SplitN(coordinate, ":", 3)
	if len(parts) != 3 {
		return false
	}
	kind, err := strconv.Atoi(parts[0])
	if err != nil || kind < 0 {
		return false
	}
	_, err = validateBIP340Pubkey(parts[1])
	return err == nil
}

func decodeZapInvoice(encoded string) (*zpay32.Invoice, error) {
	for _, params := range []*chaincfg.Params{
		&chaincfg.MainNetParams,
		&chaincfg.TestNet3Params,
		&chaincfg.RegressionNetParams,
		&chaincfg.SigNetParams,
	} {
		invoice, err := zpay32.Decode(strings.TrimSpace(encoded), params)
		if err == nil {
			return invoice, nil
		}
	}
	return nil, fmt.Errorf("zap: invalid BOLT-11 invoice")
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
