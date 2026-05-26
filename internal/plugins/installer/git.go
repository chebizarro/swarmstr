package installer

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var githubShorthandPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func installGit(ctx context.Context, repo, ref, installPath string) (Result, error) {
	repo, parsedRef, err := normalizeGitRepo(repo)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(ref) == "" {
		ref = parsedRef
	}
	installPath = strings.TrimSpace(installPath)
	if installPath == "" {
		return Result{}, fmt.Errorf("installPath is required")
	}
	if err := EnsureDir(filepath.Dir(installPath)); err != nil {
		return Result{}, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(installPath), ".metiq-git-*")
	if err != nil {
		return Result{}, fmt.Errorf("create git temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	args := []string{"clone", "--recurse-submodules", "--depth", "1"}
	if strings.TrimSpace(ref) != "" {
		args = append(args, "--branch", strings.TrimSpace(ref))
	}
	args = append(args, repo, tmp)
	res, err := runGit(ctx, args...)
	if err != nil && strings.TrimSpace(ref) != "" {
		// Some refs (e.g. commit SHA) cannot be cloned with --branch shallowly.
		res, err = runGit(ctx, "clone", "--recurse-submodules", repo, tmp)
		if err == nil {
			checkout, checkoutErr := runGitInDir(ctx, tmp, "checkout", strings.TrimSpace(ref))
			res.Stdout += checkout.Stdout
			res.Stderr += checkout.Stderr
			err = checkoutErr
		}
	}
	if err != nil {
		res.InstallPath = installPath
		return res, err
	}
	commit, _ := runGitInDir(ctx, tmp, "rev-parse", "HEAD")
	if err := os.RemoveAll(installPath); err != nil {
		return res, fmt.Errorf("remove existing installPath: %w", err)
	}
	if err := os.Rename(tmp, installPath); err != nil {
		return res, fmt.Errorf("move git checkout into place: %w", err)
	}
	res.InstallPath = installPath
	res.ResolvedSpec = repo
	res.ResolvedVersion = strings.TrimSpace(commit.Stdout)
	return res, nil
}

func normalizeGitRepo(raw string) (repo string, ref string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("git repo is required")
	}
	if before, after, ok := strings.Cut(raw, "#"); ok {
		raw = before
		ref = after
	}
	if strings.HasPrefix(raw, "github:") {
		raw = strings.TrimPrefix(raw, "github:")
	}
	if githubShorthandPattern.MatchString(raw) {
		raw = "https://github.com/" + raw + ".git"
	}
	if strings.HasPrefix(raw, "git@") {
		return raw, strings.TrimSpace(ref), nil
	}
	u, parseErr := url.Parse(raw)
	if parseErr != nil || u.Scheme == "" {
		return "", "", fmt.Errorf("invalid git repo %q", raw)
	}
	switch u.Scheme {
	case "https", "ssh", "git":
	default:
		return "", "", fmt.Errorf("unsupported git repo scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("git repo %q has no host", raw)
	}
	return raw, strings.TrimSpace(ref), nil
}

func runGit(ctx context.Context, args ...string) (Result, error) {
	return runGitInDir(ctx, "", args...)
}

func runGitInDir(ctx context.Context, dir string, args ...string) (Result, error) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return Result{}, fmt.Errorf("git not found in PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, gitBin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=echo",
		"GCM_INTERACTIVE=never",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		return res, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(res.Stderr))
	}
	return res, nil
}
