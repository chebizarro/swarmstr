package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"metiq/internal/netpolicy"
)

func ValidateSandboxSecurity(cfg Config) error {
	for _, opt := range cfg.SecurityOpt {
		lower := strings.ToLower(strings.TrimSpace(opt))
		if strings.Contains(lower, "seccomp=unconfined") || strings.Contains(lower, "apparmor=unconfined") {
			return fmt.Errorf("sandbox security_opt %q is not allowed", opt)
		}
	}
	for _, tmpfs := range cfg.Tmpfs {
		target := strings.Split(strings.TrimSpace(tmpfs), ":")[0]
		if err := validateContainerTarget("sandbox tmpfs", target); err != nil {
			return err
		}
	}
	workspace, err := cfg.workspaceMount()
	if err != nil {
		return err
	}
	if workspace.Enabled {
		if err := validateHostBindSource(workspace.Source); err != nil {
			return err
		}
		if err := validateContainerTarget("sandbox workspace target", workspace.Target); err != nil {
			return err
		}
	}
	if cfg.EgressEnforced {
		if _, err := netpolicy.NormalizePolicy(netpolicy.Policy{AllowedDomains: cfg.AllowedDomains, AllowedCIDRs: cfg.AllowedCIDRs}); err != nil {
			return fmt.Errorf("sandbox egress allowlist: %w", err)
		}
	}
	return nil
}

func validateHostBindSource(source string) error {
	clean := filepath.Clean(source)
	blocked := []string{"/etc", "/proc", "/sys", "/dev", "/var/run/docker.sock", "/run/docker.sock"}
	if home, err := os.UserHomeDir(); err == nil {
		for _, sub := range []string{".ssh", ".aws", ".config/gcloud", ".docker", ".kube"} {
			blocked = append(blocked, filepath.Join(home, sub))
		}
	}
	for _, b := range blocked {
		if clean == b || strings.HasPrefix(clean, b+string(filepath.Separator)) {
			return fmt.Errorf("sandbox bind source %q is blocked", source)
		}
	}
	return nil
}

func validateContainerTarget(label, target string) error {
	t := filepath.ToSlash(filepath.Clean(strings.TrimSpace(target)))
	if t == "" || t == "." || !strings.HasPrefix(t, "/") {
		return fmt.Errorf("%s %q must be an absolute container path", label, target)
	}
	reserved := []string{"/", "/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var"}
	for _, r := range reserved {
		if t == r || (r != "/" && strings.HasPrefix(t, r+"/")) {
			return fmt.Errorf("%s %q is under reserved container path %q", label, target, r)
		}
	}
	return nil
}
