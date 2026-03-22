// Package runtime – DMTransport interface.
//
// DMTransport is the common interface implemented by both DMBus (NIP-04) and
// NIP17Bus (NIP-17).  Code that sends or receives DMs depends only on this
// interface, allowing callers to swap protocols without changing business logic.
package runtime

import "context"

// DMTransport abstracts over NIP-04 and NIP-17 DM transports.
type DMTransport interface {
	// SendDM sends a plain-text DM to the given hex or npub public key.
	SendDM(ctx context.Context, toPubKey string, text string) error

	// PublicKey returns the agent's public key in hex.
	PublicKey() string

	// Relays returns the currently active relay list.
	Relays() []string

	// SetRelays replaces the relay list at runtime.
	SetRelays(relays []string) error

	// Close shuts down the transport and waits for in-flight work to finish.
	Close()
}

// DMSchemeTransport is an optional extension for callers that want to request
// a specific DM encryption mode at send-time.
// Supported scheme names: auto, nip17, nip44, giftwrap, nip04.
type DMSchemeTransport interface {
	SendDMWithScheme(ctx context.Context, toPubKey string, text string, scheme string) error
}

// SubHealthReporter is an optional interface for transports that expose
// subscription health snapshots.
type SubHealthReporter interface {
	HealthSnapshot() SubHealthSnapshot
}

// Compile-time checks: both concrete types must satisfy the interface.
var _ DMTransport = (*DMBus)(nil)
var _ DMTransport = (*NIP17Bus)(nil)
var _ SubHealthReporter = (*DMBus)(nil)
var _ SubHealthReporter = (*NIP17Bus)(nil)
var _ DMSchemeTransport = (*DMBus)(nil)
var _ DMSchemeTransport = (*NIP17Bus)(nil)
