package agent

import (
	"encoding/json"
	"strings"
)

type ModelCompatibility struct {
	RequiresMistralToolIDs bool `json:"requires_mistral_tool_ids,omitempty"`
	SupportsResponsesAPI   bool `json:"supports_responses_api,omitempty"`
	SupportsThinking       bool `json:"supports_thinking,omitempty"`
}
type CatalogModelRow struct {
	ID, ProviderID, Name string
	Aliases              []string
	ContextWindowTokens  int
	Capabilities         ProviderCapabilities
	Compatibility        ModelCompatibility
}

var builtinModelCatalogRows = []CatalogModelRow{
	{ID: "mistral-large-latest", ProviderID: "mistral", Name: "Mistral Large", Aliases: []string{"mistral-large"}, ContextWindowTokens: 128000, Capabilities: ProviderCapabilities{SupportsTools: true, SupportsStreaming: true}, Compatibility: ModelCompatibility{RequiresMistralToolIDs: true}},
	{ID: "kimi-k2-0711-preview", ProviderID: "moonshot", Name: "Kimi K2", Aliases: []string{"kimi-k2", "moonshot/kimi-k2"}, ContextWindowTokens: 128000, Capabilities: ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsThinking: true}, Compatibility: ModelCompatibility{SupportsThinking: true}},
	{ID: "gpt-4.1", ProviderID: "openai-responses", Name: "GPT-4.1", Aliases: []string{"openai-responses/gpt-4.1"}, ContextWindowTokens: 1047576, Capabilities: ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsThinking: true}, Compatibility: ModelCompatibility{SupportsResponsesAPI: true, SupportsThinking: true}},
	{ID: "gpt-4.1", ProviderID: "azure-responses", Name: "Azure GPT-4.1", Aliases: []string{"azure/gpt-4.1"}, ContextWindowTokens: 1047576, Capabilities: ProviderCapabilities{SupportsTools: true, SupportsStreaming: true, SupportsVision: true, SupportsThinking: true}, Compatibility: ModelCompatibility{SupportsResponsesAPI: true, SupportsThinking: true}},
	{ID: "gemini-2.5-flash", ProviderID: "vertex", Name: "Vertex Gemini 2.5 Flash", Aliases: []string{"vertex/gemini-2.5-flash"}, ContextWindowTokens: 1000000, Capabilities: ProviderCapabilities{SupportsTools: true, SupportsVision: true, SupportsThinking: true}},
}

func normalizeModelRef(ref string) (providerID, modelID string) {
	s := strings.TrimSpace(ref)
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "kimi/") || strings.HasPrefix(low, "moonshot/") || strings.HasPrefix(low, "kimicode/") {
		return "moonshot", normalizeKimiModelID(s)
	}
	if i := strings.Index(s, "/"); i > 0 {
		return strings.ToLower(s[:i]), s[i+1:]
	}
	return "", s
}
func resolveCatalogModelRef(ref string) (CatalogModelRow, bool) {
	p, m := normalizeModelRef(ref)
	for _, r := range builtinModelCatalogRows {
		if (p == "" || p == r.ProviderID) && strings.EqualFold(m, r.ID) {
			return r, true
		}
		for _, a := range r.Aliases {
			ap, am := normalizeModelRef(a)
			if strings.EqualFold(ref, a) || ((p == "" || p == ap) && strings.EqualFold(m, am)) {
				return r, true
			}
		}
	}
	return CatalogModelRow{}, false
}
func catalogRowsForProvider(providerID string, caps ProviderCapabilities) []ModelInfo {
	var out []ModelInfo
	for _, r := range builtinModelCatalogRows {
		if r.ProviderID != providerID {
			continue
		}
		c := r.Capabilities
		if c == (ProviderCapabilities{}) {
			c = caps
		}
		out = append(out, ModelInfo{ID: r.ID, Name: r.Name, ProviderID: r.ProviderID, ContextWindowTokens: r.ContextWindowTokens, Capabilities: c, Metadata: map[string]any{"aliases": r.Aliases, "compatibility": r.Compatibility}})
	}
	return out
}

func toolsToGenericOpenAI(defs []ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": d.Name, "description": d.Description, "parameters": toolInputSchemaMap(d)}})
	}
	return out
}
func toolsToResponses(defs []ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		out = append(out, map[string]any{"type": "function", "name": d.Name, "description": d.Description, "parameters": toolInputSchemaMap(d)})
	}
	return out
}

func parseGenericChatCompletion(body []byte) (*LLMResponse, error) {
	var r struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct{ PromptTokens, CompletionTokens int } `json:"usage"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	if len(r.Choices) == 0 {
		return &LLMResponse{}, nil
	}
	ch := r.Choices[0]
	var tcs []ToolCall
	for _, tc := range ch.Message.ToolCalls {
		var args map[string]any
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		tcs = append(tcs, ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: args})
	}
	return &LLMResponse{Content: strings.TrimSpace(ch.Message.Content), ToolCalls: tcs, Usage: ProviderUsage{InputTokens: int64(r.Usage.PromptTokens), OutputTokens: int64(r.Usage.CompletionTokens)}, NeedsToolResults: ch.FinishReason == "tool_calls"}, nil
}
func parseResponsesAPI(body []byte) (*LLMResponse, error) {
	var r struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Type, Role, Content     string
			Name, CallID, Arguments string
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	content := r.OutputText
	var tcs []ToolCall
	for _, o := range r.Output {
		if o.Type == "message" && content == "" {
			content = o.Content
		}
		if o.Type == "function_call" {
			var args map[string]any
			_ = json.Unmarshal([]byte(o.Arguments), &args)
			tcs = append(tcs, ToolCall{ID: o.CallID, Name: o.Name, Args: args})
		}
	}
	return &LLMResponse{Content: strings.TrimSpace(content), ToolCalls: tcs, NeedsToolResults: len(tcs) > 0, Usage: ProviderUsage{InputTokens: int64(r.Usage.InputTokens), OutputTokens: int64(r.Usage.OutputTokens)}}, nil
}
func parseGeminiLikeResponse(body []byte) (*LLMResponse, error) {
	var r geminiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	if len(r.Candidates) == 0 {
		return &LLMResponse{}, nil
	}
	var content string
	var tcs []ToolCall
	for _, p := range r.Candidates[0].Content.Parts {
		content += p.Text
		if p.FunctionCall.Name != "" {
			tcs = append(tcs, ToolCall{Name: p.FunctionCall.Name, Args: p.FunctionCall.Args})
		}
	}
	return &LLMResponse{Content: strings.TrimSpace(content), ToolCalls: tcs, NeedsToolResults: len(tcs) > 0, Usage: ProviderUsage{InputTokens: r.UsageMetadata.PromptTokenCount, OutputTokens: r.UsageMetadata.CandidatesTokenCount}}, nil
}
