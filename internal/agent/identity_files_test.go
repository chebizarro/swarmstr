package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspaceIdentityName_PrefersIdentityFileNameField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte(`
# IDENTITY.md

- **Name:** Relay
- **Emoji:** 🛰️
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveWorkspaceIdentityName(dir); got != "Relay" {
		t.Fatalf("ResolveWorkspaceIdentityName() = %q, want %q", got, "Relay")
	}
}

func TestResolveWorkspaceIdentityName_FallsBackToSoulHeading(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte(`
# SOUL.md - The Soul of Relay

Direct and useful.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveWorkspaceIdentityName(dir); got != "Relay" {
		t.Fatalf("ResolveWorkspaceIdentityName() = %q, want %q", got, "Relay")
	}
}

func TestResolveWorkspaceIdentityName_SupportsMultilineIdentityName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte(`
# IDENTITY.md

- **Name:**
  Relay
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveWorkspaceIdentityName(dir); got != "Relay" {
		t.Fatalf("ResolveWorkspaceIdentityName() = %q, want %q", got, "Relay")
	}
}

func TestResolveWorkspaceIdentityName_IgnoresTemplatePlaceholders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte(`
# IDENTITY.md - Who Am I?

- **Name:**
  _(pick something that fits)_
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveWorkspaceIdentityName(dir); got != "" {
		t.Fatalf("ResolveWorkspaceIdentityName() = %q, want empty", got)
	}
}
