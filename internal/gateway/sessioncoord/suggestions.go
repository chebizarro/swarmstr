package sessioncoord

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"metiq/internal/store/state"
)

// Suggestion states and resolutions (OpenClaw gateway-protocol parity).
const (
	SuggestionPending   = "pending"
	SuggestionAccepted  = "accepted"
	SuggestionDismissed = "dismissed"

	ResolutionSend    = "send"
	ResolutionQueue   = "queue"
	ResolutionEdit    = "edit"
	ResolutionDismiss = "dismiss"
)

const (
	maxSuggestionTextLen = 32768
	maxStoredSuggestions = 200
)

// ErrSuggestionBusy marks a suggestion whose resolution is already in flight.
var ErrSuggestionBusy = errors.New("suggestion resolution is already in progress")

func isValidResolution(resolution string) bool {
	switch resolution {
	case ResolutionSend, ResolutionQueue, ResolutionEdit, ResolutionDismiss:
		return true
	}
	return false
}

func resolutionState(resolution string) string {
	if resolution == ResolutionDismiss {
		return SuggestionDismissed
	}
	return SuggestionAccepted
}

func (s *Service) suggestionsDocLocked(ctx context.Context, key string) (state.SessionSuggestionsDoc, error) {
	if doc, ok := s.suggestionCache[key]; ok {
		return doc, nil
	}
	doc, err := s.repo.GetSessionSuggestions(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		doc = state.SessionSuggestionsDoc{Version: 1, SessionID: key}
		err = nil
	}
	if err != nil {
		return state.SessionSuggestionsDoc{}, err
	}
	if s.suggestionCache == nil {
		s.suggestionCache = map[string]state.SessionSuggestionsDoc{}
	}
	s.suggestionCache[key] = doc
	return doc, nil
}

func (s *Service) putSuggestionsDocLocked(ctx context.Context, key string, doc state.SessionSuggestionsDoc) error {
	doc.Version = 1
	doc.SessionID = key
	doc.UpdatedAtMS = s.now().UnixMilli()
	if _, err := s.repo.PutSessionSuggestions(ctx, key, doc); err != nil {
		return err
	}
	if s.suggestionCache == nil {
		s.suggestionCache = map[string]state.SessionSuggestionsDoc{}
	}
	s.suggestionCache[key] = doc
	return nil
}

func (s *Service) protocolSuggestionLocked(ctx context.Context, key string, entry state.SessionSuggestionEntry) Suggestion {
	return Suggestion{
		ID:         entry.ID,
		SessionKey: key,
		AgentID:    s.sessionAgentIDLocked(ctx, key),
		Author:     Identity{Type: "human", ID: entry.AuthorID, Label: entry.AuthorLabel},
		Text:       entry.Text,
		CreatedAt:  entry.CreatedAtMS,
		State:      entry.State,
	}
}

// AddSuggestion appends a pending suggestion authored by an identified actor
// to a session whose visibility is "suggest".
func (s *Service) AddSuggestion(ctx context.Context, key, text string, actor Actor) (Suggestion, error) {
	key = strings.TrimSpace(key)
	if s == nil || s.repo == nil {
		return Suggestion{}, fmt.Errorf("session suggestion service unavailable")
	}
	if key == "" {
		return Suggestion{}, fmt.Errorf("sessionKey is required")
	}
	if strings.TrimSpace(text) == "" {
		return Suggestion{}, fmt.Errorf("suggestion text is required")
	}
	if len(text) > maxSuggestionTextLen {
		return Suggestion{}, fmt.Errorf("suggestion text must not exceed %d characters", maxSuggestionTextLen)
	}
	subject := strings.TrimSpace(actor.Subject)
	if subject == "" {
		return Suggestion{}, fmt.Errorf("identified suggestion author required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSessionLocked(ctx, key); err != nil {
		return Suggestion{}, err
	}
	sharing, err := s.sharingDocLocked(ctx, key)
	if err != nil {
		return Suggestion{}, err
	}
	role := s.resolveRoleLocked(ctx, key, sharing, actor)
	visibility := normalizeVisibility(sharing.Visibility)
	if visibility == VisibilityDraft && !canManageSharing(role) {
		return Suggestion{}, fmt.Errorf("%w: session is draft for this connection", ErrForbidden)
	}
	if visibility != VisibilitySuggest {
		return Suggestion{}, fmt.Errorf("session is not accepting suggestions")
	}
	doc, err := s.suggestionsDocLocked(ctx, key)
	if err != nil {
		return Suggestion{}, err
	}
	if len(doc.Suggestions) >= maxStoredSuggestions {
		return Suggestion{}, fmt.Errorf("at most %d suggestions are retained per session", maxStoredSuggestions)
	}
	entry := state.SessionSuggestionEntry{
		ID:          uuid.NewString(),
		AuthorID:    subject,
		Text:        text,
		CreatedAtMS: s.now().UnixMilli(),
		State:       SuggestionPending,
	}
	doc.Suggestions = append(doc.Suggestions, entry)
	if err := s.putSuggestionsDocLocked(ctx, key, doc); err != nil {
		return Suggestion{}, err
	}
	projected := s.protocolSuggestionLocked(ctx, key, entry)
	s.emitLocked(EventSessionSuggestion, SessionSuggestionEventPayload{Action: "added", Suggestion: projected})
	return projected, nil
}

// ListSuggestions returns the caller-visible suggestion rows plus the caller
// role: viewers see only their own suggestions; managers and members see all
// pending rows plus their own resolved history.
func (s *Service) ListSuggestions(ctx context.Context, key string, actor Actor) (string, []Suggestion, error) {
	key = strings.TrimSpace(key)
	if s == nil || s.repo == nil {
		return "", nil, fmt.Errorf("session suggestion service unavailable")
	}
	if key == "" {
		return "", nil, fmt.Errorf("sessionKey is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireSessionLocked(ctx, key); err != nil {
		return "", nil, err
	}
	sharing, err := s.sharingDocLocked(ctx, key)
	if err != nil {
		return "", nil, err
	}
	role := s.resolveRoleLocked(ctx, key, sharing, actor)
	if normalizeVisibility(sharing.Visibility) == VisibilityDraft && !canManageSharing(role) {
		return "", nil, fmt.Errorf("%w: session is draft for this connection", ErrForbidden)
	}
	doc, err := s.suggestionsDocLocked(ctx, key)
	if err != nil {
		return "", nil, err
	}
	subject := strings.TrimSpace(actor.Subject)
	out := make([]Suggestion, 0, len(doc.Suggestions))
	for _, entry := range doc.Suggestions {
		own := subject != "" && entry.AuthorID == subject
		if role == RoleViewer {
			if !own {
				continue
			}
		} else if entry.State != SuggestionPending && !own {
			continue
		}
		out = append(out, s.protocolSuggestionLocked(ctx, key, entry))
	}
	return role, out, nil
}

// ResolveSuggestion finalizes one pending suggestion. Owner or admin role is
// required. For "send"/"queue" resolutions the dispatch callback runs outside
// the service mutex while the suggestion is claimed; a dispatch failure leaves
// the suggestion pending for retry.
func (s *Service) ResolveSuggestion(ctx context.Context, key, id, resolution string, actor Actor, dispatch func(Suggestion) error) (Suggestion, error) {
	key, id = strings.TrimSpace(key), strings.TrimSpace(id)
	if s == nil || s.repo == nil {
		return Suggestion{}, fmt.Errorf("session suggestion service unavailable")
	}
	if key == "" || id == "" {
		return Suggestion{}, fmt.Errorf("sessionKey and id are required")
	}
	if !isValidResolution(resolution) {
		return Suggestion{}, fmt.Errorf("invalid resolution %q", resolution)
	}
	claimKey := key + "\x00" + id

	s.mu.Lock()
	if err := s.requireSessionLocked(ctx, key); err != nil {
		s.mu.Unlock()
		return Suggestion{}, err
	}
	sharing, err := s.sharingDocLocked(ctx, key)
	if err != nil {
		s.mu.Unlock()
		return Suggestion{}, err
	}
	role := s.resolveRoleLocked(ctx, key, sharing, actor)
	if !canManageSharing(role) {
		s.mu.Unlock()
		return Suggestion{}, fmt.Errorf("%w: session owner or operator.admin required", ErrForbidden)
	}
	doc, err := s.suggestionsDocLocked(ctx, key)
	if err != nil {
		s.mu.Unlock()
		return Suggestion{}, err
	}
	index := -1
	for i, entry := range doc.Suggestions {
		if entry.ID == id {
			index = i
			break
		}
	}
	if index < 0 || doc.Suggestions[index].State != SuggestionPending {
		s.mu.Unlock()
		return Suggestion{}, fmt.Errorf("pending suggestion not found")
	}
	if s.suggestionInflight == nil {
		s.suggestionInflight = map[string]struct{}{}
	}
	if _, busy := s.suggestionInflight[claimKey]; busy {
		s.mu.Unlock()
		return Suggestion{}, ErrSuggestionBusy
	}
	s.suggestionInflight[claimKey] = struct{}{}
	claimed := doc.Suggestions[index]
	projected := s.protocolSuggestionLocked(ctx, key, claimed)
	s.mu.Unlock()

	if dispatch != nil && (resolution == ResolutionSend || resolution == ResolutionQueue) {
		if dispatchErr := dispatch(projected); dispatchErr != nil {
			s.mu.Lock()
			delete(s.suggestionInflight, claimKey)
			s.mu.Unlock()
			return Suggestion{}, fmt.Errorf("suggestion dispatch failed: %w", dispatchErr)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.suggestionInflight, claimKey)
	doc, err = s.suggestionsDocLocked(ctx, key)
	if err != nil {
		return Suggestion{}, err
	}
	index = -1
	for i, entry := range doc.Suggestions {
		if entry.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return Suggestion{}, fmt.Errorf("suggestion disappeared before finalization")
	}
	entry := doc.Suggestions[index]
	entry.State = resolutionState(resolution)
	entry.Resolution = resolution
	entry.ResolvedBy = actor.Identity().ID
	entry.ResolvedAtMS = s.now().UnixMilli()
	doc.Suggestions[index] = entry
	if err := s.putSuggestionsDocLocked(ctx, key, doc); err != nil {
		return Suggestion{}, err
	}
	projected = s.protocolSuggestionLocked(ctx, key, entry)
	s.emitLocked(EventSessionSuggestion, SessionSuggestionEventPayload{Action: "resolved", Suggestion: projected})
	return projected, nil
}
