package skills

// upload.go — Chunked skill-archive upload staging (skills.upload.begin /
// skills.upload.chunk / skills.upload.commit gateway methods, swarmstr-xfny.2).
//
// Uploads are staged file-backed under the managed skills dir tmp tree:
//
//	<managed>/tmp/skill-uploads/<uploadId>/upload.json   the upload record
//	<managed>/tmp/skill-uploads/<uploadId>/data.bin      the appended archive
//
// begin reserves a slot (max 32 active, 1h TTL) and validates the declared size;
// chunk appends canonical-decoded bytes at strictly sequential offsets under a
// 4 MiB per-chunk cap; commit verifies the declared size + sha256 and runs the
// skill linter (security verdicts) over the extracted archive, rejecting on lint
// errors. skills.install source=upload consumes committed uploads separately.
//
// Mirrors OpenClaw src/skills/lifecycle/upload-store.ts semantics.

import (
	"archive/zip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

const (
	uploadStateVersion = 1
	uploadRecordFile   = "upload.json"
	uploadDataFile     = "data.bin"
	uploadDirName      = "skill-uploads"

	// SkillUploadTTL bounds an idle staged upload before it is purged.
	SkillUploadTTL = time.Hour
	// MaxActiveSkillUploads bounds the number of concurrent staged uploads.
	MaxActiveSkillUploads = 32

	// MaxSkillArchiveBytes bounds one staged skill archive (256 MiB), mirroring
	// OpenClaw DEFAULT_MAX_ARCHIVE_BYTES_ZIP.
	MaxSkillArchiveBytes int64 = 256 * 1024 * 1024
	// MaxSkillUploadChunkBytes bounds one uploaded chunk (4 MiB).
	MaxSkillUploadChunkBytes int64 = 4 * 1024 * 1024
	// maxSkillArchiveExtractedBytes bounds total bytes extracted during a scan.
	maxSkillArchiveExtractedBytes int64 = 1 << 30
	// maxSkillArchiveFiles bounds extracted entry count during a scan.
	maxSkillArchiveFiles = 10_000
)

var (
	skillArchiveSha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	uploadLocks               sync.Map // absolute record path -> *sync.Mutex
)

// uploadRecord is the persisted per-upload metadata document.
type uploadRecord struct {
	Version        int    `json:"version"`
	UploadID       string `json:"uploadId"`
	Kind           string `json:"kind"`
	Slug           string `json:"slug"`
	SizeBytes      int64  `json:"sizeBytes"`
	DeclaredSha256 string `json:"declaredSha256,omitempty"`
	ReceivedBytes  int64  `json:"receivedBytes"`
	Force          bool   `json:"force,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	CreatedAtMs    int64  `json:"createdAtMs"`
	ExpiresAtMs    int64  `json:"expiresAtMs"`
	Committed      bool   `json:"committed"`
	CommitSha256   string `json:"commitSha256,omitempty"`
}

// UploadBeginInput carries the validated begin request fields.
type UploadBeginInput struct {
	Kind           string
	Slug           string
	SizeBytes      int64
	Sha256         string
	Force          bool
	IdempotencyKey string
}

// UploadStore is a file-backed staged-upload store rooted at the managed dir.
type UploadStore struct {
	root string
}

// NewUploadStore returns an upload store rooted under the managed skills dir tmp.
func NewUploadStore() *UploadStore {
	return &UploadStore{root: filepath.Join(ManagedSkillsDir(), "tmp", uploadDirName)}
}

func (s *UploadStore) uploadDir(id string) string { return filepath.Join(s.root, id) }
func (s *UploadStore) recordPath(id string) string {
	return filepath.Join(s.uploadDir(id), uploadRecordFile)
}
func (s *UploadStore) dataPath(id string) string {
	return filepath.Join(s.uploadDir(id), uploadDataFile)
}

func (s *UploadStore) uploadLock(id string) *sync.Mutex {
	key := s.recordPath(strings.TrimSpace(id))
	lock, _ := uploadLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func newUploadID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "upl_" + hex.EncodeToString(buf), nil
}

func (s *UploadStore) loadRecord(id string) (uploadRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return uploadRecord{}, fmt.Errorf("uploadId is required")
	}
	raw, err := os.ReadFile(s.recordPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return uploadRecord{}, state.ErrNotFound
		}
		return uploadRecord{}, err
	}
	var rec uploadRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return uploadRecord{}, err
	}
	return rec, nil
}

func (s *UploadStore) writeRecord(rec uploadRecord) error {
	rec.Version = uploadStateVersion
	if err := os.MkdirAll(s.uploadDir(rec.UploadID), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := s.recordPath(rec.UploadID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.recordPath(rec.UploadID))
}

// listActive loads all upload records, purging any that have passed their TTL,
// and returns the still-active records.
func (s *UploadStore) listActive(nowMs int64) ([]uploadRecord, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	active := make([]uploadRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rec, loadErr := s.loadRecord(entry.Name())
		if loadErr != nil {
			continue
		}
		if rec.ExpiresAtMs > 0 && nowMs >= rec.ExpiresAtMs {
			_ = os.RemoveAll(s.uploadDir(rec.UploadID))
			continue
		}
		active = append(active, rec)
	}
	return active, nil
}

func beginResult(rec uploadRecord) map[string]any {
	return map[string]any{
		"uploadId":      rec.UploadID,
		"slug":          rec.Slug,
		"kind":          rec.Kind,
		"sizeBytes":     rec.SizeBytes,
		"receivedBytes": rec.ReceivedBytes,
		"expiresAt":     rec.ExpiresAtMs,
	}
}

// Begin reserves a new staged upload (or replays an idempotent one).
func (s *UploadStore) Begin(in UploadBeginInput) (map[string]any, error) {
	kind := strings.TrimSpace(in.Kind)
	if kind != "skill-archive" {
		return nil, fmt.Errorf("unsupported upload kind %q", in.Kind)
	}
	slug := normalizedSkillKey(in.Slug)
	if !skillKeyPattern.MatchString(slug) {
		return nil, fmt.Errorf("invalid skill slug")
	}
	if in.SizeBytes < 1 {
		return nil, fmt.Errorf("invalid sizeBytes")
	}
	if in.SizeBytes > MaxSkillArchiveBytes {
		return nil, fmt.Errorf("sizeBytes exceeds maximum archive size")
	}
	declared := strings.ToLower(strings.TrimSpace(in.Sha256))
	if declared != "" && !skillArchiveSha256Pattern.MatchString(declared) {
		return nil, fmt.Errorf("invalid sha256")
	}

	now := nowMillis()
	active, err := s.listActive(now)
	if err != nil {
		return nil, err
	}
	// Idempotent replay: an active, uncommitted upload with the same key wins.
	if key := strings.TrimSpace(in.IdempotencyKey); key != "" {
		for _, rec := range active {
			if rec.IdempotencyKey == key && !rec.Committed {
				return beginResult(rec), nil
			}
		}
	}
	if len(active) >= MaxActiveSkillUploads {
		return nil, fmt.Errorf("too many active uploads (max %d)", MaxActiveSkillUploads)
	}

	id, err := newUploadID()
	if err != nil {
		return nil, err
	}
	rec := uploadRecord{
		Version:        uploadStateVersion,
		UploadID:       id,
		Kind:           kind,
		Slug:           slug,
		SizeBytes:      in.SizeBytes,
		DeclaredSha256: declared,
		Force:          in.Force,
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
		CreatedAtMs:    now,
		ExpiresAtMs:    now + SkillUploadTTL.Milliseconds(),
	}
	if err := os.MkdirAll(s.uploadDir(id), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.dataPath(id), nil, 0o644); err != nil {
		return nil, err
	}
	if err := s.writeRecord(rec); err != nil {
		return nil, err
	}
	return beginResult(rec), nil
}

// Chunk appends one sequential chunk of decoded archive bytes.
func (s *UploadStore) Chunk(uploadID string, offset int64, data []byte) (map[string]any, error) {
	lock := s.uploadLock(uploadID)
	lock.Lock()
	defer lock.Unlock()

	if len(data) == 0 {
		return nil, fmt.Errorf("empty upload chunk")
	}
	if int64(len(data)) > MaxSkillUploadChunkBytes {
		return nil, fmt.Errorf("chunk exceeds maximum size")
	}
	now := nowMillis()
	rec, err := s.loadRecord(uploadID)
	if err != nil {
		return nil, err
	}
	if rec.Committed {
		return nil, fmt.Errorf("upload %q is already committed", rec.UploadID)
	}
	if rec.ExpiresAtMs > 0 && now >= rec.ExpiresAtMs {
		_ = os.RemoveAll(s.uploadDir(rec.UploadID))
		return nil, fmt.Errorf("upload %q has expired", rec.UploadID)
	}
	if offset != rec.ReceivedBytes {
		return nil, fmt.Errorf("non-sequential chunk offset: expected %d, got %d", rec.ReceivedBytes, offset)
	}
	if rec.ReceivedBytes+int64(len(data)) > rec.SizeBytes {
		return nil, fmt.Errorf("chunk exceeds declared size")
	}

	f, err := os.OpenFile(s.dataPath(rec.UploadID), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	written, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return nil, writeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	rec.ReceivedBytes += int64(written)
	if err := s.writeRecord(rec); err != nil {
		return nil, err
	}
	return map[string]any{
		"uploadId":      rec.UploadID,
		"receivedBytes": rec.ReceivedBytes,
		"expiresAt":     rec.ExpiresAtMs,
	}, nil
}

// Commit verifies the completed upload's size + sha256, scans the staged archive
// with the skill linter, and marks the upload committed. It rejects the commit
// when the archive fails the security scan (any lint error).
func (s *UploadStore) Commit(uploadID, sha256Hex string) (map[string]any, error) {
	lock := s.uploadLock(uploadID)
	lock.Lock()
	defer lock.Unlock()

	now := nowMillis()
	rec, err := s.loadRecord(uploadID)
	if err != nil {
		return nil, err
	}
	if rec.ExpiresAtMs > 0 && now >= rec.ExpiresAtMs {
		_ = os.RemoveAll(s.uploadDir(rec.UploadID))
		return nil, fmt.Errorf("upload %q has expired", rec.UploadID)
	}
	if rec.ReceivedBytes != rec.SizeBytes {
		return nil, fmt.Errorf("incomplete upload: received %d of %d bytes", rec.ReceivedBytes, rec.SizeBytes)
	}
	declared := strings.ToLower(strings.TrimSpace(sha256Hex))
	if declared == "" {
		declared = rec.DeclaredSha256
	}
	if declared != "" && !skillArchiveSha256Pattern.MatchString(declared) {
		return nil, fmt.Errorf("invalid sha256")
	}
	actual, err := hashFile(s.dataPath(rec.UploadID))
	if err != nil {
		return nil, err
	}
	if declared != "" && declared != actual {
		return nil, fmt.Errorf("sha256 mismatch: declared %s, actual %s", declared, actual)
	}

	verdicts, scanErr := scanSkillArchive(s.dataPath(rec.UploadID))
	if scanErr != nil {
		return nil, scanErr
	}
	if valid, _ := verdicts["valid"].(bool); !valid {
		blocked, _ := verdicts["blockedCount"].(int)
		return nil, fmt.Errorf("skill archive failed security scan (%d blocked manifest(s))", blocked)
	}

	rec.Committed = true
	rec.CommitSha256 = actual
	rec.DeclaredSha256 = actual
	if err := s.writeRecord(rec); err != nil {
		return nil, err
	}
	return map[string]any{
		"uploadId":      rec.UploadID,
		"slug":          rec.Slug,
		"receivedBytes": rec.ReceivedBytes,
		"sizeBytes":     rec.SizeBytes,
		"sha256":        actual,
		"expiresAt":     rec.ExpiresAtMs,
		"verdicts":      verdicts,
	}, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// scanSkillArchive extracts the staged zip to a temp dir and runs the skill
// linter over it, returning the security verdicts. It errors when the archive
// is unreadable or contains no SKILL.md manifest.
func scanSkillArchive(archivePath string) (map[string]any, error) {
	tmpRoot, err := os.MkdirTemp("", "metiq-skill-upload-scan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpRoot)

	if err := unpackSkillArchive(archivePath, tmpRoot); err != nil {
		return nil, err
	}
	verdicts := securityVerdictsFromPaths([]string{tmpRoot}, nil)
	items, _ := verdicts["items"].([]map[string]any)
	if len(items) == 0 {
		return nil, fmt.Errorf("skill archive contains no SKILL.md manifest")
	}
	return verdicts, nil
}

// unpackSkillArchive extracts a zip skill archive to destDir with zip-slip
// protection and extraction size/file-count caps.
func unpackSkillArchive(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open skill archive: %w", err)
	}
	defer r.Close()

	if len(r.File) > maxSkillArchiveFiles {
		return fmt.Errorf("skill archive exceeds maximum file count (%d)", maxSkillArchiveFiles)
	}
	var total int64
	for _, f := range r.File {
		dest, err := safeArchiveJoin(destDir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open archive entry %q: %w", f.Name, err)
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(rc, maxSkillArchiveExtractedBytes-total+1))
		rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return fmt.Errorf("extract archive entry %q: %w", f.Name, copyErr)
		}
		if closeErr != nil {
			return closeErr
		}
		total += n
		if total > maxSkillArchiveExtractedBytes {
			return fmt.Errorf("skill archive exceeds maximum extraction size (%d bytes)", maxSkillArchiveExtractedBytes)
		}
	}
	return nil
}

// safeArchiveJoin joins base and a slash archive name, rejecting traversal.
func safeArchiveJoin(base, name string) (string, error) {
	joined := filepath.Join(base, filepath.FromSlash(name))
	rel, err := filepath.Rel(base, joined)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("archive entry escapes extraction directory: %q", name)
	}
	return joined, nil
}
