package sandbox

import (
	"strings"
	"testing"
)

func TestDockerRunArgs_WorkspaceMountDefaults(t *testing.T) {
	dir := t.TempDir()
	s := &DockerSandbox{cfg: Config{WorkspaceDir: dir}}

	args := s.dockerRunArgs("alpine:3", []string{"pwd"}, nil, "")

	mount := findPrefix(args, "--mount=type=bind,")
	if mount == "" {
		t.Fatalf("missing workspace mount in %#v", args)
	}
	if !strings.Contains(mount, "source="+dir) || !strings.Contains(mount, "target=/workspace") || !strings.HasSuffix(mount, ",rw") {
		t.Fatalf("unexpected mount arg: %s", mount)
	}
	if !contains(args, "--workdir=/workspace") {
		t.Fatalf("missing default workspace workdir in %#v", args)
	}
}

func TestDockerRunArgs_WorkspaceReadOnlyCustomTarget(t *testing.T) {
	dir := t.TempDir()
	s := &DockerSandbox{cfg: Config{
		WorkspaceDir:     dir,
		ContainerWorkdir: "/work/project",
		WorkspaceAccess:  WorkspaceAccessReadOnly,
	}}

	args := s.dockerRunArgs("alpine:3", []string{"pwd"}, nil, "")
	mount := findPrefix(args, "--mount=type=bind,")
	if !strings.Contains(mount, "target=/work/project") || !strings.HasSuffix(mount, ",ro") {
		t.Fatalf("unexpected mount arg: %s", mount)
	}
	if !contains(args, "--workdir=/work/project") {
		t.Fatalf("missing custom workspace workdir in %#v", args)
	}
}

func TestDockerRunArgs_WorkdirOverridePreserved(t *testing.T) {
	dir := t.TempDir()
	s := &DockerSandbox{cfg: Config{WorkspaceDir: dir}}

	args := s.dockerRunArgs("alpine:3", []string{"pwd"}, nil, "/tmp")
	if !contains(args, "--workdir=/tmp") {
		t.Fatalf("explicit workdir not preserved in %#v", args)
	}
}

func TestWorkspaceMountValidation(t *testing.T) {
	dir := t.TempDir()
	cases := []Config{
		{WorkspaceDir: dir, ContainerWorkdir: "relative"},
		{WorkspaceDir: dir, ContainerWorkdir: "/"},
		{WorkspaceDir: dir, ContainerWorkdir: "/proc/work"},
		{WorkspaceDir: dir, WorkspaceAccess: "write_only"},
		{WorkspaceDir: dir + "/missing"},
	}
	for _, cfg := range cases {
		if _, err := cfg.workspaceMount(); err == nil {
			t.Fatalf("workspaceMount(%+v) expected error", cfg)
		}
	}
}

func TestNewFromMap_WorkspaceConfig(t *testing.T) {
	runner, err := NewFromMap(map[string]any{
		"driver":            "docker",
		"workspace_dir":     "/tmp/project",
		"container_workdir": "/work/project",
		"workspace_access":  "read_only",
	})
	if err != nil {
		t.Fatalf("NewFromMap: %v", err)
	}
	docker := runner.(*DockerSandbox)
	if docker.cfg.WorkspaceDir != "/tmp/project" || docker.cfg.ContainerWorkdir != "/work/project" || docker.cfg.WorkspaceAccess != "read_only" {
		t.Fatalf("unexpected workspace config: %+v", docker.cfg)
	}
}

func findPrefix(values []string, prefix string) string {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return value
		}
	}
	return ""
}
