package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxExtractedSize is the maximum total bytes we'll extract from an archive (1 GiB).
	maxExtractedSize = 1 << 30
	// maxExtractedFiles is the maximum number of files we'll extract from an archive.
	maxExtractedFiles = 10_000
)

// extractArchive extracts a .tar.gz (.tgz) or .zip archive to installPath.
// It enforces zip-slip protection and extraction size limits.
func extractArchive(_ context.Context, sourcePath, installPath string) (Result, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	installPath = strings.TrimSpace(installPath)
	if sourcePath == "" {
		return Result{}, fmt.Errorf("sourcePath is required")
	}
	if installPath == "" {
		return Result{}, fmt.Errorf("installPath is required")
	}
	if err := EnsureDir(installPath); err != nil {
		return Result{}, err
	}
	absInstall, err := filepath.Abs(filepath.Clean(installPath))
	if err != nil {
		return Result{}, fmt.Errorf("resolve installPath: %w", err)
	}

	lower := strings.ToLower(sourcePath)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		if err := extractTarGz(sourcePath, absInstall); err != nil {
			return Result{InstallPath: absInstall}, err
		}
	case strings.HasSuffix(lower, ".zip"):
		if err := extractZip(sourcePath, absInstall); err != nil {
			return Result{InstallPath: absInstall}, err
		}
	default:
		return Result{}, fmt.Errorf("unsupported archive format (expected .tar.gz, .tgz, or .zip): %s", sourcePath)
	}
	return Result{InstallPath: absInstall}, nil
}

// extractTarGz extracts a gzip-compressed tar archive to destDir with zip-slip protection.
func extractTarGz(sourcePath, destDir string) error {
	f, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var totalSize int64
	fileCount := 0

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		fileCount++
		if fileCount > maxExtractedFiles {
			return fmt.Errorf("archive exceeds maximum file count (%d)", maxExtractedFiles)
		}

		// Strip the first path component (e.g. "package/") that npm archives include.
		stripped := stripTopLevel(hdr.Name)
		if stripped == "" {
			continue
		}

		destPath, err := safeJoin(destDir, stripped)
		if err != nil {
			return fmt.Errorf("unsafe archive path %q: %w", hdr.Name, err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", destPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", destPath, err)
			}
			written, err := extractFile(tr, destPath, hdr.FileInfo().Mode())
			if err != nil {
				return err
			}
			totalSize += written
			if totalSize > maxExtractedSize {
				return fmt.Errorf("archive exceeds maximum extraction size (%d bytes)", maxExtractedSize)
			}
		case tar.TypeSymlink:
			// Validate symlink target doesn't escape destDir
			linkTarget := hdr.Linkname
			if filepath.IsAbs(linkTarget) {
				return fmt.Errorf("archive contains absolute symlink target: %s -> %s", hdr.Name, linkTarget)
			}
			resolved := filepath.Join(filepath.Dir(destPath), linkTarget)
			if _, err := safeJoin(destDir, resolved); err != nil {
				return fmt.Errorf("unsafe symlink in archive %q -> %q: %w", hdr.Name, linkTarget, err)
			}
			_ = os.Remove(destPath)
			if err := os.Symlink(linkTarget, destPath); err != nil {
				return fmt.Errorf("symlink %s: %w", destPath, err)
			}
		default:
			// Skip special file types (devices, etc.)
		}
	}
	return nil
}

// extractZip extracts a .zip archive to destDir with zip-slip protection.
func extractZip(sourcePath, destDir string) error {
	r, err := zip.OpenReader(sourcePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var totalSize int64
	if len(r.File) > maxExtractedFiles {
		return fmt.Errorf("archive exceeds maximum file count (%d)", maxExtractedFiles)
	}

	for _, f := range r.File {
		stripped := stripTopLevel(f.Name)
		if stripped == "" {
			continue
		}
		destPath, err := safeJoin(destDir, stripped)
		if err != nil {
			return fmt.Errorf("unsafe archive path %q: %w", f.Name, err)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", destPath, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("mkdir parent %s: %w", destPath, err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		written, writeErr := extractFile(rc, destPath, f.Mode())
		rc.Close()
		if writeErr != nil {
			return writeErr
		}
		totalSize += written
		if totalSize > maxExtractedSize {
			return fmt.Errorf("archive exceeds maximum extraction size (%d bytes)", maxExtractedSize)
		}
	}
	return nil
}

// extractFile writes r to destPath with mode, returning bytes written.
func extractFile(r io.Reader, destPath string, mode os.FileMode) (int64, error) {
	// Enforce safe mode bits
	if mode == 0 {
		mode = 0o644
	}
	mode = mode & 0o755

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, fmt.Errorf("create %s: %w", destPath, err)
	}
	defer out.Close()

	// Limit reads to prevent decompression bombs
	lr := io.LimitReader(r, maxExtractedSize+1)
	n, err := io.Copy(out, lr)
	if err != nil {
		return n, fmt.Errorf("write %s: %w", destPath, err)
	}
	return n, nil
}

// safeJoin joins base and name, returning an error if the result escapes base.
// This is the core zip-slip prevention check.
func safeJoin(base, name string) (string, error) {
	name = filepath.FromSlash(name)
	// Clean and make absolute
	joined := filepath.Join(base, name)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	// Ensure the resolved path is within base
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(baseAbs, abs)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("path escapes extraction directory: %s", name)
	}
	return abs, nil
}

// stripTopLevel removes the first path component from an archive entry name.
// npm tarballs wrap everything in a "package/" top-level directory; we strip it
// so the contents are extracted directly into installPath.
// If the entry has only one component (it is the top-level dir itself), "" is returned.
func stripTopLevel(name string) string {
	name = filepath.ToSlash(strings.TrimPrefix(name, "./"))
	name = strings.TrimSuffix(name, "/")
	slash := strings.Index(name, "/")
	if slash < 0 {
		// Top-level entry (e.g. "package") — skip
		return ""
	}
	return name[slash+1:]
}

// readFileBytes is a tiny helper to read a file's bytes.
func readFileBytes(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
