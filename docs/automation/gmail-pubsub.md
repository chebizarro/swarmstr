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

Example webhook config — add a `gmail` mapping to your ConfigDoc:

```json5
{
  "hooks": {
    "enabled": true,
    "token": "${SWARMSTR_HOOK_TOKEN}",
    "mappings": [
      {
        "match": { "path": "gmail" },
        "action": "agent",
        "wake_mode": "now",
        "name": "Gmail",
        "session_key": "hook:gmail",
        "message_template": "New email from {{messages[0].from}}\nSubject: {{messages[0].subject}}\n{{messages[0].snippet}}\n{{messages[0].body}}",
        "deliver": true,
        "channel": "nostr",
        "to": "npub1yourcontact..."
      }
    ]
  }
}
```

Set `channel: "nostr"` and `to: "npub1..."` to deliver the agent's summary back via Nostr DM.

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

Swarmstr runs the webhook HTTP server on the admin listen address configured in `bootstrap.json`
(`admin_listen_addr`, e.g. `127.0.0.1:18080`). You need to expose it publicly for Pub/Sub push.

**Tailscale Funnel (recommended)** — expose the admin port:

```bash
tailscale funnel <admin_port>
```

This gives you a stable public HTTPS URL like `https://myhost.tail1234.ts.net`.

The full Gmail push path would be:
`https://myhost.tail1234.ts.net/hooks/gmail`

**SSH Tunnel (alternative)**:

```bash
ssh -R 80:localhost:<admin_port> serveo.net
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
  --hook-url http://127.0.0.1:<admin_port>/hooks/gmail \
  --hook-token "${SWARMSTR_HOOK_TOKEN}" \
  --include-body \
  --max-bytes 20000
```

Notes:

- `--token` protects the push endpoint from unauthorized calls.
- `--hook-url` points to swarmstr's `/hooks/gmail` endpoint (using your `admin_listen_addr` port).
- `--include-body` and `--max-bytes` control the body snippet sent to swarmstr.

## Create the Pub/Sub Push Subscription

```bash
gcloud pubsub subscriptions create swarmstr-gmail-watch-push \
  --topic swarmstr-gmail-watch \
  --push-endpoint "https://myhost.tail1234.ts.net/gmail-pubsub?token=<shared-token>"
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
- No Nostr DM received: check `swarmstr logs --lines 50` for webhook delivery errors; verify `to` npub is correct.
- Hook not triggered: verify `swarmstr hooks list` shows the gmail hook enabled and `hooks.enabled=true` in config.

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
