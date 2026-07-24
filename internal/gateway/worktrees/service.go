// Package worktrees implements the git worktree lifecycle backing the
// gateway worktrees.* methods (WS-A/A7). Managed worktrees are created under a
// per-repo container directory and tracked in an on-disk JSON registry so the
// list/remove/restore/gc surface survives daemon restarts. All git work goes
// through the git CLI; no cgo or libgit2 dependency is introduced.
package worktrees

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Record is one managed worktree. The JSON tags match the OpenClaw
// WorktreeRecord wire contract so the Web UI can consume it unchanged.
type Record struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	RepoFingerprint string `json:"repoFingerprint"`
	RepoRoot        string `json:"repoRoot"`
	Path            string `json:"path"`
	Branch          string `json:"branch"`
	BaseRef         string `json:"baseRef"`
	OwnerKind       string `json:"ownerKind"`
	OwnerID         string `json:"ownerId,omitempty"`
	SnapshotRef     string `json:"snapshotRef,omitempty"`
	CreatedAt       int64  `json:"createdAt"`
	LastActiveAt    int64  `json:"lastActiveAt"`
	RemovedAt       int64  `json:"removedAt,omitempty"`
}

// Branch is one repository branch as reported by Branches.
type Branch struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// BranchListing is the result of Branches.
type BranchListing struct {
	Branches         []Branch `json:"branches"`
	DefaultBranch    string   `json:"defaultBranch,omitempty"`
	HeadBranch       string   `json:"headBranch,omitempty"`
	RepositoryStatus string   `json:"repositoryStatus,omitempty"`
}

// RemoveResult reports the outcome of Remove.
type RemoveResult struct {
	Removed       bool   `json:"removed"`
	SnapshotRef   string `json:"snapshotRef,omitempty"`
	SnapshotError string `json:"snapshotError,omitempty"`
}

// GcResult reports the outcome of Gc.
type GcResult struct {
	Removed         []string `json:"removed"`
	OrphansDeleted  int      `json:"orphansDeleted"`
	SnapshotsPruned int      `json:"snapshotsPruned"`
}

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	// ErrNotGit reports that the requested repo root is not a git repository.
	ErrNotGit = errors.New("path is not a git repository")
)

// Service manages worktrees rooted under a container directory.
type Service struct {
	mu        sync.Mutex
	container string // directory holding managed worktrees + registry.json
	now       func() time.Time
}

// NewService returns a Service that stores worktrees and its registry under
// containerDir. The directory is created lazily on first mutation.
func NewService(containerDir string) *Service {
	return &Service{container: containerDir, now: time.Now}
}

func (s *Service) registryPath() string { return filepath.Join(s.container, "registry.json") }

func (s *Service) loadLocked() ([]Record, error) {
	raw, err := os.ReadFile(s.registryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return []Record{}, nil
		}
		return nil, err
	}
	var out []Record
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("worktree registry corrupt: %w", err)
	}
	return out, nil
}

func (s *Service) saveLocked(records []Record) error {
	if err := os.MkdirAll(s.container, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.registryPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.registryPath())
}

// List returns all live (non-removed) managed worktree records.
func (s *Service) List(ctx context.Context) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(records))
	for _, r := range records {
		if r.RemovedAt == 0 {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

// CreateParams describes a new worktree.
type CreateParams struct {
	RepoRoot string
	Name     string
	BaseRef  string
}

// Create adds a new managed worktree branched from BaseRef (default HEAD).
func (s *Service) Create(ctx context.Context, p CreateParams) (Record, error) {
	repoRoot, err := resolveRepoRoot(ctx, p.RepoRoot)
	if err != nil {
		return Record{}, err
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = fmt.Sprintf("wt-%d", s.now().UnixNano())
	}
	if !nameRe.MatchString(name) {
		return Record{}, fmt.Errorf("invalid worktree name %q", name)
	}
	baseRef := strings.TrimSpace(p.BaseRef)
	if baseRef == "" {
		baseRef = "HEAD"
	}
	// baseRef is a positional git arg; reject leading-dash values so a hostile
	// ref cannot be reinterpreted as a git option (option injection).
	if strings.HasPrefix(baseRef, "-") {
		return Record{}, fmt.Errorf("invalid baseRef %q", baseRef)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked()
	if err != nil {
		return Record{}, err
	}
	fingerprint := repoFingerprint(repoRoot)
	for _, r := range records {
		if r.RemovedAt == 0 && r.RepoFingerprint == fingerprint && r.Name == name {
			return Record{}, fmt.Errorf("worktree %q already exists for this repository", name)
		}
	}
	id := fmt.Sprintf("wt_%s_%s", fingerprint, name)
	wtPath := filepath.Join(s.container, fingerprint, name)
	branch := "worktree/" + name
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return Record{}, err
	}
	if _, err := runGit(ctx, repoRoot, "worktree", "add", "-b", branch, wtPath, baseRef); err != nil {
		return Record{}, err
	}
	nowMs := s.now().UnixMilli()
	rec := Record{
		ID:              id,
		Name:            name,
		RepoFingerprint: fingerprint,
		RepoRoot:        repoRoot,
		Path:            wtPath,
		Branch:          branch,
		BaseRef:         baseRef,
		OwnerKind:       "manual",
		CreatedAt:       nowMs,
		LastActiveAt:    nowMs,
	}
	records = append(records, rec)
	if err := s.saveLocked(records); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Remove deletes a managed worktree. When force is false and the worktree has
// uncommitted changes, git refuses and the error is returned so the caller can
// retry with force. A best-effort snapshot ref is captured before removal.
func (s *Service) Remove(ctx context.Context, id string, force bool) (RemoveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked()
	if err != nil {
		return RemoveResult{}, err
	}
	idx := indexByID(records, id)
	if idx < 0 || records[idx].RemovedAt != 0 {
		return RemoveResult{}, fmt.Errorf("worktree %q not found", id)
	}
	rec := records[idx]
	res := RemoveResult{}
	if snapshot, snapErr := snapshotWorktree(ctx, rec); snapErr == nil && snapshot != "" {
		res.SnapshotRef = snapshot
		rec.SnapshotRef = snapshot
	} else if snapErr != nil {
		res.SnapshotError = snapErr.Error()
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, rec.Path)
	if _, err := runGit(ctx, rec.RepoRoot, args...); err != nil {
		return RemoveResult{}, err
	}
	rec.RemovedAt = s.now().UnixMilli()
	records[idx] = rec
	if err := s.saveLocked(records); err != nil {
		return RemoveResult{}, err
	}
	res.Removed = true
	return res, nil
}

// Restore recreates a previously removed worktree from its snapshot ref (or the
// recorded base ref when no snapshot exists).
func (s *Service) Restore(ctx context.Context, id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked()
	if err != nil {
		return Record{}, err
	}
	idx := indexByID(records, id)
	if idx < 0 {
		return Record{}, fmt.Errorf("worktree %q not found", id)
	}
	rec := records[idx]
	if rec.RemovedAt == 0 {
		return rec, nil
	}
	ref := rec.SnapshotRef
	if ref == "" {
		ref = rec.BaseRef
	}
	if err := os.MkdirAll(filepath.Dir(rec.Path), 0o755); err != nil {
		return Record{}, err
	}
	if _, err := runGit(ctx, rec.RepoRoot, "worktree", "add", "-B", rec.Branch, rec.Path, ref); err != nil {
		return Record{}, err
	}
	rec.RemovedAt = 0
	rec.LastActiveAt = s.now().UnixMilli()
	records[idx] = rec
	if err := s.saveLocked(records); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Gc prunes stale git worktree metadata and drops removed registry records.
func (s *Service) Gc(ctx context.Context) (GcResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadLocked()
	if err != nil {
		return GcResult{}, err
	}
	res := GcResult{Removed: []string{}}
	repos := map[string]struct{}{}
	kept := make([]Record, 0, len(records))
	for _, r := range records {
		repos[r.RepoRoot] = struct{}{}
		if r.RemovedAt != 0 {
			res.Removed = append(res.Removed, r.ID)
			res.OrphansDeleted++
			continue
		}
		// Drop records whose worktree directory vanished under us.
		if _, statErr := os.Stat(r.Path); statErr != nil {
			res.Removed = append(res.Removed, r.ID)
			res.OrphansDeleted++
			continue
		}
		kept = append(kept, r)
	}
	for repo := range repos {
		if _, pruneErr := runGit(ctx, repo, "worktree", "prune"); pruneErr == nil {
			res.SnapshotsPruned++
		}
	}
	if err := s.saveLocked(kept); err != nil {
		return GcResult{}, err
	}
	return res, nil
}

// Branches lists the local and remote branches for repoRoot.
func (s *Service) Branches(ctx context.Context, repoRootIn string, includeStatus bool) (BranchListing, error) {
	repoRoot, err := resolveRepoRoot(ctx, repoRootIn)
	if err != nil {
		if includeStatus && errors.Is(err, ErrNotGit) {
			return BranchListing{Branches: []Branch{}, RepositoryStatus: "not_git"}, nil
		}
		return BranchListing{}, err
	}
	out := BranchListing{Branches: []Branch{}}
	if includeStatus {
		out.RepositoryStatus = "git"
	}
	locals, err := runGit(ctx, repoRoot, "branch", "--format=%(refname:short)")
	if err != nil {
		return BranchListing{}, err
	}
	for _, name := range splitLines(locals) {
		out.Branches = append(out.Branches, Branch{Name: name, Kind: "local"})
	}
	remotes, err := runGit(ctx, repoRoot, "branch", "-r", "--format=%(refname:short)")
	if err == nil {
		for _, name := range splitLines(remotes) {
			if strings.Contains(name, "HEAD") {
				continue
			}
			out.Branches = append(out.Branches, Branch{Name: name, Kind: "remote"})
		}
	}
	if head, err := runGit(ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		out.HeadBranch = strings.TrimSpace(head)
	}
	if def, err := runGit(ctx, repoRoot, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		out.DefaultBranch = strings.TrimPrefix(strings.TrimSpace(def), "origin/")
	} else if out.HeadBranch != "" {
		out.DefaultBranch = out.HeadBranch
	}
	return out, nil
}

func indexByID(records []Record, id string) int {
	for i, r := range records {
		if r.ID == id {
			return i
		}
	}
	return -1
}

func snapshotWorktree(ctx context.Context, rec Record) (string, error) {
	// git stash create returns a commit that captures the working tree without
	// touching the index/stash stack; empty output means nothing to snapshot.
	out, err := runGit(ctx, rec.Path, "stash", "create", "metiq worktree snapshot")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func resolveRepoRoot(ctx context.Context, in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", fmt.Errorf("repoRoot is required")
	}
	abs, err := filepath.Abs(in)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", ErrNotGit
	}
	top, err := runGit(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ErrNotGit
	}
	return strings.TrimSpace(top), nil
}

func repoFingerprint(repoRoot string) string {
	sum := sha256.Sum256([]byte(repoRoot))
	return hex.EncodeToString(sum[:])[:16]
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
