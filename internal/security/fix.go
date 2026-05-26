package security

import (
	"fmt"
	"os"
	"strings"
)

// FixOptions controls safe audit auto-remediation.
type FixOptions struct {
	BootstrapPath string
	SecretPaths   []string
	DryRun        bool
}

// FixAction records one attempted remediation.
type FixAction struct {
	ID      string `json:"id"`
	Path    string `json:"path,omitempty"`
	Applied bool   `json:"applied"`
	Error   string `json:"error,omitempty"`
}

// FixResult is returned by Fix.
type FixResult struct {
	Actions []FixAction `json:"actions"`
}

// Fix applies conservative local remediations: chmod 0600 on bootstrap and
// plaintext secret files. Risky semantic config changes remain advisory.
func Fix(opts FixOptions) FixResult {
	var result FixResult
	if path := defaultBootstrapPath(opts.BootstrapPath); path != "" {
		result.Actions = append(result.Actions, chmodFix("bootstrap-file-perms", path, 0o600, opts.DryRun))
	}
	paths := opts.SecretPaths
	if paths == nil {
		if home, err := os.UserHomeDir(); err == nil {
			paths = []string{home + "/.metiq/.env", home + "/.metiq/mcp-auth.json"}
		}
	}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		result.Actions = append(result.Actions, chmodFix("secret-file-perms", path, 0o600, opts.DryRun))
	}
	return result
}

func chmodFix(id, path string, mode os.FileMode, dryRun bool) FixAction {
	a := FixAction{ID: id, Path: path}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return a
		}
		a.Error = err.Error()
		return a
	}
	if info.IsDir() {
		mode = 0o700
	}
	if info.Mode().Perm() == mode {
		a.Applied = true
		return a
	}
	if dryRun {
		return a
	}
	if err := os.Chmod(path, mode); err != nil {
		a.Error = fmt.Sprintf("chmod: %v", err)
		return a
	}
	a.Applied = true
	return a
}

func defaultBootstrapPath(path string) string {
	if strings.TrimSpace(path) != "" {
		return strings.TrimSpace(path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/.metiq/bootstrap.json"
}
