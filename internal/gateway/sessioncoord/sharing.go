package sessioncoord

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"metiq/internal/store/state"
)

// Session collaboration visibility values (OpenClaw gateway-protocol parity).
const (
	VisibilityShared   = "shared"
	VisibilityReadOnly = "read-only"
	VisibilitySuggest  = "suggest"
	VisibilityDraft    = "draft"
)

// Sharing roles, ordered from most to least privileged.
const (
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
	RoleMember = "member"
	RoleViewer = "viewer"
)

// EventSessionSharing mirrors the OpenClaw gateway event name.
const EventSessionSharing = "session.sharing"

// ErrForbidden marks collaboration authorization failures.
var ErrForbidden = errors.New("forbidden")

var errTypingParams = errors.New("sessionKey and sessionId are required")

// Identity is the wire shape used for sharing actors and identity catalogs.
type Identity struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

// Actor describes the caller of a collaboration mutation. Subject is the
// durable connection identity (device subject or authenticated pubkey). Admin
// callers bypass ownership checks; identity-less non-admin callers are treated
// as solo-deployment owners, matching OpenClaw semantics.
type Actor struct {
	Subject string
	Admin   bool
}

// Identity projects the actor into the sharing event/actor wire shape.
func (a Actor) Identity() Identity {
	subject := strings.TrimSpace(a.Subject)
	if subject != "" {
		return Identity{Type: "human", ID: subject}
	}
	if a.Admin {
		return Identity{Type: "system", ID: "operator.admin", Label: "Administrator"}
	}
	return Identity{Type: "system", ID: "local-operator", Label: "Local operator"}
}

// SessionSharingEventPayload is the session.sharing event body.
type SessionSharingEventPayload struct {
	Action     string   `json:"action"`
	SessionKey string   `json:"sessionKey"`
	AgentID    string   `json:"agentId"`
	Actor      Identity `json:"actor"`
	Visibility string   `json:"visibility,omitempty"`
	IdentityID string   `json:"identityId,omitempty"`
	TS         int64    `json:"ts"`
}

// SessionMember is the wire shape for one membership row.
type SessionMember struct {
	IdentityID string `json:"identityId"`
	AddedBy    string `json:"addedBy"`
	AddedAt    int64  `json:"addedAt"`
}

// MembersListResult is the session.members.list response body.
type MembersListResult struct {
	SessionKey          string          `json:"sessionKey"`
	Owner               *Identity       `json:"owner,omitempty"`
	Members             []SessionMember `json:"members"`
	Identities          []Identity      `json:"identities"`
	Role                string          `json:"role"`
	AllowedVisibilities []string        `json:"allowedVisibilities"`
}

func AllowedVisibilities() []string {
	return []string{VisibilityShared, VisibilityReadOnly, VisibilitySuggest, VisibilityDraft}
}

func isValidVisibility(visibility string) bool {
	switch visibility {
	case VisibilityShared, VisibilityReadOnly, VisibilitySuggest, VisibilityDraft:
		return true
	}
	return false
}

func normalizeVisibility(visibility string) string {
	visibility = strings.TrimSpace(visibility)
	if visibility == "" {
		return VisibilityShared
	}
	return visibility
}

func (s *Service) now() time.Time {
	if s != nil && s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// SetClock overrides the service clock for deterministic tests.
func (s *Service) SetClock(now func() time.Time) {
	s.mu.Lock()
	s.nowFn = now
	s.mu.Unlock()
}

func (s *Service) sharingDocLocked(ctx context.Context, key string) (state.SessionSharingDoc, error) {
	if doc, ok := s.sharingCache[key]; ok {
		return doc, nil
	}
	doc, err := s.repo.GetSessionSharing(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		doc = state.SessionSharingDoc{Version: 1, SessionID: key}
		err = nil
	}
	if err != nil {
		return state.SessionSharingDoc{}, err
	}
	if s.sharingCache == nil {
		s.sharingCache = map[string]state.SessionSharingDoc{}
	}
	s.sharingCache[key] = doc
	return doc, nil
}

func (s *Service) putSharingDocLocked(ctx context.Context, key string, doc state.SessionSharingDoc) error {
	doc.Version = 1
	doc.SessionID = key
	doc.UpdatedAtMS = s.now().UnixMilli()
	if _, err := s.repo.PutSessionSharing(ctx, key, doc); err != nil {
		return err
	}
	if s.sharingCache == nil {
		s.sharingCache = map[string]state.SessionSharingDoc{}
	}
	s.sharingCache[key] = doc
	return nil
}

// ownerSubjectLocked resolves the durable owner for a session: the persisted
// sharing owner wins; otherwise the dispatch placement owner subject seeds it.
func (s *Service) ownerSubjectLocked(ctx context.Context, key string, doc state.SessionSharingDoc) string {
	if owner := strings.TrimSpace(doc.OwnerSubject); owner != "" {
		return owner
	}
	placement, err := s.repo.GetSessionPlacement(ctx, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(placement.OwnerSubject)
}

func (s *Service) resolveRoleLocked(ctx context.Context, key string, doc state.SessionSharingDoc, actor Actor) string {
	if actor.Admin {
		return RoleAdmin
	}
	subject := strings.TrimSpace(actor.Subject)
	if subject == "" {
		// Shared-secret/no-auth solo deployments have no durable identity.
		return RoleOwner
	}
	owner := s.ownerSubjectLocked(ctx, key, doc)
	if owner == "" || owner == subject {
		return RoleOwner
	}
	if doc.HasMember(subject) {
		return RoleMember
	}
	return RoleViewer
}

func canManageSharing(role string) bool {
	return role == RoleAdmin || role == RoleOwner
}

func canMutateSession(role, visibility string) bool {
	// Drafts stay creator/admin-only; membership becomes active only after promotion.
	if visibility == VisibilityDraft {
		return role == RoleAdmin || role == RoleOwner
	}
	return visibility == VisibilityShared || role != RoleViewer
}

func (s *Service) requireSessionLocked(ctx context.Context, key string) error {
	if _, err := s.repo.GetSession(ctx, key); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("unknown session: %s", key)
		}
		return err
	}
	return nil
}

func (s *Service) requireManagerLocked(ctx context.Context, key string, actor Actor) (state.SessionSharingDoc, string, error) {
	if err := s.requireSessionLocked(ctx, key); err != nil {
		return state.SessionSharingDoc{}, "", err
	}
	doc, err := s.sharingDocLocked(ctx, key)
	if err != nil {
		return state.SessionSharingDoc{}, "", err
	}
	role := s.resolveRoleLocked(ctx, key, doc, actor)
	if !canManageSharing(role) {
		return state.SessionSharingDoc{}, "", fmt.Errorf("%w: session owner or operator.admin required", ErrForbidden)
	}
	return doc, role, nil
}

// stampOwnerLocked records the first identified manager as the durable owner
// so later role checks stay stable across restarts.
func stampOwner(doc *state.SessionSharingDoc, actor Actor) {
	if strings.TrimSpace(doc.OwnerSubject) != "" {
		return
	}
	if actor.Admin {
		return
	}
	if subject := strings.TrimSpace(actor.Subject); subject != "" {
		doc.OwnerSubject = subject
	}
}

func (s *Service) sessionAgentIDLocked(ctx context.Context, key string) string {
	if s.store != nil {
		if entry, ok := s.store.Get(key); ok && strings.TrimSpace(entry.AgentID) != "" {
			return strings.TrimSpace(entry.AgentID)
		}
	}
	if session, err := s.repo.GetSession(ctx, key); err == nil && session.Meta != nil {
		if agentID, _ := session.Meta["agent_id"].(string); strings.TrimSpace(agentID) != "" {
			return strings.TrimSpace(agentID)
		}
	}
	return "main"
}

func (s *Service) emitSharingLocked(ctx context.Context, key, action string, actor Actor, visibility, identityID string) {
	payload := SessionSharingEventPayload{
		Action:     action,
		SessionKey: key,
		AgentID:    s.sessionAgentIDLocked(ctx, key),
		Actor:      actor.Identity(),
		Visibility: visibility,
		IdentityID: identityID,
		TS:         s.now().UnixMilli(),
	}
	s.emitLocked(EventSessionSharing, payload)
	s.emitLocked("sessions.changed", map[string]any{"reason": "sharing", "sessionKey": key})
}

// SetVisibility persists a session visibility transition after manager
// authorization and projects session.sharing / sessions.changed events.
func (s *Service) SetVisibility(ctx context.Context, key, visibility string, actor Actor) (string, error) {
	key = strings.TrimSpace(key)
	visibility = strings.TrimSpace(visibility)
	if s == nil || s.repo == nil {
		return "", fmt.Errorf("session sharing service unavailable")
	}
	if key == "" {
		return "", fmt.Errorf("sessionKey is required")
	}
	if !isValidVisibility(visibility) {
		return "", fmt.Errorf("invalid visibility %q (allowed: %s)", visibility, strings.Join(AllowedVisibilities(), ", "))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, _, err := s.requireManagerLocked(ctx, key, actor)
	if err != nil {
		return "", err
	}
	if normalizeVisibility(doc.Visibility) == visibility {
		return visibility, nil
	}
	stampOwner(&doc, actor)
	doc.Visibility = visibility
	if err := s.putSharingDocLocked(ctx, key, doc); err != nil {
		return "", err
	}
	s.emitSharingLocked(ctx, key, "visibility", actor, visibility, "")
	return visibility, nil
}

// Members returns the membership aggregate for a session after manager
// authorization.
func (s *Service) Members(ctx context.Context, key string, actor Actor) (MembersListResult, error) {
	key = strings.TrimSpace(key)
	if s == nil || s.repo == nil {
		return MembersListResult{}, fmt.Errorf("session sharing service unavailable")
	}
	if key == "" {
		return MembersListResult{}, fmt.Errorf("sessionKey is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, role, err := s.requireManagerLocked(ctx, key, actor)
	if err != nil {
		return MembersListResult{}, err
	}
	members := make([]SessionMember, 0, len(doc.Members))
	identityByID := map[string]Identity{}
	remember := func(identity Identity) {
		identity.ID = strings.TrimSpace(identity.ID)
		if identity.ID == "" {
			return
		}
		if existing, ok := identityByID[identity.ID]; ok && existing.Label != "" {
			return
		}
		identityByID[identity.ID] = identity
	}
	remember(actor.Identity())
	owner := strings.TrimSpace(s.ownerSubjectLocked(ctx, key, doc))
	var ownerIdentity *Identity
	if owner != "" {
		ownerIdentity = &Identity{Type: "human", ID: owner}
		remember(*ownerIdentity)
	}
	for _, member := range doc.Members {
		members = append(members, SessionMember{IdentityID: member.IdentityID, AddedBy: member.AddedBy, AddedAt: member.AddedAtMS})
		remember(Identity{Type: "human", ID: member.IdentityID})
	}
	identities := make([]Identity, 0, len(identityByID))
	for _, identity := range identityByID {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		li, lj := identities[i].Label, identities[j].Label
		if li == "" {
			li = identities[i].ID
		}
		if lj == "" {
			lj = identities[j].ID
		}
		if li == lj {
			return identities[i].ID < identities[j].ID
		}
		return li < lj
	})
	return MembersListResult{
		SessionKey:          key,
		Owner:               ownerIdentity,
		Members:             members,
		Identities:          identities,
		Role:                role,
		AllowedVisibilities: AllowedVisibilities(),
	}, nil
}

// AddMember grants membership to identityID after manager authorization.
// The grant is idempotent.
func (s *Service) AddMember(ctx context.Context, key, identityID string, actor Actor) error {
	key, identityID = strings.TrimSpace(key), strings.TrimSpace(identityID)
	if s == nil || s.repo == nil {
		return fmt.Errorf("session sharing service unavailable")
	}
	if key == "" || identityID == "" {
		return fmt.Errorf("sessionKey and identityId are required")
	}
	if len(identityID) > 128 {
		return fmt.Errorf("identityId must not exceed 128 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, _, err := s.requireManagerLocked(ctx, key, actor)
	if err != nil {
		return err
	}
	if doc.HasMember(identityID) {
		return nil
	}
	if len(doc.Members) >= 200 {
		return fmt.Errorf("at most 200 members are allowed")
	}
	stampOwner(&doc, actor)
	doc.Members = append(doc.Members, state.SessionMemberEntry{
		IdentityID: identityID,
		AddedBy:    actor.Identity().ID,
		AddedAtMS:  s.now().UnixMilli(),
	})
	if err := s.putSharingDocLocked(ctx, key, doc); err != nil {
		return err
	}
	s.emitSharingLocked(ctx, key, "member-added", actor, "", identityID)
	return nil
}

// RemoveMember revokes membership from identityID after manager authorization.
// The revocation is idempotent.
func (s *Service) RemoveMember(ctx context.Context, key, identityID string, actor Actor) error {
	key, identityID = strings.TrimSpace(key), strings.TrimSpace(identityID)
	if s == nil || s.repo == nil {
		return fmt.Errorf("session sharing service unavailable")
	}
	if key == "" || identityID == "" {
		return fmt.Errorf("sessionKey and identityId are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, _, err := s.requireManagerLocked(ctx, key, actor)
	if err != nil {
		return err
	}
	if !doc.HasMember(identityID) {
		return nil
	}
	kept := doc.Members[:0]
	for _, member := range doc.Members {
		if member.IdentityID != identityID {
			kept = append(kept, member)
		}
	}
	stampOwner(&doc, actor)
	doc.Members = kept
	if err := s.putSharingDocLocked(ctx, key, doc); err != nil {
		return err
	}
	s.emitSharingLocked(ctx, key, "member-removed", actor, "", identityID)
	return nil
}

// AuthorizeSessionMutation is the central visibility-scoping choke point for
// keyed session mutation methods. Sessions without a sharing doc default to
// "shared" and admit every operator, preserving pre-collaboration behavior.
func (s *Service) AuthorizeSessionMutation(ctx context.Context, key string, actor Actor) error {
	key = strings.TrimSpace(key)
	if s == nil || s.repo == nil || key == "" {
		return nil
	}
	if actor.Admin {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.sharingDocLocked(ctx, key)
	if err != nil {
		// Fail open on storage errors for sessions with no sharing state; the
		// method's own handler still validates existence.
		return nil
	}
	visibility := normalizeVisibility(doc.Visibility)
	role := s.resolveRoleLocked(ctx, key, doc, actor)
	if canMutateSession(role, visibility) {
		return nil
	}
	return fmt.Errorf("%w: session is %s for this connection", ErrForbidden, visibility)
}

// SetObserverVisibility records whether a WS connection currently renders
// session-observer output. It is connection-scoped, in-memory state.
func (s *Service) SetObserverVisibility(connectionID string, visible bool) {
	connectionID = strings.TrimSpace(connectionID)
	if s == nil || connectionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.observerVisible == nil {
		s.observerVisible = map[string]bool{}
	}
	if visible {
		s.observerVisible[connectionID] = true
	} else {
		delete(s.observerVisible, connectionID)
	}
}

// ObserverVisible reports the last declared observer visibility for a connection.
func (s *Service) ObserverVisible(connectionID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observerVisible[strings.TrimSpace(connectionID)]
}

// DropConnection releases connection-scoped collaboration state (observer
// visibility declarations) when a WS connection closes.
func (s *Service) DropConnection(connectionID string) {
	connectionID = strings.TrimSpace(connectionID)
	if s == nil || connectionID == "" {
		return
	}
	s.mu.Lock()
	delete(s.observerVisible, connectionID)
	s.dropTypingConnectionLocked(connectionID)
	s.mu.Unlock()
}

// AllowEventDelivery applies per-connection visibility filtering to the
// collaboration event stream during WS fanout. Non-collaboration events are
// always allowed; admins see everything.
func (s *Service) AllowEventDelivery(actor Actor, event string, payload any) bool {
	switch event {
	case EventSessionSharing, EventSessionSuggestion, EventSessionTyping:
	default:
		return true
	}
	if s == nil || s.repo == nil || actor.Admin {
		return true
	}
	subject := strings.TrimSpace(actor.Subject)
	if subject == "" {
		// Identity-less connections never receive suggestion/typing traffic.
		return event == EventSessionSharing
	}
	key, authorID := collaborationEventScope(event, payload)
	if key == "" {
		return false
	}
	ctx := context.Background()
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.sharingDocLocked(ctx, key)
	if err != nil {
		return false
	}
	visibility := normalizeVisibility(doc.Visibility)
	role := s.resolveRoleLocked(ctx, key, doc, actor)
	if visibility == VisibilityDraft && !canManageSharing(role) {
		return false
	}
	switch event {
	case EventSessionSuggestion:
		return authorID == subject || role != RoleViewer
	case EventSessionTyping:
		return role != RoleViewer || visibility == VisibilityShared || visibility == VisibilitySuggest
	}
	return true
}

func collaborationEventScope(event string, payload any) (sessionKey, authorID string) {
	switch p := payload.(type) {
	case SessionSharingEventPayload:
		return p.SessionKey, ""
	case SessionSuggestionEventPayload:
		return p.Suggestion.SessionKey, p.Suggestion.Author.ID
	case SessionTypingEventPayload:
		return p.SessionKey, p.Actor.ID
	}
	return "", ""
}
