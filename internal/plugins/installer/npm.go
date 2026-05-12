package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// installNPM runs a managed `npm install --prefix <installPath> <spec>` and returns resolved metadata.
func installNPM(ctx context.Context, spec, installPath string) (Result, error) {
	return managedNPMInstall(ctx, spec, installPath, "install")
}

// updateNPM resolves the latest available version for spec and re-installs if needed.
func updateNPM(ctx context.Context, spec, installPath string) (Result, error) {
	return managedNPMInstall(ctx, spec, installPath, "update")
}

func managedNPMInstall(ctx context.Context, spec, installPath, op string) (Result, error) {
	installPath = strings.TrimSpace(installPath)
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Result{}, fmt.Errorf("npm spec is required")
	}
	if installPath == "" {
		return Result{}, fmt.Errorf("installPath is required")
	}
	if strings.ContainsAny(spec, "\x00\n\r") || strings.HasPrefix(spec, "-") {
		return Result{}, fmt.Errorf("unsafe npm spec")
	}
	absPath, err := filepath.Abs(filepath.Clean(installPath))
	if err != nil {
		return Result{}, fmt.Errorf("resolve installPath: %w", err)
	}
	if err := EnsureDir(absPath); err != nil {
		return Result{}, err
	}

	backupPath, err := backupInstallPath(absPath)
	if err != nil {
		return Result{InstallPath: absPath}, err
	}
	if backupPath != "" {
		defer os.RemoveAll(backupPath)
	}

	stdout, stderr, err := runSafeNPMInstall(ctx, absPath, spec, false)
	if err != nil && strings.Contains(stderr, "ERESOLVE") {
		stdout, stderr, err = runSafeNPMInstall(ctx, absPath, spec, true)
	}
	res := Result{InstallPath: absPath, Stdout: truncateOutput(stdout), Stderr: truncateOutput(stderr)}
	if err != nil {
		restoreInstallPath(absPath, backupPath)
		return res, fmt.Errorf("npm %s failed: %w", op, err)
	}
	if auditOut, auditErr, err := runNPMAudit(ctx, absPath); err != nil {
		restoreInstallPath(absPath, backupPath)
		res.Stdout = truncateOutput(strings.TrimSpace(res.Stdout + "\n" + auditOut))
		res.Stderr = truncateOutput(strings.TrimSpace(res.Stderr + "\n" + auditErr))
		return res, fmt.Errorf("npm security audit failed: %w", err)
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
	if err := validateNPMIntegrity(spec, absPath, integrity); err != nil {
		restoreInstallPath(absPath, backupPath)
		return res, err
	}
	return res, nil
}

func runSafeNPMInstall(ctx context.Context, absPath, spec string, legacyPeerDeps bool) (string, string, error) {
	args := []string{
		"install",
		"--prefix", absPath,
		"--audit=false",
		"--fund=false",
		"--ignore-scripts",
		"--package-lock=true",
		"--save",
	}
	if legacyPeerDeps {
		args = append(args, "--legacy-peer-deps")
	}
	args = append(args, spec)
	return runNPM(ctx, args...)
}

func runNPMAudit(ctx context.Context, absPath string) (string, string, error) {
	return runNPM(ctx, "audit", "--prefix", absPath, "--audit-level=high", "--json")
}

func runNPM(ctx context.Context, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "npm", args...)
	cmd.Env = safeNPMEnv(os.Environ())
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func safeNPMEnv(env []string) []string {
	out := make([]string, 0, len(env)+5)
	for _, kv := range env {
		if strings.HasPrefix(kv, "npm_config_") || strings.HasPrefix(kv, "NPM_CONFIG_") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out,
		"npm_config_audit=false",
		"npm_config_fund=false",
		"npm_config_ignore_scripts=true",
		"npm_config_package_lock=true",
	)
	return out
}

func validateNPMIntegrity(spec, absPath, integrity string) error {
	pkgName := extractNPMPackageName(spec)
	if pkgName == "" {
		return fmt.Errorf("could not resolve npm package name")
	}
	if strings.TrimSpace(integrity) != "" {
		return nil
	}
	lockPath := filepath.Join(absPath, "package-lock.json")
	lockData, err := readJSONFile(lockPath)
	if err != nil {
		return fmt.Errorf("npm integrity validation failed: package-lock.json missing or invalid: %w", err)
	}
	packages, _ := lockData["packages"].(map[string]any)
	pkg, _ := packages["node_modules/"+pkgName].(map[string]any)
	if pkg == nil {
		return fmt.Errorf("npm integrity validation failed for %s", pkgName)
	}
	lockIntegrity, _ := pkg["integrity"].(string)
	if strings.TrimSpace(lockIntegrity) == "" {
		return fmt.Errorf("npm integrity validation failed for %s", pkgName)
	}
	return nil
}

func backupInstallPath(absPath string) (string, error) {
	info, err := os.Stat(absPath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stat installPath for rollback: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("installPath is not a directory: %s", absPath)
	}
	backupPath, err := os.MkdirTemp(filepath.Dir(absPath), ".npm-install-backup-*")
	if err != nil {
		return "", fmt.Errorf("create rollback backup: %w", err)
	}
	if err := copyDir(absPath, backupPath); err != nil {
		os.RemoveAll(backupPath)
		return "", fmt.Errorf("copy rollback backup: %w", err)
	}
	return backupPath, nil
}

func restoreInstallPath(absPath, backupPath string) {
	if backupPath == "" {
		return
	}
	_ = os.RemoveAll(absPath)
	_ = os.MkdirAll(absPath, 0o755)
	_ = copyDir(backupPath, absPath)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
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
