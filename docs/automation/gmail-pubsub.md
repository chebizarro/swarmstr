---
summary: "Gmail Pub/Sub push wired into swarmstr webhooks for email-triggered agent turns"
read_when:
  - Wiring Gmail inbox triggers to swarmstr
  - Setting up Pub/Sub push for agent wake from email
title: "Gmail PubSub"
---

# Gmail Pub/Sub → swarmstr

Goal: Gmail watch → Pub/Sub push → `gog gmail watch serve` → swarmstr webhook → agent turn → Nostr DM reply.

This is one pattern for bridging email into swarmstr. The agent reads the email, processes it, and can reply back via Nostr DM to the configured contact.

## Prereqs

- `gcloud` installed and logged in ([install guide](https://cloud.google.com/sdk/docs/install)).
- `gog` (gogcli) installed and authorized for the Gmail account ([gogcli.sh](https://gogcli.sh/)).
- swarmstr webhooks enabled (see [Webhooks](/automation/webhook)).
- `tailscale` logged in ([tailscale.com](https://tailscale.com/)) — Tailscale Funnel is the supported way to expose the push endpoint.

> **Why not just use Nostr?** Nostr is the primary swarmstr channel. Gmail Pub/Sub is for cases where you need to bridge external email events into the agent. The agent can reply via Nostr DM to a human who monitors a Nostr client.

Example webhook config (enable Gmail preset mapping):

```json5
{
  "webhooks": {
    "enabled": true,
    "token": "${SWARMSTR_HOOK_TOKEN}",
    "path": "/hooks",
    "presets": ["gmail"]
  }
}
```

To deliver the Gmail summary to a Nostr DM, override the preset with a mapping:

```json5
{
  "webhooks": {
    "enabled": true,
    "token": "${SWARMSTR_HOOK_TOKEN}",
    "presets": ["gmail"],
    "mappings": [
      {
        "match": { "path": "gmail" },
        "action": "agent",
        "wakeMode": "now",
        "name": "Gmail",
        "sessionKey": "hook:gmail:{{messages[0].id}}",
        "messageTemplate": "New email from {{messages[0].from}}\nSubject: {{messages[0].subject}}\n{{messages[0].snippet}}\n{{messages[0].body}}",
        "model": "anthropic/claude-haiku-4-5",
        "deliver": true,
        "channel": "nostr",
        "to": "npub1yourcontact..."
      }
    ]
  }
}
```

Set `channel: "nostr"` and `to: "npub1..."` to deliver the agent's summary back via Nostr DM.

To set a default model specifically for Gmail hook runs:

```json5
{
  "webhooks": {
    "gmail": {
      "model": "anthropic/claude-haiku-4-5",
      "thinking": "off"
    }
  }
}
```

Notes:

- Gmail hook content is wrapped with external-content safety boundaries by default.
- Fallback order: `webhooks.gmail.model` → `agents.defaults.model.fallbacks` → primary.

## One-time Setup

1. Select the GCP project **that owns the OAuth client** used by `gog`.

```bash
gcloud auth login
gcloud config set project <project-id>
```

Note: Gmail watch requires the Pub/Sub topic to live in the same project as the OAuth client.

2. Enable APIs:

```bash
gcloud services enable gmail.googleapis.com pubsub.googleapis.com
```

3. Create a topic:

```bash
gcloud pubsub topics create swarmstr-gmail-watch
```

4. Allow Gmail push to publish:

```bash
gcloud pubsub topics add-iam-policy-binding swarmstr-gmail-watch \
  --member=serviceAccount:gmail-api-push@system.gserviceaccount.com \
  --role=roles/pubsub.publisher
```

## Expose the Endpoint

Swarmstr runs a webhook HTTP server locally. You need to expose it publicly for Pub/Sub push.

**Tailscale Funnel (recommended)**:

```bash
tailscale funnel 18789
```

This gives you a stable public HTTPS URL like `https://myhost.tail1234.ts.net`.

Use that URL as the push endpoint base. The full Gmail push path would be:
`https://myhost.tail1234.ts.net/hooks/gmail`

**SSH Tunnel (alternative)**:

```bash
ssh -R 80:localhost:18789 serveo.net
```

## Start the Watch

```bash
gog gmail watch start \
  --account yourname@gmail.com \
  --label INBOX \
  --topic projects/<project-id>/topics/swarmstr-gmail-watch
```

Save the `history_id` from the output (for debugging).

## Run the Push Handler

```bash
gog gmail watch serve \
  --account yourname@gmail.com \
  --bind 127.0.0.1 \
  --port 8788 \
  --path /gmail-pubsub \
  --token <shared-token> \
  --hook-url http://127.0.0.1:18789/hooks/gmail \
  --hook-token "${SWARMSTR_HOOK_TOKEN}" \
  --include-body \
  --max-bytes 20000
```

Notes:

- `--token` protects the push endpoint from unauthorized calls.
- `--hook-url` points to swarmstr's `/hooks/gmail` endpoint.
- `--include-body` and `--max-bytes` control the body snippet sent to swarmstr.

## Create the Pub/Sub Push Subscription

```bash
gcloud pubsub subscriptions create swarmstr-gmail-watch-push \
  --topic swarmstr-gmail-watch \
  --push-endpoint "https://myhost.tail1234.ts.net/gmail-pubsub?token=<shared-token>"
```

## Gateway Auto-Start

When `webhooks.enabled=true` and `webhooks.gmail.account` is set, swarmstrd starts `gog gmail watch serve` on boot and auto-renews the watch.

Set `SWARMSTR_SKIP_GMAIL_WATCHER=1` to opt out (useful if you run the daemon yourself).

Manual start:

```bash
swarmstr webhooks gmail run
```

## Test

Send a message to the watched inbox:

```bash
gog gmail send \
  --account yourname@gmail.com \
  --to yourname@gmail.com \
  --subject "watch test" \
  --body "ping"
```

Check watch state and history:

```bash
gog gmail watch status --account yourname@gmail.com
gog gmail history --account yourname@gmail.com --since <historyId>
```

After a few seconds you should receive a Nostr DM from your swarmstr agent summarizing the email.

## Troubleshooting

- `Invalid topicName`: project mismatch (topic not in the OAuth client project).
- `User not authorized`: missing `roles/pubsub.publisher` on the topic.
- Empty messages: Gmail push only provides `historyId`; fetch via `gog gmail history`.
- No Nostr DM received: check `~/.swarmstr/logs/` for webhook delivery errors; verify `to` npub is correct.
- Hook not triggered: verify `swarmstr hooks list` shows the gmail hook enabled and `webhooks.enabled=true` in config.

## Cleanup

```bash
gog gmail watch stop --account yourname@gmail.com
gcloud pubsub subscriptions delete swarmstr-gmail-watch-push
gcloud pubsub topics delete swarmstr-gmail-watch
```

## See Also

- [Webhooks](/automation/webhook)
- [Nostr Channel](/channels/nostr)
- [Cron Jobs](/automation/cron-jobs) — for polling instead of push
