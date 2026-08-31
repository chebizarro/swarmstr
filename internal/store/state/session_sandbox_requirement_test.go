package state

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/sandbox"
)

func TestSessionSandboxRequirementPersistsAndIsImmutable(t *testing.T) {
	ctx := context.Background()
	repo := NewDocsRepository(newFakeStateStore(), "author-pub")
	required, err := sandbox.NewSessionRequirement("operator", sandbox.CreatorSandboxRequired, "docker")
	if err != nil {
		t.Fatal(err)
	}
	doc := SessionDoc{Version: 1, SessionID: "session-required", SandboxRequirement: required}
	if _, err := repo.PutSession(ctx, doc.SessionID, doc); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetSession(ctx, doc.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SandboxRequirement != required {
		t.Fatalf("sandbox requirement did not persist: %+v", stored.SandboxRequirement)
	}

	stored.LastReplyAt = 42
	if _, err := repo.PutSession(ctx, doc.SessionID, stored); err != nil {
		t.Fatalf("ordinary session update rejected: %v", err)
	}
	inherited, err := sandbox.NewSessionRequirement("operator", sandbox.CreatorSandboxInherit, "")
	if err != nil {
		t.Fatal(err)
	}
	stored.SandboxRequirement = inherited
	if _, err := repo.PutSession(ctx, doc.SessionID, stored); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("sandbox requirement mutation accepted: %v", err)
	}
}
