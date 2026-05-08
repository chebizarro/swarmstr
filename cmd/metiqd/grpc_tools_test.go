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
