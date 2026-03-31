package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestToolRegistry_Register_HiddenFromDefinitions(t *testing.T) {
	r := NewToolRegistry()
	r.Register("internal_only", func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil })
	if defs := r.Definitions(); len(defs) != 0 {
		t.Fatalf("expected bare Register tool to stay hidden from provider definitions, got %+v", defs)
	}
	desc, ok := r.Descriptor("internal_only")
	if !ok {
		t.Fatal("expected descriptor for bare registration")
	}
	if desc.Origin.Kind != ToolOriginKindBuiltin {
		t.Fatalf("expected builtin origin, got %+v", desc.Origin)
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

func TestToolRegistry_SchemaValidation_BlocksExecution(t *testing.T) {
	r := NewToolRegistry()
	called := false
	r.RegisterTool("typed", ToolRegistration{
		Func: func(_ context.Context, args map[string]any) (string, error) {
			called = true
			return ArgString(args, "message"), nil
		},
		Descriptor: ToolDescriptor{
			Parameters: ToolParameters{
				Type: "object",
				Properties: map[string]ToolParamProp{
					"count": {Type: "integer"},
				},
				Required: []string{"count"},
			},
		},
	})

	_, err := r.Execute(context.Background(), ToolCall{Name: "typed", Args: map[string]any{"count": "oops"}})
	if err == nil {
		t.Fatal("expected validation error")
	}
	var execErr *ToolExecutionError
	if !errors.As(err, &execErr) || execErr.Phase != ToolExecutionPhaseSchemaValidation {
		t.Fatalf("expected schema validation phase error, got %T %v", err, err)
	}
	if called {
		t.Fatal("tool function should not run when schema validation fails")
	}

	called = false
	_, err = r.Execute(context.Background(), ToolCall{Name: "typed", Args: map[string]any{"count": 7, "extra": "not-allowed"}})
	if err == nil {
		t.Fatal("expected validation error for undeclared argument")
	}
	if !errors.As(err, &execErr) || execErr.Phase != ToolExecutionPhaseSchemaValidation {
		t.Fatalf("expected schema validation phase error for undeclared argument, got %T %v", err, err)
	}
	if called {
		t.Fatal("tool function should not run when undeclared arguments are present")
	}
}

func TestToolRegistry_SemanticValidation_AfterSchemaValidation(t *testing.T) {
	r := NewToolRegistry()
	called := false
	r.RegisterTool("validated", ToolRegistration{
		Func: func(_ context.Context, _ map[string]any) (string, error) {
			called = true
			return "ok", nil
		},
		Descriptor: ToolDescriptor{
			Parameters: ToolParameters{
				Type: "object",
				Properties: map[string]ToolParamProp{
					"count": {Type: "integer"},
				},
				Required: []string{"count"},
			},
		},
		Validate: func(_ context.Context, call ToolCall, _ ToolDescriptor) error {
			if ArgInt(call.Args, "count", 0) < 0 {
				return fmt.Errorf("count must be >= 0")
			}
			return nil
		},
	})

	_, err := r.Execute(context.Background(), ToolCall{Name: "validated", Args: map[string]any{"count": -1}})
	if err == nil {
		t.Fatal("expected semantic validation error")
	}
	var execErr *ToolExecutionError
	if !errors.As(err, &execErr) || execErr.Phase != ToolExecutionPhaseSemanticValidation {
		t.Fatalf("expected semantic validation phase error, got %T %v", err, err)
	}
	if called {
		t.Fatal("tool function should not run when semantic validation fails")
	}
}

func TestToolRegistry_MiddlewareOnlyWrapsExecutePhase(t *testing.T) {
	r := NewToolRegistry()
	intercepted := false
	r.RegisterTool("typed", ToolRegistration{
		Func: func(_ context.Context, _ map[string]any) (string, error) {
			return "ok", nil
		},
		Descriptor: ToolDescriptor{
			Parameters: ToolParameters{
				Type: "object",
				Properties: map[string]ToolParamProp{
					"count": {Type: "integer"},
				},
				Required: []string{"count"},
			},
		},
	})
	r.SetMiddleware(func(ctx context.Context, call ToolCall, next func(context.Context, ToolCall) (string, error)) (string, error) {
		intercepted = true
		return next(ctx, call)
	})

	_, err := r.Execute(context.Background(), ToolCall{Name: "typed", Args: map[string]any{"count": "oops"}})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if intercepted {
		t.Fatal("middleware should not run when schema validation fails")
	}
}

func TestToolRegistry_ExecutionPhaseOrdering(t *testing.T) {
	r := NewToolRegistry()
	var phases []string
	r.RegisterTool("ordered", ToolRegistration{
		Func: func(_ context.Context, args map[string]any) (string, error) {
			phases = append(phases, "execute")
			if ArgString(args, "from_pre") != "yes" {
				t.Fatalf("expected pre hook mutation to reach execute phase, got args=%v", args)
			}
			return "raw", nil
		},
		Descriptor: ToolDescriptor{
			Parameters: ToolParameters{Type: "object"},
		},
		Validate: func(_ context.Context, _ ToolCall, _ ToolDescriptor) error {
			phases = append(phases, "validate")
			return nil
		},
	})
	r.AddPreExecuteHook(func(_ context.Context, call ToolCall, _ ToolDescriptor) (ToolCall, error) {
		phases = append(phases, "pre")
		call.Args["from_pre"] = "yes"
		return call, nil
	})
	r.SetMiddleware(func(ctx context.Context, call ToolCall, next func(context.Context, ToolCall) (string, error)) (string, error) {
		phases = append(phases, "middleware")
		return next(ctx, call)
	})
	r.AddPostExecuteHook(func(_ context.Context, _ ToolCall, _ ToolDescriptor, result string) (string, error) {
		phases = append(phases, "post")
		return result + "-post", nil
	})

	result, err := r.Execute(context.Background(), ToolCall{Name: "ordered"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "raw-post" {
		t.Fatalf("unexpected result %q", result)
	}
	if got, want := strings.Join(phases, ","), "validate,pre,middleware,execute,post"; got != want {
		t.Fatalf("phase order = %q, want %q", got, want)
	}
}

func TestToolRegistry_ExecuteErrorHook_WrapsExecuteFailures(t *testing.T) {
	r := NewToolRegistry()
	r.Register("boom", func(_ context.Context, _ map[string]any) (string, error) {
		return "", fmt.Errorf("boom")
	})
	r.AddExecuteErrorHook(func(_ context.Context, _ ToolCall, _ ToolDescriptor, err error) error {
		return fmt.Errorf("wrapped: %w", err)
	})

	_, err := r.Execute(context.Background(), ToolCall{Name: "boom"})
	if err == nil {
		t.Fatal("expected execute error")
	}
	var execErr *ToolExecutionError
	if !errors.As(err, &execErr) || execErr.Phase != ToolExecutionPhaseExecute {
		t.Fatalf("expected execute phase error, got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "wrapped") {
		t.Fatalf("expected wrapped error, got %v", err)
	}
}

func TestToolRegistry_ExecuteErrorHook_PreservesPhaseAttribution(t *testing.T) {
	r := NewToolRegistry()
	r.Register("boom", func(_ context.Context, _ map[string]any) (string, error) {
		return "", fmt.Errorf("boom")
	})
	r.AddExecuteErrorHook(func(_ context.Context, _ ToolCall, _ ToolDescriptor, err error) error {
		return fmt.Errorf("first wrapper: %w", err)
	})
	r.AddExecuteErrorHook(func(_ context.Context, _ ToolCall, _ ToolDescriptor, err error) error {
		return fmt.Errorf("second wrapper: %w", err)
	})

	_, err := r.Execute(context.Background(), ToolCall{Name: "boom"})
	if err == nil {
		t.Fatal("expected execute error")
	}
	var execErr *ToolExecutionError
	if !errors.As(err, &execErr) {
		t.Fatalf("expected ToolExecutionError wrapper, got %T %v", err, err)
	}
	if execErr.Phase != ToolExecutionPhaseExecute {
		t.Fatalf("expected phase %q, got %q", ToolExecutionPhaseExecute, execErr.Phase)
	}
	if !strings.Contains(err.Error(), "second wrapper") {
		t.Fatalf("expected outer wrapper to be preserved, got %v", err)
	}
}

func TestToolRegistry_EffectiveTraits_UsesResolversAndDefaults(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterTool("traits", ToolRegistration{
		Func: func(_ context.Context, _ map[string]any) (string, error) { return "ok", nil },
		Descriptor: ToolDescriptor{
			Traits: ToolTraits{ReadOnly: true},
		},
		Traits: ToolTraitResolvers{
			IsConcurrencySafe: func(args map[string]any) bool { return ArgString(args, "mode") == "parallel" },
			IsDestructive:     func(args map[string]any) bool { return ArgString(args, "action") == "delete" },
			InterruptBehavior: func() ToolInterruptBehavior { return ToolInterruptBehaviorCancel },
		},
	})

	traits, ok := r.EffectiveTraits(ToolCall{Name: "traits", Args: map[string]any{"mode": "parallel", "action": "delete"}})
	if !ok {
		t.Fatal("expected traits")
	}
	if !traits.ConcurrencySafe || !traits.ReadOnly || !traits.Destructive || traits.InterruptBehavior != ToolInterruptBehaviorCancel {
		t.Fatalf("unexpected effective traits: %+v", traits)
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
