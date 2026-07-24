package sessioncoord

import (
	"context"
	"strings"
	"time"
)

const (
	typingThrottle  = time.Second
	typingActiveTTL = 2500 * time.Millisecond
	maxTypingKeys   = 2048
)

// typingEntry tracks one identity's live typing state for one session across
// all of that identity's connections.
type typingEntry struct {
	subject         string
	sessionKey      string
	sessionID       string
	agentID         string
	connections     map[string]int64 // connection id -> last typing=true update (unix ms)
	lastBroadcastAt int64
	lastBroadcast   bool
	everBroadcast   bool
}

// TypingRequest describes one session.typing method call.
type TypingRequest struct {
	Key          string
	SessionID    string
	ConnectionID string
	Typing       bool
}

func typingKey(subject, sessionKey string) string {
	return subject + "\x00" + sessionKey
}

func (e *typingEntry) effectiveTyping(nowMS int64) bool {
	for connectionID, updatedAt := range e.connections {
		if nowMS-updatedAt >= typingActiveTTL.Milliseconds() {
			delete(e.connections, connectionID)
		}
	}
	return len(e.connections) > 0
}

// UpdateTyping applies one connection-scoped typing signal and reports
// whether a session.typing event was broadcast. Multi-connection identities
// stay "typing" while any live connection is typing; broadcasts are throttled
// per identity/session and always fire on state transitions.
func (s *Service) UpdateTyping(ctx context.Context, req TypingRequest, actor Actor) (bool, error) {
	req.Key = strings.TrimSpace(req.Key)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ConnectionID = strings.TrimSpace(req.ConnectionID)
	if s == nil || s.repo == nil {
		return false, nil
	}
	if req.Key == "" || req.SessionID == "" {
		return false, errTypingParams
	}
	subject := strings.TrimSpace(actor.Subject)
	if subject == "" || req.ConnectionID == "" {
		// Identity-less or non-WS callers never broadcast typing.
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSessionLocked(ctx, req.Key); err != nil {
		return false, err
	}
	// Stale session generation: accept and drop silently, matching OpenClaw.
	if current := s.currentSessionIDLocked(req.Key); current != "" && current != req.SessionID {
		return false, nil
	}
	sharing, err := s.sharingDocLocked(ctx, req.Key)
	if err != nil {
		return false, err
	}
	role := s.resolveRoleLocked(ctx, req.Key, sharing, actor)
	visibility := normalizeVisibility(sharing.Visibility)
	if visibility == VisibilityDraft && !canManageSharing(role) {
		return false, nil
	}
	if role == RoleViewer && visibility != VisibilityShared && visibility != VisibilitySuggest {
		return false, nil
	}
	nowMS := s.now().UnixMilli()
	key := typingKey(subject, req.Key)
	if s.typingState == nil {
		s.typingState = map[string]*typingEntry{}
	}
	entry := s.typingState[key]
	if entry == nil {
		if len(s.typingState) >= maxTypingKeys {
			return false, nil
		}
		entry = &typingEntry{subject: subject, sessionKey: req.Key, connections: map[string]int64{}}
		s.typingState[key] = entry
	}
	entry.sessionID = req.SessionID
	entry.agentID = s.sessionAgentIDLocked(ctx, req.Key)
	if req.Typing {
		entry.connections[req.ConnectionID] = nowMS
	} else {
		delete(entry.connections, req.ConnectionID)
	}
	effective := entry.effectiveTyping(nowMS)
	broadcast := false
	if !entry.everBroadcast {
		broadcast = effective
	} else if effective != entry.lastBroadcast {
		broadcast = true
	} else if effective && nowMS-entry.lastBroadcastAt >= typingThrottle.Milliseconds() {
		broadcast = true
	}
	if broadcast {
		entry.everBroadcast = true
		entry.lastBroadcast = effective
		entry.lastBroadcastAt = nowMS
		s.emitTypingLocked(entry, effective, nowMS)
	}
	if !effective {
		delete(s.typingState, key)
	}
	return broadcast, nil
}

func (s *Service) emitTypingLocked(entry *typingEntry, typing bool, nowMS int64) {
	s.emitLocked(EventSessionTyping, SessionTypingEventPayload{
		SessionKey: entry.sessionKey,
		SessionID:  entry.sessionID,
		AgentID:    entry.agentID,
		Actor:      Identity{Type: "human", ID: entry.subject},
		Typing:     typing,
		TS:         nowMS,
	})
}

// currentSessionIDLocked resolves the durable session id for a key; the local
// store row wins, else the key itself is the session id.
func (s *Service) currentSessionIDLocked(key string) string {
	if s.store != nil {
		if entry, ok := s.store.Get(key); ok && strings.TrimSpace(entry.SessionID) != "" {
			return strings.TrimSpace(entry.SessionID)
		}
	}
	return key
}

// dropTypingConnectionLocked removes one connection from every typing entry
// and emits typing=false for identities that fall silent as a result.
func (s *Service) dropTypingConnectionLocked(connectionID string) {
	nowMS := s.now().UnixMilli()
	for key, entry := range s.typingState {
		if _, ok := entry.connections[connectionID]; !ok {
			continue
		}
		delete(entry.connections, connectionID)
		if !entry.effectiveTyping(nowMS) {
			if entry.everBroadcast && entry.lastBroadcast {
				s.emitTypingLocked(entry, false, nowMS)
			}
			delete(s.typingState, key)
		}
	}
}
