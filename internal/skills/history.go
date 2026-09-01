package skills

// history.go — workspace skill-change history scan (skills.proposals.historyStatus
// / skills.proposals.historyScan gateway methods, swarmstr-xfny.5).
//
// Metiq skills live at <workspace>/<key>/SKILL.md. When the agent workspace is a
// git working tree, the history of those SKILL.md files is a durable record of
// skill changes (added/modified/deleted/renamed). This module shells out to the
// git CLI over the workspace to surface that record, mirroring OpenClaw's
// skill-workshop history-scan (src/skills/workshop/history-scan*.ts).
//
// Security posture (no ClawHub; local git only):
//   - git is invoked with an explicit argument vector (exec.Command, never a
//     shell) so there is no command-injection surface.
//   - the only caller-supplied value threaded into git args is the paging cursor,
//     which is validated to a bare hex commit-ish before use, so it can neither be
//     interpreted as a flag nor smuggle extra revisions.
//   - the pathspec is pinned to the workspace subtree (cwd = workspace) and to
//     SKILL.md files only, so the scan cannot traverse outside the workspace.
//   - every scan is bounded: commit counts are clamped and newer-direction paging
//     caps the range walk, reporting truncation instead of walking unbounded.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"metiq/internal/store/state"
)

const (
	// skillHistoryPathspec matches SKILL.md at any depth within the workspace
	// subtree. The :(glob) magic makes ** span path components; anchoring to the
	// process working directory (the workspace) keeps the scan in-tree.
	skillHistoryPathspec = ":(glob)**/SKILL.md"

	// DefaultSkillHistoryScanLimit is the default number of commits per scan page.
	DefaultSkillHistoryScanLimit = 50
	// MaxSkillHistoryScanLimit bounds a single scan page.
	MaxSkillHistoryScanLimit = 200
	// maxNewerScanCommits bounds the range walk for newer-direction paging so an
	// enormous <cursor>..HEAD range cannot be walked unbounded.
	maxNewerScanCommits = 512
	// MaxSkillHistoryScanEntries hard-caps the change entries returned per page so a
	// single commit touching many SKILL.md files cannot produce an unbounded result.
	MaxSkillHistoryScanEntries = 2000
	// maxGitScanBytes caps a single scan's git output held in memory; output beyond
	// this is dropped and the page is reported truncated.
	maxGitScanBytes = 8 << 20
	// scanTimeout bounds any single history scan's git invocations.
	scanTimeout = 20 * time.Second

	gitCommitMarker = "METIQ_COMMIT"
)

// commitishRe validates a paging cursor as a bare hex commit-ish. Anything else
// (flags, ranges, refs, path fragments) is rejected before reaching git.
var commitishRe = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// SkillHistoryEntry is one SKILL.md change recorded in git history.
type SkillHistoryEntry struct {
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	Subject   string `json:"subject"`
	SkillPath string `json:"skillPath"`
	SkillKey  string `json:"skillKey"`
	Change    string `json:"change"` // added|modified|deleted|renamed|copied
}

// SkillHistoryStatus is the availability/summary report for the workspace skill
// history scan (skills.proposals.historyStatus).
type SkillHistoryStatus struct {
	Available          bool   `json:"available"`
	Reason             string `json:"reason,omitempty"`
	Workspace          string `json:"workspace"`
	RepoRoot           string `json:"repoRoot,omitempty"`
	Head               string `json:"head,omitempty"`
	Branch             string `json:"branch,omitempty"`
	Dirty              bool   `json:"dirty"`
	SkillFileCount     int    `json:"skillFileCount"`
	SkillChangeCommits int    `json:"skillChangeCommits"`
}

// SkillHistoryScanParams are the bounded paging parameters for a history scan.
type SkillHistoryScanParams struct {
	Direction string // "older" (default) or "newer"
	Cursor    string // commit-ish to page from (validated hex)
	Limit     int
}

// SkillHistoryScanResult is one bounded page of skill-change history.
type SkillHistoryScanResult struct {
	Available  bool                `json:"available"`
	Reason     string              `json:"reason,omitempty"`
	Head       string              `json:"head,omitempty"`
	Direction  string              `json:"direction"`
	Entries    []SkillHistoryEntry `json:"entries"`
	NextCursor string              `json:"nextCursor,omitempty"`
	HasMore    bool                `json:"hasMore"`
	Truncated  bool                `json:"truncated,omitempty"`
}

// runGitCapture executes git in dir with an explicit argument vector (no shell)
// and returns stdout, or an error carrying git's stderr. Used for small, fixed-
// size outputs (rev-parse, status, counts).
func runGitCapture(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// runGitScan executes git for a potentially large log scan, capping the buffered
// stdout at maxGitScanBytes. It returns (output, truncated, error); truncated is
// true when git produced more than the cap (the surplus is dropped and the
// process killed). A truncated read is not an error.
func runGitScan(ctx context.Context, dir string, args ...string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", false, err
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, maxGitScanBytes+1))
	truncated := len(data) > maxGitScanBytes
	if truncated {
		data = data[:maxGitScanBytes]
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if truncated {
		// Expected: we killed git after hitting the cap.
		return string(data), true, nil
	}
	if readErr != nil {
		return "", false, readErr
	}
	if waitErr != nil {
		return "", false, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return string(data), false, nil
}

// capEntries hard-caps a page's entries, reporting whether it truncated.
func capEntries(entries []SkillHistoryEntry) ([]SkillHistoryEntry, bool) {
	if len(entries) > MaxSkillHistoryScanEntries {
		return entries[:MaxSkillHistoryScanEntries], true
	}
	return entries, false
}

// resolveWorkspaceRepo resolves the workspace dir and its git repo root, or a
// human reason when the workspace is not a usable git working tree.
func resolveWorkspaceRepo(ctx context.Context, cfg state.ConfigDoc, agentID string) (workspaceDir, repoRoot, reason string) {
	workspaceDir = ResolveAgentWorkspaceDir(cfg, agentID)
	if strings.TrimSpace(workspaceDir) == "" {
		return "", "", "workspace directory is not configured"
	}
	top, err := runGitCapture(ctx, workspaceDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return workspaceDir, "", "workspace is not a git repository"
	}
	return workspaceDir, strings.TrimSpace(top), ""
}

// HistoryStatus reports whether a workspace skill-change history scan is
// available plus a bounded summary (HEAD, branch, dirty state, skill-file count,
// and the number of commits that touched skill files).
func HistoryStatus(ctx context.Context, cfg state.ConfigDoc, agentID string) (SkillHistoryStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	workspaceDir, repoRoot, reason := resolveWorkspaceRepo(ctx, cfg, agentID)
	if reason != "" {
		return SkillHistoryStatus{Available: false, Reason: reason, Workspace: workspaceDir}, nil
	}
	status := SkillHistoryStatus{Available: true, Workspace: workspaceDir, RepoRoot: repoRoot}

	// HEAD may be absent in a freshly-initialized repo with no commits.
	if head, err := runGitCapture(ctx, workspaceDir, "rev-parse", "HEAD"); err == nil {
		status.Head = strings.TrimSpace(head)
	} else {
		// No commits yet: available, but nothing to scan.
		status.Reason = "workspace has no commits yet"
		return status, nil
	}
	if branch, err := runGitCapture(ctx, workspaceDir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		status.Branch = strings.TrimSpace(branch)
	}
	if porcelain, err := runGitCapture(ctx, workspaceDir, "status", "--porcelain", "--", skillHistoryPathspec); err == nil {
		status.Dirty = strings.TrimSpace(porcelain) != ""
	}
	if files, err := runGitCapture(ctx, workspaceDir, "ls-files", "--", skillHistoryPathspec); err == nil {
		status.SkillFileCount = countNonEmptyLines(files)
	}
	if count, err := runGitCapture(ctx, workspaceDir, "rev-list", "--count", "HEAD", "--", skillHistoryPathspec); err == nil {
		status.SkillChangeCommits = atoiSafe(strings.TrimSpace(count))
	}
	return status, nil
}

// HistoryScan returns one bounded page of SKILL.md change history for the
// workspace. direction "older" (default) pages backward from the cursor toward
// the root; "newer" pages forward toward HEAD.
func HistoryScan(ctx context.Context, cfg state.ConfigDoc, agentID string, params SkillHistoryScanParams) (SkillHistoryScanResult, error) {
	direction := strings.ToLower(strings.TrimSpace(params.Direction))
	if direction == "" {
		direction = "older"
	}
	if direction != "older" && direction != "newer" {
		return SkillHistoryScanResult{}, fmt.Errorf("invalid direction %q: want \"older\" or \"newer\"", params.Direction)
	}
	cursor := strings.TrimSpace(params.Cursor)
	if cursor != "" && !commitishRe.MatchString(cursor) {
		return SkillHistoryScanResult{}, fmt.Errorf("invalid cursor: must be a hex commit id")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = DefaultSkillHistoryScanLimit
	}
	if limit > MaxSkillHistoryScanLimit {
		limit = MaxSkillHistoryScanLimit
	}

	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	workspaceDir, _, reason := resolveWorkspaceRepo(ctx, cfg, agentID)
	if reason != "" {
		return SkillHistoryScanResult{Available: false, Reason: reason, Direction: direction, Entries: []SkillHistoryEntry{}}, nil
	}
	head := ""
	if h, err := runGitCapture(ctx, workspaceDir, "rev-parse", "HEAD"); err == nil {
		head = strings.TrimSpace(h)
	} else {
		return SkillHistoryScanResult{Available: true, Reason: "workspace has no commits yet", Direction: direction, Head: "", Entries: []SkillHistoryEntry{}}, nil
	}

	result := SkillHistoryScanResult{Available: true, Direction: direction, Head: head, Entries: []SkillHistoryEntry{}}
	if direction == "newer" {
		return scanNewer(ctx, workspaceDir, cursor, limit, result)
	}
	return scanOlder(ctx, workspaceDir, cursor, limit, result)
}

// gitLogFormatArgs emits a fully NUL-delimited token stream. The marker separates
// commit headers from -z name-status tokens; -M enables rename detection.
var gitLogFormatArgs = []string{
	"--no-color", "-M", "--name-status", "-z",
	"--format=%x00" + gitCommitMarker + "%x00%H%x00%cI%x00%s",
}

func scanOlder(ctx context.Context, dir, cursor string, limit int, result SkillHistoryScanResult) (SkillHistoryScanResult, error) {
	rev := "HEAD"
	if cursor != "" {
		// Strictly older than the cursor: start the walk at the cursor's parent.
		rev = cursor + "~1"
	}
	args := append([]string{"log", rev, fmt.Sprintf("-n%d", limit+1)}, gitLogFormatArgs...)
	args = append(args, "--", skillHistoryPathspec)
	out, truncated, err := runGitScan(ctx, dir, args...)
	if err != nil {
		// Reaching past the root commit (cursor was the first commit) yields a bad
		// revision; treat as an empty final page rather than a hard error.
		if cursor != "" && isBadRevision(err) {
			result.HasMore = false
			return result, nil
		}
		return result, err
	}
	commits := parseGitLog(out)
	hasMoreCommits := len(commits) > limit
	if hasMoreCommits {
		commits = commits[:limit]
	}
	entries, capped := capEntries(flattenEntries(commits))
	result.Entries = entries
	result.HasMore = hasMoreCommits
	result.Truncated = truncated || capped
	if n := len(commits); n > 0 {
		result.NextCursor = commits[n-1].hash
	}
	return result, nil
}

func scanNewer(ctx context.Context, dir, cursor string, limit int, result SkillHistoryScanResult) (SkillHistoryScanResult, error) {
	rev := "HEAD"
	if cursor != "" {
		// Commits reachable from HEAD but not from the cursor: strictly newer.
		rev = cursor + "..HEAD"
	}
	args := append([]string{"log", rev, fmt.Sprintf("-n%d", maxNewerScanCommits)}, gitLogFormatArgs...)
	args = append(args, "--", skillHistoryPathspec)
	out, outTruncated, err := runGitScan(ctx, dir, args...)
	if err != nil {
		if cursor != "" && isBadRevision(err) {
			result.HasMore = false
			return result, nil
		}
		return result, err
	}
	commits := parseGitLog(out) // newest-first
	truncated := outTruncated || len(commits) >= maxNewerScanCommits
	// Reverse to oldest-first so paging forward is contiguous with prior pages.
	reverseCommits(commits)
	hasMore := len(commits) > limit || truncated
	if len(commits) > limit {
		commits = commits[:limit]
	}
	entries, capped := capEntries(flattenEntries(commits))
	result.Entries = entries
	result.HasMore = hasMore
	result.Truncated = truncated || capped
	if n := len(commits); n > 0 {
		result.NextCursor = commits[n-1].hash
	}
	return result, nil
}

type parsedCommit struct {
	hash    string
	date    string
	subject string
	changes []SkillHistoryEntry
}

// parseGitLog parses the NUL-delimited git log --name-status token stream.
func parseGitLog(out string) []parsedCommit {
	tokens := strings.Split(out, "\x00")
	commits := make([]parsedCommit, 0)
	var current *parsedCommit
	flush := func() {
		if current != nil {
			commits = append(commits, *current)
			current = nil
		}
	}

	for i := 0; i < len(tokens); {
		token := strings.TrimLeft(tokens[i], "\r\n")
		if token == gitCommitMarker {
			flush()
			if i+3 >= len(tokens) {
				break
			}
			current = &parsedCommit{hash: tokens[i+1], date: tokens[i+2], subject: tokens[i+3]}
			i += 4
			continue
		}
		if current == nil || strings.TrimSpace(token) == "" {
			i++
			continue
		}

		code := strings.TrimSpace(token)
		pathCount := 1
		if code[0] == 'R' || code[0] == 'C' {
			pathCount = 2
		}
		if i+pathCount >= len(tokens) {
			break
		}
		for _, entry := range parseNameStatusFields(code, tokens[i+1:i+1+pathCount]) {
			entry.Commit = current.hash
			entry.Date = current.date
			entry.Subject = current.subject
			current.changes = append(current.changes, entry)
		}
		i += pathCount + 1
	}
	flush()
	return commits
}

// parseNameStatusFields parses one -z --name-status record into zero or more
// skill-change entries. Renames/copies carry source and destination as separate
// NUL-delimited fields, so paths are never decoded from tab/newline separators.
func parseNameStatusFields(code string, paths []string) []SkillHistoryEntry {
	if code == "" || len(paths) == 0 {
		return nil
	}
	if (code[0] == 'R' || code[0] == 'C') && len(paths) >= 2 {
		oldPath := paths[0]
		newPath := paths[1]
		oldSkill := filepath.Base(oldPath) == "SKILL.md"
		newSkill := filepath.Base(newPath) == "SKILL.md"
		var out []SkillHistoryEntry
		if newSkill {
			kind := changeKind(code) // renamed | copied
			if !oldSkill {
				kind = "added" // moved/copied a non-skill file into a SKILL.md slot
			}
			out = append(out, skillEntry(newPath, kind))
		}
		// A rename (not a copy) that drops the old SKILL.md from its key deletes it.
		if code[0] == 'R' && oldSkill && (!newSkill || skillKeyFromPath(oldPath) != skillKeyFromPath(newPath)) {
			out = append(out, skillEntry(oldPath, "deleted"))
		}
		return out
	}
	path := paths[0]
	if filepath.Base(path) != "SKILL.md" {
		return nil
	}
	return []SkillHistoryEntry{skillEntry(path, changeKind(code))}
}

func skillEntry(path, change string) SkillHistoryEntry {
	return SkillHistoryEntry{
		SkillPath: filepath.ToSlash(path),
		SkillKey:  skillKeyFromPath(path),
		Change:    change,
	}
}

func changeKind(code string) string {
	switch code[0] {
	case 'A':
		return "added"
	case 'M':
		return "modified"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "modified"
	default:
		return "modified"
	}
}

// skillKeyFromPath maps a SKILL.md path to its skill key (the directory holding
// it, workspace-relative). A root-level SKILL.md has an empty key.
func skillKeyFromPath(path string) string {
	dir := filepath.ToSlash(filepath.Dir(filepath.ToSlash(path)))
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

func flattenEntries(commits []parsedCommit) []SkillHistoryEntry {
	entries := make([]SkillHistoryEntry, 0, len(commits))
	for _, c := range commits {
		entries = append(entries, c.changes...)
	}
	return entries
}

func reverseCommits(c []parsedCommit) {
	for i, j := 0, len(c)-1; i < j; i, j = i+1, j-1 {
		c[i], c[j] = c[j], c[i]
	}
}

func isBadRevision(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "bad revision") ||
		strings.Contains(msg, "unknown revision") ||
		strings.Contains(msg, "ambiguous argument")
}

func countNonEmptyLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
