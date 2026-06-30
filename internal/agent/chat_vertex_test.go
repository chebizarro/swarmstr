package agent

import "testing"

func TestVertexBuildRequestAndModel(t *testing.T) {
	p := &VertexChatProvider{Model: normalizeVertexModel("vertex/gemini-2.5-flash")}
	req := p.buildRequest([]LLMMessage{{Role: "system", Content: "sys"}, {Role: "assistant", Content: "ok"}}, nil, ChatOptions{MaxTokens: 5})
	if p.Model != "publishers/google/models/gemini-2.5-flash" {
		t.Fatalf("bad model: %s", p.Model)
	}
	if req.SystemInstruction == nil || len(req.Contents) != 1 || req.Contents[0].Role != "model" {
		t.Fatalf("bad request: %+v", req)
	}
}
