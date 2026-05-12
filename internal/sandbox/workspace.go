package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	WorkspaceAccessReadOnly  = "read_only"
	WorkspaceAccessReadWrite = "read_write"
	defaultContainerWorkdir  = "/workspace"
)

type workspaceMount struct {
	Enabled bool
	Source  string
	Target  string
	Access  string
}

func (c Config) workspaceMount() (workspaceMount, error) {
	source := strings.TrimSpace(c.WorkspaceDir)
	if source == "" {
		return workspaceMount{}, nil
	}

	absSource, err := filepath.Abs(source)
	if err != nil {
		return workspaceMount{}, fmt.Errorf("sandbox workspace_dir: %w", err)
	}
	info, err := os.Stat(absSource)
	if err != nil {
		return workspaceMount{}, fmt.Errorf("sandbox workspace_dir %q: %w", absSource, err)
	}
	if !info.IsDir() {
		return workspaceMount{}, fmt.Errorf("sandbox workspace_dir %q is not a directory", absSource)
	}

	target := strings.TrimSpace(c.ContainerWorkdir)
	if target == "" {
		target = defaultContainerWorkdir
	}
	target = filepath.ToSlash(filepath.Clean(target))
	if err := validateWorkspaceTarget(target); err != nil {
		return workspaceMount{}, err
	}

	access := strings.TrimSpace(strings.ToLower(c.WorkspaceAccess))
	if access == "" {
		access = WorkspaceAccessReadWrite
	}
	switch access {
	case WorkspaceAccessReadOnly, "ro":
		access = WorkspaceAccessReadOnly
	case WorkspaceAccessReadWrite, "rw":
		access = WorkspaceAccessReadWrite
	default:
		return workspaceMount{}, fmt.Errorf("sandbox workspace_access %q must be %q or %q", c.WorkspaceAccess, WorkspaceAccessReadOnly, WorkspaceAccessReadWrite)
	}

	return workspaceMount{
		Enabled: true,
		Source:  absSource,
		Target:  target,
		Access:  access,
	}, nil
}

func validateWorkspaceTarget(target string) error {
	if target == "." || !strings.HasPrefix(target, "/") {
		return fmt.Errorf("sandbox container_workdir %q must be an absolute container path", target)
	}
	reserved := map[string]struct{}{
		"/": {}, "/bin": {}, "/boot": {}, "/dev": {}, "/etc": {},
		"/lib": {}, "/lib64": {}, "/proc": {}, "/root": {}, "/run": {},
		"/sbin": {}, "/sys": {}, "/usr": {}, "/var": {},
	}
	if _, ok := reserved[target]; ok {
		return fmt.Errorf("sandbox container_workdir %q is reserved", target)
	}
	for reservedTarget := range reserved {
		if reservedTarget != "/" && strings.HasPrefix(target, reservedTarget+"/") {
			return fmt.Errorf("sandbox container_workdir %q is under reserved path %q", target, reservedTarget)
		}
	}
	return nil
}

func (m workspaceMount) DockerArgs() []string {
	mode := "rw"
	if m.Access == WorkspaceAccessReadOnly {
		mode = "ro"
	}
	return []string{"--mount=type=bind,source=" + m.Source + ",target=" + m.Target + "," + mode}
}
