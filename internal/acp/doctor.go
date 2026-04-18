package acp

import (
	"context"
	"fmt"
)

// ── Doctor report ───────────────────────────────────────────────────────────

// DoctorReport is the result of a health diagnostic check.
type DoctorReport struct {
	// OK is true when the check passed.
	OK bool `json:"ok"`
	// Code is a machine-readable error code (empty when OK).
	Code string `json:"code,omitempty"`
	// Message is a human-readable summary.
	Message string `json:"message"`
	// InstallCommand is an optional remediation hint (e.g. "npm install ...").
	InstallCommand string `json:"install_command,omitempty"`
	// Details provides additional diagnostic information.
	Details []string `json:"details,omitempty"`
}

// ── MCP bridge config ───────────────────────────────────────────────────────

// MCPBridgeConfig is the rendered MCP server configuration for connecting
// ACP sessions to a local MCP loopback server. This is the Go equivalent
// of openclaw's MCPLoopbackServerConfig.
type MCPBridgeConfig struct {
	// ServerName is the name of the MCP bridge server.
	ServerName string `json:"server_name"`
	// URL is the HTTP endpoint of the MCP bridge.
	URL string `json:"url"`
	// Token is the auth token for the bridge (if any).
	Token string `json:"token,omitempty"`
	// Headers are additional HTTP headers for the bridge.
	Headers map[string]string `json:"headers,omitempty"`
}

// BuildMCPBridgeConfig constructs the config block that ACP agents use to
// connect to the local MCP loopback server.
func BuildMCPBridgeConfig(port int, token, serverName string) MCPBridgeConfig {
	if serverName == "" {
		serverName = "metiq-mcp-bridge"
	}
	cfg := MCPBridgeConfig{
		ServerName: serverName,
		URL:        fmt.Sprintf("http://127.0.0.1:%d/mcp", port),
		Headers:    make(map[string]string),
	}
	if token != "" {
		cfg.Token = token
		cfg.Headers["Authorization"] = "Bearer " + token
	}
	return cfg
}

// ── Health check functions ──────────────────────────────────────────────────

// HealthChecker is an optional interface implemented by BackendRuntime
// implementations that support health probing.
type HealthChecker interface {
	// IsHealthy reports whether the backend is operational.
	IsHealthy() bool
	// ProbeAvailability performs a connectivity or readiness check.
	ProbeAvailability(ctx context.Context) error
	// Doctor returns a detailed health report.
	Doctor(ctx context.Context) (DoctorReport, error)
}

// CheckBackend runs a health check against a backend entry.
// If the Runtime implements HealthChecker, it delegates to Doctor().
// Otherwise it returns a report indicating no health checker is available.
func CheckBackend(ctx context.Context, entry *BackendEntry) (DoctorReport, error) {
	if entry == nil {
		return DoctorReport{
			OK:      false,
			Code:    "nil_entry",
			Message: "backend entry is nil",
		}, nil
	}

	hc, ok := entry.Runtime.(HealthChecker)
	if !ok {
		return DoctorReport{
			OK:      true,
			Message: fmt.Sprintf("backend %q has no health checker", entry.ID),
		}, nil
	}

	return hc.Doctor(ctx)
}

// CheckRegistry checks all backends in a registry and returns one report per entry.
func CheckRegistry(ctx context.Context, reg *BackendRegistry) []DoctorReport {
	if reg == nil {
		return nil
	}
	entries := reg.List()
	reports := make([]DoctorReport, 0, len(entries))
	for i := range entries {
		report, err := CheckBackend(ctx, &entries[i])
		if err != nil {
			report = DoctorReport{
				OK:      false,
				Code:    "check_error",
				Message: fmt.Sprintf("backend %q: %v", entries[i].ID, err),
			}
		}
		reports = append(reports, report)
	}
	return reports
}
