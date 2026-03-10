---
summary: "Web search and fetch tools for swarmstr agents"
read_when:
  - Enabling web_search or web_fetch for the agent
  - Setting up a search API key (Perplexity, Brave, etc.)
  - Fetching web pages from within agent turns
title: "Web Tools"
---

# Web Tools

swarmstr ships two lightweight web tools:

- **`web_search`** — search the web using Perplexity, Brave, Gemini, or Grok
- **`web_fetch`** — HTTP fetch + readable extraction (HTML → markdown/text)

These are **not** browser automation. For JavaScript-heavy sites or logins, use the [Browser tool](/tools/browser).

## `web_search`

Search the web from within an agent turn:

```
web_search(query="latest Nostr NIP proposals")
```

**Parameters:**
- `query` (required): search query
- `provider`: override the configured provider
- `limit`: max results (default varies by provider)
- `freshness`: filter by recency (`day`, `week`, `month`)
- `language`: result language code

Results are cached for 15 minutes by default.

## `web_fetch`

Fetch and extract readable content from a URL:

```
web_fetch(url="https://github.com/nostr-protocol/nostr")
```

**Parameters:**
- `url` (required): URL to fetch
- `format`: `"markdown"` (default) or `"text"`
- `maxBytes`: limit response size

`web_fetch` does a plain HTTP GET and extracts readable content. It does **not** execute JavaScript. Use the [Browser tool](/tools/browser) for JavaScript-heavy sites.

## Search Provider Setup

### Provider Auto-Detection

swarmstr checks for API keys in this order:

1. `BRAVE_API_KEY` → Brave Search
2. `GEMINI_API_KEY` → Gemini with Google Search grounding
3. `PERPLEXITY_API_KEY` → Perplexity Search
4. `XAI_API_KEY` → Grok

Configure explicitly:

```json5
{
  "tools": {
    "web": {
      "search": {
        "provider": "perplexity",
        "apiKey": "${PERPLEXITY_API_KEY}"
      }
    }
  }
}
```

### Perplexity Search

1. Create an account at [perplexity.ai/settings/api](https://www.perplexity.ai/settings/api)
2. Generate an API key
3. Add to `~/.swarmstr/.env`:
   ```
   PERPLEXITY_API_KEY=pplx-...
   ```

See [Perplexity setup](/perplexity) for detailed config.

### Brave Search

1. Sign up at [api.search.brave.com](https://api.search.brave.com)
2. Generate an API key
3. Add to `~/.swarmstr/.env`:
   ```
   BRAVE_API_KEY=BSA...
   ```

See [Brave Search setup](/brave-search) for detailed config.

### Gemini (Google Search Grounding)

```bash
GEMINI_API_KEY=AI...
```

```json5
{
  "tools": {
    "web": {
      "search": {
        "provider": "gemini"
      }
    }
  }
}
```

## Configuration Reference

```json5
{
  "tools": {
    "web": {
      "search": {
        "enabled": true,
        "provider": "perplexity",    // auto-detected if not set
        "apiKey": "${PERPLEXITY_API_KEY}",
        "cacheMinutes": 15,
        "maxResults": 10,
        "perplexity": {
          "model": "sonar"
        }
      },
      "fetch": {
        "enabled": true,
        "maxBytes": 500000,
        "timeoutMs": 10000
      }
    }
  }
}
```

## Provider Comparison

| Provider | Pros | API Key Env |
|----------|------|-------------|
| **Perplexity** | Structured results, freshness filters, domain filtering | `PERPLEXITY_API_KEY` |
| **Brave** | Fast, privacy-focused | `BRAVE_API_KEY` |
| **Gemini** | Google Search grounding, AI-synthesized | `GEMINI_API_KEY` |
| **Grok** | xAI web-grounded responses | `XAI_API_KEY` |

## Examples

### Search and summarize

```
web_search(query="Nostr NIP-90 Data Vending Machine specification")
→ Returns top results with snippets

web_fetch(url="https://github.com/nostr-protocol/nostr/blob/master/90.md")
→ Returns full NIP-90 text as markdown
```

### Research workflow

```
web_search(query="best Go libraries for WebSocket relay 2026")
→ Agent reads results, identifies top candidates

web_fetch(url="https://pkg.go.dev/nhooyr.io/websocket")
→ Agent reads package documentation
```

## See Also

- [Browser Tool](/tools/browser)
- [Perplexity Setup](/perplexity)
- [Brave Search Setup](/brave-search)
- [Firecrawl](/tools/firecrawl)
