package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"fiatjaf.com/nostr"
	"github.com/gorilla/websocket"
)

type localNostrRelay struct {
	t      *testing.T
	server *httptest.Server

	mu     sync.RWMutex
	events []nostr.Event
}

func newLocalNostrRelay(t *testing.T) *localNostrRelay {
	t.Helper()
	r := &localNostrRelay{t: t}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := r.handleMessage(conn, payload); err != nil {
				_ = conn.WriteJSON([]any{"NOTICE", err.Error()})
			}
		}
	}))
	return r
}

func (r *localNostrRelay) Close() {
	if r != nil && r.server != nil {
		r.server.Close()
	}
}

func (r *localNostrRelay) URL() string {
	if r == nil || r.server == nil {
		return ""
	}
	return "ws" + strings.TrimPrefix(r.server.URL, "http")
}

func (r *localNostrRelay) handleMessage(conn *websocket.Conn, payload []byte) error {
	var frame []json.RawMessage
	if err := json.Unmarshal(payload, &frame); err != nil {
		return err
	}
	if len(frame) == 0 {
		return nil
	}
	var kind string
	if err := json.Unmarshal(frame[0], &kind); err != nil {
		return err
	}
	switch kind {
	case "EVENT":
		if len(frame) < 2 {
			return nil
		}
		var evt nostr.Event
		if err := json.Unmarshal(frame[1], &evt); err != nil {
			return err
		}
		if !evt.CheckID() {
			return conn.WriteJSON([]any{"OK", evt.ID.Hex(), false, "invalid event id"})
		}
		if !evt.VerifySignature() {
			return conn.WriteJSON([]any{"OK", evt.ID.Hex(), false, "invalid signature"})
		}
		if !r.storeEvent(evt) {
			return conn.WriteJSON([]any{"OK", evt.ID.Hex(), false, "duplicate event"})
		}
		return conn.WriteJSON([]any{"OK", evt.ID.Hex(), true, ""})
	case "REQ":
		if len(frame) < 3 {
			return nil
		}
		var subID string
		if err := json.Unmarshal(frame[1], &subID); err != nil {
			return err
		}
		filters := make([]nostr.Filter, 0, len(frame)-2)
		for _, raw := range frame[2:] {
			var f nostr.Filter
			if err := json.Unmarshal(raw, &f); err != nil {
				return err
			}
			filters = append(filters, f)
		}
		for _, evt := range r.matchingEvents(filters) {
			if err := conn.WriteJSON([]any{"EVENT", subID, evt}); err != nil {
				return err
			}
		}
		return conn.WriteJSON([]any{"EOSE", subID})
	case "CLOSE":
		return nil
	default:
		return nil
	}
}

func (r *localNostrRelay) storeEvent(evt nostr.Event) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.events {
		if existing.ID == evt.ID {
			return false
		}
	}
	r.events = append(r.events, evt)
	return true
}

func (r *localNostrRelay) matchingEvents(filters []nostr.Filter) []nostr.Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(filters) == 0 {
		return nil
	}
	matches := make([]nostr.Event, 0)
	seen := make(map[string]struct{})
	for _, filter := range filters {
		count := 0
		for i := len(r.events) - 1; i >= 0; i-- {
			evt := r.events[i]
			if !filter.Matches(evt) {
				continue
			}
			id := evt.ID.Hex()
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			matches = append(matches, evt)
			count++
			if filter.Limit > 0 && count >= filter.Limit {
				break
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].CreatedAt == matches[j].CreatedAt {
			return matches[i].ID.Hex() > matches[j].ID.Hex()
		}
		return matches[i].CreatedAt > matches[j].CreatedAt
	})
	return matches
}
