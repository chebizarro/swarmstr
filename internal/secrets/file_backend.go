package secrets

import (
	"os"
	"path/filepath"
	"strings"
)

// FileBackend is the explicit plaintext fallback for secret persistence.
type FileBackend struct {
	path string
}

func NewFileBackend(path string) *FileBackend {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return &FileBackend{path: path}
}

func (b *FileBackend) Name() string { return "file" }

func (b *FileBackend) Get(key string) (string, bool, error) {
	if b == nil || strings.TrimSpace(b.path) == "" {
		return "", false, nil
	}
	raw, err := os.ReadFile(b.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(raw), true, nil
}

func (b *FileBackend) Set(key, value string) error {
	if b == nil || strings.TrimSpace(b.path) == "" {
		return nil
	}
	dir := filepath.Dir(b.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".mcp-auth-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(value); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, b.path)
}

func (b *FileBackend) Delete(key string) error {
	if b == nil || strings.TrimSpace(b.path) == "" {
		return nil
	}
	if err := os.Remove(b.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
