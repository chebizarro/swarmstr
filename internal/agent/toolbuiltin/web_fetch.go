package toolbuiltin

import (
	"context"
	"fmt"

	"swarmstr/internal/agent"
	"swarmstr/internal/browser"
)

const (
	defaultWebFetchMaxChars   = 50_000
	defaultWebFetchTimeoutSec = 30
)

// WebFetchOpts configures the web_fetch tool.
type WebFetchOpts struct {
	// AllowLocal disables the SSRF guard (useful for intranet or testing).
	AllowLocal bool
}

// WebFetchTool returns an agent.ToolFunc for the "web_fetch" tool.
//
// Tool parameters:
//   - url (string, required) – HTTP or HTTPS URL to fetch
//   - max_chars (int, default 50000) – truncation limit in Unicode code points
//   - timeout_seconds (int, default 30) – request timeout
//   - allow_local (bool) – per-call override of the SSRF guard
// WebFetchDef is the ToolDefinition for web_fetch (native function-calling schema).
var WebFetchDef = agent.ToolDefinition{
	Name:        "web_fetch",
	Description: "Fetch the text content of a web page or URL. Returns the visible text (HTML stripped). Use for reading documentation, articles, GitHub READMEs, or any publicly accessible web content. Respects SSRF guards.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"url": {
				Type:        "string",
				Description: "The URL to fetch, e.g. \"https://example.com/page\"",
			},
		},
		Required: []string{"url"},
	},
}

func WebFetchTool(opts WebFetchOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		rawURL := agent.ArgString(args, "url")
		if rawURL == "" {
			return "", fmt.Errorf("web_fetch: url is required")
		}

		maxChars := agent.ArgInt(args, "max_chars", defaultWebFetchMaxChars)
		if maxChars <= 0 {
			maxChars = defaultWebFetchMaxChars
		}
		timeoutSec := agent.ArgInt(args, "timeout_seconds", defaultWebFetchTimeoutSec)
		if timeoutSec <= 0 {
			timeoutSec = defaultWebFetchTimeoutSec
		}

		allowLocal := opts.AllowLocal
		if v, ok := args["allow_local"].(bool); ok && v {
			allowLocal = true
		}

		if err := ValidateFetchURL(rawURL, allowLocal); err != nil {
			return "", fmt.Errorf("web_fetch: %w", err)
		}

		resp, err := browser.Fetch(ctx, browser.Request{
			URL:       rawURL,
			TimeoutMS: timeoutSec * 1000,
		})
		if err != nil {
			return "", fmt.Errorf("web_fetch: %w", err)
		}

		var content string
		if resp.Text != "" {
			content = resp.Text
		} else {
			content = resp.Body
		}
		return Truncate(content, maxChars), nil
	}
}
