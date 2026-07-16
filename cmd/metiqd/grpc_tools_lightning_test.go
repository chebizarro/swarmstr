package main

import (
	"context"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

func TestDescriptorsEqualIncludesAliasesTraitsAndExposure(t *testing.T) {
	base := agent.ToolDescriptor{
		Name:            "lnd_get_info",
		Description:     "Get info",
		InputJSONSchema: map[string]any{"type": "object"},
		ParamAliases:    map[string]string{"input": "request"},
		Origin:          agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC, ServerName: "lnd:primary", CanonicalName: "/lnrpc.Lightning/GetInfo"},
		Traits:          agent.ToolTraits{ConcurrencySafe: true, ReadOnly: true, InterruptBehavior: agent.ToolInterruptBehaviorCancel},
		Exposure:        agent.ToolExposureModeDeferred,
	}
	cases := []struct {
		name   string
		mutate func(*agent.ToolDescriptor)
	}{
		{name: "aliases", mutate: func(d *agent.ToolDescriptor) { d.ParamAliases = map[string]string{"body": "request"} }},
		{name: "traits", mutate: func(d *agent.ToolDescriptor) { d.Traits.ReadOnly = false }},
		{name: "exposure", mutate: func(d *agent.ToolDescriptor) { d.Exposure = agent.ToolExposureModeInline }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			if descriptorsEqual(base, changed) {
				t.Fatalf("descriptorsEqual ignored %s change", test.name)
			}
		})
	}
}

func TestGRPCProviderControllerReconcilesFirstClassLNDReadProfile(t *testing.T) {
	registry := agent.NewToolRegistry()
	registry.RegisterTool("builtin_ping", agent.ToolRegistration{
		Func: func(context.Context, map[string]any) (string, error) { return "pong", nil },
		Descriptor: agent.ToolDescriptor{
			Name:   "builtin_ping",
			Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindBuiltin},
		},
	})
	doc := state.ConfigDoc{Extra: map[string]any{
		"lightning": map[string]any{
			"lnd": map[string]any{
				"profiles": []any{map[string]any{
					"id":            "primary",
					"target":        "127.0.0.1:10009",
					"network":       "regtest",
					"tls_cert_file": "/tmp/lnd-tls.cert",
					"macaroon": map[string]any{
						"ref":      "file:/tmp/readonly.macaroon",
						"encoding": "hex",
					},
					"payer_enabled": true,
				}},
			},
		},
	}}
	controller := &grpcProviderController{}
	result := controller.reconcile(context.Background(), registry, doc, "test")
	t.Cleanup(controller.close)
	if result.Added == 0 {
		t.Fatalf("first-class profile added no tools: %+v", result)
	}
	info, ok := registry.Descriptor("lnd_get_info")
	if !ok {
		t.Fatalf("lnd_get_info missing; descriptors=%#v", registry.Descriptors())
	}
	if info.Origin.ServerName != "lnd:primary" || !info.Traits.ReadOnly || info.Exposure == "" {
		t.Fatalf("unexpected lnd_get_info descriptor: %#v", info)
	}
	if _, exposed := registry.Descriptor("lnd_send_payment_start"); exposed {
		t.Fatal("payer_enabled exposed a spend tool")
	}
	if _, ok := registry.Descriptor("builtin_ping"); !ok {
		t.Fatal("first-class reconcile removed builtin tool")
	}

	removed := controller.reconcile(context.Background(), registry, state.ConfigDoc{}, "test-remove")
	if removed.Removed == 0 {
		t.Fatalf("profile removal removed no tools: %+v", removed)
	}
	if _, ok := registry.Descriptor("lnd_get_info"); ok {
		t.Fatal("lnd_get_info remained after profile removal")
	}
	if _, ok := registry.Descriptor("builtin_ping"); !ok {
		t.Fatal("profile removal removed builtin tool")
	}
}

func TestGRPCProviderControllerInvalidLightningProfileKeepsCurrentTools(t *testing.T) {
	registry := agent.NewToolRegistry()
	registry.RegisterTool("lnd_get_info", agent.ToolRegistration{
		Func: func(context.Context, map[string]any) (string, error) { return "ok", nil },
		Descriptor: agent.ToolDescriptor{
			Name:   "lnd_get_info",
			Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC, ServerName: "lnd:primary"},
		},
	})
	doc := state.ConfigDoc{Extra: map[string]any{
		"lightning": map[string]any{
			"lnd": map[string]any{"profiles": []any{map[string]any{
				"id": "primary", "target": "127.0.0.1:10009", "network": "invalid",
			}}},
		},
	}}
	controller := &grpcProviderController{}
	result := controller.reconcile(context.Background(), registry, doc, "test")
	if result.Changed() {
		t.Fatalf("invalid Lightning config changed tools: %+v", result)
	}
	if _, ok := registry.Descriptor("lnd_get_info"); !ok {
		t.Fatal("invalid Lightning profile removed previous working tool")
	}
}

func TestReconcileEqualDescriptorReplacesProviderClosure(t *testing.T) {
	registry := agent.NewToolRegistry()
	descriptor := agent.ToolDescriptor{
		Name:            "lnd_get_info",
		Description:     "Get info",
		InputJSONSchema: map[string]any{"type": "object"},
		Origin:          agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC, ServerName: "lnd:primary", CanonicalName: "/lnrpc.Lightning/GetInfo"},
		Traits:          agent.ToolTraits{ConcurrencySafe: true, ReadOnly: true, InterruptBehavior: agent.ToolInterruptBehaviorCancel},
	}
	registry.RegisterTool(descriptor.Name, agent.ToolRegistration{
		Func:       func(context.Context, map[string]any) (string, error) { return "old-provider", nil },
		Descriptor: descriptor,
	})
	result := reconcileGRPCToolRegistryDesired(registry, map[string]agent.ToolRegistration{
		descriptor.Name: {
			Func:       func(context.Context, map[string]any) (string, error) { return "new-provider", nil },
			Descriptor: descriptor,
		},
	})
	if result.Unchanged != 1 || result.Updated != 0 {
		t.Fatalf("reconcile result = %+v", result)
	}
	got, err := registry.Execute(context.Background(), agent.ToolCall{Name: descriptor.Name, Args: map[string]any{}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "new-provider" {
		t.Fatalf("equal descriptor retained stale closure: %q", got)
	}
}

func TestGRPCEndpointSourcesRejectGenericLightningIDCollision(t *testing.T) {
	doc := state.ConfigDoc{Extra: map[string]any{
		"grpc": map[string]any{"endpoints": []any{map[string]any{"id": "PRIMARY", "target": "127.0.0.1:1"}}},
		"lightning": map[string]any{"lnd": map[string]any{"profiles": []any{map[string]any{
			"id": "primary", "target": "127.0.0.1:10009", "network": "regtest", "tls_cert_file": "/tmp/tls.cert",
			"macaroon": map[string]any{"ref": "file:/tmp/readonly.macaroon", "encoding": "hex"},
		}}}},
	}}
	if _, err := grpcEndpointSourcesFromConfigDoc(doc); err == nil {
		t.Fatal("generic and Lightning profile IDs collided case-insensitively")
	}
}
