package agent

import (
	"context"
	"errors"
	"testing"
)

func TestToolRegistry_ExecuteBasic(t *testing.T) {
	r := NewToolRegistry()
	r.Register("echo", func(_ context.Context, args map[string]any) (string, error) {
		return ArgString(args, "text"), nil
	})

	result, err := r.Execute(context.Background(), ToolCall{Name: "echo", Args: map[string]any{"text": "hello"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Fatalf("expected 'hello', got %q", result)
	}
}

func TestToolRegistry_UnknownTool(t *testing.T) {
	r := NewToolRegistry()
	_, err := r.Execute(context.Background(), ToolCall{Name: "nope"})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestToolRegistry_RegisterWithDescriptor_ProjectsDefinition(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterWithDescriptor("plugin/echo", func(_ context.Context, args map[string]any) (string, error) {
		return ArgString(args, "message"), nil
	}, ToolDescriptor{
		Description: "echo a plugin message",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolParamProp{
				"message": {Type: "string", Description: "message to echo"},
			},
			Required: []string{"message"},
		},
		Origin: ToolOrigin{
			Kind:          ToolOriginKindPlugin,
			PluginID:      "plugin",
			CanonicalName: "echo",
		},
	})

	desc, ok := r.Descriptor("plugin/echo")
	if !ok {
		t.Fatal("expected descriptor")
	}
	if desc.Traits.InterruptBehavior != ToolInterruptBehaviorBlock {
		t.Fatalf("expected default interrupt behavior %q, got %q", ToolInterruptBehaviorBlock, desc.Traits.InterruptBehavior)
	}
	if desc.Origin.Kind != ToolOriginKindPlugin || desc.Origin.PluginID != "plugin" || desc.Origin.CanonicalName != "echo" {
		t.Fatalf("unexpected origin: %+v", desc.Origin)
	}

	defs := r.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if defs[0].Name != "plugin/echo" || defs[0].Description != "echo a plugin message" {
		t.Fatalf("unexpected definition: %+v", defs[0])
	}
}

func TestToolInputSchemaMap_PreservesRawJSONSchema(t *testing.T) {
	schema := toolInputSchemaMap(ToolDefinition{
		Name: "plugin/raw",
		InputJSONSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filters": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"kind": map[string]any{"type": "integer"},
						},
					},
				},
			},
		},
	})
	props, _ := schema["properties"].(map[string]any)
	filters, _ := props["filters"].(map[string]any)
	items, _ := filters["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	kind, _ := itemProps["kind"].(map[string]any)
	if kind["type"] != "integer" {
		t.Fatalf("expected nested raw schema to survive, got %+v", schema)
	}
}

func TestToolRegistry_MiddlewarePassthrough(t *testing.T) {
	r := NewToolRegistry()
	r.Register("greet", func(_ context.Context, args map[string]any) (string, error) {
		return "hi", nil
	})

	var intercepted string
	r.SetMiddleware(func(ctx context.Context, call ToolCall, next func(context.Context, ToolCall) (string, error)) (string, error) {
		intercepted = call.Name
		return next(ctx, call)
	})

	result, err := r.Execute(context.Background(), ToolCall{Name: "greet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hi" {
		t.Fatalf("expected 'hi', got %q", result)
	}
	if intercepted != "greet" {
		t.Fatalf("middleware not called")
	}
}

func TestToolRegistry_MiddlewareBlock(t *testing.T) {
	r := NewToolRegistry()
	r.Register("danger", func(_ context.Context, args map[string]any) (string, error) {
		return "executed", nil
	})

	r.SetMiddleware(func(ctx context.Context, call ToolCall, next func(context.Context, ToolCall) (string, error)) (string, error) {
		if call.Name == "danger" {
			return "", errors.New("blocked by middleware")
		}
		return next(ctx, call)
	})

	_, err := r.Execute(context.Background(), ToolCall{Name: "danger"})
	if err == nil {
		t.Fatal("expected middleware to block execution")
	}
	if err.Error() != "blocked by middleware" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolRegistry_MiddlewareOnlyAppliesToGated(t *testing.T) {
	r := NewToolRegistry()
	r.Register("safe", func(_ context.Context, args map[string]any) (string, error) {
		return "ok", nil
	})
	r.Register("danger", func(_ context.Context, args map[string]any) (string, error) {
		return "executed", nil
	})

	r.SetMiddleware(func(ctx context.Context, call ToolCall, next func(context.Context, ToolCall) (string, error)) (string, error) {
		if call.Name == "danger" {
			return "", errors.New("denied")
		}
		return next(ctx, call)
	})

	// safe should pass through
	result, err := r.Execute(context.Background(), ToolCall{Name: "safe"})
	if err != nil {
		t.Fatalf("safe tool: unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("safe tool: expected 'ok', got %q", result)
	}

	// danger should be blocked
	_, err = r.Execute(context.Background(), ToolCall{Name: "danger"})
	if err == nil {
		t.Fatal("danger tool: expected block")
	}
}

func TestToolRegistry_RemoveMiddleware(t *testing.T) {
	r := NewToolRegistry()
	r.Register("t", func(_ context.Context, _ map[string]any) (string, error) {
		return "raw", nil
	})

	r.SetMiddleware(func(ctx context.Context, call ToolCall, next func(context.Context, ToolCall) (string, error)) (string, error) {
		return "intercepted", nil
	})
	result, _ := r.Execute(context.Background(), ToolCall{Name: "t"})
	if result != "intercepted" {
		t.Fatalf("expected middleware result, got %q", result)
	}

	// Remove middleware
	r.SetMiddleware(nil)
	result, _ = r.Execute(context.Background(), ToolCall{Name: "t"})
	if result != "raw" {
		t.Fatalf("after removing middleware, expected 'raw', got %q", result)
	}
}

func TestResolveSessionIDStrict_UsesContextFallback(t *testing.T) {
	ctx := ContextWithSessionID(context.Background(), "sess-ctx")
	got, err := ResolveSessionIDStrict(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sess-ctx" {
		t.Fatalf("expected context session id, got %q", got)
	}
}

func TestResolveSessionIDStrict_RejectsNonStringArg(t *testing.T) {
	ctx := ContextWithSessionID(context.Background(), "sess-ctx")
	_, err := ResolveSessionIDStrict(ctx, map[string]any{"session_id": float64(123)})
	if err == nil {
		t.Fatal("expected error for non-string session_id")
	}
}
