package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// InteractiveSandboxRunner starts a long-lived command with attached standard
// streams. It is intentionally separate from SandboxRunner so backends that
// cannot provide an isolated interactive process fail closed.
type InteractiveSandboxRunner interface {
	SandboxRunner
	StartInteractive(ctx context.Context, cmd []string, env []string, workdir string) (*InteractiveProcess, error)
}

// InteractiveProcess owns an attached sandbox process.
type InteractiveProcess struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser

	cmd      *exec.Cmd
	cidFile  string
	waitOnce sync.Once
	waitErr  error
	killOnce sync.Once
}

// Wait reaps the sandbox process. It is safe to call more than once.
func (p *InteractiveProcess) Wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		p.removeContainer()
		_ = os.Remove(p.cidFile)
	})
	return p.waitErr
}

func (p *InteractiveProcess) removeContainer() {
	data, err := os.ReadFile(p.cidFile)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = exec.CommandContext(ctx, "docker", "rm", "-f", strings.TrimSpace(string(data))).CombinedOutput()
}

// Kill forcibly removes the Docker container and terminates the attached CLI.
// Removing by container ID prevents a detached container surviving a client exit.
func (p *InteractiveProcess) Kill() error {
	var killErr error
	p.killOnce.Do(func() {
		p.removeContainer()
		if p.cmd.Process != nil {
			if err := p.cmd.Process.Kill(); err != nil && killErr == nil {
				killErr = err
			}
		}
	})
	return killErr
}

// StartInteractive starts an attached command inside an ephemeral Docker
// container using the same hardening flags as Run.
func (s *DockerSandbox) StartInteractive(ctx context.Context, cmd []string, env []string, workdir string) (*InteractiveProcess, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("sandbox: empty command")
	}
	if err := ValidateSandboxSecurity(s.cfg); err != nil {
		return nil, err
	}
	image := s.cfg.DockerImage
	if image == "" {
		return nil, fmt.Errorf("interactive docker sandbox requires docker_image")
	}
	cid, err := os.CreateTemp("", "metiq-sandbox-cid-*")
	if err != nil {
		return nil, fmt.Errorf("create sandbox cidfile: %w", err)
	}
	cidPath := cid.Name()
	if err := cid.Close(); err != nil {
		_ = os.Remove(cidPath)
		return nil, err
	}
	// Docker requires --cidfile to name a path that does not already exist.
	if err := os.Remove(cidPath); err != nil {
		return nil, fmt.Errorf("prepare sandbox cidfile: %w", err)
	}

	args := s.dockerRunArgs(image, cmd, env, workdir)
	if len(args) >= 3 {
		args[2] = "--interactive"
	}
	imageIndex := len(args) - len(cmd) - 1
	args = append(args[:imageIndex], append([]string{"--cidfile=" + cidPath}, args[imageIndex:]...)...)

	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = os.Environ()
	stdin, err := command.StdinPipe()
	if err != nil {
		_ = os.Remove(cidPath)
		return nil, fmt.Errorf("sandbox stdin pipe: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = os.Remove(cidPath)
		return nil, fmt.Errorf("sandbox stdout pipe: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = os.Remove(cidPath)
		return nil, fmt.Errorf("sandbox stderr pipe: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = os.Remove(cidPath)
		return nil, fmt.Errorf("start docker sandbox: %w", err)
	}
	return &InteractiveProcess{Stdin: stdin, Stdout: stdout, Stderr: stderr, cmd: command, cidFile: cidPath}, nil
}

var _ InteractiveSandboxRunner = (*DockerSandbox)(nil)
