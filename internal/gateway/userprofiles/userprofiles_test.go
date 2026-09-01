package userprofiles

import (
	"errors"
	"path/filepath"
	"testing"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManagerAt(filepath.Join(t.TempDir(), "user-profile-ledger.json"))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	// Deterministic monotonic clock so createdAt/updatedAt ordering is stable.
	var tick int64
	m.nowMs = func() int64 { tick++; return tick }
	return m
}

func TestEnsureForIdentityIsIdempotent(t *testing.T) {
	m := newTestManager(t)
	p1, err := m.EnsureForIdentity("pubkey-a")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if p1.ID != "pubkey-a" || p1.HasAvatar || len(p1.Emails) != 0 {
		t.Fatalf("unexpected profile: %+v", p1)
	}
	if p1.DisplayName != nil || p1.AvatarMime != nil || p1.MergedInto != nil {
		t.Fatalf("expected nil optionals: %+v", p1)
	}
	p2, err := m.EnsureForIdentity("pubkey-a")
	if err != nil {
		t.Fatalf("ensure2: %v", err)
	}
	if p2.CreatedAt != p1.CreatedAt {
		t.Fatalf("ensure should be idempotent: %d != %d", p2.CreatedAt, p1.CreatedAt)
	}
	if _, err := m.EnsureForIdentity("  "); err == nil {
		t.Fatal("expected error for blank identity")
	}
}

func TestListSortedByCreation(t *testing.T) {
	m := newTestManager(t)
	for _, id := range []string{"c", "a", "b"} {
		if _, err := m.EnsureForIdentity(id); err != nil {
			t.Fatal(err)
		}
	}
	list := m.List()
	if len(list) != 3 {
		t.Fatalf("expected 3, got %d", len(list))
	}
	if list[0].ID != "c" || list[1].ID != "a" || list[2].ID != "b" {
		t.Fatalf("expected creation order c,a,b got %s,%s,%s", list[0].ID, list[1].ID, list[2].ID)
	}
}

func TestLinkEmailLifecycle(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.LinkEmail("x@y.z", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := m.EnsureForIdentity("pk"); err != nil {
		t.Fatal(err)
	}
	p, err := m.LinkEmail("Alice@Example.com", "pk")
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	if len(p.Emails) != 1 || p.Emails[0] != "Alice@Example.com" {
		t.Fatalf("unexpected emails: %+v", p.Emails)
	}
	// Idempotent (case-insensitive) — no duplicate.
	p, err = m.LinkEmail("alice@example.com", "pk")
	if err != nil {
		t.Fatalf("link2: %v", err)
	}
	if len(p.Emails) != 1 {
		t.Fatalf("expected no duplicate email, got %+v", p.Emails)
	}
}

func TestSetDisplayNameSetAndClear(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.SetDisplayName("missing", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := m.EnsureForIdentity("pk"); err != nil {
		t.Fatal(err)
	}
	name := "  Ada Lovelace  "
	p, err := m.SetDisplayName("pk", &name)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if p.DisplayName == nil || *p.DisplayName != "Ada Lovelace" {
		t.Fatalf("expected trimmed name, got %v", p.DisplayName)
	}
	p, err = m.SetDisplayName("pk", nil)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if p.DisplayName != nil {
		t.Fatalf("expected cleared name, got %v", *p.DisplayName)
	}
}

func TestPreferencesAndRoleAreDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	m, err := NewManagerAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnsureForIdentity("pk"); err != nil {
		t.Fatal(err)
	}
	entries, err := m.SetPreferences("pk", map[string]any{"theme": "dark", "nested": map[string]any{"enabled": true}})
	if err != nil || entries["theme"] != "dark" {
		t.Fatalf("set preferences: entries=%v err=%v", entries, err)
	}
	entries, err = m.SetPreferences("pk", map[string]any{"theme": nil})
	if err != nil {
		t.Fatalf("delete preference: %v", err)
	}
	if _, ok := entries["theme"]; ok {
		t.Fatalf("null patch did not delete theme: %v", entries)
	}
	role := "operator"
	profile, err := m.SetRole("pk", &role)
	if err != nil || profile.Role == nil || *profile.Role != role {
		t.Fatalf("set role: profile=%+v err=%v", profile, err)
	}

	reloaded, err := NewManagerAt(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, err := reloaded.Preferences("pk", nil)
	if err != nil {
		t.Fatalf("preferences after reload: %v", err)
	}
	if _, ok := got["nested"]; !ok {
		t.Fatalf("preference missing after reload: %v", got)
	}
	profile, err = reloaded.Get("pk")
	if err != nil || profile.Role == nil || *profile.Role != role {
		t.Fatalf("role missing after reload: profile=%+v err=%v", profile, err)
	}
}

func TestSetAvatarValidationAndDurability(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	m, err := NewManagerAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnsureForIdentity("pk"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAvatar("pk", []byte{1, 2, 3}, "image/gif"); err == nil {
		t.Fatal("expected unsupported mime error")
	}
	if _, err := m.SetAvatar("pk", nil, "image/png"); err == nil {
		t.Fatal("expected empty avatar error")
	}
	p, err := m.SetAvatar("pk", []byte{0x89, 0x50, 0x4e, 0x47}, "image/png")
	if err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	if !p.HasAvatar || p.AvatarMime == nil || *p.AvatarMime != "image/png" {
		t.Fatalf("unexpected avatar projection: %+v", p)
	}

	// Durable reload: a fresh manager over the same ledger restores the avatar.
	reloaded, err := NewManagerAt(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, err := reloaded.Get("pk")
	if err != nil {
		t.Fatalf("get after reload: %v", err)
	}
	if !got.HasAvatar || got.AvatarMime == nil || *got.AvatarMime != "image/png" {
		t.Fatalf("avatar not durable: %+v", got)
	}
	if _, err := reloaded.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
