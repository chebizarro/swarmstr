package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

const MaxOpaqueJSONBytes = 1 << 20

var (
	ErrProtectedBackendUnavailable = errors.New("protected secret backend unavailable")
	ErrOpaqueJSONCorrupt           = errors.New("opaque JSON record is corrupt")
)

type ProtectedSecretBackend interface {
	SecretBackend
	ProtectedAtRest() bool
}

type OpaqueJSONNamespace interface {
	Get(context.Context, string, any) (bool, error)
	Put(context.Context, string, any) error
	Delete(context.Context, string) error
	Quarantine(context.Context, string) error
}

type opaqueJSONNamespace struct {
	name    string
	backend ProtectedSecretBackend
	mu      sync.Mutex
}

type CorruptRecordError struct{ Namespace, Key string }

func (e *CorruptRecordError) Error() string {
	return fmt.Sprintf("opaque JSON record %s/%s is corrupt", e.Namespace, e.Key)
}
func (e *CorruptRecordError) Unwrap() error { return ErrOpaqueJSONCorrupt }

func (s *Store) OpenProtectedJSONNamespace(name string) (OpaqueJSONNamespace, error) {
	name = strings.TrimSpace(name)
	if !validOpaqueName(name) {
		return nil, fmt.Errorf("opaque namespace name is invalid")
	}
	s.mu.RLock()
	backend, ok := s.backend.(ProtectedSecretBackend)
	s.mu.RUnlock()
	if !ok || backend == nil || !backend.ProtectedAtRest() {
		return nil, ErrProtectedBackendUnavailable
	}
	return &opaqueJSONNamespace{name: name, backend: backend}, nil
}

func (n *opaqueJSONNamespace) Get(ctx context.Context, key string, out any) (bool, error) {
	if err := validateOpaqueArgs(ctx, key); err != nil {
		return false, err
	}
	if out == nil || reflect.ValueOf(out).Kind() != reflect.Pointer || reflect.ValueOf(out).IsNil() {
		return false, fmt.Errorf("opaque JSON destination must be a non-nil pointer")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	raw, found, err := n.backend.Get(n.backendKey(key))
	if err != nil {
		return false, fmt.Errorf("protected backend %s get failed: %w", n.backend.Name(), err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if len(raw) > MaxOpaqueJSONBytes || json.Unmarshal([]byte(raw), out) != nil {
		return false, &CorruptRecordError{Namespace: n.name, Key: key}
	}
	return true, nil
}

func (n *opaqueJSONNamespace) Put(ctx context.Context, key string, value any) error {
	if err := validateOpaqueArgs(ctx, key); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode opaque JSON record: %w", err)
	}
	if len(raw) > MaxOpaqueJSONBytes {
		return fmt.Errorf("opaque JSON record exceeds %d bytes", MaxOpaqueJSONBytes)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.backend.Set(n.backendKey(key), string(raw)); err != nil {
		return fmt.Errorf("protected backend %s set failed: %w", n.backend.Name(), err)
	}
	return ctx.Err()
}

func (n *opaqueJSONNamespace) Delete(ctx context.Context, key string) error {
	if err := validateOpaqueArgs(ctx, key); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.backend.Delete(n.backendKey(key)); err != nil {
		return fmt.Errorf("protected backend %s delete failed: %w", n.backend.Name(), err)
	}
	return ctx.Err()
}

func (n *opaqueJSONNamespace) Quarantine(ctx context.Context, key string) error {
	if err := validateOpaqueArgs(ctx, key); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	active := n.backendKey(key)
	raw, found, err := n.backend.Get(active)
	if err != nil {
		return fmt.Errorf("protected backend %s quarantine read failed: %w", n.backend.Name(), err)
	}
	if !found {
		return nil
	}
	quarantineKey := n.backendKey(fmt.Sprintf("quarantine.%s.%d", key, time.Now().UTC().UnixNano()))
	if err := n.backend.Set(quarantineKey, raw); err != nil {
		return fmt.Errorf("protected backend %s quarantine write failed: %w", n.backend.Name(), err)
	}
	if err := n.backend.Delete(active); err != nil {
		return fmt.Errorf("protected backend %s quarantine delete failed: %w", n.backend.Name(), err)
	}
	return ctx.Err()
}

func (n *opaqueJSONNamespace) backendKey(key string) string { return "opaque/v1/" + n.name + "/" + key }
func validateOpaqueArgs(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validOpaqueName(strings.TrimSpace(key)) {
		return fmt.Errorf("opaque record key is invalid")
	}
	return nil
}
func validOpaqueName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
