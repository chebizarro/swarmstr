package zap

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/zpay32"
)

type zapValidationFixture struct {
	request    nostr.Event
	receipt    nostr.Event
	opts       ReceiveOpts
	providerSK nostr.SecretKey
	providerPK string
}

func makeZapValidationFixture(t *testing.T, requestAmount, invoiceAmount int64, descriptionHashOverride *[32]byte) zapValidationFixture {
	t.Helper()
	providerSK := nostr.Generate()
	senderSK := nostr.Generate()
	recipientSK := nostr.Generate()
	providerPK := testPublicKey(t, providerSK)
	senderPK := testPublicKey(t, senderSK)
	recipientPK := testPublicKey(t, recipientSK)
	lnurl, err := encodeLNURL("https://wallet.example/.well-known/lnurlp/alice")
	if err != nil {
		t.Fatal(err)
	}

	request := nostr.Event{
		Kind:      9734,
		CreatedAt: nostr.Timestamp(1_700_000_000),
		Content:   "great post",
		Tags: nostr.Tags{
			{"relays", "wss://relay.example"},
			{"amount", "21000"},
			{"lnurl", lnurl},
			{"p", recipientPK},
			{"e", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			{"k", "1"},
		},
	}
	request.Tags[1][1] = formatInt(requestAmount)
	signTestEvent(t, senderSK, &request)
	descriptionBytes, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	description := string(descriptionBytes)
	descriptionHash := sha256.Sum256(descriptionBytes)
	if descriptionHashOverride != nil {
		descriptionHash = *descriptionHashOverride
	}
	invoice := makeZapInvoice(t, descriptionHash, invoiceAmount)

	receipt := nostr.Event{
		Kind:      9735,
		CreatedAt: nostr.Timestamp(1_700_000_001),
		Tags: nostr.Tags{
			{"p", recipientPK},
			{"P", senderPK},
			{"e", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			{"bolt11", invoice},
			{"description", description},
		},
	}
	signTestEvent(t, providerSK, &receipt)
	return zapValidationFixture{
		request: request,
		receipt: receipt,
		opts: ReceiveOpts{
			RecipientPubkeyHex: recipientPK,
			ProviderPubkeyHex:  providerPK,
			RecipientLNURL:     lnurl,
		},
		providerSK: providerSK,
		providerPK: providerPK,
	}
}

func TestValidateZapReceiptValid(t *testing.T) {
	fixture := makeZapValidationFixture(t, 21000, 21000, nil)
	receipt, err := validateZapReceipt(fixture.receipt, fixture.opts, fixture.providerPK)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.AmountMsat != 21000 || receipt.SenderPubkey != fixture.request.PubKey.Hex() || receipt.Comment != "great post" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestValidateZapReceiptRejectsInvalidReceiptSignature(t *testing.T) {
	fixture := makeZapValidationFixture(t, 21000, 21000, nil)
	fixture.receipt.Content = "tampered"
	if _, err := validateZapReceipt(fixture.receipt, fixture.opts, fixture.providerPK); err == nil {
		t.Fatal("expected invalid receipt signature to be rejected")
	}
}

func TestValidateZapReceiptRejectsWrongProvider(t *testing.T) {
	fixture := makeZapValidationFixture(t, 21000, 21000, nil)
	wrongProvider := testPublicKey(t, nostr.Generate())
	if _, err := validateZapReceipt(fixture.receipt, fixture.opts, wrongProvider); err == nil {
		t.Fatal("expected wrong provider to be rejected")
	}
}

func TestValidateZapReceiptRejectsInvalidZapRequestSignature(t *testing.T) {
	fixture := makeZapValidationFixture(t, 21000, 21000, nil)
	request := fixture.request
	request.Content = "tampered"
	description, _ := json.Marshal(request)
	replaceTestTag(fixture.receipt.Tags, "description", string(description))
	hash := sha256.Sum256(description)
	replaceTestTag(fixture.receipt.Tags, "bolt11", makeZapInvoice(t, hash, 21000))
	signTestEvent(t, fixture.providerSK, &fixture.receipt)
	if _, err := validateZapReceipt(fixture.receipt, fixture.opts, fixture.providerPK); err == nil {
		t.Fatal("expected invalid zap request signature to be rejected")
	}
}

func TestValidateZapReceiptRejectsDescriptionHashMismatch(t *testing.T) {
	wrongHash := sha256.Sum256([]byte("wrong description"))
	fixture := makeZapValidationFixture(t, 21000, 21000, &wrongHash)
	if _, err := validateZapReceipt(fixture.receipt, fixture.opts, fixture.providerPK); err == nil {
		t.Fatal("expected description hash mismatch to be rejected")
	}
}

func TestValidateZapReceiptRejectsAmountMismatch(t *testing.T) {
	fixture := makeZapValidationFixture(t, 21000, 22000, nil)
	if _, err := validateZapReceipt(fixture.receipt, fixture.opts, fixture.providerPK); err == nil {
		t.Fatal("expected amount mismatch to be rejected")
	}
}

func TestValidateZapReceiptRejectsRecipientAndEventMismatch(t *testing.T) {
	for _, tagName := range []string{"p", "e"} {
		t.Run(tagName, func(t *testing.T) {
			fixture := makeZapValidationFixture(t, 21000, 21000, nil)
			replaceTestTag(fixture.receipt.Tags, tagName, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			signTestEvent(t, fixture.providerSK, &fixture.receipt)
			if _, err := validateZapReceipt(fixture.receipt, fixture.opts, fixture.providerPK); err == nil {
				t.Fatalf("expected %s mismatch to be rejected", tagName)
			}
		})
	}
}

func TestAcceptZapReceiptDeduplicatesEventID(t *testing.T) {
	fixture := makeZapValidationFixture(t, 21000, 21000, nil)
	seen := make(map[string]struct{})
	if _, accepted := acceptZapReceipt(fixture.receipt, fixture.opts, fixture.providerPK, seen); !accepted {
		t.Fatal("expected first receipt to be accepted")
	}
	if _, accepted := acceptZapReceipt(fixture.receipt, fixture.opts, fixture.providerPK, seen); accepted {
		t.Fatal("expected duplicate receipt to be rejected")
	}
}

func TestValidateBIP340Pubkey(t *testing.T) {
	if _, err := validateBIP340Pubkey(testProviderPubkey); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, invalid := range []string{"", "abc", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"} {
		if _, err := validateBIP340Pubkey(invalid); err == nil {
			t.Fatalf("invalid key %q accepted", invalid)
		}
	}
}

func testPublicKey(t *testing.T, secret nostr.SecretKey) string {
	t.Helper()
	signer := keyer.NewPlainKeySigner(secret)
	pubkey, err := signer.GetPublicKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return pubkey.Hex()
}

func signTestEvent(t *testing.T, secret nostr.SecretKey, event *nostr.Event) {
	t.Helper()
	signer := keyer.NewPlainKeySigner(secret)
	if err := signer.SignEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
}

func makeZapInvoice(t *testing.T, descriptionHash [32]byte, amountMsat int64) string {
	t.Helper()
	paymentHash := sha256.Sum256([]byte("payment hash material"))
	invoice, err := zpay32.NewInvoice(
		&chaincfg.RegressionNetParams,
		paymentHash,
		time.Unix(1_700_000_000, 0),
		zpay32.Amount(lnwire.MilliSatoshi(amountMsat)),
		zpay32.DescriptionHash(descriptionHash),
	)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, _ := btcec.PrivKeyFromBytes([]byte{
		1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
		17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
	})
	encoded, err := invoice.Encode(zpay32.MessageSigner{SignCompact: func(message []byte) ([]byte, error) {
		digest := sha256.Sum256(message)
		return ecdsa.SignCompact(privateKey, digest[:], true), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func replaceTestTag(tags nostr.Tags, name, value string) {
	for i := range tags {
		if len(tags[i]) >= 2 && tags[i][0] == name {
			tags[i][1] = value
			return
		}
	}
}

func formatInt(value int64) string {
	return fmt.Sprintf("%d", value)
}
