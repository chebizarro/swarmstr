package checkpoint

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestRepositoryPersistsCASBranchesAndCleansTrimmedArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	var cleaned []string
	repo, err := OpenRepository(path, 700, func(ref ArtifactRef) error {
		cleaned = append(cleaned, ref.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	base := PersistParams{
		SessionKey: "s", SessionID: "sid", Reason: ReasonManual,
		Snapshot:       &Snapshot{SessionKey: "s", SessionID: "sid", EntryCount: 2, FirstEntry: "a", LastEntry: "b", ActiveLeafID: "b", GraphRevision: 4},
		PostEntryCount: 1, PostFirstEntry: "b", PostLastEntry: "b", PostLeafEntryID: "b", PostGraphRevision: 5,
		RetainedBytes: 400,
	}
	base.CheckpointID = "cp1"
	base.CreatedAt = 1
	base.SnapshotArtifact = &ArtifactRef{ID: "a1", Bytes: 300}
	if _, rev, err := repo.Persist(base, 0); err != nil || rev != 1 {
		t.Fatalf("persist first rev=%d err=%v", rev, err)
	}
	base.CheckpointID = "cp2"
	base.CreatedAt = 2
	base.SnapshotArtifact = &ArtifactRef{ID: "a2", Bytes: 300}
	if _, rev, err := repo.Persist(base, 1); err != nil || rev != 2 {
		t.Fatalf("persist second rev=%d err=%v", rev, err)
	}
	if len(repo.List("s")) != 1 || len(cleaned) != 1 || cleaned[0] != "a1" {
		t.Fatalf("retention list=%+v cleaned=%v", repo.List("s"), cleaned)
	}
	if _, _, err := repo.Persist(base, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected CAS conflict, got %v", err)
	}

	reopened, err := OpenRepository(path, 700, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get("s", "cp2"); got == nil || got.PreCompaction.LeafEntryID != "b" || got.PreCompaction.GraphRevision != 4 {
		t.Fatalf("reopened checkpoint = %+v", got)
	}
	if _, err := reopened.Branch("s", "cp2", "fork", "fork-id", 2); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get("fork", "cp2"); got == nil || got.SessionID != "fork-id" {
		t.Fatalf("fork checkpoint = %+v", got)
	}
	if _, rev, err := reopened.Restore("s", "cp2", 2); err != nil || rev != 3 {
		t.Fatalf("restore rev=%d err=%v", rev, err)
	}
}

func TestRepositoryDoesNotCleanArtifactStillReferencedByBranch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoints.json")
	var cleaned []string
	repo, err := OpenRepository(path, 500, func(ref ArtifactRef) error {
		cleaned = append(cleaned, ref.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	base := PersistParams{
		CheckpointID: "shared", SessionKey: "source", SessionID: "source-id", Reason: ReasonManual,
		Snapshot:         &Snapshot{SessionKey: "source", SessionID: "source-id", EntryCount: 1, LastEntry: "leaf", ActiveLeafID: "leaf"},
		SnapshotArtifact: &ArtifactRef{ID: "artifact", Bytes: 300}, RetainedBytes: 300, CreatedAt: 1,
	}
	if _, _, err := repo.Persist(base, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Branch("source", "shared", "branch", "branch-id", 1); err != nil {
		t.Fatal(err)
	}
	base.CheckpointID = "new"
	base.SnapshotArtifact = &ArtifactRef{ID: "new-artifact", Bytes: 300}
	base.CreatedAt = 2
	if _, _, err := repo.Persist(base, 1); err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 0 {
		t.Fatalf("shared branch artifact was cleaned: %v", cleaned)
	}
	if err := repo.DeleteSession("branch"); err != nil {
		t.Fatal(err)
	}
	if len(cleaned) != 1 || cleaned[0] != "artifact" {
		t.Fatalf("cleanup after final reference = %v", cleaned)
	}
}
