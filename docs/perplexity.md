---
summary: "Perplexity Search API setup for swarmstr's web_search tool"
read_when:
  - Setting up Perplexity for the agent's web search
  - Configuring the PERPLEXITY_API_KEY
title: "Perplexity Search"
---

# Perplexity Search

[Perplexity](https://perplexity.ai) provides AI-powered web search with structured results, domain filtering, and freshness controls.

## Setup

1. Create an account at [perplexity.ai/settings/api](https://www.perplexity.ai/settings/api)
2. Generate an API key
3. Note your usage tier

Add to `~/.swarmstr/.env`:

```
PERPLEXITY_API_KEY=pplx-...
```

Config:

```json5
{
  "tools": {
    "web": {
      "search": {
        "provider": "perplexity",
        "apiKey": "${PERPLEXITY_API_KEY}",
        "perplexity": {
          "model": "sonar"   // "sonar" | "sonar-pro"
        }
      }
    }
  }
}
```

## Models

| Model | Notes |
|-------|-------|
| `sonar` | Standard, fast, good for most queries |
| `sonar-pro` | Higher quality, more expensive |

## Advanced Options

```json5
{
  "tools": {
    "web": {
      "search": {
        "provider": "perplexity",
        "apiKey": "${PERPLEXITY_API_KEY}",
        "perplexity": {
          "model": "sonar",
          "searchDomainFilter": ["github.com", "nostr.com"],  // optional domain allowlist
          "returnImages": false,
          "returnRelatedQuestions": false,
          "searchRecencyFilter": "week"  // "day" | "week" | "month" | "year"
        }
      }
    }
  }
}
```

## See Also

- [Web Tools](/tools/web)
- [Brave Search](/brave-search)
- [Provider Overview](/providers/)
