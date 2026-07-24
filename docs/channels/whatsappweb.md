---
summary: "Unofficial WhatsApp personal-account linked-device channel using a local Baileys bridge"
read_when:
  - Connecting a personal WhatsApp account
  - Running the whatsappweb Baileys bridge
title: "WhatsApp Linked Device"
---

# WhatsApp linked-device channel

> **Account-ban and Terms of Service risk:** This transport automates WhatsApp Web
> through the unofficial Baileys library. It may violate WhatsApp's terms and can
> cause temporary restriction or permanent account/number bans. Use a separate
> account and phone number that you can afford to lose. The official
> `whatsapp` channel (Meta Cloud API) remains the recommended production option.

The `whatsappweb` channel is separate from the official `whatsapp` Cloud API
channel. It runs Baileys in a local Node.js bridge and keeps credentials in one
auth-state directory per configured swarmstr account.

## Run the bridge

Node.js 22 or later is required:

```bash
cd internal/extensions/whatsappweb/bridge
npm install
WHATSAPPWEB_BRIDGE_TOKEN='choose-a-local-secret' npm start
```

The bridge listens on `127.0.0.1:18789` by default and stores managed account
state below `~/.metiq/whatsappweb/accounts`. Do not expose it to a network.
Remote bridge URLs require HTTPS, a bearer token, and
`allow_remote_bridge: true`; the bridge itself also refuses a remote bind unless
`WHATSAPPWEB_ALLOW_REMOTE=true` and a token are configured.

## Configure accounts

Every top-level `nostr_channels` key is an account. This reuses the same account
resolution and `channels.start`/`channels.stop` lifecycle as other extensions:

```json5
{
  "nostr_channels": {
    "personal": {
      "kind": "whatsappweb",
      "allow_from": ["15551234567@s.whatsapp.net"],
      "config": {
        "bridge_url": "http://127.0.0.1:18789",
        "bridge_token": "choose-a-local-secret",
        "default_account": true,
        "default_to": "15551234567",
        "reaction_level": "minimal"
      }
    },
    "family": {
      "kind": "whatsappweb",
      "config": {
        "bridge_url": "http://127.0.0.1:18789",
        "bridge_token": "choose-a-local-secret",
        "auth_dir": "/secure/swarmstr/whatsapp-family"
      }
    }
  }
}
```

`auth_dir` is a path on the bridge host. If omitted, a distinct managed
directory is derived from the account ID. For the literal `default` account,
the bridge detects legacy credentials in its old data root and copies them
safely into the new per-account directory. The legacy source is preserved.

## Link a device

Start the account, then request a QR value:

```json
{"method":"channels.start","params":{"channel":"whatsappweb","account_id":"personal"}}
{"method":"whatsappweb.auth_qr","params":{"account_id":"personal"}}
```

Render the returned `result.qr` as a QR code and scan it from **WhatsApp →
Linked Devices**. A phone-number pairing code is also supported:

```json
{"method":"whatsappweb.auth_pair_code","params":{"account_id":"personal","phone_number":"+1 555 123 4567"}}
```

Use `whatsappweb.auth_status` to inspect public connection state.
`channels.stop` disconnects without deleting credentials.
`whatsappweb.logout` logs out and removes recognized credential files.

These device-authentication operations are **not** `channels.pairing.*`.
The latter approves unknown people who message an agent; it does not link a
WhatsApp account.

## Messaging behavior

- Direct targets accept phone numbers or `@s.whatsapp.net`/`@lid` JIDs.
- Group threads use their `@g.us` JID.
- Inbound group messages preserve the group as `ThreadID` and the actual member
  as `SenderID`, so access checks and sessions use participant identity.
- Inbound delivery is event-driven over one long-lived WebSocket. Both the Go
  connector and bridge reconnect with exponential backoff; there is no inbox
  polling fallback.
- Reaction levels are `off`, `ack`, `minimal`, and `extensive`. Agent reactions
  are allowed only at `minimal` or `extensive`; `minimal` restricts reactions to
  a conservative emoji set.

The bridge methods `whatsappweb.send`, `whatsappweb.typing`,
`whatsappweb.add_reaction`, and `whatsappweb.remove_reaction` all resolve the
configured account internally. Raw auth-state contents are never returned.
