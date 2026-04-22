package agent

import (
	"context"
	"testing"
)

// ─── camelToSnake ─────────────────────────────────────────────────────────────

func TestCamelToSnake(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"maxDepth", "max_depth"},
		{"contextLines", "context_lines"},
		{"fixedStrings", "fixed_strings"},
		{"dirsOnly", "dirs_only"},
		{"timeoutSeconds", "timeout_seconds"},
		{"maxResults", "max_results"},
		{"allowLocal", "allow_local"},
		{"startLine", "start_line"},
		{"endLine", "end_line"},
		// Already snake_case — unchanged.
		{"max_depth", "max_depth"},
		{"context_lines", "context_lines"},
		// Single word — unchanged.
		{"pattern", "pattern"},
		{"query", "query"},
		// PascalCase.
		{"MaxDepth", "max_depth"},
		// All-lower — unchanged.
		{"timeout", "timeout"},
	}
	for _, tt := range tests {
		if got := camelToSnake(tt.input); got != tt.want {
			t.Errorf("camelToSnake(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─── applyParamAliases ───────────────────────────────────────────────────────

func TestApplyParamAliases_ExplicitAlias(t *testing.T) {
	def := ToolDefinition{
		Parameters: ToolParameters{
			Properties: map[string]ToolParamProp{
				"max_depth": {Type: "integer"},
				"path":      {Type: "string"},
			},
		},
		ParamAliases: map[string]string{
			"depth": "max_depth",
			"dir":   "path",
		},
	}
	args := map[string]any{"depth": 5, "dir": "/tmp"}
	result := applyParamAliases(args, def)

	if v, ok := result["max_depth"]; !ok || v != 5 {
		t.Errorf("expected max_depth=5, got %v (ok=%v)", v, ok)
	}
	if v, ok := result["path"]; !ok || v != "/tmp" {
		t.Errorf("expected path=/tmp, got %v (ok=%v)", v, ok)
	}
	// Alias keys should not survive.
	if _, ok := result["depth"]; ok {
		t.Error("alias key 'depth' should have been removed")
	}
	if _, ok := result["dir"]; ok {
		t.Error("alias key 'dir' should have been removed")
	}
}

func TestApplyParamAliases_CamelCaseConversion(t *testing.T) {
	def := ToolDefinition{
		Parameters: ToolParameters{
			Properties: map[string]ToolParamProp{
				"max_depth":     {Type: "integer"},
				"context_lines": {Type: "integer"},
			},
		},
	}
	args := map[string]any{"maxDepth": 3, "contextLines": 2}
	result := applyParamAliases(args, def)

	if v := result["max_depth"]; v != 3 {
		t.Errorf("expected max_depth=3, got %v", v)
	}
	if v := result["context_lines"]; v != 2 {
		t.Errorf("expected context_lines=2, got %v", v)
	}
}

func TestApplyParamAliases_CanonicalKeyPassthrough(t *testing.T) {
	def := ToolDefinition{
		Parameters: ToolParameters{
			Properties: map[string]ToolParamProp{
				"max_depth": {Type: "integer"},
				"path":      {Type: "string"},
			},
		},
		ParamAliases: map[string]string{
			"depth": "max_depth",
		},
	}
	// Canonical names should pass through untouched.
	args := map[string]any{"max_depth": 7, "path": "/home"}
	result := applyParamAliases(args, def)

	if v := result["max_depth"]; v != 7 {
		t.Errorf("expected max_depth=7, got %v", v)
	}
	if v := result["path"]; v != "/home" {
		t.Errorf("expected path=/home, got %v", v)
	}
}

func TestApplyParamAliases_CanonicalWinsOverAlias(t *testing.T) {
	def := ToolDefinition{
		Parameters: ToolParameters{
			Properties: map[string]ToolParamProp{
				"max_depth": {Type: "integer"},
			},
		},
		ParamAliases: map[string]string{
			"depth": "max_depth",
		},
	}
	// When both canonical and alias are provided, canonical wins.
	args := map[string]any{"max_depth": 10, "depth": 3}
	result := applyParamAliases(args, def)

	if v := result["max_depth"]; v != 10 {
		t.Errorf("expected canonical max_depth=10, got %v", v)
	}
}

func TestApplyParamAliases_UnknownKeyPassedThrough(t *testing.T) {
	def := ToolDefinition{
		Parameters: ToolParameters{
			Properties: map[string]ToolParamProp{
				"path": {Type: "string"},
			},
		},
	}
	// Unknown key should survive for schema validator to catch.
	args := map[string]any{"path": "/tmp", "bogus": true}
	result := applyParamAliases(args, def)

	if _, ok := result["bogus"]; !ok {
		t.Error("unknown key 'bogus' should pass through for schema validation")
	}
}

func TestApplyParamAliases_EmptyArgs(t *testing.T) {
	def := ToolDefinition{
		Parameters: ToolParameters{
			Properties: map[string]ToolParamProp{
				"path": {Type: "string"},
			},
		},
	}
	result := applyParamAliases(nil, def)
	if result != nil {
		t.Errorf("expected nil for nil args, got %v", result)
	}

	result = applyParamAliases(map[string]any{}, def)
	if len(result) != 0 {
		t.Errorf("expected empty for empty args, got %v", result)
	}
}

func TestApplyParamAliases_NoProperties(t *testing.T) {
	def := ToolDefinition{
		Parameters: ToolParameters{Type: "object"},
	}
	args := map[string]any{"anything": "goes"}
	result := applyParamAliases(args, def)

	// With no properties defined, args pass through unchanged.
	if v := result["anything"]; v != "goes" {
		t.Errorf("expected passthrough, got %v", v)
	}
}

func TestApplyParamAliases_AliasPriority(t *testing.T) {
	// Explicit alias should take priority over camelCase conversion.
	def := ToolDefinition{
		Parameters: ToolParameters{
			Properties: map[string]ToolParamProp{
				"max_depth": {Type: "integer"},
				"depth":     {Type: "integer"}, // "depth" is also a canonical param
			},
		},
		ParamAliases: map[string]string{
			"depth": "max_depth", // alias overrides the canonical match
		},
	}
	// Since "depth" is in the canonical set, it should be kept as "depth"
	// (canonical match takes precedence over alias).
	args := map[string]any{"depth": 5}
	result := applyParamAliases(args, def)

	if v, ok := result["depth"]; !ok || v != 5 {
		t.Errorf("canonical 'depth' should pass through as-is, got result=%v", result)
	}
}

// TestToolRegistry_AliasBeforeSchemaValidation verifies that parameter aliases
// are resolved before JSON Schema validation, preventing "unexpected additional
// properties" errors when models hallucinate shorthand parameter names.
func TestToolRegistry_AliasBeforeSchemaValidation(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterWithDescriptor("test_tree", func(_ context.Context, args map[string]any) (string, error) {
		// Verify the handler receives canonicalized names.
		if v := ArgInt(args, "max_depth", 0); v != 3 {
			t.Errorf("handler saw max_depth=%d, want 3", v)
		}
		if v := ArgString(args, "path"); v != "/tmp" {
			t.Errorf("handler saw path=%q, want /tmp", v)
		}
		return "ok", nil
	}, ToolDescriptor{
		Name: "test_tree",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolParamProp{
				"max_depth": {Type: "integer"},
				"path":      {Type: "string"},
			},
		},
		ParamAliases: map[string]string{
			"depth": "max_depth",
			"dir":   "path",
		},
	})

	// Call with aliased names — should NOT fail schema validation.
	result, err := r.Execute(context.Background(), ToolCall{
		Name: "test_tree",
		Args: map[string]any{"depth": float64(3), "dir": "/tmp"},
	})
	if err != nil {
		t.Fatalf("expected alias to resolve before validation, got error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("unexpected result: %q", result)
	}
}

// TestToolRegistry_CamelCaseBeforeSchemaValidation verifies automatic
// camelCase→snake_case conversion resolves before schema validation.
func TestToolRegistry_CamelCaseBeforeSchemaValidation(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterWithDescriptor("test_search", func(_ context.Context, args map[string]any) (string, error) {
		return "ok", nil
	}, ToolDescriptor{
		Name: "test_search",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolParamProp{
				"max_results":   {Type: "integer"},
				"context_lines": {Type: "integer"},
				"pattern":       {Type: "string"},
			},
			Required: []string{"pattern"},
		},
	})

	// Call with camelCase — should auto-convert and pass validation.
	_, err := r.Execute(context.Background(), ToolCall{
		Name: "test_search",
		Args: map[string]any{"pattern": "TODO", "maxResults": float64(10), "contextLines": float64(2)},
	})
	if err != nil {
		t.Fatalf("expected camelCase auto-conversion, got error: %v", err)
	}
}

// TestToolRegistry_RegisterWithDef_PropagatesAliases verifies that aliases
// defined in a ToolDefinition survive the RegisterWithDef → descriptorFromDefinition
// → validatedArgs path (this is the registration path used by real tools).
func TestToolRegistry_RegisterWithDef_PropagatesAliases(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterWithDef("test_aliased", func(_ context.Context, args map[string]any) (string, error) {
		if v := ArgInt(args, "max_depth", 0); v != 5 {
			t.Errorf("handler saw max_depth=%d, want 5", v)
		}
		return "ok", nil
	}, ToolDefinition{
		Name: "test_aliased",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolParamProp{
				"max_depth": {Type: "integer"},
			},
		},
		ParamAliases: map[string]string{
			"depth": "max_depth",
		},
	})

	result, err := r.Execute(context.Background(), ToolCall{
		Name: "test_aliased",
		Args: map[string]any{"depth": float64(5)},
	})
	if err != nil {
		t.Fatalf("RegisterWithDef alias failed: %v", err)
	}
	if result != "ok" {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestApplyParamAliases_CamelCaseDoesNotOverrideExisting(t *testing.T) {
	def := ToolDefinition{
		Parameters: ToolParameters{
			Properties: map[string]ToolParamProp{
				"max_depth": {Type: "integer"},
			},
		},
	}
	// Both camelCase and canonical provided — canonical wins.
	args := map[string]any{"max_depth": 10, "maxDepth": 3}
	result := applyParamAliases(args, def)

	if v := result["max_depth"]; v != 10 {
		t.Errorf("expected canonical max_depth=10, got %v", v)
	}
}
