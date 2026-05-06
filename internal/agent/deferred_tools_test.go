package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestDeferredToolSet_AddAndGet(t *testing.T) {
	s := NewDeferredToolSet()
	s.Add(DeferredToolEntry{
		Name:       "web_fetch",
		Summary:    "Fetch a web page",
		Definition: ToolDefinition{Name: "web_fetch", Description: "Fetch a web page"},
	})
	e, ok := s.Get("web_fetch")
	if !ok {
		t.Fatal("expected to find web_fetch")
	}
	if e.Summary != "Fetch a web page" {
		t.Errorf("unexpected summary: %s", e.Summary)
	}
}

func TestDeferredToolSet_Remove(t *testing.T) {
	s := NewDeferredToolSet()
	s.Add(DeferredToolEntry{Name: "test_tool"})
	s.Remove("test_tool")
	if _, ok := s.Get("test_tool"); ok {
		t.Error("expected tool to be removed")
	}
}

func TestDeferredToolSet_Count(t *testing.T) {
	s := NewDeferredToolSet()
	if s.Count() != 0 {
		t.Error("expected 0")
	}
	s.Add(DeferredToolEntry{Name: "a"})
	s.Add(DeferredToolEntry{Name: "b"})
	if s.Count() != 2 {
		t.Errorf("expected 2, got %d", s.Count())
	}
}

func TestDeferredToolSet_Search_Keywords(t *testing.T) {
	s := NewDeferredToolSet()
	s.Add(DeferredToolEntry{Name: "mcp__slack__send", Summary: "Send a Slack message"})
	s.Add(DeferredToolEntry{Name: "mcp__slack__read", Summary: "Read Slack channel messages"})
	s.Add(DeferredToolEntry{Name: "web_fetch", Summary: "Fetch a web page by URL"})

	results := s.Search("slack", 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 slack results, got %d", len(results))
	}
	// Both should match; name match scores higher.
	if results[0].Name != "mcp__slack__read" && results[0].Name != "mcp__slack__send" {
		t.Errorf("expected slack tools, got %s", results[0].Name)
	}
}

func TestDeferredToolSet_Search_Select(t *testing.T) {
	s := NewDeferredToolSet()
	s.Add(DeferredToolEntry{Name: "web_fetch", Summary: "Fetch"})
	s.Add(DeferredToolEntry{Name: "web_search", Summary: "Search"})

	results := s.Search("select:web_fetch,web_search", 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results from select, got %d", len(results))
	}
}

func TestDeferredToolSet_Search_EmptyQuery(t *testing.T) {
	s := NewDeferredToolSet()
	s.Add(DeferredToolEntry{Name: "test"})
	if results := s.Search("", 5); len(results) != 0 {
		t.Errorf("expected no results for empty query, got %d", len(results))
	}
}

func TestDeferredToolSet_Search_MaxResults(t *testing.T) {
	s := NewDeferredToolSet()
	for i := 0; i < 20; i++ {
		s.Add(DeferredToolEntry{
			Name:    "tool_" + string(rune('a'+i)),
			Summary: "tool description",
		})
	}
	results := s.Search("tool", 3)
	if len(results) != 3 {
		t.Errorf("expected 3 results (max), got %d", len(results))
	}
}

func TestDeferredToolSet_ListSummaries(t *testing.T) {
	s := NewDeferredToolSet()
	s.Add(DeferredToolEntry{Name: "beta", Summary: "B tool"})
	s.Add(DeferredToolEntry{Name: "alpha", Summary: "A tool"})

	summaries := s.ListSummaries()
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
	// Should be sorted by name.
	if summaries[0] != "alpha — A tool" {
		t.Errorf("expected alpha first, got %s", summaries[0])
	}
}

// ─── IsDeferrableTool tests ─────────────────────────────────────────────────

func TestIsDeferrableTool_MCP(t *testing.T) {
	desc := ToolDescriptor{Name: "mcp__server__tool", Origin: ToolOrigin{Kind: ToolOriginKindMCP}}
	if !IsDeferrableTool(desc) {
		t.Error("MCP tools should be deferrable")
	}
}

func TestIsDeferrableTool_Plugin(t *testing.T) {
	desc := ToolDescriptor{Name: "plugin_tool", Origin: ToolOrigin{Kind: ToolOriginKindPlugin}}
	if !IsDeferrableTool(desc) {
		t.Error("Plugin tools should be deferrable")
	}
}

func TestIsDeferrableTool_Builtin(t *testing.T) {
	desc := ToolDescriptor{Name: "memory_search", Origin: ToolOrigin{Kind: ToolOriginKindBuiltin}}
	if IsDeferrableTool(desc) {
		t.Error("Builtin tools should not be deferrable")
	}
}

// ─── PartitionTools tests ───────────────────────────────────────────────────

func TestPartitionTools_AllInlineWhenBelowThreshold(t *testing.T) {
	descs := []ToolDescriptor{
		{Name: "builtin_a", Description: "A", Origin: ToolOrigin{Kind: ToolOriginKindBuiltin}},
		{Name: "mcp__small", Description: "Small MCP tool", Origin: ToolOrigin{Kind: ToolOriginKindMCP}},
	}

	// With a huge budget, the MCP tool's chars won't exceed 10%.
	result := PartitionTools(descs, 100_000, 10, nil)
	if len(result.Inline) != 2 {
		t.Errorf("expected all 2 tools inline, got %d", len(result.Inline))
	}
	if result.Deferred.Count() != 0 {
		t.Error("expected no deferred tools below threshold")
	}
}

func TestPartitionTools_DefersWhenAboveThreshold(t *testing.T) {
	// Create descriptors where MCP tools have large schemas.
	var descs []ToolDescriptor
	descs = append(descs, ToolDescriptor{
		Name: "builtin", Description: "Small builtin",
		Origin: ToolOrigin{Kind: ToolOriginKindBuiltin},
	})
	for i := 0; i < 10; i++ {
		descs = append(descs, ToolDescriptor{
			Name:        "mcp__tool_" + string(rune('a'+i)),
			Description: "Large MCP tool with lots of parameters " + string(make([]byte, 500)),
			Origin:      ToolOrigin{Kind: ToolOriginKindMCP},
		})
	}

	// Small budget → MCP tools exceed 10% → should defer.
	result := PartitionTools(descs, 1000, 10, nil)
	if result.Deferred.Count() == 0 {
		t.Error("expected MCP tools to be deferred with small budget")
	}
	// Builtin should remain inline.
	builtinFound := false
	for _, def := range result.Inline {
		if def.Name == "builtin" {
			builtinFound = true
		}
	}
	if !builtinFound {
		t.Error("builtin tool should remain inline")
	}
}

func TestPartitionTools_CriticalNeverDeferred(t *testing.T) {
	descs := []ToolDescriptor{
		{Name: "mcp__critical", Description: "Critical MCP tool" + string(make([]byte, 500)),
			Origin: ToolOrigin{Kind: ToolOriginKindMCP}},
		{Name: "mcp__regular", Description: "Regular MCP tool" + string(make([]byte, 500)),
			Origin: ToolOrigin{Kind: ToolOriginKindMCP}},
	}

	result := PartitionTools(descs, 100, 10, []string{"mcp__critical"})
	// mcp__critical should be inline despite being MCP.
	criticalInline := false
	for _, def := range result.Inline {
		if def.Name == "mcp__critical" {
			criticalInline = true
		}
	}
	if !criticalInline {
		t.Error("critical tool should always be inline")
	}
}

func TestPartitionTools_ForceDeferralWhenExceedsMaxInline(t *testing.T) {
	// Create 30 builtin tools (normally not deferrable).
	var descs []ToolDescriptor
	for i := 0; i < 30; i++ {
		descs = append(descs, ToolDescriptor{
			Name:        fmt.Sprintf("builtin_%02d", i),
			Description: "A builtin tool with description",
			Origin:      ToolOrigin{Kind: ToolOriginKindBuiltin},
		})
	}

	// With maxInlineCount=10, 20 tools should be force-deferred.
	result := PartitionTools(descs, 100_000, 10, nil, 10)
	if len(result.Inline) > 10 {
		t.Errorf("expected at most 10 inline tools, got %d", len(result.Inline))
	}
	if result.Deferred.Count() < 20 {
		t.Errorf("expected at least 20 deferred tools, got %d", result.Deferred.Count())
	}
}

func TestPartitionTools_ForceDeferralPreservesCritical(t *testing.T) {
	var descs []ToolDescriptor
	// All critical tools
	for _, name := range DefaultCriticalToolNames() {
		descs = append(descs, ToolDescriptor{
			Name:        name,
			Description: "Critical tool",
			Origin:      ToolOrigin{Kind: ToolOriginKindBuiltin},
		})
	}
	// 20 regular builtin tools
	for i := 0; i < 20; i++ {
		descs = append(descs, ToolDescriptor{
			Name:        fmt.Sprintf("regular_%02d", i),
			Description: "Regular builtin tool",
			Origin:      ToolOrigin{Kind: ToolOriginKindBuiltin},
		})
	}

	// maxInlineCount=10: all critical tools + 1-2 regular inline, rest deferred
	criticalCount := len(DefaultCriticalToolNames())
	maxInline := criticalCount + 2
	result := PartitionTools(descs, 100_000, 10, DefaultCriticalToolNames(), maxInline)
	if len(result.Inline) > maxInline {
		t.Errorf("expected at most %d inline tools, got %d", maxInline, len(result.Inline))
	}

	// All critical tools must be inline.
	criticalNames := map[string]bool{}
	for _, def := range result.Inline {
		criticalNames[def.Name] = true
	}
	for _, name := range DefaultCriticalToolNames() {
		if !criticalNames[name] {
			t.Errorf("critical tool %q should be inline", name)
		}
	}
}

func TestPartitionTools_ForceDeferralDefersLargestFirst(t *testing.T) {
	descs := []ToolDescriptor{
		{Name: "small", Description: "A", Origin: ToolOrigin{Kind: ToolOriginKindBuiltin}},
		{Name: "medium", Description: strings.Repeat("x", 200), Origin: ToolOrigin{Kind: ToolOriginKindBuiltin}},
		{Name: "large", Description: strings.Repeat("x", 500), Origin: ToolOrigin{Kind: ToolOriginKindBuiltin}},
	}

	// maxInlineCount=1: only 1 should stay inline, and it should be the smallest.
	result := PartitionTools(descs, 100_000, 10, nil, 1)
	if len(result.Inline) != 1 {
		t.Fatalf("expected 1 inline tool, got %d", len(result.Inline))
	}
	if result.Inline[0].Name != "small" {
		t.Errorf("expected smallest tool to stay inline, got %q", result.Inline[0].Name)
	}
	if result.Deferred.Count() != 2 {
		t.Errorf("expected 2 deferred, got %d", result.Deferred.Count())
	}
}

func TestPartitionTools_NoMaxInlineBackwardCompatible(t *testing.T) {
	// Without maxInlineCount arg, behavior should be unchanged.
	descs := []ToolDescriptor{
		{Name: "a", Description: "tool a", Origin: ToolOrigin{Kind: ToolOriginKindBuiltin}},
		{Name: "b", Description: "tool b", Origin: ToolOrigin{Kind: ToolOriginKindBuiltin}},
	}
	result := PartitionTools(descs, 100_000, 10, nil)
	if len(result.Inline) != 2 {
		t.Errorf("expected 2 inline without maxInline, got %d", len(result.Inline))
	}
}
