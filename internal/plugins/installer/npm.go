package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// installNPM runs `npm install --prefix <installPath> <spec>` and returns resolved metadata.
func installNPM(ctx context.Context, spec, installPath string) (Result, error) {
	installPath = strings.TrimSpace(installPath)
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Result{}, fmt.Errorf("npm spec is required")
	}
	if installPath == "" {
		return Result{}, fmt.Errorf("installPath is required")
	}
	if err := EnsureDir(installPath); err != nil {
		return Result{}, err
	}
	absPath, err := filepath.Abs(filepath.Clean(installPath))
	if err != nil {
		return Result{}, fmt.Errorf("resolve installPath: %w", err)
	}

	// Run npm install
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "npm", "install",
		"--prefix", absPath,
		"--no-audit",
		"--no-fund",
		"--save",
		spec,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{
			InstallPath: absPath,
			Stdout:      truncateOutput(stdout.String()),
			Stderr:      truncateOutput(stderr.String()),
		}, fmt.Errorf("npm install failed: %w", err)
	}

	// Resolve version from installed package metadata
	res := Result{
		InstallPath: absPath,
		Stdout:      truncateOutput(stdout.String()),
		Stderr:      truncateOutput(stderr.String()),
	}
	resolvedVersion, resolvedName, integrity, shasum := resolveNPMInstallMeta(ctx, spec, absPath)
	res.ResolvedVersion = resolvedVersion
	if resolvedName != "" && resolvedVersion != "" {
		res.ResolvedSpec = resolvedName + "@" + resolvedVersion
	} else {
		res.ResolvedSpec = spec
	}
	res.Integrity = integrity
	res.Shasum = shasum
	return res, nil
}

// updateNPM resolves the latest available version for spec and re-installs if needed.
func updateNPM(ctx context.Context, spec, installPath string) (Result, error) {
	installPath = strings.TrimSpace(installPath)
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Result{}, fmt.Errorf("npm spec is required")
	}
	if installPath == "" {
		return Result{}, fmt.Errorf("installPath is required")
	}
	absPath, err := filepath.Abs(filepath.Clean(installPath))
	if err != nil {
		return Result{}, fmt.Errorf("resolve installPath: %w", err)
	}
	if err := EnsureDir(absPath); err != nil {
		return Result{}, err
	}

	// Use npm update (works for both installed packages and fresh install)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "npm", "install",
		"--prefix", absPath,
		"--no-audit",
		"--no-fund",
		"--save",
		spec,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{
			InstallPath: absPath,
			Stdout:      truncateOutput(stdout.String()),
			Stderr:      truncateOutput(stderr.String()),
		}, fmt.Errorf("npm update failed: %w", err)
	}

	res := Result{
		InstallPath: absPath,
		Stdout:      truncateOutput(stdout.String()),
		Stderr:      truncateOutput(stderr.String()),
	}
	resolvedVersion, resolvedName, integrity, shasum := resolveNPMInstallMeta(ctx, spec, absPath)
	res.ResolvedVersion = resolvedVersion
	if resolvedName != "" && resolvedVersion != "" {
		res.ResolvedSpec = resolvedName + "@" + resolvedVersion
	} else {
		res.ResolvedSpec = spec
	}
	res.Integrity = integrity
	res.Shasum = shasum
	return res, nil
}

// resolveNPMInstallMeta reads the installed package's package.json to find the
// resolved version and integrity values. Falls back to npm ls --json.
func resolveNPMInstallMeta(ctx context.Context, spec, absPath string) (version, name, integrity, shasum string) {
	pkgName := extractNPMPackageName(spec)
	if pkgName != "" {
		// Try reading the package.json directly from node_modules
		pkgJSONPath := filepath.Join(absPath, "node_modules", pkgName, "package.json")
		data, err := readJSONFile(pkgJSONPath)
		if err == nil {
			version, _ = data["version"].(string)
			name, _ = data["name"].(string)
			// npm package.json doesn't carry integrity/shasum; fall through to ls
		}
	}

	// Use npm ls --json to get richer metadata including integrity
	var lsOut bytes.Buffer
	lsCmd := exec.CommandContext(ctx, "npm", "ls",
		"--prefix", absPath,
		"--depth=0",
		"--json",
	)
	lsCmd.Stdout = &lsOut
	if err := lsCmd.Run(); err == nil {
		var lsJSON map[string]any
		if json.Unmarshal(lsOut.Bytes(), &lsJSON) == nil {
			deps, _ := lsJSON["dependencies"].(map[string]any)
			for depName, depRaw := range deps {
				dep, ok := depRaw.(map[string]any)
				if !ok {
					continue
				}
				if pkgName != "" && depName != pkgName {
					continue
				}
				if v, ok := dep["version"].(string); ok && v != "" {
					version = v
				}
				if n := strings.TrimSpace(depName); n != "" {
					name = n
				}
				integrity, _ = dep["integrity"].(string)
				// npm ls doesn't expose shasum directly; it's in the lock file
				break
			}
		}
	}

	// Try package-lock.json for shasum/integrity
	lockPath := filepath.Join(absPath, "package-lock.json")
	lockData, err := readJSONFile(lockPath)
	if err == nil {
		if pkgName != "" {
			if packages, ok := lockData["packages"].(map[string]any); ok {
				lockKey := "node_modules/" + pkgName
				if pkg, ok := packages[lockKey].(map[string]any); ok {
					if integrity == "" {
						integrity, _ = pkg["integrity"].(string)
					}
					if version == "" {
						version, _ = pkg["version"].(string)
					}
				}
			}
		}
	}

	version = strings.TrimSpace(version)
	name = strings.TrimSpace(name)
	integrity = strings.TrimSpace(integrity)
	shasum = strings.TrimSpace(shasum)
	return
}

// extractNPMPackageName extracts the package name from a spec like:
//   - "my-package"
//   - "my-package@1.2.3"
//   - "@scope/package"
//   - "@scope/package@1.2.3"
func extractNPMPackageName(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	if strings.HasPrefix(spec, "@") {
		// Scoped package: @scope/name[@version]
		rest := spec[1:]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			return spec
		}
		nameEnd := len(spec)
		if at := strings.LastIndex(spec, "@"); at > slash+1 {
			nameEnd = at
		}
		return spec[:nameEnd]
	}
	// Unscoped: name[@version]
	at := strings.LastIndex(spec, "@")
	if at < 0 {
		return spec
	}
	return spec[:at]
}

func readJSONFile(path string) (map[string]any, error) {
	data, err := readFileBytes(path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func truncateOutput(s string) string {
	const maxBytes = 8192
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n...(truncated)"
}
