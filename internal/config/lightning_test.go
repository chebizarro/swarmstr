package config

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"metiq/internal/store/state"
)

func validLightningConfig() LightningConfig {
	return LightningConfig{
		Wallets: map[string]LightningWalletConfig{
			"default": {
				Type: LightningWalletTypeNWC, Network: LightningNetworkMainnet,
				URI: "secret:NWC_CONNECTION_STRING", TrustWalletFeeLimit: true,
			},
		},
		L402: L402Config{
			Enabled: true, Payer: "default", AllowedOrigins: []string{"https://api.example.com:443"},
			MaxInvoiceMSat: 100_000, MaxFeeMSat: 5_000, MaxSpendMSatPerHour: 500_000,
			PaymentTimeoutMS: 60_000, Cache: L402CacheConfig{Persistence: "secret_store", TTL: "24h", MaxEntries: 128},
		},
	}
}

func TestParseLightningConfigExtraAndDefaults(t *testing.T) {
	extra := map[string]any{"lightning": map[string]any{
		"wallets": map[string]any{"default": map[string]any{
			"type": "nwc", "network": "mainnet", "uri": "secret:NWC_CONNECTION_STRING", "trust_wallet_fee_limit": true,
		}},
		"l402": map[string]any{
			"enabled": true, "payer": "default", "allowed_origins": []any{"https://api.example.com"},
			"max_invoice_msat": float64(1000), "max_fee_msat": float64(10),
			"max_spend_msat_per_hour": float64(2000), "payment_timeout_ms": float64(30000),
		},
	}}
	cfg, err := ParseLightningConfigExtra(extra)
	if err != nil {
		t.Fatalf("ParseLightningConfigExtra: %v", err)
	}
	if errs := ValidateLightningConfig(cfg); len(errs) != 0 {
		t.Fatalf("ValidateLightningConfig: %v", errs)
	}
	if got := cfg.L402.Cache.EffectiveTTL(); got != 24*time.Hour {
		t.Fatalf("cache TTL = %v", got)
	}
	if got := cfg.L402.Cache.EffectiveMaxEntries(); got != 128 {
		t.Fatalf("max entries = %d", got)
	}
	if got := cfg.L402.Cache.EffectivePersistence(); got != L402CachePersistenceSecretStore {
		t.Fatalf("persistence = %q", got)
	}
}

func TestParseLightningConfigExtraRejectsUnknownNestedField(t *testing.T) {
	_, err := ParseLightningConfigExtra(map[string]any{"lightning": map[string]any{
		"l402": map[string]any{"enabled": false, "pay_any_invoice": true},
	}})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestValidateLightningConfigSafetyAndCrossReferences(t *testing.T) {
	cfg := validLightningConfig()
	cfg.L402.AllowedOrigins = []string{"https://*.example.com", "http://api.example.com", "https://api.example.com/path"}
	cfg.L402.MaxSpendMSatPerHour = cfg.L402.MaxInvoiceMSat
	cfg.Wallets["default"] = LightningWalletConfig{Type: "nwc", Network: "unknown", URI: "nostr+walletconnect://plaintext"}
	errs := ValidateLightningConfig(cfg)
	joined := errorsString(errs)
	for _, want := range []string{"wildcards", "exact https", "max_spend_msat_per_hour", "unknown or missing network", "secret reference", "trust_wallet_fee_limit"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
}

func TestValidateLightningLNDWalletRequiresEnabledProfile(t *testing.T) {
	cfg := validLightningConfig()
	cfg.Wallets = map[string]LightningWalletConfig{"local": {Type: "lnd", LNDProfile: "primary"}}
	cfg.L402.Payer = "local"
	cfg.LND.Profiles = []LightningGRPCProfile{{
		ID: "primary", Target: "127.0.0.1:10009", Network: "regtest", TLSCertFile: "/tmp/tls.cert",
		Macaroon: CredentialSourceConfig{Ref: "file:/tmp/invoice.macaroon", Encoding: "hex"},
	}}
	if got := errorsString(ValidateLightningConfig(cfg)); !strings.Contains(got, "not payer_enabled") {
		t.Fatalf("expected payer_enabled error, got %s", got)
	}
	cfg.LND.Profiles[0].PayerEnabled = true
	if errs := ValidateLightningConfig(cfg); len(errs) != 0 {
		t.Fatalf("expected valid LND payer, got %v", errs)
	}
}

func TestValidateConfigDocRejectsLightningGRPCIDCollision(t *testing.T) {
	cfg := validLightningConfig()
	cfg.LND.Profiles = []LightningGRPCProfile{{
		ID: "Primary", Target: "127.0.0.1:10009", Network: "regtest", TLSCertFile: "/tmp/tls.cert",
		Macaroon: CredentialSourceConfig{Ref: "file:/tmp/admin.macaroon", Encoding: "hex"}, PayerEnabled: true,
	}}
	doc := state.ConfigDoc{Extra: map[string]any{
		"lightning": structToAny(t, cfg),
		"grpc":      map[string]any{"endpoints": []any{map[string]any{"id": "primary", "target": "service:443"}}},
	}}
	if got := errorsString(ValidateConfigDoc(doc)); !strings.Contains(got, "collides") {
		t.Fatalf("expected collision error, got %s", got)
	}
}

func TestResolveNWCURIPrecedence(t *testing.T) {
	t.Setenv("NWC_CONNECTION_STRING", "from-env")
	extra := map[string]any{"nwc": map[string]any{"uri": "legacy-uri", "connection_string": "legacy-connection"}}
	if value, source, ok := ResolveNWCURI(extra, LightningWalletConfig{URI: "secret:NEW"}); !ok || value != "secret:NEW" || source != "extra.lightning.wallets.uri" {
		t.Fatalf("new source precedence: %q %q %v", value, source, ok)
	}
	if value, source, ok := ResolveNWCURI(extra, LightningWalletConfig{}); !ok || value != "legacy-uri" || source != "extra.nwc.uri" {
		t.Fatalf("legacy URI precedence: %q %q %v", value, source, ok)
	}
	delete(extra["nwc"].(map[string]any), "uri")
	if value, source, ok := ResolveNWCURI(extra, LightningWalletConfig{}); !ok || value != "legacy-connection" || source != "extra.nwc.connection_string" {
		t.Fatalf("legacy connection precedence: %q %q %v", value, source, ok)
	}
	delete(extra["nwc"].(map[string]any), "connection_string")
	if value, source, ok := ResolveNWCURI(extra, LightningWalletConfig{}); !ok || value != "from-env" || source != "NWC_CONNECTION_STRING" {
		t.Fatalf("env precedence: %q %q %v", value, source, ok)
	}
}

func TestValidateLightningConfigExtraAcceptsLegacyNWCSourceWithoutRewriting(t *testing.T) {
	cfg := validLightningConfig()
	wallet := cfg.Wallets["default"]
	wallet.URI = ""
	cfg.Wallets["default"] = wallet
	if got := errorsString(ValidateLightningConfig(cfg)); !strings.Contains(got, ".uri is required") {
		t.Fatalf("canonical validation should require URI, got %s", got)
	}
	extra := map[string]any{"nwc": map[string]any{"connection_string": "nostr+walletconnect://legacy"}}
	if errs := ValidateLightningConfigExtra(cfg, extra); len(errs) != 0 {
		t.Fatalf("legacy source should satisfy compatibility validation: %v", errs)
	}
	if cfg.Wallets["default"].URI != "" {
		t.Fatal("compatibility validation rewrote the config")
	}
}

func TestParseConfigBytesRejectsUnknownLightningField(t *testing.T) {
	_, err := ParseConfigBytes([]byte("extra:\n  lightning:\n    l402:\n      enabled: false\n      surprise: true\n"), ".yaml")
	if err == nil || !strings.Contains(err.Error(), "extra.lightning.l402.surprise") {
		t.Fatalf("expected strict nested key error, got %v", err)
	}
}

func errorsString(errs []error) string {
	parts := make([]string, len(errs))
	for i, err := range errs {
		parts[i] = err.Error()
	}
	return strings.Join(parts, " | ")
}

func structToAny(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
