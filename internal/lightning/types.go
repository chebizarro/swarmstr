package lightning

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// PaymentStatus is the closed set of states returned by every invoice payer.
type PaymentStatus string

const (
	PaymentStatusSucceeded PaymentStatus = "succeeded"
	PaymentStatusFailed    PaymentStatus = "failed"
	PaymentStatusInFlight  PaymentStatus = "in_flight"
	PaymentStatusNotFound  PaymentStatus = "not_found"
)

var (
	ErrInvoiceInvalid       = errors.New("lightning invoice is invalid")
	ErrInvoiceExpired       = errors.New("lightning invoice is expired")
	ErrInvoiceAmount        = errors.New("lightning invoice amount is not allowed")
	ErrFeeLimit             = errors.New("lightning payment fee exceeds limit")
	ErrHourlyBudget         = errors.New("lightning hourly spend budget exceeded")
	ErrPaymentResultInvalid = errors.New("lightning payment result is invalid")
	ErrPaymentPending       = errors.New("lightning payment remains in flight")
)

// PaymentRequest is the validated request passed from Coordinator to a wallet.
// Invoice and secret payment material must never be included in errors or logs.
type PaymentRequest struct {
	Invoice     string
	PaymentHash [32]byte
	AmountMSat  int64
	MaxFeeMSat  int64
	Deadline    time.Time
	AttemptID   string
}

// PaymentLookup identifies an earlier, possibly ambiguous, wallet attempt.
type PaymentLookup struct {
	PaymentHash [32]byte
	AttemptID   string
}

// PaymentResult is a wallet-independent payment state.
type PaymentResult struct {
	Status         PaymentStatus
	PaymentHash    [32]byte
	Preimage       []byte
	AmountMSat     int64
	FeeMSat        int64
	FailureCode    string
	FailureMessage string
}

// Validate checks the closed result shape. Succeeded preimages are verified
// separately against the expected payment hash by ValidateSucceededResult.
func (r PaymentResult) Validate() error {
	switch r.Status {
	case PaymentStatusSucceeded:
		if len(r.Preimage) != 32 {
			return fmt.Errorf("%w: succeeded payment requires a 32-byte preimage", ErrPaymentResultInvalid)
		}
		if r.AmountMSat <= 0 || r.FeeMSat < 0 {
			return fmt.Errorf("%w: succeeded payment has invalid amount or fee", ErrPaymentResultInvalid)
		}
	case PaymentStatusFailed:
		if r.FailureCode == "" {
			return fmt.Errorf("%w: failed payment requires a failure code", ErrPaymentResultInvalid)
		}
		if len(r.Preimage) != 0 {
			return fmt.Errorf("%w: failed payment contains a preimage", ErrPaymentResultInvalid)
		}
	case PaymentStatusInFlight, PaymentStatusNotFound:
		if len(r.Preimage) != 0 {
			return fmt.Errorf("%w: non-settled payment contains a preimage", ErrPaymentResultInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown status %q", ErrPaymentResultInvalid, r.Status)
	}
	return nil
}

// InvoicePayer is implemented by concrete NWC and LND wallets.
type InvoicePayer interface {
	ID() string
	PayInvoice(context.Context, PaymentRequest) (PaymentResult, error)
	LookupPayment(context.Context, PaymentLookup) (PaymentResult, error)
	Close() error
}

// DecodedInvoice contains the policy-relevant BOLT-11 fields.
type DecodedInvoice struct {
	PaymentHash [32]byte
	AmountMSat  int64
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Network     string
}

// InvoiceDecoder validates and decodes a BOLT-11 invoice for one network.
type InvoiceDecoder interface {
	Decode(invoice, network string) (DecodedInvoice, error)
}

// InvoiceDecoderFunc adapts a function to InvoiceDecoder.
type InvoiceDecoderFunc func(invoice, network string) (DecodedInvoice, error)

func (f InvoiceDecoderFunc) Decode(invoice, network string) (DecodedInvoice, error) {
	return f(invoice, network)
}

// CoordinatorPolicy contains irreversible-spend limits enforced before a
// wallet is invoked.
type CoordinatorPolicy struct {
	Network             string
	PayerID             string
	MaxInvoiceMSat      int64
	MaxFeeMSat          int64
	MaxSpendMSatPerHour int64
	PaymentTimeout      time.Duration
}

// PaymentCoordinator is the high-level interface consumed by L402.
type PaymentCoordinator interface {
	PayInvoice(context.Context, string) (PaymentResult, error)
}
