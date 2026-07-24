package board

import (
	"bytes"
	"encoding/json"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// EventMaxBytes bounds a serialized board.event payload.
	EventMaxBytes = 8 * 1024
	// noticeMaxChars bounds the rendered notice line.
	noticeMaxChars = 500
	// eventDedupeWindow suppresses identical payload repeats per widget.
	eventDedupeWindow = 5 * time.Second
)

type noticeRecord struct {
	summary string
	at      time.Time
}

// NoticeDeduper renders and dedupes board.event notices. Widgets emit events
// aggressively (e.g. on every interaction); identical payloads within the
// dedupe window are dropped so the agent is not flooded.
type NoticeDeduper struct {
	mu     sync.Mutex
	recent map[string]noticeRecord
}

// NewNoticeDeduper returns an empty deduper.
func NewNoticeDeduper() *NoticeDeduper {
	return &NoticeDeduper{recent: map[string]noticeRecord{}}
}

// FormatNotice renders the OpenClaw-compatible notice line, clipping the
// summary so prefix and suffix always survive.
func FormatNotice(widget, summary string) string {
	prefix := "[dashboard] "
	suffix := " on widget " + widget
	available := noticeMaxChars - len(prefix) - len(suffix)
	if available < 0 {
		available = 0
	}
	clipped := summary
	if len(clipped) > available {
		limit := available - 1
		if limit < 0 {
			limit = 0
		}
		for limit > 0 && !utf8.RuneStart(clipped[limit]) {
			limit--
		}
		clipped = clipped[:limit] + "…"
	}
	return prefix + clipped + suffix
}

// Render validates and compacts payload, applies the dedupe window, and
// returns the formatted notice. ok=false means the event was deduped and no
// notice should be delivered.
func (d *NoticeDeduper) Render(sessionKey, widget string, payload json.RawMessage, now time.Time) (notice string, ok bool, err error) {
	summaryBuf := &bytes.Buffer{}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		trimmed = []byte("null")
	}
	if compactErr := json.Compact(summaryBuf, trimmed); compactErr != nil {
		return "", false, errInvalid("board event payload must be JSON serializable")
	}
	summary := summaryBuf.String()
	if len(summary) > EventMaxBytes {
		return "", false, errInvalid("board event payload exceeds %d bytes", EventMaxBytes)
	}
	key := sessionKey + "\x00" + widget
	d.mu.Lock()
	defer d.mu.Unlock()
	if recent, exists := d.recent[key]; exists && recent.summary == summary && now.Sub(recent.at) < eventDedupeWindow {
		return "", false, nil
	}
	d.recent[key] = noticeRecord{summary: summary, at: now}
	for candidate, record := range d.recent {
		if now.Sub(record.at) >= eventDedupeWindow {
			delete(d.recent, candidate)
		}
	}
	return FormatNotice(widget, summary), true, nil
}
