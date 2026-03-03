package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type Provider interface {
	Generate(context.Context, Turn) (ProviderResult, error)
}

type ProviderResult struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type EchoProvider struct{}

func (EchoProvider) Generate(_ context.Context, turn Turn) (ProviderResult, error) {
	return ProviderResult{Text: "ack: " + turn.UserText}, nil
}

type HTTPProvider struct {
	URL    string
	APIKey string
	Client *http.Client
}

type httpRequest struct {
	SessionID string `json:"session_id"`
	Prompt    string `json:"prompt"`
	Context   string `json:"context,omitempty"`
}

type httpResponse struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

func (p *HTTPProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	contextText := turn.Context
	const maxContextBytes = 16 * 1024
	if len(contextText) > maxContextBytes {
		contextText = contextText[:maxContextBytes]
	}
	body, err := json.Marshal(httpRequest{SessionID: turn.SessionID, Prompt: turn.UserText, Context: contextText})
	if err != nil {
		return ProviderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.URL, bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("provider returned %s", resp.Status)
	}
	var out httpResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ProviderResult{}, err
	}
	if strings.TrimSpace(out.Text) == "" && len(out.ToolCalls) == 0 {
		return ProviderResult{}, fmt.Errorf("provider returned empty response")
	}
	return ProviderResult{Text: out.Text, ToolCalls: out.ToolCalls}, nil
}

func NewProviderFromEnv() (Provider, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_PROVIDER")))
	switch mode {
	case "", "echo":
		return EchoProvider{}, nil
	case "http":
		url := strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_URL"))
		if url == "" {
			return nil, fmt.Errorf("SWARMSTR_AGENT_HTTP_URL is required when SWARMSTR_AGENT_PROVIDER=http")
		}
		return &HTTPProvider{URL: url, APIKey: strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_API_KEY"))}, nil
	default:
		return nil, fmt.Errorf("unknown SWARMSTR_AGENT_PROVIDER %q", mode)
	}
}
