package grasp

import (
	"context"
	"testing"
	"time"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/testutil"
)

// validRepoAddr returns a valid NIP-34 repo address using the given pubkey.
func validRepoAddr(pubkey string) string {
	return "30617:" + pubkey + ":my-repo"
}

// ─── AnnounceRepo + ListRepos round-trip ─────────────────────────────────────

func TestAnnounceAndListRepos_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := AnnounceRepo(ctx, pool, kp.Keyer, []string{url}, Repo{
		ID:             "my-repo",
		Name:           "Test Repo",
		Description:    "A test repository",
		WebURLs:        []string{"https://github.com/test/repo"},
		CloneURLs:      []string{"https://github.com/test/repo.git"},
		Relays:         []string{"wss://relay.example"},
		EarliestCommit: "abc123",
		Maintainers:    []string{kp.PubKeyHex()},
		Labels:         []string{"go", "nostr"},
	})
	if err != nil {
		t.Fatalf("AnnounceRepo: %v", err)
	}

	repos, err := ListRepos(ctx, pool, []string{url}, kp.PubKeyHex(), 10)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Name != "Test Repo" {
		t.Errorf("name: %q", repos[0].Name)
	}
	if repos[0].Description != "A test repository" {
		t.Errorf("desc: %q", repos[0].Description)
	}
	if repos[0].ID != "my-repo" {
		t.Errorf("id: %q", repos[0].ID)
	}
}

func TestAnnounceRepo_MissingID(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := AnnounceRepo(ctx, pool, kp.Keyer, []string{url}, Repo{Name: "No ID"})
	if err == nil {
		t.Error("expected error for missing repo ID")
	}
}

func TestListRepos_Empty(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	repos, err := ListRepos(ctx, pool, []string{url}, kp.PubKeyHex(), 10)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repos))
	}
}

func TestListRepos_AllAuthors(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	AnnounceRepo(ctx, pool, kp.Keyer, []string{url}, Repo{ID: "r1", Name: "Repo 1"})

	repos, err := ListRepos(ctx, pool, []string{url}, "", 10) // empty pubkey = all
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) < 1 {
		t.Error("expected at least 1 repo")
	}
}

// ─── CreateIssue + ListIssues ────────────────────────────────────────────────

func TestCreateAndListIssues_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	repoAddr := validRepoAddr(kp.PubKeyHex())

	_, err := CreateIssue(ctx, pool, kp.Keyer, []string{url}, Issue{
		RepoAddr: repoAddr,
		Subject:  "Bug: something broken",
		Content:  "Steps to reproduce...",
		Labels:   []string{"bug"},
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	issues, err := ListIssues(ctx, pool, []string{url}, repoAddr, 10)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Subject != "Bug: something broken" {
		t.Errorf("subject: %q", issues[0].Subject)
	}
	if issues[0].Content != "Steps to reproduce..." {
		t.Errorf("content: %q", issues[0].Content)
	}
}

func TestCreateIssue_InvalidRepoAddr(t *testing.T) {
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)
	ctx := t.Context()

	_, err := CreateIssue(ctx, pool, kp.Keyer, nil, Issue{
		RepoAddr: "invalid",
		Content:  "test",
	})
	if err == nil {
		t.Error("expected error for invalid repo addr")
	}
}

func TestCreateIssue_EmptyContent(t *testing.T) {
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)
	ctx := t.Context()

	_, err := CreateIssue(ctx, pool, kp.Keyer, nil, Issue{
		RepoAddr: validRepoAddr(kp.PubKeyHex()),
		Content:  "",
	})
	if err == nil {
		t.Error("expected error for empty content")
	}
}

// ─── SubmitPatch ─────────────────────────────────────────────────────────────

func TestSubmitPatch_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	eventID, err := SubmitPatch(ctx, pool, kp.Keyer, []string{url}, Patch{
		RepoAddr: validRepoAddr(kp.PubKeyHex()),
		Content:  "From abc123\n--- a/file.go\n+++ b/file.go\n...",
		CommitID: "abc123def456",
	})
	if err != nil {
		t.Fatalf("SubmitPatch: %v", err)
	}
	if eventID == "" {
		t.Fatal("expected non-empty event ID")
	}
}

func TestSubmitPatch_EmptyContent(t *testing.T) {
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)
	ctx := t.Context()

	_, err := SubmitPatch(ctx, pool, kp.Keyer, nil, Patch{
		RepoAddr: validRepoAddr(kp.PubKeyHex()),
		Content:  "",
	})
	if err == nil {
		t.Error("expected error for empty content")
	}
}

// ─── CreatePR ────────────────────────────────────────────────────────────────

func TestCreatePR_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	eventID, err := CreatePR(ctx, pool, kp.Keyer, []string{url}, PR{
		RepoAddr:   validRepoAddr(kp.PubKeyHex()),
		Subject:    "Add feature X",
		Content:    "This PR adds feature X",
		CommitTip:  "abc123",
		CloneURLs:  []string{"https://github.com/fork/repo.git"},
		BranchName: "feature-x",
		MergeBase:  "def456",
		Labels:     []string{"enhancement"},
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if eventID == "" {
		t.Fatal("expected non-empty event ID")
	}
}

func TestCreatePR_InvalidRepoAddr(t *testing.T) {
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)
	ctx := t.Context()

	_, err := CreatePR(ctx, pool, kp.Keyer, nil, PR{
		RepoAddr: "bad",
		Content:  "test",
	})
	if err == nil {
		t.Error("expected error for invalid repo addr")
	}
}

// ─── publishEvent ────────────────────────────────────────────────────────────

func TestPublishEvent_Integration(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	evt := nostr.Event{
		Kind:      1,
		CreatedAt: nostr.Now(),
		Content:   "test note",
	}
	kp.SignEvent(t, &evt)

	id, err := publishEvent(ctx, pool, []string{url}, evt)
	if err != nil {
		t.Fatalf("publishEvent: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty event ID")
	}
}

func TestAnnounceRepo_PersonalFork(t *testing.T) {
	url := testutil.NewTestRelay(t)
	pool := nostr.NewPool(nostr.PoolOptions{})
	kp := testutil.NewTestKeyPair(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := AnnounceRepo(ctx, pool, kp.Keyer, []string{url}, Repo{
		ID:           "my-fork",
		PersonalFork: true,
	})
	if err != nil {
		t.Fatalf("AnnounceRepo: %v", err)
	}

	repos, err := ListRepos(ctx, pool, []string{url}, kp.PubKeyHex(), 10)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if !repos[0].PersonalFork {
		t.Error("expected PersonalFork=true")
	}
}
