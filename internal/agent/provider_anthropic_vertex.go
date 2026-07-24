package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/oauth2/google"
)

// AnthropicVertexProvider calls Anthropic models through Vertex AI's native
// streamRawPredict transport. It accepts an explicit OAuth access token or ADC.
type AnthropicVertexProvider struct {
	Model       string
	ProjectID   string
	Region      string
	AccessToken string
	BaseURL     string
	Client      *http.Client
}

func (p *AnthropicVertexProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	return p.StreamEvents(ctx, turn, nil)
}

func (p *AnthropicVertexProvider) Stream(ctx context.Context, turn Turn, onChunk func(string)) (ProviderResult, error) {
	return streamEventsAsLegacy(ctx, turn, onChunk, p)
}

func (p *AnthropicVertexProvider) StreamEvents(ctx context.Context, turn Turn, emit ProviderStreamEventSink) (ProviderResult, error) {
	return runProviderEventStream(emit, func(emit ProviderStreamEventSink) (ProviderResult, error) {
		body, err := p.requestBody(turn)
		if err != nil {
			return ProviderResult{}, err
		}
		endpoint, err := p.endpoint(ctx)
		if err != nil {
			return ProviderResult{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return ProviderResult{}, err
		}
		token, err := p.accessToken(ctx)
		if err != nil {
			return ProviderResult{}, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		client := p.Client
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return ProviderResult{}, fmt.Errorf("anthropic vertex stream: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return ProviderResult{}, fmt.Errorf("anthropic vertex stream returned %s", resp.Status)
		}
		return consumeAnthropicVertexSSE(resp.Body, emit)
	})
}

func (p *AnthropicVertexProvider) accessToken(ctx context.Context) (string, error) {
	token := strings.TrimSpace(p.AccessToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("VERTEX_ACCESS_TOKEN"))
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GOOGLE_ACCESS_TOKEN"))
	}
	if token != "" {
		return token, nil
	}
	credentials, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return "", fmt.Errorf("resolve Anthropic Vertex application default credentials: %w", err)
	}
	oauthToken, err := credentials.TokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("refresh Anthropic Vertex access token: %w", err)
	}
	if strings.TrimSpace(oauthToken.AccessToken) == "" {
		return "", fmt.Errorf("Anthropic Vertex application default credentials returned an empty access token")
	}
	return oauthToken.AccessToken, nil
}

func (p *AnthropicVertexProvider) endpoint(ctx context.Context) (string, error) {
	if base := strings.TrimSpace(p.BaseURL); base != "" {
		return strings.TrimRight(base, "/"), nil
	}
	region := strings.TrimSpace(p.Region)
	if region == "" {
		region = strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_LOCATION"))
	}
	if region == "" {
		region = strings.TrimSpace(os.Getenv("VERTEX_REGION"))
	}
	if region == "" {
		region = "us-east5"
	}
	project := strings.TrimSpace(p.ProjectID)
	if project == "" {
		project = strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
	}
	if project == "" {
		credentials, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return "", fmt.Errorf("resolve Anthropic Vertex project: %w", err)
		}
		project = strings.TrimSpace(credentials.ProjectID)
	}
	if project == "" {
		return "", fmt.Errorf("GOOGLE_CLOUD_PROJECT or an ADC project id is required for Anthropic Vertex")
	}
	model := normalizeAnthropicVertexModel(p.Model)
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:streamRawPredict", region, url.PathEscape(project), region, url.PathEscape(model)), nil
}

func normalizeAnthropicVertexModel(model string) string {
	model = strings.TrimSpace(model)
	for _, prefix := range []string{"anthropic-vertex/", "vertex-claude/"} {
		model = strings.TrimPrefix(model, prefix)
	}
	return model
}

func (p *AnthropicVertexProvider) requestBody(turn Turn) ([]byte, error) {
	model := normalizeAnthropicVertexModel(p.Model)
	if model == "" || model == "anthropic-vertex" {
		return nil, fmt.Errorf("Anthropic Vertex model id is required")
	}
	messages := buildLLMMessagesFromTurn(turn, "")
	var system string
	wire := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "system" {
			if system != "" {
				system += "\n\n"
			}
			system += msg.Content
			continue
		}
		role := msg.Role
		if role == "tool" {
			role = "user"
		}
		var content []map[string]any
		if msg.Role == "tool" {
			content = append(content, map[string]any{"type": "tool_result", "tool_use_id": msg.ToolCallID, "content": msg.Content})
		} else {
			if msg.Content != "" {
				content = append(content, map[string]any{"type": "text", "text": msg.Content})
			}
			for _, call := range msg.ToolCalls {
				args := call.Args
				if args == nil {
					args = map[string]any{}
				}
				content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": args})
			}
		}
		if len(content) > 0 {
			if len(wire) > 0 && wire[len(wire)-1]["role"] == role {
				prior, _ := wire[len(wire)-1]["content"].([]map[string]any)
				wire[len(wire)-1]["content"] = append(prior, content...)
			} else {
				wire = append(wire, map[string]any{"role": role, "content": content})
			}
		}
	}
	maxTokens := turn.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	if turn.ThinkingBudget > 0 && maxTokens <= turn.ThinkingBudget {
		maxTokens = turn.ThinkingBudget + turn.ThinkingBudget/2
		if maxTokens < 16000 {
			maxTokens = 16000
		}
	}
	payload := map[string]any{"anthropic_version": "vertex-2023-10-16", "messages": wire, "max_tokens": maxTokens, "stream": true}
	if turn.ThinkingBudget > 0 {
		payload["thinking"] = map[string]any{"type": "enabled", "budget_tokens": turn.ThinkingBudget}
	}
	if system != "" {
		payload["system"] = system
	}
	if len(turn.Tools) > 0 {
		tools := make([]map[string]any, 0, len(turn.Tools))
		for _, def := range turn.Tools {
			tools = append(tools, map[string]any{"name": def.Name, "description": def.Description, "input_schema": toolInputSchemaMap(def)})
		}
		payload["tools"] = tools
	}
	return json.Marshal(payload)
}

type anthropicVertexStreamEnvelope struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage anthropicVertexUsage `json:"usage"`
	} `json:"message"`
	Usage        anthropicVertexUsage                            `json:"usage"`
	ContentBlock struct{ Type, ID, Name, Text, Thinking string } `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicVertexUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

func consumeAnthropicVertexSSE(r interface{ Read([]byte) (int, error) }, emit ProviderStreamEventSink) (ProviderResult, error) {
	var text strings.Builder
	var usage ProviderUsage
	type toolAcc struct {
		id, name  string
		arguments strings.Builder
	}
	tools := map[int]*toolAcc{}
	maxTool := -1
	err := decodeProviderSSE(r, func(raw []byte) error {
		var evt anthropicVertexStreamEnvelope
		if err := json.Unmarshal(raw, &evt); err != nil {
			return fmt.Errorf("anthropic vertex stream decode: %w", err)
		}
		if evt.Error != nil || evt.Type == "error" {
			if evt.Error != nil {
				return fmt.Errorf("anthropic vertex API: %s", evt.Error.Message)
			}
			return fmt.Errorf("anthropic vertex API error")
		}
		switch evt.Type {
		case "message_start":
			usage = normalizeAnthropicVertexUsage(evt.Message.Usage, usage)
			if emit != nil && hasProviderUsage(usage) {
				emit(ProviderStreamEvent{Type: ProviderStreamUsage, Usage: usage})
			}
		case "content_block_start":
			if evt.ContentBlock.Type == "tool_use" {
				tools[evt.Index] = &toolAcc{id: evt.ContentBlock.ID, name: evt.ContentBlock.Name}
				if evt.Index > maxTool {
					maxTool = evt.Index
				}
				if emit != nil {
					emit(ProviderStreamEvent{Type: ProviderStreamToolCallDelta, ToolCallDelta: ProviderToolCallDelta{Index: evt.Index, ID: evt.ContentBlock.ID, Name: evt.ContentBlock.Name}})
				}
			}
			if evt.ContentBlock.Type == "thinking" && evt.ContentBlock.Thinking != "" && emit != nil {
				emit(ProviderStreamEvent{Type: ProviderStreamThinkingDelta, ThinkingDelta: evt.ContentBlock.Thinking})
			}
		case "content_block_delta":
			switch evt.Delta.Type {
			case "text_delta":
				text.WriteString(evt.Delta.Text)
				if emit != nil && evt.Delta.Text != "" {
					emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: evt.Delta.Text})
				}
			case "thinking_delta":
				if emit != nil && evt.Delta.Thinking != "" {
					emit(ProviderStreamEvent{Type: ProviderStreamThinkingDelta, ThinkingDelta: evt.Delta.Thinking})
				}
			case "input_json_delta":
				acc := tools[evt.Index]
				if acc == nil {
					acc = &toolAcc{}
					tools[evt.Index] = acc
				}
				acc.arguments.WriteString(evt.Delta.PartialJSON)
				if evt.Index > maxTool {
					maxTool = evt.Index
				}
				if emit != nil {
					emit(ProviderStreamEvent{Type: ProviderStreamToolCallDelta, ToolCallDelta: ProviderToolCallDelta{Index: evt.Index, ID: acc.id, Name: acc.name, ArgumentsDelta: evt.Delta.PartialJSON}})
				}
			}
		case "message_delta":
			next := normalizeAnthropicVertexUsage(evt.Usage, usage)
			if hasProviderUsage(next) && !providerUsageEqual(next, usage) {
				usage = next
				if emit != nil {
					emit(ProviderStreamEvent{Type: ProviderStreamUsage, Usage: usage})
				}
			}
		}
		return nil
	})
	if err != nil {
		return ProviderResult{}, err
	}
	var calls []ToolCall
	for idx := 0; idx <= maxTool; idx++ {
		acc := tools[idx]
		if acc == nil || acc.name == "" {
			continue
		}
		args := map[string]any{}
		if raw := acc.arguments.String(); raw != "" {
			if err := json.Unmarshal([]byte(raw), &args); err != nil {
				return ProviderResult{}, fmt.Errorf("anthropic vertex tool input: %w", err)
			}
		}
		calls = append(calls, ToolCall{ID: acc.id, Name: acc.name, Args: args})
	}
	if text.Len() == 0 && len(calls) == 0 {
		return ProviderResult{}, fmt.Errorf("anthropic vertex stream: empty response")
	}
	return ProviderResult{Text: text.String(), ToolCalls: calls, Usage: usage}, nil
}

func normalizeAnthropicVertexUsage(next anthropicVertexUsage, current ProviderUsage) ProviderUsage {
	if next.InputTokens != 0 {
		current.InputTokens = next.InputTokens
	}
	if next.OutputTokens != 0 {
		current.OutputTokens = next.OutputTokens
	}
	if next.CacheReadTokens != 0 {
		current.CacheReadTokens = next.CacheReadTokens
	}
	if next.CacheCreationTokens != 0 {
		current.CacheCreationTokens = next.CacheCreationTokens
	}
	return current
}

var _ EventStreamingProvider = (*AnthropicVertexProvider)(nil)
