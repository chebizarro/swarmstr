package lightning

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/lnwire"
	"github.com/lightningnetwork/lnd/zpay32"
)

func makeTestInvoice(t *testing.T, network *chaincfg.Params, preimage [32]byte, amountMSat int64, created time.Time, expiry time.Duration) string {
	t.Helper()
	hash := sha256.Sum256(preimage[:])
	options := []func(*zpay32.Invoice){
		zpay32.Description("test invoice"),
		zpay32.Expiry(expiry),
	}
	if amountMSat > 0 {
		options = append(options, zpay32.Amount(lnwire.MilliSatoshi(amountMSat)))
	}
	invoice, err := zpay32.NewInvoice(network, hash, created, options...)
	if err != nil {
		t.Fatalf("NewInvoice: %v", err)
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
		t.Fatalf("Encode: %v", err)
	}
	return encoded
}

func TestBOLT11DecoderExtractsSignedPolicyFields(t *testing.T) {
	var preimage [32]byte
	copy(preimage[:], []byte("0123456789abcdef0123456789abcdef"))
	created := time.Unix(1_800_000_000, 0).UTC()
	encoded := makeTestInvoice(t, &chaincfg.MainNetParams, preimage, 42_000, created, 15*time.Minute)

	decoded, err := (BOLT11Decoder{}).Decode(encoded, "mainnet")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.PaymentHash != sha256.Sum256(preimage[:]) {
		t.Fatalf("payment hash mismatch: %x", decoded.PaymentHash)
	}
	if decoded.AmountMSat != 42_000 || !decoded.CreatedAt.Equal(created) ||
		!decoded.ExpiresAt.Equal(created.Add(15*time.Minute)) || decoded.Network != "mainnet" {
		t.Fatalf("decoded invoice = %#v", decoded)
	}
}

func TestBOLT11DecoderRejectsWrongNetworkAndAmountlessInvoice(t *testing.T) {
	var preimage [32]byte
	preimage[0] = 1
	created := time.Unix(1_800_000_000, 0).UTC()
	mainnet := makeTestInvoice(t, &chaincfg.MainNetParams, preimage, 1_000, created, time.Hour)
	if _, err := (BOLT11Decoder{}).Decode(mainnet, "testnet"); !errors.Is(err, ErrInvoiceInvalid) {
		t.Fatalf("wrong-network error = %v", err)
	}
	amountless := makeTestInvoice(t, &chaincfg.RegressionNetParams, preimage, 0, created, time.Hour)
	if _, err := (BOLT11Decoder{}).Decode(amountless, "regtest"); !errors.Is(err, ErrInvoiceAmount) {
		t.Fatalf("amountless error = %v", err)
	}
}

func TestValidateSucceededResultCryptographicallyChecksPreimage(t *testing.T) {
	preimage := []byte("0123456789abcdef0123456789abcdef")
	request := PaymentRequest{
		PaymentHash: sha256.Sum256(preimage), AmountMSat: 10_000, MaxFeeMSat: 500,
	}
	result := PaymentResult{
		Status: PaymentStatusSucceeded, PaymentHash: request.PaymentHash,
		Preimage: append([]byte(nil), preimage...), AmountMSat: 10_000, FeeMSat: 499,
	}
	if err := ValidateSucceededResult(request, result); err != nil {
		t.Fatalf("valid result: %v", err)
	}
	result.Preimage[0] ^= 0xff
	if err := ValidateSucceededResult(request, result); !errors.Is(err, ErrPaymentResultInvalid) {
		t.Fatalf("bad preimage error = %v", err)
	}
	result.Preimage = append([]byte(nil), preimage...)
	result.FeeMSat = 501
	if err := ValidateSucceededResult(request, result); !errors.Is(err, ErrFeeLimit) {
		t.Fatalf("fee error = %v", err)
	}
}
