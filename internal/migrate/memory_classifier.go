// Package migrate — LLM-powered memory classification during import.
//
// This module provides optional LLM-based classification of memories during
// the OpenClaw import process. It can:
//   - Extract topics from memory content
//   - Classify memory types (user_preference, project_note, etc.)
//   - Extract searchable keywords
//   - Estimate confidence scores
//   - Generate brief summaries
package migrate

import (
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

// ClassifyConfig configures the memory classifier.
type ClassifyConfig struct {
	// Enabled toggles classification (default: false for speed).
	Enabled bool `json:"enabled"`

	// Provider is the LLM provider ("openai" or "anthropic").
	Provider string `json:"provider,omitempty"`

	// Model is the model to use (default: "gpt-4o-mini" for openai, "claude-3-haiku-20240307" for anthropic).
	Model string `json:"model,omitempty"`

	// APIKey is the API key for the provider.
	// If empty, reads from OPENAI_API_KEY or ANTHROPIC_API_KEY environment variable.
	APIKey string `json:"-"` // Don't serialize API keys

	// BatchSize is the number of memories to classify in one batch (default: 10).
	BatchSize int `json:"batch_size,omitempty"`

	// CustomPrompt is a custom classification prompt template.
	// Use {text} as placeholder for memory content.
	CustomPrompt string `json:"custom_prompt,omitempty"`

	// Timeout is the per-batch timeout (default: 30s).
	Timeout time.Duration `json:"timeout,omitempty"`
}

// DefaultClassifyConfig returns sensible defaults.
func DefaultClassifyConfig() ClassifyConfig {
	return ClassifyConfig{
		Enabled:   false,
		Provider:  "openai",
		Model:     "gpt-4o-mini",
		BatchSize: 10,
		Timeout:   30 * time.Second,
	}
}

// ClassifyResult holds the classification for a single memory.
type ClassifyResult struct {
	Topic      string   `json:"topic"`
	Type       string   `json:"type"`
	Keywords   []string `json:"keywords"`
	Confidence float64  `json:"confidence"`
	Summary    string   `json:"summary,omitempty"`
}

// MemoryClassifier classifies memories using an LLM.
type MemoryClassifier struct {
	cfg    ClassifyConfig
	client *http.Client
}

// NewMemoryClassifier creates a new memory classifier.
func NewMemoryClassifier(cfg ClassifyConfig) *MemoryClassifier {
	if cfg.Provider == "" {
		cfg.Provider = "openai"
	}
	if cfg.Model == "" {
		switch cfg.Provider {
		case "anthropic":
			cfg.Model = "claude-3-haiku-20240307"
		default:
			cfg.Model = "gpt-4o-mini"
		}
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.APIKey == "" {
		switch cfg.Provider {
		case "anthropic":
			cfg.APIKey = os.Getenv("ANTHROPIC_API_KEY")
		default:
			cfg.APIKey = os.Getenv("OPENAI_API_KEY")
		}
	}

	return &MemoryClassifier{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// ClassifyBatch classifies a batch of memory texts.
// Returns a map from index to classification result.
func (c *MemoryClassifier) ClassifyBatch(ctx context.Context, texts []string) (map[int]ClassifyResult, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Build the classification prompt
	prompt := c.buildBatchPrompt(texts)

	// Call the LLM
	response, err := c.callLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	// Parse the response
	return c.parseResponse(response, len(texts))
}

// Classify classifies a single memory text.
func (c *MemoryClassifier) Classify(ctx context.Context, text string) (*ClassifyResult, error) {
	results, err := c.ClassifyBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if result, ok := results[0]; ok {
		return &result, nil
	}
	return nil, fmt.Errorf("no classification result")
}

const defaultClassifyPrompt = `Classify the following memory entries. For each entry, provide:
- topic: A brief topic (2-5 words)
- type: One of: user_preference, project_note, conversation, task, reference, code, personal, other
- keywords: 3-5 relevant search keywords
- confidence: How reliable/current is this (0.0-1.0)
- summary: One sentence summary

Respond with a JSON array matching the input order.

Memory entries:
%s

Respond ONLY with valid JSON array:
[{"topic": "...", "type": "...", "keywords": [...], "confidence": 0.0, "summary": "..."}, ...]`

func (c *MemoryClassifier) buildBatchPrompt(texts []string) string {
	if c.cfg.CustomPrompt != "" {
		// For custom prompts with single text placeholder
		if len(texts) == 1 {
			return strings.ReplaceAll(c.cfg.CustomPrompt, "{text}", texts[0])
		}
	}

	// Build numbered list of memories
	var sb strings.Builder
	for i, text := range texts {
		// Truncate very long texts
		if len(text) > 2000 {
			text = text[:2000] + "..."
		}
		text = strings.TrimSpace(text)
		fmt.Fprintf(&sb, "[%d] %s\n\n", i, text)
	}

	return fmt.Sprintf(defaultClassifyPrompt, sb.String())
}

func (c *MemoryClassifier) callLLM(ctx context.Context, prompt string) (string, error) {
	switch c.cfg.Provider {
	case "anthropic":
		return c.callAnthropic(ctx, prompt)
	default:
		return c.callOpenAI(ctx, prompt)
	}
}

func (c *MemoryClassifier) callOpenAI(ctx context.Context, prompt string) (string, error) {
	if c.cfg.APIKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}

	reqBody := map[string]any{
		"model": c.cfg.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.1,
		"max_tokens":  4096,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("OpenAI API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}

	return result.Choices[0].Message.Content, nil
}

func (c *MemoryClassifier) callAnthropic(ctx context.Context, prompt string) (string, error) {
	if c.cfg.APIKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	reqBody := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": 4096,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Anthropic API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	for _, c := range result.Content {
		if c.Type == "text" {
			return c.Text, nil
		}
	}

	return "", fmt.Errorf("no text response from Anthropic")
}

func (c *MemoryClassifier) parseResponse(response string, expectedCount int) (map[int]ClassifyResult, error) {
	// Extract JSON array from response (handle markdown code blocks)
	response = strings.TrimSpace(response)
	if strings.HasPrefix(response, "```json") {
		response = strings.TrimPrefix(response, "```json")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	} else if strings.HasPrefix(response, "```") {
		response = strings.TrimPrefix(response, "```")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	}

	// Find the JSON array
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON array in response: %s", truncateForError(response))
	}
	jsonStr := response[start : end+1]

	var results []ClassifyResult
	if err := json.Unmarshal([]byte(jsonStr), &results); err != nil {
		return nil, fmt.Errorf("parse JSON: %w (%s)", err, truncateForError(jsonStr))
	}

	// Build map by index
	resultMap := make(map[int]ClassifyResult)
	for i, r := range results {
		if i < expectedCount {
			// Normalize type
			r.Type = normalizeMemoryType(r.Type)
			resultMap[i] = r
		}
	}

	return resultMap, nil
}

func truncateForError(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func normalizeMemoryType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "user_preference", "preference", "preferences":
		return "user_preference"
	case "project_note", "project", "note", "notes":
		return "project_note"
	case "conversation", "chat", "dialogue":
		return "conversation"
	case "task", "todo", "action":
		return "task"
	case "reference", "doc", "documentation":
		return "reference"
	case "code", "snippet", "programming":
		return "code"
	case "personal", "private":
		return "personal"
	default:
		return "other"
	}
}
