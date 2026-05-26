---
id: nostr-no-broad-kind-subscription
name: Nostr broad subscription warning
action: warn
event_types: [nostr]
message: Scope Nostr subscriptions with routing tags and since/until instead of kind-only filters.
nostr:
  filter_hashtag: broad-kind-only
---
Warn when a generated subscription is known to be broad and tagless.
