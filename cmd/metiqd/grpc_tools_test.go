package main

import (
	"context"
	"testing"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

func TestGRPCProviderControllerReconcileRemovesGRPCToolsWhenConfigRemoved(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterTool("grpc_demo_call", agent.ToolRegistration{
		Func: func(context.Context, map[string]any) (string, error) { return "ok", nil },
		Descriptor: agent.ToolDescriptor{
			Name:   "grpc_demo_call",
			Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC},
		},
	})
	reg.RegisterTool("builtin_ping", agent.ToolRegistration{
		Func: func(context.Context, map[string]any) (string, error) { return "pong", nil },
		Descriptor: agent.ToolDescriptor{
			Name:   "builtin_ping",
			Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindBuiltin},
		},
	})

	ctl := &grpcProviderController{}
	result := ctl.reconcile(context.Background(), reg, state.ConfigDoc{}, "test")
	if result.Removed != 1 {
		t.Fatalf("removed=%d, want 1", result.Removed)
	}
	if _, ok := reg.Descriptor("grpc_demo_call"); ok {
		t.Fatal("grpc tool still registered after reconcile")
	}
	if _, ok := reg.Descriptor("builtin_ping"); !ok {
		t.Fatal("non-grpc tool should be preserved")
	}
}

func TestReconcileGRPCToolRegistryDesiredConflictPreservesExistingOwner(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterTool("shared_tool", agent.ToolRegistration{
		Func: func(context.Context, map[string]any) (string, error) { return "builtin", nil },
		Descriptor: agent.ToolDescriptor{
			Name:   "shared_tool",
			Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindBuiltin},
		},
	})

	result := reconcileGRPCToolRegistryDesired(reg, map[string]agent.ToolRegistration{
		"shared_tool": {
			Func: func(context.Context, map[string]any) (string, error) { return "grpc", nil },
			Descriptor: agent.ToolDescriptor{
				Name:        "shared_tool",
				Description: "grpc version",
				Origin:      agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC},
			},
		},
	})
	if result.Conflicts != 1 {
		t.Fatalf("conflicts=%d, want 1", result.Conflicts)
	}
	if result.Added != 0 {
		t.Fatalf("added=%d, want 0", result.Added)
	}
	desc, ok := reg.Descriptor("shared_tool")
	if !ok {
		t.Fatal("shared tool missing after reconcile")
	}
	if desc.Origin.Kind != agent.ToolOriginKindBuiltin {
		t.Fatalf("origin=%q, want %q", desc.Origin.Kind, agent.ToolOriginKindBuiltin)
	}
}

func TestReconcileGRPCToolRegistryDesiredSemanticEqualityIgnoresDerivedFields(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterTool("grpc_semantic", agent.ToolRegistration{
		Func: func(context.Context, map[string]any) (string, error) { return "ok", nil },
		Descriptor: agent.ToolDescriptor{
			Name:            "grpc_semantic",
			Description:     "semantic",
			Parameters:      agent.ToolParameters{Type: "object", Required: []string{"legacy"}},
			InputJSONSchema: map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}},
			Origin:          agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC, ServerName: "billing", CanonicalName: "/svc.Method"},
		},
	})

	result := reconcileGRPCToolRegistryDesired(reg, map[string]agent.ToolRegistration{
		"grpc_semantic": {
			Func: func(context.Context, map[string]any) (string, error) { return "ok", nil },
			Descriptor: agent.ToolDescriptor{
				Name:            "grpc_semantic",
				Description:     "semantic",
				Parameters:      agent.ToolParameters{Type: "object", Required: []string{"new"}},
				InputJSONSchema: map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}},
				Origin:          agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC, ServerName: "billing", CanonicalName: "/svc.Method"},
			},
		},
	})
	if result.Unchanged != 1 {
		t.Fatalf("unchanged=%d, want 1", result.Unchanged)
	}
	if result.Updated != 0 {
		t.Fatalf("updated=%d, want 0", result.Updated)
	}
}

func TestGRPCProviderControllerInvalidConfigKeepsCurrentTools(t *testing.T) {
	reg := agent.NewToolRegistry()
	reg.RegisterTool("grpc_demo_call", agent.ToolRegistration{
		Func: func(context.Context, map[string]any) (string, error) { return "ok", nil },
		Descriptor: agent.ToolDescriptor{
			Name:   "grpc_demo_call",
			Origin: agent.ToolOrigin{Kind: agent.ToolOriginKindGRPC},
		},
	})

	ctl := &grpcProviderController{}
	doc := state.ConfigDoc{Extra: map[string]any{"grpc": map[string]any{"endpoints": "not-an-array"}}}
	result := ctl.reconcile(context.Background(), reg, doc, "test")
	if result.Changed() {
		t.Fatalf("unexpected changes for invalid config: %+v", result)
	}
	if _, ok := reg.Descriptor("grpc_demo_call"); !ok {
		t.Fatal("grpc tool should remain when new config is invalid")
	}
}
