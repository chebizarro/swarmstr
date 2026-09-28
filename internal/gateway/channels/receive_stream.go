package channels

import (
	"context"
	"log"
	"time"
)

// receiveStreamPollAfter is the number of consecutive failed redials after
// which an explicitly enabled polling fallback replaces a stream that was
// previously connected.
const receiveStreamPollAfter = 10

// SuperviseReceiveStream keeps an extension's event-driven receive stream alive
// until ctx is cancelled. dial opens the stream; serve blocks reading it, owns
// closing it, and returns when it ends. Every failure is retried with the
// shared bounded channel reconnect backoff, so a channel never silently stops
// receiving. poll is the explicit allow_polling fallback (nil when disabled):
// it takes over if the first dial fails or after receiveStreamPollAfter
// consecutive redial failures.
func SuperviseReceiveStream[C any](
	ctx context.Context,
	label string,
	dial func(context.Context) (C, error),
	serve func(context.Context, C) error,
	poll func(context.Context),
) {
	backoff := channelReconnectInitialBackoff
	connected := false
	failures := 0
	for {
		conn, err := dial(ctx)
		if err == nil {
			log.Printf("%s: receive stream connected", label)
			connected, failures = true, 0
			start := time.Now()
			err = serve(ctx, conn)
			if time.Since(start) >= channelReconnectMaxBackoff {
				backoff = channelReconnectInitialBackoff
			}
		} else {
			failures++
		}
		if ctx.Err() != nil {
			return
		}
		if poll != nil && (!connected || failures >= receiveStreamPollAfter) {
			log.Printf("%s: receive stream unavailable (%v); using explicitly enabled polling fallback", label, err)
			poll(ctx)
			return
		}
		log.Printf("%s: receive stream ended (%v); reconnecting in %s", label, err, backoff)
		if !channelReconnectDelay(ctx, backoff) {
			return
		}
		backoff = nextChannelReconnectBackoff(backoff)
	}
}
