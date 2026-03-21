// Package installer provides real npm and archive installation backends
// for the metiq plugin lifecycle runtime.
package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Result holds the resolved metadata from an install/update operation.
type Result struct {
	// ResolvedVersion is the actual installed version (e.g. "1.2.3").
	ResolvedVersion string `json:"resolvedVersion,omitempty"`
	// ResolvedSpec is the canonical spec that was installed (e.g. "my-pkg@1.2.3").
	ResolvedSpec string `json:"resolvedSpec,omitempty"`
	// Integrity is the sri hash if available.
	Integrity string `json:"integrity,omitempty"`
	// Shasum is the npm shasum if available.
	Shasum string `json:"shasum,omitempty"`
	// InstallPath is the resolved absolute install path.
	InstallPath string `json:"installPath,omitempty"`
	// Stdout captured from the subprocess.
	Stdout string `json:"stdout,omitempty"`
	// Stderr captured from the subprocess.
	Stderr string `json:"stderr,omitempty"`
}

// Installer is the interface for plugin installation backends.
type Installer interface {
	// InstallNPM installs an npm package spec into installPath.
	InstallNPM(ctx context.Context, spec, installPath string) (Result, error)
	// UpdateNPM updates (or re-installs latest) of an npm package at installPath.
	UpdateNPM(ctx context.Context, spec, installPath string) (Result, error)
	// ExtractArchive extracts a .tar.gz or .zip archive from sourcePath to installPath.
	ExtractArchive(ctx context.Context, sourcePath, installPath string) (Result, error)
}

// New returns the default real Installer backed by npm CLI and stdlib archive support.
func New() Installer {
	return &defaultInstaller{}
}

type defaultInstaller struct{}

func (d *defaultInstaller) InstallNPM(ctx context.Context, spec, installPath string) (Result, error) {
	return installNPM(ctx, spec, installPath)
}

func (d *defaultInstaller) UpdateNPM(ctx context.Context, spec, installPath string) (Result, error) {
	return updateNPM(ctx, spec, installPath)
}

func (d *defaultInstaller) ExtractArchive(ctx context.Context, sourcePath, installPath string) (Result, error) {
	return extractArchive(ctx, sourcePath, installPath)
}

// ResolveManagedPath checks that targetPath is within the managed extensions root
// and returns the cleaned absolute path when safe. Returns ("", false) otherwise.
func ResolveManagedPath(targetPath string) (string, bool) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return "", false
	}
	root, err := filepath.Abs("./extensions")
	if err != nil {
		return "", false
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootResolved = root
	}
	candidate, err := filepath.Abs(filepath.Clean(targetPath))
	if err != nil {
		return "", false
	}
	candidateResolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		candidateResolved = candidate
	}
	rel, err := filepath.Rel(rootResolved, candidateResolved)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || rel == ".." {
		return "", false
	}
	return candidate, true
}

// EnsureDir creates a directory (and parents) if it does not exist.
// Returns an error if the path exists but is not a directory.
func EnsureDir(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("path exists but is not a directory: %s", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("stat installPath: %w", err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("mkdir installPath: %w", err)
	}
	return nil
}
