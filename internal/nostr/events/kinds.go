package events

import cascadia "git.sharegap.net/cascadia/cascadia-nips/generated/go"

// Kind represents a Nostr event kind number.
type Kind int

const (
	// ─────────────────────────────────────────────────────────────────────────
	// Standard NIP Kinds
	// ─────────────────────────────────────────────────────────────────────────

	KindDMNIP04  Kind = 4
	KindSeal     Kind = 13
	KindDMNIP44  Kind = 44
	KindGiftWrap Kind = 1059

	// ─────────────────────────────────────────────────────────────────────────
	// Cascadia Canonical Kinds (CAS_*)
	// Reference: cascadia-nips/docs/nostr-native-event-strategy.md
	// ─────────────────────────────────────────────────────────────────────────

	// CAS_AUDIT is the canonical audit trail / attestation kind.
	// Append-only, immutable. Relays SHOULD enforce extended retention.
	CAS_AUDIT Kind = Kind(cascadia.CAS_AUDIT)

	// CAS_WORKER_AD is the canonical worker advertisement kind.
	// Replaceable (one per pubkey). Refreshes every 60-300 seconds.
	CAS_WORKER_AD Kind = Kind(cascadia.CAS_WORKER_AD)

	// CAS_AGENT_HEARTBEAT is the canonical agent lifecycle heartbeat kind.
	// Addressable (d=<agent_id>). Refreshes every 30-120 seconds.
	CAS_AGENT_HEARTBEAT Kind = Kind(cascadia.CAS_AGENT_HEARTBEAT)

	// CAS_AGENT_CAPABILITY is the canonical agent capability descriptor kind.
	// Addressable (d=<agent_id>:<capability_name>).
	CAS_AGENT_CAPABILITY Kind = Kind(cascadia.CAS_AGENT_CAPABILITY)

	// CAS_CP_STATE is the canonical control-plane state projection kind.
	// Addressable (d=<domain>:<entity>:<id>). Latest-wins semantics.
	CAS_CP_STATE Kind = Kind(cascadia.CAS_CP_STATE)

	// ─────────────────────────────────────────────────────────────────────────
	// NIP-38 User Status
	// ─────────────────────────────────────────────────────────────────────────

	// KindNIP38Status is the NIP-38 user/entity status kind.
	// Addressable with d-tag for status category.
	KindNIP38Status Kind = 30315

	// ─────────────────────────────────────────────────────────────────────────
	// NIP-78 App-Specific Data
	// ─────────────────────────────────────────────────────────────────────────

	// KindAppData is the NIP-78 app-specific data kind.
	// Addressable. Used with type tag for discrimination.
	KindAppData Kind = 30078

	// ─────────────────────────────────────────────────────────────────────────
	// ContextVM Kinds
	// Reference: contextvm-docs/src/content/docs/spec/ctxvm-draft-spec.md
	// ─────────────────────────────────────────────────────────────────────────

	// KindContextVM is the ephemeral ContextVM message kind (JSON-RPC 2.0).
	KindContextVM Kind = Kind(cascadia.CAS_INTENT)

	// KindContextVMServerAnnouncement is the server capability announcement.
	KindContextVMServerAnnouncement Kind = Kind(cascadia.CTXVM_SERVER_ANNOUNCEMENT)

	// KindContextVMToolsList is the tools list announcement.
	KindContextVMToolsList Kind = Kind(cascadia.CTXVM_TOOLS_ANNOUNCEMENT)

	// KindContextVMResourcesList is the resources list announcement.
	KindContextVMResourcesList Kind = Kind(cascadia.CTXVM_RESOURCES_ANNOUNCEMENT)

	// KindContextVMResourceTemplatesList is the resource templates list announcement.
	KindContextVMResourceTemplatesList Kind = Kind(cascadia.CTXVM_RESOURCE_TEMPLATES_ANNOUNCEMENT)

	// KindContextVMPromptsList is the prompts list announcement.
	KindContextVMPromptsList Kind = Kind(cascadia.CTXVM_PROMPTS_ANNOUNCEMENT)

	// ─────────────────────────────────────────────────────────────────────────
	// NIP-60 Cashu Wallet Event Kinds
	// ─────────────────────────────────────────────────────────────────────────

	KindNIP60UnspentToken Kind = 7375  // encrypted unspent token bundle
	KindNIP60TokenHistory Kind = 7376  // encrypted token history entry
	KindNIP60Wallet       Kind = 17375 // encrypted wallet metadata (replaceable)

	// ─────────────────────────────────────────────────────────────────────────
	// NIP-61 Nutzap Event Kinds
	// ─────────────────────────────────────────────────────────────────────────

	KindNIP61NutzapInfo Kind = 10019 // replaceable: advertise supported mints + P2PK pubkey
	KindNIP61Nutzap     Kind = 9321  // nutzap: send Cashu proofs to a recipient

	// ─────────────────────────────────────────────────────────────────────────
	// NIP-58 Badge Event Kinds
	// ─────────────────────────────────────────────────────────────────────────

	KindNIP58BadgeAward      Kind = 8     // badge award
	KindNIP58ProfileBadges   Kind = 10008 // replaceable profile badges list
	KindNIP58BadgeSet        Kind = 30008 // addressable badge set
	KindNIP58BadgeDefinition Kind = 30009 // addressable badge definition

	// ─────────────────────────────────────────────────────────────────────────
	// NIP-34 Repository Collaboration Kinds
	// ─────────────────────────────────────────────────────────────────────────

	KindRepoAnnouncement Kind = 30617
	KindRepoState        Kind = 30618
	KindPatch            Kind = 1617
	KindPR               Kind = 1618
	KindPRUpdate         Kind = 1619
	KindIssue            Kind = 1621
	KindStatusOpen       Kind = 1630
	KindStatusApplied    Kind = 1631
	KindStatusClosed     Kind = 1632
	KindStatusDraft      Kind = 1633
)
