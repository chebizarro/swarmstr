package channels

import (
	"strings"
	"sync"
	"time"
)

// Debouncer coalesces rapid messages from the same key (e.g. channelID:senderID)
// within a configurable window before delivering them as a single combined message.
//
// When Submit is called:
//   - If no pending timer exists for the key, one is started.
//   - If a timer already exists, it is reset (extended by the window duration).
//   - When the timer fires, all buffered texts for that key are joined and delivered
//     to the flush function.
//
// This mirrors the per-channel debounce queue found in OpenClaw, which prevents
// duplicate or fragmented agent responses when a user types quickly.
type Debouncer struct {
	mu      sync.Mutex
	timers  map[string]*time.Timer
	buffers map[string][]string
	delay   time.Duration
	flush   func(key string, messages []string)
}

// NewDebouncer creates a Debouncer with the given window duration.
// flush is called with the debounce key and all buffered messages once the
// window expires without a new submission.
func NewDebouncer(delay time.Duration, flush func(key string, messages []string)) *Debouncer {
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}
	return &Debouncer{
		timers:  make(map[string]*time.Timer),
		buffers: make(map[string][]string),
		delay:   delay,
		flush:   flush,
	}
}

// Submit queues text for the given key, resetting the debounce window.
func (d *Debouncer) Submit(key, text string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.buffers[key] = append(d.buffers[key], text)

	if t, ok := d.timers[key]; ok {
		t.Reset(d.delay)
		return
	}

	d.timers[key] = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		msgs := d.buffers[key]
		delete(d.buffers, key)
		delete(d.timers, key)
		d.mu.Unlock()

		if len(msgs) > 0 {
			d.flush(key, msgs)
		}
	})
}

// Flush immediately fires any pending timer for key, delivering buffered messages
// without waiting for the window to expire.  No-op if no messages are pending.
func (d *Debouncer) Flush(key string) {
	d.mu.Lock()
	t, hasTimer := d.timers[key]
	msgs := d.buffers[key]
	delete(d.buffers, key)
	delete(d.timers, key)
	d.mu.Unlock()

	if hasTimer {
		t.Stop()
	}
	if len(msgs) > 0 {
		d.flush(key, msgs)
	}
}

// FlushAll immediately fires all pending timers.  Useful on graceful shutdown.
func (d *Debouncer) FlushAll() {
	d.mu.Lock()
	keys := make([]string, 0, len(d.timers))
	for k := range d.timers {
		keys = append(keys, k)
	}
	d.mu.Unlock()

	for _, k := range keys {
		d.Flush(k)
	}
}

// DebounceKey returns a canonical debounce key for a channel+sender pair.
func DebounceKey(channelID, senderID string) string {
	return channelID + ":" + senderID
}

// JoinMessages joins multiple debounced messages with a newline separator,
// treating them as a single user turn for the agent.
func JoinMessages(msgs []string) string {
	return strings.Join(msgs, "\n")
}
