package sessioncoord

import (
	"context"
	"fmt"
	"strings"
)

// Discussion states (OpenClaw gateway-protocol parity).
const (
	DiscussionNone      = "none"
	DiscussionAvailable = "available"
	DiscussionOpen      = "open"
)

// DiscussionState is the session.discussion.info/open result body.
type DiscussionState struct {
	State    string `json:"state"`
	EmbedURL string `json:"embedUrl,omitempty"`
	OpenURL  string `json:"openUrl,omitempty"`
}

// DiscussionProvider supplies the discussion surface for sessions. An absent
// provider means "none"; a failing provider is a transient error, never
// "none", so clients do not cache-hide the feature.
type DiscussionProvider interface {
	Info(ctx context.Context, sessionKey string) (DiscussionState, error)
	Open(ctx context.Context, sessionKey string) (DiscussionState, error)
}

// SetDiscussionProvider installs the discussion provider contract.
func (s *Service) SetDiscussionProvider(provider DiscussionProvider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.discussion = provider
	s.mu.Unlock()
}

func validateDiscussionState(state DiscussionState) (DiscussionState, error) {
	switch state.State {
	case DiscussionNone, DiscussionAvailable, DiscussionOpen:
		return state, nil
	}
	return DiscussionState{}, fmt.Errorf("invalid session discussion state %q", state.State)
}

// DiscussionInfo reports the discussion surface for one session.
func (s *Service) DiscussionInfo(ctx context.Context, key string) (DiscussionState, error) {
	key = strings.TrimSpace(key)
	if s == nil || s.repo == nil {
		return DiscussionState{}, fmt.Errorf("session discussion service unavailable")
	}
	if key == "" {
		return DiscussionState{}, fmt.Errorf("sessionKey is required")
	}
	s.mu.Lock()
	provider := s.discussion
	err := s.requireSessionLocked(ctx, key)
	s.mu.Unlock()
	if err != nil {
		return DiscussionState{}, err
	}
	if provider == nil {
		return DiscussionState{State: DiscussionNone}, nil
	}
	state, err := provider.Info(ctx, key)
	if err != nil {
		return DiscussionState{}, fmt.Errorf("session discussion provider failed: %w", err)
	}
	return validateDiscussionState(state)
}

// DiscussionOpen opens (or reports) the discussion surface for one session.
// It is a session mutation: the collaboration visibility matrix applies.
func (s *Service) DiscussionOpen(ctx context.Context, key string, actor Actor) (DiscussionState, error) {
	key = strings.TrimSpace(key)
	if s == nil || s.repo == nil {
		return DiscussionState{}, fmt.Errorf("session discussion service unavailable")
	}
	if key == "" {
		return DiscussionState{}, fmt.Errorf("sessionKey is required")
	}
	s.mu.Lock()
	provider := s.discussion
	err := s.requireSessionLocked(ctx, key)
	s.mu.Unlock()
	if err != nil {
		return DiscussionState{}, err
	}
	if err := s.AuthorizeSessionMutation(ctx, key, actor); err != nil {
		return DiscussionState{}, err
	}
	if provider == nil {
		return DiscussionState{State: DiscussionNone}, nil
	}
	state, err := provider.Open(ctx, key)
	if err != nil {
		return DiscussionState{}, fmt.Errorf("session discussion provider failed: %w", err)
	}
	state, err = validateDiscussionState(state)
	if err != nil {
		return DiscussionState{}, err
	}
	s.mu.Lock()
	s.emitLocked("sessions.changed", map[string]any{"reason": "discussion", "sessionKey": key})
	s.mu.Unlock()
	return state, nil
}
