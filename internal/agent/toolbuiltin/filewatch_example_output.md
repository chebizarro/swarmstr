# File Watch Tool - Agent-Observable Limitations

This document shows examples of how limitations and potential issues are surfaced to agents.

## Example 1: Recursive Watch with Many Directories

**Tool Call:**
```json
{
  "name": "large-repo-watch",
  "session_id": "sess-123",
  "path": "/large/monorepo",
  "recursive": true
}
```

**Response with Warning:**
```json
{
  "watching": true,
  "name": "large-repo-watch",
  "dir_count": 87,
  "warning": "Watching 87 directories (limit: 100). Consider watching a more specific subdirectory if performance issues occur."
}
```

## Example 2: Approaching Global Watch Limit

**Response when 16 of 20 slots used:**
```json
{
  "watching": true,
  "name": "another-watch",
  "warning_capacity": "Using 16 of 20 available watch slots. Remove unused watches with file_watch_remove."
}
```

## Example 3: Too Many Directories (Error)

**Tool Call:**
```json
{
  "name": "huge-watch",
  "path": "/entire/filesystem",
  "recursive": true
}
```

**Error Response:**
```
file_watch_add: recursive watch would monitor 1523 directories (limit: 100); consider watching a more specific subdirectory or using non-recursive mode
```

## Example 4: File Too Large for Content Filter

**Tool Call:**
```json
{
  "name": "log-watch",
  "path": "/var/log/huge.log",
  "contains": "ERROR"
}
```

**Error in Event Delivery:**
```json
{
  "error": "file \"/var/log/huge.log\" is 52428800 bytes (limit: 10485760 bytes for content filtering); consider removing contains/contains_regex filter or watching a smaller file",
  "watch_path": "/var/log/huge.log",
  "at": 1710534847
}
```

## Example 5: Watcher Errors Delivered to Agent

**Event Payload when fsnotify encounters an error:**
```json
{
  "error": "inotify watch limit reached",
  "watch_path": "/watched/path",
  "at": 1710534847
}
```

## Tool Definition Warnings

The tool description now includes:

> LIMITATIONS: (1) Maximum 20 concurrent watches system-wide. (2) Content filters (contains/contains_regex) only work on files ≤10MB. (3) Recursive mode limited to 100 directories. (4) If both 'contains' and 'contains_regex' are specified, file must match BOTH filters (AND logic). (5) Watches auto-expire after ttl_seconds or max_events.

Each parameter also includes specific warnings:

- **contains**: "WARNING: Reads entire file into memory; fails on files >10MB."
- **contains_regex**: "WARNING: Reads entire file into memory; fails on files >10MB. If both 'contains' and 'contains_regex' are set, file must match BOTH."
- **recursive**: "WARNING: Limited to 100 directories total. OS may have lower limits (e.g., Linux inotify.max_user_watches)."
- **max_events**: "Prevents runaway watches on high-activity paths."
