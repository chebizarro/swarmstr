package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/config"
	"metiq/internal/lightning"
	"metiq/internal/secrets"
	"metiq/internal/store/state"
)

type fakeNWCOperationClient struct {
	requestCalls *int
	paymentCalls *int
	closeCalls   *int
}

func (c *fakeNWCOperationClient) Request(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	if c.requestCalls != nil {
		*c.requestCalls++
	}
	return map[string]any{"ok": true}, nil
}

func (c *fakeNWCOperationClient) PayInvoiceTool(_ context.Context, _ string, _ int64) (map[string]any, error) {
	if c.paymentCalls != nil {
		*c.paymentCalls++
	}
	return map[string]any{"ok": true}, nil
}

func (c *fakeNWCOperationClient) Close() error {
	if c.closeCalls != nil {
		*c.closeCalls++
	}
	return nil
}

func TestResolveStandaloneNWCMigrationMatrix(t *testing.T) {
	const (
		canonicalURI = "nwc://canonical"
		secretURI    = "nwc://secret-ref"
		envURI       = "nwc://environment"
	)
	tests := []struct {
		name        string
		doc         state.ConfigDoc
		env         map[string]string
		wantFound   bool
		wantURI     string
		wantSource  string
		wantWallet  string
		wantTimeout time.Duration
	}{
		{
			name: "canonical-only",
			doc: standaloneNWCDoc(map[string]any{
				"lightning": map[string]any{"wallets": map[string]any{
					"default": map[string]any{"type": "nwc", "network": "mainnet", "uri": "env:CANONICAL_ONLY", "timeout_ms": 12345},
				}},
			}),
			env:       map[string]string{"CANONICAL_ONLY": canonicalURI},
			wantFound: true, wantURI: canonicalURI, wantSource: "extra.lightning.wallets.uri",
			wantWallet: "default", wantTimeout: 12345 * time.Millisecond,
		},
		{
			name:      "legacy-uri-only",
			doc:       standaloneNWCDoc(map[string]any{"nwc": map[string]any{"uri": "nwc://legacy-uri"}}),
			wantFound: true, wantURI: "nwc://legacy-uri", wantSource: "extra.nwc.uri",
			wantWallet: "nwc", wantTimeout: 30 * time.Second,
		},
		{
			name:      "legacy-connection-string",
			doc:       standaloneNWCDoc(map[string]any{"nwc": map[string]any{"connection_string": "nwc://legacy-connection"}}),
			wantFound: true, wantURI: "nwc://legacy-connection", wantSource: "extra.nwc.connection_string",
			wantWallet: "nwc", wantTimeout: 30 * time.Second,
		},
		{
			name:      "env-only",
			doc:       standaloneNWCDoc(nil),
			env:       map[string]string{"NWC_CONNECTION_STRING": envURI},
			wantFound: true, wantURI: envURI, wantSource: "NWC_CONNECTION_STRING",
			wantWallet: "nwc", wantTimeout: 30 * time.Second,
		},
		{
			name: "canonical-beats-legacy",
			doc: standaloneNWCDoc(map[string]any{
				"lightning": map[string]any{"wallets": map[string]any{
					"default": map[string]any{"type": "nwc", "network": "mainnet", "uri": "env:CANONICAL_PRECEDENCE"},
				}},
				"nwc": map[string]any{"uri": "nwc://legacy-uri", "connection_string": "nwc://legacy-connection"},
			}),
			env: map[string]string{
				"CANONICAL_PRECEDENCE":  canonicalURI,
				"NWC_CONNECTION_STRING": envURI,
			},
			wantFound: true, wantURI: canonicalURI, wantSource: "extra.lightning.wallets.uri",
			wantWallet: "default", wantTimeout: 30 * time.Second,
		},
		{
			name: "explicit-secret-ref",
			doc: standaloneNWCDoc(map[string]any{
				"lightning": map[string]any{"wallets": map[string]any{
					"only-wallet": map[string]any{"type": "nwc", "network": "mainnet", "uri": "secret:STANDALONE_NWC_SECRET"},
				}},
			}),
			env:       map[string]string{"STANDALONE_NWC_SECRET": secretURI},
			wantFound: true, wantURI: secretURI, wantSource: "extra.lightning.wallets.uri",
			wantWallet: "only-wallet", wantTimeout: 30 * time.Second,
		},
		{
			name:      "no-config",
			doc:       standaloneNWCDoc(nil),
			wantFound: false, wantWallet: "nwc", wantTimeout: 30 * time.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NWC_CONNECTION_STRING", "")
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			store := secrets.NewStore([]string{filepath.Join(t.TempDir(), "missing.env")})
			got, found, err := resolveStandaloneNWC(context.Background(), store, test.doc)
			if err != nil {
				t.Fatalf("resolveStandaloneNWC: %v", err)
			}
			if found != test.wantFound {
				t.Fatalf("found = %v, want %v", found, test.wantFound)
			}
			if got.URI != test.wantURI || got.Source != test.wantSource ||
				got.WalletID != test.wantWallet || got.Timeout != test.wantTimeout {
				t.Fatalf("resolved = {uri:%q source:%q wallet:%q timeout:%s}, want {uri:%q source:%q wallet:%q timeout:%s}",
					got.URI, got.Source, got.WalletID, got.Timeout,
					test.wantURI, test.wantSource, test.wantWallet, test.wantTimeout)
			}
		})
	}
}

func TestResolveStandaloneNWCBrokenCanonicalRefDoesNotFallBack(t *testing.T) {
	t.Setenv("NWC_CONNECTION_STRING", "nwc://environment")
	doc := standaloneNWCDoc(map[string]any{
		"lightning": map[string]any{"wallets": map[string]any{
			"default": map[string]any{"type": "nwc", "network": "mainnet", "uri": "secret:MISSING_CANONICAL_NWC"},
		}},
		"nwc": map[string]any{"uri": "nwc://legacy"},
	})
	store := secrets.NewStore([]string{filepath.Join(t.TempDir(), "missing.env")})
	got, found, err := resolveStandaloneNWC(context.Background(), store, doc)
	if err == nil {
		t.Fatal("expected unresolved canonical reference error")
	}
	if !found || got.Source != "extra.lightning.wallets.uri" || got.URI != "" {
		t.Fatalf("failed resolution = %#v found=%v", got, found)
	}
}

func TestRegisterConfiguredNWCToolsNoConfigLeavesToolsAbsent(t *testing.T) {
	t.Setenv("NWC_CONNECTION_STRING", "")
	registry := agent.NewToolRegistry()
	factoryCalls := 0
	client := &liveNWCToolClient{
		snapshot: func() state.ConfigDoc { return standaloneNWCDoc(nil) },
		secrets:  secrets.NewStore([]string{filepath.Join(t.TempDir(), "missing.env")}),
		newClient: func(lightning.NWCClientConfig) (nwcOperationClient, error) {
			factoryCalls++
			return &fakeNWCOperationClient{}, nil
		},
	}
	active, source, err := registerConfiguredNWCTools(context.Background(), registry, client)
	if err != nil {
		t.Fatalf("registerConfiguredNWCTools: %v", err)
	}
	if active || source != "" || factoryCalls != 0 {
		t.Fatalf("no-config registration = active:%v source:%q factory_calls:%d", active, source, factoryCalls)
	}
	for _, name := range standaloneNWCToolNames() {
		if slices.Contains(registry.List(), name) {
			t.Fatalf("tool %q registered without NWC config", name)
		}
	}
}

func TestLiveNWCToolClientReResolvesSecretsAndPreservesTypedPayment(t *testing.T) {
	t.Setenv("LIVE_NWC_URI", "nwc://first")
	doc := standaloneNWCDoc(map[string]any{
		"lightning": map[string]any{"wallets": map[string]any{
			"default": map[string]any{"type": "nwc", "network": "mainnet", "uri": "env:LIVE_NWC_URI"},
		}},
	})
	store := secrets.NewStore([]string{filepath.Join(t.TempDir(), "missing.env")})
	var uris []string
	requestCalls, paymentCalls, closeCalls := 0, 0, 0
	client := &liveNWCToolClient{
		snapshot: func() state.ConfigDoc { return doc },
		secrets:  store,
		newClient: func(cfg lightning.NWCClientConfig) (nwcOperationClient, error) {
			uris = append(uris, cfg.URI)
			return &fakeNWCOperationClient{
				requestCalls: &requestCalls,
				paymentCalls: &paymentCalls,
				closeCalls:   &closeCalls,
			}, nil
		},
	}
	if _, err := client.Request(context.Background(), "get_balance", nil); err != nil {
		t.Fatalf("Request: %v", err)
	}
	t.Setenv("LIVE_NWC_URI", "nwc://second")
	if _, err := client.PayInvoiceTool(context.Background(), "lnbc-test", 0); err != nil {
		t.Fatalf("PayInvoiceTool: %v", err)
	}
	if !slices.Equal(uris, []string{"nwc://first", "nwc://second"}) {
		t.Fatalf("client URIs = %v", uris)
	}
	if requestCalls != 1 || paymentCalls != 1 || closeCalls != 2 {
		t.Fatalf("delegation counts = request:%d payment:%d close:%d", requestCalls, paymentCalls, closeCalls)
	}
	t.Setenv("LIVE_NWC_URI", "")
	if _, err := client.Request(context.Background(), "get_balance", nil); err == nil {
		t.Fatal("expected removed secret to fail closed")
	}
	if len(uris) != 2 {
		t.Fatalf("client factory called after secret removal: %v", uris)
	}
}

func TestRegisterConfiguredNWCToolsKeepsPublicNames(t *testing.T) {
	t.Setenv("NWC_CONNECTION_STRING", "")
	doc := standaloneNWCDoc(map[string]any{"nwc": map[string]any{"uri": "nwc://legacy"}})
	registry := agent.NewToolRegistry()
	closeCalls := 0
	client := &liveNWCToolClient{
		snapshot: func() state.ConfigDoc { return doc },
		secrets:  secrets.NewStore([]string{filepath.Join(t.TempDir(), "missing.env")}),
		newClient: func(lightning.NWCClientConfig) (nwcOperationClient, error) {
			return &fakeNWCOperationClient{closeCalls: &closeCalls}, nil
		},
	}
	active, source, err := registerConfiguredNWCTools(context.Background(), registry, client)
	if err != nil {
		t.Fatalf("registerConfiguredNWCTools: %v", err)
	}
	if !active || source != "extra.nwc.uri" || closeCalls != 1 {
		t.Fatalf("registration = active:%v source:%q close_calls:%d", active, source, closeCalls)
	}
	for _, name := range standaloneNWCToolNames() {
		if !slices.Contains(registry.List(), name) {
			t.Fatalf("tool %q missing after registration: %v", name, registry.List())
		}
	}
}

func standaloneNWCDoc(extra map[string]any) state.ConfigDoc {
	if extra == nil {
		extra = map[string]any{}
	}
	return state.ConfigDoc{Extra: extra}
}

func standaloneNWCToolNames() []string {
	return []string{
		"nwc_get_balance",
		"nwc_pay_invoice",
		"nwc_make_invoice",
		"nwc_lookup_invoice",
		"nwc_list_transactions",
	}
}

func TestSelectStandaloneNWCWalletDeterministicFallbacks(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config.LightningConfig
		wantID string
		wantOK bool
	}{
		{
			name: "enabled payer",
			cfg: config.LightningConfig{
				Wallets: map[string]config.LightningWalletConfig{
					"default": {Type: "nwc"},
					"payer":   {Type: "NWC"},
				},
				L402: config.L402Config{Enabled: true, Payer: "PAYER"},
			},
			wantID: "PAYER", wantOK: true,
		},
		{
			name: "disabled payer does not override default",
			cfg: config.LightningConfig{
				Wallets: map[string]config.LightningWalletConfig{
					"default": {Type: "nwc"},
					"payer":   {Type: "nwc"},
				},
				L402: config.L402Config{Payer: "payer"},
			},
			wantID: "default", wantOK: true,
		},
		{
			name: "sole NWC after LND default",
			cfg: config.LightningConfig{Wallets: map[string]config.LightningWalletConfig{
				"default": {Type: "lnd"},
				"wallet":  {Type: "nwc"},
			}},
			wantID: "wallet", wantOK: true,
		},
		{
			name: "multiple wallets are ambiguous",
			cfg: config.LightningConfig{Wallets: map[string]config.LightningWalletConfig{
				"one": {Type: "nwc"},
				"two": {Type: "nwc"},
			}},
			wantOK: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id, _, ok := selectStandaloneNWCWallet(test.cfg)
			if ok != test.wantOK || id != test.wantID {
				t.Fatalf("selection = id:%q ok:%v, want id:%q ok:%v", id, ok, test.wantID, test.wantOK)
			}
		})
	}
}
