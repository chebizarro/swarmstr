package nip86

import (
	"context"
	"sort"
	"sync"
)

type Entry struct {
	PubKey string `json:"pubkey,omitempty"`
	ID     string `json:"id,omitempty"`
	IP     string `json:"ip,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ManagementStore interface {
	BanPubKey(context.Context, string, string) error
	AllowPubKey(context.Context, string, string) error
	ListBannedPubKeys(context.Context) ([]Entry, error)
	ListAllowedPubKeys(context.Context) ([]Entry, error)
	BanEvent(context.Context, string, string) error
	AllowEvent(context.Context, string, string) error
	ListBannedEvents(context.Context) ([]Entry, error)
	ChangeRelayName(context.Context, string) error
	ChangeRelayDescription(context.Context, string) error
	ChangeRelayIcon(context.Context, string) error
	AllowKind(context.Context, int) error
	DisallowKind(context.Context, int) error
	ListAllowedKinds(context.Context) ([]int, error)
	ListDisallowedKinds(context.Context) ([]int, error)
	BlockIP(context.Context, string, string) error
	UnblockIP(context.Context, string) error
	ListBlockedIPs(context.Context) ([]Entry, error)
}

type MemoryStore struct {
	mu                                                      sync.RWMutex
	bannedPubKeys, allowedPubKeys, bannedEvents, blockedIPs map[string]string
	allowedKinds, disallowedKinds                           map[int]struct{}
	RelayName, RelayDescription, RelayIcon                  string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{bannedPubKeys: map[string]string{}, allowedPubKeys: map[string]string{}, bannedEvents: map[string]string{}, blockedIPs: map[string]string{}, allowedKinds: map[int]struct{}{}, disallowedKinds: map[int]struct{}{}}
}
func (s *MemoryStore) init() {
	if s.bannedPubKeys == nil {
		*s = *NewMemoryStore()
	}
}
func (s *MemoryStore) BanPubKey(_ context.Context, v, r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	s.bannedPubKeys[v] = r
	return nil
}
func (s *MemoryStore) AllowPubKey(_ context.Context, v, r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	s.allowedPubKeys[v] = r
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
func (s *MemoryStore) BanEvent(_ context.Context, v, r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	s.bannedEvents[v] = r
	return nil
}
func (s *MemoryStore) AllowEvent(_ context.Context, v, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	delete(s.bannedEvents, v)
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
	s.init()
	s.allowedKinds[k] = struct{}{}
	return nil
}
func (s *MemoryStore) DisallowKind(_ context.Context, k int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	s.disallowedKinds[k] = struct{}{}
	return nil
}
func (s *MemoryStore) ListAllowedKinds(context.Context) ([]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return kinds(s.allowedKinds), nil
}
func (s *MemoryStore) ListDisallowedKinds(context.Context) ([]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return kinds(s.disallowedKinds), nil
}
func (s *MemoryStore) BlockIP(_ context.Context, ip, r string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	s.blockedIPs[ip] = r
	return nil
}
func (s *MemoryStore) UnblockIP(_ context.Context, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
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
