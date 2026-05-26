---
id: testing-no-sleep-based-events
name: No sleep-based event tests
action: warn
event_types: [file, tool]
message: Nostr tests should inject EVENT/EOSE/OK/CLOSED callbacks rather than sleeping for delivery.
conditions:
  - field: content
    regex: 'time\.Sleep|asyncio\.sleep|setTimeout'
---
Warns on tests that use sleeps as event-delivery synchronization.
