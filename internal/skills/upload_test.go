package skills

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"metiq/internal/store/state"
)

// makeSkillZip builds an in-memory zip archive from path→content entries.
func makeSkillZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestUploadBeginChunkCommitHappyPath(t *testing.T) {
	isolatedWorkspace(t) // points METIQ_MANAGED_SKILLS_DIR at a temp dir
	archive := makeSkillZip(t, map[string]string{"newskill/SKILL.md": validSkillDraft})
	sum := sha256Of(archive)
	store := NewUploadStore()

	begin, err := store.Begin(UploadBeginInput{
		Kind:      "skill-archive",
		Slug:      "newskill",
		SizeBytes: int64(len(archive)),
		Sha256:    sum,
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	uploadID, _ := begin["uploadId"].(string)
	if uploadID == "" {
		t.Fatalf("expected uploadId, got %#v", begin)
	}
	if rb, _ := begin["receivedBytes"].(int64); rb != 0 {
		t.Fatalf("expected receivedBytes 0, got %#v", begin["receivedBytes"])
	}

	half := len(archive) / 2
	if half == 0 {
		t.Fatal("archive too small to split")
	}

	// Non-sequential offset is rejected before any bytes land.
	if _, err := store.Chunk(uploadID, 5, archive[:half]); err == nil {
		t.Fatal("expected non-sequential offset error")
	}

	got, err := store.Chunk(uploadID, 0, archive[:half])
	if err != nil {
		t.Fatalf("Chunk 1: %v", err)
	}
	if rb, _ := got["receivedBytes"].(int64); rb != int64(half) {
		t.Fatalf("expected receivedBytes %d, got %#v", half, got["receivedBytes"])
	}

	// Replaying the first offset now conflicts with the advanced cursor.
	if _, err := store.Chunk(uploadID, 0, archive[half:]); err == nil {
		t.Fatal("expected offset conflict on replay")
	}

	got, err = store.Chunk(uploadID, int64(half), archive[half:])
	if err != nil {
		t.Fatalf("Chunk 2: %v", err)
	}
	if rb, _ := got["receivedBytes"].(int64); rb != int64(len(archive)) {
		t.Fatalf("expected full receivedBytes, got %#v", got["receivedBytes"])
	}

	commit, err := store.Commit(uploadID, "")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if commit["sha256"] != sum {
		t.Fatalf("expected sha256 %s, got %#v", sum, commit["sha256"])
	}
	if sz, _ := commit["sizeBytes"].(int64); sz != int64(len(archive)) {
		t.Fatalf("unexpected sizeBytes: %#v", commit["sizeBytes"])
	}
	verdicts, _ := commit["verdicts"].(map[string]any)
	if valid, _ := verdicts["valid"].(bool); !valid {
		t.Fatalf("expected valid verdicts, got %#v", verdicts)
	}
}

func TestUploadCommitRejectsIncompleteAndBadSha(t *testing.T) {
	isolatedWorkspace(t)
	archive := makeSkillZip(t, map[string]string{"newskill/SKILL.md": validSkillDraft})
	store := NewUploadStore()

	// Incomplete: no chunks uploaded.
	begin, err := store.Begin(UploadBeginInput{Kind: "skill-archive", Slug: "newskill", SizeBytes: int64(len(archive))})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := store.Commit(begin["uploadId"].(string), ""); err == nil {
		t.Fatal("expected incomplete-upload error")
	}

	// sha256 mismatch at commit.
	begin2, err := store.Begin(UploadBeginInput{
		Kind:      "skill-archive",
		Slug:      "newskill",
		SizeBytes: int64(len(archive)),
		Sha256:    strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("Begin2: %v", err)
	}
	if _, err := store.Chunk(begin2["uploadId"].(string), 0, archive); err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if _, err := store.Commit(begin2["uploadId"].(string), ""); err == nil {
		t.Fatal("expected sha256 mismatch error")
	}
}

func TestUploadChunkRejectsOversizeAndUnknown(t *testing.T) {
	isolatedWorkspace(t)
	store := NewUploadStore()

	begin, err := store.Begin(UploadBeginInput{Kind: "skill-archive", Slug: "tiny", SizeBytes: 4})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// Chunk larger than the declared total size is rejected.
	if _, err := store.Chunk(begin["uploadId"].(string), 0, []byte("hello world")); err == nil {
		t.Fatal("expected chunk-exceeds-size error")
	}
	// Unknown upload id resolves to not-found.
	if _, err := store.Chunk("upl_missing", 0, []byte("x")); err != state.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUploadCommitRejectsFailedScan(t *testing.T) {
	isolatedWorkspace(t)
	// Broken SKILL.md (missing description) → lint error → scan rejects commit.
	archive := makeSkillZip(t, map[string]string{"broken/SKILL.md": "---\nname: broken\n---\n# Broken\n"})
	store := NewUploadStore()

	begin, err := store.Begin(UploadBeginInput{Kind: "skill-archive", Slug: "broken", SizeBytes: int64(len(archive))})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := store.Chunk(begin["uploadId"].(string), 0, archive); err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	_, err = store.Commit(begin["uploadId"].(string), "")
	if err == nil || !strings.Contains(err.Error(), "security scan") {
		t.Fatalf("expected security-scan rejection, got %v", err)
	}
}

func TestUploadEmptyArchiveScanRejected(t *testing.T) {
	isolatedWorkspace(t)
	// A zip with no SKILL.md manifest must be rejected at commit.
	archive := makeSkillZip(t, map[string]string{"readme.txt": "not a skill"})
	store := NewUploadStore()

	begin, err := store.Begin(UploadBeginInput{Kind: "skill-archive", Slug: "empty", SizeBytes: int64(len(archive))})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := store.Chunk(begin["uploadId"].(string), 0, archive); err != nil {
		t.Fatalf("Chunk: %v", err)
	}
	if _, err := store.Commit(begin["uploadId"].(string), ""); err == nil {
		t.Fatal("expected no-manifest rejection")
	}
}

func TestUploadBeginIdempotencyAndValidation(t *testing.T) {
	isolatedWorkspace(t)
	store := NewUploadStore()

	first, err := store.Begin(UploadBeginInput{Kind: "skill-archive", Slug: "newskill", SizeBytes: 128, IdempotencyKey: "abc"})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	second, err := store.Begin(UploadBeginInput{Kind: "skill-archive", Slug: "newskill", SizeBytes: 128, IdempotencyKey: "abc"})
	if err != nil {
		t.Fatalf("Begin replay: %v", err)
	}
	if first["uploadId"] != second["uploadId"] {
		t.Fatalf("expected idempotent replay to reuse uploadId: %v vs %v", first["uploadId"], second["uploadId"])
	}

	if _, err := store.Begin(UploadBeginInput{Kind: "not-a-skill", Slug: "x", SizeBytes: 1}); err == nil {
		t.Fatal("expected unsupported-kind error")
	}
	if _, err := store.Begin(UploadBeginInput{Kind: "skill-archive", Slug: "Bad Slug", SizeBytes: 1}); err == nil {
		t.Fatal("expected invalid-slug error")
	}
	if _, err := store.Begin(UploadBeginInput{Kind: "skill-archive", Slug: "ok", SizeBytes: 0}); err == nil {
		t.Fatal("expected invalid-size error")
	}
}

func TestUploadExpiredChunkRejected(t *testing.T) {
	isolatedWorkspace(t)
	store := NewUploadStore()

	begin, err := store.Begin(UploadBeginInput{Kind: "skill-archive", Slug: "newskill", SizeBytes: 128})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	uploadID := begin["uploadId"].(string)

	// Force the record's TTL into the past, then confirm chunking rejects it.
	rec, err := store.loadRecord(uploadID)
	if err != nil {
		t.Fatalf("loadRecord: %v", err)
	}
	rec.ExpiresAtMs = nowMillis() - 1
	if err := store.writeRecord(rec); err != nil {
		t.Fatalf("writeRecord: %v", err)
	}
	if _, err := store.Chunk(uploadID, 0, []byte("x")); err == nil {
		t.Fatal("expected expired-upload error")
	}
}
