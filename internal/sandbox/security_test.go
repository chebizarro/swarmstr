package sandbox

import "testing"

func TestValidateSandboxSecurityBlocksDangerousModes(t *testing.T) {
	for _, cfg := range []Config{{SecurityOpt: []string{"seccomp=unconfined"}}, {SecurityOpt: []string{"apparmor=unconfined"}}, {Tmpfs: []string{"/proc:rw"}}} {
		if err := ValidateSandboxSecurity(cfg); err == nil {
			t.Fatalf("expected validation error for %+v", cfg)
		}
	}
}

func TestValidateSandboxSecurityBlocksDangerousWorkspace(t *testing.T) {
	if err := ValidateSandboxSecurity(Config{WorkspaceDir: "/etc", ContainerWorkdir: "/workspace"}); err == nil {
		t.Fatal("expected /etc workspace source to be blocked")
	}
}

func TestValidateSandboxSecurityAllowsSafeConfig(t *testing.T) {
	tmp := t.TempDir()
	cfg := Config{WorkspaceDir: tmp, ContainerWorkdir: "/workspace", WorkspaceAccess: WorkspaceAccessReadOnly, EgressEnforced: true, AllowedDomains: []string{"api.example.com"}, AllowedCIDRs: []string{"8.8.8.0/24"}}
	if err := ValidateSandboxSecurity(cfg); err != nil {
		t.Fatalf("safe config rejected: %v", err)
	}
}
