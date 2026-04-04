package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"metiq/internal/nostr/secure"
)

const (
	teamMemoryDirName       = ".metiq/team-memory"
	teamMemorySyncDirName   = ".metiq/team-memory-sync"
	TeamMemoryStateFileName = "state.json"
)

type PathTraversalError struct {
	Message string
}

func (e PathTraversalError) Error() string {
	return e.Message
}

type TeamMemorySurface struct {
	RootDir        string
	EntrypointPath string
	SyncStatePath  string
}

type TeamMemorySecretFinding struct {
	Key         string `json:"key"`
	PatternName string `json:"pattern_name"`
	Severity    string `json:"severity"`
	Excerpt     string `json:"excerpt,omitempty"`
}

type TeamMemoryConflict struct {
	Key              string `json:"key"`
	ExpectedChecksum string `json:"expected_checksum,omitempty"`
	ActualChecksum   string `json:"actual_checksum,omitempty"`
}

type TeamMemoryWriteResult struct {
	OK             bool                      `json:"ok"`
	Path           string                    `json:"path,omitempty"`
	Key            string                    `json:"key,omitempty"`
	Checksum       string                    `json:"checksum,omitempty"`
	Conflict       *TeamMemoryConflict       `json:"conflict,omitempty"`
	SecretBlocked  bool                      `json:"secret_blocked,omitempty"`
	SecretFindings []TeamMemorySecretFinding `json:"secret_findings,omitempty"`
	Error          string                    `json:"error,omitempty"`
}

type TeamMemoryContent struct {
	Entries        map[string]string `json:"entries"`
	EntryChecksums map[string]string `json:"entry_checksums,omitempty"`
}

type TeamMemorySnapshot struct {
	Version      int               `json:"version"`
	LastModified string            `json:"last_modified,omitempty"`
	Checksum     string            `json:"checksum"`
	Content      TeamMemoryContent `json:"content"`
}

type TeamMemoryExportResult struct {
	OK               bool                      `json:"ok"`
	Snapshot         TeamMemorySnapshot        `json:"snapshot,omitempty"`
	BlockedBySecrets bool                      `json:"blocked_by_secrets,omitempty"`
	SecretFindings   []TeamMemorySecretFinding `json:"secret_findings,omitempty"`
	Error            string                    `json:"error,omitempty"`
}

type TeamMemorySyncState struct {
	Version      int    `json:"version,omitempty"`
	Checksum     string `json:"checksum,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	LastPulledAt string `json:"last_pulled_at,omitempty"`
	LastPushedAt string `json:"last_pushed_at,omitempty"`
	LastError    string `json:"last_error,omitempty"`
}

func ResolveTeamMemorySurface(workspaceDir string) TeamMemorySurface {
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir == "" {
		return TeamMemorySurface{}
	}
	return TeamMemorySurface{
		RootDir:        filepath.Join(workspaceDir, teamMemoryDirName),
		EntrypointPath: filepath.Join(workspaceDir, teamMemoryDirName, FileMemoryEntrypointName),
		SyncStatePath:  filepath.Join(workspaceDir, teamMemorySyncDirName, TeamMemoryStateFileName),
	}
}

func ValidateTeamMemoryKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", PathTraversalError{Message: "team memory key is required"}
	}
	if strings.ContainsRune(key, '\x00') {
		return "", PathTraversalError{Message: fmt.Sprintf("null byte in team memory key %q", key)}
	}
	decoded, err := url.PathUnescape(key)
	if err == nil && decoded != key && (strings.Contains(decoded, "..") || strings.Contains(decoded, "/") || strings.Contains(decoded, "\\")) {
		return "", PathTraversalError{Message: fmt.Sprintf("encoded traversal in team memory key %q", key)}
	}
	normalized := norm.NFKC.String(key)
	if normalized != key && (strings.Contains(normalized, "..") || strings.Contains(normalized, "/") || strings.Contains(normalized, "\\") || strings.ContainsRune(normalized, '\x00')) {
		return "", PathTraversalError{Message: fmt.Sprintf("unicode-normalized traversal in team memory key %q", key)}
	}
	if strings.Contains(key, "\\") {
		return "", PathTraversalError{Message: fmt.Sprintf("backslash in team memory key %q", key)}
	}
	if path.IsAbs(key) || strings.HasPrefix(key, "/") {
		return "", PathTraversalError{Message: fmt.Sprintf("absolute team memory key %q", key)}
	}
	cleaned := path.Clean(strings.ReplaceAll(key, "\\", "/"))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", PathTraversalError{Message: fmt.Sprintf("team memory key %q escapes the shared-memory root", key)}
	}
	parts := strings.Split(cleaned, "/")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			return "", PathTraversalError{Message: fmt.Sprintf("invalid team memory key %q", key)}
		}
		if strings.HasPrefix(part, ".") {
			return "", PathTraversalError{Message: fmt.Sprintf("hidden path segment in team memory key %q", key)}
		}
	}
	if strings.ToLower(filepath.Ext(cleaned)) != ".md" {
		return "", fmt.Errorf("team memory key %q must point to a markdown file", key)
	}
	return cleaned, nil
}

func TeamMemoryWritePath(workspaceDir, key string) (string, string, error) {
	surface := ResolveTeamMemorySurface(workspaceDir)
	if surface.RootDir == "" {
		return "", "", fmt.Errorf("team memory workspace is not available")
	}
	normalizedKey, err := ValidateTeamMemoryKey(key)
	if err != nil {
		return "", "", err
	}
	workspaceRoot := resolvedWorkspaceRoot(workspaceDir)
	candidate := filepath.Join(surface.RootDir, filepath.FromSlash(normalizedKey))
	if !isContainedWithin(workspaceRoot, candidate) {
		return "", "", PathTraversalError{Message: fmt.Sprintf("team memory path %q resolves outside the workspace root", key)}
	}
	return normalizedKey, candidate, nil
}

func WriteTeamMemoryEntry(workspaceDir, key, content, expectedChecksum string) TeamMemoryWriteResult {
	normalizedKey, targetPath, err := TeamMemoryWritePath(workspaceDir, key)
	if err != nil {
		return TeamMemoryWriteResult{Error: err.Error()}
	}
	if int64(len(content)) > maxFileMemoryFileBytes {
		return TeamMemoryWriteResult{
			Key:   normalizedKey,
			Path:  targetPath,
			Error: fmt.Sprintf("team memory content exceeds the safe size limit (%d bytes)", maxFileMemoryFileBytes),
		}
	}
	findings := scanTeamMemorySecrets(normalizedKey, content)
	if len(findings) > 0 {
		return TeamMemoryWriteResult{
			Key:            normalizedKey,
			Path:           targetPath,
			SecretBlocked:  true,
			SecretFindings: findings,
			Error:          "team memory write blocked by secret scan",
		}
	}
	actualChecksum := ""
	if raw, err := os.ReadFile(targetPath); err == nil {
		actualChecksum = teamMemoryChecksum(raw)
	} else if err != nil && !os.IsNotExist(err) {
		return TeamMemoryWriteResult{Key: normalizedKey, Path: targetPath, Error: err.Error()}
	}
	if strings.TrimSpace(expectedChecksum) != "" && expectedChecksum != actualChecksum {
		return TeamMemoryWriteResult{
			Key:  normalizedKey,
			Path: targetPath,
			Conflict: &TeamMemoryConflict{
				Key:              normalizedKey,
				ExpectedChecksum: expectedChecksum,
				ActualChecksum:   actualChecksum,
			},
			Error: "team memory write conflict",
		}
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return TeamMemoryWriteResult{Key: normalizedKey, Path: targetPath, Error: err.Error()}
	}
	if err := writeAtomicFile(targetPath, []byte(content), 0o644); err != nil {
		return TeamMemoryWriteResult{Key: normalizedKey, Path: targetPath, Error: err.Error()}
	}
	return TeamMemoryWriteResult{
		OK:       true,
		Key:      normalizedKey,
		Path:     targetPath,
		Checksum: teamMemoryChecksum([]byte(content)),
	}
}

func BuildTeamMemorySyncPayload(workspaceDir string) TeamMemoryExportResult {
	surface := ResolveTeamMemorySurface(workspaceDir)
	if surface.RootDir == "" {
		return TeamMemoryExportResult{Error: "team memory workspace is not available"}
	}
	entries := map[string]string{}
	entryChecksums := map[string]string{}
	var secretFindings []TeamMemorySecretFinding
	latest := time.Time{}
	workspaceRoot := resolvedWorkspaceRoot(workspaceDir)
	if !isContainedWithin(workspaceRoot, surface.RootDir) {
		return TeamMemoryExportResult{Error: "team memory root resolves outside the workspace root"}
	}

	if info, err := os.Stat(surface.RootDir); err != nil {
		if os.IsNotExist(err) {
			snapshot := TeamMemorySnapshot{
				Version:  0,
				Checksum: teamMemoryContentChecksum(entryChecksums),
				Content:  TeamMemoryContent{Entries: entries, EntryChecksums: entryChecksums},
			}
			return TeamMemoryExportResult{OK: true, Snapshot: snapshot}
		}
		return TeamMemoryExportResult{Error: err.Error()}
	} else if !info.IsDir() {
		return TeamMemoryExportResult{Error: fmt.Sprintf("team memory root %q is not a directory", surface.RootDir)}
	}

	err := filepath.WalkDir(surface.RootDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != surface.RootDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || strings.ToLower(filepath.Ext(d.Name())) != ".md" {
			return nil
		}
		if !isContainedWithin(workspaceRoot, path) {
			return PathTraversalError{Message: fmt.Sprintf("team memory file %q resolves outside the workspace root", path)}
		}
		relPath, err := filepath.Rel(surface.RootDir, path)
		if err != nil {
			return err
		}
		normalizedKey, err := ValidateTeamMemoryKey(filepath.ToSlash(relPath))
		if err != nil {
			return err
		}
		raw, err := readLimitedMemoryFile(path)
		if err != nil {
			return fmt.Errorf("read team memory %q: %w", normalizedKey, err)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		content := string(raw)
		findings := scanTeamMemorySecrets(normalizedKey, content)
		if len(findings) > 0 {
			secretFindings = append(secretFindings, findings...)
			return nil
		}
		entries[normalizedKey] = content
		entryChecksums[normalizedKey] = teamMemoryChecksum(raw)
		return nil
	})
	if err != nil {
		return TeamMemoryExportResult{Error: err.Error()}
	}
	if len(secretFindings) > 0 {
		sort.Slice(secretFindings, func(i, j int) bool {
			if secretFindings[i].Key == secretFindings[j].Key {
				return secretFindings[i].PatternName < secretFindings[j].PatternName
			}
			return secretFindings[i].Key < secretFindings[j].Key
		})
		return TeamMemoryExportResult{
			BlockedBySecrets: true,
			SecretFindings:   secretFindings,
			Error:            "team memory sync payload blocked by secret scan",
		}
	}
	snapshot := TeamMemorySnapshot{
		Version:  0,
		Checksum: teamMemoryContentChecksum(entryChecksums),
		Content:  TeamMemoryContent{Entries: entries, EntryChecksums: entryChecksums},
	}
	if !latest.IsZero() {
		snapshot.LastModified = latest.UTC().Format(time.RFC3339Nano)
	}
	return TeamMemoryExportResult{OK: true, Snapshot: snapshot}
}

func ReadTeamMemorySyncState(workspaceDir string) (TeamMemorySyncState, error) {
	surface := ResolveTeamMemorySurface(workspaceDir)
	if surface.SyncStatePath == "" {
		return TeamMemorySyncState{}, fmt.Errorf("team memory workspace is not available")
	}
	if !isContainedWithin(resolvedWorkspaceRoot(workspaceDir), surface.SyncStatePath) {
		return TeamMemorySyncState{}, PathTraversalError{Message: "team memory sync-state path resolves outside the workspace root"}
	}
	data, err := os.ReadFile(surface.SyncStatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return TeamMemorySyncState{}, nil
		}
		return TeamMemorySyncState{}, err
	}
	var state TeamMemorySyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return TeamMemorySyncState{}, err
	}
	return state, nil
}

func WriteTeamMemorySyncState(workspaceDir string, state TeamMemorySyncState) error {
	surface := ResolveTeamMemorySurface(workspaceDir)
	if surface.SyncStatePath == "" {
		return fmt.Errorf("team memory workspace is not available")
	}
	if !isContainedWithin(resolvedWorkspaceRoot(workspaceDir), surface.SyncStatePath) {
		return PathTraversalError{Message: "team memory sync-state path resolves outside the workspace root"}
	}
	if err := os.MkdirAll(filepath.Dir(surface.SyncStatePath), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(surface.SyncStatePath, data, 0o600)
}

func scanTeamMemorySecrets(key, content string) []TeamMemorySecretFinding {
	scanner := secure.NewContentScanner()
	result := scanner.Scan(content)
	if result.Clean {
		return nil
	}
	findings := make([]TeamMemorySecretFinding, 0, len(result.Findings))
	for _, finding := range result.Findings {
		findings = append(findings, TeamMemorySecretFinding{
			Key:         key,
			PatternName: finding.PatternName,
			Severity:    finding.Severity,
			Excerpt:     finding.Excerpt,
		})
	}
	return findings
}

func teamMemoryChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeAtomicFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-team-memory-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func teamMemoryContentChecksum(entryChecksums map[string]string) string {
	keys := make([]string, 0, len(entryChecksums))
	for key := range entryChecksums {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		_, _ = h.Write([]byte(key))
		_, _ = h.Write([]byte("\n"))
		_, _ = h.Write([]byte(entryChecksums[key]))
		_, _ = h.Write([]byte("\n"))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
