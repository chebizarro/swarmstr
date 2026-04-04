package memory

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

const (
	userAgentMemoryDirName     = "agent-memory"
	projectAgentMemoryDirName  = ".metiq/agent-memory"
	localAgentMemoryDirName    = ".metiq/agent-memory-local"
	agentMemorySnapshotDirName = ".metiq/agent-memory-snapshots"
	snapshotMetaFileName       = "snapshot.json"
	snapshotSyncedMetaFileName = ".snapshot-synced.json"
)

type FileMemorySurface struct {
	RootDir        string
	SnapshotDir    string
	SnapshotNotice string
}

type fileMemorySnapshot struct {
	Dir       string
	UpdatedAt string
	Available bool
}

type fileMemorySnapshotMeta struct {
	UpdatedAt string `json:"updated_at"`
}

type fileMemorySyncedMeta struct {
	SyncedFrom string `json:"synced_from"`
}

var fileMemorySnapshotMu sync.Mutex

func ResolveFileMemorySurface(scope ScopedContext, projectWorkspaceDir string) FileMemorySurface {
	projectWorkspaceDir = strings.TrimSpace(projectWorkspaceDir)
	if !scope.Enabled() {
		return FileMemorySurface{RootDir: projectWorkspaceDir}
	}
	rootDir := scopedFileMemoryDir(scope, projectWorkspaceDir)
	if rootDir == "" {
		return FileMemorySurface{}
	}
	surface := FileMemorySurface{RootDir: rootDir}
	if projectWorkspaceDir == "" || strings.TrimSpace(scope.AgentID) == "" {
		return surface
	}

	fileMemorySnapshotMu.Lock()
	defer fileMemorySnapshotMu.Unlock()

	snapshot, err := refreshProjectFileMemorySnapshot(projectWorkspaceDir, scope.AgentID)
	if err != nil {
		surface.SnapshotNotice = fmt.Sprintf("> WARNING: agent memory snapshot refresh failed: %v", err)
		return surface
	}
	surface.SnapshotDir = snapshot.Dir
	if !snapshot.Available || scope.Scope == state.AgentMemoryScopeProject {
		return surface
	}

	action := checkAgentMemorySnapshotAction(rootDir, snapshot.UpdatedAt)
	switch action {
	case "initialize":
		if err := initializeAgentMemoryFromSnapshot(rootDir, snapshot); err != nil {
			surface.SnapshotNotice = fmt.Sprintf("> WARNING: agent memory snapshot seed failed: %v", err)
		}
	case "prompt-update":
		surface.SnapshotNotice = strings.Join([]string{
			"## Agent Memory Snapshot",
			fmt.Sprintf("- A newer project memory snapshot is available at `%s`.", snapshot.Dir),
			fmt.Sprintf("- It was last updated at `%s` and can be used to refresh this `%s`-scoped agent memory surface if needed.", snapshot.UpdatedAt, scope.Scope),
		}, "\n")
	}
	return surface
}

func scopedFileMemoryDir(scope ScopedContext, projectWorkspaceDir string) string {
	agentID := sanitizeAgentMemoryPathComponent(scope.AgentID)
	if agentID == "" {
		return ""
	}
	switch scope.Scope {
	case state.AgentMemoryScopeUser:
		baseDir := defaultFileMemoryBaseDir()
		if baseDir == "" {
			return ""
		}
		return filepath.Join(baseDir, userAgentMemoryDirName, agentID)
	case state.AgentMemoryScopeProject:
		baseDir := strings.TrimSpace(scope.WorkspaceDir)
		if baseDir == "" {
			baseDir = strings.TrimSpace(projectWorkspaceDir)
		}
		if baseDir == "" {
			return ""
		}
		return filepath.Join(baseDir, projectAgentMemoryDirName, agentID)
	case state.AgentMemoryScopeLocal:
		baseDir := strings.TrimSpace(scope.WorkspaceDir)
		if baseDir == "" {
			baseDir = strings.TrimSpace(projectWorkspaceDir)
		}
		if baseDir == "" {
			return ""
		}
		return filepath.Join(baseDir, localAgentMemoryDirName, agentID)
	default:
		return strings.TrimSpace(projectWorkspaceDir)
	}
}

func projectAgentMemorySnapshotDir(projectWorkspaceDir, agentID string) string {
	projectWorkspaceDir = strings.TrimSpace(projectWorkspaceDir)
	agentID = sanitizeAgentMemoryPathComponent(agentID)
	if projectWorkspaceDir == "" || agentID == "" {
		return ""
	}
	return filepath.Join(projectWorkspaceDir, agentMemorySnapshotDirName, agentID)
}

func defaultFileMemoryBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".metiq")
}

func sanitizeAgentMemoryPathComponent(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func refreshProjectFileMemorySnapshot(projectWorkspaceDir, agentID string) (fileMemorySnapshot, error) {
	snapshot := fileMemorySnapshot{Dir: projectAgentMemorySnapshotDir(projectWorkspaceDir, agentID)}
	sourceDir := filepath.Join(strings.TrimSpace(projectWorkspaceDir), projectAgentMemoryDirName, sanitizeAgentMemoryPathComponent(agentID))
	relPaths, updatedAt, ok, err := listManagedMemoryFiles(sourceDir)
	if err != nil {
		return snapshot, err
	}
	if !ok {
		if snapshot.Dir != "" {
			_ = os.RemoveAll(snapshot.Dir)
		}
		return snapshot, nil
	}
	snapshot.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	snapshot.Available = true
	if meta, ok := readSnapshotMeta(filepath.Join(snapshot.Dir, snapshotMetaFileName)); ok && meta.UpdatedAt == snapshot.UpdatedAt {
		return snapshot, nil
	}
	if err := os.RemoveAll(snapshot.Dir); err != nil {
		return fileMemorySnapshot{}, err
	}
	if err := os.MkdirAll(snapshot.Dir, 0o700); err != nil {
		return fileMemorySnapshot{}, err
	}
	if err := copyManagedMemoryFiles(sourceDir, snapshot.Dir, relPaths); err != nil {
		return fileMemorySnapshot{}, err
	}
	if err := writeJSONFile(filepath.Join(snapshot.Dir, snapshotMetaFileName), fileMemorySnapshotMeta{UpdatedAt: snapshot.UpdatedAt}); err != nil {
		return fileMemorySnapshot{}, err
	}
	return snapshot, nil
}

func checkAgentMemorySnapshotAction(targetDir, snapshotUpdatedAt string) string {
	if strings.TrimSpace(targetDir) == "" || strings.TrimSpace(snapshotUpdatedAt) == "" {
		return ""
	}
	if _, _, ok, _ := listManagedMemoryFiles(targetDir); !ok {
		return "initialize"
	}
	meta, ok := readSyncedMeta(filepath.Join(targetDir, snapshotSyncedMetaFileName))
	if !ok {
		return "prompt-update"
	}
	if snapshotTimestampAfter(snapshotUpdatedAt, meta.SyncedFrom) {
		return "prompt-update"
	}
	return ""
}

func initializeAgentMemoryFromSnapshot(targetDir string, snapshot fileMemorySnapshot) error {
	if strings.TrimSpace(targetDir) == "" || !snapshot.Available || strings.TrimSpace(snapshot.Dir) == "" {
		return nil
	}
	relPaths, _, ok, err := listManagedMemoryFiles(snapshot.Dir)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return err
	}
	if err := copyManagedMemoryFiles(snapshot.Dir, targetDir, relPaths); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(targetDir, snapshotSyncedMetaFileName), fileMemorySyncedMeta{SyncedFrom: snapshot.UpdatedAt})
}

func listManagedMemoryFiles(rootDir string) ([]string, time.Time, bool, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil, time.Time{}, false, nil
	}
	rootResolved := resolvedWorkspaceRoot(rootDir)
	paths := make([]string, 0, 8)
	latest := time.Time{}

	entrypointPath := filepath.Join(rootDir, FileMemoryEntrypointName)
	if info, err := os.Stat(entrypointPath); err == nil && info.Mode().IsRegular() {
		if isContainedWithin(rootResolved, entrypointPath) {
			paths = append(paths, FileMemoryEntrypointName)
			latest = info.ModTime()
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, time.Time{}, false, err
	}

	memoryDir := filepath.Join(rootDir, fileMemoryDirName)
	info, err := os.Stat(memoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			if len(paths) == 0 {
				return nil, time.Time{}, false, nil
			}
			return paths, latest, true, nil
		}
		return nil, time.Time{}, false, err
	}
	if !info.IsDir() {
		if len(paths) == 0 {
			return nil, time.Time{}, false, nil
		}
		return paths, latest, true, nil
	}
	err = filepath.WalkDir(memoryDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(d.Name())) != ".md" {
			return nil
		}
		if !isContainedWithin(rootResolved, path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relPath))
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return nil, time.Time{}, false, err
	}
	if len(paths) == 0 {
		return nil, time.Time{}, false, nil
	}
	sort.Strings(paths)
	return paths, latest, true, nil
}

func copyManagedMemoryFiles(srcRoot, dstRoot string, relPaths []string) error {
	for _, relPath := range relPaths {
		srcPath := filepath.Join(srcRoot, filepath.FromSlash(relPath))
		dstPath := filepath.Join(dstRoot, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o700); err != nil {
			return err
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func readSnapshotMeta(path string) (fileMemorySnapshotMeta, bool) {
	var meta fileMemorySnapshotMeta
	data, err := os.ReadFile(path)
	if err != nil {
		return meta, false
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return fileMemorySnapshotMeta{}, false
	}
	return meta, strings.TrimSpace(meta.UpdatedAt) != ""
}

func readSyncedMeta(path string) (fileMemorySyncedMeta, bool) {
	var meta fileMemorySyncedMeta
	data, err := os.ReadFile(path)
	if err != nil {
		return meta, false
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return fileMemorySyncedMeta{}, false
	}
	return meta, strings.TrimSpace(meta.SyncedFrom) != ""
}

func snapshotTimestampAfter(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, strings.TrimSpace(a))
	tb, errB := time.Parse(time.RFC3339Nano, strings.TrimSpace(b))
	if errA == nil && errB == nil {
		return ta.After(tb)
	}
	return strings.TrimSpace(a) > strings.TrimSpace(b)
}
