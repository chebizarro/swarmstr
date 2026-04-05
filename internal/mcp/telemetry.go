package mcp

import "sort"

// TelemetrySummary is a lightweight health/telemetry rollup for the current
// MCP inventory.
type TelemetrySummary struct {
	Healthy                 bool `json:"healthy"`
	TotalServers            int  `json:"total_servers"`
	ConnectedServers        int  `json:"connected_servers,omitempty"`
	PendingServers          int  `json:"pending_servers,omitempty"`
	FailedServers           int  `json:"failed_servers,omitempty"`
	NeedsAuthServers        int  `json:"needs_auth_servers,omitempty"`
	DisabledServers         int  `json:"disabled_servers,omitempty"`
	BlockedServers          int  `json:"blocked_servers,omitempty"`
	ApprovalRequiredServers int  `json:"approval_required_servers,omitempty"`
	SuppressedServers       int  `json:"suppressed_servers,omitempty"`
	ToolCapableServers      int  `json:"tool_capable_servers,omitempty"`
	ResourceCapableServers  int  `json:"resource_capable_servers,omitempty"`
	PromptCapableServers    int  `json:"prompt_capable_servers,omitempty"`
}

// TelemetryServer is the operator-facing health/telemetry view of one MCP
// server, combining resolved config, policy outcome, and live runtime state.
type TelemetryServer struct {
	Name              string              `json:"name"`
	State             string              `json:"state"`
	Healthy           bool                `json:"healthy"`
	Enabled           bool                `json:"enabled,omitempty"`
	RuntimePresent    bool                `json:"runtime_present,omitempty"`
	PolicyStatus      PolicyStatus        `json:"policy_status,omitempty"`
	PolicyReason      PolicyReason        `json:"policy_reason,omitempty"`
	Source            ConfigSource        `json:"source,omitempty"`
	Precedence        int                 `json:"precedence,omitempty"`
	Signature         string              `json:"signature,omitempty"`
	Transport         string              `json:"transport,omitempty"`
	Command           string              `json:"command,omitempty"`
	URL               string              `json:"url,omitempty"`
	Capabilities      CapabilitySnapshot  `json:"capabilities,omitempty"`
	ServerInfo        *ServerInfoSnapshot `json:"server_info,omitempty"`
	Instructions      string              `json:"instructions,omitempty"`
	ToolCount         int                 `json:"tool_count,omitempty"`
	LastError         string              `json:"last_error,omitempty"`
	ReconnectAttempts int                 `json:"reconnect_attempts,omitempty"`
	LastAttemptAtMS   int64               `json:"last_attempt_at_ms,omitempty"`
	LastConnectedAtMS int64               `json:"last_connected_at_ms,omitempty"`
	LastFailedAtMS    int64               `json:"last_failed_at_ms,omitempty"`
	UpdatedAtMS       int64               `json:"updated_at_ms,omitempty"`
}

// TelemetrySnapshot is the combined MCP runtime/config telemetry surfaced to
// operators and runtime observers.
type TelemetrySnapshot struct {
	Enabled    bool               `json:"enabled"`
	Summary    TelemetrySummary   `json:"summary"`
	Servers    []TelemetryServer  `json:"servers,omitempty"`
	Suppressed []SuppressedServer `json:"suppressed,omitempty"`
}

// Empty reports whether the snapshot contains no MCP inventory or telemetry.
func (s TelemetrySnapshot) Empty() bool {
	return !s.Enabled && len(s.Servers) == 0 && len(s.Suppressed) == 0
}

// BuildTelemetrySnapshot merges resolved config inventory with the current
// manager snapshot into a stable, JSON-friendly telemetry view.
func BuildTelemetrySnapshot(resolved Config, runtime ManagerSnapshot) TelemetrySnapshot {
	out := TelemetrySnapshot{
		Enabled:    resolved.Enabled || runtime.Enabled,
		Suppressed: append([]SuppressedServer(nil), resolved.Suppressed...),
	}
	out.Summary.Healthy = true
	out.Summary.SuppressedServers = len(out.Suppressed)

	runtimeByName := make(map[string]ServerStateSnapshot, len(runtime.Servers))
	names := make([]string, 0, len(resolved.Servers)+len(resolved.DisabledServers)+len(resolved.FilteredServers)+len(runtime.Servers))
	seen := make(map[string]struct{}, len(names))
	addName := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, server := range runtime.Servers {
		runtimeByName[server.Name] = server
		addName(server.Name)
	}
	for name := range resolved.Servers {
		addName(name)
	}
	for name := range resolved.DisabledServers {
		addName(name)
	}
	for name := range resolved.FilteredServers {
		addName(name)
	}
	sort.Strings(names)

	out.Servers = make([]TelemetryServer, 0, len(names))
	for _, name := range names {
		resolvedServer, hasResolved := resolved.Servers[name]
		disabledServer, hasDisabled := resolved.DisabledServers[name]
		filteredServer, hasFiltered := resolved.FilteredServers[name]
		runtimeServer, hasRuntime := runtimeByName[name]

		server := TelemetryServer{Name: name}
		switch {
		case hasRuntime:
			server = telemetryServerFromRuntime(runtimeServer)
			if hasFiltered {
				server.PolicyStatus = filteredServer.PolicyStatus
				server.PolicyReason = filteredServer.PolicyReason
			}
		case hasFiltered:
			server = telemetryServerFromResolved(filteredServer.ResolvedServerConfig)
			server.State = string(filteredServer.PolicyStatus)
			server.PolicyStatus = filteredServer.PolicyStatus
			server.PolicyReason = filteredServer.PolicyReason
			server.Healthy = false
		case hasResolved:
			server = telemetryServerFromResolved(resolvedServer)
			server.State = string(ConnectionStatePending)
			server.Healthy = false
		case hasDisabled:
			server = telemetryServerFromResolved(disabledServer)
			server.State = string(ConnectionStateDisabled)
			server.Healthy = false
		default:
			continue
		}
		out.Servers = append(out.Servers, server)
		applyTelemetrySummary(&out.Summary, server)
	}
	if out.Summary.TotalServers == 0 && !out.Enabled {
		out.Summary.Healthy = true
	}
	return out
}

func telemetryServerFromResolved(server ResolvedServerConfig) TelemetryServer {
	return TelemetryServer{
		Name:       server.Name,
		Enabled:    server.Enabled,
		Source:     server.Source,
		Precedence: server.Precedence,
		Signature:  server.Signature,
		Transport:  transportTypeForSignature(server.ServerConfig),
		Command:    server.Command,
		URL:        server.URL,
	}
}

func telemetryServerFromRuntime(server ServerStateSnapshot) TelemetryServer {
	return TelemetryServer{
		Name:              server.Name,
		State:             string(server.State),
		Healthy:           server.State == ConnectionStateConnected,
		Enabled:           server.Enabled,
		RuntimePresent:    true,
		Source:            server.Source,
		Precedence:        server.Precedence,
		Signature:         server.Signature,
		Transport:         server.Transport,
		Command:           server.Command,
		URL:               server.URL,
		Capabilities:      server.Capabilities,
		ServerInfo:        cloneServerInfo(server.ServerInfo),
		Instructions:      server.Instructions,
		ToolCount:         server.ToolCount,
		LastError:         server.LastError,
		ReconnectAttempts: server.ReconnectAttempts,
		LastAttemptAtMS:   server.LastAttemptAtMS,
		LastConnectedAtMS: server.LastConnectedAtMS,
		LastFailedAtMS:    server.LastFailedAtMS,
		UpdatedAtMS:       server.UpdatedAtMS,
	}
}

func applyTelemetrySummary(summary *TelemetrySummary, server TelemetryServer) {
	if summary == nil {
		return
	}
	summary.TotalServers++
	if server.Capabilities.Tools {
		summary.ToolCapableServers++
	}
	if server.Capabilities.Resources {
		summary.ResourceCapableServers++
	}
	if server.Capabilities.Prompts {
		summary.PromptCapableServers++
	}
	switch server.State {
	case string(ConnectionStateConnected):
		summary.ConnectedServers++
	case string(ConnectionStatePending):
		summary.PendingServers++
		summary.Healthy = false
	case string(ConnectionStateFailed):
		summary.FailedServers++
		summary.Healthy = false
	case string(ConnectionStateNeedsAuth):
		summary.NeedsAuthServers++
		summary.Healthy = false
	case string(ConnectionStateDisabled):
		summary.DisabledServers++
	case string(PolicyStatusBlocked):
		summary.BlockedServers++
		summary.Healthy = false
	case string(PolicyStatusApprovalRequired):
		summary.ApprovalRequiredServers++
		summary.Healthy = false
	default:
		if server.Enabled && !server.Healthy {
			summary.Healthy = false
		}
	}
	if server.Enabled && !server.Healthy && server.State == "" {
		summary.Healthy = false
	}
}
