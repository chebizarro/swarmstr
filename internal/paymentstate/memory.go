package paymentstate

import (
	"context"
	"sort"
	"strings"
	"sync"
)

type MemoryL402TokenRepository struct {
	mu      sync.RWMutex
	records map[string]L402TokenRecord
}

func NewMemoryL402TokenRepository() *MemoryL402TokenRepository {
	return &MemoryL402TokenRepository{records: map[string]L402TokenRecord{}}
}
func (r *MemoryL402TokenRepository) Load(ctx context.Context) ([]L402TokenRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]L402TokenRecord, 0, len(r.records))
	for _, record := range r.records {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceKey < out[j].ResourceKey })
	return out, nil
}
func (r *MemoryL402TokenRepository) Put(ctx context.Context, record L402TokenRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[record.ResourceKey] = record
	return nil
}
func (r *MemoryL402TokenRepository) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, strings.TrimSpace(key))
	return nil
}
func (r *MemoryL402TokenRepository) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = map[string]L402TokenRecord{}
	return nil
}

type MemoryPaymentAttemptRepository struct {
	mu      sync.RWMutex
	records map[string]PaymentAttemptRecord
}

func NewMemoryPaymentAttemptRepository() *MemoryPaymentAttemptRepository {
	return &MemoryPaymentAttemptRepository{records: map[string]PaymentAttemptRecord{}}
}
func (r *MemoryPaymentAttemptRepository) Load(ctx context.Context) ([]PaymentAttemptRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PaymentAttemptRecord, 0, len(r.records))
	for _, record := range r.records {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PaymentHashHex < out[j].PaymentHashHex })
	return out, nil
}
func (r *MemoryPaymentAttemptRepository) Get(ctx context.Context, hash string) (PaymentAttemptRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return PaymentAttemptRecord{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.records[strings.TrimSpace(hash)]
	return record, ok, nil
}
func (r *MemoryPaymentAttemptRepository) Put(ctx context.Context, record PaymentAttemptRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[record.PaymentHashHex] = record
	return nil
}
func (r *MemoryPaymentAttemptRepository) Delete(ctx context.Context, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, strings.TrimSpace(hash))
	return nil
}
func (r *MemoryPaymentAttemptRepository) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = map[string]PaymentAttemptRecord{}
	return nil
}
