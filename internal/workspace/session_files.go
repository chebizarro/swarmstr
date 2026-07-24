package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"metiq/internal/store/state"
)

const (
	MaxSessionFileBytes     = 256 << 10
	MaxSessionBrowserItems  = 250
	MaxSessionSearchItems   = 500
	MaxSessionSearchVisited = 5000
)

var (
	ErrUnsafePath  = errors.New("unsafe workspace path")
	ErrNotRegular  = errors.New("workspace path is not a regular file")
	ErrTooLarge    = errors.New("workspace file exceeds size limit")
	ErrNotUTF8     = errors.New("workspace file is not valid UTF-8")
	ErrCASConflict = errors.New("workspace file changed")
)

type TouchedFile struct {
	Path string
	Kind string
}

type FileEntry struct {
	Path          string `json:"path"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Missing       bool   `json:"missing"`
	Size          int64  `json:"size,omitempty"`
	UpdatedAtMS   int64  `json:"updatedAtMs,omitempty"`
	Content       string `json:"content,omitempty"`
	Hash          string `json:"hash,omitempty"`
}

type BrowserEntry struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	SessionKind string `json:"sessionKind,omitempty"`
	Size        int64  `json:"size,omitempty"`
	UpdatedAtMS int64  `json:"updatedAtMs,omitempty"`
}

type BrowserResult struct {
	Path       string         `json:"path"`
	ParentPath *string        `json:"parentPath,omitempty"`
	Search     string         `json:"search,omitempty"`
	Entries    []BrowserEntry `json:"entries"`
	Truncated  bool           `json:"truncated,omitempty"`
}

type ListResult struct {
	Root    string
	Files   []FileEntry
	Browser BrowserResult
}

type OpenPathFunc func(context.Context, string) error

type FileService struct {
	writeMu  sync.Mutex
	openPath OpenPathFunc
}

func NewFileService(openPath OpenPathFunc) *FileService {
	if openPath == nil {
		openPath = defaultOpenPath
	}
	return &FileService{openPath: openPath}
}

func ResolveSessionWorkspaceDir(cfg state.ConfigDoc, entry state.SessionEntry) string {
	if root := strings.TrimSpace(entry.SpawnedWorkspace); root != "" {
		return root
	}
	return ResolveWorkspaceDir(cfg, entry.AgentID)
}

func (s *FileService) List(ctx context.Context, rootPath, browserPath, search string, touched []TouchedFile) (ListResult, error) {
	root, canonicalRoot, err := openWorkspaceRoot(rootPath)
	if err != nil {
		return ListResult{}, err
	}
	defer root.Close()

	relevance := make(map[string]string)
	files := make([]FileEntry, 0, len(touched))
	for _, item := range touched {
		if err := ctx.Err(); err != nil {
			return ListResult{}, err
		}
		name, err := normalizeTouchedPath(canonicalRoot, item.Path)
		if err != nil {
			continue
		}
		kind := normalizeTouchedKind(item.Kind)
		if previous := relevance[name]; previous != "" && previous != kind {
			relevance[name] = "mixed"
		} else {
			relevance[name] = kind
		}
		entry := FileEntry{Path: name, WorkspacePath: name, Name: path.Base(name), Kind: kind}
		info, statErr := root.Lstat(name)
		if statErr != nil || !info.Mode().IsRegular() {
			entry.Missing = true
			entry.WorkspacePath = ""
		} else {
			entry.Size = info.Size()
			entry.UpdatedAtMS = info.ModTime().UnixMilli()
		}
		files = append(files, entry)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Kind != files[j].Kind {
			return files[i].Kind == "modified"
		}
		return files[i].Path < files[j].Path
	})

	browser, err := listBrowser(ctx, root, browserPath, search, relevance)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Root: canonicalRoot, Files: files, Browser: browser}, nil
}

func (s *FileService) Get(ctx context.Context, rootPath, filePath, kind string) (FileEntry, error) {
	root, _, err := openWorkspaceRoot(rootPath)
	if err != nil {
		return FileEntry{}, err
	}
	defer root.Close()
	name, err := normalizeWorkspacePath(filePath, false)
	if err != nil {
		return FileEntry{}, err
	}
	data, info, err := readRootFile(ctx, root, name)
	if err != nil {
		return FileEntry{}, err
	}
	hash := sha256.Sum256(data)
	return FileEntry{Path: name, WorkspacePath: name, Name: path.Base(name), Kind: normalizeTouchedKind(kind), Size: info.Size(), UpdatedAtMS: info.ModTime().UnixMilli(), Content: string(data), Hash: hex.EncodeToString(hash[:])}, nil
}

func (s *FileService) Set(ctx context.Context, rootPath, filePath, content, expectedHash, kind string) (FileEntry, error) {
	if len(content) > MaxSessionFileBytes {
		return FileEntry{}, ErrTooLarge
	}
	if !utf8.ValidString(content) || strings.IndexByte(content, 0) >= 0 {
		return FileEntry{}, ErrNotUTF8
	}
	if len(expectedHash) != 64 {
		return FileEntry{}, fmt.Errorf("expectedHash must be a lowercase SHA-256 hash")
	}
	if _, err := hex.DecodeString(expectedHash); err != nil || strings.ToLower(expectedHash) != expectedHash {
		return FileEntry{}, fmt.Errorf("expectedHash must be a lowercase SHA-256 hash")
	}
	name, err := normalizeWorkspacePath(filePath, false)
	if err != nil {
		return FileEntry{}, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root, _, err := openWorkspaceRoot(rootPath)
	if err != nil {
		return FileEntry{}, err
	}
	defer root.Close()
	current, info, err := readRootFile(ctx, root, name)
	if err != nil {
		return FileEntry{}, err
	}
	currentHash := sha256.Sum256(current)
	if hex.EncodeToString(currentHash[:]) != expectedHash {
		return FileEntry{}, ErrCASConflict
	}

	tmpName := path.Join(path.Dir(name), fmt.Sprintf(".%s.metiq-%d", path.Base(name), time.Now().UnixNano()))
	file, err := root.OpenFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return FileEntry{}, fmt.Errorf("create temporary workspace file: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = root.Remove(tmpName)
		}
	}()
	if _, err = io.WriteString(file, content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return FileEntry{}, fmt.Errorf("write temporary workspace file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return FileEntry{}, err
	}
	latest, _, err := readRootFile(ctx, root, name)
	if err != nil {
		return FileEntry{}, err
	}
	latestHash := sha256.Sum256(latest)
	if hex.EncodeToString(latestHash[:]) != expectedHash {
		return FileEntry{}, ErrCASConflict
	}
	if err := root.Rename(tmpName, name); err != nil {
		return FileEntry{}, fmt.Errorf("replace workspace file: %w", err)
	}
	cleanup = false
	return s.Get(context.Background(), rootPath, name, kind)
}

func (s *FileService) CanonicalRoot(rootPath string) (string, error) {
	root, canonicalRoot, err := openWorkspaceRoot(rootPath)
	if err != nil {
		return "", err
	}
	_ = root.Close()
	return canonicalRoot, nil
}

func (s *FileService) Reveal(ctx context.Context, rootPath string) (string, error) {
	root, canonicalRoot, err := openWorkspaceRoot(rootPath)
	if err != nil {
		return "", err
	}
	_ = root.Close()
	if err := s.openPath(ctx, canonicalRoot); err != nil {
		return canonicalRoot, err
	}
	return canonicalRoot, nil
}

func openWorkspaceRoot(rootPath string) (*os.Root, string, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, "", fmt.Errorf("workspace root is empty")
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, "", fmt.Errorf("resolve workspace root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, "", fmt.Errorf("workspace unavailable")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return nil, "", fmt.Errorf("workspace unavailable")
	}
	root, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, "", fmt.Errorf("workspace unavailable")
	}
	return root, canonical, nil
}

func normalizeWorkspacePath(value string, allowRoot bool) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if strings.IndexByte(value, 0) >= 0 || strings.HasPrefix(value, "/") || filepath.VolumeName(value) != "" {
		return "", ErrUnsafePath
	}
	clean := path.Clean(value)
	if clean == "." && allowRoot {
		return ".", nil
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || !fs.ValidPath(clean) {
		return "", ErrUnsafePath
	}
	return clean, nil
}

func normalizeTouchedPath(rootPath, value string) (string, error) {
	value = strings.TrimSpace(value)
	if filepath.IsAbs(value) {
		absolute, err := filepath.Abs(value)
		if err != nil {
			return "", ErrUnsafePath
		}
		relative, err := filepath.Rel(rootPath, absolute)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", ErrUnsafePath
		}
		value = filepath.ToSlash(relative)
	}
	return normalizeWorkspacePath(value, false)
}

func normalizeTouchedKind(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), "modified") {
		return "modified"
	}
	return "read"
}

func readRootFile(ctx context.Context, root *os.Root, name string) ([]byte, fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace file not found")
	}
	if !info.Mode().IsRegular() {
		return nil, nil, ErrNotRegular
	}
	if info.Size() > MaxSessionFileBytes {
		return nil, nil, ErrTooLarge
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace file not found")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxSessionFileBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read workspace file")
	}
	if len(data) > MaxSessionFileBytes {
		return nil, nil, ErrTooLarge
	}
	if !utf8.Valid(data) {
		return nil, nil, ErrNotUTF8
	}
	return data, info, nil
}

func listBrowser(ctx context.Context, root *os.Root, browserPath, search string, relevance map[string]string) (BrowserResult, error) {
	if search = strings.TrimSpace(search); len([]rune(search)) > 500 {
		return BrowserResult{}, fmt.Errorf("search exceeds 500 characters")
	}
	if search != "" {
		return searchBrowser(ctx, root, search, relevance)
	}
	name, err := normalizeWorkspacePath(browserPath, true)
	if err != nil {
		return BrowserResult{}, err
	}
	file, err := root.Open(name)
	if err != nil {
		return BrowserResult{}, fmt.Errorf("workspace directory not found")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return BrowserResult{}, fmt.Errorf("workspace path is not a directory")
	}
	dirents, err := file.ReadDir(MaxSessionBrowserItems + 1)
	if err != nil {
		return BrowserResult{}, fmt.Errorf("read workspace directory")
	}
	resultPath := ""
	if name != "." {
		resultPath = name
	}
	entries := make([]BrowserEntry, 0, min(len(dirents), MaxSessionBrowserItems))
	for _, dirent := range dirents {
		if len(entries) >= MaxSessionBrowserItems {
			break
		}
		entryPath := dirent.Name()
		if resultPath != "" {
			entryPath = resultPath + "/" + entryPath
		}
		entry, ok := browserEntry(root, entryPath, dirent, relevance)
		if ok {
			entries = append(entries, entry)
		}
	}
	sortBrowser(entries)
	out := BrowserResult{Path: resultPath, Entries: entries, Truncated: len(dirents) > MaxSessionBrowserItems}
	if resultPath != "" {
		parent := path.Dir(resultPath)
		if parent == "." {
			parent = ""
		}
		out.ParentPath = &parent
	}
	return out, nil
}

func searchBrowser(ctx context.Context, root *os.Root, search string, relevance map[string]string) (BrowserResult, error) {
	out := BrowserResult{Path: "", Search: search, Entries: []BrowserEntry{}}
	visited := 0
	var walk func(string) error
	walk = func(dir string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if visited >= MaxSessionSearchVisited || len(out.Entries) >= MaxSessionSearchItems {
			out.Truncated = true
			return nil
		}
		name := dir
		if name == "" {
			name = "."
		}
		file, err := root.Open(name)
		if err != nil {
			return nil
		}
		dirents, err := file.ReadDir(-1)
		_ = file.Close()
		if err != nil {
			return nil
		}
		sort.Slice(dirents, func(i, j int) bool { return dirents[i].Name() < dirents[j].Name() })
		for _, de := range dirents {
			visited++
			entryPath := de.Name()
			if dir != "" {
				entryPath = dir + "/" + entryPath
			}
			entry, ok := browserEntry(root, entryPath, de, relevance)
			if ok && strings.Contains(strings.ToLower(entryPath), strings.ToLower(search)) {
				out.Entries = append(out.Entries, entry)
			}
			if ok && entry.Kind == "directory" && !skipSearchDir(de.Name()) {
				if err := walk(entryPath); err != nil {
					return err
				}
			}
			if visited >= MaxSessionSearchVisited || len(out.Entries) >= MaxSessionSearchItems {
				out.Truncated = true
				break
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return BrowserResult{}, err
	}
	sortBrowser(out.Entries)
	return out, nil
}

func browserEntry(root *os.Root, name string, de fs.DirEntry, relevance map[string]string) (BrowserEntry, bool) {
	info, err := root.Lstat(name)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return BrowserEntry{}, false
	}
	kind := ""
	if info.IsDir() {
		kind = "directory"
	} else if info.Mode().IsRegular() {
		kind = "file"
	} else {
		return BrowserEntry{}, false
	}
	entry := BrowserEntry{Path: name, Name: de.Name(), Kind: kind, UpdatedAtMS: info.ModTime().UnixMilli()}
	if kind == "file" {
		entry.Size = info.Size()
		entry.SessionKind = relevance[name]
	} else {
		entry.SessionKind = directoryRelevance(name, relevance)
	}
	return entry, true
}

func directoryRelevance(dir string, relevance map[string]string) string {
	prefix := dir + "/"
	result := ""
	for name, kind := range relevance {
		if strings.HasPrefix(name, prefix) {
			if result == "" {
				result = kind
			} else if result != kind {
				return "mixed"
			}
		}
	}
	return result
}

func sortBrowser(entries []BrowserEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind == "directory"
		}
		a, b := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if a != b {
			return a < b
		}
		return entries[i].Path < entries[j].Path
	})
}

func skipSearchDir(name string) bool {
	switch name {
	case ".git", ".hg", ".next", ".turbo", ".yarn", "coverage", "dist", "node_modules":
		return true
	}
	return false
}

func defaultOpenPath(ctx context.Context, value string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
		args = []string{value}
	case "windows":
		command = "explorer.exe"
		args = []string{value}
	default:
		command = "xdg-open"
		args = []string{value}
	}
	return exec.CommandContext(ctx, command, args...).Run()
}
