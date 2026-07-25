// Package artifacts implements the workspace-backed artifact store surfaced by
// the gateway artifacts.* methods (WS-A/A7 deferred slice).
//
// Artifacts are files or payloads produced by sessions, runs, tasks, or
// agents. Blobs are content-addressed by SHA-256 under <dir>/blobs and
// described by one JSON metadata record per artifact under <dir>/meta.
// The store directory is opened through os.Root so every read and write is
// contained even if a record is ever tampered with on disk.
package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound reports a lookup for an unknown artifact id or an artifact that
// is not visible in the requested scope.
var ErrNotFound = errors.New("artifact not found")

const (
	blobsDir    = "blobs"
	metaDir     = "meta"
	idPrefix    = "art_"
	idHashChars = 32
	// MaxBlobBytes caps a single stored artifact payload (16 MiB). Download
	// responses are inline base64, so unbounded blobs would be transport-hostile.
	MaxBlobBytes = 16 << 20
)

// Summary is the public artifact projection returned by list/get/download.
// Field names mirror the OpenClaw ArtifactSummary wire shape.
type Summary struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Title      string   `json:"title"`
	MimeType   string   `json:"mimeType,omitempty"`
	SizeBytes  int64    `json:"sizeBytes,omitempty"`
	SessionKey string   `json:"sessionKey,omitempty"`
	RunID      string   `json:"runId,omitempty"`
	TaskID     string   `json:"taskId,omitempty"`
	AgentID    string   `json:"agentId,omitempty"`
	Source     string   `json:"source,omitempty"`
	Download   Download `json:"download"`
}

// Download describes how the artifact payload can be retrieved.
type Download struct {
	Mode string `json:"mode"` // "bytes" | "unsupported"
}

// record is the durable metadata document stored next to the blob.
type record struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	MimeType    string `json:"mimeType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes"`
	SHA256      string `json:"sha256"`
	SessionKey  string `json:"sessionKey,omitempty"`
	RunID       string `json:"runId,omitempty"`
	TaskID      string `json:"taskId,omitempty"`
	AgentID     string `json:"agentId,omitempty"`
	Source      string `json:"source,omitempty"`
	CreatedAtMs int64  `json:"createdAtMs"`
}

// Query filters artifact visibility. Empty fields match everything; set
// fields must match the stored metadata exactly.
type Query struct {
	SessionKey string
	RunID      string
	TaskID     string
	AgentID    string
}

func (q Query) matches(r record) bool {
	if q.SessionKey != "" && q.SessionKey != r.SessionKey {
		return false
	}
	if q.RunID != "" && q.RunID != r.RunID {
		return false
	}
	if q.TaskID != "" && q.TaskID != r.TaskID {
		return false
	}
	if q.AgentID != "" && q.AgentID != r.AgentID {
		return false
	}
	return true
}

// PutRequest describes one artifact payload plus its scope metadata.
type PutRequest struct {
	Type       string
	Title      string
	MimeType   string
	SessionKey string
	RunID      string
	TaskID     string
	AgentID    string
	Source     string
	Data       []byte
}

// Store is an on-disk, content-addressed artifact store rooted at one
// directory. It is safe for concurrent use.
type Store struct {
	dir string
	now func() time.Time

	mu sync.Mutex
}

// NewStore returns a store rooted at dir. The directory is created lazily on
// first write.
func NewStore(dir string) *Store {
	return &Store{dir: strings.TrimSpace(dir), now: time.Now}
}

// SetNowFunc overrides the clock (tests only).
func (s *Store) SetNowFunc(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

func (s *Store) openRoot(create bool) (*os.Root, error) {
	if s.dir == "" {
		return nil, fmt.Errorf("artifact store directory is not configured")
	}
	if create {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			return nil, fmt.Errorf("create artifact store: %w", err)
		}
	}
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		if !create && errors.Is(err, fs.ErrNotExist) {
			return nil, nil // empty store
		}
		return nil, fmt.Errorf("open artifact store: %w", err)
	}
	return root, nil
}

// artifactIDForHash derives the public artifact id from a content hash.
func artifactIDForHash(hash string) string {
	return idPrefix + hash[:idHashChars]
}

// validArtifactID enforces the minted id alphabet so ids can never traverse
// outside the metadata directory.
func validArtifactID(id string) bool {
	if !strings.HasPrefix(id, idPrefix) || len(id) != len(idPrefix)+idHashChars {
		return false
	}
	for _, r := range id[len(idPrefix):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validBlobHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Put stores one artifact payload and its metadata, returning the public
// summary. Content is deduplicated by SHA-256: re-putting identical bytes
// refreshes the metadata record under the same artifact id.
func (s *Store) Put(req PutRequest) (Summary, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return Summary{}, fmt.Errorf("artifact title is required")
	}
	if len(req.Data) == 0 {
		return Summary{}, fmt.Errorf("artifact data is required")
	}
	if len(req.Data) > MaxBlobBytes {
		return Summary{}, fmt.Errorf("artifact exceeds %d bytes", MaxBlobBytes)
	}
	typ := strings.TrimSpace(req.Type)
	if typ == "" {
		typ = "file"
	}
	sum := sha256.Sum256(req.Data)
	hash := hex.EncodeToString(sum[:])
	rec := record{
		ID:          artifactIDForHash(hash),
		Type:        typ,
		Title:       title,
		MimeType:    strings.TrimSpace(req.MimeType),
		SizeBytes:   int64(len(req.Data)),
		SHA256:      hash,
		SessionKey:  strings.TrimSpace(req.SessionKey),
		RunID:       strings.TrimSpace(req.RunID),
		TaskID:      strings.TrimSpace(req.TaskID),
		AgentID:     strings.TrimSpace(req.AgentID),
		Source:      strings.TrimSpace(req.Source),
		CreatedAtMs: s.now().UnixMilli(),
	}
	meta, err := json.Marshal(rec)
	if err != nil {
		return Summary{}, fmt.Errorf("encode artifact metadata: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.openRoot(true)
	if err != nil {
		return Summary{}, err
	}
	defer root.Close()
	for _, dir := range []string{blobsDir, metaDir} {
		if err := root.Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return Summary{}, fmt.Errorf("create artifact store %s: %w", dir, err)
		}
	}
	blobPath := path.Join(blobsDir, hash)
	if _, err := root.Stat(blobPath); errors.Is(err, fs.ErrNotExist) {
		if err := writeRootFile(root, blobPath, req.Data); err != nil {
			return Summary{}, fmt.Errorf("write artifact blob: %w", err)
		}
	} else if err != nil {
		return Summary{}, fmt.Errorf("stat artifact blob: %w", err)
	}
	if err := writeRootFile(root, path.Join(metaDir, rec.ID+".json"), meta); err != nil {
		return Summary{}, fmt.Errorf("write artifact metadata: %w", err)
	}
	return rec.summary(), nil
}

// writeRootFile writes atomically inside the contained root via temp+rename.
func writeRootFile(root *os.Root, name string, data []byte) error {
	tmp := name + ".tmp"
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

func (r record) summary() Summary {
	return Summary{
		ID:         r.ID,
		Type:       r.Type,
		Title:      r.Title,
		MimeType:   r.MimeType,
		SizeBytes:  r.SizeBytes,
		SessionKey: r.SessionKey,
		RunID:      r.RunID,
		TaskID:     r.TaskID,
		AgentID:    r.AgentID,
		Source:     r.Source,
		Download:   Download{Mode: "bytes"},
	}
}

func (s *Store) loadRecord(root *os.Root, id string) (record, error) {
	if !validArtifactID(id) {
		return record{}, ErrNotFound
	}
	data, err := readRootFile(root, path.Join(metaDir, id+".json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return record{}, ErrNotFound
		}
		return record{}, fmt.Errorf("read artifact metadata: %w", err)
	}
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return record{}, fmt.Errorf("parse artifact metadata: %w", err)
	}
	if rec.ID != id {
		return record{}, ErrNotFound
	}
	return rec, nil
}

func readRootFile(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxBlobBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBlobBytes {
		return nil, fmt.Errorf("artifact store entry %s exceeds size cap", name)
	}
	return data, nil
}

// List returns summaries for every artifact visible in the query scope,
// ordered newest-first (ties broken by id for determinism).
func (s *Store) List(q Query) ([]Summary, error) {
	root, err := s.openRoot(false)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return []Summary{}, nil
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), metaDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Summary{}, nil
		}
		return nil, fmt.Errorf("list artifact metadata: %w", err)
	}
	type sortable struct {
		rec record
	}
	items := make([]sortable, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		rec, err := s.loadRecord(root, strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue // skip damaged records rather than failing the listing
		}
		if !q.matches(rec) {
			continue
		}
		items = append(items, sortable{rec: rec})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].rec.CreatedAtMs != items[j].rec.CreatedAtMs {
			return items[i].rec.CreatedAtMs > items[j].rec.CreatedAtMs
		}
		return items[i].rec.ID < items[j].rec.ID
	})
	out := make([]Summary, 0, len(items))
	for _, item := range items {
		out = append(out, item.rec.summary())
	}
	return out, nil
}

// Get returns one artifact summary when it exists and is visible in the scope.
func (s *Store) Get(id string, q Query) (Summary, error) {
	root, err := s.openRoot(false)
	if err != nil {
		return Summary{}, err
	}
	if root == nil {
		return Summary{}, ErrNotFound
	}
	defer root.Close()
	rec, err := s.loadRecord(root, strings.TrimSpace(id))
	if err != nil {
		return Summary{}, err
	}
	if !q.matches(rec) {
		return Summary{}, ErrNotFound
	}
	return rec.summary(), nil
}

// Download returns the artifact summary plus its raw payload bytes.
func (s *Store) Download(id string, q Query) (Summary, []byte, error) {
	root, err := s.openRoot(false)
	if err != nil {
		return Summary{}, nil, err
	}
	if root == nil {
		return Summary{}, nil, ErrNotFound
	}
	defer root.Close()
	rec, err := s.loadRecord(root, strings.TrimSpace(id))
	if err != nil {
		return Summary{}, nil, err
	}
	if !q.matches(rec) {
		return Summary{}, nil, ErrNotFound
	}
	if !validBlobHash(rec.SHA256) {
		return Summary{}, nil, fmt.Errorf("artifact %s has an invalid content hash", rec.ID)
	}
	data, err := readRootFile(root, path.Join(blobsDir, rec.SHA256))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Summary{}, nil, ErrNotFound
		}
		return Summary{}, nil, fmt.Errorf("read artifact blob: %w", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != rec.SHA256 {
		return Summary{}, nil, fmt.Errorf("artifact %s content hash mismatch", rec.ID)
	}
	return rec.summary(), data, nil
}
