package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// StreamingProvider extends Provider with incremental text delivery.
//
// Stream generates a response for turn, calling onChunk for each text token (or
// small token group) as it arrives from the LLM.  When generation completes the
// returned ProviderResult holds the full accumulated Text and any ToolCalls that
// were requested by the model.  Callers should not rely on Text in ProviderResult
// from onChunk — use only onChunk for incremental delivery.
type StreamingProvider interface {
	Provider
	Stream(ctx context.Context, turn Turn, onChunk func(text string)) (ProviderResult, error)
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
		contextText = truncateUTF8ByBytes(contextText, maxContextBytes)
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

func truncateUTF8ByBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	if cut == 0 {
		return ""
	}
	return s[:cut]
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

// NewProviderForModel constructs a Provider for the given model identifier.
//   - "" / "echo"                → EchoProvider (no external dependency)
//   - "http" / "http-default"    → HTTPProvider configured from env vars
//
// This is used by BuildRuntimeForModel and the agents.create RPC to spin up
// per-agent runtimes with model-specific providers.
// AnthropicProvider calls the Anthropic Messages API (POST /v1/messages).
// Set ANTHROPIC_API_KEY in the environment or use ProviderOverride.APIKey.
type AnthropicProvider struct {
	APIKey       string
	Model        string
	SystemPrompt string
	Client       *http.Client
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

// anthropicMessage supports both plain-text content (string) and multi-modal
// content ([]any with image + text blocks). Content is typed as any so that
// json.Marshal produces the correct shape for each case.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []map[string]any for multi-modal
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// buildAnthropicContent constructs the message content block for a user turn.
// When images are present the content is a []map[string]any with image blocks
// followed by the text block (Anthropic multi-modal format).
func buildAnthropicContent(text string, images []ImageRef) any {
	if len(images) == 0 {
		return text
	}
	blocks := make([]map[string]any, 0, len(images)+1)
	for _, img := range images {
		if img.Base64 != "" {
			mt := img.MimeType
			if mt == "" {
				mt = "image/jpeg"
			}
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": mt,
					"data":       img.Base64,
				},
			})
		} else if img.URL != "" {
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type": "url",
					"url":  img.URL,
				},
			})
		}
	}
	blocks = append(blocks, map[string]any{"type": "text", "text": text})
	return blocks
}

func (p *AnthropicProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	}
	if apiKey == "" {
		// Also check the OAuth token env var (OpenClaw compat).
		apiKey = strings.TrimSpace(os.Getenv("ANTHROPIC_OAUTH_TOKEN"))
	}
	if apiKey == "" {
		return ProviderResult{}, fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}

	// Detect OAuth credentials (OpenClaw format or sk-ant-oat prefix).
	if access, refresh, isOAuth := ParseAnthropicOAuthCredential(apiKey); isOAuth {
		return p.generateWithOAuth(ctx, turn, model, access, refresh)
	}

	userText := strings.TrimSpace(turn.UserText)
	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 4096,
		Messages:  []anthropicMessage{{Role: "user", Content: buildAnthropicContent(userText, turn.Images)}},
	}
	sys := strings.TrimSpace(turn.Context)
	if sys == "" {
		sys = strings.TrimSpace(p.SystemPrompt)
	} else if p.SystemPrompt != "" {
		sys = strings.TrimSpace(p.SystemPrompt) + "\n\n" + sys
	}
	if sys != "" {
		reqBody.System = sys
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return ProviderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{}, err
	}
	defer resp.Body.Close()

	var out anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ProviderResult{}, fmt.Errorf("anthropic decode: %w", err)
	}
	if out.Error != nil {
		return ProviderResult{}, fmt.Errorf("anthropic error %s: %s", out.Error.Type, out.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("anthropic returned %s", resp.Status)
	}
	for _, c := range out.Content {
		if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
			return ProviderResult{Text: c.Text}, nil
		}
	}
	return ProviderResult{}, fmt.Errorf("anthropic returned no text content")
}

// generateWithOAuth performs an Anthropic API call using OAuth Bearer token auth.
// On 401, it attempts to refresh the token once and retries.
func (p *AnthropicProvider) generateWithOAuth(ctx context.Context, turn Turn, model, access, refresh string) (ProviderResult, error) {
	result, err := p.doAnthropicOAuthRequest(ctx, turn, model, access)
	if err == nil {
		return result, nil
	}
	// On auth failure, try refreshing if we have a refresh token.
	if refresh == "" || !isAnthropicAuthError(err) {
		return ProviderResult{}, fmt.Errorf("anthropic oauth: %w", err)
	}
	newAccess, newRefresh, refreshErr := AnthropicOAuthRefresh(ctx, refresh)
	if refreshErr != nil {
		return ProviderResult{}, fmt.Errorf("anthropic oauth: request failed (%v); token refresh also failed: %w", err, refreshErr)
	}
	// Update in-process cache with new tokens.
	oauthTokenCache.mu.Lock()
	oauthTokenCache.access = newAccess
	oauthTokenCache.refresh = newRefresh
	oauthTokenCache.expiry = time.Now().Add(55 * time.Minute)
	oauthTokenCache.mu.Unlock()

	return p.doAnthropicOAuthRequest(ctx, turn, model, newAccess)
}

// isAnthropicAuthError reports whether err indicates an authentication failure.
func isAnthropicAuthError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "401") || strings.Contains(s, "authentication_error") || strings.Contains(s, "permission_error")
}

// doAnthropicOAuthRequest performs a single Anthropic API call with Bearer auth.
func (p *AnthropicProvider) doAnthropicOAuthRequest(ctx context.Context, turn Turn, model, accessToken string) (ProviderResult, error) {
	userText := strings.TrimSpace(turn.UserText)
	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 4096,
		Messages:  []anthropicMessage{{Role: "user", Content: buildAnthropicContent(userText, turn.Images)}},
	}
	sys := strings.TrimSpace(turn.Context)
	if sys == "" {
		sys = strings.TrimSpace(p.SystemPrompt)
	} else if p.SystemPrompt != "" {
		sys = strings.TrimSpace(p.SystemPrompt) + "\n\n" + sys
	}
	if sys != "" {
		reqBody.System = sys
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return ProviderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	applyAnthropicOAuthHeaders(req, accessToken)

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{}, err
	}
	defer resp.Body.Close()

	var out anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ProviderResult{}, fmt.Errorf("anthropic oauth decode: %w", err)
	}
	if out.Error != nil {
		return ProviderResult{}, fmt.Errorf("anthropic oauth error %s: %s", out.Error.Type, out.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("anthropic oauth returned %s", resp.Status)
	}
	for _, c := range out.Content {
		if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
			return ProviderResult{Text: c.Text}, nil
		}
	}
	return ProviderResult{}, fmt.Errorf("anthropic oauth returned no text content")
}

// OpenAIChatProvider calls the OpenAI Chat Completions API (POST /v1/chat/completions).
// Also compatible with Ollama and other OpenAI-compatible endpoints.
// Set OPENAI_API_KEY (or ANTHROPIC_API_KEY is not used for this provider).
type OpenAIChatProvider struct {
	BaseURL string // defaults to https://api.openai.com
	APIKey  string
	Model   string
	Client  *http.Client
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
}

// openAIMessage supports both plain-text content (string) and multi-modal
// content ([]map[string]any). Content is typed as any so json.Marshal produces
// the correct wire format for each case.
type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []map[string]any for vision
}

// buildOpenAIContent constructs the message content for a user turn.
// When images are present the content is a []map[string]any with image_url blocks
// followed by the text block (OpenAI multi-modal format).
func buildOpenAIContent(text string, images []ImageRef) any {
	if len(images) == 0 {
		return text
	}
	blocks := make([]map[string]any, 0, len(images)+1)
	for _, img := range images {
		if img.Base64 != "" {
			mt := img.MimeType
			if mt == "" {
				mt = "image/jpeg"
			}
			// OpenAI requires data URI format: data:<mime>;base64,<data>
			dataURI := "data:" + mt + ";base64," + img.Base64
			blocks = append(blocks, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": dataURI},
			})
		} else if img.URL != "" {
			blocks = append(blocks, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": img.URL},
			})
		}
	}
	blocks = append(blocks, map[string]any{"type": "text", "text": text})
	return blocks
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (p *OpenAIChatProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "gpt-4o"
	}
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}

	baseURL := strings.TrimSpace(p.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	messages := make([]openAIMessage, 0, 2)
	if context := strings.TrimSpace(turn.Context); context != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: context})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: buildOpenAIContent(strings.TrimSpace(turn.UserText), turn.Images)})

	reqBody := openAIRequest{Model: model, Messages: messages}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return ProviderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{}, err
	}
	defer resp.Body.Close()

	var out openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ProviderResult{}, fmt.Errorf("openai decode: %w", err)
	}
	if out.Error != nil {
		return ProviderResult{}, fmt.Errorf("openai error %s: %s", out.Error.Type, out.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("openai returned %s", resp.Status)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return ProviderResult{}, fmt.Errorf("openai returned no content")
	}
	return ProviderResult{Text: out.Choices[0].Message.Content}, nil
}

// Stream implements StreamingProvider for OpenAIChatProvider.
// It uses Server-Sent Events (SSE) to deliver text tokens incrementally via
// onChunk and accumulates any tool_call deltas for return in ProviderResult.
func (p *OpenAIChatProvider) Stream(ctx context.Context, turn Turn, onChunk func(text string)) (ProviderResult, error) {
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "gpt-4o"
	}
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	baseURL := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	messages := make([]openAIMessage, 0, 2)
	if ctxText := strings.TrimSpace(turn.Context); ctxText != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: ctxText})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: buildOpenAIContent(strings.TrimSpace(turn.UserText), turn.Images)})

	// stream:true makes the API emit SSE data lines.
	reqBody := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return ProviderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := p.Client
	if client == nil {
		// No overall timeout on the client; rely on ctx cancellation.
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return ProviderResult{}, fmt.Errorf("openai stream: HTTP %d: %s", resp.StatusCode, raw)
	}

	// toolCallAccumulators accumulates tool_call deltas by index.
	type toolCallAcc struct {
		ID        string
		Name      string
		Arguments strings.Builder
	}
	toolAcc := map[int]*toolCallAcc{}

	var textBuf strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	// Streaming chunks (especially tool_call argument deltas) can exceed the
	// scanner's default token size (~64 KiB).
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id,omitempty"`
						Function struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						} `json:"function"`
					} `json:"tool_calls,omitempty"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			textBuf.WriteString(delta.Content)
			if onChunk != nil {
				onChunk(delta.Content)
			}
		}
		for _, tc := range delta.ToolCalls {
			acc, ok := toolAcc[tc.Index]
			if !ok {
				acc = &toolCallAcc{}
				toolAcc[tc.Index] = acc
			}
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Function.Name != "" {
				acc.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.Arguments.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ProviderResult{}, fmt.Errorf("openai stream scanner: %w", err)
	}

	// Assemble tool calls from accumulators.
	var toolCalls []ToolCall
	for idx := 0; idx < len(toolAcc); idx++ {
		acc, ok := toolAcc[idx]
		if !ok {
			continue
		}
		var args map[string]any
		if argStr := acc.Arguments.String(); argStr != "" {
			_ = json.Unmarshal([]byte(argStr), &args)
		}
		toolCalls = append(toolCalls, ToolCall{
			Name: acc.Name,
			Args: args,
		})
	}

	return ProviderResult{
		Text:      textBuf.String(),
		ToolCalls: toolCalls,
	}, nil
}

// ─── Google Gemini provider ───────────────────────────────────────────────────

// GoogleGeminiProvider calls the Google AI Gemini API.
// API key is read from GEMINI_API_KEY (or GOOGLE_API_KEY / GOOGLE_GENERATIVE_AI_API_KEY).
// Default model: "gemini-2.0-flash".
type GoogleGeminiProvider struct {
	Model  string
	APIKey string
	Client *http.Client
}

type geminiContent struct {
	Role  string      `json:"role"`
	Parts []geminiPart `json:"parts"`
}

// geminiPart supports text, inline_data (base64 image), and file_data (URL image).
// Fields are omitempty so the JSON output only includes the relevant shape.
type geminiPart struct {
	Text       string              `json:"text,omitempty"`
	InlineData *geminiInlineData   `json:"inline_data,omitempty"`
	FileData   *geminiFileData     `json:"file_data,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"` // base64
}

type geminiFileData struct {
	MimeType string `json:"mime_type,omitempty"`
	FileURI  string `json:"file_uri"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  map[string]any  `json:"generationConfig,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// buildGeminiParts constructs the parts slice for a user turn.
// When images are present they appear before the text part (Gemini multi-modal format).
func buildGeminiParts(text string, images []ImageRef) []geminiPart {
	if len(images) == 0 {
		return []geminiPart{{Text: text}}
	}
	parts := make([]geminiPart, 0, len(images)+1)
	for _, img := range images {
		if img.Base64 != "" {
			mt := img.MimeType
			if mt == "" {
				mt = "image/jpeg"
			}
			parts = append(parts, geminiPart{InlineData: &geminiInlineData{MimeType: mt, Data: img.Base64}})
		} else if img.URL != "" {
			parts = append(parts, geminiPart{FileData: &geminiFileData{MimeType: img.MimeType, FileURI: img.URL}})
		}
	}
	parts = append(parts, geminiPart{Text: text})
	return parts
}

func (p *GoogleGeminiProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		for _, envKey := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"} {
			if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
				apiKey = v
				break
			}
		}
	}
	if apiKey == "" {
		return ProviderResult{}, fmt.Errorf("Gemini API key not configured (set GEMINI_API_KEY)")
	}
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "gemini-2.0-flash"
	}

	req := geminiRequest{
		Contents: []geminiContent{
			{Role: "user", Parts: buildGeminiParts(strings.TrimSpace(turn.UserText), turn.Images)},
		},
	}
	if turn.Context != "" {
		req.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: turn.Context}},
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return ProviderResult{}, err
	}

	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return ProviderResult{}, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	var out geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ProviderResult{}, fmt.Errorf("gemini decode: %w", err)
	}
	if out.Error != nil {
		return ProviderResult{}, fmt.Errorf("gemini API error %d: %s", out.Error.Code, out.Error.Message)
	}
	if len(out.Candidates) == 0 || len(out.Candidates[0].Content.Parts) == 0 {
		return ProviderResult{}, fmt.Errorf("gemini: empty response")
	}
	return ProviderResult{Text: out.Candidates[0].Content.Parts[0].Text}, nil
}

// ─── Provider table for OpenAI-compatible endpoints ───────────────────────────

// openAICompatProviders maps model prefixes / aliases to their base URL and env key name.
// All of these use the OpenAI Chat Completions API format.
var openAICompatProviders = []struct {
	prefix   string // lowercase prefix (or exact alias) to match
	alias    string // exact alias (e.g. "groq", "mistral")
	base     string // default base URL
	envKey   string // primary env var name for the API key
	baseEnv  string // optional env var that overrides base URL (for local servers)
}{
	{prefix: "grok-", alias: "xai", base: "https://api.x.ai/v1", envKey: "XAI_API_KEY"},
	{prefix: "", alias: "groq", base: "https://api.groq.com/openai/v1", envKey: "GROQ_API_KEY"},
	{prefix: "groq/", alias: "", base: "https://api.groq.com/openai/v1", envKey: "GROQ_API_KEY"},
	{prefix: "mistral-", alias: "mistral", base: "https://api.mistral.ai/v1", envKey: "MISTRAL_API_KEY"},
	{prefix: "", alias: "together", base: "https://api.together.xyz/v1", envKey: "TOGETHER_API_KEY"},
	{prefix: "together/", alias: "", base: "https://api.together.xyz/v1", envKey: "TOGETHER_API_KEY"},
	{prefix: "", alias: "openrouter", base: "https://openrouter.ai/api/v1", envKey: "OPENROUTER_API_KEY"},
	{prefix: "openrouter/", alias: "", base: "https://openrouter.ai/api/v1", envKey: "OPENROUTER_API_KEY"},
	// Ollama: local inference server with OpenAI-compatible API.
	// Base URL defaults to http://localhost:11434/v1; override with OLLAMA_BASE_URL.
	// No API key required for local Ollama (OLLAMA_API_KEY optional for remote).
	{prefix: "ollama/", alias: "ollama", base: "http://localhost:11434/v1", envKey: "OLLAMA_API_KEY", baseEnv: "OLLAMA_BASE_URL"},
	// LM Studio: OpenAI-compatible local server, typically on :1234.
	{prefix: "lmstudio/", alias: "lmstudio", base: "http://localhost:1234/v1", envKey: "", baseEnv: "LMSTUDIO_BASE_URL"},
	// Fireworks AI: fast inference for open-source models.
	{prefix: "fireworks/", alias: "fireworks", base: "https://api.fireworks.ai/inference/v1", envKey: "FIREWORKS_API_KEY"},
	// DeepInfra: affordable hosted inference.
	{prefix: "deepinfra/", alias: "deepinfra", base: "https://api.deepinfra.com/v1/openai", envKey: "DEEPINFRA_API_KEY"},
	// Perplexity: search-augmented chat.
	{prefix: "pplx-", alias: "perplexity", base: "https://api.perplexity.ai", envKey: "PERPLEXITY_API_KEY"},
}

// resolveOpenAICompat checks whether a model string matches one of the known
// OpenAI-compatible provider aliases/prefixes.  On match it returns the base URL
// and env key name; otherwise returns empty strings.
func resolveOpenAICompat(norm string) (baseURL, envKey string) {
	for _, p := range openAICompatProviders {
		matched := (p.alias != "" && norm == p.alias) ||
			(p.prefix != "" && strings.HasPrefix(norm, p.prefix))
		if !matched {
			continue
		}
		base := p.base
		// Allow base URL override via environment variable (e.g. OLLAMA_BASE_URL).
		if p.baseEnv != "" {
			if override := strings.TrimRight(strings.TrimSpace(os.Getenv(p.baseEnv)), "/"); override != "" {
				base = override
			}
		}
		return base, p.envKey
	}
	return "", ""
}

// NewProviderForModel constructs a Provider for the given model identifier.
//
//   - "" / "echo"                         → EchoProvider
//   - "http" / "http-default"             → HTTPProvider (SWARMSTR_AGENT_HTTP_URL)
//   - "anthropic" / "claude-*"            → AnthropicProvider (ANTHROPIC_API_KEY)
//   - "openai" / "gpt-*" / "o1-*" …      → OpenAIChatProvider (OPENAI_API_KEY)
//   - "gemini" / "gemini-*"              → GoogleGeminiProvider (GEMINI_API_KEY)
//   - "grok-*" / "xai"                   → OpenAIChatProvider → api.x.ai (XAI_API_KEY)
//   - "groq" / "groq/*"                  → OpenAIChatProvider → api.groq.com (GROQ_API_KEY)
//   - "mistral" / "mistral-*"            → OpenAIChatProvider → api.mistral.ai (MISTRAL_API_KEY)
//   - "together" / "together/*"          → OpenAIChatProvider → api.together.xyz (TOGETHER_API_KEY)
//   - "openrouter" / "openrouter/*"      → OpenAIChatProvider → openrouter.ai (OPENROUTER_API_KEY)
func NewProviderForModel(model string) (Provider, error) {
	norm := strings.ToLower(strings.TrimSpace(model))
	switch {
	case norm == "" || norm == "echo":
		return EchoProvider{}, nil
	case norm == "http" || norm == "http-default":
		url := strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_URL"))
		if url == "" {
			return nil, fmt.Errorf("SWARMSTR_AGENT_HTTP_URL is required for http model")
		}
		return &HTTPProvider{URL: url, APIKey: strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_API_KEY"))}, nil
	case norm == "github-copilot":
		// GitHub Copilot: OpenAI-compatible API with OAuth device-flow auth.
		tok, _, _ := FetchOAuthToken(context.Background(), "github-copilot")
		return &OpenAIChatProvider{
			BaseURL: "https://api.githubcopilot.com",
			APIKey:  tok,
			Model:   "gpt-4o", // default Copilot model; override via ProviderOverride.Model
		}, nil
	case norm == "anthropic" || strings.HasPrefix(norm, "claude-"):
		return &AnthropicProvider{Model: strings.TrimSpace(model)}, nil
	case norm == "openai" || strings.HasPrefix(norm, "gpt-") || strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-"):
		return &OpenAIChatProvider{Model: strings.TrimSpace(model)}, nil
	case norm == "gemini" || strings.HasPrefix(norm, "gemini-"):
		return &GoogleGeminiProvider{Model: strings.TrimSpace(model)}, nil
	case norm == "cohere" || strings.HasPrefix(norm, "command-"):
		return &CohereProvider{Model: strings.TrimSpace(model)}, nil
	}
	// OpenAI-compatible providers by prefix/alias.
	if baseURL, envKey := resolveOpenAICompat(norm); baseURL != "" {
		apiKey := ""
		if envKey != "" {
			apiKey = strings.TrimSpace(os.Getenv(envKey))
		}
		return &OpenAIChatProvider{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   strings.TrimSpace(model),
		}, nil
	}
	return nil, fmt.Errorf("unsupported model %q — try: echo, claude-*, gpt-*, gemini-*, grok-*, groq/*, mistral-*, together/*, openrouter/*, cohere/command-*, ollama/*, or http", model)
}

// ─── Cohere provider ──────────────────────────────────────────────────────────

// CohereProvider implements Provider using the Cohere Chat API v2.
// Docs: https://docs.cohere.com/reference/chat
// Reads COHERE_API_KEY from the environment at call time.
type CohereProvider struct {
	Model  string
	APIKey string
}

type cohereMessage struct {
	Role    string `json:"role"`    // "user" | "assistant"
	Content string `json:"content"` // plain text
}

type cohereRequest struct {
	Model    string          `json:"model"`
	Messages []cohereMessage `json:"messages"`
	Preamble string          `json:"preamble,omitempty"` // system prompt equivalent
	Stream   bool            `json:"stream"`
}

type cohereResponse struct {
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *CohereProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	apiKey := strings.TrimSpace(p.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("COHERE_API_KEY"))
	}
	if apiKey == "" {
		return ProviderResult{}, fmt.Errorf("COHERE_API_KEY is not set")
	}
	model := strings.TrimSpace(p.Model)
	if model == "" || model == "cohere" {
		model = "command-r-plus"
	}

	req := cohereRequest{
		Model:  model,
		Stream: false,
		Messages: []cohereMessage{
			{Role: "user", Content: strings.TrimSpace(turn.UserText)},
		},
	}
	if turn.Context != "" {
		req.Preamble = turn.Context
	}

	body, err := json.Marshal(req)
	if err != nil {
		return ProviderResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.cohere.com/v2/chat", bytes.NewReader(body))
	if err != nil {
		return ProviderResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return ProviderResult{}, fmt.Errorf("call Cohere API: %w", err)
	}
	defer resp.Body.Close()

	var cohereResp cohereResponse
	if err := json.NewDecoder(resp.Body).Decode(&cohereResp); err != nil {
		return ProviderResult{}, fmt.Errorf("decode Cohere response: %w", err)
	}
	if cohereResp.Error != nil {
		return ProviderResult{}, fmt.Errorf("Cohere API error: %s", cohereResp.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return ProviderResult{}, fmt.Errorf("Cohere API HTTP %d", resp.StatusCode)
	}

	var sb strings.Builder
	for _, block := range cohereResp.Message.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return ProviderResult{}, fmt.Errorf("Cohere API returned empty response")
	}
	return ProviderResult{Text: text}, nil
}

// BuildRuntimeForModel constructs a Runtime for the given model identifier.
// tools may be nil (tool calls will error gracefully).
func BuildRuntimeForModel(model string, tools ToolExecutor) (Runtime, error) {
	p, err := NewProviderForModel(model)
	if err != nil {
		return nil, err
	}
	return NewProviderRuntime(p, tools)
}

// ProviderOverride carries explicit credentials from the providers config section.
// Either BaseURL or APIKey (or both) can override the env-based defaults.
type ProviderOverride struct {
	BaseURL      string
	APIKey       string
	Model        string
	SystemPrompt string // injected as system context for every turn
}

// BuildRuntimeWithOverride constructs a Runtime using explicit provider credentials
// from the providers[] config section, falling back to env vars when fields are empty.
// This enables OpenClaw-compatible provider configuration via config file.
func BuildRuntimeWithOverride(model string, override ProviderOverride, tools ToolExecutor) (Runtime, error) {
	// If no override is specified, delegate to the standard env-based builder.
	if override.BaseURL == "" && override.APIKey == "" {
		return BuildRuntimeForModel(model, tools)
	}
	// GitHub Copilot OAuth: detect via base URL or model prefix.
	normModel := strings.ToLower(strings.TrimSpace(override.Model))
	if normModel == "" {
		normModel = strings.ToLower(strings.TrimSpace(model))
	}
	isGHCopilot := strings.Contains(strings.ToLower(override.BaseURL), "githubcopilot.com") ||
		normModel == "github-copilot"
	if isGHCopilot && strings.TrimSpace(override.APIKey) == "" {
		if tok, found, tokErr := FetchOAuthToken(context.Background(), "github-copilot"); found && tokErr == nil {
			override.APIKey = tok
		}
		if override.BaseURL == "" {
			override.BaseURL = "https://api.githubcopilot.com"
		}
	}
	// Resolve effective model: prefer override.Model, then the passed model arg.
	effectiveModel := strings.TrimSpace(override.Model)
	if effectiveModel == "" {
		effectiveModel = strings.TrimSpace(model)
	}
	norm := strings.ToLower(effectiveModel)

	// Anthropic (claude-* models).
	if norm == "anthropic" || strings.HasPrefix(norm, "claude-") {
		apiKey := strings.TrimSpace(override.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
		}
		return NewProviderRuntime(&AnthropicProvider{Model: effectiveModel, APIKey: apiKey, SystemPrompt: override.SystemPrompt}, tools)
	}

	// Google Gemini.
	if norm == "gemini" || strings.HasPrefix(norm, "gemini-") {
		apiKey := strings.TrimSpace(override.APIKey)
		if apiKey == "" {
			for _, k := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"} {
				if v := strings.TrimSpace(os.Getenv(k)); v != "" {
					apiKey = v
					break
				}
			}
		}
		return NewProviderRuntime(&GoogleGeminiProvider{Model: effectiveModel, APIKey: apiKey}, tools)
	}

	// OpenAI and OpenAI-compatible providers.
	// If an explicit base URL is provided in the override, use it; otherwise
	// look up the default base URL from the model prefix table.
	overrideBaseURL := strings.TrimSpace(override.BaseURL)
	overrideAPIKey := strings.TrimSpace(override.APIKey)

	isOpenAIClass := norm == "openai" || strings.HasPrefix(norm, "gpt-") ||
		strings.HasPrefix(norm, "o1-") || strings.HasPrefix(norm, "o3-") || strings.HasPrefix(norm, "o4-")

	compatBase, compatEnvKey := resolveOpenAICompat(norm)
	if isOpenAIClass || overrideBaseURL != "" || compatBase != "" {
		baseURL := overrideBaseURL
		apiKey := overrideAPIKey
		if baseURL == "" {
			baseURL = compatBase
		}
		if apiKey == "" {
			envKey := "OPENAI_API_KEY"
			if compatEnvKey != "" {
				envKey = compatEnvKey
			}
			apiKey = strings.TrimSpace(os.Getenv(envKey))
		}
		return NewProviderRuntime(&OpenAIChatProvider{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   effectiveModel,
		}, tools)
	}

	// Generic HTTP-compatible endpoint (Ollama, custom servers, etc.)
	baseURL := overrideBaseURL
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_URL"))
	}
	if baseURL == "" {
		return nil, fmt.Errorf("provider base_url is required (set in providers config or SWARMSTR_AGENT_HTTP_URL)")
	}
	apiKey := overrideAPIKey
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("SWARMSTR_AGENT_HTTP_API_KEY"))
	}
	return NewProviderRuntime(&HTTPProvider{URL: baseURL, APIKey: apiKey}, tools)
}
