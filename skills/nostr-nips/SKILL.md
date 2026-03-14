---
name: nostr-nips
description: Nostr Improvement Proposals (NIPs) reference. Use when implementing, verifying, or explaining any Nostr protocol — event kinds, encryption schemes, relay behaviour, identity, payments, DVMs, git, etc. Read the specific NIP file before writing protocol-level code.
metadata:
  { "openclaw": { "emoji": "📡", "os": ["darwin", "linux", "windows"] } }
---

# Nostr NIPs Reference

Full NIP specifications are available as individual files in this directory.
Read the relevant file(s) before implementing or debugging any Nostr protocol feature.

## When to Use

✅ **USE this skill when:**

- Implementing any Nostr event kind (what tags are required? what's the schema?)
- Choosing an encryption scheme (NIP-04 vs NIP-44 vs NIP-59/NIP-17)
- Building relay interactions (AUTH, filters, subscription behaviour)
- Working with identity, zaps, DVMs, git, ecash, or any Nostr-native protocol
- Debugging unexpected relay or client behaviour
- Verifying your event structure matches the spec before publishing

❌ **DON'T use this skill when:**

- You already know the spec well for the NIP in question
- The task is purely application logic with no protocol surface

## NIP Index

| File | Topics |
|------|--------|
| NIP-01.md | Core protocol — event structure, relay communication, filters, basic event kinds |
| NIP-02.md | Follow lists (kind 3) |
| NIP-03.md | OpenTimestamps attestations |
| NIP-04.md | Encrypted DMs — kind 4, AES-CBC (legacy; prefer NIP-17 for new work) |
| NIP-05.md | DNS-based identity verification (`user@domain`) |
| NIP-06.md | Basic key derivation from mnemonic seed phrase |
| NIP-07.md | Browser extension signing (`window.nostr`) |
| NIP-08.md | Mentions (deprecated — see NIP-27) |
| NIP-09.md | Event deletion requests (kind 5) |
| NIP-10.md | `e` and `p` tag conventions for replies and threads |
| NIP-11.md | Relay information document (NIP-11 JSON) |
| NIP-12.md | Generic tag queries (deprecated — folded into NIP-01) |
| NIP-13.md | Proof of work |
| NIP-14.md | Subject tag for text events |
| NIP-15.md | Nostr marketplace (kind 30017/30018 stalls and products) |
| NIP-16.md | Event treatment — replaceable/ephemeral (deprecated — folded into NIP-01) |
| NIP-17.md | Private DMs — gift-wrap (NIP-59) + NIP-44, kind 14/13/1059 — **preferred DM protocol** |
| NIP-18.md | Reposts (kind 6 / kind 16) |
| NIP-19.md | Bech32-encoded identifiers — npub, nsec, note, nprofile, nevent, naddr |
| NIP-20.md | Command results (deprecated — folded into NIP-01) |
| NIP-21.md | `nostr:` URI scheme |
| NIP-22.md | Comment events (kind 1111) — threaded replies on any event kind |
| NIP-23.md | Long-form content — articles (kind 30023) |
| NIP-24.md | Extra metadata fields (display_name, website, etc.) |
| NIP-25.md | Reactions (kind 7) — `+`, `-`, emoji |
| NIP-26.md | Delegated event signing |
| NIP-27.md | Text note references — `nostr:` inline mentions |
| NIP-28.md | Public chat (kinds 40/41/42/43/44) |
| NIP-29.md | Relay-based groups |
| NIP-30.md | Custom emoji |
| NIP-31.md | Labelling (kind 1985) |
| NIP-32.md | Reporting / moderation labels |
| NIP-33.md | Parameterised replaceable events (d-tag) |
| NIP-34.md | Git — repository announcements, issues, patches, PRs (kinds 30617/1621/1617/1618) |
| NIP-35.md | Torrents |
| NIP-36.md | Sensitive content warning |
| NIP-37.md | Draft events |
| NIP-38.md | User statuses (kind 30315) — NIP-38 typing indicators, presence |
| NIP-39.md | External identity claims |
| NIP-40.md | Expiration timestamp |
| NIP-42.md | Relay authentication (AUTH) |
| NIP-43.md | Fast authentication |
| NIP-44.md | Versioned encryption — ChaCha20 / HMAC — **preferred encryption scheme** |
| NIP-45.md | Event counts (`COUNT` verb) |
| NIP-46.md | Nostr Connect / remote signing (NIP-46 bunker) |
| NIP-47.md | Wallet Connect — NWC lightning payments |
| NIP-48.md | Proxy tags |
| NIP-49.md | Private key encryption |
| NIP-50.md | Search capability (`search` filter) |
| NIP-51.md | Lists — mute (10000), pins (10001), people (30000), bookmarks (30001), etc. |
| NIP-52.md | Calendar events |
| NIP-54.md | Wikis |
| NIP-55.md | Android signer |
| NIP-56.md | Reporting (kind 1984) |
| NIP-57.md | Lightning zaps — LNURL-pay + kind 9734/9735 |
| NIP-58.md | Badges |
| NIP-59.md | Gift wrap — rumor → seal (kind 13) → gift wrap (kind 1059) — used by NIP-17 |
| NIP-62.md | Request to vanish |
| NIP-64.md | Chess (PGN) |
| NIP-65.md | Relay list metadata (kind 10002) — outbox model |
| NIP-66.md | Relay discovery / status |
| NIP-68.md | Picture events |
| NIP-70.md | Protected events |
| NIP-71.md | Video events |
| NIP-72.md | Moderated communities |
| NIP-73.md | External content IDs |
| NIP-75.md | Zap goals |
| NIP-77.md | Negentropy reconciliation |
| NIP-78.md | Application-specific data (kind 30078) |
| NIP-84.md | Highlights |
| NIP-85.md | Reviews |
| NIP-86.md | Relay management API |
| NIP-88.md | Polls |
| NIP-89.md | Recommended application handlers |
| NIP-90.md | Data Vending Machines — job requests (kind 5000-5999) and results (6000-6999) |
| NIP-92.md | Media attachments (imeta tag) |
| NIP-94.md | File metadata (kind 1063) |
| NIP-96.md | HTTP file storage integration |
| NIP-98.md | HTTP auth — signed Nostr events as Bearer tokens |
| NIP-99.md | Classified listings |

## Quick Protocol Decisions

**DM encryption:** Use NIP-44 (ChaCha20) + NIP-17 (gift wrap) for new work. NIP-04 (AES-CBC) is legacy — only implement it for backwards compatibility.

**Replaceable events:** Use a `d` tag (NIP-33) for parameterised replaceable events (kind 30000–39999). Plain replaceable events are kinds 10000–19999.

**Relay auth:** NIP-42. Always sign with the agent's key; handle `AUTH` challenges before subscribing on restricted relays.

**Identity:** NIP-19 for encoding (npub/nsec/naddr etc.). NIP-05 for DNS verification. NIP-39 for external identity claims.

**Payments:** NIP-57 for zaps (LNURL + kind 9734/9735). NIP-47 (NWC) for programmatic wallet access.

**DVMs:** NIP-90. Job request = kind 5000+, result = kind 6000+, status = kind 7000. Use `#p` tag to address a specific DVM.
