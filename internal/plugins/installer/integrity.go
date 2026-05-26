package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const PluginIntegrityFile = ".metiq-integrity.json"

type PluginIntegrityRecord struct {
	Algorithm  string    `json:"algorithm"`
	Hash       string    `json:"hash"`
	RecordedAt time.Time `json:"recorded_at"`
	FileCount  int       `json:"file_count"`
}

// RecordPluginIntegrity computes and persists the current install-tree hash.
func RecordPluginIntegrity(pluginPath string) (PluginIntegrityRecord, error) {
	record, err := ComputePluginIntegrity(pluginPath)
	if err != nil {
		return PluginIntegrityRecord{}, err
	}
	record.RecordedAt = time.Now().UTC()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return PluginIntegrityRecord{}, fmt.Errorf("marshal plugin integrity: %w", err)
	}
	if err := os.WriteFile(filepath.Join(pluginPath, PluginIntegrityFile), append(data, '\n'), 0o644); err != nil {
		return PluginIntegrityRecord{}, fmt.Errorf("write plugin integrity: %w", err)
	}
	return record, nil
}

// VerifyPluginIntegrity verifies a recorded install-tree hash when present.
// Missing records are tolerated so local path/development plugins continue to
// load; managed installer paths record this file after successful install.
func VerifyPluginIntegrity(pluginPath string) error {
	pluginPath = strings.TrimSpace(pluginPath)
	if pluginPath == "" {
		return fmt.Errorf("pluginPath is required")
	}
	root := pluginPath
	if info, statErr := os.Stat(root); statErr == nil && !info.IsDir() {
		root = filepath.Dir(root)
	}
	data, err := os.ReadFile(filepath.Join(root, PluginIntegrityFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read plugin integrity: %w", err)
	}
	var expected PluginIntegrityRecord
	if err := json.Unmarshal(data, &expected); err != nil {
		return fmt.Errorf("parse plugin integrity: %w", err)
	}
	if expected.Algorithm != "sha256" || strings.TrimSpace(expected.Hash) == "" {
		return fmt.Errorf("plugin integrity record is invalid")
	}
	actual, err := ComputePluginIntegrity(root)
	if err != nil {
		return err
	}
	if !strings.EqualFold(expected.Hash, actual.Hash) {
		return fmt.Errorf("plugin integrity mismatch: expected %s, got %s", expected.Hash, actual.Hash)
	}
	return nil
}

// ComputePluginIntegrity returns a stable SHA-256 hash over regular files in a
// plugin directory. The integrity sidecar itself is excluded.
func ComputePluginIntegrity(pluginPath string) (PluginIntegrityRecord, error) {
	pluginPath = strings.TrimSpace(pluginPath)
	if pluginPath == "" {
		return PluginIntegrityRecord{}, fmt.Errorf("pluginPath is required")
	}
	root, err := filepath.Abs(filepath.Clean(pluginPath))
	if err != nil {
		return PluginIntegrityRecord{}, fmt.Errorf("resolve plugin path: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return PluginIntegrityRecord{}, fmt.Errorf("stat plugin path: %w", err)
	}
	if !info.IsDir() {
		return PluginIntegrityRecord{}, fmt.Errorf("plugin path is not a directory: %s", pluginPath)
	}

	var files []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeType != 0 {
			return nil
		}
		if filepath.Base(path) == PluginIntegrityFile {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return PluginIntegrityRecord{}, fmt.Errorf("walk plugin tree: %w", err)
	}
	sort.Strings(files)

	h := sha256.New()
	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		fmt.Fprintf(h, "file:%s\n", rel)
		f, err := os.Open(path)
		if err != nil {
			return PluginIntegrityRecord{}, fmt.Errorf("open %s: %w", rel, err)
		}
		if _, err := io.Copy(h, f); err != nil {
			_ = f.Close()
			return PluginIntegrityRecord{}, fmt.Errorf("hash %s: %w", rel, err)
		}
		if err := f.Close(); err != nil {
			return PluginIntegrityRecord{}, fmt.Errorf("close %s: %w", rel, err)
		}
		h.Write([]byte("\n"))
	}
	return PluginIntegrityRecord{Algorithm: "sha256", Hash: hex.EncodeToString(h.Sum(nil)), FileCount: len(files)}, nil
}

func recordIntegrityForResult(res Result, fallbackPath string) (Result, error) {
	installPath := strings.TrimSpace(res.InstallPath)
	if installPath == "" {
		installPath = strings.TrimSpace(fallbackPath)
		res.InstallPath = installPath
	}
	if installPath == "" {
		return res, nil
	}
	record, err := RecordPluginIntegrity(installPath)
	if err != nil {
		return res, err
	}
	if res.Integrity == "" {
		res.Integrity = "sha256-" + record.Hash
	}
	return res, nil
}
