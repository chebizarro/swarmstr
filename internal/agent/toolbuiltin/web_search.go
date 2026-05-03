package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"metiq/internal/agent"
	searchproviders "metiq/internal/search"
)

const (
	defaultBraveEndpoint  = "https://api.search.brave.com/res/v1/web/search"
	defaultSerperEndpoint = "https://google.serper.dev/search"
	defaultSearchCount    = 5
	maxSearchCount        = 10
	searchTimeoutSec      = 15
)

// SearchResult is a single web search result.
type SearchResult = searchproviders.SearchResult

// WebSearchConfig configures the web_search tool.
type WebSearchConfig struct {
	// DefaultProvider selects "brave" or "serper".
	// When empty, the tool auto-detects from available API keys.
	DefaultProvider string
	// BraveAPIKey overrides the BRAVE_SEARCH_API_KEY env var.
	BraveAPIKey string
	// SerperAPIKey overrides the SERPER_API_KEY env var.
	SerperAPIKey string
	// BraveEndpoint overrides the Brave Search API endpoint (for testing).
	BraveEndpoint string
	// SerperEndpoint overrides the Serper API endpoint (for testing).
	SerperEndpoint string
}

func (c WebSearchConfig) resolveBraveKey() string {
	if c.BraveAPIKey != "" {
		return c.BraveAPIKey
	}
	return os.Getenv("BRAVE_SEARCH_API_KEY")
}

func (c WebSearchConfig) resolveSerperKey() string {
	if c.SerperAPIKey != "" {
		return c.SerperAPIKey
	}
	return os.Getenv("SERPER_API_KEY")
}

func (c WebSearchConfig) detectProvider() string {
	if c.resolveBraveKey() != "" {
		return "brave"
	}
	if c.resolveSerperKey() != "" {
		return "serper"
	}
	return ""
}

func (c WebSearchConfig) braveURL() string {
	if c.BraveEndpoint != "" {
		return c.BraveEndpoint
	}
	return defaultBraveEndpoint
}

func (c WebSearchConfig) serperURL() string {
	if c.SerperEndpoint != "" {
		return c.SerperEndpoint
	}
	return defaultSerperEndpoint
}

// WebSearchTool returns an agent.ToolFunc for the "web_search" tool.
//
// Tool parameters:
//   - query (string, required) – search query
//   - provider ("brave"|"serper", optional) – defaults to auto-detect
//   - count (int, default 5, max 10) – number of results to return
//
// WebSearchDef is the ToolDefinition for web_search (native function-calling schema).
var WebSearchDef = agent.ToolDefinition{
	Name:        "web_search",
	Description: "Search the web using Brave Search or Serper. Returns a list of results with titles, URLs, and snippets. Use to find current information, recent events, or when you need to discover resources you don't have a URL for.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"query": {
				Type:        "string",
				Description: "The search query, e.g. \"Go 1.22 release notes\"",
			},
			"count": {
				Type:        "integer",
				Description: "Number of results to return (1–20, default 5)",
			},
		},
		Required: []string{"query"},
	},
	ParamAliases: map[string]string{
		"q":           "query",
		"search":      "query",
		"limit":       "count",
		"num_results": "count",
		"max_results": "count",
	},
}

func WebSearchTool(cfg WebSearchConfig) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		query := agent.ArgString(args, "query")
		if query == "" {
			return "", fmt.Errorf("web_search: query is required")
		}
		count := agent.ArgInt(args, "count", defaultSearchCount)
		if count <= 0 {
			count = defaultSearchCount
		}
		if count > maxSearchCount {
			count = maxSearchCount
		}

		provider := agent.ArgString(args, "provider")
		if provider == "" {
			provider = cfg.DefaultProvider
		}
		if provider == "" {
			provider = cfg.detectProvider()
		}
		if provider == "" {
			if p, ok := searchproviders.DefaultRegistry().FirstWebSearchProvider(); ok {
				provider = p.ID()
			}
		}

		var results []SearchResult
		var err error
		if provider != "" {
			if p, ok := searchproviders.DefaultRegistry().WebSearchProvider(provider); ok {
				results, err = p.Search(ctx, query, searchproviders.SearchOptions{MaxResults: count})
			} else {
				switch strings.ToLower(strings.TrimSpace(provider)) {
				case "brave":
					results, err = braveSearch(ctx, cfg.resolveBraveKey(), query, count, cfg.braveURL())
				case "serper":
					results, err = serperSearch(ctx, cfg.resolveSerperKey(), query, count, cfg.serperURL())
				default:
					return "", fmt.Errorf("web_search: unknown provider %q", provider)
				}
			}
		} else {
			return "", fmt.Errorf("web_search: no provider configured; set BRAVE_SEARCH_API_KEY or SERPER_API_KEY or register a web search provider plugin")
		}
		if err != nil {
			return "", fmt.Errorf("web_search: %w", err)
		}

		out, _ := json.Marshal(results)
		return string(out), nil
	}
}

// braveSearch queries the Brave Search API and returns parsed results.
func braveSearch(ctx context.Context, apiKey, query string, count int, endpoint string) ([]SearchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Brave Search requires BRAVE_SEARCH_API_KEY")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("brave endpoint: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", count))
	u.RawQuery = q.Encode()

	ctx2, cancel := context.WithTimeout(ctx, searchTimeoutSec*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx2, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("brave HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("brave parse: %w", err)
	}

	out := make([]SearchResult, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		out = append(out, SearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}
	return out, nil
}

// serperSearch queries the Serper Google Search API and returns parsed results.
func serperSearch(ctx context.Context, apiKey, query string, count int, endpoint string) ([]SearchResult, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Serper search requires SERPER_API_KEY")
	}

	payload, _ := json.Marshal(map[string]any{"q": query, "num": count})

	ctx2, cancel := context.WithTimeout(ctx, searchTimeoutSec*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx2, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serper request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("serper HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("serper parse: %w", err)
	}

	out := make([]SearchResult, 0, len(parsed.Organic))
	for _, r := range parsed.Organic {
		out = append(out, SearchResult{
			Title:   r.Title,
			URL:     r.Link,
			Snippet: r.Snippet,
		})
	}
	return out, nil
}
