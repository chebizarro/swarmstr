package agent

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLMStudioLive_SmallPrompt verifies a live LM Studio server can handle a
// simple prompt through the full provider→agentic loop stack.
//
// Skipped when LM Studio is not running (connection refused on localhost:1234).
// Run explicitly: go test -run TestLMStudioLive -v -timeout 120s
func TestLMStudioLive_SmallPrompt(t *testing.T) {
	if os.Getenv("LMSTUDIO_LIVE_TEST") == "" {
		// Quick check: is LM Studio reachable?
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://localhost:1234/v1/models")
		if err != nil {
			t.Skipf("LM Studio not reachable: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Skipf("LM Studio returned %d", resp.StatusCode)
		}
	}

	provider := &OpenAIChatProvider{
		BaseURL: "http://localhost:1234/v1",
		Model:   "openai/gpt-oss-20b",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	turn := Turn{
		UserText:            "What is 2+2? Reply with just the number.",
		ContextWindowTokens: 65536,
	}

	t.Log("Sending prompt to LM Studio...")
	start := time.Now()
	result, err := provider.Generate(ctx, turn)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Generate failed after %v: %v", elapsed, err)
	}

	t.Logf("Response in %v: %q", elapsed, result.Text)
	t.Logf("Usage: input=%d output=%d",
		result.Usage.InputTokens, result.Usage.OutputTokens)

	if strings.TrimSpace(result.Text) == "" {
		t.Error("empty response from LM Studio")
	}
}

// TestLMStudioLive_WithTools verifies tool definitions pass through correctly
// to a local LM Studio model.
func TestLMStudioLive_WithTools(t *testing.T) {
	if os.Getenv("LMSTUDIO_LIVE_TEST") == "" {
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get("http://localhost:1234/v1/models")
		if err != nil {
			t.Skipf("LM Studio not reachable: %v", err)
		}
		resp.Body.Close()
	}

	provider := &OpenAIChatProvider{
		BaseURL: "http://localhost:1234/v1",
		Model:   "openai/gpt-oss-20b",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	turn := Turn{
		UserText:            "List the files in the current directory.",
		ContextWindowTokens: 65536,
		Tools: []ToolDefinition{
			{
				Name:        "file_tree",
				Description: "Generate a recursive directory tree.",
				Parameters: ToolParameters{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"path":      {Type: "string", Description: "Root directory."},
						"max_depth": {Type: "integer", Description: "Max recursion depth (1-10)."},
					},
				},
			},
		},
	}

	t.Log("Sending prompt with 1 tool to LM Studio...")
	start := time.Now()
	result, err := provider.Generate(ctx, turn)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Generate with tools failed after %v: %v", elapsed, err)
	}

	t.Logf("Response in %v: text=%q toolCalls=%d", elapsed, truncate(result.Text, 200), len(result.ToolCalls))
	t.Logf("Usage: input=%d output=%d",
		result.Usage.InputTokens, result.Usage.OutputTokens)

	if result.Text == "" && len(result.ToolCalls) == 0 {
		t.Error("empty response and no tool calls from LM Studio")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("... (%d more chars)", len(s)-n)
}
