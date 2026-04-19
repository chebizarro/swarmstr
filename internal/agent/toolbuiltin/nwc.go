// Package toolbuiltin – NIP-47 Nostr Wallet Connect (NWC) agent tools.
//
// Registers: nwc_get_balance, nwc_pay_invoice, nwc_make_invoice,
// nwc_lookup_invoice, nwc_list_transactions
//
// NWC enables the agent to interact with any NWC-compatible lightning wallet
// (Alby, Mutiny, LNbits NWC extension, etc.) using nostr events. The agent's
// own nostr identity sends encrypted kind-23194 requests and receives
// kind-23195 responses — no direct node access required.
//
// See: https://github.com/nostr-protocol/nips/blob/master/47.md
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"fiatjaf.com/nostr/nip44"

	"metiq/internal/agent"
	nostruntime "metiq/internal/nostr/runtime"
)

// NIP-47 event kinds.
const (
	KindNWCInfo     = 13194
	KindNWCRequest  = 23194
	KindNWCResponse = 23195
)

// NWC method names (NIP-47 spec).
const (
	nwcMethodGetBalance       = "get_balance"
	nwcMethodPayInvoice       = "pay_invoice"
	nwcMethodMakeInvoice      = "make_invoice"
	nwcMethodLookupInvoice    = "lookup_invoice"
	nwcMethodListTransactions = "list_transactions"
)

// NWCToolOpts configures the NWC tools.
type NWCToolOpts struct {
	// HubFunc returns the shared nostr hub. Required.
	HubFunc func() *nostruntime.NostrHub
	// Keyer for signing and encryption. Required.
	Keyer nostr.Keyer
	// NWCUri is the nostrwalletconnect:// connection URI.
	// Format: nostrwalletconnect://<wallet_pubkey>?relay=<relay_url>&secret=<hex_privkey>
	// If empty, tools return a helpful configuration error.
	NWCUri string
	// Relays override. If empty, extracted from NWCUri.
	Relays []string
	// Timeout for NWC request-response round-trips. Default 30s.
	Timeout time.Duration
}

// nwcKeyer wraps keyer.KeySigner with NIP-44 encryption/decryption so it
// satisfies the full nostr.Keyer interface.  Follows the same pattern as
// mainTestKeyer in cmd/metiqd/main_test.go.
type nwcKeyer struct {
	keyer.KeySigner
	sk nostr.SecretKey
}

func newNWCKeyer(hexSecret string) (nostr.Keyer, error) {
	sk, err := nostr.SecretKeyFromHex(hexSecret)
	if err != nil {
		return nil, fmt.Errorf("invalid NWC secret key: %w", err)
	}
	return nwcKeyer{KeySigner: keyer.NewPlainKeySigner([32]byte(sk)), sk: sk}, nil
}

func (k nwcKeyer) Encrypt(_ context.Context, plaintext string, recipient nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(recipient, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, ck)
}

func (k nwcKeyer) Decrypt(_ context.Context, ciphertext string, sender nostr.PubKey) (string, error) {
	ck, err := nip44.GenerateConversationKey(sender, k.sk)
	if err != nil {
		return "", err
	}
	return nip44.Decrypt(ciphertext, ck)
}

// nwcConn holds parsed NWC connection parameters.
type nwcConn struct {
	walletPubkey string   // hex pubkey of the NWC wallet service
	secret       string   // hex secret key for this connection (optional — falls back to agent keyer)
	relays       []string // relay URLs for NWC communication
	keyer        nostr.Keyer
}

// parseNWCUri parses a nostrwalletconnect:// URI into connection parameters.
func parseNWCUri(uri string) (*nwcConn, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil, fmt.Errorf("nwc_uri is empty — configure nwc_uri in agent config to enable wallet operations")
	}

	// Normalise scheme variants.
	uri = strings.Replace(uri, "nostrwalletconnect://", "nwc://", 1)
	uri = strings.Replace(uri, "nostr+walletconnect://", "nwc://", 1)
	if !strings.HasPrefix(uri, "nwc://") {
		return nil, fmt.Errorf("invalid NWC URI: expected nostrwalletconnect:// or nwc:// scheme")
	}

	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid NWC URI: %w", err)
	}

	walletPubkey := u.Host
	if walletPubkey == "" {
		return nil, fmt.Errorf("invalid NWC URI: missing wallet pubkey")
	}

	conn := &nwcConn{
		walletPubkey: walletPubkey,
		secret:       u.Query().Get("secret"),
	}

	// Extract relay URLs.
	for _, r := range u.Query()["relay"] {
		r = strings.TrimSpace(r)
		if r != "" {
			conn.relays = append(conn.relays, r)
		}
	}

	return conn, nil
}

// nwcRequest is the JSON structure sent as encrypted content of a kind-23194 event.
type nwcRequest struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

// nwcResponse is the JSON structure received as encrypted content of a kind-23195 event.
type nwcResponse struct {
	ResultType string         `json:"result_type"`
	Error      *nwcError      `json:"error,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
}

type nwcError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RegisterNWCTools registers NIP-47 Nostr Wallet Connect tools into the registry.
func RegisterNWCTools(tools *agent.ToolRegistry, opts NWCToolOpts) {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}

	var (
		conn     *nwcConn
		connOnce sync.Once
		connErr  error
	)
	getConn := func() (*nwcConn, error) {
		connOnce.Do(func() {
			conn, connErr = parseNWCUri(opts.NWCUri)
			if connErr != nil {
				return
			}
			if len(opts.Relays) > 0 {
				conn.relays = opts.Relays
			}
			if len(conn.relays) == 0 {
				connErr = fmt.Errorf("NWC: no relays — add relay= to the nwc_uri or configure relays")
				return
			}
			// If a secret is provided in the URI, build a dedicated keyer for NWC.
			// Otherwise fall back to the agent's keyer.
			if conn.secret != "" {
				k, kErr := newNWCKeyer(conn.secret)
				if kErr != nil {
					connErr = fmt.Errorf("NWC: invalid secret in URI: %w", kErr)
					return
				}
				conn.keyer = k
			} else {
				conn.keyer = opts.Keyer
			}
		})
		return conn, connErr
	}

	// sendNWCRequest sends an NWC request and waits for the response.
	sendNWCRequest := func(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
		c, err := getConn()
		if err != nil {
			return nil, err
		}

		hub := opts.HubFunc()
		if hub == nil {
			return nil, fmt.Errorf("NWC: nostr hub unavailable")
		}

		// Build the request payload.
		reqPayload, err := json.Marshal(nwcRequest{Method: method, Params: params})
		if err != nil {
			return nil, fmt.Errorf("NWC: marshal request: %w", err)
		}

		// Encrypt the payload with the wallet's pubkey.
		walletPK, err := nostr.PubKeyFromHex(c.walletPubkey)
		if err != nil {
			return nil, fmt.Errorf("NWC: invalid wallet pubkey: %w", err)
		}
		encrypted, err := c.keyer.Encrypt(ctx, string(reqPayload), walletPK)
		if err != nil {
			return nil, fmt.Errorf("NWC: encrypt request: %w", err)
		}

		// Get our pubkey for the event.
		ourPK, err := c.keyer.GetPublicKey(ctx)
		if err != nil {
			return nil, fmt.Errorf("NWC: get public key: %w", err)
		}

		// Build the kind-23194 event.
		ev := nostr.Event{
			Kind:      nostr.Kind(KindNWCRequest),
			Content:   encrypted,
			CreatedAt: nostr.Now(),
			Tags: nostr.Tags{
				{"p", c.walletPubkey},
			},
		}
		if err := c.keyer.SignEvent(ctx, &ev); err != nil {
			return nil, fmt.Errorf("NWC: sign request: %w", err)
		}

		// Subscribe for the response BEFORE publishing the request
		// to avoid missing a fast response.
		respCtx, respCancel := context.WithTimeout(ctx, opts.Timeout)
		defer respCancel()

		responseCh := make(chan *nwcResponse, 1)

		sub, subErr := hub.Subscribe(respCtx, nostruntime.SubOpts{
			Filter: nostr.Filter{
				Kinds:   []nostr.Kind{nostr.Kind(KindNWCResponse)},
				Authors: []nostr.PubKey{walletPK},
				Tags:    nostr.TagMap{"p": []string{ourPK.Hex()}, "e": []string{ev.ID.Hex()}},
				Since:   nostr.Now() - 10, // slight backdate
			},
			Relays: c.relays,
			OnEvent: func(re nostr.RelayEvent) {
				// Decrypt the response.
				plaintext, decErr := c.keyer.Decrypt(respCtx, re.Event.Content, walletPK)
				if decErr != nil {
					log.Printf("NWC: decrypt response error: %v", decErr)
					return
				}
				var resp nwcResponse
				if jsonErr := json.Unmarshal([]byte(plaintext), &resp); jsonErr != nil {
					log.Printf("NWC: unmarshal response error: %v", jsonErr)
					return
				}
				select {
				case responseCh <- &resp:
				default:
				}
			},
		})
		if subErr != nil {
			return nil, fmt.Errorf("NWC: subscribe for response: %w", subErr)
		}
		defer hub.Unsubscribe(sub.ID)

		// Publish the request.
		results := hub.Publish(ctx, c.relays, ev)
		published := false
		for pr := range results {
			if pr.Error == nil {
				published = true
			}
		}
		if !published {
			return nil, fmt.Errorf("NWC: failed to publish request to any relay")
		}

		// Wait for the response.
		select {
		case resp := <-responseCh:
			if resp.Error != nil {
				return nil, fmt.Errorf("NWC wallet error (%s): %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		case <-respCtx.Done():
			return nil, fmt.Errorf("NWC: timeout waiting for wallet response (method=%s, timeout=%s)", method, opts.Timeout)
		}
	}

	// Helper to format tool results as JSON.
	jsonResult := func(data any) (string, error) {
		out, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return "", fmt.Errorf("NWC: marshal result: %w", err)
		}
		return string(out), nil
	}

	// ── nwc_get_balance ──────────────────────────────────────────────────────
	tools.RegisterWithDef("nwc_get_balance", func(ctx context.Context, args map[string]any) (string, error) {
		result, err := sendNWCRequest(ctx, nwcMethodGetBalance, nil)
		if err != nil {
			return "", err
		}
		return jsonResult(result)
	}, NWCGetBalanceDef)

	// ── nwc_pay_invoice ──────────────────────────────────────────────────────
	tools.RegisterWithDef("nwc_pay_invoice", func(ctx context.Context, args map[string]any) (string, error) {
		invoice, _ := args["invoice"].(string)
		if invoice == "" {
			return "", fmt.Errorf("nwc_pay_invoice: invoice is required")
		}
		params := map[string]any{"invoice": invoice}
		if amountMsats, ok := args["amount_msats"].(float64); ok && amountMsats > 0 {
			params["amount"] = int64(amountMsats) // NIP-47 uses "amount" in msats
		}
		result, err := sendNWCRequest(ctx, nwcMethodPayInvoice, params)
		if err != nil {
			return "", err
		}
		return jsonResult(result)
	}, NWCPayInvoiceDef)

	// ── nwc_make_invoice ─────────────────────────────────────────────────────
	tools.RegisterWithDef("nwc_make_invoice", func(ctx context.Context, args map[string]any) (string, error) {
		amountMsats, ok := args["amount_msats"].(float64)
		if !ok || amountMsats <= 0 {
			return "", fmt.Errorf("nwc_make_invoice: amount_msats is required and must be positive")
		}
		params := map[string]any{"amount": int64(amountMsats)}
		if desc, ok := args["description"].(string); ok && desc != "" {
			params["description"] = desc
		}
		if expiry, ok := args["expiry"].(float64); ok && expiry > 0 {
			params["expiry"] = int64(expiry)
		}
		result, err := sendNWCRequest(ctx, nwcMethodMakeInvoice, params)
		if err != nil {
			return "", err
		}
		return jsonResult(result)
	}, NWCMakeInvoiceDef)

	// ── nwc_lookup_invoice ───────────────────────────────────────────────────
	tools.RegisterWithDef("nwc_lookup_invoice", func(ctx context.Context, args map[string]any) (string, error) {
		params := map[string]any{}
		if ph, ok := args["payment_hash"].(string); ok && ph != "" {
			params["payment_hash"] = ph
		} else if inv, ok := args["invoice"].(string); ok && inv != "" {
			params["invoice"] = inv
		} else {
			return "", fmt.Errorf("nwc_lookup_invoice: payment_hash or invoice is required")
		}
		result, err := sendNWCRequest(ctx, nwcMethodLookupInvoice, params)
		if err != nil {
			return "", err
		}
		return jsonResult(result)
	}, NWCLookupInvoiceDef)

	// ── nwc_list_transactions ────────────────────────────────────────────────
	tools.RegisterWithDef("nwc_list_transactions", func(ctx context.Context, args map[string]any) (string, error) {
		params := map[string]any{}
		if from, ok := args["from"].(float64); ok && from > 0 {
			params["from"] = int64(from)
		}
		if until, ok := args["until"].(float64); ok && until > 0 {
			params["until"] = int64(until)
		}
		if limit, ok := args["limit"].(float64); ok && limit > 0 {
			params["limit"] = int64(limit)
		} else {
			params["limit"] = int64(20)
		}
		if txType, ok := args["type"].(string); ok && txType != "" {
			params["type"] = txType
		}
		result, err := sendNWCRequest(ctx, nwcMethodListTransactions, params)
		if err != nil {
			return "", err
		}
		return jsonResult(result)
	}, NWCListTransactionsDef)
}
