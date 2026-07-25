// Package userprofiles implements the durable backing for the gateway users.*
// surface (users.list / users.self / users.linkEmail / users.setDisplayName /
// users.setAvatar).
//
// Metiq deviation (parity triage group nostr-user-identity, accepted-deviation):
// OpenClaw keys user profiles on authenticated e-mail accounts. Metiq's control
// plane authenticates nostr identities, so a profile is keyed by an opaque
// identity id (the caller's nostr pubkey for users.self) with optional e-mail
// aliases layered on top. The wire projection matches OpenClaw's UserProfile
// shape (id/displayName/avatarMime/mergedInto/createdAt/updatedAt/emails/
// hasAvatar) so existing clients keep working.
//
// The manager mirrors the durable question / plugin-approval ledgers
// (internal/gateway/questions, internal/gateway/pluginapproval): an atomic JSON
// document persisted under the config dir that survives daemon restarts.
package userprofiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const ledgerVersion = 1

// MaxProfileIDLength mirrors OpenClaw's UserProfileId maxLength.
const MaxProfileIDLength = 128

// MaxDisplayNameLength mirrors OpenClaw's UserProfileDisplayName maxLength.
const MaxDisplayNameLength = 256

// MaxEmailLength mirrors OpenClaw's linkEmail e-mail maxLength.
const MaxEmailLength = 320

// AllowedAvatarMimes is the closed set of avatar content types (OpenClaw parity).
var AllowedAvatarMimes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/webp": {},
}

// ErrNotFound is returned when a profile id does not resolve.
var ErrNotFound = fmt.Errorf("user profile not found")

// Profile is the wire projection returned by every users.* method. It mirrors
// OpenClaw's UserProfileSchema. DisplayName / AvatarMime / MergedInto are
// pointers so they serialize as JSON null when unset (OpenClaw uses nullable
// unions).
type Profile struct {
	ID          string   `json:"id"`
	DisplayName *string  `json:"displayName"`
	AvatarMime  *string  `json:"avatarMime"`
	MergedInto  *string  `json:"mergedInto"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
	Emails      []string `json:"emails"`
	HasAvatar   bool     `json:"hasAvatar"`
}

// record is the durable representation (includes the avatar blob, which the
// wire Profile deliberately omits — clients fetch avatars out of band).
type record struct {
	ID          string   `json:"id"`
	DisplayName *string  `json:"displayName,omitempty"`
	AvatarMime  *string  `json:"avatarMime,omitempty"`
	AvatarBytes []byte   `json:"avatarBytes,omitempty"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
	Emails      []string `json:"emails,omitempty"`
}

type ledgerDocument struct {
	Version  int      `json:"version"`
	Profiles []record `json:"profiles"`
}

// Manager is the durable lifecycle owner for user profiles.
type Manager struct {
	mu          sync.Mutex
	profiles    map[string]record
	storagePath string
	// nowMs is injectable for deterministic tests; defaults to the wall clock.
	nowMs func() int64
}

// NewManager returns an in-memory-only manager (no durable persistence).
func NewManager() *Manager {
	m, _ := NewManagerAt("")
	return m
}

// NewManagerAt loads (or initializes) a manager backed by the durable ledger at
// path. Profiles recorded by a prior process are restored verbatim.
func NewManagerAt(path string) (*Manager, error) {
	m := &Manager{
		profiles:    map[string]record{},
		storagePath: strings.TrimSpace(path),
		nowMs:       func() int64 { return time.Now().UnixMilli() },
	}
	if m.storagePath == "" {
		return m, nil
	}
	raw, err := os.ReadFile(m.storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, fmt.Errorf("read user profile ledger: %w", err)
	}
	var doc ledgerDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode user profile ledger: %w", err)
	}
	if doc.Version != ledgerVersion {
		return nil, fmt.Errorf("unsupported user profile ledger version %d", doc.Version)
	}
	for _, rec := range doc.Profiles {
		id := strings.TrimSpace(rec.ID)
		if id == "" {
			continue
		}
		rec.ID = id
		m.profiles[id] = cloneRecord(rec)
	}
	return m, nil
}

// List returns every known profile, sorted by creation time then id.
func (m *Manager) List() []Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Profile, 0, len(m.profiles))
	for _, rec := range m.profiles {
		out = append(out, toProfile(rec))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out
}

// Get resolves one profile by id.
func (m *Manager) Get(id string) (Profile, error) {
	id = strings.TrimSpace(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.profiles[id]
	if !ok {
		return Profile{}, ErrNotFound
	}
	return toProfile(rec), nil
}

// EnsureForIdentity returns the profile keyed by identity, creating it on first
// use. identity is the caller's nostr pubkey for users.self.
func (m *Manager) EnsureForIdentity(identity string) (Profile, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return Profile{}, fmt.Errorf("identity is required")
	}
	if len(identity) > MaxProfileIDLength {
		return Profile{}, fmt.Errorf("identity exceeds %d characters", MaxProfileIDLength)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.profiles[identity]; ok {
		return toProfile(rec), nil
	}
	now := m.nowMs()
	rec := record{ID: identity, CreatedAt: now, UpdatedAt: now, Emails: []string{}}
	m.profiles[identity] = rec
	if err := m.persistLocked(); err != nil {
		delete(m.profiles, identity)
		return Profile{}, err
	}
	return toProfile(rec), nil
}

// LinkEmail attaches an e-mail alias to an existing profile.
func (m *Manager) LinkEmail(email, targetProfileID string) (Profile, error) {
	email = strings.TrimSpace(email)
	targetProfileID = strings.TrimSpace(targetProfileID)
	if email == "" {
		return Profile{}, fmt.Errorf("email must not be empty")
	}
	if len(email) > MaxEmailLength {
		return Profile{}, fmt.Errorf("email exceeds %d characters", MaxEmailLength)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.profiles[targetProfileID]
	if !ok {
		return Profile{}, ErrNotFound
	}
	lower := strings.ToLower(email)
	for _, existing := range rec.Emails {
		if strings.ToLower(existing) == lower {
			// Idempotent: already linked.
			return toProfile(rec), nil
		}
	}
	prev := rec.Emails
	rec.Emails = append(append([]string{}, rec.Emails...), email)
	rec.UpdatedAt = m.nowMs()
	m.profiles[targetProfileID] = rec
	if err := m.persistLocked(); err != nil {
		rec.Emails = prev
		m.profiles[targetProfileID] = rec
		return Profile{}, err
	}
	return toProfile(rec), nil
}

// SetDisplayName sets (or clears, when displayName is nil) a profile's display
// name. The profile must already exist.
func (m *Manager) SetDisplayName(profileID string, displayName *string) (Profile, error) {
	profileID = strings.TrimSpace(profileID)
	if displayName != nil {
		trimmed := strings.TrimSpace(*displayName)
		if len([]rune(trimmed)) > MaxDisplayNameLength {
			return Profile{}, fmt.Errorf("displayName exceeds %d characters", MaxDisplayNameLength)
		}
		displayName = &trimmed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.profiles[profileID]
	if !ok {
		return Profile{}, ErrNotFound
	}
	prev := rec.DisplayName
	rec.DisplayName = displayName
	rec.UpdatedAt = m.nowMs()
	m.profiles[profileID] = rec
	if err := m.persistLocked(); err != nil {
		rec.DisplayName = prev
		m.profiles[profileID] = rec
		return Profile{}, err
	}
	return toProfile(rec), nil
}

// SetAvatar stores a profile's avatar bytes and content type. The profile must
// already exist. mime must be one of AllowedAvatarMimes.
func (m *Manager) SetAvatar(profileID string, avatar []byte, mime string) (Profile, error) {
	profileID = strings.TrimSpace(profileID)
	mime = strings.TrimSpace(mime)
	if _, ok := AllowedAvatarMimes[mime]; !ok {
		return Profile{}, fmt.Errorf("unsupported avatar mime %q", mime)
	}
	if len(avatar) == 0 {
		return Profile{}, fmt.Errorf("avatar must not be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.profiles[profileID]
	if !ok {
		return Profile{}, ErrNotFound
	}
	prevBytes, prevMime := rec.AvatarBytes, rec.AvatarMime
	rec.AvatarBytes = append([]byte{}, avatar...)
	mimeCopy := mime
	rec.AvatarMime = &mimeCopy
	rec.UpdatedAt = m.nowMs()
	m.profiles[profileID] = rec
	if err := m.persistLocked(); err != nil {
		rec.AvatarBytes, rec.AvatarMime = prevBytes, prevMime
		m.profiles[profileID] = rec
		return Profile{}, err
	}
	return toProfile(rec), nil
}

func (m *Manager) persistLocked() error {
	if m.storagePath == "" {
		return nil
	}
	doc := ledgerDocument{Version: ledgerVersion, Profiles: make([]record, 0, len(m.profiles))}
	for _, rec := range m.profiles {
		doc.Profiles = append(doc.Profiles, cloneRecord(rec))
	}
	sort.Slice(doc.Profiles, func(i, j int) bool {
		if doc.Profiles[i].CreatedAt == doc.Profiles[j].CreatedAt {
			return doc.Profiles[i].ID < doc.Profiles[j].ID
		}
		return doc.Profiles[i].CreatedAt < doc.Profiles[j].CreatedAt
	})
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode user profile ledger: %w", err)
	}
	dir := filepath.Dir(m.storagePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create user profile ledger directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".user-profile-ledger-*.tmp")
	if err != nil {
		return fmt.Errorf("create user profile ledger temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, m.storagePath); err != nil {
		return fmt.Errorf("replace user profile ledger: %w", err)
	}
	return nil
}

func cloneRecord(rec record) record {
	out := rec
	if rec.DisplayName != nil {
		v := *rec.DisplayName
		out.DisplayName = &v
	}
	if rec.AvatarMime != nil {
		v := *rec.AvatarMime
		out.AvatarMime = &v
	}
	if rec.AvatarBytes != nil {
		out.AvatarBytes = append([]byte{}, rec.AvatarBytes...)
	}
	if rec.Emails != nil {
		out.Emails = append([]string{}, rec.Emails...)
	}
	return out
}

func toProfile(rec record) Profile {
	emails := rec.Emails
	if emails == nil {
		emails = []string{}
	} else {
		emails = append([]string{}, emails...)
	}
	p := Profile{
		ID:        rec.ID,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
		Emails:    emails,
		HasAvatar: len(rec.AvatarBytes) > 0,
	}
	if rec.DisplayName != nil {
		v := *rec.DisplayName
		p.DisplayName = &v
	}
	if rec.AvatarMime != nil {
		v := *rec.AvatarMime
		p.AvatarMime = &v
	}
	return p
}
