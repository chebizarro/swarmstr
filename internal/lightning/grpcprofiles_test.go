package lightning

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/agent/toolgrpc"
	"metiq/internal/config"
)

func TestBundledDescriptorManifestIsComplete(t *testing.T) {
	if err := ValidateBundledDescriptors(); err != nil {
		t.Fatalf("ValidateBundledDescriptors: %v", err)
	}
	for _, family := range []string{FamilyLND, FamilyTapd} {
		set, err := BundledDescriptorSet(family)
		if err != nil {
			t.Fatalf("BundledDescriptorSet(%s): %v", family, err)
		}
		if len(set.File) == 0 {
			t.Fatalf("%s descriptor set is empty", family)
		}
	}
}

func TestBuildGRPCEndpointSourcesDefaultsToReadOnly(t *testing.T) {
	cfg := config.LightningConfig{
		LND: config.LNDProfilesConfig{Profiles: []config.LightningGRPCProfile{{
			ID:          "primary",
			Target:      "127.0.0.1:10009",
			Network:     config.LightningNetworkMainnet,
			TLSCertFile: "/tmp/lnd-tls.cert",
			ServerName:  "localhost",
			Macaroon: config.CredentialSourceConfig{
				Ref:      "file:/tmp/readonly.macaroon",
				Encoding: config.CredentialEncodingHex,
			},
			PayerEnabled: true,
		}}},
		Tapd: config.TapdProfilesConfig{Profiles: []config.LightningGRPCProfile{{
			ID:          "assets",
			Target:      "127.0.0.1:10029",
			Network:     config.LightningNetworkMainnet,
			TLSCertFile: "/tmp/tapd-tls.cert",
			Macaroon: config.CredentialSourceConfig{
				Ref:      "secret:TAPD_MACAROON",
				Encoding: config.CredentialEncodingText,
			},
		}}},
	}
	sources, err := BuildGRPCEndpointSources(cfg)
	if err != nil {
		t.Fatalf("BuildGRPCEndpointSources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("sources=%d, want 2", len(sources))
	}
	for _, source := range sources {
		if source.Profile.Transport.TLSMode != config.GRPCTransportTLSModeCustomCA {
			t.Fatalf("%s TLS mode = %q", source.Profile.ID, source.Profile.Transport.TLSMode)
		}
		if source.Profile.Exposure.Namespace != "" {
			t.Fatalf("%s namespace should not override stable names", source.Profile.ID)
		}
		for rpc, name := range source.ToolNames {
			if strings.Contains(name, "send_payment") || strings.Contains(name, "send_asset") || strings.Contains(name, "add_invoice") || strings.Contains(name, "new_address") {
				t.Fatalf("default source exposed non-read method %s as %s", rpc, name)
			}
			if traits := source.ToolTraits[rpc]; !traits.ReadOnly || traits.Destructive {
				t.Fatalf("default method %s has unsafe traits %#v", rpc, traits)
			}
		}
	}
	if sources[0].Profile.ID != "lnd:primary" || sources[0].ToolNames["/lnrpc.Lightning/GetInfo"] != "lnd_get_info" {
		t.Fatalf("unexpected LND source: %#v", sources[0])
	}
	if _, exposed := sources[0].ToolNames["/routerrpc.Router/SendPaymentV2"]; exposed {
		t.Fatal("payer_enabled implicitly exposed SendPaymentV2")
	}
	if got := sources[0].MetadataSources["macaroon"]; got != (toolgrpc.CredentialSource{Ref: "file:/tmp/readonly.macaroon", Encoding: "hex"}) {
		t.Fatalf("LND macaroon source = %#v", got)
	}
	if sources[1].Profile.ID != "tapd:assets" || sources[1].ToolNames["/taprpc.TaprootAssets/GetInfo"] != "tap_get_info" {
		t.Fatalf("unexpected tapd source: %#v", sources[1])
	}
}

func TestBuildGRPCEndpointSourcesToolsetsAndStableStreamingNames(t *testing.T) {
	cfg := config.LightningConfig{LND: config.LNDProfilesConfig{Profiles: []config.LightningGRPCProfile{{
		ID:          "operator",
		Target:      "127.0.0.1:10009",
		Network:     config.LightningNetworkRegtest,
		TLSCertFile: "/tmp/tls.cert",
		Macaroon:    config.CredentialSourceConfig{Ref: "secret:LND_MACAROON", Encoding: "hex"},
		Toolsets:    []string{config.LightningToolsetRead, config.LightningToolsetReceive, config.LightningToolsetSpend},
		Exposure:    config.GRPCExposureConfig{Mode: config.GRPCExposureModeDeferred},
	}}}}
	sources, err := BuildGRPCEndpointSources(cfg)
	if err != nil {
		t.Fatalf("BuildGRPCEndpointSources: %v", err)
	}
	source := sources[0]
	for rpc, name := range map[string]string{
		"/lnrpc.Lightning/GetInfo":        "lnd_get_info",
		"/lnrpc.Lightning/AddInvoice":     "lnd_add_invoice",
		"/routerrpc.Router/SendPaymentV2": "lnd_send_payment",
	} {
		if got := source.ToolNames[rpc]; got != name {
			t.Fatalf("tool name for %s = %q, want %q", rpc, got, name)
		}
	}
	provider, err := toolgrpc.NewProviderFromSources(context.Background(), sources)
	if err != nil {
		t.Fatalf("NewProviderFromSources: %v", err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	names := map[string]bool{}
	for _, registration := range provider.Registrations() {
		names[registration.Descriptor.Name] = true
	}
	for _, want := range []string{"lnd_get_info", "lnd_add_invoice", "lnd_send_payment_start", "lnd_send_payment_receive", "lnd_send_payment_finish"} {
		if !names[want] {
			t.Fatalf("missing curated tool %q", want)
		}
	}
	if names["lnd_stop_daemon"] {
		t.Fatal("admin tool exposed without admin toolset")
	}
}

func TestBundledDescriptorSetReturnsIsolatedCopies(t *testing.T) {
	first, err := BundledDescriptorSet(FamilyLND)
	if err != nil {
		t.Fatal(err)
	}
	first.File = nil
	second, err := BundledDescriptorSet(FamilyLND)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.File) == 0 {
		t.Fatal("mutating one descriptor copy changed bundled assets")
	}
}
