package nip86

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Entry struct {
	PubKey string `json:"pubkey,omitempty"`
	ID     string `json:"id,omitempty"`
	IP     string `json:"ip,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type Role struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Order       int    `json:"order"`
}

type ManagementStore interface {
	BanPubKey(context.Context, string, string) error
	UnbanPubKey(context.Context, string, string) error
	AllowPubKey(context.Context, string, string) error
	UnallowPubKey(context.Context, string, string) error
	ListBannedPubKeys(context.Context) ([]Entry, error)
	ListAllowedPubKeys(context.Context) ([]Entry, error)
	CreateRole(context.Context, Role) error
	EditRole(context.Context, Role) error
	DeleteRole(context.Context, string) error
	AssignRole(context.Context, string, string) error
	UnassignRole(context.Context, string, string) error
	ListEventsNeedingModeration(context.Context) ([]Entry, error)
	BanEvent(context.Context, string, string) error
	AllowEvent(context.Context, string, string) error
	ListBannedEvents(context.Context) ([]Entry, error)
	ChangeRelayName(context.Context, string) error
	ChangeRelayDescription(context.Context, string) error
	ChangeRelayIcon(context.Context, string) error
	AllowKind(context.Context, int) error
	DisallowKind(context.Context, int) error
	ListAllowedKinds(context.Context) ([]int, error)
	BlockIP(context.Context, string, string) error
	UnblockIP(context.Context, string) error
	ListBlockedIPs(context.Context) ([]Entry, error)
}

type MemoryStore struct {
	mu sync.RWMutex

	bannedPubKeys, allowedPubKeys, bannedEvents, moderationEvents, blockedIPs map[string]string
	allowedKinds, disallowedKinds                                             map[int]struct{}
	roles                                                                     map[string]Role
	roleAssignments                                                           map[string]map[string]struct{}
	admins                                                                    map[string]struct{}

	RelayName, RelayDescription, RelayIcon string
}

// NewMemoryStore creates a store and optionally seeds the verified pubkeys that
// may administer a Handler. With no admins the HTTP handler fails closed.
func NewMemoryStore(admins ...string) *MemoryStore {
	s := &MemoryStore{}
	s.initLocked()
	for _, admin := range admins {
		if admin = strings.ToLower(strings.TrimSpace(admin)); admin != "" {
			s.admins[admin] = struct{}{}
		}
	}
	return s
}

func (s *MemoryStore) initLocked() {
	if s.bannedPubKeys == nil {
		s.bannedPubKeys = map[string]string{}
	}
	if s.allowedPubKeys == nil {
		s.allowedPubKeys = map[string]string{}
	}
	if s.bannedEvents == nil {
		s.bannedEvents = map[string]string{}
	}
	if s.moderationEvents == nil {
		s.moderationEvents = map[string]string{}
	}
	if s.blockedIPs == nil {
		s.blockedIPs = map[string]string{}
	}
	if s.allowedKinds == nil {
		s.allowedKinds = map[int]struct{}{}
	}
	if s.disallowedKinds == nil {
		s.disallowedKinds = map[int]struct{}{}
	}
	if s.roles == nil {
		s.roles = map[string]Role{}
	}
	if s.roleAssignments == nil {
		s.roleAssignments = map[string]map[string]struct{}{}
	}
	if s.admins == nil {
		s.admins = map[string]struct{}{}
	}
}

func (s *MemoryStore) IsAdmin(_ context.Context, pubkey string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.admins[strings.ToLower(strings.TrimSpace(pubkey))]
	return ok
}

func (s *MemoryStore) BanPubKey(_ context.Context, v, r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.bannedPubKeys[v] = r
	return nil
}
func (s *MemoryStore) UnbanPubKey(_ context.Context, v, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	delete(s.bannedPubKeys, v)
	return nil
}
func (s *MemoryStore) AllowPubKey(_ context.Context, v, r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.allowedPubKeys[v] = r
	return nil
}
func (s *MemoryStore) UnallowPubKey(_ context.Context, v, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	delete(s.allowedPubKeys, v)
	return nil
}
func (s *MemoryStore) ListBannedPubKeys(context.Context) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return entries(s.bannedPubKeys, "pubkey"), nil
}
func (s *MemoryStore) ListAllowedPubKeys(context.Context) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return entries(s.allowedPubKeys, "pubkey"), nil
}

func (s *MemoryStore) CreateRole(_ context.Context, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if _, exists := s.roles[role.ID]; exists {
		return fmt.Errorf("role %q already exists", role.ID)
	}
	s.roles[role.ID] = role
	return nil
}
func (s *MemoryStore) EditRole(_ context.Context, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if _, exists := s.roles[role.ID]; !exists {
		return fmt.Errorf("role %q not found", role.ID)
	}
	s.roles[role.ID] = role
	return nil
}
func (s *MemoryStore) DeleteRole(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	delete(s.roles, id)
	for pubkey, assigned := range s.roleAssignments {
		delete(assigned, id)
		if len(assigned) == 0 {
			delete(s.roleAssignments, pubkey)
		}
	}
	return nil
}
func (s *MemoryStore) AssignRole(_ context.Context, pubkey, roleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	if _, exists := s.roles[roleID]; !exists {
		return fmt.Errorf("role %q not found", roleID)
	}
	if s.roleAssignments[pubkey] == nil {
		s.roleAssignments[pubkey] = map[string]struct{}{}
	}
	s.roleAssignments[pubkey][roleID] = struct{}{}
	return nil
}
func (s *MemoryStore) UnassignRole(_ context.Context, pubkey, roleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	delete(s.roleAssignments[pubkey], roleID)
	if len(s.roleAssignments[pubkey]) == 0 {
		delete(s.roleAssignments, pubkey)
	}
	return nil
}

// Role returns a stored role for local inspection and tests.
func (s *MemoryStore) Role(id string) (Role, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	role, ok := s.roles[id]
	return role, ok
}
func (s *MemoryStore) HasRole(pubkey, roleID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.roleAssignments[pubkey][roleID]
	return ok
}

// FlagEventForModeration adds an event to listeventsneedingmoderation.
func (s *MemoryStore) FlagEventForModeration(id, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.moderationEvents[id] = reason
}
func (s *MemoryStore) ListEventsNeedingModeration(context.Context) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return entries(s.moderationEvents, "id"), nil
}
func (s *MemoryStore) BanEvent(_ context.Context, v, r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.bannedEvents[v] = r
	delete(s.moderationEvents, v)
	return nil
}
func (s *MemoryStore) AllowEvent(_ context.Context, v, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	delete(s.bannedEvents, v)
	delete(s.moderationEvents, v)
	return nil
}
func (s *MemoryStore) ListBannedEvents(context.Context) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return entries(s.bannedEvents, "id"), nil
}
func (s *MemoryStore) ChangeRelayName(_ context.Context, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RelayName = v
	return nil
}
func (s *MemoryStore) ChangeRelayDescription(_ context.Context, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RelayDescription = v
	return nil
}
func (s *MemoryStore) ChangeRelayIcon(_ context.Context, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RelayIcon = v
	return nil
}
func (s *MemoryStore) AllowKind(_ context.Context, k int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.allowedKinds[k] = struct{}{}
	delete(s.disallowedKinds, k)
	return nil
}
func (s *MemoryStore) DisallowKind(_ context.Context, k int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	delete(s.allowedKinds, k)
	s.disallowedKinds[k] = struct{}{}
	return nil
}
func (s *MemoryStore) ListAllowedKinds(context.Context) ([]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return kinds(s.allowedKinds), nil
}
func (s *MemoryStore) BlockIP(_ context.Context, ip, r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.blockedIPs[ip] = r
	return nil
}
func (s *MemoryStore) UnblockIP(_ context.Context, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	delete(s.blockedIPs, ip)
	return nil
}
func (s *MemoryStore) ListBlockedIPs(context.Context) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return entries(s.blockedIPs, "ip"), nil
}

func entries(m map[string]string, field string) []Entry {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Entry, 0, len(keys))
	for _, k := range keys {
		e := Entry{Reason: m[k]}
		switch field {
		case "pubkey":
			e.PubKey = k
		case "id":
			e.ID = k
		case "ip":
			e.IP = k
		}
		out = append(out, e)
	}
	return out
}
func kinds(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
