package channels

import (
	"context"
	"log"
	"sync"
	"time"
)

// TypingKeepalive sends periodic typing indicators to a channel while an
// agent turn is in progress. Most platforms expire typing indicators after
// ~5 s; the keepalive re-sends every intervalMS to maintain continuous
// feedback during long agent runs.
//
// Usage:
//
//	ka := NewTypingKeepalive(typingHandle.SendTyping, 3*time.Second, 60*time.Second, 2)
//	ka.Start(ctx)
//	defer ka.Stop()
type TypingKeepalive struct {
	send     func(ctx context.Context, durationMS int) error
	interval time.Duration
	maxTTL   time.Duration
	maxFails int

	once sync.Once
	done chan struct{}
	wg   sync.WaitGroup
}

// NewTypingKeepalive creates a keepalive for the given send function.
//
//   - send: the typing indicator API call (e.g. slackBot.SendTyping).
//   - interval: how often to call send (default 3 s for most platforms).
//   - maxTTL: safety auto-stop after this duration (default 60 s).
//   - maxConsecutiveFails: stop the loop after this many consecutive errors (default 2).
func NewTypingKeepalive(
	send func(ctx context.Context, durationMS int) error,
	interval, maxTTL time.Duration,
	maxConsecutiveFails int,
) *TypingKeepalive {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	if maxTTL <= 0 {
		maxTTL = 60 * time.Second
	}
	if maxConsecutiveFails <= 0 {
		maxConsecutiveFails = 2
	}
	return &TypingKeepalive{
		send:     send,
		interval: interval,
		maxTTL:   maxTTL,
		maxFails: maxConsecutiveFails,
		done:     make(chan struct{}),
	}
}

// Start launches the keepalive goroutine. Safe to call only once; subsequent
// calls are no-ops. The goroutine stops when Stop is called or ctx is cancelled.
func (k *TypingKeepalive) Start(ctx context.Context) {
	k.once.Do(func() {
		k.wg.Add(1)
		go k.loop(ctx)
	})
}

// Stop signals the keepalive goroutine to exit and waits for it to finish.
func (k *TypingKeepalive) Stop() {
	select {
	case <-k.done:
	default:
		close(k.done)
	}
	k.wg.Wait()
}

func (k *TypingKeepalive) loop(ctx context.Context) {
	defer k.wg.Done()

	ticker := time.NewTicker(k.interval)
	defer ticker.Stop()

	ttl := time.NewTimer(k.maxTTL)
	defer ttl.Stop()

	consecutiveFails := 0

	// Send immediately on start.
	if err := k.send(ctx, 0); err != nil {
		log.Printf("typing keepalive: initial send error: %v", err)
		consecutiveFails++
	}

	for {
		select {
		case <-k.done:
			return
		case <-ctx.Done():
			return
		case <-ttl.C:
			log.Printf("typing keepalive: TTL exceeded (%s), stopping", k.maxTTL)
			return
		case <-ticker.C:
			if err := k.send(ctx, 0); err != nil {
				consecutiveFails++
				log.Printf("typing keepalive: send error (%d/%d): %v", consecutiveFails, k.maxFails, err)
				if consecutiveFails >= k.maxFails {
					log.Printf("typing keepalive: circuit breaker tripped after %d consecutive failures", k.maxFails)
					return
				}
			} else {
				consecutiveFails = 0
			}
		}
	}
}
