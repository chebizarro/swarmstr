package sessioncoord

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

const groupCatalogName = "session-groups"

var ErrPlacementConflict = errors.New("session placement conflict")

type BroadcastFunc func(string, any)

type pendingEvent struct {
	name    string
	payload any
}

type Service struct {
	mu        sync.Mutex
	repo      *state.DocsRepository
	store     *state.SessionStore
	broadcast BroadcastFunc
	pending   []pendingEvent
	active    map[string]string // session key -> owning connection id for this process

	// Collaboration state.
	nowFn           func() time.Time
	sharingCache    map[string]state.SessionSharingDoc
	observerVisible map[string]bool // connection id -> declared observer visibility
}

type DispatchRequest struct {
	Key, AgentID, Backend, ConnectionID, Subject string
}

type ReclaimRequest struct {
	Key, ConnectionID, Reason string
	Force                     bool
}

func New(repo *state.DocsRepository, store *state.SessionStore) *Service {
	return &Service{repo: repo, store: store, active: map[string]string{}}
}

func (s *Service) SetBroadcaster(fn BroadcastFunc) {
	s.mu.Lock()
	s.broadcast = fn
	pending := append([]pendingEvent{}, s.pending...)
	s.pending = nil
	s.mu.Unlock()
	if fn != nil {
		for _, event := range pending {
			fn(event.name, event.payload)
		}
	}
}

func (s *Service) Dispatch(ctx context.Context, req DispatchRequest) (state.SessionPlacementDoc, error) {
	req.Key = strings.TrimSpace(req.Key)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.Backend = strings.TrimSpace(req.Backend)
	req.ConnectionID = strings.TrimSpace(req.ConnectionID)
	req.Subject = strings.TrimSpace(req.Subject)
	if req.Key == "" || req.Backend == "" {
		return state.SessionPlacementDoc{}, fmt.Errorf("key and backend are required")
	}
	if s == nil || s.repo == nil {
		return state.SessionPlacementDoc{}, fmt.Errorf("session placement service unavailable")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.repo.GetSession(ctx, req.Key)
	if err != nil {
		return state.SessionPlacementDoc{}, err
	}
	prior, priorErr := s.repo.GetSessionPlacement(ctx, req.Key)
	if priorErr != nil && !errors.Is(priorErr, state.ErrNotFound) {
		return state.SessionPlacementDoc{}, priorErr
	}
	if prior.State == "active" {
		if prior.OwnerConnectionID == req.ConnectionID && prior.AgentID == req.AgentID && prior.Backend == req.Backend {
			return prior, nil
		}
		return state.SessionPlacementDoc{}, fmt.Errorf("%w: session %q is already active on backend %q", ErrPlacementConflict, req.Key, prior.Backend)
	}

	entry, hadEntry := state.SessionEntry{}, false
	if s.store != nil {
		entry, hadEntry = s.store.Get(req.Key)
	}
	originalEntry := entry
	if req.AgentID == "" {
		req.AgentID = strings.TrimSpace(entry.AgentID)
		if req.AgentID == "" && session.Meta != nil {
			req.AgentID, _ = session.Meta["agent_id"].(string)
			req.AgentID = strings.TrimSpace(req.AgentID)
		}
		if req.AgentID == "" {
			req.AgentID = "main"
		}
	}

	now := time.Now().UnixMilli()
	created := prior.CreatedAtMS
	if created == 0 {
		created = now
	}
	placement := state.SessionPlacementDoc{
		Version: 1, SessionID: req.Key, State: "active", Generation: prior.Generation + 1,
		AgentID: req.AgentID, Backend: req.Backend, OwnerConnectionID: req.ConnectionID,
		OwnerSubject: req.Subject, PreviousAgentID: entry.AgentID, PreviousBackend: entry.ProviderOverride,
		CreatedAtMS: created, UpdatedAtMS: now, StateChangedAtMS: now,
	}
	if s.store != nil {
		entry.SessionID = req.Key
		entry.AgentID = req.AgentID
		entry.ProviderOverride = req.Backend
		if err := s.store.Put(req.Key, entry); err != nil {
			return state.SessionPlacementDoc{}, fmt.Errorf("persist session route: %w", err)
		}
	}
	if _, err := s.repo.PutSessionPlacement(ctx, req.Key, placement); err != nil {
		if s.store != nil {
			var rollbackErr error
			if hadEntry {
				rollbackErr = s.store.Put(req.Key, originalEntry)
			} else {
				rollbackErr = s.store.Delete(req.Key)
			}
			if rollbackErr != nil {
				return state.SessionPlacementDoc{}, fmt.Errorf("persist placement: %w (route rollback failed: %v)", err, rollbackErr)
			}
		}
		return state.SessionPlacementDoc{}, err
	}
	if req.ConnectionID != "" {
		s.active[req.Key] = req.ConnectionID
	}
	s.emitLocked("session.placement", placement)
	return placement, nil
}

func (s *Service) Reclaim(ctx context.Context, req ReclaimRequest) (state.SessionPlacementDoc, error) {
	if s == nil || s.repo == nil {
		return state.SessionPlacementDoc{}, fmt.Errorf("session placement service unavailable")
	}
	req.Key, req.ConnectionID, req.Reason = strings.TrimSpace(req.Key), strings.TrimSpace(req.ConnectionID), strings.TrimSpace(req.Reason)
	if req.Key == "" {
		return state.SessionPlacementDoc{}, fmt.Errorf("key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reclaimLocked(ctx, req)
}

func (s *Service) reclaimLocked(ctx context.Context, req ReclaimRequest) (state.SessionPlacementDoc, error) {
	placement, err := s.repo.GetSessionPlacement(ctx, req.Key)
	if err != nil {
		return state.SessionPlacementDoc{}, err
	}
	if placement.State == "reclaimed" {
		delete(s.active, req.Key)
		return placement, nil
	}
	if placement.State != "active" {
		return state.SessionPlacementDoc{}, fmt.Errorf("session %q has no active placement", req.Key)
	}
	if !req.Force && placement.OwnerConnectionID != "" && req.ConnectionID != placement.OwnerConnectionID {
		return state.SessionPlacementDoc{}, fmt.Errorf("%w: placement is owned by another connection", ErrPlacementConflict)
	}
	prior := placement
	now := time.Now().UnixMilli()
	placement.State = "reclaimed"
	placement.Generation++
	placement.UpdatedAtMS, placement.StateChangedAtMS = now, now
	placement.ReclaimReason = req.Reason
	placement.OwnerConnectionID = ""
	if _, err := s.repo.PutSessionPlacement(ctx, req.Key, placement); err != nil {
		return state.SessionPlacementDoc{}, err
	}
	if s.store != nil {
		if entry, ok := s.store.Get(req.Key); ok {
			entry.AgentID, entry.ProviderOverride = prior.PreviousAgentID, prior.PreviousBackend
			if err := s.store.Put(req.Key, entry); err != nil {
				_, _ = s.repo.PutSessionPlacement(ctx, req.Key, prior)
				return state.SessionPlacementDoc{}, fmt.Errorf("restore session route: %w", err)
			}
		}
	}
	delete(s.active, req.Key)
	s.emitLocked("session.placement", placement)
	return placement, nil
}

func (s *Service) ReclaimConnection(ctx context.Context, connectionID string) []error {
	connectionID = strings.TrimSpace(connectionID)
	if s == nil || connectionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0)
	for key, owner := range s.active {
		if owner == connectionID {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var errs []error
	for _, key := range keys {
		if _, err := s.reclaimLocked(ctx, ReclaimRequest{Key: key, ConnectionID: connectionID, Reason: "owner disconnected"}); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (s *Service) ListGroups(ctx context.Context) ([]string, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("session group service unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listGroupsLocked(ctx)
}

func (s *Service) PutGroups(ctx context.Context, names []string) ([]string, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("session group service unavailable")
	}
	normalized, err := normalizeGroups(names)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.repo.PutList(ctx, groupCatalogName, state.ListDoc{Version: 1, Name: groupCatalogName, Items: normalized}); err != nil {
		return nil, err
	}
	s.emitLocked("sessions.changed", map[string]any{"reason": "groups"})
	return normalized, nil
}

func (s *Service) RenameGroup(ctx context.Context, from, to string) (int, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" || len(to) > 64 {
		return 0, fmt.Errorf("name and to are required and must not exceed 64 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	groups, err := s.listGroupsLocked(ctx)
	if err != nil {
		return 0, err
	}
	for i := range groups {
		if groups[i] == from {
			groups[i] = to
		}
	}
	groups, err = normalizeGroups(groups)
	if err != nil {
		return 0, err
	}
	updated, err := s.rewriteSessionGroupLocked(ctx, from, to)
	if err != nil {
		return 0, err
	}
	if _, err := s.repo.PutList(ctx, groupCatalogName, state.ListDoc{Version: 1, Name: groupCatalogName, Items: groups}); err != nil {
		return 0, err
	}
	s.emitLocked("sessions.changed", map[string]any{"reason": "groups"})
	return updated, nil
}

func (s *Service) DeleteGroup(ctx context.Context, name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	groups, err := s.listGroupsLocked(ctx)
	if err != nil {
		return 0, err
	}
	kept := groups[:0]
	for _, group := range groups {
		if group != name {
			kept = append(kept, group)
		}
	}
	updated, err := s.rewriteSessionGroupLocked(ctx, name, "")
	if err != nil {
		return 0, err
	}
	if _, err := s.repo.PutList(ctx, groupCatalogName, state.ListDoc{Version: 1, Name: groupCatalogName, Items: kept}); err != nil {
		return 0, err
	}
	s.emitLocked("sessions.changed", map[string]any{"reason": "groups"})
	return updated, nil
}

func (s *Service) listGroupsLocked(ctx context.Context) ([]string, error) {
	doc, err := s.repo.GetList(ctx, groupCatalogName)
	if errors.Is(err, state.ErrNotFound) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return append([]string{}, doc.Items...), nil
}

func (s *Service) rewriteSessionGroupLocked(ctx context.Context, from, to string) (int, error) {
	sessions, err := s.repo.ListSessions(ctx, 5000)
	if err != nil {
		return 0, err
	}
	updated := 0
	for _, session := range sessions {
		if session.Meta == nil {
			continue
		}
		group, _ := session.Meta["group"].(string)
		if strings.TrimSpace(group) != from {
			continue
		}
		meta := make(map[string]any, len(session.Meta))
		for k, v := range session.Meta {
			meta[k] = v
		}
		if to == "" {
			delete(meta, "group")
		} else {
			meta["group"] = to
		}
		session.Meta = meta
		if _, err := s.repo.PutSession(ctx, session.SessionID, session); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func normalizeGroups(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 64 {
			return nil, fmt.Errorf("group names must be non-empty and at most 64 characters")
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) > 200 {
		return nil, fmt.Errorf("at most 200 groups are allowed")
	}
	return out, nil
}

func (s *Service) emitLocked(event string, payload any) {
	if s.broadcast == nil {
		if len(s.pending) >= 128 {
			s.pending = s.pending[1:]
		}
		s.pending = append(s.pending, pendingEvent{name: event, payload: payload})
		return
	}
	// Broadcast outside the mutation call stack so a slow-client disconnect can
	// synchronously reclaim its leases without re-entering this mutex.
	go s.broadcast(event, payload)
}
