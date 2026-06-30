package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FsBridgeMount struct {
	HostRoot      string
	ContainerRoot string
	ReadOnly      bool
}

type FsBridge struct {
	mounts []FsBridgeMount
}

type FsBridgeEntry struct {
	Name  string
	Path  string
	IsDir bool
	Size  int64
}

type FsBridgeStat struct {
	Path  string
	IsDir bool
	Size  int64
	Mode  os.FileMode
}

func NewWorkspaceFsBridge(workspace workspaceMount, skills ...ReadOnlyWorkspaceSkillMount) (*FsBridge, error) {
	if !workspace.Enabled {
		return nil, fmt.Errorf("sandbox fs bridge requires an enabled workspace mount")
	}
	mounts := []FsBridgeMount{{HostRoot: workspace.Source, ContainerRoot: workspace.Target, ReadOnly: workspace.Access == WorkspaceAccessReadOnly}}
	for _, skill := range skills {
		mounts = append(mounts, FsBridgeMount{HostRoot: skill.Source, ContainerRoot: skill.Target, ReadOnly: true})
	}
	return NewFsBridge(mounts)
}

func NewFsBridge(mounts []FsBridgeMount) (*FsBridge, error) {
	if len(mounts) == 0 {
		return nil, fmt.Errorf("sandbox fs bridge requires at least one mount")
	}
	out := make([]FsBridgeMount, 0, len(mounts))
	for _, mount := range mounts {
		host, err := filepath.Abs(strings.TrimSpace(mount.HostRoot))
		if err != nil {
			return nil, fmt.Errorf("sandbox fs bridge host root: %w", err)
		}
		real, err := filepath.EvalSymlinks(host)
		if err != nil {
			return nil, fmt.Errorf("sandbox fs bridge host root %q: %w", host, err)
		}
		info, err := os.Stat(real)
		if err != nil {
			return nil, fmt.Errorf("sandbox fs bridge host root %q: %w", real, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("sandbox fs bridge host root %q is not a directory", real)
		}
		container := filepath.ToSlash(filepath.Clean(strings.TrimSpace(mount.ContainerRoot)))
		if err := validateWorkspaceTarget(container); err != nil {
			return nil, err
		}
		out = append(out, FsBridgeMount{HostRoot: real, ContainerRoot: container, ReadOnly: mount.ReadOnly})
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].ContainerRoot) > len(out[j].ContainerRoot) })
	return &FsBridge{mounts: out}, nil
}

func (b *FsBridge) ReadFile(path string) ([]byte, error) {
	host, _, err := b.resolve(path, false)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(host)
}

func (b *FsBridge) WriteFile(path string, data []byte, perm os.FileMode) error {
	host, mount, err := b.resolve(path, true)
	if err != nil {
		return err
	}
	if mount.ReadOnly {
		return fmt.Errorf("sandbox fs bridge %q is read-only", path)
	}
	if err := os.MkdirAll(filepath.Dir(host), 0755); err != nil {
		return err
	}
	return os.WriteFile(host, data, perm)
}

func (b *FsBridge) List(path string) ([]FsBridgeEntry, error) {
	host, _, err := b.resolve(path, false)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(host)
	if err != nil {
		return nil, err
	}
	out := make([]FsBridgeEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, FsBridgeEntry{Name: entry.Name(), Path: filepath.ToSlash(filepath.Join(path, entry.Name())), IsDir: info.IsDir(), Size: info.Size()})
	}
	return out, nil
}

func (b *FsBridge) Stat(path string) (FsBridgeStat, error) {
	host, _, err := b.resolve(path, false)
	if err != nil {
		return FsBridgeStat{}, err
	}
	info, err := os.Stat(host)
	if err != nil {
		return FsBridgeStat{}, err
	}
	return FsBridgeStat{Path: filepath.ToSlash(filepath.Clean(path)), IsDir: info.IsDir(), Size: info.Size(), Mode: info.Mode()}, nil
}

func (b *FsBridge) resolve(path string, forWrite bool) (string, FsBridgeMount, error) {
	containerPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if containerPath == "." || !strings.HasPrefix(containerPath, "/") {
		return "", FsBridgeMount{}, fmt.Errorf("sandbox fs bridge path %q must be absolute", path)
	}
	for _, mount := range b.mounts {
		if containerPath != mount.ContainerRoot && !strings.HasPrefix(containerPath, mount.ContainerRoot+"/") {
			continue
		}
		rel := strings.TrimPrefix(containerPath, mount.ContainerRoot)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return mount.HostRoot, mount, nil
		}
		cleanRel := filepath.Clean(filepath.FromSlash(rel))
		if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(os.PathSeparator)) || filepath.IsAbs(cleanRel) {
			return "", FsBridgeMount{}, fmt.Errorf("sandbox fs bridge path %q escapes mount", path)
		}
		host := filepath.Join(mount.HostRoot, cleanRel)
		if err := assertInside(mount.HostRoot, host); err != nil {
			return "", FsBridgeMount{}, err
		}
		if forWrite {
			if err := assertExistingAncestorInside(mount.HostRoot, host); err != nil {
				return "", FsBridgeMount{}, err
			}
			return host, mount, nil
		}
		real, err := filepath.EvalSymlinks(host)
		if err != nil {
			return "", FsBridgeMount{}, err
		}
		if err := assertInside(mount.HostRoot, real); err != nil {
			return "", FsBridgeMount{}, err
		}
		return real, mount, nil
	}
	return "", FsBridgeMount{}, fmt.Errorf("sandbox fs bridge path %q is outside mounted workspace", path)
}

func assertExistingAncestorInside(root, path string) error {
	current := path
	for {
		if _, err := os.Lstat(current); err == nil {
			real, err := filepath.EvalSymlinks(current)
			if err != nil {
				return err
			}
			return assertInside(root, real)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return fmt.Errorf("sandbox fs bridge could not resolve existing ancestor for %q", path)
}

func assertInside(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("sandbox fs bridge path %q escapes root %q", path, root)
	}
	return nil
}
