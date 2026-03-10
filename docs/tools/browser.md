---
summary: "Browser tool: agent-controlled Chromium automation in swarmstr"
read_when:
  - Adding agent-controlled browser automation
  - Debugging browser connectivity or CDP
  - Configuring the browser tool for the agent
title: "Browser Tool"
---

# Browser Tool

swarmstr can control a **dedicated Chromium/Chrome/Brave/Edge profile** for agent-driven browser automation. The browser is isolated from your personal browser and managed through the daemon.

## What You Get

- A separate browser profile named **swarmstr** (isolated from your daily driver).
- Deterministic tab control: list, open, focus, close tabs.
- Agent actions: click, type, drag, select, screenshot, snapshot, PDF.
- No interference with your personal browser profile.

## Quick Start

```bash
# Check browser status
swarmstr browser status

# Start the managed browser
swarmstr browser start

# Open a URL
swarmstr browser open https://relay.damus.io

# Take a snapshot
swarmstr browser snapshot

# Screenshot
swarmstr browser screenshot
```

## Configuration

```json5
{
  "browser": {
    "enabled": true,
    "defaultProfile": "swarmstr",
    "headless": false,
    "executablePath": "/usr/bin/chromium",   // or Brave, Chrome path
    "profiles": {
      "swarmstr": {
        "cdpPort": 18800
      }
    }
  }
}
```

## Agent Browser Tools

The following tools are available to the agent when the browser is configured:

### `browser_navigate`

```
browser_navigate(url, targetId?)
```

Navigate to a URL in the browser.

### `browser_snapshot`

```
browser_snapshot(format?, targetId?)
```

Capture the current page as an accessibility tree (AI-readable page structure).

### `browser_screenshot`

```
browser_screenshot(fullPage?, targetId?)
```

Capture a PNG screenshot of the current page.

### `browser_click`

```
browser_click(ref, button?, double?, targetId?)
```

Click on an element identified by `ref` (from a snapshot).

### `browser_type`

```
browser_type(ref, text, submit?, targetId?)
```

Type text into a field.

### `browser_navigate` + `browser_wait`

Navigate then wait for content:

```
browser_navigate(url="https://example.com")
browser_wait(text="Expected content")
```

### `browser_evaluate`

```
browser_evaluate(fn, ref?, targetId?)
```

Execute JavaScript in the page context.

### `browser_pdf`

```
browser_pdf(targetId?)
```

Export the current page as PDF.

## SSRF Policy

The browser tool uses a SSRF (Server-Side Request Forgery) policy to prevent the agent from accessing internal network resources:

```json5
{
  "browser": {
    "ssrfPolicy": {
      "dangerouslyAllowPrivateNetwork": false,   // default: false
      "hostnameAllowlist": ["*.relay.nostr.com"]  // optional explicit allowlist
    }
  }
}
```

By default, the browser cannot access private network addresses (192.168.x.x, 10.x.x.x, 127.x.x.x) to prevent the agent from probing your local network.

## Multi-Profile Support

Create separate browser profiles for different use cases:

```json5
{
  "browser": {
    "profiles": {
      "swarmstr": { "cdpPort": 18800 },
      "research": { "cdpPort": 18801 },
      "remote": { "cdpUrl": "http://10.0.0.42:9222" }
    }
  }
}
```

## CLI Reference

```bash
swarmstr browser status
swarmstr browser start [--browser-profile swarmstr]
swarmstr browser stop
swarmstr browser reset-profile
swarmstr browser tabs
swarmstr browser open <url>
swarmstr browser focus <targetId>
swarmstr browser close [targetId]
swarmstr browser screenshot [targetId] [--full-page]
swarmstr browser snapshot [--format aria|ai]
swarmstr browser navigate <url>
swarmstr browser click <ref>
swarmstr browser type <ref> <text>
swarmstr browser press <key>
swarmstr browser evaluate --fn <code>
swarmstr browser pdf
```

## Browser vs Web Fetch

| | Browser | Web Fetch |
|--|---------|-----------|
| JavaScript | ✅ Full JS execution | ❌ Static HTML only |
| Login sessions | ✅ Persistent cookies | ❌ No session |
| Screenshots | ✅ | ❌ |
| Speed | Slower | Faster |
| Use case | Dynamic sites, logins | Simple page reading |

Use `web_fetch` for reading static content. Use the browser for JavaScript-heavy sites, requiring login, or when you need screenshots.

## See Also

- [Web Tools](/tools/web)
- [CLI: browser](/cli/index#browser)
- [Sandboxing](/gateway/sandboxing)
