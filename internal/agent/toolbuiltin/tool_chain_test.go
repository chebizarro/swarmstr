package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"metiq/internal/agent"
)

// ─── Registry ───────────────────────────────────────────────────────────────

func TestChainRegistry_DefineAndGet(t *testing.T) {
	reg := NewChainRegistry()
	def := ChainDef{
		Name:        "test_chain",
		Description: "a test chain",
		Steps: []ChainStep{
			{Name: "s1", Tool: "echo"},
		},
	}
	reg.define(def)

	got, ok := reg.get("test_chain")
	if !ok {
		t.Fatal("expected to find test_chain")
	}
	if got.Name != "test_chain" {
		t.Errorf("name = %q, want test_chain", got.Name)
	}
	if len(got.Steps) != 1 {
		t.Errorf("steps = %d, want 1", len(got.Steps))
	}
}

func TestChainRegistry_GetReturnsCopy(t *testing.T) {
	reg := NewChainRegistry()
	reg.define(ChainDef{
		Name:  "c",
		Steps: []ChainStep{{Name: "s", Tool: "t"}},
	})

	got, _ := reg.get("c")
	got.Steps = append(got.Steps, ChainStep{Name: "extra", Tool: "x"})

	original, _ := reg.get("c")
	if len(original.Steps) != 1 {
		t.Error("modifying returned copy should not affect registry")
	}
}

func TestChainRegistry_GetNotFound(t *testing.T) {
	reg := NewChainRegistry()
	_, ok := reg.get("nope")
	if ok {
		t.Error("expected not found")
	}
}

func TestChainRegistry_List(t *testing.T) {
	reg := NewChainRegistry()
	if len(reg.list()) != 0 {
		t.Error("empty registry should return empty list")
	}

	reg.define(ChainDef{Name: "a", Steps: []ChainStep{{Tool: "t"}}})
	reg.define(ChainDef{Name: "b", Steps: []ChainStep{{Tool: "t"}}})

	chains := reg.list()
	if len(chains) != 2 {
		t.Fatalf("got %d, want 2", len(chains))
	}
}

func TestChainRegistry_Remove(t *testing.T) {
	reg := NewChainRegistry()
	reg.define(ChainDef{Name: "rm_me", Steps: []ChainStep{{Tool: "t"}}})

	if !reg.remove("rm_me") {
		t.Error("remove should return true")
	}
	if reg.remove("rm_me") {
		t.Error("second remove should return false")
	}
	if _, ok := reg.get("rm_me"); ok {
		t.Error("chain should be gone")
	}
}

func TestChainRegistry_Overwrite(t *testing.T) {
	reg := NewChainRegistry()
	reg.define(ChainDef{Name: "c", Description: "v1", Steps: []ChainStep{{Tool: "a"}}})
	reg.define(ChainDef{Name: "c", Description: "v2", Steps: []ChainStep{{Tool: "b"}, {Tool: "c"}}})

	got, _ := reg.get("c")
	if got.Description != "v2" {
		t.Error("overwrite should replace description")
	}
	if len(got.Steps) != 2 {
		t.Error("overwrite should replace steps")
	}
}

// ─── Template expansion ────────────────────────────────────────────────────

func TestChainExpandTemplates_Params(t *testing.T) {
	params := map[string]any{"dir": "/src", "count": 5}
	result := chainExpandTemplates("search in {{params.dir}} limit {{params.count}}", params, nil)
	if result != "search in /src limit 5" {
		t.Errorf("got %q", result)
	}
}

func TestChainExpandTemplates_Steps(t *testing.T) {
	outputs := map[string]string{"find": "main.go"}
	result := chainExpandTemplates("reading {{steps.find}}", nil, outputs)
	if result != "reading main.go" {
		t.Errorf("got %q", result)
	}
}

func TestChainExpandTemplates_Unresolved(t *testing.T) {
	result := chainExpandTemplates("{{params.missing}} and {{steps.nope}}", nil, nil)
	if result != "{{params.missing}} and {{steps.nope}}" {
		t.Errorf("unresolved templates should remain: got %q", result)
	}
}

func TestChainExpandTemplates_Mixed(t *testing.T) {
	params := map[string]any{"q": "TODO"}
	outputs := map[string]string{"search": "found 3 matches"}
	result := chainExpandTemplates("query={{params.q}} result={{steps.search}}", params, outputs)
	if result != "query=TODO result=found 3 matches" {
		t.Errorf("got %q", result)
	}
}

func TestChainExpandArgs_NestedMap(t *testing.T) {
	args := map[string]any{
		"outer": map[string]any{
			"inner": "{{params.x}}",
		},
	}
	params := map[string]any{"x": "replaced"}
	result := chainExpandArgs(args, params, nil)

	outer, ok := result["outer"].(map[string]any)
	if !ok {
		t.Fatal("expected nested map")
	}
	if outer["inner"] != "replaced" {
		t.Errorf("nested expansion failed: got %v", outer["inner"])
	}
}

func TestChainExpandArgs_Array(t *testing.T) {
	args := map[string]any{
		"items": []any{"{{params.a}}", "literal", "{{steps.s1}}"},
	}
	params := map[string]any{"a": "A"}
	outputs := map[string]string{"s1": "S1"}
	result := chainExpandArgs(args, params, outputs)

	items, ok := result["items"].([]any)
	if !ok {
		t.Fatal("expected array")
	}
	if items[0] != "A" || items[1] != "literal" || items[2] != "S1" {
		t.Errorf("array expansion failed: %v", items)
	}
}

func TestChainExpandArgs_NonStringUntouched(t *testing.T) {
	args := map[string]any{"n": 42, "b": true}
	result := chainExpandArgs(args, nil, nil)
	if result["n"] != 42 || result["b"] != true {
		t.Error("non-string values should pass through unchanged")
	}
}

func TestChainExpandArgs_Empty(t *testing.T) {
	result := chainExpandArgs(nil, nil, nil)
	if len(result) != 0 {
		t.Error("nil args should return empty map")
	}
}

// ─── Step validation ────────────────────────────────────────────────────────

func TestParseChainSteps_Valid(t *testing.T) {
	js := `[{"name":"a","tool":"echo"},{"tool":"greet","on_fail":"skip"}]`
	steps, err := parseChainSteps(js)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("got %d steps", len(steps))
	}
	if steps[0].Name != "a" {
		t.Error("step 0 name not preserved")
	}
	if steps[1].Name != "step_1" {
		t.Errorf("auto-name = %q, want step_1", steps[1].Name)
	}
}

func TestParseChainSteps_InvalidJSON(t *testing.T) {
	_, err := parseChainSteps("not json")
	if err == nil {
		t.Error("expected error")
	}
}

func TestParseChainSteps_EmptyArray(t *testing.T) {
	_, err := parseChainSteps("[]")
	if err == nil || !strings.Contains(err.Error(), "at least one step") {
		t.Errorf("expected empty error, got %v", err)
	}
}

func TestParseChainSteps_MissingTool(t *testing.T) {
	_, err := parseChainSteps(`[{"name":"a"}]`)
	if err == nil || !strings.Contains(err.Error(), "tool is required") {
		t.Errorf("expected tool error, got %v", err)
	}
}

func TestParseChainSteps_BadOnFail(t *testing.T) {
	_, err := parseChainSteps(`[{"tool":"x","on_fail":"explode"}]`)
	if err == nil || !strings.Contains(err.Error(), "on_fail must be") {
		t.Errorf("expected on_fail error, got %v", err)
	}
}

func TestParseChainSteps_TooMany(t *testing.T) {
	steps := make([]ChainStep, maxChainSteps+1)
	for i := range steps {
		steps[i] = ChainStep{Tool: "x"}
	}
	b, _ := json.Marshal(steps)
	_, err := parseChainSteps(string(b))
	if err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Errorf("expected max steps error, got %v", err)
	}
}

// ─── Chain execution ────────────────────────────────────────────────────────

// mockToolRegistry creates a ToolRegistry with simple mock tools for testing.
func mockToolRegistry() *agent.ToolRegistry {
	reg := agent.NewToolRegistry()
	reg.Register("echo", func(ctx context.Context, args map[string]any) (string, error) {
		return agent.ArgString(args, "text"), nil
	})
	reg.Register("upper", func(ctx context.Context, args map[string]any) (string, error) {
		return strings.ToUpper(agent.ArgString(args, "text")), nil
	})
	reg.Register("fail", func(ctx context.Context, args map[string]any) (string, error) {
		return "", fmt.Errorf("intentional failure")
	})
	reg.Register("concat", func(ctx context.Context, args map[string]any) (string, error) {
		a := agent.ArgString(args, "a")
		b := agent.ArgString(args, "b")
		return a + b, nil
	})
	return reg
}

func TestExecuteChain_HappyPath(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name: "test",
		Steps: []ChainStep{
			{Name: "greet", Tool: "echo", Args: map[string]any{"text": "hello"}},
			{Name: "shout", Tool: "upper", Args: map[string]any{"text": "{{steps.greet}}"}},
		},
	}

	results, err := executeChain(context.Background(), toolReg, chain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].Output != "hello" {
		t.Errorf("step 0 output = %q", results[0].Output)
	}
	if results[1].Output != "HELLO" {
		t.Errorf("step 1 output = %q, want HELLO", results[1].Output)
	}
}

func TestExecuteChain_ParamExpansion(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name: "test",
		Steps: []ChainStep{
			{Name: "say", Tool: "echo", Args: map[string]any{"text": "hi {{params.name}}"}},
		},
	}

	params := map[string]any{"name": "world"}
	results, err := executeChain(context.Background(), toolReg, chain, params)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Output != "hi world" {
		t.Errorf("got %q", results[0].Output)
	}
}

func TestExecuteChain_StepChaining(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name: "test",
		Steps: []ChainStep{
			{Name: "a", Tool: "echo", Args: map[string]any{"text": "foo"}},
			{Name: "b", Tool: "echo", Args: map[string]any{"text": "bar"}},
			{Name: "c", Tool: "concat", Args: map[string]any{"a": "{{steps.a}}", "b": "{{steps.b}}"}},
		},
	}

	results, err := executeChain(context.Background(), toolReg, chain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[2].Output != "foobar" {
		t.Errorf("concat output = %q, want foobar", results[2].Output)
	}
}

func TestExecuteChain_OnFailStop(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name: "test",
		Steps: []ChainStep{
			{Name: "ok", Tool: "echo", Args: map[string]any{"text": "fine"}},
			{Name: "bad", Tool: "fail", OnFail: "stop"},
			{Name: "never", Tool: "echo", Args: map[string]any{"text": "nope"}},
		},
	}

	results, err := executeChain(context.Background(), toolReg, chain, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "stopped at step") {
		t.Errorf("unexpected error: %v", err)
	}
	// Should have 2 results: ok + failed. "never" was not reached.
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Status != "ok" {
		t.Error("step 0 should be ok")
	}
	if results[1].Status != "failed" {
		t.Error("step 1 should be failed")
	}
}

func TestExecuteChain_OnFailSkip(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name: "test",
		Steps: []ChainStep{
			{Name: "bad", Tool: "fail", OnFail: "skip"},
			{Name: "ok", Tool: "echo", Args: map[string]any{"text": "reached"}},
		},
	}

	results, err := executeChain(context.Background(), toolReg, chain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].Status != "skipped" {
		t.Errorf("step 0 status = %q, want skipped", results[0].Status)
	}
	if results[1].Status != "ok" {
		t.Error("step 1 should still execute")
	}
	if results[1].Output != "reached" {
		t.Errorf("step 1 output = %q", results[1].Output)
	}
}

func TestExecuteChain_OnFailContinue(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name: "test",
		Steps: []ChainStep{
			{Name: "bad", Tool: "fail", OnFail: "continue"},
			{Name: "ref", Tool: "echo", Args: map[string]any{"text": "prev={{steps.bad}}!"}},
		},
	}

	results, err := executeChain(context.Background(), toolReg, chain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "failed" {
		t.Error("step 0 should be failed")
	}
	if results[0].Error == "" {
		t.Error("step 0 should have error")
	}
	// The failed step's output should be empty string.
	if results[1].Output != "prev=!" {
		t.Errorf("step 1 output = %q, want prev=!", results[1].Output)
	}
}

func TestExecuteChain_DefaultOnFailIsStop(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name: "test",
		Steps: []ChainStep{
			{Name: "bad", Tool: "fail"}, // no on_fail → default stop
			{Name: "never", Tool: "echo", Args: map[string]any{"text": "nope"}},
		},
	}

	results, err := executeChain(context.Background(), toolReg, chain, nil)
	if err == nil {
		t.Fatal("expected error with default stop")
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (stopped after first)", len(results))
	}
}

func TestExecuteChain_UnknownTool(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name:  "test",
		Steps: []ChainStep{{Name: "bad", Tool: "no_such_tool"}},
	}

	results, err := executeChain(context.Background(), toolReg, chain, nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	if results[0].Status != "failed" {
		t.Error("unknown tool step should be failed")
	}
}

func TestExecuteChain_RecursionDepthLimit(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name:  "test",
		Steps: []ChainStep{{Tool: "echo", Args: map[string]any{"text": "hi"}}},
	}

	// Simulate being at max depth already.
	ctx := context.WithValue(context.Background(), chainDepthKey{}, maxChainDepth)
	_, err := executeChain(ctx, toolReg, chain, nil)
	if err == nil || !strings.Contains(err.Error(), "recursion limit") {
		t.Errorf("expected recursion error, got %v", err)
	}
}

func TestExecuteChain_ContextCancellation(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name:  "test",
		Steps: []ChainStep{{Tool: "echo", Args: map[string]any{"text": "hi"}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := executeChain(ctx, toolReg, chain, nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestExecuteChain_AutoStepNames(t *testing.T) {
	toolReg := mockToolRegistry()
	chain := &ChainDef{
		Name: "test",
		Steps: []ChainStep{
			{Tool: "echo", Args: map[string]any{"text": "a"}},  // no name
			{Tool: "echo", Args: map[string]any{"text": "b"}},  // no name
		},
	}

	results, err := executeChain(context.Background(), toolReg, chain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Step != "step_0" || results[1].Step != "step_1" {
		t.Errorf("auto-names: %q, %q", results[0].Step, results[1].Step)
	}

	// Verify auto-named steps can be referenced.
	chain2 := &ChainDef{
		Name: "test2",
		Steps: []ChainStep{
			{Tool: "echo", Args: map[string]any{"text": "x"}},
			{Tool: "echo", Args: map[string]any{"text": "got={{steps.step_0}}"}},
		},
	}
	results2, err := executeChain(context.Background(), toolReg, chain2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results2[1].Output != "got=x" {
		t.Errorf("auto-name reference: got %q", results2[1].Output)
	}
}

// ─── Result formatting ─────────────────────────────────────────────────────

func TestFormatChainResult_Success(t *testing.T) {
	results := []chainStepResult{
		{Step: "a", Tool: "echo", Output: "hello", Status: "ok"},
		{Step: "b", Tool: "upper", Output: "HELLO", Status: "ok"},
	}
	out := formatChainResult("my_chain", results, nil)
	if !strings.Contains(out, `"my_chain" completed (2/2 steps ok)`) {
		t.Errorf("header missing: %s", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "HELLO") {
		t.Error("outputs missing")
	}
}

func TestFormatChainResult_Failure(t *testing.T) {
	results := []chainStepResult{
		{Step: "ok", Tool: "echo", Output: "fine", Status: "ok"},
		{Step: "bad", Tool: "fail", Error: "boom", Status: "failed"},
	}
	out := formatChainResult("test", results, fmt.Errorf("stopped at bad"))
	if !strings.Contains(out, "failed (1/2 steps completed)") {
		t.Errorf("failure header missing: %s", out)
	}
	if !strings.Contains(out, "⚠️") {
		t.Error("warning marker missing")
	}
}

func TestFormatChainResult_Truncation(t *testing.T) {
	longOutput := strings.Repeat("x", chainOutputMax+500)
	results := []chainStepResult{
		{Step: "big", Tool: "echo", Output: longOutput, Status: "ok"},
	}
	out := formatChainResult("test", results, nil)
	if !strings.Contains(out, "truncated") {
		t.Error("long output should be truncated in summary")
	}
	if strings.Contains(out, longOutput) {
		t.Error("full long output should not appear")
	}
}

// ─── Tool functions ─────────────────────────────────────────────────────────

func TestChainDefineTool_Valid(t *testing.T) {
	reg := NewChainRegistry()
	fn := ChainDefineTool(reg)

	result, err := fn(context.Background(), map[string]any{
		"name":        "my_chain",
		"description": "test chain",
		"steps_json":  `[{"name":"s1","tool":"echo","args":{"text":"hi"}}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, `"my_chain" defined`) {
		t.Errorf("unexpected result: %s", result)
	}

	// Verify it was stored.
	got, ok := reg.get("my_chain")
	if !ok {
		t.Fatal("chain not in registry")
	}
	if got.Description != "test chain" {
		t.Error("description not stored")
	}
}

func TestChainDefineTool_MissingName(t *testing.T) {
	fn := ChainDefineTool(NewChainRegistry())
	_, err := fn(context.Background(), map[string]any{
		"steps_json": `[{"tool":"echo"}]`,
	})
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestChainDefineTool_MissingSteps(t *testing.T) {
	fn := ChainDefineTool(NewChainRegistry())
	_, err := fn(context.Background(), map[string]any{
		"name": "test",
	})
	if err == nil {
		t.Error("expected error for missing steps_json")
	}
}

func TestChainDefineTool_InvalidStepsJSON(t *testing.T) {
	fn := ChainDefineTool(NewChainRegistry())
	_, err := fn(context.Background(), map[string]any{
		"name":       "test",
		"steps_json": "not json at all",
	})
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestChainRunTool_Success(t *testing.T) {
	chainReg := NewChainRegistry()
	toolReg := mockToolRegistry()

	chainReg.define(ChainDef{
		Name: "greet",
		Steps: []ChainStep{
			{Name: "say", Tool: "echo", Args: map[string]any{"text": "hello {{params.who}}"}},
			{Name: "shout", Tool: "upper", Args: map[string]any{"text": "{{steps.say}}"}},
		},
	})

	fn := ChainRunTool(chainReg, toolReg)
	result, err := fn(context.Background(), map[string]any{
		"chain":       "greet",
		"params_json": `{"who":"world"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "completed") {
		t.Errorf("expected completed: %s", result)
	}
	if !strings.Contains(result, "HELLO WORLD") {
		t.Errorf("expected HELLO WORLD in result: %s", result)
	}
}

func TestChainRunTool_UnknownChain(t *testing.T) {
	chainReg := NewChainRegistry()
	toolReg := mockToolRegistry()

	fn := ChainRunTool(chainReg, toolReg)
	_, err := fn(context.Background(), map[string]any{
		"chain": "nonexistent",
	})
	if err == nil {
		t.Error("expected error for unknown chain")
	}
	if !strings.Contains(err.Error(), "no chains defined") {
		t.Errorf("error should mention no chains: %v", err)
	}
}

func TestChainRunTool_UnknownChainWithSuggestions(t *testing.T) {
	chainReg := NewChainRegistry()
	toolReg := mockToolRegistry()
	chainReg.define(ChainDef{Name: "foo", Steps: []ChainStep{{Tool: "echo"}}})

	fn := ChainRunTool(chainReg, toolReg)
	_, err := fn(context.Background(), map[string]any{
		"chain": "bar",
	})
	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "foo") {
		t.Errorf("error should list available chains: %v", err)
	}
}

func TestChainRunTool_InvalidParamsJSON(t *testing.T) {
	chainReg := NewChainRegistry()
	toolReg := mockToolRegistry()
	chainReg.define(ChainDef{Name: "c", Steps: []ChainStep{{Tool: "echo"}}})

	fn := ChainRunTool(chainReg, toolReg)
	_, err := fn(context.Background(), map[string]any{
		"chain":       "c",
		"params_json": "not json",
	})
	if err == nil {
		t.Error("expected error for invalid params_json")
	}
}

func TestChainRunTool_NoParams(t *testing.T) {
	chainReg := NewChainRegistry()
	toolReg := mockToolRegistry()
	chainReg.define(ChainDef{
		Name:  "simple",
		Steps: []ChainStep{{Name: "s", Tool: "echo", Args: map[string]any{"text": "no params"}}},
	})

	fn := ChainRunTool(chainReg, toolReg)
	result, err := fn(context.Background(), map[string]any{"chain": "simple"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "no params") {
		t.Error("should work without params_json")
	}
}

func TestChainRunTool_ChainFailureReturnsResult(t *testing.T) {
	chainReg := NewChainRegistry()
	toolReg := mockToolRegistry()
	chainReg.define(ChainDef{
		Name: "fail_chain",
		Steps: []ChainStep{
			{Name: "ok", Tool: "echo", Args: map[string]any{"text": "fine"}},
			{Name: "bad", Tool: "fail"}, // on_fail=stop (default)
		},
	})

	fn := ChainRunTool(chainReg, toolReg)
	result, err := fn(context.Background(), map[string]any{"chain": "fail_chain"})
	// Chain failures are reported as content, not Go errors.
	if err != nil {
		t.Fatalf("chain_run should not return Go error for chain failures: %v", err)
	}
	if !strings.Contains(result, "failed") {
		t.Error("result should describe the failure")
	}
	if !strings.Contains(result, "⚠️") {
		t.Error("result should have warning marker")
	}
}

func TestChainRunTool_MissingChainName(t *testing.T) {
	fn := ChainRunTool(NewChainRegistry(), mockToolRegistry())
	_, err := fn(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing chain name")
	}
}

func TestChainListTool_Empty(t *testing.T) {
	fn := ChainListTool(NewChainRegistry())
	result, err := fn(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "No chains defined") {
		t.Errorf("expected empty message: %s", result)
	}
}

func TestChainListTool_WithChains(t *testing.T) {
	reg := NewChainRegistry()
	reg.define(ChainDef{
		Name:        "search_read",
		Description: "Search then read",
		Steps: []ChainStep{
			{Name: "find", Tool: "grep_search"},
			{Name: "read", Tool: "read_file", OnFail: "skip"},
		},
	})
	reg.define(ChainDef{
		Name:  "quick",
		Steps: []ChainStep{{Name: "run", Tool: "echo"}},
	})

	fn := ChainListTool(reg)
	result, err := fn(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "2 chain(s)") {
		t.Error("should show count")
	}
	if !strings.Contains(result, "search_read") || !strings.Contains(result, "quick") {
		t.Error("should list both chains")
	}
	if !strings.Contains(result, "Search then read") {
		t.Error("should show description")
	}
}

// ─── End-to-end chain workflow ──────────────────────────────────────────────

func TestChainWorkflow_DefineAndRun(t *testing.T) {
	chainReg := NewChainRegistry()
	toolReg := mockToolRegistry()

	defineFn := ChainDefineTool(chainReg)
	runFn := ChainRunTool(chainReg, toolReg)
	listFn := ChainListTool(chainReg)

	// 1. Define a chain.
	_, err := defineFn(context.Background(), map[string]any{
		"name":        "make_greeting",
		"description": "Generate and uppercase a greeting",
		"steps_json": `[
			{"name":"greet","tool":"echo","args":{"text":"hello {{params.name}}"}},
			{"name":"loud","tool":"upper","args":{"text":"{{steps.greet}}"}}
		]`,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. List chains.
	list, err := listFn(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "make_greeting") {
		t.Error("chain not in list")
	}

	// 3. Run the chain.
	result, err := runFn(context.Background(), map[string]any{
		"chain":       "make_greeting",
		"params_json": `{"name":"alice"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "HELLO ALICE") {
		t.Errorf("expected HELLO ALICE in result: %s", result)
	}
	if !strings.Contains(result, "completed (2/2 steps ok)") {
		t.Errorf("expected completion summary: %s", result)
	}
}
