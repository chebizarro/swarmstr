package agent

import (
	"context"
	"strings"
	"testing"
)

func TestToolSearchDefinition_IncludesDeferredList(t *testing.T) {
	deferred := NewDeferredToolSet()
	deferred.Add(DeferredToolEntry{Name: "web_fetch", Summary: "Fetch web pages"})

	def := ToolSearchDefinition(deferred)
	if def.Name != ToolSearchToolName {
		t.Errorf("expected name %q, got %q", ToolSearchToolName, def.Name)
	}
	if !strings.Contains(def.Description, "web_fetch") {
		t.Error("expected deferred tool list in description")
	}
}

func TestToolSearchDefinition_EmptyDeferred(t *testing.T) {
	def := ToolSearchDefinition(NewDeferredToolSet())
	if strings.Contains(def.Description, "Available deferred") {
		t.Error("should not show deferred tools section when empty")
	}
}

func TestToolSearchFunc_KeywordSearch(t *testing.T) {
	deferred := NewDeferredToolSet()
	deferred.Add(DeferredToolEntry{
		Name:       "mcp__slack__send",
		Summary:    "Send a Slack message",
		Definition: ToolDefinition{Name: "mcp__slack__send"},
	})

	var discovered []ToolDefinition
	fn := ToolSearchFunc(deferred, func(defs []ToolDefinition) {
		discovered = append(discovered, defs...)
	})

	result, err := fn(context.Background(), map[string]any{"query": "slack"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "mcp__slack__send") {
		t.Error("expected result to contain tool name")
	}
	if len(discovered) != 1 {
		t.Errorf("expected 1 discovered tool, got %d", len(discovered))
	}
}

func TestToolSearchFunc_DirectSelect(t *testing.T) {
	deferred := NewDeferredToolSet()
	deferred.Add(DeferredToolEntry{
		Name:       "web_fetch",
		Summary:    "Fetch",
		Definition: ToolDefinition{Name: "web_fetch"},
	})
	deferred.Add(DeferredToolEntry{
		Name:       "web_search",
		Summary:    "Search",
		Definition: ToolDefinition{Name: "web_search"},
	})

	var discovered []ToolDefinition
	fn := ToolSearchFunc(deferred, func(defs []ToolDefinition) {
		discovered = append(discovered, defs...)
	})

	result, err := fn(context.Background(), map[string]any{"query": "select:web_fetch,web_search"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "2 tool(s)") {
		t.Errorf("expected 2 tools found, got: %s", result)
	}
	if len(discovered) != 2 {
		t.Errorf("expected 2 discovered, got %d", len(discovered))
	}
}

func TestToolSearchFunc_EmptyQuery(t *testing.T) {
	deferred := NewDeferredToolSet()
	fn := ToolSearchFunc(deferred, nil)

	result, err := fn(context.Background(), map[string]any{"query": ""})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Error") {
		t.Error("expected error for empty query")
	}
}

func TestToolSearchFunc_NoMatches(t *testing.T) {
	deferred := NewDeferredToolSet()
	deferred.Add(DeferredToolEntry{Name: "web_fetch", Summary: "Fetch pages"})

	fn := ToolSearchFunc(deferred, nil)
	result, err := fn(context.Background(), map[string]any{"query": "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "No tools found") {
		t.Errorf("expected no tools found, got: %s", result)
	}
}

func TestArgInt_Various(t *testing.T) {
	// ArgInt is declared in tools.go with signature (args, key, def)
	if ArgInt(nil, "key", 0) != 0 {
		t.Error("nil args should return 0")
	}
	if ArgInt(map[string]any{"n": float64(5)}, "n", 0) != 5 {
		t.Error("expected 5 from float64")
	}
	if ArgInt(map[string]any{"n": 7}, "n", 0) != 7 {
		t.Error("expected 7 from int")
	}
	// existing ArgInt in tools.go also handles string via strconv.Atoi;
	// "not-int" isn't parseable so it falls back to def.
	if ArgInt(map[string]any{"n": "not-int"}, "n", 0) != 0 {
		t.Error("expected 0 from unparseable string")
	}
}
