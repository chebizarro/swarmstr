package secrets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

const vaultMaxResponseBytes = 1 << 20

// VaultConfig configures a HashiCorp Vault KV backend.
type VaultConfig struct {
	Address           string
	Token             string
	Namespace         string
	Mount             string
	Prefix            string
	KVVersion         int
	Timeout           time.Duration
	AllowInsecureHTTP bool
	HTTPClient        *http.Client
}

// VaultBackend stores named values in a HashiCorp Vault KV v1/v2 mount.
type VaultBackend struct {
	base      *url.URL
	token     string
	namespace string
	mount     string
	prefix    string
	kvVersion int
	client    *http.Client
}

var _ ListableSecretBackend = (*VaultBackend)(nil)
var _ ProtectedSecretBackend = (*VaultBackend)(nil)

// NewVaultBackend validates cfg and returns a fail-closed Vault backend.
func NewVaultBackend(cfg VaultConfig) (*VaultBackend, error) {
	base, err := url.Parse(strings.TrimSpace(cfg.Address))
	if err != nil || base == nil || base.Host == "" {
		return nil, fmt.Errorf("vault address is invalid")
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("vault address must not contain credentials, query, or fragment")
	}
	if base.Scheme != "https" && !(cfg.AllowInsecureHTTP && base.Scheme == "http") {
		return nil, fmt.Errorf("vault address must use https")
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, fmt.Errorf("vault token is required")
	}
	mount := strings.Trim(strings.TrimSpace(cfg.Mount), "/")
	if mount == "" {
		mount = "secret"
	}
	if err := validateVaultPath(mount); err != nil {
		return nil, fmt.Errorf("vault mount: %w", err)
	}
	prefix := strings.Trim(strings.TrimSpace(cfg.Prefix), "/")
	if prefix != "" {
		if err := validateVaultPath(prefix); err != nil {
			return nil, fmt.Errorf("vault prefix: %w", err)
		}
	}
	kvVersion := cfg.KVVersion
	if kvVersion == 0 {
		kvVersion = 2
	}
	if kvVersion != 1 && kvVersion != 2 {
		return nil, fmt.Errorf("vault KV version must be 1 or 2")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	} else {
		copyClient := *client
		if copyClient.Timeout <= 0 {
			copyClient.Timeout = timeout
		}
		client = &copyClient
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if previousRedirect != nil {
			if err := previousRedirect(req, via); err != nil {
				return err
			}
		}
		return http.ErrUseLastResponse
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	return &VaultBackend{
		base:      base,
		token:     token,
		namespace: strings.TrimSpace(cfg.Namespace),
		mount:     mount,
		prefix:    prefix,
		kvVersion: kvVersion,
		client:    client,
	}, nil
}

func (b *VaultBackend) Name() string          { return "vault" }
func (b *VaultBackend) ProtectedAtRest() bool { return true }

func (b *VaultBackend) Get(key string) (string, bool, error) {
	endpoint, err := b.endpoint(key, "data")
	if err != nil {
		return "", false, err
	}
	resp, err := b.request(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, vaultStatusError(resp)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := decodeVaultJSON(resp.Body, &envelope); err != nil {
		return "", false, err
	}
	var record struct {
		Value string `json:"value"`
	}
	raw := envelope.Data
	if b.kvVersion == 2 {
		var wrapped struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return "", false, fmt.Errorf("decode vault KV v2 data: %w", err)
		}
		raw = wrapped.Data
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return "", false, fmt.Errorf("decode vault secret: %w", err)
	}
	return record.Value, true, nil
}

func (b *VaultBackend) Set(key, value string) error {
	endpoint, err := b.endpoint(key, "data")
	if err != nil {
		return err
	}
	payload := map[string]any{"value": value}
	if b.kvVersion == 2 {
		payload = map[string]any{"data": payload}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode vault secret: %w", err)
	}
	resp, err := b.request(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return vaultStatusError(resp)
	}
	return nil
}

// List returns secret keys beneath prefix without resolving any secret values.
// Vault folder markers retain their trailing slash so callers can recurse.
func (b *VaultBackend) List(prefix string) ([]string, error) {
	endpoint, err := b.listEndpoint(prefix)
	if err != nil {
		return nil, err
	}
	resp, err := b.request("LIST", endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return []string{}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, vaultStatusError(resp)
	}
	var envelope struct {
		Data struct {
			Keys []string `json:"keys"`
		} `json:"data"`
	}
	if err := decodeVaultJSON(resp.Body, &envelope); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(envelope.Data.Keys))
	seen := map[string]bool{}
	for _, key := range envelope.Data.Keys {
		key = strings.TrimSpace(key)
		plain := strings.TrimSuffix(key, "/")
		if key == "" || validateVaultPath(plain) != nil || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func (b *VaultBackend) Delete(key string) error {
	kind := "data"
	if b.kvVersion == 2 {
		kind = "metadata"
	}
	endpoint, err := b.endpoint(key, kind)
	if err != nil {
		return err
	}
	resp, err := b.request(http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return vaultStatusError(resp)
	}
	return nil
}

func (b *VaultBackend) endpoint(key, kind string) (*url.URL, error) {
	key = strings.Trim(strings.TrimSpace(key), "/")
	if err := validateVaultPath(key); err != nil {
		return nil, fmt.Errorf("vault key: %w", err)
	}
	segments := []string{b.mount}
	if b.kvVersion == 2 {
		segments = append(segments, kind)
	}
	if b.prefix != "" {
		segments = append(segments, b.prefix)
	}
	segments = append(segments, key)
	u := *b.base
	u.Path = path.Join(b.base.Path, "v1", path.Join(segments...))
	return &u, nil
}

func (b *VaultBackend) listEndpoint(prefix string) (*url.URL, error) {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix != "" {
		if err := validateVaultPath(prefix); err != nil {
			return nil, fmt.Errorf("vault list prefix: %w", err)
		}
	}
	segments := []string{b.mount}
	if b.kvVersion == 2 {
		segments = append(segments, "metadata")
	}
	if b.prefix != "" {
		segments = append(segments, b.prefix)
	}
	if prefix != "" {
		segments = append(segments, prefix)
	}
	u := *b.base
	u.Path = path.Join(b.base.Path, "v1", path.Join(segments...))
	return &u, nil
}

func (b *VaultBackend) request(method string, endpoint *url.URL, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build vault request: %w", err)
	}
	req.Header.Set("X-Vault-Token", b.token)
	if b.namespace != "" {
		req.Header.Set("X-Vault-Namespace", b.namespace)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault request failed: %w", err)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("vault redirects are not allowed")
	}
	return resp, nil
}

func validateVaultPath(value string) error {
	if value == "" {
		return fmt.Errorf("is required")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("contains an invalid path segment")
		}
	}
	return nil
}

func decodeVaultJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, vaultMaxResponseBytes+1))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode vault response: %w", err)
	}
	return nil
}

func vaultStatusError(resp *http.Response) error {
	var envelope struct {
		Errors []string `json:"errors"`
	}
	_ = decodeVaultJSON(resp.Body, &envelope)
	message := strings.Join(envelope.Errors, "; ")
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("vault returned HTTP %d: %s", resp.StatusCode, message)
}
