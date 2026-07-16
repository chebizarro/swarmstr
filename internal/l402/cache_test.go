package l402

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"metiq/internal/paymentstate"
)

func cacheToken(challenge Challenge, created time.Time, marker string) Token {
	return Token{
		Challenge:      challenge,
		PreimageHex:    strings.Repeat(marker, 64),
		PaymentHashHex: strings.Repeat("b", 64),
		PayerID:        "test-payer",
		CreatedAt:      created,
	}
}

func TestCacheExactResourceTTLAndCanonicalKey(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	repository := paymentstate.NewMemoryL402TokenRepository()
	cache, err := NewCache(context.Background(), repository, CacheOptions{TTL: time.Hour, MaxEntries: 4, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	challenge := Challenge{Scheme: "L402", Macaroon: "opaque", Invoice: "lnbc1"}
	record, err := cache.Put(context.Background(), "get", "https://EXAMPLE.com:443/path?secret=query", cacheToken(challenge, now, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(record.ResourceKey, "secret") || record.Origin != "https://example.com" {
		t.Fatalf("unsafe record: %#v", record)
	}
	got, ok, err := cache.Get(context.Background(), "GET", "https://example.com/path?secret=query")
	if err != nil || !ok || got.PreimageHex != strings.Repeat("a", 64) {
		t.Fatalf("cache get = %#v, %v, %v", got, ok, err)
	}
	if _, ok, _ := cache.Get(context.Background(), "GET", "https://example.com/other?secret=query"); ok {
		t.Fatal("token reused for a different resource")
	}
	now = now.Add(time.Hour + time.Second)
	if _, ok, err := cache.Get(context.Background(), "GET", "https://example.com/path?secret=query"); err != nil || ok {
		t.Fatalf("expired token = ok %v, err %v", ok, err)
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	cache, err := NewCache(context.Background(), paymentstate.NewMemoryL402TokenRepository(), CacheOptions{TTL: time.Hour, MaxEntries: 2, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	challenge := Challenge{Scheme: "L402", Macaroon: "m", Invoice: "i"}
	if _, err := cache.Put(context.Background(), "GET", "https://example.com/a", cacheToken(challenge, now, "a")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := cache.Put(context.Background(), "GET", "https://example.com/b", cacheToken(challenge, now, "b")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, ok, err := cache.Get(context.Background(), "GET", "https://example.com/a"); err != nil || !ok {
		t.Fatalf("refresh a: %v, %v", ok, err)
	}
	now = now.Add(time.Second)
	if _, err := cache.Put(context.Background(), "GET", "https://example.com/c", cacheToken(challenge, now, "c")); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := cache.Get(context.Background(), "GET", "https://example.com/b"); ok {
		t.Fatal("least-recently-used entry was not evicted")
	}
	if _, ok, _ := cache.Get(context.Background(), "GET", "https://example.com/a"); !ok {
		t.Fatal("recent entry was evicted")
	}
}

type failingTokenRepository struct {
	records map[string]paymentstate.L402TokenRecord
	putErr  error
}

func (r *failingTokenRepository) Load(context.Context) ([]paymentstate.L402TokenRecord, error) {
	var records []paymentstate.L402TokenRecord
	for _, record := range r.records {
		records = append(records, record)
	}
	return records, nil
}
func (r *failingTokenRepository) Put(_ context.Context, record paymentstate.L402TokenRecord) error {
	r.records[record.ResourceKey] = record
	return r.putErr
}
func (r *failingTokenRepository) Delete(_ context.Context, key string) error {
	delete(r.records, key)
	return nil
}
func (r *failingTokenRepository) Clear(context.Context) error {
	r.records = map[string]paymentstate.L402TokenRecord{}
	return nil
}

func TestCachePersistenceFailureRetainsInProcessToken(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	backendErr := errors.New("protected backend down")
	repository := &failingTokenRepository{records: map[string]paymentstate.L402TokenRecord{}, putErr: backendErr}
	cache, err := NewCache(context.Background(), repository, CacheOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	challenge := Challenge{Scheme: "LSAT", Macaroon: "m", Invoice: "i"}
	if _, err := cache.Put(context.Background(), "GET", "https://example.com/a", cacheToken(challenge, now, "a")); !errors.Is(err, backendErr) {
		t.Fatalf("put error = %v", err)
	}
	record, ok, err := cache.Get(context.Background(), "GET", "https://example.com/a")
	if !ok || record.Scheme != "LSAT" || !errors.Is(err, backendErr) {
		t.Fatalf("retained token = %#v, %v, %v", record, ok, err)
	}
}
