package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	LightningWalletTypeNWC          = "nwc"
	LightningWalletTypeLND          = "lnd"
	LightningNetworkMainnet         = "mainnet"
	LightningNetworkTestnet         = "testnet"
	LightningNetworkRegtest         = "regtest"
	LightningNetworkSignet          = "signet"
	LightningToolsetRead            = "read"
	LightningToolsetReceive         = "receive"
	LightningToolsetSpend           = "spend"
	LightningToolsetAdmin           = "admin"
	CredentialEncodingText          = "text"
	CredentialEncodingHex           = "hex"
	L402CachePersistenceSecretStore = "secret_store"
	L402CachePersistenceMemory      = "memory"
	DefaultLightningWalletTimeoutMS = 30_000
	DefaultL402CacheTTL             = 24 * time.Hour
	DefaultL402CacheMaxEntries      = 128
)

type LightningConfig struct {
	Wallets map[string]LightningWalletConfig `json:"wallets,omitempty" yaml:"wallets,omitempty"`
	L402    L402Config                       `json:"l402,omitempty" yaml:"l402,omitempty"`
	LND     LNDProfilesConfig                `json:"lnd,omitempty" yaml:"lnd,omitempty"`
	Tapd    TapdProfilesConfig               `json:"tapd,omitempty" yaml:"tapd,omitempty"`
}

type LightningWalletConfig struct {
	Type                string `json:"type" yaml:"type"`
	Network             string `json:"network,omitempty" yaml:"network,omitempty"`
	URI                 string `json:"uri,omitempty" yaml:"uri,omitempty"`
	LNDProfile          string `json:"lnd_profile,omitempty" yaml:"lnd_profile,omitempty"`
	TimeoutMS           int    `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	TrustWalletFeeLimit bool   `json:"trust_wallet_fee_limit,omitempty" yaml:"trust_wallet_fee_limit,omitempty"`
}

func (c LightningWalletConfig) EffectiveTimeoutMS() int {
	if c.TimeoutMS > 0 {
		return c.TimeoutMS
	}
	return DefaultLightningWalletTimeoutMS
}

type L402Config struct {
	Enabled             bool            `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Payer               string          `json:"payer,omitempty" yaml:"payer,omitempty"`
	AllowedOrigins      []string        `json:"allowed_origins,omitempty" yaml:"allowed_origins,omitempty"`
	MaxInvoiceMSat      int64           `json:"max_invoice_msat,omitempty" yaml:"max_invoice_msat,omitempty"`
	MaxFeeMSat          int64           `json:"max_fee_msat,omitempty" yaml:"max_fee_msat,omitempty"`
	MaxSpendMSatPerHour int64           `json:"max_spend_msat_per_hour,omitempty" yaml:"max_spend_msat_per_hour,omitempty"`
	PaymentTimeoutMS    int             `json:"payment_timeout_ms,omitempty" yaml:"payment_timeout_ms,omitempty"`
	Cache               L402CacheConfig `json:"cache,omitempty" yaml:"cache,omitempty"`
}

type L402CacheConfig struct {
	Persistence string `json:"persistence,omitempty" yaml:"persistence,omitempty"`
	TTL         string `json:"ttl,omitempty" yaml:"ttl,omitempty"`
	MaxEntries  int    `json:"max_entries,omitempty" yaml:"max_entries,omitempty"`
}

func (c L402CacheConfig) EffectivePersistence() string {
	if value := strings.ToLower(strings.TrimSpace(c.Persistence)); value != "" {
		return value
	}
	return L402CachePersistenceSecretStore
}
func (c L402CacheConfig) EffectiveTTL() time.Duration {
	if value := strings.TrimSpace(c.TTL); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return DefaultL402CacheTTL
}
func (c L402CacheConfig) EffectiveMaxEntries() int {
	if c.MaxEntries > 0 {
		return c.MaxEntries
	}
	return DefaultL402CacheMaxEntries
}

type LNDProfilesConfig struct {
	Profiles []LightningGRPCProfile `json:"profiles,omitempty" yaml:"profiles,omitempty"`
}
type TapdProfilesConfig struct {
	Profiles []LightningGRPCProfile `json:"profiles,omitempty" yaml:"profiles,omitempty"`
}

type LightningGRPCProfile struct {
	ID           string                 `json:"id" yaml:"id"`
	Target       string                 `json:"target" yaml:"target"`
	Network      string                 `json:"network" yaml:"network"`
	TLSCertFile  string                 `json:"tls_cert_file" yaml:"tls_cert_file"`
	ServerName   string                 `json:"server_name,omitempty" yaml:"server_name,omitempty"`
	Macaroon     CredentialSourceConfig `json:"macaroon" yaml:"macaroon"`
	PayerEnabled bool                   `json:"payer_enabled,omitempty" yaml:"payer_enabled,omitempty"`
	Toolsets     []string               `json:"toolsets,omitempty" yaml:"toolsets,omitempty"`
	Defaults     GRPCDefaultsConfig     `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Exposure     GRPCExposureConfig     `json:"exposure,omitempty" yaml:"exposure,omitempty"`
}

func (p LightningGRPCProfile) EffectiveToolsets() []string {
	if len(p.Toolsets) == 0 {
		return []string{LightningToolsetRead}
	}
	return append([]string(nil), p.Toolsets...)
}

type CredentialSourceConfig struct {
	Ref      string `json:"ref" yaml:"ref"`
	Encoding string `json:"encoding,omitempty" yaml:"encoding,omitempty"`
}

func (c CredentialSourceConfig) EffectiveEncoding() string {
	if value := strings.ToLower(strings.TrimSpace(c.Encoding)); value != "" {
		return value
	}
	return CredentialEncodingText
}

func ParseLightningConfigExtra(extra map[string]any) (LightningConfig, error) {
	if len(extra) == 0 || extra["lightning"] == nil {
		return LightningConfig{}, nil
	}
	raw, err := json.Marshal(extra["lightning"])
	if err != nil {
		return LightningConfig{}, fmt.Errorf("marshal lightning config: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var cfg LightningConfig
	if err := decoder.Decode(&cfg); err != nil {
		return LightningConfig{}, fmt.Errorf("parse lightning config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return LightningConfig{}, fmt.Errorf("parse lightning config: multiple values")
		}
		return LightningConfig{}, fmt.Errorf("parse lightning config: %w", err)
	}
	return cfg, nil
}

func ValidateLightningConfig(cfg LightningConfig) []error {
	return validateLightningConfig(cfg, nil, false)
}

// ValidateLightningConfigExtra applies the legacy NWC source compatibility
// rules in addition to the canonical typed validation. It never rewrites cfg.
func ValidateLightningConfigExtra(cfg LightningConfig, extra map[string]any) []error {
	return validateLightningConfig(cfg, extra, true)
}

func validateLightningConfig(cfg LightningConfig, extra map[string]any, allowLegacyNWC bool) []error {
	var errs []error
	lndProfiles, lndErrs := validateLightningProfiles("lightning.lnd.profiles", cfg.LND.Profiles, true)
	_, tapdErrs := validateLightningProfiles("lightning.tapd.profiles", cfg.Tapd.Profiles, false)
	errs = append(errs, lndErrs...)
	errs = append(errs, tapdErrs...)

	wallets := make(map[string]LightningWalletConfig, len(cfg.Wallets))
	for id, wallet := range cfg.Wallets {
		path := fmt.Sprintf("lightning.wallets[%q]", id)
		key := normalizeLightningID(id)
		if strings.TrimSpace(id) == "" || !validIdentifier(id, false) {
			errs = append(errs, fmt.Errorf("%s: wallet id must contain only letters, digits, '-' or '_' and start with a letter or digit", path))
			continue
		}
		if _, exists := wallets[key]; exists {
			errs = append(errs, fmt.Errorf("%s duplicates another wallet id case-insensitively", path))
		}
		wallets[key] = wallet
		switch walletType := strings.ToLower(strings.TrimSpace(wallet.Type)); walletType {
		case LightningWalletTypeNWC:
			if strings.TrimSpace(wallet.Network) == "" || !validLightningNetwork(wallet.Network) {
				errs = append(errs, fmt.Errorf("%s.network: unknown or missing network %q", path, wallet.Network))
			}
			if strings.TrimSpace(wallet.LNDProfile) != "" {
				errs = append(errs, fmt.Errorf("%s.lnd_profile is only valid for lnd wallets", path))
			}
			if uri := strings.TrimSpace(wallet.URI); uri != "" && !isWalletSecretReference(uri) {
				errs = append(errs, fmt.Errorf("%s.uri must be a secret reference", path))
			}
		case LightningWalletTypeLND:
			profileID := normalizeLightningID(wallet.LNDProfile)
			profile, ok := lndProfiles[profileID]
			if profileID == "" || !ok {
				errs = append(errs, fmt.Errorf("%s.lnd_profile must reference an existing LND profile", path))
			} else if !profile.PayerEnabled {
				errs = append(errs, fmt.Errorf("%s.lnd_profile %q is not payer_enabled", path, wallet.LNDProfile))
			}
			if strings.TrimSpace(wallet.URI) != "" {
				errs = append(errs, fmt.Errorf("%s.uri is only valid for nwc wallets", path))
			}
		default:
			errs = append(errs, fmt.Errorf("%s.type: unknown value %q (valid: nwc, lnd)", path, wallet.Type))
		}
		if wallet.TimeoutMS < 0 {
			errs = append(errs, fmt.Errorf("%s.timeout_ms must be >= 0", path))
		}
	}

	if cfg.L402.Enabled {
		payerID := normalizeLightningID(cfg.L402.Payer)
		payer, ok := wallets[payerID]
		if payerID == "" || !ok {
			errs = append(errs, fmt.Errorf("lightning.l402.payer must reference an existing wallet"))
		} else if strings.EqualFold(strings.TrimSpace(payer.Type), LightningWalletTypeNWC) {
			_, _, legacyFound := ResolveNWCURI(extra, payer)
			if strings.TrimSpace(payer.URI) == "" && (!allowLegacyNWC || !legacyFound) {
				errs = append(errs, fmt.Errorf("lightning.wallets[%q].uri is required for an NWC L402 payer", cfg.L402.Payer))
			}
			if !payer.TrustWalletFeeLimit {
				errs = append(errs, fmt.Errorf("lightning.wallets[%q].trust_wallet_fee_limit must explicitly be true for an NWC L402 payer", cfg.L402.Payer))
			}
		}
		if len(cfg.L402.AllowedOrigins) == 0 {
			errs = append(errs, fmt.Errorf("lightning.l402.allowed_origins requires at least one exact HTTPS origin"))
		}
		for i, origin := range cfg.L402.AllowedOrigins {
			if err := validateL402Origin(origin); err != nil {
				errs = append(errs, fmt.Errorf("lightning.l402.allowed_origins[%d]: %w", i, err))
			}
		}
		if cfg.L402.MaxInvoiceMSat <= 0 {
			errs = append(errs, fmt.Errorf("lightning.l402.max_invoice_msat must be positive"))
		}
		if cfg.L402.MaxFeeMSat <= 0 {
			errs = append(errs, fmt.Errorf("lightning.l402.max_fee_msat must be positive"))
		}
		if cfg.L402.MaxSpendMSatPerHour <= 0 {
			errs = append(errs, fmt.Errorf("lightning.l402.max_spend_msat_per_hour must be positive"))
		} else if cfg.L402.MaxSpendMSatPerHour < cfg.L402.MaxInvoiceMSat+cfg.L402.MaxFeeMSat {
			errs = append(errs, fmt.Errorf("lightning.l402.max_spend_msat_per_hour must be >= max_invoice_msat + max_fee_msat"))
		}
		if cfg.L402.PaymentTimeoutMS <= 0 {
			errs = append(errs, fmt.Errorf("lightning.l402.payment_timeout_ms must be positive"))
		}
	}
	switch cfg.L402.Cache.EffectivePersistence() {
	case L402CachePersistenceSecretStore, L402CachePersistenceMemory:
	default:
		errs = append(errs, fmt.Errorf("lightning.l402.cache.persistence: unknown value %q (valid: secret_store, memory)", cfg.L402.Cache.Persistence))
	}
	if ttl := strings.TrimSpace(cfg.L402.Cache.TTL); ttl != "" {
		if duration, err := time.ParseDuration(ttl); err != nil || duration <= 0 {
			errs = append(errs, fmt.Errorf("lightning.l402.cache.ttl must be a positive duration"))
		}
	}
	if cfg.L402.Cache.MaxEntries < 0 {
		errs = append(errs, fmt.Errorf("lightning.l402.cache.max_entries must be >= 0"))
	}
	return errs
}

func validateLightningProfiles(path string, profiles []LightningGRPCProfile, allowPayer bool) (map[string]LightningGRPCProfile, []error) {
	seen := make(map[string]LightningGRPCProfile, len(profiles))
	var errs []error
	for i, profile := range profiles {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		id := strings.TrimSpace(profile.ID)
		key := normalizeLightningID(id)
		if id == "" || !validIdentifier(id, false) {
			errs = append(errs, fmt.Errorf("%s.id must contain only letters, digits, '-' or '_' and start with a letter or digit", itemPath))
		} else if _, exists := seen[key]; exists {
			errs = append(errs, fmt.Errorf("%s.id %q duplicates another profile id case-insensitively", itemPath, profile.ID))
		} else {
			seen[key] = profile
		}
		if strings.TrimSpace(profile.Target) == "" {
			errs = append(errs, fmt.Errorf("%s.target is required", itemPath))
		}
		if !validLightningNetwork(profile.Network) {
			errs = append(errs, fmt.Errorf("%s.network: unknown value %q", itemPath, profile.Network))
		}
		if certFile := strings.TrimSpace(profile.TLSCertFile); certFile == "" || !filepath.IsAbs(certFile) {
			errs = append(errs, fmt.Errorf("%s.tls_cert_file must be an absolute path", itemPath))
		}
		errs = append(errs, validateCredentialSource(itemPath+".macaroon", profile.Macaroon)...)
		if profile.PayerEnabled && !allowPayer {
			errs = append(errs, fmt.Errorf("%s.payer_enabled is only valid for LND profiles", itemPath))
		}
		seenToolsets := map[string]struct{}{}
		for j, raw := range profile.EffectiveToolsets() {
			value := strings.ToLower(strings.TrimSpace(raw))
			switch value {
			case LightningToolsetRead, LightningToolsetReceive, LightningToolsetSpend, LightningToolsetAdmin:
			default:
				errs = append(errs, fmt.Errorf("%s.toolsets[%d]: unknown value %q", itemPath, j, raw))
			}
			if _, ok := seenToolsets[value]; ok {
				errs = append(errs, fmt.Errorf("%s.toolsets[%d] duplicates %q", itemPath, j, raw))
			}
			seenToolsets[value] = struct{}{}
		}
		errs = append(errs, validateGRPCDefaults(itemPath+".defaults", profile.Defaults)...)
		errs = append(errs, validateGRPCExposure(itemPath+".exposure", profile.Exposure)...)
	}
	return seen, errs
}

func validateCredentialSource(path string, source CredentialSourceConfig) []error {
	var errs []error
	ref := strings.TrimSpace(source.Ref)
	if ref == "" {
		errs = append(errs, fmt.Errorf("%s.ref is required", path))
	} else if strings.HasPrefix(ref, "file:") {
		filePath := strings.TrimPrefix(ref, "file:")
		if filePath == "" || !filepath.IsAbs(filePath) {
			errs = append(errs, fmt.Errorf("%s.ref file path must be absolute", path))
		}
	} else if !isSecretReference(ref) {
		errs = append(errs, fmt.Errorf("%s.ref must be a supported secret or absolute file reference", path))
	}
	switch source.EffectiveEncoding() {
	case CredentialEncodingText, CredentialEncodingHex:
	default:
		errs = append(errs, fmt.Errorf("%s.encoding: unknown value %q (valid: text, hex)", path, source.Encoding))
	}
	return errs
}

func validateL402Origin(raw string) error {
	if strings.Contains(raw, "*") {
		return fmt.Errorf("wildcards are not supported")
	}
	if raw != strings.TrimSpace(raw) || raw == "" {
		return fmt.Errorf("must be a non-empty exact HTTPS origin")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid origin")
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || parsed.Opaque != "" {
		return fmt.Errorf("must use exact https://host[:port] form without userinfo, path, query, or fragment")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("host is required")
	}
	return nil
}

func validLightningNetwork(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case LightningNetworkMainnet, LightningNetworkTestnet, LightningNetworkRegtest, LightningNetworkSignet:
		return true
	default:
		return false
	}
}
func normalizeLightningID(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func isSecretReference(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "secret:") || strings.HasPrefix(value, "env:") ||
		strings.HasPrefix(value, "$") || strings.HasPrefix(value, "file:")
}

func isWalletSecretReference(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "secret:") || strings.HasPrefix(value, "env:") || strings.HasPrefix(value, "$")
}

// ResolveNWCURI applies the compatibility precedence without logging the value.
func ResolveNWCURI(extra map[string]any, wallet LightningWalletConfig) (uri, source string, found bool) {
	if value := strings.TrimSpace(wallet.URI); value != "" {
		return value, "extra.lightning.wallets.uri", true
	}
	if nwc, ok := extra["nwc"].(map[string]any); ok {
		if value, ok := nwc["uri"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), "extra.nwc.uri", true
		}
		if value, ok := nwc["connection_string"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), "extra.nwc.connection_string", true
		}
	}
	if value, ok := os.LookupEnv("NWC_CONNECTION_STRING"); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), "NWC_CONNECTION_STRING", true
	}
	return "", "", false
}

func validateLightningConfigDocExtra(extra map[string]any) []error {
	cfg, err := ParseLightningConfigExtra(extra)
	if err != nil {
		return []error{err}
	}
	errs := ValidateLightningConfigExtra(cfg, extra)
	var grpcCfg GRPCConfig
	if raw := extra["grpc"]; raw != nil && decodeAnyIntoStruct(raw, &grpcCfg) {
		genericIDs := map[string]struct{}{}
		for _, endpoint := range grpcCfg.Endpoints {
			genericIDs[normalizeLightningID(endpoint.ID)] = struct{}{}
		}
		for kind, profiles := range map[string][]LightningGRPCProfile{"lnd": cfg.LND.Profiles, "tapd": cfg.Tapd.Profiles} {
			for i, profile := range profiles {
				if _, collision := genericIDs[normalizeLightningID(profile.ID)]; collision {
					errs = append(errs, fmt.Errorf("lightning.%s.profiles[%d].id %q collides with generic gRPC endpoint id", kind, i, profile.ID))
				}
			}
		}
	}
	return errs
}

func detectUnknownLightningKeys(raw any) []string {
	var errs []string
	root, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	errs = append(errs, detectUnknownMapKeys("extra.lightning", raw, []string{"wallets", "l402", "lnd", "tapd"})...)
	if wallets, ok := root["wallets"].(map[string]any); ok {
		for id, wallet := range wallets {
			errs = append(errs, detectUnknownMapKeys(fmt.Sprintf("extra.lightning.wallets.%s", id), wallet,
				[]string{"type", "network", "uri", "lnd_profile", "timeout_ms", "trust_wallet_fee_limit"})...)
		}
	}
	if l402, ok := root["l402"].(map[string]any); ok {
		errs = append(errs, detectUnknownMapKeys("extra.lightning.l402", l402,
			[]string{"enabled", "payer", "allowed_origins", "max_invoice_msat", "max_fee_msat", "max_spend_msat_per_hour", "payment_timeout_ms", "cache"})...)
		errs = append(errs, detectUnknownMapKeys("extra.lightning.l402.cache", l402["cache"], []string{"persistence", "ttl", "max_entries"})...)
	}
	for _, section := range []string{"lnd", "tapd"} {
		container, ok := root[section].(map[string]any)
		if !ok {
			continue
		}
		errs = append(errs, detectUnknownMapKeys("extra.lightning."+section, container, []string{"profiles"})...)
		profiles, _ := container["profiles"].([]any)
		for i, profileRaw := range profiles {
			path := fmt.Sprintf("extra.lightning.%s.profiles[%d]", section, i)
			errs = append(errs, detectUnknownMapKeys(path, profileRaw,
				[]string{"id", "target", "network", "tls_cert_file", "server_name", "macaroon", "payer_enabled", "toolsets", "defaults", "exposure"})...)
			profile, _ := profileRaw.(map[string]any)
			errs = append(errs, detectUnknownMapKeys(path+".macaroon", profile["macaroon"], []string{"ref", "encoding"})...)
			errs = append(errs, detectUnknownMapKeys(path+".defaults", profile["defaults"],
				[]string{"dial_timeout_ms", "reflection_timeout_ms", "deadline_ms", "max_deadline_ms", "max_recv_message_bytes"})...)
			errs = append(errs, detectUnknownMapKeys(path+".exposure", profile["exposure"],
				[]string{"mode", "deferred_threshold", "namespace", "include_services", "exclude_methods"})...)
		}
	}
	return errs
}
