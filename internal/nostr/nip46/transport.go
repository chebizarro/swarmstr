package nip46

import (
	"context"
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
)

// Subscription is a long-lived event stream. Closed reports relay CLOSED
// messages without treating EOSE as completion; callers keep listening for
// realtime events until the context is canceled.
type Subscription struct {
	Events <-chan nostr.Event
	Closed <-chan error
}

// Transport is the event-driven seam used by Client and deterministic tests.
type Transport interface {
	Subscribe(context.Context, []string, nostr.Filter) Subscription
	Publish(context.Context, []string, nostr.Event) error
}

// PoolTransport adapts a nostr.Pool and verifies relay OK results.
type PoolTransport struct{ Pool *nostr.Pool }

func NewPoolTransport(pool *nostr.Pool, clientKey nostr.SecretKey) *PoolTransport {
	if pool == nil {
		pool = nostr.NewPool()
		pool.StartPenaltyBox()
		pool.AuthRequiredHandler = func(_ context.Context, event *nostr.Event) error { return event.Sign(clientKey) }
		pool.RelayOptions.AuthHandler = func(_ context.Context, _ *nostr.Relay, event *nostr.Event) error { return event.Sign(clientKey) }
	}
	return &PoolTransport{Pool: pool}
}

func (t *PoolTransport) Subscribe(ctx context.Context, relays []string, filter nostr.Filter) Subscription {
	out := make(chan nostr.Event)
	errs := make(chan error, len(relays))
	events, closed := t.Pool.SubscribeManyNotifyClosed(ctx, relays, filter, nostr.SubscriptionOptions{Label: "nip46-client"})
	go func() {
		defer close(out)
		defer close(errs)
		for events != nil || closed != nil {
			select {
			case re, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				select {
				case out <- re.Event:
				case <-ctx.Done():
					return
				}
			case rc, ok := <-closed:
				if !ok {
					closed = nil
					continue
				}
				if rc.HandledAuth {
					continue
				}
				relay := "relay"
				if rc.Relay != nil {
					relay = rc.Relay.URL
				}
				select {
				case errs <- fmt.Errorf("NIP-46 subscription CLOSED by %s: %s", relay, rc.Reason):
				default:
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return Subscription{Events: out, Closed: errs}
}

func (t *PoolTransport) Publish(ctx context.Context, relays []string, event nostr.Event) error {
	if len(relays) == 0 {
		return fmt.Errorf("NIP-46 publish requires a relay")
	}
	var failures []string
	accepted := false
	for result := range t.Pool.PublishMany(ctx, relays, event) {
		if result.Error == nil {
			accepted = true
			continue
		}
		failures = append(failures, result.RelayURL+": "+result.Error.Error())
	}
	if accepted {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(failures) == 0 {
		return fmt.Errorf("NIP-46 publish returned no relay OK responses")
	}
	return fmt.Errorf("NIP-46 publish rejected: %s", strings.Join(failures, "; "))
}
