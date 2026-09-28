package channels

import (
	"context"
	"errors"
	"testing"
	"time"
)

func instantReconnectDelay(t *testing.T) {
	orig := channelReconnectDelay
	channelReconnectDelay = func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }
	t.Cleanup(func() { channelReconnectDelay = orig })
}

// Without polling, the supervisor must keep redialing after the initial dial
// fails and after the stream ends, instead of giving up.
func TestSuperviseReceiveStreamRetriesUntilCancelled(t *testing.T) {
	instantReconnectDelay(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dials, serves := 0, 0
	dial := func(context.Context) (int, error) {
		dials++
		if dials <= 2*receiveStreamPollAfter {
			return 0, errors.New("down")
		}
		return dials, nil
	}
	serve := func(context.Context, int) error {
		serves++
		if serves == 3 {
			cancel()
		}
		return errors.New("stream ended")
	}
	SuperviseReceiveStream(ctx, "test", dial, serve, nil)
	if serves != 3 {
		t.Fatalf("serves = %d, want 3 (dials=%d)", serves, dials)
	}
}

// With explicit polling, the fallback takes over when the stream is
// unavailable at startup, or after repeated redial failures once connected.
func TestSuperviseReceiveStreamPollingFallback(t *testing.T) {
	instantReconnectDelay(t)
	polled := 0
	poll := func(context.Context) { polled++ }

	SuperviseReceiveStream(context.Background(), "test",
		func(context.Context) (int, error) { return 0, errors.New("down") },
		func(context.Context, int) error { return nil }, poll)
	if polled != 1 {
		t.Fatalf("initial dial failure: polled = %d, want 1", polled)
	}

	dials := 0
	SuperviseReceiveStream(context.Background(), "test",
		func(context.Context) (int, error) {
			dials++
			if dials == 1 {
				return 1, nil
			}
			return 0, errors.New("down")
		},
		func(context.Context, int) error { return errors.New("stream ended") }, poll)
	if polled != 2 || dials != 1+receiveStreamPollAfter {
		t.Fatalf("after connect: polled = %d dials = %d, want 2 and %d", polled, dials, 1+receiveStreamPollAfter)
	}
}
