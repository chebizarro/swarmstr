package toolgrpc

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestProviderCloseDoubleCloseRaceDoesNotPanic(t *testing.T) {
	for i := 0; i < 100; i++ {
		provider := newProvider(nil)
		if !provider.beginCall() {
			t.Fatal("beginCall returned false before close")
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = provider.Close()
		}()
		go func() {
			defer wg.Done()
			provider.endCall()
		}()
		wg.Wait()

		if err := provider.Close(); err != nil {
			t.Fatalf("second Close: %v", err)
		}
	}
}

func TestProviderCloseClosesStreamManagersBeforeConnections(t *testing.T) {
	baseManager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	provider := newProvider(baseManager.connManager)
	method := methodByName(t, methods, "Bidi")
	ctx := agent.ContextWithToolLifecycle(context.Background(), agent.ToolLifecycleContext{SessionID: "sess-close", TurnID: "turn-close"})

	manager := provider.streamManagerForContext(ctx)
	startRaw, err := manager.Start(ctx, method, nil, "bidi_start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	streamID := decodeField[string](t, startRaw, "stream_id")
	if streamID == "" {
		t.Fatalf("missing stream id in %s", startRaw)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !manager.isClosed() {
		t.Fatal("stream manager was not closed by provider Close")
	}
	provider.mu.Lock()
	remaining := len(provider.streamManagers)
	provider.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("provider retained %d stream managers after Close", remaining)
	}
	if _, err := manager.Send(ctx, map[string]any{"stream_id": streamID, "message": map[string]any{"text": "late"}}, "bidi_send"); err == nil || !strings.Contains(err.Error(), "stream manager is closed") {
		t.Fatalf("expected closed stream manager error after provider Close, got %v", err)
	}
}

func TestProviderStreamManagersCleanedUpAfterIdle(t *testing.T) {
	baseManager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	provider := newProvider(baseManager.connManager)
	provider.streamManagerIdleTTL = 0
	method := methodByName(t, methods, "Bidi")
	ctx := agent.ContextWithToolLifecycle(context.Background(), agent.ToolLifecycleContext{SessionID: "sess-idle", TurnID: "turn-idle"})

	manager := provider.streamManagerForContext(ctx)
	startRaw, err := manager.Start(ctx, method, nil, "bidi_start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	streamID := decodeField[string](t, startRaw, "stream_id")
	if _, err := manager.Finish(ctx, map[string]any{"stream_id": streamID}, "bidi_finish"); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	provider.mu.Lock()
	remaining := len(provider.streamManagers)
	provider.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("provider retained %d idle stream managers", remaining)
	}
	if !manager.isClosed() {
		t.Fatal("idle cleanup removed manager without closing it")
	}
}

func TestProviderStreamManagerIdleCleanupKeepsActiveManager(t *testing.T) {
	baseManager, methods, cleanup := startStreamManagerTest(t, nil)
	defer cleanup()
	provider := newProvider(baseManager.connManager)
	method := methodByName(t, methods, "Bidi")
	ctx := agent.ContextWithToolLifecycle(context.Background(), agent.ToolLifecycleContext{SessionID: "sess-active", TurnID: "turn-active"})

	manager := provider.streamManagerForContext(ctx)
	startRaw, err := manager.Start(ctx, method, nil, "bidi_start")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	streamID := decodeField[string](t, startRaw, "stream_id")
	provider.cleanupStreamManager("sess-active\x00turn-active", manager)

	provider.mu.Lock()
	remaining := len(provider.streamManagers)
	provider.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("active manager was cleaned up; remaining=%d", remaining)
	}
	if manager.isClosed() {
		t.Fatal("active manager was closed by idle cleanup")
	}
	if _, err := manager.Finish(ctx, map[string]any{"stream_id": streamID}, "bidi_finish"); err != nil {
		t.Fatalf("Finish: %v", err)
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
	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Execute")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Close")
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
