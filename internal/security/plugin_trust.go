package security

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

const IntegritySidecarName = ".metiq-integrity.json"

// PluginIntegrityRecord stores a deterministic digest for an installed plugin.
type PluginIntegrityRecord struct {
	Algorithm string    `json:"algorithm"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	Files     []string  `json:"files,omitempty"`
}

// RecordPluginIntegrity computes and writes an integrity sidecar for pluginPath.
func RecordPluginIntegrity(pluginPath string) (PluginIntegrityRecord, error) {
	record, err := ComputePluginIntegrity(pluginPath)
	if err != nil {
		return PluginIntegrityRecord{}, err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return PluginIntegrityRecord{}, err
	}
	if err := os.WriteFile(filepath.Join(pluginPath, IntegritySidecarName), data, 0o644); err != nil {
		return PluginIntegrityRecord{}, fmt.Errorf("write integrity sidecar: %w", err)
	}
	return record, nil
}

// VerifyPluginIntegrity compares current plugin files with the recorded digest.
func VerifyPluginIntegrity(pluginPath string) (PluginIntegrityRecord, bool, error) {
	data, err := os.ReadFile(filepath.Join(pluginPath, IntegritySidecarName))
	if err != nil {
		return PluginIntegrityRecord{}, false, err
	}
	var recorded PluginIntegrityRecord
	if err := json.Unmarshal(data, &recorded); err != nil {
		return PluginIntegrityRecord{}, false, fmt.Errorf("parse integrity sidecar: %w", err)
	}
	current, err := ComputePluginIntegrity(pluginPath)
	if err != nil {
		return recorded, false, err
	}
	return recorded, strings.EqualFold(recorded.Hash, current.Hash), nil
}

// ComputePluginIntegrity returns a sha256 over relative file names and content.
func ComputePluginIntegrity(pluginPath string) (PluginIntegrityRecord, error) {
	pluginPath = strings.TrimSpace(pluginPath)
	if pluginPath == "" {
		return PluginIntegrityRecord{}, fmt.Errorf("plugin path is required")
	}
	var files []string
	if err := filepath.WalkDir(pluginPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == IntegritySidecarName {
			return nil
		}
		rel, err := filepath.Rel(pluginPath, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return PluginIntegrityRecord{}, fmt.Errorf("walk plugin: %w", err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		path := filepath.Join(pluginPath, filepath.FromSlash(rel))
		fmt.Fprintf(h, "file:%s\n", rel)
		f, err := os.Open(path)
		if err != nil {
			return PluginIntegrityRecord{}, err
		}
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return PluginIntegrityRecord{}, copyErr
		}
		if closeErr != nil {
			return PluginIntegrityRecord{}, closeErr
		}
		fmt.Fprintln(h)
	}
	return PluginIntegrityRecord{Algorithm: "sha256", Hash: hex.EncodeToString(h.Sum(nil)), CreatedAt: time.Now().UTC(), Files: files}, nil
}

func auditPluginIntegrity(paths []string) []Finding {
	var findings []Finding
	for _, pluginPath := range paths {
		pluginPath = strings.TrimSpace(pluginPath)
		if pluginPath == "" {
			continue
		}
		info, err := os.Stat(pluginPath)
		if err != nil || !info.IsDir() {
			continue
		}
		_, ok, err := VerifyPluginIntegrity(pluginPath)
		if err != nil {
			if os.IsNotExist(err) {
				findings = append(findings, Finding{CheckID: "plugin-integrity-missing", Severity: SeverityWarn, Message: fmt.Sprintf("plugin %s has no recorded integrity sidecar", pluginPath), Remediation: "Record a SHA-256 integrity sidecar at install time and verify it before load."})
				continue
			}
			findings = append(findings, Finding{CheckID: "plugin-integrity-error", Severity: SeverityWarn, Message: fmt.Sprintf("plugin %s integrity could not be verified: %v", pluginPath, err), Remediation: "Reinstall the plugin from a trusted source and record integrity metadata."})
			continue
		}
		if !ok {
			findings = append(findings, Finding{CheckID: "plugin-integrity-mismatch", Severity: SeverityCritical, Message: fmt.Sprintf("plugin %s files do not match recorded integrity hash", pluginPath), Remediation: "Refuse to load the plugin until it is reinstalled or re-attested from a trusted source."})
		}
	}
	return findings
}
