package sandbox

import (
	"fmt"
	"strings"
)

const (
	DefaultBrowserImage      = "ghcr.io/openclaw/sandbox-browser:latest"
	DefaultBrowserCDPPort    = 9222
	DefaultBrowserVNCPort    = 5900
	DefaultBrowserNoVNCPort  = 6080
	DefaultBrowserNetwork    = "bridge"
	DefaultBrowserTargetCDP  = 9222
	DefaultBrowserTargetVNC  = 5900
	DefaultBrowserTargetWeb  = 6080
	DefaultBrowserBridgeName = "sandbox-browser"
)

type BrowserSandboxConfig struct {
	Enabled          bool
	Image            string
	ContainerName    string
	Network          string
	CDPPort          int
	VNCPort          int
	NoVNCPort        int
	Headless         bool
	EnableNoVNC      bool
	AutoStart        bool
	AllowHostControl bool
	CDPSourceRange   string
	Binds            []string
}

type BrowserSandboxSpec struct {
	Enabled          bool
	Image            string
	ContainerName    string
	Network          string
	Ports            BrowserSandboxPorts
	Bridge           BrowserBridgeMetadata
	Headless         bool
	AutoStart        bool
	AllowHostControl bool
	DockerArgs       []string
}

type BrowserSandboxPorts struct {
	CDPHostPort        int
	CDPContainerPort   int
	VNCHostPort        int
	VNCContainerPort   int
	NoVNCHostPort      int
	NoVNCContainerPort int
}

type BrowserBridgeMetadata struct {
	Name           string
	CDPURL         string
	VNCURL         string
	NoVNCURL       string
	CDPSourceRange string
	HostControl    string
}

func DefaultBrowserSandboxConfig() BrowserSandboxConfig {
	return BrowserSandboxConfig{
		Image:       DefaultBrowserImage,
		Network:     DefaultBrowserNetwork,
		CDPPort:     DefaultBrowserCDPPort,
		VNCPort:     DefaultBrowserVNCPort,
		NoVNCPort:   DefaultBrowserNoVNCPort,
		EnableNoVNC: true,
		AutoStart:   true,
	}
}

func NewBrowserSandboxSpec(cfg BrowserSandboxConfig) (BrowserSandboxSpec, error) {
	defaults := DefaultBrowserSandboxConfig()
	if strings.TrimSpace(cfg.Image) == "" {
		cfg.Image = defaults.Image
	}
	if strings.TrimSpace(cfg.Network) == "" {
		cfg.Network = defaults.Network
	}
	if cfg.CDPPort == 0 {
		cfg.CDPPort = defaults.CDPPort
	}
	if cfg.VNCPort == 0 {
		cfg.VNCPort = defaults.VNCPort
	}
	if cfg.NoVNCPort == 0 {
		cfg.NoVNCPort = defaults.NoVNCPort
	}
	if !cfg.EnableNoVNC {
		cfg.EnableNoVNC = defaults.EnableNoVNC
	}
	if cfg.CDPPort < 0 || cfg.VNCPort < 0 || cfg.NoVNCPort < 0 {
		return BrowserSandboxSpec{}, fmt.Errorf("sandbox browser ports must be non-negative")
	}
	name := strings.TrimSpace(cfg.ContainerName)
	if name == "" {
		name = "swarmstr-browser"
	}
	hostControl := "disabled"
	if cfg.AllowHostControl {
		hostControl = "enabled"
	}
	ports := BrowserSandboxPorts{
		CDPHostPort:        cfg.CDPPort,
		CDPContainerPort:   DefaultBrowserTargetCDP,
		VNCHostPort:        cfg.VNCPort,
		VNCContainerPort:   DefaultBrowserTargetVNC,
		NoVNCHostPort:      cfg.NoVNCPort,
		NoVNCContainerPort: DefaultBrowserTargetWeb,
	}
	args := []string{"create", "--name", name, "--network", cfg.Network}
	args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", ports.CDPHostPort, ports.CDPContainerPort))
	args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", ports.VNCHostPort, ports.VNCContainerPort))
	if cfg.EnableNoVNC {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", ports.NoVNCHostPort, ports.NoVNCContainerPort))
	}
	args = append(args, "--env", fmt.Sprintf("SANDBOX_BROWSER_HEADLESS=%t", cfg.Headless))
	args = append(args, "--env", fmt.Sprintf("SANDBOX_BROWSER_HOST_CONTROL=%s", hostControl))
	if strings.TrimSpace(cfg.CDPSourceRange) != "" {
		args = append(args, "--env", "SANDBOX_BROWSER_CDP_SOURCE_RANGE="+strings.TrimSpace(cfg.CDPSourceRange))
	}
	for _, bind := range cfg.Binds {
		if strings.TrimSpace(bind) != "" {
			args = append(args, "-v", strings.TrimSpace(bind))
		}
	}
	args = append(args, cfg.Image)
	return BrowserSandboxSpec{
		Enabled:          cfg.Enabled,
		Image:            cfg.Image,
		ContainerName:    name,
		Network:          cfg.Network,
		Ports:            ports,
		Headless:         cfg.Headless,
		AutoStart:        cfg.AutoStart,
		AllowHostControl: cfg.AllowHostControl,
		DockerArgs:       args,
		Bridge: BrowserBridgeMetadata{
			Name:           DefaultBrowserBridgeName,
			CDPURL:         fmt.Sprintf("http://127.0.0.1:%d", ports.CDPHostPort),
			VNCURL:         fmt.Sprintf("vnc://127.0.0.1:%d", ports.VNCHostPort),
			NoVNCURL:       fmt.Sprintf("http://127.0.0.1:%d", ports.NoVNCHostPort),
			CDPSourceRange: strings.TrimSpace(cfg.CDPSourceRange),
			HostControl:    hostControl,
		},
	}, nil
}
