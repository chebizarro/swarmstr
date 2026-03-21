---
summary: "Browser fetch and optional browser proxy for metiq agents"
read_when:
  - Using web_fetch or browser.request gateway method
  - Connecting metiq to a Playwright or CDP browser proxy
title: "Browser"
---

# Browser

metiq's browser package provides HTTP fetch with HTML-to-text extraction.
It is the backing implementation for the `web_fetch` agent tool and the
`browser.request` gateway method.

## `web_fetch` tool

The agent's built-in web fetcher. See [Web Tools](/tools/web) for full details.

```
web_fetch(url="https://nostr.com/protocol")
```

- Plain HTTP GET; HTML tags stripped; result returned as readable text.
- Does **not** execute JavaScript.

## `browser.request` gateway method

Low-level HTTP fetch callable from the gateway API:

```json
{
  "method": "browser.request",
  "params": {
    "method": "GET",
    "path": "https://relay.damus.io",
    "timeout_ms": 10000
  }
}
```

Response:

```json
{
  "ok": true,
  "status_code": 200,
  "content_type": "text/html; charset=utf-8",
  "url": "https://relay.damus.io",
  "text": "..."
}
```

**Parameters:**

- `method`: HTTP method (`GET`, `POST`, etc.)
- `path`: absolute URL (e.g. `https://example.com/page`) or path relative to `METIQ_BROWSER_URL`
- `query`: optional query parameters map
- `headers`: optional additional request headers map
- `body`: optional request body (string, object, or array)
- `timeout_ms`: request timeout in milliseconds

## Optional browser proxy (`METIQ_BROWSER_URL`)

For JavaScript-heavy sites or browser automation, you can run a Playwright or CDP
bridge server and configure metiq to route `browser.request` calls through it:

```bash
export METIQ_BROWSER_URL=http://127.0.0.1:19222
export METIQ_BROWSER_TOKEN=your-bridge-token  # optional
```

When `METIQ_BROWSER_URL` is set, `browser.request` calls with relative paths
are proxied to the bridge server. Absolute URLs are fetched directly without proxying.

When `METIQ_BROWSER_URL` is **not** set, `browser.request` with relative paths
returns an error (`browser control is disabled`).

## SSRF protection

`web_fetch` enforces an SSRF guard by default — it rejects requests to private
network addresses (`127.x.x.x`, `10.x.x.x`, `192.168.x.x`, etc.).

To allow local addresses (useful for intranet or testing):

```
web_fetch(url="http://192.168.1.10/api", allow_local=true)
```

Or configure the tool with `AllowLocal: true` at registration time.

## See Also

- [Web Tools](/tools/web) — `web_search` and `web_fetch` tool reference
- [Sandboxing](/gateway/sandboxing)
