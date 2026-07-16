package lightning

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"github.com/btcsuite/btcd/chaincfg"
)

type protocolNWCTransport struct {
	t              *testing.T
	walletKeyer    nostr.Keyer
	subscription   NWCSubscription
	response       func(nwcRequest) nwcResponse
	request        nwcRequest
	publishedEvent nostr.Event

	mu       sync.Mutex
	cleanups int
}

func (t *protocolNWCTransport) Subscribe(_ context.Context, subscription NWCSubscription) (func(), error) {
	t.subscription = subscription
	return func() {
		t.mu.Lock()
		t.cleanups++
		t.mu.Unlock()
	}, nil
}

func (t *protocolNWCTransport) Publish(ctx context.Context, _ []string, event nostr.Event) (bool, error) {
	t.publishedEvent = event
	if !event.CheckID() || !event.VerifySignature() {
		t.t.Fatal("published NWC request is not a valid signed event")
	}
	plaintext, err := t.walletKeyer.Decrypt(ctx, event.Content, event.PubKey)
	if err != nil {
		t.t.Fatalf("wallet decrypt request: %v", err)
	}
	if err := json.Unmarshal([]byte(plaintext), &t.request); err != nil {
		t.t.Fatalf("decode encrypted request: %v", err)
	}
	if t.response == nil {
		return true, nil
	}
	responsePayload, err := json.Marshal(t.response(t.request))
	if err != nil {
		t.t.Fatalf("marshal response: %v", err)
	}
	encrypted, err := t.walletKeyer.Encrypt(ctx, string(responsePayload), event.PubKey)
	if err != nil {
		t.t.Fatalf("encrypt response: %v", err)
	}
	walletPubKey, err := t.walletKeyer.GetPublicKey(ctx)
	if err != nil {
		t.t.Fatalf("wallet pubkey: %v", err)
	}
	responseEvent := nostr.Event{
		Kind: nostr.Kind(KindNWCResponse), PubKey: walletPubKey,
		CreatedAt: nostr.Now(), Content: encrypted,
		Tags: nostr.Tags{{"p", event.PubKey.Hex()}, {"e", event.ID.Hex()}},
	}
	if err := t.walletKeyer.SignEvent(ctx, &responseEvent); err != nil {
		t.t.Fatalf("sign response: %v", err)
	}
	t.subscription.OnEvent(responseEvent)
	return true, nil
}

func newProtocolNWCClient(t *testing.T, transport *protocolNWCTransport, timeout time.Duration) *NWCClient {
	t.Helper()
	clientSecret := "1111111111111111111111111111111111111111111111111111111111111111"
	walletPubKey, err := transport.walletKeyer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatalf("wallet public key: %v", err)
	}
	client, err := NewNWCClient(NWCClientConfig{
		ID:      "test-nwc",
		URI:     fmt.Sprintf("nostr+walletconnect://%s?relay=wss%%3A%%2F%%2Frelay.example&secret=%s", walletPubKey.Hex(), clientSecret),
		Timeout: timeout, Transport: transport,
	})
	if err != nil {
		t.Fatalf("NewNWCClient: %v", err)
	}
	return client
}

func TestNWCEncryptedPayRequestAndVerifiedResponse(t *testing.T) {
	walletKeyer, err := NewNWCKeyer("2222222222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("wallet keyer: %v", err)
	}
	preimage := []byte("0123456789abcdef0123456789abcdef")
	hash := sha256.Sum256(preimage)
	transport := &protocolNWCTransport{t: t, walletKeyer: walletKeyer}
	transport.response = func(request nwcRequest) nwcResponse {
		return nwcResponse{
			ResultType: NWCMethodPayInvoice,
			Result: map[string]any{
				"preimage":  fmt.Sprintf("%x", preimage),
				"fees_paid": float64(17),
			},
		}
	}
	client := newProtocolNWCClient(t, transport, time.Second)
	request := PaymentRequest{
		Invoice: "lnbc-signed-invoice", PaymentHash: hash,
		AmountMSat: 5_000, MaxFeeMSat: 20, Deadline: time.Now().Add(time.Second),
	}
	result, err := client.PayInvoice(context.Background(), request)
	if err != nil {
		t.Fatalf("PayInvoice: %v", err)
	}
	if result.Status != PaymentStatusSucceeded || result.FeeMSat != 17 ||
		result.PaymentHash != hash || string(result.Preimage) != string(preimage) {
		t.Fatalf("payment result = %#v", result)
	}
	if transport.request.Method != NWCMethodPayInvoice {
		t.Fatalf("encrypted method = %q", transport.request.Method)
	}
	if transport.request.Params["invoice"] != request.Invoice {
		t.Fatalf("encrypted params = %#v", transport.request.Params)
	}
	if _, present := transport.request.Params["max_fee"]; present {
		t.Fatalf("non-standard fee field leaked into NIP-47 request: %#v", transport.request.Params)
	}
	if transport.publishedEvent.Kind != nostr.Kind(KindNWCRequest) {
		t.Fatalf("request kind = %d", transport.publishedEvent.Kind)
	}
	if !hasNostrTag(transport.publishedEvent.Tags, "encryption", "nip44_v2") {
		t.Fatalf("request tags = %#v", transport.publishedEvent.Tags)
	}
}

func TestNWCPayInvoiceToolKeepsRawResultAfterVerification(t *testing.T) {
	walletKeyer, err := NewNWCKeyer("2222222222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("wallet keyer: %v", err)
	}
	var preimage [32]byte
	copy(preimage[:], []byte("0123456789abcdef0123456789abcdef"))
	invoice := makeTestInvoice(t, &chaincfg.RegressionNetParams, preimage, 3_000, time.Now(), time.Hour)
	transport := &protocolNWCTransport{t: t, walletKeyer: walletKeyer}
	transport.response = func(request nwcRequest) nwcResponse {
		return nwcResponse{ResultType: NWCMethodPayInvoice, Result: map[string]any{
			"preimage": fmt.Sprintf("%x", preimage[:]), "fees_paid": float64(4), "wallet_field": "preserved",
		}}
	}
	client := newProtocolNWCClient(t, transport, time.Second)
	result, err := client.PayInvoiceTool(context.Background(), invoice, 0)
	if err != nil {
		t.Fatalf("PayInvoiceTool: %v", err)
	}
	if result["wallet_field"] != "preserved" || result["fees_paid"] != float64(4) {
		t.Fatalf("raw result = %#v", result)
	}
	if transport.request.Method != NWCMethodPayInvoice || transport.request.Params["invoice"] != invoice {
		t.Fatalf("encrypted tool payment request = %#v", transport.request)
	}
}

func TestNWCPublishedTimeoutBecomesInFlightAndUnsubscribes(t *testing.T) {
	walletKeyer, err := NewNWCKeyer("2222222222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("wallet keyer: %v", err)
	}
	transport := &protocolNWCTransport{t: t, walletKeyer: walletKeyer}
	client := newProtocolNWCClient(t, transport, 5*time.Millisecond)
	preimage := []byte("0123456789abcdef0123456789abcdef")
	hash := sha256.Sum256(preimage)
	result, err := client.PayInvoice(context.Background(), PaymentRequest{
		Invoice: "lnbc-timeout", PaymentHash: hash, AmountMSat: 1_000,
		MaxFeeMSat: 10, Deadline: time.Now().Add(time.Second),
	})
	if err != nil {
		t.Fatalf("PayInvoice: %v", err)
	}
	if result.Status != PaymentStatusInFlight || result.PaymentHash != hash {
		t.Fatalf("timeout result = %#v", result)
	}
	transport.mu.Lock()
	cleanups := transport.cleanups
	transport.mu.Unlock()
	if cleanups != 1 {
		t.Fatalf("subscription cleanup calls = %d, want 1", cleanups)
	}
}

func TestNWCLookupRequiresConclusiveOutgoingState(t *testing.T) {
	walletKeyer, err := NewNWCKeyer("2222222222222222222222222222222222222222222222222222222222222222")
	if err != nil {
		t.Fatalf("wallet keyer: %v", err)
	}
	hash := sha256.Sum256([]byte("0123456789abcdef0123456789abcdef"))
	transport := &protocolNWCTransport{t: t, walletKeyer: walletKeyer}
	transport.response = func(request nwcRequest) nwcResponse {
		if request.Method != NWCMethodLookupInvoice ||
			request.Params["payment_hash"] != fmt.Sprintf("%x", hash) {
			t.Fatalf("lookup encrypted request = %#v", request)
		}
		return nwcResponse{
			ResultType: NWCMethodLookupInvoice,
			Result:     map[string]any{"type": "incoming", "state": "settled"},
		}
	}
	client := newProtocolNWCClient(t, transport, time.Second)
	result, err := client.LookupPayment(context.Background(), PaymentLookup{PaymentHash: hash})
	if err != nil {
		t.Fatalf("LookupPayment: %v", err)
	}
	if result.Status != PaymentStatusInFlight {
		t.Fatalf("incoming lookup must remain ambiguous: %#v", result)
	}
}

func TestParseNWCURIValidatesWalletKeyAndRelays(t *testing.T) {
	walletKeyer, _ := NewNWCKeyer("2222222222222222222222222222222222222222222222222222222222222222")
	walletPubKey, _ := walletKeyer.GetPublicKey(context.Background())
	connection, err := ParseNWCURI("nostrwalletconnect://" + walletPubKey.Hex() + "?relay=wss%3A%2F%2Fa.example&relay=wss%3A%2F%2Fb.example&secret=11")
	if err != nil {
		t.Fatalf("ParseNWCURI: %v", err)
	}
	if connection.WalletPubKey != walletPubKey.Hex() || len(connection.Relays) != 2 || connection.Secret != "11" {
		t.Fatalf("connection = %#v", connection)
	}
	if _, err := ParseNWCURI("nwc://not-a-pubkey?relay=wss%3A%2F%2Fa.example"); err == nil {
		t.Fatal("expected invalid wallet public key error")
	}
}

func hasNostrTag(tags nostr.Tags, name, value string) bool {
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name && tag[1] == value {
			return true
		}
	}
	return false
}
