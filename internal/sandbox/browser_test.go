package sandbox

import (
	"strings"
	"testing"
)

func TestBrowserSandboxSpecDefaults(t *testing.T) {
	spec, err := NewBrowserSandboxSpec(BrowserSandboxConfig{Enabled: true})
	if err != nil {
		t.Fatalf("NewBrowserSandboxSpec: %v", err)
	}
	if !spec.Enabled || spec.Image != DefaultBrowserImage || spec.Network != DefaultBrowserNetwork {
		t.Fatalf("unexpected default spec: %+v", spec)
	}
	if spec.Ports.CDPHostPort != DefaultBrowserCDPPort || spec.Ports.VNCHostPort != DefaultBrowserVNCPort || spec.Ports.NoVNCHostPort != DefaultBrowserNoVNCPort {
		t.Fatalf("unexpected ports: %+v", spec.Ports)
	}
	if spec.Bridge.CDPURL != "http://127.0.0.1:9222" || spec.Bridge.NoVNCURL != "http://127.0.0.1:6080" {
		t.Fatalf("unexpected bridge metadata: %+v", spec.Bridge)
	}
	if spec.AllowHostControl || spec.Bridge.HostControl != "disabled" {
		t.Fatalf("host control should default disabled: %+v", spec)
	}
	if !contains(spec.DockerArgs, "127.0.0.1:9222:9222") || !contains(spec.DockerArgs, "127.0.0.1:5900:5900") || !contains(spec.DockerArgs, "127.0.0.1:6080:6080") {
		t.Fatalf("missing port args: %#v", spec.DockerArgs)
	}
}

func TestBrowserSandboxSpecCustomPortsAndPolicy(t *testing.T) {
	spec, err := NewBrowserSandboxSpec(BrowserSandboxConfig{
		Image:            "browser:test",
		ContainerName:    "browser-one",
		Network:          "sandbox-net",
		CDPPort:          19333,
		VNCPort:          15900,
		NoVNCPort:        16080,
		Headless:         true,
		EnableNoVNC:      true,
		AutoStart:        true,
		AllowHostControl: true,
		CDPSourceRange:   "127.0.0.1/32",
		Binds:            []string{"/host/cache:/cache:ro"},
	})
	if err != nil {
		t.Fatalf("NewBrowserSandboxSpec: %v", err)
	}
	if spec.Image != "browser:test" || spec.ContainerName != "browser-one" || spec.Network != "sandbox-net" {
		t.Fatalf("unexpected custom spec: %+v", spec)
	}
	if spec.Ports.CDPHostPort != 19333 || spec.Ports.VNCHostPort != 15900 || spec.Ports.NoVNCHostPort != 16080 {
		t.Fatalf("unexpected custom ports: %+v", spec.Ports)
	}
	if !spec.Headless || !spec.AutoStart || !spec.AllowHostControl || spec.Bridge.HostControl != "enabled" {
		t.Fatalf("unexpected browser policies: %+v", spec)
	}
	joined := strings.Join(spec.DockerArgs, " ")
	for _, want := range []string{"--name browser-one", "--network sandbox-net", "127.0.0.1:19333:9222", "SANDBOX_BROWSER_CDP_SOURCE_RANGE=127.0.0.1/32", "/host/cache:/cache:ro", "browser:test"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in args %#v", want, spec.DockerArgs)
		}
	}
}

func TestBrowserSandboxSpecRejectsNegativePorts(t *testing.T) {
	if _, err := NewBrowserSandboxSpec(BrowserSandboxConfig{CDPPort: -1}); err == nil {
		t.Fatal("expected negative port error")
	}
}
