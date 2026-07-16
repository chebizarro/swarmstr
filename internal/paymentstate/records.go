package paymentstate

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	L402TokenSchemaVersion      = 1
	PaymentAttemptSchemaVersion = 1
	L402TokenNamespace          = "l402-token-cache"
	PaymentAttemptNamespace     = "lightning-payment-attempts"
	SnapshotKey                 = "snapshot"
)

var ErrCorruptState = errors.New("payment persistence state is corrupt")

type PaymentAttemptState string

const (
	PaymentAttemptPrepared  PaymentAttemptState = "prepared"
	PaymentAttemptSubmitted PaymentAttemptState = "submitted"
	PaymentAttemptInFlight  PaymentAttemptState = "in_flight"
	PaymentAttemptSucceeded PaymentAttemptState = "succeeded"
	PaymentAttemptFailed    PaymentAttemptState = "failed"
)

type L402TokenRecord struct {
	ResourceKey          string              `json:"resource_key"`
	Origin               string              `json:"origin"`
	Scheme               string              `json:"scheme"`
	Macaroon             string              `json:"macaroon"`
	MacaroonSHA256       string              `json:"macaroon_sha256"`
	PreimageHex          string              `json:"preimage_hex"`
	PaymentHashHex       string              `json:"payment_hash_hex"`
	PayerID              string              `json:"payer_id"`
	CreatedAt            time.Time           `json:"created_at"`
	ExpiresAt            time.Time           `json:"expires_at"`
	LastUsedAt           time.Time           `json:"last_used_at"`
	PendingPaymentStatus PaymentAttemptState `json:"pending_payment_status,omitempty"`
}

type L402TokenSnapshot struct {
	SchemaVersion int               `json:"schema_version"`
	Records       []L402TokenRecord `json:"records"`
}

type L402TokenRepository interface {
	Load(context.Context) ([]L402TokenRecord, error)
	Put(context.Context, L402TokenRecord) error
	Delete(context.Context, string) error
	Clear(context.Context) error
}

type PaymentAttemptRecord struct {
	AttemptID        string              `json:"attempt_id"`
	PaymentHashHex   string              `json:"payment_hash_hex"`
	PayerID          string              `json:"payer_id"`
	State            PaymentAttemptState `json:"state"`
	AmountMSat       int64               `json:"amount_msat"`
	MaxFeeMSat       int64               `json:"max_fee_msat"`
	ReservedMSat     int64               `json:"reserved_msat"`
	ActualFeeMSat    int64               `json:"actual_fee_msat,omitempty"`
	PreimageHex      string              `json:"preimage_hex,omitempty"`
	FailureCode      string              `json:"failure_code,omitempty"`
	InvoiceExpiresAt time.Time           `json:"invoice_expires_at"`
	CreatedAt        time.Time           `json:"created_at"`
	SubmittedAt      *time.Time          `json:"submitted_at,omitempty"`
	LastLookupAt     *time.Time          `json:"last_lookup_at,omitempty"`
	CompletedAt      *time.Time          `json:"completed_at,omitempty"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

type PaymentAttemptSnapshot struct {
	SchemaVersion int                    `json:"schema_version"`
	Records       []PaymentAttemptRecord `json:"records"`
}

type PaymentAttemptRepository interface {
	Load(context.Context) ([]PaymentAttemptRecord, error)
	Get(context.Context, string) (PaymentAttemptRecord, bool, error)
	Put(context.Context, PaymentAttemptRecord) error
	Delete(context.Context, string) error
	Clear(context.Context) error
}

func (r L402TokenRecord) Validate() error {
	if !isLowerHex32(r.ResourceKey) {
		return fmt.Errorf("resource key must be a lowercase SHA-256 hex value")
	}
	if err := validateOrigin(r.Origin); err != nil {
		return err
	}
	if r.Scheme != "L402" && r.Scheme != "LSAT" {
		return fmt.Errorf("challenge scheme must be L402 or LSAT")
	}
	if strings.TrimSpace(r.Macaroon) == "" {
		return fmt.Errorf("macaroon is required")
	}
	if !isLowerHex32(r.MacaroonSHA256) {
		return fmt.Errorf("macaroon fingerprint must be a lowercase SHA-256 hex value")
	}
	if !isLowerHex32(r.PreimageHex) {
		return fmt.Errorf("preimage must be 32-byte lowercase hex")
	}
	if !isLowerHex32(r.PaymentHashHex) {
		return fmt.Errorf("payment hash must be 32-byte lowercase hex")
	}
	if strings.TrimSpace(r.PayerID) == "" {
		return fmt.Errorf("payer id is required")
	}
	if err := validateTimes(r.CreatedAt, r.ExpiresAt, r.LastUsedAt); err != nil {
		return err
	}
	switch r.PendingPaymentStatus {
	case "", PaymentAttemptPrepared, PaymentAttemptSubmitted, PaymentAttemptInFlight:
	default:
		return fmt.Errorf("invalid pending payment status %q", r.PendingPaymentStatus)
	}
	return nil
}

func (r PaymentAttemptRecord) Validate() error {
	if strings.TrimSpace(r.AttemptID) == "" {
		return fmt.Errorf("attempt id is required")
	}
	if !isLowerHex32(r.PaymentHashHex) {
		return fmt.Errorf("payment hash must be 32-byte lowercase hex")
	}
	if strings.TrimSpace(r.PayerID) == "" {
		return fmt.Errorf("payer id is required")
	}
	switch r.State {
	case PaymentAttemptPrepared, PaymentAttemptSubmitted, PaymentAttemptInFlight, PaymentAttemptSucceeded, PaymentAttemptFailed:
	default:
		return fmt.Errorf("invalid payment attempt state %q", r.State)
	}
	if r.AmountMSat <= 0 || r.MaxFeeMSat < 0 || r.ReservedMSat < r.AmountMSat || r.ActualFeeMSat < 0 {
		return fmt.Errorf("invalid payment amount or fee values")
	}
	if r.State == PaymentAttemptSucceeded && !isLowerHex32(r.PreimageHex) {
		return fmt.Errorf("succeeded payment requires a 32-byte lowercase preimage")
	}
	if r.PreimageHex != "" && !isLowerHex32(r.PreimageHex) {
		return fmt.Errorf("preimage must be 32-byte lowercase hex")
	}
	if r.State == PaymentAttemptFailed && strings.TrimSpace(r.FailureCode) == "" {
		return fmt.Errorf("failed payment requires a failure code")
	}
	if r.InvoiceExpiresAt.IsZero() || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return fmt.Errorf("invoice expiry and record timestamps are required")
	}
	if !r.InvoiceExpiresAt.After(r.CreatedAt) || r.UpdatedAt.Before(r.CreatedAt) {
		return fmt.Errorf("payment attempt timestamps are inconsistent")
	}
	return nil
}

func validateOrigin(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("origin must be canonical https://host[:port]")
	}
	return nil
}
func validateTimes(created, expires, lastUsed time.Time) error {
	if created.IsZero() || expires.IsZero() || lastUsed.IsZero() {
		return fmt.Errorf("token timestamps are required")
	}
	if !expires.After(created) || lastUsed.Before(created) {
		return fmt.Errorf("token timestamps are inconsistent")
	}
	return nil
}
func isLowerHex32(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
