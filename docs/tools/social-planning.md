---
title: "Social Planning Tools"
summary: "Built-in scaffolding for agent social media outreach"
read_when:
  - You want to set up recurring social posting
  - You want to manage follow/engage strategies
  - You want to audit social action history
  - You want to understand rate limiting for social actions
---

# Social Planning Tools

metiq includes built-in social media planning tools so agents don't have to
build cadence management, rate limiting, and history tracking from scratch.

---

## Tools

### `social_plan_add` — register a recurring social action

Create a named plan for a recurring social task.

```
social_plan_add(
  id: "daily-dev-post",
  type: "post",
  schedule: "0 9 * * *",
  instructions: "Write a short technical note about a Go concurrency pattern",
  tags: "golang,dev"
)
```

Plan types:
- `post` — publish a note, article, or thread
- `follow` — follow users matching a strategy
- `engage` — reply, react, or repost relevant content

**Important:** `social_plan_add` only registers the plan. To actually execute
it on schedule, also call `cron_add` with matching instructions:

```
cron_add(
  schedule: "0 9 * * *",
  instructions: "Execute social plan 'daily-dev-post': write and publish a short technical note",
  label: "social:daily-dev-post"
)
```

### `social_plan_list` — list plans and usage

Shows all registered plans and current daily rate limit usage:

```
social_plan_list()
→ {
    "plans": [...],
    "plan_count": 2,
    "daily_usage": [
      {"type": "post", "used_today": 3, "daily_limit": 10, "remaining": 7},
      {"type": "follow", "used_today": 0, "daily_limit": 20, "remaining": 20},
      {"type": "engage", "used_today": 5, "daily_limit": 30, "remaining": 25}
    ]
  }
```

### `social_plan_remove` — remove a plan

```
social_plan_remove(id: "daily-dev-post")
```

### `social_record` — log a completed action

Call after successfully posting, following, or engaging. This updates the
rate limiter and history log:

```
social_record(
  type: "post",
  action: "Posted note about Go channels",
  event_id: "abc123...",
  plan_id: "daily-dev-post"
)
→ {"recorded": true, "type": "post", "remaining_today": 6}
```

Returns an error if the daily rate limit for that action type has been exceeded.

### `social_history` — query past actions

```
social_history(type: "post", limit: 10)
→ {"entries": [...], "count": 5}
```

Filter by type or leave empty for all. Newest entries first.

---

## Rate Limits

Default daily limits (sliding 24-hour window):

| Type | Daily limit |
|------|------------|
| `post` | 10 |
| `follow` | 20 |
| `engage` | 30 |

Rate limits are enforced by `social_record`. If you call `nostr_publish`
directly without recording, the limit won't apply — but you also won't
have history tracking.

---

## Workflow Example

### 1. Create a posting plan

```
social_plan_add(
  id: "4h-nostr-post",
  type: "post",
  schedule: "0 */4 * * *",
  instructions: "Write a 1-2 paragraph note about something interesting in the Nostr ecosystem"
)
```

### 2. Wire cron execution

```
cron_add(
  schedule: "0 */4 * * *",
  instructions: "Check social_plan_list for the '4h-nostr-post' plan. Write a fresh note using nostr_publish. Then call social_record to log it.",
  label: "social:4h-nostr-post"
)
```

### 3. Agent execution flow (on cron trigger)

1. Agent receives cron instructions
2. Checks `social_plan_list` → sees plan details and remaining quota
3. Checks `social_history(type: "post")` → avoids repeating recent topics
4. Composes note content
5. Publishes via `nostr_publish`
6. Records via `social_record(type: "post", action: "...", event_id: "...")`

### 4. Audit

```
social_history(type: "post", limit: 20)
social_plan_list()
```

---

## Memory Integration

Social plans and history are held in-process. For persistent cross-restart
storage, the agent should periodically save plan state to memory:

```
memory_store(
  text: "Social plan state: 3 posts today, last topic was 'Go error handling'",
  topic: "social_state"
)
```

On restart, the agent can search memory to restore context:

```
memory_search(query: "social plan state")
```

---

## Tips

- Keep plan IDs descriptive: `daily-dev-post`, `weekly-follow-devs`
- Use `social_history` before posting to avoid topic repetition
- Check `social_plan_list` before executing to see remaining quota
- Use tags to categorise plans for easier filtering
- Combine with `memory_pin` for persistent social strategy rules
