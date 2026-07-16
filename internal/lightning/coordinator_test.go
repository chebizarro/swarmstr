package lightning

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"metiq/internal/paymentstate"
)

type testPayer struct {
	id string

	mu          sync.Mutex
	payCalls    int
	lookupCalls int
	payFunc     func(context.Context, PaymentRequest) (PaymentResult, error)
	lookupFunc  func(context.Context, PaymentLookup) (PaymentResult, error)
	closed      bool
}

func (p *testPayer) ID() string { return p.id }
func (p *testPayer) PayInvoice(ctx context.Context, request PaymentRequest) (PaymentResult, error) {
	p.mu.Lock()
	p.payCalls++
	fn := p.payFunc
	p.mu.Unlock()
	return fn(ctx, request)
}
func (p *testPayer) LookupPayment(ctx context.Context, lookup PaymentLookup) (PaymentResult, error) {
	p.mu.Lock()
	p.lookupCalls++
	fn := p.lookupFunc
	p.mu.Unlock()
	return fn(ctx, lookup)
}
func (p *testPayer) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return nil
}
func (p *testPayer) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.payCalls, p.lookupCalls
}

func newTestCoordinator(t *testing.T, now time.Time, decoder InvoiceDecoder, payer InvoicePayer, attempts paymentstate.PaymentAttemptRepository, budget int64) *Coordinator {
	t.Helper()
	maxInvoice := int64(10_000)
	if budget-100 < maxInvoice {
		maxInvoice = budget - 100
	}
	coordinator, err := NewCoordinator(context.Background(), CoordinatorConfig{
		Policy: CoordinatorPolicy{
			Network: "regtest", PayerID: payer.ID(), MaxInvoiceMSat: maxInvoice,
			MaxFeeMSat: 100, MaxSpendMSatPerHour: budget, PaymentTimeout: time.Minute,
		},
		Payers: map[string]InvoicePayer{payer.ID(): payer}, Decoder: decoder,
		Attempts: attempts, Clock: func() time.Time { return now },
		NewAttemptID: func() string { return "attempt-1" },
	})
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	return coordinator
}

func invoiceDecoderFor(now time.Time, values map[string]struct {
	preimage []byte
	amount   int64
	expiry   time.Time
}) InvoiceDecoder {
	return InvoiceDecoderFunc(func(invoice, network string) (DecodedInvoice, error) {
		value, ok := values[invoice]
		if !ok {
			return DecodedInvoice{}, ErrInvoiceInvalid
		}
		return DecodedInvoice{
			PaymentHash: sha256.Sum256(value.preimage), AmountMSat: value.amount,
			CreatedAt: now.Add(-time.Minute), ExpiresAt: value.expiry, Network: network,
		}, nil
	})
}

func TestCoordinatorRejectsAmountAndExpiryBeforeCallingPayer(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	payer := &testPayer{id: "wallet", payFunc: func(context.Context, PaymentRequest) (PaymentResult, error) {
		t.Fatal("payer must not be called")
		return PaymentResult{}, nil
	}, lookupFunc: func(context.Context, PaymentLookup) (PaymentResult, error) {
		t.Fatal("lookup must not be called")
		return PaymentResult{}, nil
	}}
	decoder := invoiceDecoderFor(now, map[string]struct {
		preimage []byte
		amount   int64
		expiry   time.Time
	}{
		"too-large": {preimage: []byte("0123456789abcdef0123456789abcdef"), amount: 10_001, expiry: now.Add(time.Hour)},
		"expired":   {preimage: []byte("abcdef0123456789abcdef0123456789"), amount: 100, expiry: now},
	})
	coordinator := newTestCoordinator(t, now, decoder, payer, nil, 20_000)
	if _, err := coordinator.PayInvoice(context.Background(), "too-large"); !errors.Is(err, ErrInvoiceAmount) {
		t.Fatalf("large invoice error = %v", err)
	}
	if _, err := coordinator.PayInvoice(context.Background(), "expired"); !errors.Is(err, ErrInvoiceExpired) {
		t.Fatalf("expired invoice error = %v", err)
	}
}

func TestCoordinatorPaymentHashSingleFlight(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	preimage := []byte("0123456789abcdef0123456789abcdef")
	started := make(chan struct{})
	release := make(chan struct{})
	payer := &testPayer{id: "wallet"}
	payer.payFunc = func(_ context.Context, request PaymentRequest) (PaymentResult, error) {
		close(started)
		<-release
		return PaymentResult{
			Status: PaymentStatusSucceeded, PaymentHash: request.PaymentHash,
			Preimage: preimage, AmountMSat: request.AmountMSat, FeeMSat: 3,
		}, nil
	}
	payer.lookupFunc = func(context.Context, PaymentLookup) (PaymentResult, error) {
		return PaymentResult{Status: PaymentStatusNotFound}, nil
	}
	decoder := invoiceDecoderFor(now, map[string]struct {
		preimage []byte
		amount   int64
		expiry   time.Time
	}{"invoice": {preimage: preimage, amount: 1_000, expiry: now.Add(time.Hour)}})
	coordinator := newTestCoordinator(t, now, decoder, payer, nil, 10_000)

	type outcome struct {
		result PaymentResult
		err    error
	}
	first := make(chan outcome, 1)
	second := make(chan outcome, 1)
	go func() {
		result, err := coordinator.PayInvoice(context.Background(), "invoice")
		first <- outcome{result: result, err: err}
	}()
	<-started
	coordinator.flightsMu.Lock()
	flight := coordinator.flights[sha256Hex(preimage)]
	coordinator.flightsMu.Unlock()
	if flight == nil {
		t.Fatal("single-flight entry was not installed")
	}
	go func() {
		result, err := coordinator.PayInvoice(context.Background(), "invoice")
		second <- outcome{result: result, err: err}
	}()
	<-flight.joined
	close(release)

	for index, channel := range []<-chan outcome{first, second} {
		got := <-channel
		if got.err != nil || got.result.Status != PaymentStatusSucceeded {
			t.Fatalf("outcome %d = %#v, %v", index, got.result, got.err)
		}
	}
	if payCalls, _ := payer.counts(); payCalls != 1 {
		t.Fatalf("PayInvoice calls = %d, want 1", payCalls)
	}
}

func TestCoordinatorPersistsAmbiguityAndReconcilesWithoutRepaying(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	preimage := []byte("0123456789abcdef0123456789abcdef")
	repository := paymentstate.NewMemoryPaymentAttemptRepository()
	payer := &testPayer{id: "wallet"}
	payer.payFunc = func(_ context.Context, request PaymentRequest) (PaymentResult, error) {
		return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: request.PaymentHash}, nil
	}
	payer.lookupFunc = func(_ context.Context, lookup PaymentLookup) (PaymentResult, error) {
		return PaymentResult{
			Status: PaymentStatusSucceeded, PaymentHash: lookup.PaymentHash,
			Preimage: preimage, AmountMSat: 2_000, FeeMSat: 9,
		}, nil
	}
	decoder := invoiceDecoderFor(now, map[string]struct {
		preimage []byte
		amount   int64
		expiry   time.Time
	}{"invoice": {preimage: preimage, amount: 2_000, expiry: now.Add(time.Hour)}})
	coordinator := newTestCoordinator(t, now, decoder, payer, repository, 10_000)

	first, err := coordinator.PayInvoice(context.Background(), "invoice")
	if err != nil || first.Status != PaymentStatusInFlight {
		t.Fatalf("first payment = %#v, %v", first, err)
	}
	record, found, err := repository.Get(context.Background(), sha256Hex(preimage))
	if err != nil || !found || record.State != paymentstate.PaymentAttemptInFlight {
		t.Fatalf("pending record = %#v, found=%v, err=%v", record, found, err)
	}
	second, err := coordinator.PayInvoice(context.Background(), "invoice")
	if err != nil || second.Status != PaymentStatusSucceeded {
		t.Fatalf("reconciled payment = %#v, %v", second, err)
	}
	if payCalls, lookupCalls := payer.counts(); payCalls != 1 || lookupCalls != 1 {
		t.Fatalf("calls pay=%d lookup=%d", payCalls, lookupCalls)
	}
	record, found, _ = repository.Get(context.Background(), sha256Hex(preimage))
	if !found || record.State != paymentstate.PaymentAttemptSucceeded ||
		record.PreimageHex == "" || record.ActualFeeMSat != 9 {
		t.Fatalf("succeeded record = %#v", record)
	}
}

func TestCoordinatorHourlyBudgetUsesReservationsAndActualSpend(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	firstPreimage := []byte("0123456789abcdef0123456789abcdef")
	secondPreimage := []byte("abcdef0123456789abcdef0123456789")
	payer := &testPayer{id: "wallet"}
	payer.payFunc = func(_ context.Context, request PaymentRequest) (PaymentResult, error) {
		preimage := firstPreimage
		if request.PaymentHash == sha256.Sum256(secondPreimage) {
			preimage = secondPreimage
		}
		return PaymentResult{
			Status: PaymentStatusSucceeded, PaymentHash: request.PaymentHash,
			Preimage: preimage, AmountMSat: request.AmountMSat, FeeMSat: 50,
		}, nil
	}
	payer.lookupFunc = func(context.Context, PaymentLookup) (PaymentResult, error) {
		return PaymentResult{Status: PaymentStatusNotFound}, nil
	}
	decoder := invoiceDecoderFor(now, map[string]struct {
		preimage []byte
		amount   int64
		expiry   time.Time
	}{
		"first":  {preimage: firstPreimage, amount: 600, expiry: now.Add(time.Hour)},
		"second": {preimage: secondPreimage, amount: 400, expiry: now.Add(time.Hour)},
	})
	coordinator := newTestCoordinator(t, now, decoder, payer, nil, 1_000)
	if _, err := coordinator.PayInvoice(context.Background(), "first"); err != nil {
		t.Fatalf("first payment: %v", err)
	}
	if _, err := coordinator.PayInvoice(context.Background(), "second"); !errors.Is(err, ErrHourlyBudget) {
		t.Fatalf("second payment error = %v", err)
	}
	if payCalls, _ := payer.counts(); payCalls != 1 {
		t.Fatalf("payer calls = %d, want 1", payCalls)
	}
}

func TestCoordinatorInvalidPreimageRemainsPending(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	preimage := []byte("0123456789abcdef0123456789abcdef")
	payer := &testPayer{id: "wallet"}
	payer.payFunc = func(_ context.Context, request PaymentRequest) (PaymentResult, error) {
		return PaymentResult{
			Status: PaymentStatusSucceeded, PaymentHash: request.PaymentHash,
			Preimage:   []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
			AmountMSat: request.AmountMSat, FeeMSat: 1,
		}, nil
	}
	payer.lookupFunc = func(_ context.Context, lookup PaymentLookup) (PaymentResult, error) {
		return PaymentResult{Status: PaymentStatusInFlight, PaymentHash: lookup.PaymentHash}, nil
	}
	decoder := invoiceDecoderFor(now, map[string]struct {
		preimage []byte
		amount   int64
		expiry   time.Time
	}{"invoice": {preimage: preimage, amount: 500, expiry: now.Add(time.Hour)}})
	coordinator := newTestCoordinator(t, now, decoder, payer, nil, 5_000)

	if _, err := coordinator.PayInvoice(context.Background(), "invoice"); !errors.Is(err, ErrPaymentResultInvalid) {
		t.Fatalf("invalid preimage error = %v", err)
	}
	result, err := coordinator.PayInvoice(context.Background(), "invoice")
	if err != nil || result.Status != PaymentStatusInFlight {
		t.Fatalf("second call = %#v, %v", result, err)
	}
	if payCalls, lookupCalls := payer.counts(); payCalls != 1 || lookupCalls != 1 {
		t.Fatalf("calls pay=%d lookup=%d", payCalls, lookupCalls)
	}
}

func sha256Hex(preimage []byte) string {
	hash := sha256.Sum256(preimage)
	return fmt.Sprintf("%x", hash)
}
