package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	gatewayStoreProvider = "gateway-store"
	gatewayStoreCatalog  = "gateway-secret-store/catalog"
	MaxStoredSecretValue = 64 << 10
)

var storedSecretNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
var githubSetupHandlePattern = regexp.MustCompile(`^github-setup-[a-f0-9]{32}$`)

type StoredSecretKind string

const (
	StoredSecretKindSecret StoredSecretKind = "secret"
	StoredSecretKindEnv    StoredSecretKind = "env"
)

// StoredSecretMetadata is safe to serialize: it contains lifecycle metadata but
// never the stored value.
type StoredSecretMetadata struct {
	Name         string           `json:"name"`
	Kind         StoredSecretKind `json:"kind"`
	CreatedAtMS  int64            `json:"createdAtMs"`
	UpdatedAtMS  int64            `json:"updatedAtMs"`
	UpdatedBy    string           `json:"updatedBy,omitempty"`
	AllowedHosts []string         `json:"allowedHosts,omitempty"`
}

type storedSecretCatalog struct {
	Version int                    `json:"version"`
	Entries []StoredSecretMetadata `json:"entries"`
}

// StoredSecretRecord combines safe metadata with a resolved value for callers
// that explicitly need it. Value is always excluded from JSON and logs.
type StoredSecretRecord struct {
	StoredSecretMetadata
	Value string `json:"-"`
}

func StoredSecretRef(name string) SecretRef {
	return SecretRef{Source: SecretRefStore, Provider: gatewayStoreProvider, ID: strings.TrimSpace(name)}
}

// StoredSecretName reports the gateway-store entry name a ref points at, if
// any. Only such refs can be rotated via RotateStoredSecret.
func StoredSecretName(ref SecretRef) (string, bool) {
	if ref.Source != SecretRefStore || strings.TrimSpace(ref.Provider) != gatewayStoreProvider {
		return "", false
	}
	name := strings.TrimSpace(ref.ID)
	return name, name != ""
}

func ValidateStoredSecretName(name string, allowInternal bool) error {
	name = strings.TrimSpace(name)
	if storedSecretNamePattern.MatchString(name) || (allowInternal && githubSetupHandlePattern.MatchString(name)) {
		return nil
	}
	return errors.New("invalid secret name")
}

func (s *Store) protectedGatewayBackendLocked() (ProtectedSecretBackend, error) {
	backend, ok := s.backend.(ProtectedSecretBackend)
	if !ok || backend == nil || !backend.ProtectedAtRest() {
		return nil, ErrProtectedBackendUnavailable
	}
	return backend, nil
}

func loadStoredSecretCatalog(backend ProtectedSecretBackend) (storedSecretCatalog, error) {
	raw, found, err := backend.Get("opaque/v1/" + gatewayStoreCatalog)
	if err != nil {
		return storedSecretCatalog{}, fmt.Errorf("read protected secret catalog: %w", err)
	}
	if !found {
		return storedSecretCatalog{Version: 1, Entries: []StoredSecretMetadata{}}, nil
	}
	if len(raw) > MaxOpaqueJSONBytes {
		return storedSecretCatalog{}, ErrOpaqueJSONCorrupt
	}
	var catalog storedSecretCatalog
	if err := json.Unmarshal([]byte(raw), &catalog); err != nil || catalog.Version != 1 {
		return storedSecretCatalog{}, ErrOpaqueJSONCorrupt
	}
	return catalog, nil
}

func saveStoredSecretCatalog(backend ProtectedSecretBackend, catalog storedSecretCatalog) error {
	catalog.Version = 1
	sort.Slice(catalog.Entries, func(i, j int) bool { return catalog.Entries[i].Name < catalog.Entries[j].Name })
	raw, err := json.Marshal(catalog)
	if err != nil {
		return fmt.Errorf("encode protected secret catalog: %w", err)
	}
	if len(raw) > MaxOpaqueJSONBytes {
		return fmt.Errorf("protected secret catalog exceeds %d bytes", MaxOpaqueJSONBytes)
	}
	if err := backend.Set("opaque/v1/"+gatewayStoreCatalog, string(raw)); err != nil {
		return fmt.Errorf("write protected secret catalog: %w", err)
	}
	return nil
}

func storedSecretValueKey(name string) string { return gatewayStoreProvider + "/" + name }

// ListStoredSecrets returns catalog entries, resolving values only for env-kind
// projections. Internal one-time GitHub setup handles are deliberately hidden.
func (s *Store) ListStoredSecrets() ([]StoredSecretRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	backend, err := s.protectedGatewayBackendLocked()
	if err != nil {
		return nil, err
	}
	catalog, err := loadStoredSecretCatalog(backend)
	if err != nil {
		return nil, err
	}
	out := make([]StoredSecretRecord, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		if githubSetupHandlePattern.MatchString(entry.Name) {
			continue
		}
		record := StoredSecretRecord{StoredSecretMetadata: entry}
		record.AllowedHosts = append([]string(nil), entry.AllowedHosts...)
		if entry.Kind == StoredSecretKindEnv {
			value, found, err := backend.Get(storedSecretValueKey(entry.Name))
			if err != nil {
				return nil, fmt.Errorf("read protected env entry %q: %w", entry.Name, err)
			}
			if !found {
				return nil, fmt.Errorf("protected env entry %q is missing", entry.Name)
			}
			record.Value = value
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SetStoredSecret atomically updates a protected value and its metadata catalog.
// It never falls back to metiq-managed plaintext storage.
func (s *Store) SetStoredSecret(name, value string, kind StoredSecretKind, allowedHosts []string, updatedBy string) (StoredSecretMetadata, error) {
	name = strings.TrimSpace(name)
	if err := ValidateStoredSecretName(name, true); err != nil {
		return StoredSecretMetadata{}, err
	}
	if len(value) > MaxStoredSecretValue {
		return StoredSecretMetadata{}, fmt.Errorf("secret value exceeds %d bytes", MaxStoredSecretValue)
	}
	if kind != StoredSecretKindSecret && kind != StoredSecretKindEnv {
		return StoredSecretMetadata{}, errors.New("secret kind must be secret or env")
	}
	allowedHosts = normalizeAllowedHosts(allowedHosts)
	if kind == StoredSecretKindEnv && len(allowedHosts) > 0 {
		return StoredSecretMetadata{}, errors.New("allowedHosts is only valid for secret entries")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	backend, err := s.protectedGatewayBackendLocked()
	if err != nil {
		return StoredSecretMetadata{}, err
	}
	catalog, err := loadStoredSecretCatalog(backend)
	if err != nil {
		return StoredSecretMetadata{}, err
	}
	now := time.Now().UnixMilli()
	metadata := StoredSecretMetadata{Name: name, Kind: kind, CreatedAtMS: now, UpdatedAtMS: now, UpdatedBy: strings.TrimSpace(updatedBy), AllowedHosts: allowedHosts}
	if index := catalog.index(name); index >= 0 {
		metadata.CreatedAtMS = catalog.Entries[index].CreatedAtMS
		catalog.Entries[index] = metadata
	} else {
		catalog.Entries = append(catalog.Entries, metadata)
	}
	if err := commitStoredSecret(backend, catalog, name, value); err != nil {
		return StoredSecretMetadata{}, err
	}
	return metadata, nil
}

// RotateStoredSecret atomically replaces the value of an existing entry,
// preserving its kind and allowed hosts. It is the write-back path for
// credentials a service rotates itself (for example OAuth refresh tokens).
func (s *Store) RotateStoredSecret(name, value, updatedBy string) (StoredSecretMetadata, error) {
	name = strings.TrimSpace(name)
	if err := ValidateStoredSecretName(name, false); err != nil {
		return StoredSecretMetadata{}, err
	}
	if value == "" || len(value) > MaxStoredSecretValue {
		return StoredSecretMetadata{}, fmt.Errorf("secret value must be 1..%d bytes", MaxStoredSecretValue)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	backend, err := s.protectedGatewayBackendLocked()
	if err != nil {
		return StoredSecretMetadata{}, err
	}
	catalog, err := loadStoredSecretCatalog(backend)
	if err != nil {
		return StoredSecretMetadata{}, err
	}
	index := catalog.index(name)
	if index < 0 {
		return StoredSecretMetadata{}, fmt.Errorf("stored secret %q does not exist", name)
	}
	metadata := catalog.Entries[index]
	metadata.UpdatedAtMS = time.Now().UnixMilli()
	metadata.UpdatedBy = strings.TrimSpace(updatedBy)
	catalog.Entries[index] = metadata
	if err := commitStoredSecret(backend, catalog, name, value); err != nil {
		return StoredSecretMetadata{}, err
	}
	return metadata, nil
}

func (c storedSecretCatalog) index(name string) int {
	for i := range c.Entries {
		if c.Entries[i].Name == name {
			return i
		}
	}
	return -1
}

// commitStoredSecret writes value and the updated catalog, restoring the prior
// value if the catalog write fails. Callers hold s.mu.
func commitStoredSecret(backend ProtectedSecretBackend, catalog storedSecretCatalog, name, value string) error {
	key := storedSecretValueKey(name)
	oldValue, oldFound, err := backend.Get(key)
	if err != nil {
		return fmt.Errorf("read prior protected secret: %w", err)
	}
	if err := backend.Set(key, value); err != nil {
		return fmt.Errorf("write protected secret: %w", err)
	}
	if err := saveStoredSecretCatalog(backend, catalog); err != nil {
		if oldFound {
			_ = backend.Set(key, oldValue)
		} else {
			_ = backend.Delete(key)
		}
		return err
	}
	return nil
}

func (s *Store) DeleteStoredSecret(name string) (bool, error) {
	name = strings.TrimSpace(name)
	if err := ValidateStoredSecretName(name, true); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	backend, err := s.protectedGatewayBackendLocked()
	if err != nil {
		return false, err
	}
	catalog, err := loadStoredSecretCatalog(backend)
	if err != nil {
		return false, err
	}
	index := -1
	for i := range catalog.Entries {
		if catalog.Entries[i].Name == name {
			index = i
			break
		}
	}
	if index < 0 {
		return false, nil
	}
	key := storedSecretValueKey(name)
	oldValue, oldFound, err := backend.Get(key)
	if err != nil {
		return false, fmt.Errorf("read protected secret before delete: %w", err)
	}
	if err := backend.Delete(key); err != nil {
		return false, fmt.Errorf("delete protected secret: %w", err)
	}
	catalog.Entries = append(catalog.Entries[:index], catalog.Entries[index+1:]...)
	if err := saveStoredSecretCatalog(backend, catalog); err != nil {
		if oldFound {
			_ = backend.Set(key, oldValue)
		}
		return false, err
	}
	return true, nil
}

func normalizeAllowedHosts(hosts []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}
