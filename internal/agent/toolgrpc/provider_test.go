package toolgrpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/config"

	"google.golang.org/protobuf/types/dynamicpb"
)

func TestProviderAutoExposureDefersLargeCatalogs(t *testing.T) {
	path := writeDescriptorSet(t, testDescriptorSet())
	cfg := config.GRPCConfig{Endpoints: []config.GRPCEndpointConfig{{
		ID:        "billing",
		Target:    "127.0.0.1:1",
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeDescriptorSet, DescriptorSet: path},
		Exposure: config.GRPCExposureConfig{
			Mode:              config.GRPCExposureModeAuto,
			DeferredThreshold: 4,
			Namespace:         "grpc_billing",
		},
	}}}
	provider, err := NewProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer provider.Close()

	registry := agent.NewToolRegistry()
	provider.RegisterInto(registry)
	descs := registry.Descriptors()
	if len(descs) != 5 { // two unary tools + server-stream start/receive/finish
		t.Fatalf("expected 5 generated gRPC descriptors, got %d: %#v", len(descs), descs)
	}
	for _, desc := range descs {
		if desc.Exposure != agent.ToolExposureModeDeferred {
			t.Fatalf("expected auto exposure to defer %s, got %q", desc.Name, desc.Exposure)
		}
	}
	partition := agent.PartitionTools(descs, 1_000_000, 10, nil)
	if partition.Deferred.Count() != len(descs) {
		t.Fatalf("expected all auto-deferred descriptors to partition as deferred, got %d of %d", partition.Deferred.Count(), len(descs))
	}
}

func TestProviderBuildsRegistrationsWithExposureTraitsAliases(t *testing.T) {
	path := writeDescriptorSet(t, testDescriptorSet())
	cfg := config.GRPCConfig{Endpoints: []config.GRPCEndpointConfig{{
		ID:        "billing",
		Target:    "127.0.0.1:1",
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeDescriptorSet, DescriptorSet: path},
		Exposure: config.GRPCExposureConfig{
			Mode:            config.GRPCExposureModeDeferred,
			Namespace:       "grpc_billing",
			IncludeServices: []string{"acme.billing.InvoiceService"},
			ExcludeMethods:  []string{"/acme.billing.InvoiceService/DeleteInvoice"},
		},
	}}}
	provider, err := NewProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer provider.Close()

	registry := agent.NewToolRegistry()
	provider.RegisterInto(registry)
	descs := registry.Descriptors()
	if len(descs) != 4 { // unary + server stream start/receive/finish
		t.Fatalf("expected 4 gRPC descriptors, got %d: %#v", len(descs), descs)
	}
	unary, ok := registry.Descriptor("grpc_billing_acme_billing_invoice_service_get_invoice")
	if !ok {
		t.Fatalf("missing unary gRPC tool; descriptors=%#v", descs)
	}
	if unary.Origin.Kind != agent.ToolOriginKindGRPC || unary.Origin.ServerName != "billing" || unary.Origin.CanonicalName != "/acme.billing.InvoiceService/GetInvoice" {
		t.Fatalf("unexpected origin: %#v", unary.Origin)
	}
	if unary.Exposure != agent.ToolExposureModeDeferred {
		t.Fatalf("exposure = %q, want deferred", unary.Exposure)
	}
	if unary.Traits.InterruptBehavior != agent.ToolInterruptBehaviorCancel || !unary.Traits.ConcurrencySafe {
		t.Fatalf("unexpected unary traits: %#v", unary.Traits)
	}
	if unary.ParamAliases["input"] != "request" || unary.ParamAliases["headers"] != "metadata" || unary.ParamAliases["timeout_ms"] != "deadline_ms" {
		t.Fatalf("missing aliases: %#v", unary.ParamAliases)
	}
	streamStart, ok := registry.Descriptor("grpc_billing_acme_billing_invoice_service_watch_invoices_start")
	if !ok {
		t.Fatalf("missing stream start descriptor")
	}
	if streamStart.Traits.ConcurrencySafe || streamStart.Traits.InterruptBehavior != agent.ToolInterruptBehaviorCancel {
		t.Fatalf("unexpected stream traits: %#v", streamStart.Traits)
	}

	partition := agent.PartitionTools(descs, 1_000_000, 10, nil)
	if partition.Deferred.Count() != len(descs) {
		t.Fatalf("forced deferred exposure not honored: deferred=%d descs=%d", partition.Deferred.Count(), len(descs))
	}
}

func TestProviderCloseWaitsForInFlightCall(t *testing.T) {
	method := invoiceUnaryMethodSpec(t)
	started := make(chan struct{})
	release := make(chan struct{})
	target := startUnaryInvoiceServer(t, method, func(_ context.Context, req, resp *dynamicpb.Message) error {
		close(started)
		<-release
		resp.Set(resp.Descriptor().Fields().ByName("id"), req.Get(req.Descriptor().Fields().ByName("id")))
		return nil
	})
	path := writeDescriptorSet(t, testDescriptorSet())
	cfg := config.GRPCConfig{Endpoints: []config.GRPCEndpointConfig{{
		ID:        "billing",
		Target:    target,
		Transport: config.GRPCTransportConfig{TLSMode: config.GRPCTransportTLSModeInsecure},
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeDescriptorSet, DescriptorSet: path},
		Exposure:  config.GRPCExposureConfig{Mode: config.GRPCExposureModeInline, Namespace: "grpc_billing"},
	}}}
	provider, err := NewProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	registry := agent.NewToolRegistry()
	provider.RegisterInto(registry)

	callDone := make(chan error, 1)
	go func() {
		_, execErr := registry.Execute(context.Background(), agent.ToolCall{
			Name: "grpc_billing_acme_billing_invoice_service_get_invoice",
			Args: map[string]any{"input": map[string]any{"id": "inv-123"}},
		})
		callDone <- execErr
	}()

	<-started
	closeDone := make(chan error, 1)
	go func() { closeDone <- provider.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("close returned before in-flight call completed: %v", err)
	default:
	}
	close(release)
	if err := <-callDone; err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestProviderUnaryEndToEndThroughRegistryHooksAndRedaction(t *testing.T) {
	method := invoiceUnaryMethodSpec(t)
	target := startUnaryInvoiceServer(t, method, func(_ context.Context, req, resp *dynamicpb.Message) error {
		resp.Set(resp.Descriptor().Fields().ByName("id"), req.Get(req.Descriptor().Fields().ByName("id")))
		return nil
	})
	path := writeDescriptorSet(t, testDescriptorSet())
	cfg := config.GRPCConfig{Endpoints: []config.GRPCEndpointConfig{{
		ID:        "billing",
		Target:    target,
		Transport: config.GRPCTransportConfig{TLSMode: config.GRPCTransportTLSModeInsecure},
		Discovery: config.GRPCDiscoveryConfig{Mode: config.GRPCDiscoveryModeDescriptorSet, DescriptorSet: path},
		Auth:      config.GRPCAuthConfig{Metadata: map[string]string{"authorization": "Bearer default-secret"}},
		Exposure: config.GRPCExposureConfig{
			Mode:            config.GRPCExposureModeInline,
			Namespace:       "grpc_billing",
			IncludeServices: []string{"acme.billing.InvoiceService"},
			ExcludeMethods:  []string{"/acme.billing.InvoiceService/DeleteInvoice", "/acme.billing.InvoiceService/WatchInvoices"},
		},
	}}}
	provider, err := NewProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	defer provider.Close()
	registry := agent.NewToolRegistry()
	provider.RegisterInto(registry)

	var hookResult string
	registry.AddPostExecuteHook(func(_ context.Context, _ agent.ToolCall, desc agent.ToolDescriptor, result string) (string, error) {
		if desc.Origin.Kind == agent.ToolOriginKindGRPC {
			hookResult = result
		}
		return result, nil
	})
	result, err := registry.Execute(context.Background(), agent.ToolCall{
		Name: "grpc_billing_acme_billing_invoice_service_get_invoice",
		Args: map[string]any{"input": map[string]any{"id": "inv-123"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if hookResult == "" {
		t.Fatal("expected registry post hook to observe gRPC result")
	}
	if strings.Contains(result, "default-secret") || strings.Contains(hookResult, "default-secret") {
		t.Fatalf("secret leaked into result/hook: result=%s hook=%s", result, hookResult)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result), &envelope); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if envelope["method"] != "/acme.billing.InvoiceService/GetInvoice" || envelope["profile"] != "billing" {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	defs := registry.Definitions()
	if len(defs) != 1 || defs[0].Name != "grpc_billing_acme_billing_invoice_service_get_invoice" {
		t.Fatalf("unexpected definitions: %#v", defs)
	}
}
