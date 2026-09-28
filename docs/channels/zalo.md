---
summary: "Zalo Official Account channel for metiq, including durable refresh-token rotation"
read_when:
  - Adding a Zalo Official Account as a secondary channel
  - Seeing "refresh_token is not a gateway-store secret reference" in metiqd logs
title: "Zalo Channel"
---

# Zalo Channel

The `zalo` channel bridges a Zalo Official Account (OA) into the
`nostr_channels` pipeline using the Zalo OA Open API. It is compiled into
metiqd.

## Configuration

```json5
{
  "nostr_channels": {
    "zalo-oa": {
      "kind": "zalo",
      "enabled": true,
      "config": {
        "app_id": "...",
        "app_secret": "...",
        "oa_id": "...",
        "refresh_token": {"source": "store", "provider": "gateway-store", "id": "ZALO_REFRESH_TOKEN"},
        "allowed_senders": []          // optional follower user-ID allowlist
      }
    }
  }
}
```

Register `<admin_addr>/webhooks/zalo/<channel_id>` in the Zalo OA Admin Portal
under Webhook Settings and enable the `user_send_text` event.

## Refresh-token rotation

Zalo refresh tokens are **single-use**. Every access-token refresh (at connect
and every 90 minutes afterwards) returns a new refresh token and invalidates
the previous one. metiqd can only save the new token when `refresh_token` is a
gateway-store secret reference, as in the example above. It then writes each
rotated token back to the store before the refresh counts as successful.

If `refresh_token` is a plaintext string, the channel keeps working until
metiqd restarts. Each rotation logs:

```
zalo: channel=<id> refresh token rotated but refresh_token is not a gateway-store secret reference; the rotation will not survive a restart. ...
```

After a restart, the plaintext token in config has already been used and Zalo
rejects it.

### Migrating a plaintext `refresh_token`

Prerequisites: metiqd runs with a protected (OS keychain-backed) secret store,
and the gateway WebSocket is enabled (`gateway_ws_listen_addr` and
`gateway_ws_token` in `bootstrap.json`). `secrets.store.set` accepts secret
values only over that WebSocket.

1. **Get a fresh refresh token.** Do not copy the plaintext value from your
   config. metiqd spent it the first time the channel connected. Issue a new
   token for the OA from the Zalo developer console or OAuth flow.

2. **Store it in the gateway-store.** The value is read from stdin, so it does
   not end up in shell history:

   ```bash
   metiq secrets set ZALO_REFRESH_TOKEN < zalo-refresh-token.txt
   # stored ZALO_REFRESH_TOKEN; reference it in config as {"source":"store","provider":"gateway-store","id":"ZALO_REFRESH_TOKEN"}
   ```

   Pass `--ws-url ws://host:port/ws --ws-token ...` if the CLI cannot read
   your bootstrap config. For more than one Zalo channel, use a distinct name
   per channel, e.g. `ZALO_SHOP_REFRESH_TOKEN`.

3. **Point the config field at the stored entry.** In the channel's `config`
   block, replace the plaintext string with the reference and leave the other
   fields unchanged:

   ```json5
   // before
   "refresh_token": "<plaintext token>",
   // after
   "refresh_token": {"source": "store", "provider": "gateway-store", "id": "ZALO_REFRESH_TOKEN"},
   ```

4. **Restart metiqd.** On connect, the channel resolves the reference, and
   from then on each rotation is written back to `ZALO_REFRESH_TOKEN`. The
   warning above should no longer appear.

If the reference cannot be resolved (missing entry, or no protected store),
the channel fails to start instead of connecting with a bad token.

## See Also

- [Secrets Management](/gateway/secrets)
- [Channel Index](/channels/)
