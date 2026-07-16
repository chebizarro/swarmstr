package paymentstate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"metiq/internal/secrets"
)

type SecretL402TokenRepository struct {
	mu        sync.Mutex
	namespace secrets.OpaqueJSONNamespace
}

func NewSecretL402TokenRepository(namespace secrets.OpaqueJSONNamespace) *SecretL402TokenRepository {
	return &SecretL402TokenRepository{namespace: namespace}
}
func (r *SecretL402TokenRepository) Load(ctx context.Context) ([]L402TokenRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked(ctx)
}
func (r *SecretL402TokenRepository) loadLocked(ctx context.Context) ([]L402TokenRecord, error) {
	var snapshot L402TokenSnapshot
	found, err := r.namespace.Get(ctx, SnapshotKey, &snapshot)
	if err != nil {
		if errors.Is(err, secrets.ErrOpaqueJSONCorrupt) {
			_ = r.namespace.Quarantine(ctx, SnapshotKey)
			return nil, ErrCorruptState
		}
		return nil, err
	}
	if !found {
		return nil, nil
	}
	if snapshot.SchemaVersion != L402TokenSchemaVersion {
		_ = r.namespace.Quarantine(ctx, SnapshotKey)
		return nil, fmt.Errorf("%w: unsupported L402 token schema", ErrCorruptState)
	}
	seen := map[string]struct{}{}
	for _, record := range snapshot.Records {
		if err := record.Validate(); err != nil {
			_ = r.namespace.Quarantine(ctx, SnapshotKey)
			return nil, fmt.Errorf("%w: invalid L402 token record", ErrCorruptState)
		}
		if _, ok := seen[record.ResourceKey]; ok {
			_ = r.namespace.Quarantine(ctx, SnapshotKey)
			return nil, fmt.Errorf("%w: duplicate L402 resource key", ErrCorruptState)
		}
		seen[record.ResourceKey] = struct{}{}
	}
	return append([]L402TokenRecord(nil), snapshot.Records...), nil
}
func (r *SecretL402TokenRepository) saveLocked(ctx context.Context, records []L402TokenRecord) error {
	sort.Slice(records, func(i, j int) bool { return records[i].ResourceKey < records[j].ResourceKey })
	return r.namespace.Put(ctx, SnapshotKey, L402TokenSnapshot{SchemaVersion: L402TokenSchemaVersion, Records: records})
}
func (r *SecretL402TokenRepository) Put(ctx context.Context, record L402TokenRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	records, err := r.loadLocked(ctx)
	if err != nil {
		return err
	}
	replaced := false
	for i := range records {
		if records[i].ResourceKey == record.ResourceKey {
			records[i] = record
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, record)
	}
	return r.saveLocked(ctx, records)
}
func (r *SecretL402TokenRepository) Delete(ctx context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	records, err := r.loadLocked(ctx)
	if err != nil {
		return err
	}
	out := records[:0]
	for _, record := range records {
		if record.ResourceKey != key {
			out = append(out, record)
		}
	}
	return r.saveLocked(ctx, out)
}
func (r *SecretL402TokenRepository) Clear(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked(ctx, nil)
}

type SecretPaymentAttemptRepository struct {
	mu        sync.Mutex
	namespace secrets.OpaqueJSONNamespace
}

func NewSecretPaymentAttemptRepository(namespace secrets.OpaqueJSONNamespace) *SecretPaymentAttemptRepository {
	return &SecretPaymentAttemptRepository{namespace: namespace}
}
func (r *SecretPaymentAttemptRepository) Load(ctx context.Context) ([]PaymentAttemptRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked(ctx)
}
func (r *SecretPaymentAttemptRepository) loadLocked(ctx context.Context) ([]PaymentAttemptRecord, error) {
	var snapshot PaymentAttemptSnapshot
	found, err := r.namespace.Get(ctx, SnapshotKey, &snapshot)
	if err != nil {
		if errors.Is(err, secrets.ErrOpaqueJSONCorrupt) {
			_ = r.namespace.Quarantine(ctx, SnapshotKey)
			return nil, ErrCorruptState
		}
		return nil, err
	}
	if !found {
		return nil, nil
	}
	if snapshot.SchemaVersion != PaymentAttemptSchemaVersion {
		_ = r.namespace.Quarantine(ctx, SnapshotKey)
		return nil, fmt.Errorf("%w: unsupported payment attempt schema", ErrCorruptState)
	}
	seen := map[string]struct{}{}
	for _, record := range snapshot.Records {
		if err := record.Validate(); err != nil {
			_ = r.namespace.Quarantine(ctx, SnapshotKey)
			return nil, fmt.Errorf("%w: invalid payment attempt record", ErrCorruptState)
		}
		if _, ok := seen[record.PaymentHashHex]; ok {
			_ = r.namespace.Quarantine(ctx, SnapshotKey)
			return nil, fmt.Errorf("%w: duplicate payment hash", ErrCorruptState)
		}
		seen[record.PaymentHashHex] = struct{}{}
	}
	return append([]PaymentAttemptRecord(nil), snapshot.Records...), nil
}
func (r *SecretPaymentAttemptRepository) saveLocked(ctx context.Context, records []PaymentAttemptRecord) error {
	sort.Slice(records, func(i, j int) bool { return records[i].PaymentHashHex < records[j].PaymentHashHex })
	return r.namespace.Put(ctx, SnapshotKey, PaymentAttemptSnapshot{SchemaVersion: PaymentAttemptSchemaVersion, Records: records})
}
func (r *SecretPaymentAttemptRepository) Get(ctx context.Context, hash string) (PaymentAttemptRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	records, err := r.loadLocked(ctx)
	if err != nil {
		return PaymentAttemptRecord{}, false, err
	}
	for _, record := range records {
		if record.PaymentHashHex == hash {
			return record, true, nil
		}
	}
	return PaymentAttemptRecord{}, false, nil
}
func (r *SecretPaymentAttemptRepository) Put(ctx context.Context, record PaymentAttemptRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	records, err := r.loadLocked(ctx)
	if err != nil {
		return err
	}
	replaced := false
	for i := range records {
		if records[i].PaymentHashHex == record.PaymentHashHex {
			records[i] = record
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, record)
	}
	return r.saveLocked(ctx, records)
}
func (r *SecretPaymentAttemptRepository) Delete(ctx context.Context, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	records, err := r.loadLocked(ctx)
	if err != nil {
		return err
	}
	out := records[:0]
	for _, record := range records {
		if record.PaymentHashHex != hash {
			out = append(out, record)
		}
	}
	return r.saveLocked(ctx, out)
}
func (r *SecretPaymentAttemptRepository) Clear(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked(ctx, nil)
}
