package events

// Kind represents a Nostr event kind number.
type Kind int

const (
	// Standard Nostr kinds.
	KindDMNIP04 Kind = 4
	KindDMNIP44 Kind = 44

	// AI-Hub-derived operational kinds.
	KindTask       Kind = 38383
	KindControl    Kind = 38384
	KindMCPCall    Kind = 38385
	KindMCPResult  Kind = 38386
	KindLogStatus  Kind = 30315
	KindLifecycle  Kind = 30316
	KindCapability Kind = 30317

	// Swarmstr application state kinds.
	// We use parameterized-replaceable addressing via `d` tag.
	KindStateDoc      Kind = 30078
	KindTranscriptDoc Kind = 30079
	KindMemoryDoc     Kind = 30080
)
