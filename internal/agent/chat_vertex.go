package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type VertexChatProvider struct {
	BaseURL, APIKey, Model string
	Client                 *http.Client
}
type vertexRequest struct {
	Contents          []geminiContent    `json:"contents"`
	SystemInstruction *geminiContent     `json:"systemInstruction,omitempty"`
	GenerationConfig  map[string]any     `json:"generationConfig,omitempty"`
	Tools             []geminiToolBundle `json:"tools,omitempty"`
}

func (p *VertexChatProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	r, e := p.Chat(ctx, buildLLMMessagesFromTurnWithProfile(turn, "", disabledPromptCacheProfile()), nil, chatOptionsFromTurn(turn, disabledPromptCacheProfile()))
	if r == nil {
		return ProviderResult{}, e
	}
	return llmResponseToProviderResult(r), e
}
func (p *VertexChatProvider) Chat(ctx context.Context, messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) (*LLMResponse, error) {
	rb := p.buildRequest(messages, tools, opts)
	b, _ := json.Marshal(rb)
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://aiplatform.googleapis.com/v1"
	}
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "gemini-2.5-flash"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+model+":generateContent", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	c := p.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vertex: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return parseGeminiLikeResponse(body)
}
func (p *VertexChatProvider) buildRequest(messages []LLMMessage, tools []ToolDefinition, opts ChatOptions) vertexRequest {
	var sys *geminiContent
	contents := make([]geminiContent, 0, len(messages))
	for _, m := range messages {
		if m.Role == "system" {
			sys = &geminiContent{Role: "system", Parts: []geminiPart{{Text: m.Content}}}
			continue
		}
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, geminiContent{Role: role, Parts: []geminiPart{{Text: m.Content}}})
	}
	r := vertexRequest{Contents: contents, SystemInstruction: sys}
	if opts.MaxTokens > 0 {
		r.GenerationConfig = map[string]any{"maxOutputTokens": opts.MaxTokens}
	}
	if len(tools) > 0 {
		r.Tools = toolDefsToGemini(tools)
	}
	return r
}

// StreamEvents implements native Vertex streamGenerateContent SSE and reuses
// the Gemini event accumulator for text, thought, tool-call, and usage frames.
func (p *VertexChatProvider) StreamEvents(ctx context.Context, turn Turn, emit ProviderStreamEventSink) (ProviderResult, error) {
	return runProviderEventStream(emit, func(emit ProviderStreamEventSink) (ProviderResult, error) {
		messages := buildLLMMessagesFromTurnWithProfile(turn, "", disabledPromptCacheProfile())
		body, err := json.Marshal(p.buildRequest(messages, turn.Tools, chatOptionsFromTurn(turn, disabledPromptCacheProfile())))
		if err != nil {
			return ProviderResult{}, err
		}
		base := strings.TrimRight(p.BaseURL, "/")
		if base == "" {
			base = "https://aiplatform.googleapis.com/v1"
		}
		model := strings.TrimSpace(p.Model)
		if model == "" {
			model = "publishers/google/models/gemini-2.5-flash"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+model+":streamGenerateContent?alt=sse", bytes.NewReader(body))
		if err != nil {
			return ProviderResult{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if p.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}
		client := p.Client
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return ProviderResult{}, fmt.Errorf("vertex stream request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			return ProviderResult{}, fmt.Errorf("vertex stream: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
		}
		return consumeGeminiLikeStream(resp.Body, emit)
	})
}

func (p *VertexChatProvider) Stream(ctx context.Context, turn Turn, onChunk func(string)) (ProviderResult, error) {
	return streamEventsAsLegacy(ctx, turn, onChunk, p)
}

var _ EventStreamingProvider = (*VertexChatProvider)(nil)
var _ StreamingProvider = (*VertexChatProvider)(nil)

func normalizeVertexModel(model string) string {
	m := strings.TrimPrefix(strings.TrimSpace(model), "vertex/")
	if strings.Contains(m, "/publishers/") || strings.HasPrefix(m, "projects/") {
		return m
	}
	return "publishers/google/models/" + m
}
