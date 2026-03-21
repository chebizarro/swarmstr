---
summary: "Perplexity Search is not currently a supported provider for metiq's web_search tool"
read_when:
  - Wondering whether Perplexity can be used for agent web search
title: "Perplexity Search"
---

# Perplexity Search

> **Not currently supported**: The `web_search` tool supports [Brave Search](/brave-search) (`BRAVE_SEARCH_API_KEY`) and [Serper](/tools/web) (`SERPER_API_KEY`). Perplexity is not a supported provider at this time.

## Workaround

You can use Perplexity's API indirectly via the `browser.request` gateway method or the `web_fetch` agent tool to call the Perplexity API endpoint directly:

```
web_fetch(url="https://api.perplexity.ai/chat/completions", ...)
```

However, this is not integrated into `web_search`'s provider system.

## See Also

- [Web Tools](/tools/web) — supported search providers (`brave`, `serper`)
- [Brave Search](/brave-search) — setup guide for Brave Search API
- [Browser Tool](/tools/browser) — HTTP fetch for arbitrary URLs
