package lightning

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"metiq/internal/paymentstate"
)

// CoordinatorConfig supplies wallets, policy, and durable ambiguity records.
type CoordinatorConfig struct {
	Policy       CoordinatorPolicy
	Payers       map[string]InvoicePayer
	Decoder      InvoiceDecoder
	Attempts     paymentstate.PaymentAttemptRepository
	Clock        func() time.Time
	NewAttemptID func() string
}

type paymentFlight struct {
	done     chan struct{}
	joined   chan struct{}
	joinOnce sync.Once
	result   PaymentResult
	err      error
}

type budgetEntry struct {
	amount int64
	at     time.Time
}

// Coordinator validates invoices, serializes each payment hash, and prevents
// ambiguous attempts from being paid a second time.
type Coordinator struct {
	policy       CoordinatorPolicy
	payers       map[string]InvoicePayer
	decoder      InvoiceDecoder
	attempts     paymentstate.PaymentAttemptRepository
	clock        func() time.Time
	newAttemptID func() string

	flightsMu sync.Mutex
	flights   map[string]*paymentFlight

	budgetMu sync.Mutex
	budget   map[string]budgetEntry

	closeOnce sync.Once
	closeErr  error
}

// NewCoordinator validates configuration and restores rolling spend
// reservations from the pending-attempt repository.
func NewCoordinator(ctx context.Context, cfg CoordinatorConfig) (*Coordinator, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.Decoder == nil {
		cfg.Decoder = BOLT11Decoder{}
	}
	if cfg.Attempts == nil {
		cfg.Attempts = paymentstate.NewMemoryPaymentAttemptRepository()
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.NewAttemptID == nil {
		cfg.NewAttemptID = uuid.NewString
	}
	if strings.TrimSpace(cfg.Policy.Network) == "" {
		return nil, fmt.Errorf("lightning coordinator network is required")
	}
	if cfg.Policy.MaxInvoiceMSat <= 0 || cfg.Policy.MaxFeeMSat < 0 ||
		cfg.Policy.MaxSpendMSatPerHour <= 0 || cfg.Policy.PaymentTimeout <= 0 {
		return nil, fmt.Errorf("lightning coordinator policy limits must be positive")
	}
	if cfg.Policy.MaxInvoiceMSat > cfg.Policy.MaxSpendMSatPerHour-cfg.Policy.MaxFeeMSat {
		return nil, fmt.Errorf("lightning coordinator hourly budget is below one maximum payment")
	}
	payers := make(map[string]InvoicePayer, len(cfg.Payers))
	for id, payer := range cfg.Payers {
		key := normalizePayerID(id)
		if key == "" || payer == nil {
			return nil, fmt.Errorf("lightning coordinator contains an invalid payer")
		}
		if _, duplicate := payers[key]; duplicate {
			return nil, fmt.Errorf("lightning coordinator payer %q is duplicated", id)
		}
		payers[key] = payer
	}
	defaultPayer := normalizePayerID(cfg.Policy.PayerID)
	if defaultPayer == "" {
		return nil, fmt.Errorf("lightning coordinator payer id is required")
	}
	if payers[defaultPayer] == nil {
		return nil, fmt.Errorf("lightning coordinator payer %q is unavailable", cfg.Policy.PayerID)
	}

	c := &Coordinator{
		policy: cfg.Policy, payers: payers, decoder: cfg.Decoder,
		attempts: cfg.Attempts, clock: cfg.Clock, newAttemptID: cfg.NewAttemptID,
		flights: map[string]*paymentFlight{}, budget: map[string]budgetEntry{},
	}
	records, err := c.attempts.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load pending Lightning payments: %w", err)
	}
	c.restoreBudget(records)
	return c, nil
}

// PayInvoice pays with the configured default payer.
func (c *Coordinator) PayInvoice(ctx context.Context, invoice string) (PaymentResult, error) {
	return c.PayInvoiceWithPayer(ctx, c.policy.PayerID, invoice)
}

// Pay is a concise compatibility alias for L402 clients.
func (c *Coordinator) Pay(ctx context.Context, invoice string) (PaymentResult, error) {
	return c.PayInvoice(ctx, invoice)
}

// PayInvoiceWithPayer selects a configured payer before any attempt starts.
// Existing pending records always reconcile through their original payer.
func (c *Coordinator) PayInvoiceWithPayer(ctx context.Context, payerID, invoice string) (PaymentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	decoded, err := c.decoder.Decode(invoice, c.policy.Network)
	if err != nil {
		return PaymentResult{}, err
	}
	now := c.clock()
	if decoded.AmountMSat <= 0 || decoded.AmountMSat > c.policy.MaxInvoiceMSat {
		return PaymentResult{}, fmt.Errorf("%w: amount exceeds configured maximum", ErrInvoiceAmount)
	}
	if decoded.ExpiresAt.IsZero() || !now.Before(decoded.ExpiresAt) {
		return PaymentResult{}, ErrInvoiceExpired
	}
	payerKey := normalizePayerID(payerID)
	if c.payers[payerKey] == nil {
		return PaymentResult{}, fmt.Errorf("lightning payer %q is unavailable", payerID)
	}
	hashKey := hex.EncodeToString(decoded.PaymentHash[:])

	c.flightsMu.Lock()
	if existing := c.flights[hashKey]; existing != nil {
		existing.joinOnce.Do(func() { close(existing.joined) })
		c.flightsMu.Unlock()
		select {
		case <-existing.done:
			return clonePaymentResult(existing.result), existing.err
		case <-ctx.Done():
			return PaymentResult{}, ctx.Err()
		}
	}
	flight := &paymentFlight{done: make(chan struct{}), joined: make(chan struct{})}
	c.flights[hashKey] = flight
	c.flightsMu.Unlock()

	flight.result, flight.err = c.payOne(ctx, payerKey, invoice, decoded)
	close(flight.done)
	c.flightsMu.Lock()
	delete(c.flights, hashKey)
	c.flightsMu.Unlock()
	return clonePaymentResult(flight.result), flight.err
}

func (c *Coordinator) payOne(ctx context.Context, requestedPayer, invoice string, decoded DecodedInvoice) (PaymentResult, error) {
	hashKey := hex.EncodeToString(decoded.PaymentHash[:])
	if record, found, err := c.attempts.Get(ctx, hashKey); err != nil {
		return PaymentResult{}, fmt.Errorf("load Lightning payment attempt: %w", err)
	} else if found {
		result, resolved, reconcileErr := c.reconcileExisting(ctx, record, decoded)
		if reconcileErr != nil || resolved {
			return result, reconcileErr
		}
	}

	reserved, err := checkedAdd(decoded.AmountMSat, c.policy.MaxFeeMSat)
	if err != nil {
		return PaymentResult{}, err
	}
	if err := c.reserveBudget(hashKey, reserved, c.clock()); err != nil {
		return PaymentResult{}, err
	}
	attemptID := strings.TrimSpace(c.newAttemptID())
	if attemptID == "" {
		c.releaseBudget(hashKey)
		return PaymentResult{}, errors.New("lightning attempt id generator returned an empty value")
	}
	now := c.clock()
	record := paymentstate.PaymentAttemptRecord{
		AttemptID: attemptID, PaymentHashHex: hashKey, PayerID: requestedPayer,
		State: paymentstate.PaymentAttemptPrepared, AmountMSat: decoded.AmountMSat,
		MaxFeeMSat: c.policy.MaxFeeMSat, ReservedMSat: reserved,
		InvoiceExpiresAt: decoded.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	if err := c.attempts.Put(ctx, record); err != nil {
		c.releaseBudget(hashKey)
		return PaymentResult{}, fmt.Errorf("persist prepared Lightning payment: %w", err)
	}

	payer := c.payers[requestedPayer]
	deadline := now.Add(c.policy.PaymentTimeout)
	if existingDeadline, ok := ctx.Deadline(); ok && existingDeadline.Before(deadline) {
		deadline = existingDeadline
	}
	request := PaymentRequest{
		Invoice: invoice, PaymentHash: decoded.PaymentHash, AmountMSat: decoded.AmountMSat,
		MaxFeeMSat: c.policy.MaxFeeMSat, Deadline: deadline, AttemptID: attemptID,
	}
	submittedAt := c.clock()
	record.State = paymentstate.PaymentAttemptSubmitted
	record.SubmittedAt = &submittedAt
	record.UpdatedAt = submittedAt
	if err := c.attempts.Put(ctx, record); err != nil {
		deleteErr := c.attempts.Delete(context.WithoutCancel(ctx), hashKey)
		if deleteErr == nil {
			c.releaseBudget(hashKey)
		}
		return PaymentResult{}, errors.Join(
			fmt.Errorf("persist submitted Lightning payment: %w", err), deleteErr,
		)
	}

	payCtx, cancel := context.WithDeadline(ctx, deadline)
	result, payErr := payer.PayInvoice(payCtx, request)
	cancel()
	result = normalizePaymentResult(result, request)
	if payErr != nil && result.Status == PaymentStatusInFlight {
		if _, persistErr := c.finishAttempt(context.WithoutCancel(ctx), record, request, result); persistErr != nil {
			return result, errors.Join(payErr, persistErr)
		}
		return result, payErr
	}
	if payErr != nil {
		record.State = paymentstate.PaymentAttemptFailed
		record.FailureCode = "payer_error"
		completed := c.clock()
		record.CompletedAt, record.UpdatedAt = &completed, completed
		if persistErr := c.attempts.Put(context.WithoutCancel(ctx), record); persistErr != nil {
			return PaymentResult{}, errors.Join(payErr, fmt.Errorf("persist failed Lightning payment: %w", persistErr))
		}
		c.releaseBudget(hashKey)
		return PaymentResult{}, payErr
	}
	return c.finishAttempt(context.WithoutCancel(ctx), record, request, result)
}

func (c *Coordinator) reconcileExisting(ctx context.Context, record paymentstate.PaymentAttemptRecord, decoded DecodedInvoice) (PaymentResult, bool, error) {
	if record.AmountMSat != decoded.AmountMSat {
		return PaymentResult{}, true, fmt.Errorf("%w: existing payment hash has a different amount", ErrInvoiceInvalid)
	}
	request := PaymentRequest{
		PaymentHash: decoded.PaymentHash, AmountMSat: decoded.AmountMSat,
		MaxFeeMSat: record.MaxFeeMSat, AttemptID: record.AttemptID,
	}
	switch record.State {
	case paymentstate.PaymentAttemptSucceeded:
		result, err := resultFromRecord(record)
		if err != nil {
			return PaymentResult{}, true, err
		}
		if err := ValidateSucceededResult(request, result); err != nil {
			return PaymentResult{}, true, err
		}
		return result, true, nil
	case paymentstate.PaymentAttemptFailed:
		return PaymentResult{}, false, nil
	case paymentstate.PaymentAttemptPrepared, paymentstate.PaymentAttemptSubmitted, paymentstate.PaymentAttemptInFlight:
		payer := c.payers[normalizePayerID(record.PayerID)]
		if payer == nil {
			return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: decoded.PaymentHash}, true,
				fmt.Errorf("%w: original payer is unavailable", ErrPaymentPending)
		}
		result, err := payer.LookupPayment(ctx, PaymentLookup{
			PaymentHash: decoded.PaymentHash, AttemptID: record.AttemptID,
		})
		lookupAt := c.clock()
		record.LastLookupAt, record.UpdatedAt = &lookupAt, lookupAt
		if err != nil {
			record.State = paymentstate.PaymentAttemptInFlight
			_ = c.attempts.Put(context.WithoutCancel(ctx), record)
			return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: decoded.PaymentHash}, true, err
		}
		result = normalizePaymentResult(result, request)
		switch result.Status {
		case PaymentStatusNotFound:
			if err := c.attempts.Delete(context.WithoutCancel(ctx), record.PaymentHashHex); err != nil {
				return PaymentResult{}, true, fmt.Errorf("delete absent Lightning payment: %w", err)
			}
			c.releaseBudget(record.PaymentHashHex)
			return PaymentResult{}, false, nil
		case PaymentStatusFailed:
			if _, err := c.finishAttempt(context.WithoutCancel(ctx), record, request, result); err != nil {
				return PaymentResult{}, true, err
			}
			return PaymentResult{}, false, nil
		default:
			finished, finishErr := c.finishAttempt(context.WithoutCancel(ctx), record, request, result)
			return finished, true, finishErr
		}
	default:
		return PaymentResult{}, true, fmt.Errorf("unknown persisted Lightning payment state %q", record.State)
	}
}

func (c *Coordinator) finishAttempt(ctx context.Context, record paymentstate.PaymentAttemptRecord, request PaymentRequest, result PaymentResult) (PaymentResult, error) {
	now := c.clock()
	switch result.Status {
	case PaymentStatusSucceeded:
		if err := ValidateSucceededResult(request, result); err != nil {
			record.State = paymentstate.PaymentAttemptInFlight
			record.UpdatedAt = now
			if persistErr := c.attempts.Put(ctx, record); persistErr != nil {
				return PaymentResult{}, errors.Join(err, persistErr)
			}
			return PaymentResult{}, err
		}
		record.State = paymentstate.PaymentAttemptSucceeded
		record.ActualFeeMSat = result.FeeMSat
		record.PreimageHex = hex.EncodeToString(result.Preimage)
		record.CompletedAt, record.UpdatedAt = &now, now
		if err := c.attempts.Put(ctx, record); err != nil {
			return PaymentResult{}, fmt.Errorf("persist succeeded Lightning payment: %w", err)
		}
		c.commitBudget(record.PaymentHashHex, result.AmountMSat+result.FeeMSat, now)
		return result, nil
	case PaymentStatusFailed:
		if err := result.Validate(); err != nil {
			return PaymentResult{}, err
		}
		record.State = paymentstate.PaymentAttemptFailed
		record.FailureCode = result.FailureCode
		record.CompletedAt, record.UpdatedAt = &now, now
		if err := c.attempts.Put(ctx, record); err != nil {
			return PaymentResult{}, fmt.Errorf("persist failed Lightning payment: %w", err)
		}
		c.releaseBudget(record.PaymentHashHex)
		return result, nil
	case PaymentStatusInFlight:
		if err := result.Validate(); err != nil {
			return PaymentResult{}, err
		}
		record.State = paymentstate.PaymentAttemptInFlight
		record.UpdatedAt = now
		if err := c.attempts.Put(ctx, record); err != nil {
			return PaymentResult{}, fmt.Errorf("persist in-flight Lightning payment: %w", err)
		}
		return result, nil
	case PaymentStatusNotFound:
		return result, nil
	default:
		return PaymentResult{}, fmt.Errorf("%w: unknown payer status %q", ErrPaymentResultInvalid, result.Status)
	}
}

func (c *Coordinator) restoreBudget(records []paymentstate.PaymentAttemptRecord) {
	now := c.clock()
	c.budgetMu.Lock()
	defer c.budgetMu.Unlock()
	for _, record := range records {
		var amount int64
		at := record.UpdatedAt
		switch record.State {
		case paymentstate.PaymentAttemptPrepared, paymentstate.PaymentAttemptSubmitted, paymentstate.PaymentAttemptInFlight:
			if now.Before(record.InvoiceExpiresAt) {
				amount = record.ReservedMSat
			}
		case paymentstate.PaymentAttemptSucceeded:
			amount = record.AmountMSat + record.ActualFeeMSat
			if record.CompletedAt != nil {
				at = *record.CompletedAt
			}
		}
		if amount > 0 && at.After(now.Add(-time.Hour)) {
			c.budget[record.PaymentHashHex] = budgetEntry{amount: amount, at: at}
		}
	}
}

func (c *Coordinator) reserveBudget(hash string, amount int64, now time.Time) error {
	c.budgetMu.Lock()
	defer c.budgetMu.Unlock()
	c.pruneBudgetLocked(now)
	var total int64
	for _, entry := range c.budget {
		var err error
		total, err = checkedAdd(total, entry.amount)
		if err != nil {
			return ErrHourlyBudget
		}
	}
	if amount > c.policy.MaxSpendMSatPerHour-total {
		return ErrHourlyBudget
	}
	c.budget[hash] = budgetEntry{amount: amount, at: now}
	return nil
}

func (c *Coordinator) commitBudget(hash string, amount int64, now time.Time) {
	c.budgetMu.Lock()
	c.budget[hash] = budgetEntry{amount: amount, at: now}
	c.pruneBudgetLocked(now)
	c.budgetMu.Unlock()
}

func (c *Coordinator) releaseBudget(hash string) {
	c.budgetMu.Lock()
	delete(c.budget, hash)
	c.budgetMu.Unlock()
}

func (c *Coordinator) pruneBudgetLocked(now time.Time) {
	cutoff := now.Add(-time.Hour)
	for hash, entry := range c.budget {
		if !entry.at.After(cutoff) {
			delete(c.budget, hash)
		}
	}
}

// Close closes every distinct configured payer.
func (c *Coordinator) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		seen := map[string]struct{}{}
		for _, payer := range c.payers {
			key := normalizePayerID(payer.ID())
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			c.closeErr = errors.Join(c.closeErr, payer.Close())
		}
	})
	return c.closeErr
}

func resultFromRecord(record paymentstate.PaymentAttemptRecord) (PaymentResult, error) {
	preimage, err := hex.DecodeString(record.PreimageHex)
	if err != nil || len(preimage) != 32 {
		return PaymentResult{}, fmt.Errorf("%w: persisted preimage is invalid", ErrPaymentResultInvalid)
	}
	hash, err := decodeHash(record.PaymentHashHex)
	if err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{
		Status: PaymentStatusSucceeded, PaymentHash: hash, Preimage: preimage,
		AmountMSat: record.AmountMSat, FeeMSat: record.ActualFeeMSat,
	}, nil
}

func normalizePaymentResult(result PaymentResult, request PaymentRequest) PaymentResult {
	if result.PaymentHash == ([32]byte{}) {
		result.PaymentHash = request.PaymentHash
	}
	if result.Status == PaymentStatusSucceeded && result.AmountMSat == 0 {
		result.AmountMSat = request.AmountMSat
	}
	return result
}

func decodeHash(value string) ([32]byte, error) {
	var out [32]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return out, fmt.Errorf("%w: persisted payment hash is invalid", ErrPaymentResultInvalid)
	}
	copy(out[:], decoded)
	return out, nil
}

func clonePaymentResult(result PaymentResult) PaymentResult {
	result.Preimage = append([]byte(nil), result.Preimage...)
	return result
}

func normalizePayerID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func checkedAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > int64(^uint64(0)>>1)-b {
		return 0, fmt.Errorf("%w: amount overflow", ErrInvoiceAmount)
	}
	return a + b, nil
}
