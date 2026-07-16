//go:build lightning_integration

package lightning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"metiq/internal/agent"
	"metiq/internal/agent/toolgrpc"
	"metiq/internal/config"
)

func TestContainerRegtestInteroperability(t *testing.T) {
	requireDocker(t)

	payer := newIntegrationRegistry(t, FamilyLND, "payer", env(t, "METIQ_LND_A_TARGET"), env(t, "METIQ_LND_A_TLS_CERT"), env(t, "METIQ_LND_A_MACAROON"))
	payee := newIntegrationRegistry(t, FamilyLND, "payee", env(t, "METIQ_LND_B_TARGET"), env(t, "METIQ_LND_B_TLS_CERT"), env(t, "METIQ_LND_B_MACAROON"))
	tapd := newIntegrationRegistry(t, FamilyTapd, "tapd", env(t, "METIQ_TAPD_TARGET"), env(t, "METIQ_TAPD_TLS_CERT"), env(t, "METIQ_TAPD_MACAROON"))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	mustTool(t, ctx, payer, "lnd_get_info", map[string]any{})
	mustTool(t, ctx, payer, "lnd_wallet_balance", map[string]any{})
	mustTool(t, ctx, payer, "lnd_channel_balance", map[string]any{})

	invoiceRaw := mustTool(t, ctx, payee, "lnd_add_invoice", map[string]any{
		"memo":  "metiq pinned-descriptor interoperability",
		"value": 1000,
	})
	var invoice map[string]any
	if err := json.Unmarshal([]byte(invoiceRaw), &invoice); err != nil {
		t.Fatalf("decode AddInvoice response: %v", err)
	}
	paymentRequest, _ := invoice["paymentRequest"].(string)
	if paymentRequest == "" {
		t.Fatalf("AddInvoice response has no paymentRequest: %s", invoiceRaw)
	}

	startRaw := mustTool(t, ctx, payer, "lnd_send_payment_start", map[string]any{
		"payment_request":     paymentRequest,
		"fee_limit_sat":       100,
		"timeout_seconds":     30,
		"no_inflight_updates": true,
	})
	var started struct {
		StreamID string `json:"stream_id"`
	}
	if err := json.Unmarshal([]byte(startRaw), &started); err != nil || started.StreamID == "" {
		t.Fatalf("decode SendPaymentV2 start response: %v: %s", err, startRaw)
	}
	paymentRaw := mustToolArgs(t, ctx, payer, "lnd_send_payment_receive", map[string]any{
		"stream_id":    started.StreamID,
		"max_messages": 10,
	})
	if !strings.Contains(paymentRaw, "SUCCEEDED") {
		t.Fatalf("controlled payment did not succeed: %s", paymentRaw)
	}

	mustTool(t, ctx, tapd, "tap_get_info", map[string]any{})
	assetsRaw := mustTool(t, ctx, tapd, "tap_list_assets", map[string]any{})
	assetID := findStringField(t, assetsRaw, "assetId")
	addressRaw := mustTool(t, ctx, tapd, "tap_new_address", map[string]any{
		"asset_id": assetID,
		"amt":      1,
	})
	if !strings.Contains(addressRaw, "encodedAddr") {
		t.Fatalf("NewAddr response has no encodedAddr: %s", addressRaw)
	}
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("lightning integration requires Docker: docker executable was not found")
	}
	if output, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Fatalf("lightning integration requires a running Docker daemon: %v\n%s", err, strings.TrimSpace(string(output)))
	}
}

func env(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required; run internal/lightning/integration/run.sh", name)
	}
	return value
}

func newIntegrationRegistry(t *testing.T, family, id, target, cert, macaroon string) *agent.ToolRegistry {
	t.Helper()
	profile := config.LightningGRPCProfile{
		ID:          id,
		Target:      target,
		Network:     config.LightningNetworkRegtest,
		TLSCertFile: cert,
		ServerName:  "localhost",
		Macaroon: config.CredentialSourceConfig{
			Ref:      "file:" + macaroon,
			Encoding: config.CredentialEncodingHex,
		},
		Toolsets: []string{
			config.LightningToolsetRead,
			config.LightningToolsetReceive,
			config.LightningToolsetSpend,
			config.LightningToolsetAdmin,
		},
		Exposure: config.GRPCExposureConfig{Mode: config.GRPCExposureModeInline},
	}
	var cfg config.LightningConfig
	if family == FamilyLND {
		cfg.LND.Profiles = []config.LightningGRPCProfile{profile}
	} else {
		cfg.Tapd.Profiles = []config.LightningGRPCProfile{profile}
	}
	sources, err := BuildGRPCEndpointSources(cfg)
	if err != nil {
		t.Fatalf("BuildGRPCEndpointSources(%s): %v", family, err)
	}
	provider, err := toolgrpc.NewProviderFromSources(context.Background(), sources)
	if err != nil {
		t.Fatalf("NewProviderFromSources(%s): %v", family, err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	registry := agent.NewToolRegistry()
	provider.RegisterInto(registry)
	return registry
}

func mustTool(t *testing.T, ctx context.Context, registry *agent.ToolRegistry, name string, request map[string]any) string {
	t.Helper()
	return mustToolArgs(t, ctx, registry, name, map[string]any{"request": request})
}

func mustToolArgs(t *testing.T, ctx context.Context, registry *agent.ToolRegistry, name string, args map[string]any) string {
	t.Helper()
	result, err := registry.Execute(ctx, agent.ToolCall{Name: name, Args: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func findStringField(t *testing.T, raw, name string) string {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("decode response while finding %s: %v", name, err)
	}
	var walk func(any) string
	walk = func(current any) string {
		switch typed := current.(type) {
		case map[string]any:
			if found, ok := typed[name].(string); ok && found != "" {
				return found
			}
			for _, child := range typed {
				if found := walk(child); found != "" {
					return found
				}
			}
		case []any:
			for _, child := range typed {
				if found := walk(child); found != "" {
					return found
				}
			}
		}
		return ""
	}
	if found := walk(value); found != "" {
		return found
	}
	t.Fatalf("%s response has no non-empty %s: %s", fmt.Sprintf("%T", value), name, raw)
	return ""
}
