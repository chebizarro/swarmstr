---
summary: "Web search and fetch tools for metiq agents"
read_when:
  - Enabling web_search or web_fetch for the agent
  - Setting up a search API key (Perplexity, Brave, etc.)
  - Fetching web pages from within agent turns
title: "Web Tools"
---

# Web Tools

metiq ships two lightweight web tools:

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
- `count`: max results (default 10)

Provider is auto-detected from available API keys.

## `web_fetch`

Fetch and extract readable content from a URL:

```
web_fetch(url="https://github.com/nostr-protocol/nostr")
```

**Parameters:**
- `url` (required): URL to fetch
- `max_chars` (optional, default 50000): truncation limit in characters
- `timeout_seconds` (optional, default 30): request timeout
- `allow_local` (optional, default false): allow loopback/private addresses

`web_fetch` does a plain HTTP GET and extracts readable content (HTML tags stripped). It does **not** execute JavaScript.

## Search Provider Setup

### Provider Auto-Detection

metiq checks for API keys in this order:

1. `BRAVE_SEARCH_API_KEY` → Brave Search
2. `SERPER_API_KEY` → Serper (Google via Serper.dev)

Set the appropriate environment variable to enable the tool.

### Brave Search

1. Sign up at [api.search.brave.com](https://api.search.brave.com)
2. Generate an API key
3. Set in your environment:
   ```
   BRAVE_SEARCH_API_KEY=BSA...
   ```

See [Brave Search setup](/brave-search) for detailed config.

### Serper (Google Search)

1. Sign up at [serper.dev](https://serper.dev)
2. Generate an API key
3. Set in your environment:
   ```
   SERPER_API_KEY=...
   ```

## Configuration Reference

Web tools are configured via environment variables:

| Variable | Provider | Notes |
|----------|----------|---------|
| `BRAVE_SEARCH_API_KEY` | Brave Search | First-choice if set |
| `SERPER_API_KEY` | Serper (Google) | Fallback |

There is no config-file section for web tools — just set the env var for the provider you want to use.

## Provider Comparison

| Provider | Pros | API Key Env |
|----------|------|-------------|
| **Brave** | Fast, privacy-focused, structured results | `BRAVE_SEARCH_API_KEY` |
| **Serper** | Google Search results via Serper.dev | `SERPER_API_KEY` |

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
