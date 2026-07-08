package runtime

import (
	"context"

	nostr "fiatjaf.com/nostr"
)

// NewPoolNIP42 creates a new Pool with full NIP-42 auth support wired to keyer.
func NewPoolNIP42(keyer nostr.Keyer) *nostr.Pool {
	pool := nostr.NewPool()
	pool.StartPenaltyBox()
	if keyer != nil {
		pool.AuthRequiredHandler = func(ctx context.Context, evt *nostr.Event) error {
			return keyer.SignEvent(ctx, evt)
		}
		pool.RelayOptions.AuthHandler = func(ctx context.Context, _ *nostr.Relay, evt *nostr.Event) error {
			return keyer.SignEvent(ctx, evt)
		}
	}
	return pool
}
