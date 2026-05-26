# Nostr-native Adoption Advantage Contracts

Metiq adoption features expose unsigned draft event contracts so runtime code can sign/publish through the existing Nostr stack.

| Contract | Kind | d tag | Purpose |
| --- | ---: | --- | --- |
| Team policies | 30382 | namespace | Relay-synced dynamic rule bundles |
| Trajectory audit summaries | 30383 | session id | Signed decentralized session audit summaries |
| Commitment sync | 30384 | commitment id | Private replaceable commitment lifecycle notes |
| Loom worker ads | 10100 | worker id | Model/capability discovery without duplicating provider catalog ownership |
| QA credential leases | 30385 | lease id | Cashu-backed test credit lease metadata |
| Node capabilities | 30386 | node id | Nostr-native node discovery and invoke capability metadata |
| Skill marketplace | 30387 | skill id | Skill metadata and content hash publication |

All publication code must validate event IDs/signatures on inbound events, handle OK/CLOSED/AUTH relay responses, and use scoped filters with `#t`, `#p`, `#d`, and `since` where appropriate.
