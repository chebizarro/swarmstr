package agent

import (
	"fmt"
	"strings"
	"testing"
)

func makeLargeTool(name string, descLen int, paramCount int) ToolDefinition {
	def := ToolDefinition{
		Name:        name,
		Description: strings.Repeat("d", descLen),
		Parameters: ToolParameters{
			Type:       "object",
			Properties: make(map[string]ToolParamProp),
			Required:   []string{},
		},
	}
	for i := 0; i < paramCount; i++ {
		pName := "param_" + strings.Repeat("x", 5)
		def.Parameters.Properties[pName] = ToolParamProp{
			Type:        "string",
			Description: strings.Repeat("p", 80),
		}
	}
	return def
}

func TestEstimateToolDefinitionChars(t *testing.T) {
	def := ToolDefinition{
		Name:        "web_search",
		Description: "Search the web for information",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolParamProp{
				"query": {Type: "string", Description: "The search query"},
			},
			Required: []string{"query"},
		},
	}
	chars := EstimateToolDefinitionChars(def)
	if chars < 50 {
		t.Errorf("estimate too low: %d", chars)
	}
	if chars > 500 {
		t.Errorf("estimate suspiciously high for simple tool: %d", chars)
	}
}

func TestCompressToolDefinitionByPressure_NoPressure(t *testing.T) {
	def := makeLargeTool("test_tool", 300, 3)
	compressed := CompressToolDefinitionByPressure(def, 0.0)
	if compressed.Description != def.Description {
		t.Error("zero pressure should not modify description")
	}
}

func TestCompressToolDefinitionByPressure_LightPressure(t *testing.T) {
	def := makeLargeTool("test_tool", 300, 3)
	compressed := CompressToolDefinitionByPressure(def, 0.20)

	if len(compressed.Description) > 202 {
		t.Errorf("light pressure should truncate description to ~200, got len=%d", len(compressed.Description))
	}
	for _, prop := range compressed.Parameters.Properties {
		if len(prop.Description) > 62 {
			t.Errorf("light pressure should truncate param desc to ~60, got len=%d", len(prop.Description))
		}
	}
}

func TestCompressToolDefinitionByPressure_ModeratePressure(t *testing.T) {
	def := makeLargeTool("test_tool", 300, 3)
	compressed := CompressToolDefinitionByPressure(def, 0.50)

	if len(compressed.Description) > 152 {
		t.Errorf("moderate pressure should truncate description to ~150, got len=%d", len(compressed.Description))
	}
	for _, prop := range compressed.Parameters.Properties {
		if len(prop.Description) > 42 {
			t.Errorf("moderate pressure should truncate param desc to ~40, got len=%d", len(prop.Description))
		}
	}
}

func TestCompressToolDefinitionByPressure_AggressivePressure(t *testing.T) {
	def := makeLargeTool("test_tool", 300, 3)
	compressed := CompressToolDefinitionByPressure(def, 0.85)

	if len(compressed.Description) > 82 {
		t.Errorf("aggressive pressure should truncate description to ~80, got len=%d", len(compressed.Description))
	}
	for _, prop := range compressed.Parameters.Properties {
		if prop.Description != "" {
			t.Errorf("aggressive pressure should strip param descriptions, got %q", prop.Description)
		}
	}
	if compressed.InputJSONSchema != nil {
		t.Error("aggressive pressure should clear InputJSONSchema")
	}
}

func TestCompressToolDefinition_TierMapping(t *testing.T) {
	def := makeLargeTool("test_tool", 300, 3)

	// Standard → no compression
	std := CompressToolDefinition(def, TierStandard)
	if std.Description != def.Description {
		t.Error("TierStandard should not compress")
	}

	// Micro → aggressive
	micro := CompressToolDefinition(def, TierMicro)
	if len(micro.Description) > 82 {
		t.Errorf("TierMicro should compress aggressively, got desc len=%d", len(micro.Description))
	}

	// Small → moderate
	small := CompressToolDefinition(def, TierSmall)
	if len(small.Description) > 152 {
		t.Errorf("TierSmall should compress moderately, got desc len=%d", len(small.Description))
	}
}

func TestFitToolDefinitions_CriticalToolsFirst(t *testing.T) {
	defs := []ToolDefinition{
		{Name: "regular_tool", Description: "Regular"},
		{Name: "memory_search", Description: "Search memory"},
		{Name: "another_regular", Description: "Another regular tool"},
		{Name: "session_send", Description: "Send session message"},
	}

	budget := ContextBudget{
		ToolDefsMax: 500, // very tight
		Profile:     ModelContextProfile{ContextWindowTokens: 4096, Tier: TierMicro},
	}

	result := FitToolDefinitions(defs, budget, DefaultCriticalToolNames())

	// Critical tools should be present
	criticalFound := make(map[string]bool)
	for _, def := range result {
		criticalFound[def.Name] = true
	}
	if !criticalFound["memory_search"] {
		t.Error("memory_search should be included as critical tool")
	}
	if !criticalFound["session_send"] {
		t.Error("session_send should be included as critical tool")
	}
}

func TestFitToolDefinitions_RespectsCharBudget(t *testing.T) {
	// Create many tools that exceed the budget
	var defs []ToolDefinition
	for i := 0; i < 20; i++ {
		defs = append(defs, makeLargeTool("tool_"+string(rune('a'+i)), 200, 2))
	}

	budget := ContextBudget{
		ToolDefsMax: 2000,
		Profile:     ModelContextProfile{ContextWindowTokens: 8192, Tier: TierSmall},
	}

	result := FitToolDefinitions(defs, budget, nil)

	if len(result) >= len(defs) {
		t.Errorf("expected fewer tools than input (%d), got %d", len(defs), len(result))
	}
	if len(result) == 0 {
		t.Error("should have at least some tools")
	}

	// Verify total estimated chars fits within budget
	totalChars := 0
	for _, def := range result {
		totalChars += EstimateToolDefinitionChars(def)
	}
	if totalChars > budget.ToolDefsMax+200 { // small tolerance for estimation
		t.Errorf("total chars %d exceeds budget %d", totalChars, budget.ToolDefsMax)
	}
}

func TestFitToolDefinitions_ZeroBudgetReturnsAll(t *testing.T) {
	defs := []ToolDefinition{
		{Name: "tool1", Description: "desc1"},
		{Name: "tool2", Description: "desc2"},
	}
	result := FitToolDefinitions(defs, ContextBudget{ToolDefsMax: 0}, nil)
	if len(result) != len(defs) {
		t.Errorf("zero budget should return all defs, got %d", len(result))
	}
}

func TestFitToolDefinitions_EmptyInput(t *testing.T) {
	result := FitToolDefinitions(nil, ContextBudget{ToolDefsMax: 5000}, nil)
	if len(result) != 0 {
		t.Errorf("empty input should return empty, got %d", len(result))
	}
}

func TestFitToolDefinitions_DynamicPressure(t *testing.T) {
	// Large budget → no compression
	defs := []ToolDefinition{makeLargeTool("tool", 300, 3)}
	largeBudget := ContextBudget{
		ToolDefsMax: 100_000,
		Profile:     ModelContextProfile{ContextWindowTokens: 200_000},
	}
	result := FitToolDefinitions(defs, largeBudget, nil)
	if len(result) != 1 {
		t.Fatal("expected 1 tool")
	}
	// With such a large budget, pressure should be ~0, so description preserved
	if result[0].Description != defs[0].Description {
		t.Error("large budget should not compress descriptions")
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hell…"},
		{"hello", 1, "…"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncateStr(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestFitToolDefinitions_RespectsMaxToolCount(t *testing.T) {
	// Create 30 small tools that all fit by chars but exceed MaxToolCount.
	var defs []ToolDefinition
	for i := 0; i < 30; i++ {
		defs = append(defs, ToolDefinition{
			Name:        fmt.Sprintf("tool_%02d", i),
			Description: "A small tool",
		})
	}

	budget := ContextBudget{
		ToolDefsMax:  100_000, // huge char budget — won't be the limiter
		MaxToolCount: 10,
		Profile:      ModelContextProfile{ContextWindowTokens: 65536},
	}

	result := FitToolDefinitions(defs, budget, nil)
	if len(result) > 10 {
		t.Errorf("expected at most 10 tools (MaxToolCount), got %d", len(result))
	}
}

func TestFitToolDefinitions_MaxToolCountPreservesCritical(t *testing.T) {
	defs := []ToolDefinition{
		{Name: "memory_search", Description: "Search memory"},
		{Name: "session_send", Description: "Send message"},
		{Name: "session_spawn", Description: "Spawn session"},
	}
	// Add 20 regular tools
	for i := 0; i < 20; i++ {
		defs = append(defs, ToolDefinition{
			Name:        fmt.Sprintf("regular_%02d", i),
			Description: "Regular tool",
		})
	}

	budget := ContextBudget{
		ToolDefsMax:  100_000,
		MaxToolCount: 5,
		Profile:      ModelContextProfile{ContextWindowTokens: 65536},
	}

	result := FitToolDefinitions(defs, budget, DefaultCriticalToolNames())
	if len(result) > 5 {
		t.Errorf("expected at most 5 tools, got %d", len(result))
	}

	// All 3 critical tools must be present.
	found := make(map[string]bool)
	for _, def := range result {
		found[def.Name] = true
	}
	for _, critical := range DefaultCriticalToolNames() {
		if !found[critical] {
			t.Errorf("critical tool %q missing from result", critical)
		}
	}
}

func TestComputeContextBudget_MaxToolCountScaling(t *testing.T) {
	tests := []struct {
		tokens   int
		maxCount int // approximate expected MaxToolCount
	}{
		{4096, 10},
		{65536, 17},
		{128000, 60},
		{200000, 200},
	}
	for _, tt := range tests {
		b := ComputeContextBudgetForTokens(tt.tokens)
		// Allow ±5 tolerance for rounding
		if b.MaxToolCount < tt.maxCount-5 || b.MaxToolCount > tt.maxCount+5 {
			t.Errorf("at %d tokens: MaxToolCount=%d, want ~%d", tt.tokens, b.MaxToolCount, tt.maxCount)
		}
	}
}

func TestComputeContextBudget_ToolDefsMaxReduced(t *testing.T) {
	// Verify the JSON tokenization correction reduces ToolDefsMax
	b65 := ComputeContextBudgetForTokens(65536)
	if b65.ToolDefsMax > 20_000 {
		t.Errorf("65K model ToolDefsMax=%d, want <= 20000 (JSON correction)", b65.ToolDefsMax)
	}
	// 200K should still hit the cap
	b200 := ComputeContextBudgetForTokens(200000)
	if b200.ToolDefsMax < 40_000 {
		t.Errorf("200K model ToolDefsMax=%d, want >= 40000", b200.ToolDefsMax)
	}
}
