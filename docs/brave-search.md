---
summary: "Brave Search API setup for metiq's web_search tool"
read_when:
  - Setting up Brave Search for the agent
  - Configuring the BRAVE_API_KEY
title: "Brave Search"
---

# Brave Search

[Brave Search](https://search.brave.com/) provides a privacy-focused web search API for the `web_search` tool.

## Setup

1. Sign up at [api.search.brave.com](https://api.search.brave.com)
2. Create an API subscription (free tier available)
3. Copy your API key

Add to `~/.metiq/.env`:

```
BRAVE_SEARCH_API_KEY=BSAxxxxxxxxxxxxxxxxxx
```

The `web_search` tool auto-detects Brave when `BRAVE_SEARCH_API_KEY` is set — no additional config needed.

## API Tiers

| Tier | Queries/mo | Cost |
|------|-----------|------|
| Free | 2,000 | Free |
| Basic | 20,000 | $5/mo |
| Pro | Unlimited | $10/mo |

For a personal agent with moderate use, the free tier is usually sufficient.

## Privacy

Brave Search doesn't track searches by IP or build user profiles, making it a good choice for privacy-sensitive agent deployments.

## See Also

- [Web Tools](/tools/web)
- [Perplexity Search](/perplexity)
- [Provider Overview](/providers/)
