package methods

import (
	"encoding/json"
	"fmt"
	"strings"

	secretspkg "metiq/internal/secrets"
)

type SecretsStoreListRequest struct{}

type SecretsStoreSetRequest struct {
	Name         string                      `json:"name"`
	Value        string                      `json:"value"`
	Kind         secretspkg.StoredSecretKind `json:"kind"`
	AllowedHosts []string                    `json:"allowedHosts,omitempty"`
}

type SecretsStoreDeleteRequest struct {
	Name string `json:"name"`
}

type SecretsStoreSecretEntry struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	ScopeKind    string   `json:"scopeKind"`
	ScopeID      string   `json:"scopeId"`
	CreatedAtMS  int64    `json:"createdAtMs"`
	UpdatedAtMS  int64    `json:"updatedAtMs"`
	UpdatedBy    string   `json:"updatedBy,omitempty"`
	AllowedHosts []string `json:"allowedHosts,omitempty"`
}

type SecretsStoreEnvEntry struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	ScopeKind   string `json:"scopeKind"`
	ScopeID     string `json:"scopeId"`
	CreatedAtMS int64  `json:"createdAtMs"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
	UpdatedBy   string `json:"updatedBy,omitempty"`
	Value       string `json:"value"`
}

func (r SecretsStoreListRequest) Normalize() (SecretsStoreListRequest, error) { return r, nil }

func (r SecretsStoreSetRequest) Normalize() (SecretsStoreSetRequest, error) {
	r.Name = strings.TrimSpace(r.Name)
	if err := secretspkg.ValidateStoredSecretName(r.Name, true); err != nil {
		return r, fmt.Errorf("invalid secrets.store.set params: name is invalid")
	}
	if len(r.Value) > secretspkg.MaxStoredSecretValue {
		return r, fmt.Errorf("invalid secrets.store.set params: value exceeds maximum size")
	}
	if r.Kind != secretspkg.StoredSecretKindSecret && r.Kind != secretspkg.StoredSecretKindEnv {
		return r, fmt.Errorf("invalid secrets.store.set params: kind must be secret or env")
	}
	if len(r.AllowedHosts) > 128 {
		return r, fmt.Errorf("invalid secrets.store.set params: allowedHosts exceeds 128 entries")
	}
	seen := make(map[string]struct{}, len(r.AllowedHosts))
	for i, host := range r.AllowedHosts {
		host = strings.TrimSpace(host)
		if host == "" {
			return r, fmt.Errorf("invalid secrets.store.set params: allowedHosts entries must not be empty")
		}
		if _, ok := seen[host]; ok {
			return r, fmt.Errorf("invalid secrets.store.set params: duplicate allowed host")
		}
		seen[host] = struct{}{}
		r.AllowedHosts[i] = host
	}
	if r.Kind == secretspkg.StoredSecretKindEnv && len(r.AllowedHosts) > 0 {
		return r, fmt.Errorf("invalid secrets.store.set params: allowedHosts is only valid for secret entries")
	}
	return r, nil
}

func (r SecretsStoreDeleteRequest) Normalize() (SecretsStoreDeleteRequest, error) {
	r.Name = strings.TrimSpace(r.Name)
	if err := secretspkg.ValidateStoredSecretName(r.Name, true); err != nil {
		return r, fmt.Errorf("invalid secrets.store.delete params: name is invalid")
	}
	return r, nil
}

func DecodeSecretsStoreListParams(params json.RawMessage) (SecretsStoreListRequest, error) {
	return decodeMethodParams[SecretsStoreListRequest](params)
}

func DecodeSecretsStoreSetParams(params json.RawMessage) (SecretsStoreSetRequest, error) {
	return decodeMethodParams[SecretsStoreSetRequest](params)
}

func DecodeSecretsStoreDeleteParams(params json.RawMessage) (SecretsStoreDeleteRequest, error) {
	return decodeMethodParams[SecretsStoreDeleteRequest](params)
}
