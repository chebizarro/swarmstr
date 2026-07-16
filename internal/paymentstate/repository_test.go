package paymentstate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func tokenRecord() L402TokenRecord {
	now := time.Now().UTC().Truncate(time.Second)
	return L402TokenRecord{
		ResourceKey: strings.Repeat("a", 64), Origin: "https://api.example.com", Scheme: "L402",
		Macaroon: "opaque-macaroon", MacaroonSHA256: strings.Repeat("b", 64),
		PreimageHex: strings.Repeat("c", 64), PaymentHashHex: strings.Repeat("d", 64), PayerID: "default",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastUsedAt: now,
	}
}

func attemptRecord() PaymentAttemptRecord {
	now := time.Now().UTC().Truncate(time.Second)
	return PaymentAttemptRecord{
		AttemptID: "attempt-1", PaymentHashHex: strings.Repeat("d", 64), PayerID: "default",
		State: PaymentAttemptSubmitted, AmountMSat: 1000, MaxFeeMSat: 20, ReservedMSat: 1020,
		InvoiceExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
}

func TestMemoryRepositoriesRoundTrip(t *testing.T) {
	ctx := context.Background()
	tokens := NewMemoryL402TokenRepository()
	token := tokenRecord()
	if err := tokens.Put(ctx, token); err != nil {
		t.Fatal(err)
	}
	loaded, err := tokens.Load(ctx)
	if err != nil || len(loaded) != 1 || loaded[0].Macaroon != token.Macaroon {
		t.Fatalf("token load = %#v, %v", loaded, err)
	}
	if err := tokens.Delete(ctx, token.ResourceKey); err != nil {
		t.Fatal(err)
	}
	if loaded, _ := tokens.Load(ctx); len(loaded) != 0 {
		t.Fatalf("token delete failed: %#v", loaded)
	}

	attempts := NewMemoryPaymentAttemptRepository()
	attempt := attemptRecord()
	if err := attempts.Put(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	got, found, err := attempts.Get(ctx, attempt.PaymentHashHex)
	if err != nil || !found || got.AttemptID != attempt.AttemptID {
		t.Fatalf("attempt get = %#v, %v, %v", got, found, err)
	}
	if err := attempts.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if loaded, _ := attempts.Load(ctx); len(loaded) != 0 {
		t.Fatalf("attempt clear failed: %#v", loaded)
	}
}

func TestRepositoriesRejectInvalidRecords(t *testing.T) {
	token := tokenRecord()
	token.ResourceKey = "not-a-hash"
	if err := NewMemoryL402TokenRepository().Put(context.Background(), token); err == nil {
		t.Fatal("expected token validation error")
	}
	attempt := attemptRecord()
	attempt.State = PaymentAttemptSucceeded
	if err := NewMemoryPaymentAttemptRepository().Put(context.Background(), attempt); err == nil {
		t.Fatal("expected succeeded preimage validation error")
	}
}

type fakeNamespace struct {
	mu          sync.Mutex
	raw         map[string][]byte
	quarantined bool
}

func (n *fakeNamespace) Get(ctx context.Context, key string, out any) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	raw, ok := n.raw[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, out)
}
func (n *fakeNamespace) Put(ctx context.Context, key string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.raw == nil {
		n.raw = map[string][]byte{}
	}
	n.raw[key] = raw
	return nil
}
func (n *fakeNamespace) Delete(ctx context.Context, key string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.raw, key)
	return ctx.Err()
}
func (n *fakeNamespace) Quarantine(ctx context.Context, key string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.quarantined = true
	delete(n.raw, key)
	return ctx.Err()
}

func TestSecretRepositoriesPersistVersionedSnapshots(t *testing.T) {
	ctx := context.Background()
	tokenNS := &fakeNamespace{raw: map[string][]byte{}}
	tokens := NewSecretL402TokenRepository(tokenNS)
	if err := tokens.Put(ctx, tokenRecord()); err != nil {
		t.Fatal(err)
	}
	var tokenSnapshot L402TokenSnapshot
	if err := json.Unmarshal(tokenNS.raw[SnapshotKey], &tokenSnapshot); err != nil {
		t.Fatal(err)
	}
	if tokenSnapshot.SchemaVersion != 1 || len(tokenSnapshot.Records) != 1 {
		t.Fatalf("token snapshot = %#v", tokenSnapshot)
	}

	attemptNS := &fakeNamespace{raw: map[string][]byte{}}
	attempts := NewSecretPaymentAttemptRepository(attemptNS)
	if err := attempts.Put(ctx, attemptRecord()); err != nil {
		t.Fatal(err)
	}
	var attemptSnapshot PaymentAttemptSnapshot
	if err := json.Unmarshal(attemptNS.raw[SnapshotKey], &attemptSnapshot); err != nil {
		t.Fatal(err)
	}
	if attemptSnapshot.SchemaVersion != 1 || len(attemptSnapshot.Records) != 1 {
		t.Fatalf("attempt snapshot = %#v", attemptSnapshot)
	}
}

func TestSecretRepositoryQuarantinesUnsupportedSchema(t *testing.T) {
	raw, _ := json.Marshal(L402TokenSnapshot{SchemaVersion: 99})
	ns := &fakeNamespace{raw: map[string][]byte{SnapshotKey: raw}}
	_, err := NewSecretL402TokenRepository(ns).Load(context.Background())
	if !errors.Is(err, ErrCorruptState) {
		t.Fatalf("expected corrupt state, got %v", err)
	}
	if !ns.quarantined {
		t.Fatal("corrupt snapshot was not quarantined")
	}
}

func TestPaymentAttemptNeverPersistsInvoiceOrFailureText(t *testing.T) {
	raw, err := json.Marshal(attemptRecord())
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"invoice\"", "failure_message", "wallet_error"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("record contains forbidden field %q: %s", forbidden, text)
		}
	}
}
