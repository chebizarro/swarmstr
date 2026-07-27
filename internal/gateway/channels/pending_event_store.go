// Package channels — pending_event_store.go provides durable cross-restart
// replay for inbound events whose dispatch has not yet confirmed delivery
// (swarmstr-qye5, 2lpe follow-up). The in-memory redispatch/seen-gating state is
// process-local; this store persists the still-unsettled events so a crash or
// restart mid-retry replays them instead of relying on the relay's replay
// window.
package channels

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	nostr "fiatjaf.com/nostr"
)

// PendingEventStore durably records inbound events that are in the retry
// lifecycle (added on first delivery failure, removed on confirmed delivery).
// It is safe for concurrent use and persists atomically on every mutation.
type PendingEventStore struct {
	mu    sync.Mutex
	path  string
	items map[string]json.RawMessage // event id -> serialized event
}

// NewPendingEventStore opens (creating parent dirs as needed) and loads the
// store at path. A corrupt/absent file yields an empty store.
func NewPendingEventStore(path string) (*PendingEventStore, error) {
	if path == "" {
		return nil, fmt.Errorf("pending event store: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("pending event store: mkdir: %w", err)
	}
	s := &PendingEventStore{path: path, items: map[string]json.RawMessage{}}
	s.load()
	return s, nil
}

// load reads the persisted map, tolerating an absent or corrupt file.
func (s *PendingEventStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil || len(data) == 0 {
		return
	}
	var items map[string]json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil || items == nil {
		return
	}
	s.items = items
}

func (s *PendingEventStore) persistLocked() error {
	data, err := json.Marshal(s.items)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Add records an unsettled event (idempotent by id).
func (s *PendingEventStore) Add(id string, ev nostr.Event) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("pending event store: marshal: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; ok {
		return nil
	}
	s.items[id] = raw
	return s.persistLocked()
}

// Remove clears an event once its dispatch has confirmed delivery.
func (s *PendingEventStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return nil
	}
	delete(s.items, id)
	return s.persistLocked()
}

// Pending returns the currently-unsettled events (corrupt entries skipped).
func (s *PendingEventStore) Pending() []nostr.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]nostr.Event, 0, len(s.items))
	for _, raw := range s.items {
		var ev nostr.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// Len reports the number of pending events.
func (s *PendingEventStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// SanitizeChannelPathSegment turns a channel id (e.g. "relay.example'group")
// into a filesystem-safe path segment.
func SanitizeChannelPathSegment(id string) string {
	out := make([]rune, 0, len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "channel"
	}
	return string(out)
}
