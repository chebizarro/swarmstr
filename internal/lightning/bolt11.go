package lightning

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightningnetwork/lnd/zpay32"
)

// BOLT11Decoder uses LND's mature, signature-verifying BOLT-11 implementation.
type BOLT11Decoder struct{}

func (BOLT11Decoder) Decode(encoded, network string) (DecodedInvoice, error) {
	params, canonical, err := lightningChainParams(network)
	if err != nil {
		return DecodedInvoice{}, err
	}
	invoice, err := zpay32.Decode(strings.TrimSpace(encoded), params)
	if err != nil {
		return DecodedInvoice{}, fmt.Errorf("%w: decode failed", ErrInvoiceInvalid)
	}
	if invoice.PaymentHash == nil {
		return DecodedInvoice{}, fmt.Errorf("%w: payment hash is missing", ErrInvoiceInvalid)
	}
	if invoice.MilliSat == nil || int64(*invoice.MilliSat) <= 0 {
		return DecodedInvoice{}, fmt.Errorf("%w: a non-zero amount is required", ErrInvoiceAmount)
	}
	created := invoice.Timestamp
	return DecodedInvoice{
		PaymentHash: *invoice.PaymentHash,
		AmountMSat:  int64(*invoice.MilliSat),
		CreatedAt:   created,
		ExpiresAt:   created.Add(invoice.Expiry()),
		Network:     canonical,
	}, nil
}

func decodeBOLT11AnyNetwork(encoded string) (DecodedInvoice, error) {
	for _, network := range []string{"mainnet", "testnet", "regtest", "signet"} {
		params, canonical, _ := lightningChainParams(network)
		invoice, err := zpay32.Decode(strings.TrimSpace(encoded), params)
		if err != nil {
			continue
		}
		if invoice.PaymentHash == nil {
			return DecodedInvoice{}, fmt.Errorf("%w: payment hash is missing", ErrInvoiceInvalid)
		}
		amount := int64(0)
		if invoice.MilliSat != nil {
			amount = int64(*invoice.MilliSat)
		}
		return DecodedInvoice{
			PaymentHash: *invoice.PaymentHash, AmountMSat: amount,
			CreatedAt: invoice.Timestamp, ExpiresAt: invoice.Timestamp.Add(invoice.Expiry()),
			Network: canonical,
		}, nil
	}
	return DecodedInvoice{}, fmt.Errorf("%w: decode failed", ErrInvoiceInvalid)
}

func lightningChainParams(network string) (*chaincfg.Params, string, error) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "mainnet":
		return &chaincfg.MainNetParams, "mainnet", nil
	case "testnet":
		return &chaincfg.TestNet3Params, "testnet", nil
	case "regtest":
		return &chaincfg.RegressionNetParams, "regtest", nil
	case "signet":
		return &chaincfg.SigNetParams, "signet", nil
	default:
		return nil, "", fmt.Errorf("%w: unsupported network %q", ErrInvoiceInvalid, network)
	}
}

// ValidateSucceededResult cryptographically binds the returned preimage to the
// invoice payment hash and checks amount/fee policy.
func ValidateSucceededResult(request PaymentRequest, result PaymentResult) error {
	if result.Status != PaymentStatusSucceeded {
		return fmt.Errorf("%w: expected succeeded status", ErrPaymentResultInvalid)
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if result.PaymentHash != request.PaymentHash {
		return fmt.Errorf("%w: wallet returned a different payment hash", ErrPaymentResultInvalid)
	}
	if result.AmountMSat != request.AmountMSat {
		return fmt.Errorf("%w: wallet returned a different amount", ErrPaymentResultInvalid)
	}
	if result.FeeMSat > request.MaxFeeMSat {
		return fmt.Errorf("%w: reported fee is above the requested maximum", ErrFeeLimit)
	}
	digest := sha256.Sum256(result.Preimage)
	if digest != request.PaymentHash {
		return fmt.Errorf("%w: preimage does not match payment hash", ErrPaymentResultInvalid)
	}
	return nil
}
