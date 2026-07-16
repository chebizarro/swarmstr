package toolgrpc

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/config"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestProviderEmbeddedSourceInvokesDynamicGRPCWithoutReflection(t *testing.T) {
	method := invoiceUnaryMethodSpec(t)
	target := startUnaryInvoiceServer(t, method, func(ctx context.Context, req, resp *dynamicpb.Message) error {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok || len(md.Get("macaroon")) != 1 || md.Get("macaroon")[0] != "0102" {
			t.Fatalf("macaroon metadata = %v", md.Get("macaroon"))
		}
		resp.Set(resp.Descriptor().Fields().ByName("id"), req.Get(req.Descriptor().Fields().ByName("id")))
		return nil
	})
	fullMethod := "/acme.billing.InvoiceService/GetInvoice"
	provider, err := NewProviderFromSources(context.Background(), []EndpointSource{{
		Profile: config.GRPCEndpointConfig{
			ID:        "lnd:primary",
			Target:    target,
			Transport: config.GRPCTransportConfig{TLSMode: config.GRPCTransportTLSModeInsecure},
			Exposure:  config.GRPCExposureConfig{Mode: config.GRPCExposureModeInline},
		},
		DescriptorSet: testDescriptorSet(),
		ToolNames:     map[string]string{fullMethod: "lnd_get_invoice"},
		ToolTraits: map[string]agent.ToolTraits{
			fullMethod: {ConcurrencySafe: true, ReadOnly: true, InterruptBehavior: agent.ToolInterruptBehaviorCancel},
		},
		MetadataSources: map[string]CredentialSource{
			"macaroon": {Ref: "secret:LND_MACAROON", Encoding: "hex"},
		},
	}}, WithValueResolver(ValueResolverFunc(func(context.Context, CredentialSource) ([]byte, error) {
		return []byte("0102"), nil
	})))
	if err != nil {
		t.Fatalf("NewProviderFromSources: %v", err)
	}
	t.Cleanup(func() { _ = provider.Close() })

	registry := agent.NewToolRegistry()
	provider.RegisterInto(registry)
	desc, ok := registry.Descriptor("lnd_get_invoice")
	if !ok {
		t.Fatalf("stable tool missing; descriptors=%v", registry.Descriptors())
	}
	if desc.Origin.ServerName != "lnd:primary" || desc.Origin.CanonicalName != fullMethod {
		t.Fatalf("unexpected origin: %#v", desc.Origin)
	}
	if !desc.Traits.ReadOnly || desc.Traits.Destructive {
		t.Fatalf("unexpected curated traits: %#v", desc.Traits)
	}
	if len(registry.Descriptors()) != 1 {
		t.Fatalf("embedded source exposed non-curated methods: %#v", registry.Descriptors())
	}

	result, err := registry.Execute(context.Background(), agent.ToolCall{
		Name: "lnd_get_invoice",
		Args: map[string]any{"request": map[string]any{"id": "inv-42"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "inv-42") {
		t.Fatalf("dynamic result = %s", result)
	}
}

func TestProviderEmbeddedSourceRejectsManifestDrift(t *testing.T) {
	_, err := NewProviderFromSources(context.Background(), []EndpointSource{{
		Profile:       config.GRPCEndpointConfig{ID: "tapd:assets", Target: "127.0.0.1:1"},
		DescriptorSet: testDescriptorSet(),
		ToolNames: map[string]string{
			"/taprpc.TaprootAssets/MethodRemovedUpstream": "tap_removed",
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "missing from descriptor set") {
		t.Fatalf("expected curated manifest drift error, got %v", err)
	}
}

func TestProviderEmbeddedStreamingOverrideKeepsLifecycleSuffixes(t *testing.T) {
	fullMethod := "/acme.billing.InvoiceService/WatchInvoices"
	provider, err := NewProviderFromSources(context.Background(), []EndpointSource{{
		Profile:       config.GRPCEndpointConfig{ID: "tapd:assets", Target: "127.0.0.1:1"},
		DescriptorSet: testDescriptorSet(),
		ToolNames:     map[string]string{fullMethod: "tap_watch_invoices"},
	}})
	if err != nil {
		t.Fatalf("NewProviderFromSources: %v", err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	names := map[string]bool{}
	for _, registration := range provider.Registrations() {
		names[registration.Descriptor.Name] = true
	}
	for _, want := range []string{"tap_watch_invoices_start", "tap_watch_invoices_receive", "tap_watch_invoices_finish"} {
		if !names[want] {
			t.Fatalf("missing streaming tool %q from %#v", want, names)
		}
	}
}

func TestProviderCrossProfileCollisionsSuffixAllToolsDeterministically(t *testing.T) {
	fullMethod := "/acme.billing.InvoiceService/GetInvoice"
	source := func(id string) EndpointSource {
		return EndpointSource{
			Profile:       config.GRPCEndpointConfig{ID: id, Target: "127.0.0.1:1"},
			DescriptorSet: testDescriptorSet(),
			ToolNames:     map[string]string{fullMethod: "lnd_get_invoice"},
		}
	}
	provider, err := NewProviderFromSources(context.Background(), []EndpointSource{source("lnd:first"), source("lnd:second")})
	if err != nil {
		t.Fatalf("NewProviderFromSources: %v", err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	registrations := provider.Registrations()
	if len(registrations) != 2 {
		t.Fatalf("registrations=%d, want 2", len(registrations))
	}
	first := registrations[0].Descriptor.Name
	second := registrations[1].Descriptor.Name
	if first == second || first == "lnd_get_invoice" || second == "lnd_get_invoice" {
		t.Fatalf("cross-profile names are not uniquely and symmetrically suffixed: %q %q", first, second)
	}
	if !strings.HasPrefix(first, "lnd_get_invoice_") || !strings.HasPrefix(second, "lnd_get_invoice_") {
		t.Fatalf("unexpected collision names: %q %q", first, second)
	}

	reversed, err := NewProviderFromSources(context.Background(), []EndpointSource{source("lnd:second"), source("lnd:first")})
	if err != nil {
		t.Fatalf("NewProviderFromSources reversed: %v", err)
	}
	t.Cleanup(func() { _ = reversed.Close() })
	names := map[string]bool{first: true, second: true}
	for _, registration := range reversed.Registrations() {
		if !names[registration.Descriptor.Name] {
			t.Fatalf("source order changed stable collision name to %q", registration.Descriptor.Name)
		}
	}
}

func TestProviderEmbeddedSourceRequiresExactAllowlist(t *testing.T) {
	_, err := NewProviderFromSources(context.Background(), []EndpointSource{{
		Profile:       config.GRPCEndpointConfig{ID: "lnd:unsafe", Target: "127.0.0.1:1"},
		DescriptorSet: testDescriptorSet(),
	}})
	if err == nil || !strings.Contains(err.Error(), "requires an exact tool allowlist") {
		t.Fatalf("embedded source without allowlist error = %v", err)
	}
}
