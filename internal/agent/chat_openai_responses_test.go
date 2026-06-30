package agent

import "testing"

func TestOpenAIResponsesBuildRequest(t *testing.T) {
	p := &OpenAIResponsesProvider{Model: "gpt-4.1"}
	req := p.buildRequest([]LLMMessage{{Role: "user", Content: "hi"}}, []ToolDefinition{{Name: "lookup"}}, ChatOptions{MaxTokens: 7, ThinkingBudget: 1})
	if req.Model != "gpt-4.1" || req.MaxOutputTokens != 7 {
		t.Fatalf("bad request: %+v", req)
	}
	if len(req.Input) != 1 || len(req.Tools) != 1 || req.Reasoning == nil {
		t.Fatalf("missing mapping: %+v", req)
	}
}
