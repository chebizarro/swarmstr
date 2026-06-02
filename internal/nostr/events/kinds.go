package events

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
	CAS_AUDIT Kind = 4903

	// CAS_WORKER_AD is the canonical worker advertisement kind.
	// Replaceable (one per pubkey). Refreshes every 60-300 seconds.
	CAS_WORKER_AD Kind = 10100

	// CAS_AGENT_HEARTBEAT is the canonical agent lifecycle heartbeat kind.
	// Addressable (d=<agent_id>). Refreshes every 30-120 seconds.
	CAS_AGENT_HEARTBEAT Kind = 30316

	// CAS_AGENT_CAPABILITY is the canonical agent capability descriptor kind.
	// Addressable (d=<agent_id>:<capability_name>).
	CAS_AGENT_CAPABILITY Kind = 30317

	// CAS_CP_STATE is the canonical control-plane state projection kind.
	// Addressable (d=<domain>:<entity>:<id>). Latest-wins semantics.
	CAS_CP_STATE Kind = 30900

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
	KindContextVM Kind = 25910

	// KindContextVMServerAnnouncement is the server capability announcement.
	KindContextVMServerAnnouncement Kind = 11316

	// KindContextVMToolsList is the tools list announcement.
	KindContextVMToolsList Kind = 11317

	// KindContextVMResourcesList is the resources list announcement.
	KindContextVMResourcesList Kind = 11318

	// KindContextVMResourceTemplatesList is the resource templates list announcement.
	KindContextVMResourceTemplatesList Kind = 11319

	// KindContextVMPromptsList is the prompts list announcement.
	KindContextVMPromptsList Kind = 11320

	// ─────────────────────────────────────────────────────────────────────────
	// NIP-60 Cashu Wallet Event Kinds
	// ─────────────────────────────────────────────────────────────────────────

	KindNIP60UnspentToken Kind = 7375  // encrypted unspent token bundle
	KindNIP60TokenHistory Kind = 7376  // encrypted token history entry
	KindNIP60Wallet       Kind = 37375 // encrypted wallet metadata (parameterized-replaceable)

	// ─────────────────────────────────────────────────────────────────────────
	// NIP-61 Nutzap Event Kinds
	// ─────────────────────────────────────────────────────────────────────────

	KindNIP61NutzapInfo Kind = 10019 // replaceable: advertise supported mints + P2PK pubkey
	KindNIP61Nutzap     Kind = 9321  // nutzap: send Cashu proofs to a recipient

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

	// ─────────────────────────────────────────────────────────────────────────
	// Legacy/Deprecated Kinds
	// These are preserved for backward compatibility during migration.
	// New code should use canonical constants above.
	// ─────────────────────────────────────────────────────────────────────────

	// KindTask is DEPRECATED. Conflicts with NIP-69.
	// Migrate to ContextVM Intent Layer (KindContextVM).
	KindTask Kind = 38383

	// KindControl is DEPRECATED.
	// Migrate to ContextVM Intent Layer (KindContextVM).
	KindControl Kind = 38384

	// KindMCPCall is DEPRECATED.
	// Migrate to ContextVM (KindContextVM with tools/call method).
	KindMCPCall Kind = 38385

	// KindMCPResult is DEPRECATED.
	// Migrate to ContextVM responses or NIP-90 DVM results.
	KindMCPResult Kind = 38386

	// KindLogStatus is DEPRECATED. Use KindNIP38Status instead.
	KindLogStatus Kind = KindNIP38Status

	// KindLifecycle is DEPRECATED. Use CAS_AGENT_HEARTBEAT instead.
	KindLifecycle Kind = CAS_AGENT_HEARTBEAT

	// KindCapability is DEPRECATED. Use CAS_AGENT_CAPABILITY instead.
	KindCapability Kind = CAS_AGENT_CAPABILITY

	// KindStateDoc is DEPRECATED. Use KindAppData instead.
	KindStateDoc Kind = KindAppData

	// KindTranscriptDoc is DEPRECATED. Use KindAppData with type:transcript.
	KindTranscriptDoc Kind = 30079

	// KindMemoryDoc is DEPRECATED. Use KindAppData with type:memory.
	KindMemoryDoc Kind = 30080
)
