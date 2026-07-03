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
	hasAllowlist := len(cleanStrings(cfg.AllowedDomains)) > 0 || len(cleanStrings(cfg.AllowedCIDRs)) > 0
	if cfg.EgressEnforced {
		if _, err := netpolicy.NormalizePolicy(netpolicy.Policy{AllowedDomains: cfg.AllowedDomains, AllowedCIDRs: cfg.AllowedCIDRs}); err != nil {
			return fmt.Errorf("sandbox egress allowlist: %w", err)
		}
		// Real egress enforcement (an external network namespace, host firewall,
		// or an enforcing proxy sidecar that untrusted in-container code cannot
		// bypass) is not yet implemented. The previous in-container root+NET_ADMIN
		// +iptables approach and the nop proxy-env approach were brittle and
		// bypassable. Rather than silently advertise enforcement we cannot deliver,
		// fail closed.
		return fmt.Errorf("sandbox egress_enforced=true is not supported: no real egress enforcement backend is available (deferred: external netns/firewall/proxy sidecar); refusing to advertise unenforced egress. Set egress_enforced=false and network_disabled=true, or provide an enforcing backend")
	}
	if cfg.AllowNetwork && hasAllowlist {
		// An allowlist without enforcement gives operators a false sense of
		// containment while egress is actually unrestricted. Fail closed.
		return fmt.Errorf("sandbox allow_network is enabled with an egress allowlist but egress cannot be enforced: the allowlist is advisory metadata only and egress would be unrestricted. Disable allow_network to fail closed, or remove the allowlist to explicitly accept unrestricted egress")
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
