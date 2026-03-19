package runtime

import (
	"context"

	nostr "fiatjaf.com/nostr"
)

// PoolOptsNIP42 returns PoolOptions with full NIP-42 authentication wired to
// the given keyer.  Both the reactive AuthHandler (signs AUTH challenge events
// sent by relays) and the pool-level AuthRequiredHandler (retries after
// "auth-required:" CLOSED/OK responses) are set.
//
// If keyer is nil the returned options enable PenaltyBox only.
func PoolOptsNIP42(keyer nostr.Keyer) nostr.PoolOptions {
	if keyer == nil {
		return nostr.PoolOptions{PenaltyBox: true}
	}
	return nostr.PoolOptions{
		PenaltyBox: true,
		AuthRequiredHandler: func(ctx context.Context, evt *nostr.Event) error {
			return keyer.SignEvent(ctx, evt)
		},
		RelayOptions: nostr.RelayOptions{
			AuthHandler: func(ctx context.Context, r *nostr.Relay, evt *nostr.Event) error {
				return keyer.SignEvent(ctx, evt)
			},
		},
	}
}

// NewPoolNIP42 creates a new Pool with full NIP-42 auth support wired to keyer.
func NewPoolNIP42(keyer nostr.Keyer) *nostr.Pool {
	return nostr.NewPool(PoolOptsNIP42(keyer))
}
